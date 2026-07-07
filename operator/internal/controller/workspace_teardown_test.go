package controller

// Teardown visibility: a failing finalizer must never be silent — the CR
// gets a Warning Event and a TeardownFailed condition, the finalizer
// stays (removing it would trade a visible stuck deletion for a silent
// leak), and the reconcile error keeps the backoff retries going.

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func TestTeardownFailureIsVisible(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Placement = &waasv1alpha1.WorkspacePlacement{Namespace: "waas-{user}"}
	ws := placedWorkspace()
	ctx := context.Background()

	boom := errors.New("api server unavailable")
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(tpl, ws).
		WithStatusSubresource(&waasv1alpha1.Workspace{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				// The teardown deletes through unstructured objects.
				if obj.GetObjectKind().GroupVersionKind().Kind == "Service" {
					return boom
				}
				return cl.Delete(ctx, obj, opts...)
			},
		}).
		Build()
	rec := record.NewFakeRecorder(8)
	r := &WorkspaceReconciler{Client: c, Recorder: rec}

	reconcile(t, r, ws)
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "marc"}})
	if err == nil {
		t.Fatal("a failing teardown must return an error (backoff retry)")
	}

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("CR must still exist (finalizer kept): %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		found = found || f == finalizerTeardown
	}
	if !found {
		t.Fatalf("finalizer must never be removed on failure, got %v", got.Finalizers)
	}
	var cond *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == waasv1alpha1.ConditionReady {
			cond = &got.Status.Conditions[i]
		}
	}
	if cond == nil || cond.Reason != "TeardownFailed" || !strings.Contains(cond.Message, "api server unavailable") {
		t.Fatalf("expected a TeardownFailed condition carrying the cause, got %+v", cond)
	}
	// Drain the recorder (provisioning emitted events too) and look for
	// the failure warning.
	seen := false
	for {
		select {
		case ev := <-rec.Events:
			seen = seen || strings.Contains(ev, "TeardownFailed")
			continue
		default:
		}
		break
	}
	if !seen {
		t.Fatal("expected a TeardownFailed warning event on the CR")
	}
}
