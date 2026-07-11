package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/kasmcfg"
	"github.com/xorhub/waas/operator/pkg/metakeys"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/operator/pkg/params"
	"github.com/xorhub/waas/operator/pkg/schedule"
)

// +kubebuilder:webhook:path=/validate-waas-xorhub-io-v1alpha1-workspacetemplate,mutating=false,failurePolicy=Fail,sideEffects=None,groups=waas.xorhub.io,resources=workspacetemplates,verbs=create;update,versions=v1alpha1,name=vworkspacetemplate.waas.xorhub.io,admissionReviewVersions=v1

// WorkspaceTemplateValidator gates template specs against the guacd
// parameter registry (operator/pkg/params): unknown parameters, malformed
// values and platform-owned parameters (credentials, gateways, repeaters)
// are rejected at admission — kubectl/GitOps goes through the exact same
// gate as the portal's template editor.
type WorkspaceTemplateValidator struct{}

var _ admission.Validator[*waasv1alpha1.WorkspaceTemplate] = &WorkspaceTemplateValidator{}

// SetupWorkspaceTemplateWebhookWithManager registers the validating webhook.
func SetupWorkspaceTemplateWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &waasv1alpha1.WorkspaceTemplate{}).
		WithValidator(&WorkspaceTemplateValidator{}).
		Complete()
}

// ValidateCreate implements admission.Validator.
func (v *WorkspaceTemplateValidator) ValidateCreate(_ context.Context, tpl *waasv1alpha1.WorkspaceTemplate) (admission.Warnings, error) {
	return v.validate(tpl)
}

// ValidateUpdate implements admission.Validator.
func (v *WorkspaceTemplateValidator) ValidateUpdate(_ context.Context, _, tpl *waasv1alpha1.WorkspaceTemplate) (admission.Warnings, error) {
	return v.validate(tpl)
}

// ValidateDelete implements admission.Validator.
func (v *WorkspaceTemplateValidator) ValidateDelete(context.Context, *waasv1alpha1.WorkspaceTemplate) (admission.Warnings, error) {
	return nil, nil
}

func (v *WorkspaceTemplateValidator) validate(tpl *waasv1alpha1.WorkspaceTemplate) (admission.Warnings, error) {
	seen := map[string]bool{}
	defaults := 0
	for i := range tpl.Spec.Protocols {
		entry := &tpl.Spec.Protocols[i]
		if seen[entry.Name] {
			return nil, v.deny(tpl, fmt.Sprintf("protocol %q is declared twice", entry.Name))
		}
		seen[entry.Name] = true
		if entry.Default {
			defaults++
		}
		if violation := params.ValidateTemplateParams(entry.Name, entry.Params); violation != nil {
			return nil, v.deny(tpl, fmt.Sprintf("protocols[%s].params: %v", entry.Name, violation))
		}
		if violation := params.ValidateUserParamNames(entry.Name, entry.UserParams); violation != nil {
			return nil, v.deny(tpl, fmt.Sprintf("protocols[%s].userParams: %v", entry.Name, violation))
		}
		// kasmvnc is the web endpoint of Linux kasmweb/* images; a
		// windows template is a KubeVirt VM reached over RDP.
		if entry.Name == string(waasv1alpha1.ProtocolKasmVNC) && tpl.Spec.OS == waasv1alpha1.OSWindows {
			return nil, v.deny(tpl, "protocol kasmvnc is not available on windows templates")
		}
		// The PulseAudio sidecar port only exists for guacd's VNC audio
		// path (enable-audio); accepting it elsewhere would silently open
		// a port nothing uses.
		if entry.ExposeAudioPort && entry.Name != string(waasv1alpha1.ProtocolVNC) {
			return nil, v.deny(tpl, fmt.Sprintf("protocols[%s].exposeAudioPort: only the vnc protocol can expose the PulseAudio port", entry.Name))
		}
	}
	if tpl.Spec.AudioPortExposed() {
		for i := range tpl.Spec.Protocols {
			if tpl.Spec.Protocols[i].Port == waasv1alpha1.PulseAudioPort {
				return nil, v.deny(tpl, fmt.Sprintf("protocols[%s].port: %d collides with the exposed PulseAudio port", tpl.Spec.Protocols[i].Name, waasv1alpha1.PulseAudioPort))
			}
		}
	}
	if defaults > 1 {
		return nil, v.deny(tpl, "at most one protocol may be marked default")
	}
	// kasmvnc is exclusive: it bypasses guacd, and its generated-password
	// mechanism and the vnc/rdp one both inject VNC_PW under the same
	// pod-copy Secret name — only one connection stack per template.
	if seen[string(waasv1alpha1.ProtocolKasmVNC)] && len(seen) > 1 {
		return nil, v.deny(tpl, "protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses guacd and must be the template's only protocol")
	}
	// kasmvncConfig only means something to a KasmVNC endpoint: an
	// honest refusal beats a silently ignored field.
	if tpl.Spec.KasmVNCConfig != "" && !seen[string(waasv1alpha1.ProtocolKasmVNC)] {
		return nil, v.deny(tpl, "kasmvncConfig requires a kasmvnc protocol entry")
	}
	// The operator derives the clipboard DLP directives from
	// WorkspacePolicy.Clipboard and stamps them over kasmvncConfig at
	// reconcile (see controller.applyClipboardPolicy). Directives an admin
	// writes here for those exact keys would be silently overwritten — same
	// "silently ignored field" trap, so refuse them and point at the policy.
	// Other clipboard sub-keys (size, allow_mimetypes, delay, primary) are
	// left to the admin and untouched.
	if tpl.Spec.KasmVNCConfig != "" {
		if bad, err := kasmcfg.PolicyManagedClipboardKeys(tpl.Spec.KasmVNCConfig); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("kasmvncConfig is %v", err))
		} else if len(bad) > 0 {
			return nil, v.deny(tpl, fmt.Sprintf(
				"kasmvncConfig must not set %s: clipboard enforcement is derived from WorkspacePolicy.Clipboard, not the template",
				strings.Join(bad, ", ")))
		}
	}
	if s := tpl.Spec.Schedule; s != nil {
		if err := (schedule.Spec{Timezone: s.Timezone, Uptime: s.Uptime, Downtime: s.Downtime}).Validate(); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("schedule: %v", err))
		}
	}
	if p := tpl.Spec.Placement; p != nil {
		if p.Namespace != "" {
			// Static validation: unknown placeholders (typos) are rejected
			// here, never resolved to an empty string; the literal part of
			// the pattern is the admin's to get right.
			if err := naming.ValidatePattern(p.Namespace); err != nil {
				return nil, v.deny(tpl, fmt.Sprintf("placement.namespace: %v", err))
			}
		}
		if err := metakeys.Check(p.NamespaceLabels); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("placement.namespaceLabels: %v", err))
		}
		if err := metakeys.Check(p.NamespaceAnnotations); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("placement.namespaceAnnotations: %v", err))
		}
	}
	if w := tpl.Spec.Workload; w != nil {
		if err := metakeys.Check(w.Labels); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("workload.labels: %v", err))
		}
		if err := metakeys.Check(w.Annotations); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("workload.annotations: %v", err))
		}
	}
	return nil, nil
}

func (v *WorkspaceTemplateValidator) deny(tpl *waasv1alpha1.WorkspaceTemplate, message string) error {
	return apierrors.NewForbidden(
		waasv1alpha1.GroupVersion.WithResource("workspacetemplates").GroupResource(), tpl.Name,
		fmt.Errorf("[InvalidProtocolParams] %s", message))
}
