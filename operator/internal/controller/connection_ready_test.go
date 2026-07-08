package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// ConnectionReady says the desktop server LISTENS — pod readiness only
// proves the container is up.
func TestConnectionReadyFollowsTheProbe(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	// Make the workload report ready so the phase reaches Running.
	reconcile(t, r, ws)
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Status.ReadyReplicas = 1
	if err := c.Status().Update(ctx, dep); err != nil {
		t.Fatal(err)
	}

	// Desktop not listening yet: condition False + bounded retry loop.
	r.Probe = func(string) error { return errors.New("connection refused") }
	res := reconcile(t, r, ws)
	if res.RequeueAfter == 0 || res.RequeueAfter > 10*time.Second {
		t.Fatalf("expected a short retry while the desktop is not listening, got %+v", res)
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if cond := findCondition(got, waasv1alpha1.ConditionConnectionReady); cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "DesktopNotListening" {
		t.Fatalf("expected ConnectionReady=False/DesktopNotListening, got %+v", cond)
	}

	// Desktop up: condition True, no more forced retry.
	var probedAddr string
	r.Probe = func(addr string) error { probedAddr = addr; return nil }
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	cond := findCondition(got, waasv1alpha1.ConditionConnectionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DesktopListening" {
		t.Fatalf("expected ConnectionReady=True, got %+v", cond)
	}
	if cond.ObservedGeneration != got.Generation {
		t.Fatalf("conditions must carry the evaluated generation, got %d (gen %d)", cond.ObservedGeneration, got.Generation)
	}
	if probedAddr != "ws-marc.default.svc.cluster.local:5901" {
		t.Fatalf("probe must target the service endpoint on the default protocol port, got %q", probedAddr)
	}
}

func findCondition(ws *waasv1alpha1.Workspace, condType string) *metav1.Condition {
	for i := range ws.Status.Conditions {
		if ws.Status.Conditions[i].Type == condType {
			return &ws.Status.Conditions[i]
		}
	}
	return nil
}
