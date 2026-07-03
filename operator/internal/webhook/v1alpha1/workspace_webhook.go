// Package v1alpha1 contains the admission webhooks for the waas API group.
package v1alpha1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-waas-xorhub-io-v1alpha1-workspace,mutating=false,failurePolicy=Fail,sideEffects=None,groups=waas.xorhub.io,resources=workspaces,verbs=create;update,versions=v1alpha1,name=vworkspace.waas.xorhub.io,admissionReviewVersions=v1

// WorkspaceValidator rejects Workspace specs the cluster cannot honor —
// most importantly windows workspaces on clusters without KubeVirt, which
// must fail at admission time, never be silently ignored.
type WorkspaceValidator struct {
	Client            client.Client
	KubeVirtAvailable bool
}

var _ admission.Validator[*waasv1alpha1.Workspace] = &WorkspaceValidator{}

// SetupWorkspaceWebhookWithManager registers the validating webhook.
func SetupWorkspaceWebhookWithManager(mgr ctrl.Manager, kubeVirtAvailable bool) error {
	return ctrl.NewWebhookManagedBy(mgr, &waasv1alpha1.Workspace{}).
		WithValidator(&WorkspaceValidator{Client: mgr.GetClient(), KubeVirtAvailable: kubeVirtAvailable}).
		Complete()
}

// ValidateCreate implements admission.Validator.
func (v *WorkspaceValidator) ValidateCreate(ctx context.Context, ws *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return v.validate(ctx, ws)
}

// ValidateUpdate implements admission.Validator.
func (v *WorkspaceValidator) ValidateUpdate(ctx context.Context, _, newWS *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return v.validate(ctx, newWS)
}

// ValidateDelete implements admission.Validator.
func (v *WorkspaceValidator) ValidateDelete(context.Context, *waasv1alpha1.Workspace) (admission.Warnings, error) {
	return nil, nil
}

func (v *WorkspaceValidator) validate(ctx context.Context, ws *waasv1alpha1.Workspace) (admission.Warnings, error) {
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	err := v.Client.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: ws.Spec.TemplateRef}, tpl)
	if apierrors.IsNotFound(err) {
		// GitOps tools may apply the workspace before its template; admit it
		// with a warning and let the reconciler wait for the template.
		return admission.Warnings{fmt.Sprintf(
			"workspace template %q does not exist yet; the workspace will stay Pending until it is created",
			ws.Spec.TemplateRef)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching template %s: %w", ws.Spec.TemplateRef, err)
	}

	if tpl.Spec.OS == waasv1alpha1.OSWindows && !v.KubeVirtAvailable {
		return nil, apierrors.NewForbidden(
			waasv1alpha1.GroupVersion.WithResource("workspaces").GroupResource(),
			ws.Name,
			fmt.Errorf("template %q requires a windows VM, but KubeVirt is not installed in this cluster", tpl.Name),
		)
	}
	return nil, nil
}
