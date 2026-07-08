package controller

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// Private-registry pull credentials.
//
// The catalog entry approving an image (or a whole registry) may name a
// kubernetes.io/dockerconfigjson Secret next to the CRs
// (WorkspaceImage.spec.imagePullSecretRef). imagePullSecrets are read by
// the kubelet in the POD's namespace, and pods land in dynamic per-user
// namespaces — so the operator copies the source Secret there under a
// stable name shared by every workspace of that namespace.
//
// Lifecycle: the copy is create-or-update (rotations propagate on the
// next reconcile), labeled LabelPullSecret so the namespace janitor does
// NOT count it as workspace content (it would otherwise pin
// DeleteWhenEmpty namespaces forever); it is reclaimed by the namespace
// cascade. A missing source is FAIL-CLOSED: the workspace goes
// PhaseFailed with reason PullSecretMissing instead of crash-looping in
// ImagePullBackOff with no explanation.

// ReasonPullSecretMissing marks the fail-closed denial when the catalog
// references a pull secret that cannot be resolved.
const ReasonPullSecretMissing = policy.Reason("PullSecretMissing")

// pullSecretPodName is the name the PodSpec references: the source name
// itself when the workspace is unplaced (pod next to the source), the
// copy's stable name otherwise.
func pullSecretPodName(ws *waasv1alpha1.Workspace, ref string) string {
	if computeNamespace(ws) == ws.Namespace {
		return ref
	}
	return "waas-pull-" + ref
}

// imagePullSecretRef resolves the template's catalog entry and returns
// its pull-secret reference ("" when none applies).
func (r *WorkspaceReconciler) imagePullSecretRef(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) string {
	catalog := &waasv1alpha1.WorkspaceImageList{}
	if err := r.List(ctx, catalog, client.InNamespace(ws.Namespace)); err != nil {
		return ""
	}
	if img := policy.FindImage(catalog.Items, tpl.Spec.Image); img != nil {
		return img.Spec.ImagePullSecretRef
	}
	return ""
}

// ensurePullSecret materializes the pull credentials in the workspace's
// target namespace. A denial (not an error) is returned when the source
// is missing — same handling as a governance denial: Event + PhaseFailed
// condition + slow requeue, since the secret may arrive via GitOps.
func (r *WorkspaceReconciler) ensurePullSecret(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (*policy.Denial, error) {
	ref := r.imagePullSecretRef(ctx, ws, tpl)
	if ref == "" {
		return nil, nil
	}

	source := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ws.Namespace, Name: ref}, source); err != nil {
		if apierrors.IsNotFound(err) {
			return &policy.Denial{
				Reason: ReasonPullSecretMissing,
				Message: fmt.Sprintf("catalog entry for image %q references pull secret %q, which does not exist in %s",
					tpl.Spec.Image, ref, ws.Namespace),
			}, nil
		}
		return nil, fmt.Errorf("fetching pull secret %s: %w", ref, err)
	}

	// Unplaced workspace: the pod resolves the source directly.
	if computeNamespace(ws) == ws.Namespace {
		return nil, nil
	}

	// Deliberately NO LabelWorkspace: the copy is shared by every
	// workspace of the namespace, not owned by one.
	key := client.ObjectKey{Namespace: computeNamespace(ws), Name: pullSecretPodName(ws, ref)}
	copyLabels := map[string]string{
		labelManagedBy:               managerName,
		waasv1alpha1.LabelPullSecret: "true",
	}

	dest := &corev1.Secret{}
	err := r.Get(ctx, key, dest)
	switch {
	case apierrors.IsNotFound(err):
		dest = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: copyLabels},
			Type:       source.Type,
			Data:       source.Data,
		}
		if err := r.Create(ctx, dest); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("creating pull secret copy %s: %w", key.Name, err)
		}
	case err != nil:
		return nil, fmt.Errorf("fetching pull secret copy %s: %w", key.Name, err)
	case dest.Type != source.Type:
		// Secret type is immutable: recreate.
		if err := r.Delete(ctx, dest); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("recreating pull secret copy %s: %w", key.Name, err)
		}
		fresh := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: copyLabels},
			Type:       source.Type,
			Data:       source.Data,
		}
		if err := r.Create(ctx, fresh); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("recreating pull secret copy %s: %w", key.Name, err)
		}
	case !secretDataEqual(dest.Data, source.Data):
		// Rotation on the source: converge the copy.
		dest.Data = source.Data
		dest.StringData = nil
		if err := r.Update(ctx, dest); err != nil {
			return nil, fmt.Errorf("syncing pull secret copy %s: %w", key.Name, err)
		}
	}
	return nil, nil
}

func secretDataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !bytes.Equal(b[k], v) {
			return false
		}
	}
	return true
}
