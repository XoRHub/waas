package controller

// Volume retention tests: at deletion the home PVC is DETACHED by
// default (retained volume: still owned, still counted in the storage
// quota) and only deleted on the explicit opt-in annotation; a retained
// volume can be adopted back as the home of a new workspace.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func TestDeletionRetainsHomeVolumeByDefault(t *testing.T) {
	ws := workspace()
	ws.Spec.DisplayName = "Poste CAD"
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws) // provisions PVC + finalizer
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws) // finalizer path

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, &waasv1alpha1.Workspace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("workspace must be gone, got err=%v", err)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, pvc); err != nil {
		t.Fatalf("home volume must survive by default: %v", err)
	}
	if pvc.Labels[waasv1alpha1.LabelRetained] != "true" {
		t.Fatalf("retained volume must carry the retained label, got %v", pvc.Labels)
	}
	if pvc.Labels[waasv1alpha1.LabelOwner] != ws.Spec.Owner {
		t.Fatalf("retained volume must keep its owner, got %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "" {
		t.Fatalf("retained volume must not point at a dead workspace, got %v", pvc.Labels)
	}
	if pvc.Annotations[waasv1alpha1.AnnotationOriginWorkspace] != "Poste CAD" {
		t.Fatalf("provenance annotation missing, got %v", pvc.Annotations)
	}
	if pvc.Annotations[waasv1alpha1.AnnotationRetainedAt] == "" {
		t.Fatalf("retained-at annotation missing, got %v", pvc.Annotations)
	}
}

func TestDeletionDeletesHomeVolumeOnExplicitChoice(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	// The api-server stamps the opt-in annotation right before deleting.
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations == nil {
		got.Annotations = map[string]string{}
	}
	got.Annotations[waasv1alpha1.AnnotationDeleteHome] = "true"
	if err := c.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, got); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, &corev1.PersistentVolumeClaim{}); !apierrors.IsNotFound(err) {
		t.Fatalf("explicitly-deleted home volume must be gone, got err=%v", err)
	}
}

func TestAdoptedRetainedVolumeIsRelabeledLive(t *testing.T) {
	retained := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:      "old-home",
		Namespace: "default",
		Labels: map[string]string{
			labelManagedBy:             managerName,
			waasv1alpha1.LabelOwner:    "8f4e1f1a-0000-4000-8000-000000000001",
			waasv1alpha1.LabelRetained: "true",
		},
		Annotations: map[string]string{
			waasv1alpha1.AnnotationOriginWorkspace: "ancien poste",
			waasv1alpha1.AnnotationRetainedAt:      "2026-07-01T00:00:00Z",
		},
	}}
	ws := workspace()
	ws.Spec.HomeVolumeName = "old-home"
	r, c := newFixture(t, linuxTemplate(), ws, retained)
	ctx := context.Background()

	reconcile(t, r, ws)

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "old-home"}, pvc); err != nil {
		t.Fatal(err)
	}
	if pvc.Labels[waasv1alpha1.LabelRetained] != "" {
		t.Fatalf("adopted volume must shed the retained marker, got %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "marc" {
		t.Fatalf("adopted volume must belong to its new workspace, got %v", pvc.Labels)
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.PVCName != "old-home" {
		t.Fatalf("status must advertise the adopted volume, got %q", got.Status.PVCName)
	}
}
