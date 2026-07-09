package controller

// Metric increments ride the code paths they mirror: a janitor reclaim
// bumps the reclaim counter, a failing teardown pass bumps the failure
// counter. Both use deltas — the counters are process-global.

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/metrics"
)

func TestNamespaceJanitorReclaimIncrementsCounter(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "waas-user-bob",
		Labels: map[string]string{
			labelManagedBy:            managerName,
			waasv1alpha1.LabelCleanup: string(waasv1alpha1.CleanupDeleteWhenEmpty),
		},
	}}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(ns).Build()
	j := &NamespaceJanitor{Client: c}

	before := testutil.ToFloat64(metrics.NamespaceReclaims)
	if _, err := j.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: ns.Name},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	err := c.Get(context.Background(), types.NamespacedName{Name: ns.Name}, &corev1.Namespace{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("namespace should be deleted, got err=%v", err)
	}
	if got := testutil.ToFloat64(metrics.NamespaceReclaims); got != before+1 {
		t.Fatalf("NamespaceReclaims = %v, want %v", got, before+1)
	}
}

func TestReportTeardownFailureIncrementsCounter(t *testing.T) {
	ws := workspace()
	r, _ := newFixture(t, ws)

	before := testutil.ToFloat64(metrics.TeardownFailures)
	r.reportTeardownFailure(context.Background(), ws, "tearing down placed objects", errors.New("boom"))
	if got := testutil.ToFloat64(metrics.TeardownFailures); got != before+1 {
		t.Fatalf("TeardownFailures = %v, want %v", got, before+1)
	}
}
