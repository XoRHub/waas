package envtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// The teardown finalizer against a real apiserver, with the reconciler
// running: real deletionTimestamp semantics (the object is retained
// until the finalizer clears), real optimistic-concurrency conflicts.
// GC caveat: envtest has no kube-controller-manager, so ownerReference
// cascades never run here — this test asserts the ownerRefs are SET;
// the cascade itself is the kind smoke test's job.
func TestFinalizerLifecycle(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "finalizer")
	seedGovernance(t, ns)
	ctx := context.Background()

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "lifecycle", Namespace: ns},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "alice"},
	}
	if err := aliceCli.Create(ctx, ws); err != nil {
		t.Fatalf("creating workspace: %v", err)
	}
	key := types.NamespacedName{Namespace: ns, Name: ws.Name}

	// The reconciler must stamp the teardown finalizer BEFORE any
	// compute exists — it is the deletion-time decision point for the
	// home volume.
	waitFor(t, 15*time.Second, "teardown finalizer", func() error {
		got := &waasv1alpha1.Workspace{}
		if err := adminCli.Get(ctx, key, got); err != nil {
			return err
		}
		for _, f := range got.Finalizers {
			if f == "waas.xorhub.io/teardown" {
				return nil
			}
		}
		return fmt.Errorf("finalizers = %v", got.Finalizers)
	})

	// Compute and home volume materialize; same-namespace children carry
	// an ownerReference to the workspace (the GC contract).
	var pvcName string
	waitFor(t, 15*time.Second, "deployment and home PVC", func() error {
		deps := &appsv1.DeploymentList{}
		if err := adminCli.List(ctx, deps, client.InNamespace(ns),
			client.MatchingLabels{waasv1alpha1.LabelWorkspace: ws.Name}); err != nil {
			return err
		}
		if len(deps.Items) != 1 {
			return fmt.Errorf("deployments: %d", len(deps.Items))
		}
		if !ownedBy(deps.Items[0].OwnerReferences, ws.Name) {
			return fmt.Errorf("deployment has no ownerReference to the workspace: %+v", deps.Items[0].OwnerReferences)
		}
		pvcs := &corev1.PersistentVolumeClaimList{}
		if err := adminCli.List(ctx, pvcs, client.InNamespace(ns),
			client.MatchingLabels{waasv1alpha1.LabelWorkspace: ws.Name}); err != nil {
			return err
		}
		if len(pvcs.Items) != 1 {
			return fmt.Errorf("home pvcs: %d", len(pvcs.Items))
		}
		pvcName = pvcs.Items[0].Name
		return nil
	})

	if err := aliceCli.Delete(ctx, ws); err != nil {
		t.Fatalf("deleting workspace: %v", err)
	}

	// Real deletion semantics: the CR survives as deletionTimestamp-set
	// until the reconciler finishes teardown and clears the finalizer,
	// then vanishes.
	waitFor(t, 15*time.Second, "workspace to be finalized away", func() error {
		got := &waasv1alpha1.Workspace{}
		err := adminCli.Get(ctx, key, got)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("still present (deletionTimestamp=%v, finalizers=%v)", got.DeletionTimestamp, got.Finalizers)
	})

	// Data-safety default: without the explicit delete-home annotation
	// the home volume is DETACHED, not deleted — retained label on, the
	// per-workspace label gone (the CR it pointed at no longer exists).
	pvc := &corev1.PersistentVolumeClaim{}
	if err := adminCli.Get(ctx, types.NamespacedName{Namespace: ns, Name: pvcName}, pvc); err != nil {
		t.Fatalf("home PVC must survive workspace deletion: %v", err)
	}
	if pvc.Labels[waasv1alpha1.LabelRetained] != "true" {
		t.Fatalf("detached home PVC must carry the retained label, got %v", pvc.Labels)
	}
	if _, still := pvc.Labels[waasv1alpha1.LabelWorkspace]; still {
		t.Fatal("detached home PVC must drop the workspace label (provenance moves to annotations)")
	}
}

func ownedBy(refs []metav1.OwnerReference, name string) bool {
	for _, r := range refs {
		if r.Kind == "Workspace" && r.Name == name {
			return true
		}
	}
	return false
}
