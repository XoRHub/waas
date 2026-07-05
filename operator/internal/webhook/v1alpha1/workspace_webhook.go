// Package v1alpha1 contains the admission webhooks for the waas API group.
package v1alpha1

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// +kubebuilder:webhook:path=/validate-waas-xorhub-io-v1alpha1-workspace,mutating=false,failurePolicy=Fail,sideEffects=None,groups=waas.xorhub.io,resources=workspaces,verbs=create;update,versions=v1alpha1,name=vworkspace.waas.xorhub.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaceimages,verbs=get;list;watch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspacepolicies,verbs=get;list;watch

// WorkspaceValidator is the enforcement point for workspace governance.
// FailurePolicy=Fail: if this webhook is down nobody creates workspaces —
// governance is fail-closed by construction, and kubectl goes through the
// exact same gate as the portal.
//
// Identity model (trusted-writer, validated design decision): the
// api-server authenticates users against Authentik and is the only caller
// whose spec.owner and identity annotations are believed. Any other
// caller gets its identity from the Kubernetes request userInfo:
// spec.owner must equal the authenticated username and the identity
// annotations must be absent. Either way identity is frozen at creation —
// owner and identity annotations are immutable for every caller.
type WorkspaceValidator struct {
	Client            client.Client
	KubeVirtAvailable bool

	// TrustedWriters are exact usernames (in practice: the api-server's
	// ServiceAccount) allowed to set owner and identity annotations.
	TrustedWriters []string

	// BypassSubjects are usernames or groups exempt from policy checks —
	// the GitOps applier and break-glass admins. They still cannot touch
	// identity immutability, and the KubeVirt capability check still
	// applies (cluster fact, not policy).
	BypassSubjects []string
}

var _ admission.Validator[*waasv1alpha1.Workspace] = &WorkspaceValidator{}

// SetupWorkspaceWebhookWithManager registers the validating webhook.
func SetupWorkspaceWebhookWithManager(mgr ctrl.Manager, kubeVirtAvailable bool, trustedWriters, bypassSubjects []string) error {
	return ctrl.NewWebhookManagedBy(mgr, &waasv1alpha1.Workspace{}).
		WithValidator(&WorkspaceValidator{
			Client:            mgr.GetClient(),
			KubeVirtAvailable: kubeVirtAvailable,
			TrustedWriters:    trustedWriters,
			BypassSubjects:    bypassSubjects,
		}).
		Complete()
}

// ValidateCreate implements admission.Validator.
func (v *WorkspaceValidator) ValidateCreate(ctx context.Context, ws *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return v.validate(ctx, nil, ws)
}

// ValidateUpdate implements admission.Validator.
func (v *WorkspaceValidator) ValidateUpdate(ctx context.Context, oldWS, newWS *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return v.validate(ctx, oldWS, newWS)
}

// ValidateDelete implements admission.Validator.
func (v *WorkspaceValidator) ValidateDelete(context.Context, *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return nil, nil
}

func (v *WorkspaceValidator) validate(ctx context.Context, oldWS, ws *waasv1alpha1.Workspace) (admission.Warnings, error) {
	log := logf.FromContext(ctx).WithName("workspace-webhook").WithValues("workspace", ws.Name)
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("no admission request in context: %w", err)
	}
	caller := req.UserInfo.Username

	// Identity integrity first: it binds everything else and applies to
	// every caller — trusted, bypassed or not.
	if oldWS != nil {
		if ws.Spec.Owner != oldWS.Spec.Owner {
			return nil, v.deny(ws, policy.ReasonIdentityViolation,
				fmt.Sprintf("spec.owner is immutable (was %q); a workspace cannot change hands", oldWS.Spec.Owner))
		}
		for _, ann := range []string{waasv1alpha1.AnnotationUsername, waasv1alpha1.AnnotationGroups} {
			if ws.Annotations[ann] != oldWS.Annotations[ann] {
				return nil, v.deny(ws, policy.ReasonIdentityViolation,
					fmt.Sprintf("identity annotation %s is immutable", ann))
			}
		}
	}

	// Grandfathering (validated design): pre-governance workspaces keep
	// running; compliance is demanded at the next spec change. Metadata
	// and status churn must never re-trigger policy evaluation, or GitOps
	// sync and controller updates would break existing workspaces.
	if oldWS != nil && reflect.DeepEqual(oldWS.Spec, ws.Spec) {
		return nil, nil
	}

	tpl := &waasv1alpha1.WorkspaceTemplate{}
	err = v.Client.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: ws.Spec.TemplateRef}, tpl)
	if apierrors.IsNotFound(err) {
		// GitOps may apply the workspace before its template. Admit with
		// a warning: the reconciler re-checks policy before creating any
		// compute, so this is a deferral, not a hole.
		return admission.Warnings{fmt.Sprintf(
			"workspace template %q does not exist yet; the workspace stays Pending and policy will be enforced when it appears",
			ws.Spec.TemplateRef)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching template %s: %w", ws.Spec.TemplateRef, err)
	}

	// Cluster capability, not policy: applies to everyone, bypass included.
	if tpl.Spec.OS == waasv1alpha1.OSWindows && !v.KubeVirtAvailable {
		return nil, v.deny(ws, policy.ReasonProtocolMismatch,
			fmt.Sprintf("template %q requires a windows VM, but KubeVirt is not installed in this cluster", tpl.Name))
	}

	if v.bypassed(req.UserInfo.Username, req.UserInfo.Groups) {
		log.Info("policy enforcement bypassed", "caller", caller)
		return admission.Warnings{fmt.Sprintf("workspace policy enforcement bypassed for %q", caller)}, nil
	}

	id, denial := v.resolveIdentity(req, ws)
	if denial == nil {
		var warnings admission.Warnings
		warnings, denial = v.enforce(ctx, ws, tpl, id)
		if denial == nil {
			return warnings, nil
		}
	}
	log.Info("workspace denied", "caller", caller, "owner", ws.Spec.Owner,
		"reason", denial.Reason, "message", denial.Message)
	return nil, v.deny(ws, denial.Reason, denial.Message)
}

// resolveIdentity produces the trusted identity of the workspace owner.
func (v *WorkspaceValidator) resolveIdentity(req admission.Request, ws *waasv1alpha1.Workspace) (policy.Identity, *policy.Denial) {
	if slices.Contains(v.TrustedWriters, req.UserInfo.Username) {
		return policy.Identity{
			Owner:    ws.Spec.Owner,
			Username: ws.Annotations[waasv1alpha1.AnnotationUsername],
			Groups:   splitGroups(ws.Annotations[waasv1alpha1.AnnotationGroups]),
		}, nil
	}

	// Untrusted caller: identity comes from the Kubernetes request, and
	// the object must not carry self-granted identity claims.
	for _, ann := range []string{waasv1alpha1.AnnotationUsername, waasv1alpha1.AnnotationGroups} {
		if _, found := ws.Annotations[ann]; found {
			return policy.Identity{}, &policy.Denial{
				Reason: policy.ReasonIdentityViolation,
				Message: fmt.Sprintf("annotation %s may only be set by the platform api-server (caller %q is not a trusted writer)",
					ann, req.UserInfo.Username),
			}
		}
	}
	if ws.Spec.Owner != req.UserInfo.Username {
		return policy.Identity{}, &policy.Denial{
			Reason: policy.ReasonIdentityViolation,
			Message: fmt.Sprintf("spec.owner %q does not match your authenticated identity %q; direct API access requires owner == username",
				ws.Spec.Owner, req.UserInfo.Username),
		}
	}
	return policy.Identity{
		Owner:    req.UserInfo.Username,
		Username: req.UserInfo.Username,
		Groups:   req.UserInfo.Groups,
	}, nil
}

// enforce runs the full governance decision for one workspace.
func (v *WorkspaceValidator) enforce(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, id policy.Identity) (admission.Warnings, *policy.Denial) {
	catalog := &waasv1alpha1.WorkspaceImageList{}
	if err := v.Client.List(ctx, catalog, client.InNamespace(ws.Namespace)); err != nil {
		return nil, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("listing workspace images: %v", err)}
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := v.Client.List(ctx, policies, client.InNamespace(ws.Namespace)); err != nil {
		return nil, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("listing workspace policies: %v", err)}
	}

	pol, warns, denial := policy.Resolve(policies.Items, id)
	if denial != nil {
		return admission.Warnings(warns), denial
	}
	warnings := admission.Warnings(warns)

	img := policy.FindImage(catalog.Items, tpl.Spec.Image)
	if img == nil {
		return warnings, &policy.Denial{
			Reason: policy.ReasonImageNotInCatalog,
			Message: fmt.Sprintf("image %q (template %q) is not in the catalog; an admin must approve it as a WorkspaceImage first",
				tpl.Spec.Image, tpl.Name),
		}
	}
	if d := policy.ImageAllowed(img, pol, id); d != nil {
		return warnings, d
	}
	if d := policy.CheckProtocol(tpl, img); d != nil {
		return warnings, d
	}

	load, known := policy.LoadOf(ws, tpl, img)
	others, err := v.otherLoads(ctx, ws, id.Owner, catalog.Items)
	if err != nil {
		return warnings, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("computing current usage: %v", err)}
	}
	if d := policy.CheckLimits(load, known, img, pol, others); d != nil {
		return warnings, d
	}
	return warnings, nil
}

// otherLoads sums the footprint of the owner's OTHER workspaces. A
// sibling whose template vanished still counts for count and storage
// (its home PVC exists) with zero compute.
func (v *WorkspaceValidator) otherLoads(ctx context.Context, ws *waasv1alpha1.Workspace, owner string, catalog []waasv1alpha1.WorkspaceImage) ([]policy.Load, error) {
	all := &waasv1alpha1.WorkspaceList{}
	if err := v.Client.List(ctx, all, client.InNamespace(ws.Namespace)); err != nil {
		return nil, err
	}
	var loads []policy.Load
	for i := range all.Items {
		sib := &all.Items[i]
		if sib.Name == ws.Name || sib.Spec.Owner != owner || !sib.DeletionTimestamp.IsZero() {
			continue
		}
		tpl := &waasv1alpha1.WorkspaceTemplate{}
		err := v.Client.Get(ctx, types.NamespacedName{Namespace: sib.Namespace, Name: sib.Spec.TemplateRef}, tpl)
		if apierrors.IsNotFound(err) {
			loads = append(loads, policy.Load{Storage: resource.MustParse(policy.DefaultHomeSize), Paused: sib.Spec.Paused})
			continue
		}
		if err != nil {
			return nil, err
		}
		load, _ := policy.LoadOf(sib, tpl, policy.FindImage(catalog, tpl.Spec.Image))
		loads = append(loads, load)
	}
	return loads, nil
}

func (v *WorkspaceValidator) bypassed(username string, groups []string) bool {
	if slices.Contains(v.BypassSubjects, username) {
		return true
	}
	for _, g := range groups {
		if slices.Contains(v.BypassSubjects, g) {
			return true
		}
	}
	return false
}

// deny wraps a policy denial into a Forbidden error whose message —
// "[Reason] human explanation with numbers" — reaches kubectl verbatim
// and is parsed by the portal for display.
func (v *WorkspaceValidator) deny(ws *waasv1alpha1.Workspace, reason policy.Reason, message string) error {
	return apierrors.NewForbidden(
		waasv1alpha1.GroupVersion.WithResource("workspaces").GroupResource(), ws.Name,
		fmt.Errorf("[%s] %s", reason, message))
}

func splitGroups(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
