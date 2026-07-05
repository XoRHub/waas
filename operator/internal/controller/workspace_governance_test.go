package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func catalogEntry(enabled bool, archs ...string) *waasv1alpha1.WorkspaceImage {
	return &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName:   "XFCE",
			Image:         "ghcr.io/xorhub/waas/desktop-xfce:latest", // matches linuxTemplate()
			Protocols:     []waasv1alpha1.Protocol{waasv1alpha1.ProtocolVNC},
			Enabled:       enabled,
			Architectures: archs,
		},
	}
}

func openPolicy(lifecycle *waasv1alpha1.PolicyLifecycle) *waasv1alpha1.WorkspacePolicy {
	return &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"},
		Spec:       waasv1alpha1.WorkspacePolicySpec{Lifecycle: lifecycle},
	}
}

func TestReconcileDeniesDisabledImageBeforeCreatingCompute(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws, catalogEntry(false), openPolicy(nil))
	ctx := context.Background()

	res := reconcile(t, r, ws)
	if res.RequeueAfter != requeueMissing {
		t.Fatalf("denied workspace should requeue on the slow loop, got %+v", res)
	}

	// No compute may exist.
	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); !apierrors.IsNotFound(err) {
		t.Fatalf("no pod must be created for a denied workspace, got err=%v", err)
	}

	// The denial must be explicit in status for the IHM.
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseFailed {
		t.Fatalf("expected Failed phase, got %s", got.Status.Phase)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "ImageDisabled" {
		t.Fatalf("expected ImageDisabled condition, got %+v", got.Status.Conditions)
	}
}

func TestReconcileGrandfathersRunningComputeWhenImageDisabled(t *testing.T) {
	// The pod already exists: disabling the image must NOT tear it down.
	ws := workspace()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ws-marc", Namespace: "default"}}
	r, c := newFixture(t, linuxTemplate(), ws, pod, catalogEntry(false), openPolicy(nil))

	reconcile(t, r, ws)

	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, &corev1.Pod{}); err != nil {
		t.Fatalf("running compute must survive image disabling: %v", err)
	}
}

func TestReconcileAppliesArchAffinity(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws, catalogEntry(true, "amd64"), openPolicy(nil))

	reconcile(t, r, ws)

	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatal(err)
	}
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || terms[0].MatchExpressions[0].Key != "kubernetes.io/arch" || terms[0].MatchExpressions[0].Values[0] != "amd64" {
		t.Fatalf("expected amd64 node affinity, got %+v", terms)
	}
}

func TestReconcileDeletesExpiredWorkspaceAndHome(t *testing.T) {
	ws := workspace()
	ws.CreationTimestamp = metav1.NewTime(time.Now().Add(-48 * time.Hour))
	ttl := &waasv1alpha1.PolicyLifecycle{MaxLifetime: &metav1.Duration{Duration: 24 * time.Hour}}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "ws-marc-home", Namespace: "default"}}
	r, c := newFixture(t, linuxTemplate(), ws, pvc, catalogEntry(true), openPolicy(ttl))
	ctx := context.Background()

	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, &waasv1alpha1.Workspace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expired workspace must be deleted, got err=%v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, &corev1.PersistentVolumeClaim{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expired home PVC must be deleted, got err=%v", err)
	}
}

func TestReconcileRequeuesAtTTLExpiry(t *testing.T) {
	ws := workspace()
	ws.CreationTimestamp = metav1.NewTime(time.Now())
	ttl := &waasv1alpha1.PolicyLifecycle{MaxLifetime: &metav1.Duration{Duration: 24 * time.Hour}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-marc", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	r, _ := newFixture(t, linuxTemplate(), ws, pod, catalogEntry(true), openPolicy(ttl))

	res := reconcile(t, r, ws)
	if res.RequeueAfter < 23*time.Hour || res.RequeueAfter > 24*time.Hour {
		t.Fatalf("expected requeue near TTL expiry, got %v", res.RequeueAfter)
	}
}
