package v1alpha1

// Volume adoption enforcement: spec.homeVolumeName may only point at a
// RETAINED waas volume of the SAME owner, in the target namespace; the
// field is immutable and retained volumes weigh on the storage quota.

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func retainedPVC(name, ns, owner, size string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				waasv1alpha1.LabelManagedBy: waasv1alpha1.ManagerName,
				waasv1alpha1.LabelRetained:  "true",
				waasv1alpha1.LabelOwner:     owner,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
	}
}

func TestVolumeAdoptionOfOwnRetainedVolumeAllowed(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy(),
		retainedPVC("old-home", "default", ownerUUID, "10Gi"))
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.HomeVolumeName = "old-home"
	})
	if _, err := v.ValidateCreate(asCaller(apiSA), ws); err != nil {
		t.Fatalf("expected admit, got %v", err)
	}
}

func TestVolumeAdoptionOfForeignVolumeDenied(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy(),
		retainedPVC("bob-home", "default", "uuid-bob", "10Gi"))
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.HomeVolumeName = "bob-home"
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "VolumeDenied") {
		t.Fatalf("expected VolumeDenied on another user's volume, got %v", err)
	}
}

func TestVolumeAdoptionOfLiveVolumeDenied(t *testing.T) {
	// A live workspace's home (no retained label) must never be adoptable,
	// even by its own owner: two pods on one RWO volume.
	live := retainedPVC("live-home", "default", ownerUUID, "10Gi")
	delete(live.Labels, waasv1alpha1.LabelRetained)
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy(), live)
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.HomeVolumeName = "live-home"
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "VolumeDenied") {
		t.Fatalf("expected VolumeDenied on a live volume, got %v", err)
	}
}

func TestVolumeAdoptionOfMissingVolumeDenied(t *testing.T) {
	v := newValidator(t, tpl(), catalogImage(), defaultPolicy())
	ws := workspace("w1", func(w *waasv1alpha1.Workspace) {
		w.Spec.HomeVolumeName = "does-not-exist"
	})
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "VolumeDenied") {
		t.Fatalf("expected VolumeDenied on a missing volume, got %v", err)
	}
}

func TestRetainedVolumesWeighOnStorageQuota(t *testing.T) {
	// Aggregate storage cap 15Gi; a 10Gi retained volume + a template
	// wanting 10Gi (tpl homeSize) must exceed it — retention keeps its
	// quota share, that is the whole point.
	cap15 := resource.MustParse("15Gi")
	pol := defaultPolicy()
	pol.Spec.Limits.Aggregate = &waasv1alpha1.AggregateCaps{Storage: &cap15}
	template := tpl()
	size := resource.MustParse("10Gi")
	template.Spec.HomeSize = &size
	v := newValidator(t, template, catalogImage(), pol,
		retainedPVC("kept", "default", ownerUUID, "10Gi"))
	ws := workspace("w1")
	_, err := v.ValidateCreate(asCaller(apiSA), ws)
	if err == nil || !strings.Contains(err.Error(), "QuotaExceeded") {
		t.Fatalf("expected QuotaExceeded with a retained volume in the aggregate, got %v", err)
	}
}
