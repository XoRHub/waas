package v1alpha1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
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
	}
	if defaults > 1 {
		return nil, v.deny(tpl, "at most one protocol may be marked default")
	}
	if s := tpl.Spec.Schedule; s != nil {
		if err := (schedule.Spec{Timezone: s.Timezone, Uptime: s.Uptime, Downtime: s.Downtime}).Validate(); err != nil {
			return nil, v.deny(tpl, fmt.Sprintf("schedule: %v", err))
		}
	}
	if p := tpl.Spec.Placement; p != nil {
		if p.Namespace != "" {
			// The tokens expand to sanitized values by construction; what
			// needs vetting is the literal part of the pattern.
			if _, err := naming.ResolveNamespace(p.Namespace, "sample-user", "sample-workspace"); err != nil {
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
