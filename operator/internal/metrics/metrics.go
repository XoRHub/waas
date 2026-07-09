// Package metrics holds the operator's business metrics. Everything
// registers on the controller-runtime registry, so it is served by the
// manager's existing metrics endpoint (--metrics-bind-address) next to
// the standard controller-runtime/client-go metrics.
package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

var (
	// NamespaceReclaims counts namespaces deleted by the janitor (cleanup
	// policy DeleteWhenEmpty, provably empty).
	NamespaceReclaims = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "waas_operator_namespace_reclaims_total",
		Help: "Namespaces reclaimed by the janitor (DeleteWhenEmpty policy, empty).",
	})

	// TeardownFailures counts failed finalizer passes (mirror of the
	// TeardownFailed Event — see docs/workspace-deletion.md). A non-flat
	// curve means workspaces are stuck Terminating and retrying.
	TeardownFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "waas_operator_teardown_failures_total",
		Help: "Workspace teardown finalizer failures (each retry counts).",
	})
)

func init() {
	ctrlmetrics.Registry.MustRegister(NamespaceReclaims, TeardownFailures)
}

var (
	workspacesDesc = prometheus.NewDesc(
		"waas_operator_workspaces",
		"Workspaces by status.phase.",
		[]string{"phase"}, nil,
	)
	workspacesDriftedDesc = prometheus.NewDesc(
		"waas_operator_workspaces_drifted",
		"Workspaces whose TemplateDrifted condition is True (docs/adr/0001).",
		nil, nil,
	)
)

// knownPhases zero-fills the phase gauge so dashboards see every series
// from the first scrape (absent != zero in PromQL).
var knownPhases = []waasv1alpha1.WorkspacePhase{
	waasv1alpha1.PhasePending,
	waasv1alpha1.PhaseProvisioning,
	waasv1alpha1.PhaseRunning,
	waasv1alpha1.PhasePaused,
	waasv1alpha1.PhaseStopped,
	waasv1alpha1.PhaseFailed,
	waasv1alpha1.PhaseTerminating,
}

// WorkspaceCollector derives fleet-level gauges from the Workspace CRs at
// scrape time, straight off the manager's cache — no bookkeeping to drift
// away from the actual cluster state, no reconcile-order dependence.
type WorkspaceCollector struct {
	reader client.Reader
}

// NewWorkspaceCollector builds a collector over the given (cached) reader.
func NewWorkspaceCollector(reader client.Reader) *WorkspaceCollector {
	return &WorkspaceCollector{reader: reader}
}

// Describe implements prometheus.Collector.
func (c *WorkspaceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- workspacesDesc
	ch <- workspacesDriftedDesc
}

// Collect implements prometheus.Collector. A failing list emits an
// invalid metric so the scrape surfaces the error instead of reporting a
// silently empty fleet.
func (c *WorkspaceCollector) Collect(ch chan<- prometheus.Metric) {
	list := &waasv1alpha1.WorkspaceList{}
	if err := c.reader.List(context.Background(), list); err != nil {
		ch <- prometheus.NewInvalidMetric(workspacesDesc, err)
		return
	}
	byPhase := map[waasv1alpha1.WorkspacePhase]int{}
	drifted := 0
	for i := range list.Items {
		ws := &list.Items[i]
		byPhase[ws.Status.Phase]++
		for j := range ws.Status.Conditions {
			cond := &ws.Status.Conditions[j]
			if cond.Type == waasv1alpha1.ConditionTemplateDrifted && cond.Status == metav1.ConditionTrue {
				drifted++
				break
			}
		}
	}
	for _, phase := range knownPhases {
		ch <- prometheus.MustNewConstMetric(workspacesDesc, prometheus.GaugeValue,
			float64(byPhase[phase]), string(phase))
		delete(byPhase, phase)
	}
	// A phase outside the known set (new API version) still shows up.
	for phase, n := range byPhase {
		if phase == "" {
			// Freshly created CRs have no phase yet; not a fleet state
			// worth a series of its own.
			continue
		}
		ch <- prometheus.MustNewConstMetric(workspacesDesc, prometheus.GaugeValue,
			float64(n), string(phase))
	}
	ch <- prometheus.MustNewConstMetric(workspacesDriftedDesc, prometheus.GaugeValue, float64(drifted))
}
