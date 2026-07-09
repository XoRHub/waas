package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func workspaceIn(name string, phase waasv1alpha1.WorkspacePhase, drifted bool) *waasv1alpha1.Workspace {
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     waasv1alpha1.WorkspaceStatus{Phase: phase},
	}
	status := metav1.ConditionFalse
	if drifted {
		status = metav1.ConditionTrue
	}
	ws.Status.Conditions = []metav1.Condition{{
		Type: waasv1alpha1.ConditionTemplateDrifted, Status: status,
		Reason: "test", LastTransitionTime: metav1.Now(),
	}}
	return ws
}

func TestWorkspaceCollector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := waasv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding waas scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		workspaceIn("a", waasv1alpha1.PhaseRunning, true),
		workspaceIn("b", waasv1alpha1.PhaseRunning, false),
		workspaceIn("c", waasv1alpha1.PhasePaused, false),
	).Build()

	expected := `
# HELP waas_operator_workspaces Workspaces by status.phase.
# TYPE waas_operator_workspaces gauge
waas_operator_workspaces{phase="Failed"} 0
waas_operator_workspaces{phase="Paused"} 1
waas_operator_workspaces{phase="Pending"} 0
waas_operator_workspaces{phase="Provisioning"} 0
waas_operator_workspaces{phase="Running"} 2
waas_operator_workspaces{phase="Stopped"} 0
waas_operator_workspaces{phase="Terminating"} 0
# HELP waas_operator_workspaces_drifted Workspaces whose TemplateDrifted condition is True (docs/adr/0001).
# TYPE waas_operator_workspaces_drifted gauge
waas_operator_workspaces_drifted 1
`
	if err := testutil.CollectAndCompare(NewWorkspaceCollector(c), strings.NewReader(expected)); err != nil {
		t.Fatalf("unexpected collector output: %v", err)
	}
}

// failingReader forces the list-error path: the scrape must surface the
// failure instead of reporting an empty fleet.
type failingReader struct{ client.Reader }

func (failingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("boom")
}

func TestWorkspaceCollectorListError(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewWorkspaceCollector(failingReader{}))
	if _, err := reg.Gather(); err == nil {
		t.Fatal("expected the gather to report the list error")
	}
}

func TestCountersAreRegistered(t *testing.T) {
	before := testutil.ToFloat64(NamespaceReclaims)
	NamespaceReclaims.Inc()
	if got := testutil.ToFloat64(NamespaceReclaims); got != before+1 {
		t.Fatalf("NamespaceReclaims = %v, want %v", got, before+1)
	}
	before = testutil.ToFloat64(TeardownFailures)
	TeardownFailures.Inc()
	if got := testutil.ToFloat64(TeardownFailures); got != before+1 {
		t.Fatalf("TeardownFailures = %v, want %v", got, before+1)
	}
}
