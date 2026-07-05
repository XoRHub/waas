package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// Governance in the reconciler is the second line of defense behind the
// admission webhook. It exists for three reasons:
//   - TOCTOU: two creates racing through admission can each pass the
//     quota check; only one may get compute.
//   - Deferral: GitOps applies a Workspace before its template; admission
//     admitted it with a warning and the real decision happens here.
//   - Drift: an image disabled after admission must stop yielding compute
//     for NEW pods (existing pods are never torn down by policy — that is
//     the grandfathering rule).
//
// It re-checks ONLY when compute is about to be created, never against
// running workspaces.

// evaluateGovernance runs the same decision the webhook runs, from the
// identity persisted on the CR.
func (r *WorkspaceReconciler) evaluateGovernance(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (*waasv1alpha1.WorkspacePolicy, *policy.Denial) {
	catalog := &waasv1alpha1.WorkspaceImageList{}
	if err := r.List(ctx, catalog, client.InNamespace(ws.Namespace)); err != nil {
		return nil, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("listing catalog: %v", err)}
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(ws.Namespace)); err != nil {
		return nil, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("listing policies: %v", err)}
	}

	// No governance objects at all: the platform predates this feature or
	// runs without it — do not strand every workspace.
	if len(policies.Items) == 0 && len(catalog.Items) == 0 {
		return nil, nil
	}

	id := policy.IdentityOf(ws)
	pol, _, denial := policy.Resolve(policies.Items, id)
	if denial != nil {
		return nil, denial
	}

	img := policy.FindImage(catalog.Items, tpl.Spec.Image)
	if img == nil {
		return pol, &policy.Denial{
			Reason:  policy.ReasonImageNotInCatalog,
			Message: fmt.Sprintf("image %q is not in the catalog", tpl.Spec.Image),
		}
	}
	if d := policy.ImageAllowed(img, pol, id); d != nil {
		return pol, d
	}
	if d := policy.CheckProtocol(tpl, img); d != nil {
		return pol, d
	}

	load, known := policy.LoadOf(ws, tpl, img)
	others, err := r.siblingLoads(ctx, ws, catalog.Items)
	if err != nil {
		return pol, &policy.Denial{Reason: policy.ReasonInternalError, Message: fmt.Sprintf("computing usage: %v", err)}
	}
	if d := policy.CheckLimits(load, known, img, pol, others); d != nil {
		return pol, d
	}
	return pol, nil
}

// siblingLoads mirrors the webhook's usage computation, counting only
// siblings that already HAVE compute or a home volume claim — i.e. it
// measures granted capacity, so of two admission-raced workspaces the
// first to reconcile wins and the second is denied here.
func (r *WorkspaceReconciler) siblingLoads(ctx context.Context, ws *waasv1alpha1.Workspace, catalog []waasv1alpha1.WorkspaceImage) ([]policy.Load, error) {
	all := &waasv1alpha1.WorkspaceList{}
	if err := r.List(ctx, all, client.InNamespace(ws.Namespace)); err != nil {
		return nil, err
	}
	var loads []policy.Load
	for i := range all.Items {
		sib := &all.Items[i]
		if sib.Name == ws.Name || sib.Spec.Owner != ws.Spec.Owner || !sib.DeletionTimestamp.IsZero() {
			continue
		}
		tpl := &waasv1alpha1.WorkspaceTemplate{}
		err := r.Get(ctx, types.NamespacedName{Namespace: sib.Namespace, Name: sib.Spec.TemplateRef}, tpl)
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

// enforceLifetime applies lifecycle.maxLifetime: expired workspaces are
// deleted — home volume included, that is the announced contract of a
// TTL — and live ones get a requeue hint at their expiry instant.
// Workspaces with no resolvable policy or no maxLifetime never expire.
func (r *WorkspaceReconciler) enforceLifetime(ctx context.Context, ws *waasv1alpha1.Workspace) (deleted bool, requeueAfter time.Duration, err error) {
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := r.List(ctx, policies, client.InNamespace(ws.Namespace)); err != nil {
		return false, 0, fmt.Errorf("listing policies: %w", err)
	}
	pol, _, denial := policy.Resolve(policies.Items, policy.IdentityOf(ws))
	if denial != nil || pol.Spec.Lifecycle == nil || pol.Spec.Lifecycle.MaxLifetime == nil {
		return false, 0, nil
	}
	ttl := pol.Spec.Lifecycle.MaxLifetime.Duration
	if ttl <= 0 {
		return false, 0, nil
	}

	expiry := ws.CreationTimestamp.Add(ttl)
	if remaining := time.Until(expiry); remaining > 0 {
		return false, remaining, nil
	}

	r.recordEvent(ws, corev1.EventTypeWarning, "MaxLifetimeReached",
		fmt.Sprintf("workspace exceeded the %s max lifetime of policy %q and is being deleted (home volume included)", ttl, pol.Name))

	// The home PVC deliberately has no owner reference; a TTL delete is
	// the one case where user state goes too.
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Namespace: ws.Namespace, Name: computeName(ws) + "-home",
	}}
	if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
		return false, 0, fmt.Errorf("deleting expired home pvc: %w", err)
	}
	if err := r.Delete(ctx, ws); err != nil && !apierrors.IsNotFound(err) {
		return false, 0, fmt.Errorf("deleting expired workspace: %w", err)
	}
	return true, 0, nil
}

// recordEvent is nil-safe so tests without a recorder stay simple.
func (r *WorkspaceReconciler) recordEvent(ws *waasv1alpha1.Workspace, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(ws, eventType, reason, message)
	}
}

// archAffinity translates the catalog entry's architectures into a node
// affinity, so an amd64-only image never lands on the ARM64 nodes.
func archAffinity(archs []string) *corev1.Affinity {
	if len(archs) == 0 {
		return nil
	}
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      "kubernetes.io/arch",
						Operator: corev1.NodeSelectorOpIn,
						Values:   archs,
					}},
				}},
			},
		},
	}
}
