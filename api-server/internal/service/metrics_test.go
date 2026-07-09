package service

// The business gauges/counters ride existing code paths: subscribing an
// event stream, sweeping sessions, recording an audit entry. Deltas only —
// the metrics are process-global and other tests exercise the same paths.

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/metrics"
	"github.com/xorhub/waas/api-server/internal/model"
)

func TestEventHubTracksSSEClientsGauge(t *testing.T) {
	hub := NewEventHub()
	before := testutil.ToFloat64(metrics.SSEClients)

	_, cancelA := hub.Subscribe("u1", false)
	_, cancelB := hub.Subscribe("u2", true)
	if got := testutil.ToFloat64(metrics.SSEClients); got != before+2 {
		t.Fatalf("SSEClients after 2 subscriptions = %v, want %v", got, before+2)
	}
	cancelA()
	cancelA() // double-cancel must not decrement twice
	cancelB()
	if got := testutil.ToFloat64(metrics.SSEClients); got != before {
		t.Fatalf("SSEClients after cancel = %v, want %v", got, before)
	}
}

func TestSessionSweeperSetsActiveSessionsGauge(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)
	ctx := context.Background()

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "alive", Namespace: testNS, UID: types.UID("uid-alive")},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
	}
	if err := f.kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}
	openSession(t, f, "s1", "uid-alive", model.SessionKindWorkspace)
	openSession(t, f, "s2", "uid-alive", model.SessionKindWorkspace)

	sweeper := NewSessionSweeper(f.kube, testNS, f.sessions(), f.remotes, f.audit(), time.Minute)
	if err := sweeper.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := testutil.ToFloat64(metrics.ActiveSessions); got != 2 {
		t.Fatalf("ActiveSessions = %v, want 2", got)
	}

	// Both sessions closed: the next sweep must drop the gauge to zero,
	// including through the len(open)==0 early return.
	now := time.Now().UTC()
	for _, id := range []string{"s1", "s2"} {
		if err := f.sessions().End(ctx, id, now); err != nil {
			t.Fatalf("ending session %s: %v", id, err)
		}
	}
	if err := sweeper.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := testutil.ToFloat64(metrics.ActiveSessions); got != 0 {
		t.Fatalf("ActiveSessions after close = %v, want 0", got)
	}
}

func TestAuditRecordCountsByAction(t *testing.T) {
	f := newRemoteFixture(t, []model.User{{ID: "u1", Username: "alice"}}, nil)

	counter := metrics.AuditEvents.WithLabelValues("workspace.denied")
	before := testutil.ToFloat64(counter)
	f.audit().Record(context.Background(), Actor{ID: "u1", Username: "alice"},
		"workspace.denied", "workspace", "ws-1", "quota exceeded")
	if got := testutil.ToFloat64(counter); got != before+1 {
		t.Fatalf("AuditEvents{action=workspace.denied} = %v, want %v", got, before+1)
	}
}
