package controller

// Lifecycle-schedule tests: they prove the uptime/downtime crons actually
// scale the workload (spec.replicas 0/1), not just compute a phase. The
// reconciler clock is injected so every case runs at a chosen instant:
// plain windows, a missed tick (controller down when the edge fired),
// manual pause/resume against the schedule (conflict rule B), timezones.

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// scheduledTemplate is up 08:00→22:00 every day, Paris time.
func scheduledTemplate() *waasv1alpha1.WorkspaceTemplate {
	tpl := linuxTemplate()
	tpl.Spec.Schedule = &waasv1alpha1.WorkspaceSchedule{
		Timezone: "Europe/Paris",
		Uptime:   []string{"0 8 * * *"},
		Downtime: []string{"0 22 * * *"},
	}
	return tpl
}

func inZone(t *testing.T, tz, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("loading %s: %v", tz, err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatalf("parsing %q: %v", value, err)
	}
	return ts
}

func paris(t *testing.T, value string) time.Time { return inZone(t, "Europe/Paris", value) }

func reconcileAt(t *testing.T, r *WorkspaceReconciler, ws *waasv1alpha1.Workspace, at time.Time) ctrl.Result {
	t.Helper()
	r.Now = func() time.Time { return at }
	return reconcile(t, r, ws)
}

func replicasOf(t *testing.T, c client.Client) int32 {
	t.Helper()
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("fetching deployment: %v", err)
	}
	if dep.Spec.Replicas == nil {
		t.Fatalf("deployment has nil replicas")
	}
	return *dep.Spec.Replicas
}

func phaseOf(t *testing.T, c client.Client) waasv1alpha1.WorkspacePhase {
	t.Helper()
	ws := &waasv1alpha1.Workspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	return ws.Status.Phase
}

func setManualState(t *testing.T, c client.Client, paused bool, at time.Time) *waasv1alpha1.Workspace {
	t.Helper()
	ws := &waasv1alpha1.Workspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	ws.Spec.Paused = paused
	if ws.Annotations == nil {
		ws.Annotations = map[string]string{}
	}
	ws.Annotations[waasv1alpha1.AnnotationManualStateAt] = at.UTC().Format(time.RFC3339)
	if err := c.Update(context.Background(), ws); err != nil {
		t.Fatalf("updating workspace: %v", err)
	}
	return ws
}

// A downtime edge must scale the Deployment to 0 (phase Stopped) and an
// uptime edge back to 1 — the workload object survives either way.
func TestScheduleEdgesScaleWorkload(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, scheduledTemplate(), ws)

	// Monday 10:00, inside the uptime window.
	reconcileAt(t, r, ws, paris(t, "2026-07-06 10:00"))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("inside uptime window: expected 1 replica, got %d", got)
	}

	// 23:00, past the 22:00 downtime edge.
	res := reconcileAt(t, r, ws, paris(t, "2026-07-06 23:00"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("after downtime edge: expected 0 replicas, got %d", got)
	}
	if got := phaseOf(t, c); got != waasv1alpha1.PhaseStopped {
		t.Fatalf("scheduled downtime must report Stopped (not Paused), got %s", got)
	}
	// The controller must wake up exactly at the next uptime edge (08:00
	// next day = 9h later) to scale back up.
	if want := 9 * time.Hour; res.RequeueAfter != want {
		t.Fatalf("expected requeue at next up edge in %s, got %s", want, res.RequeueAfter)
	}

	// 08:30 next day: scaled back up.
	reconcileAt(t, r, ws, paris(t, "2026-07-07 08:30"))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("after uptime edge: expected 1 replica, got %d", got)
	}

	// NextTransition must point at the coming 22:00 downtime edge.
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	next := got.Status.NextTransition
	if next == nil || next.Up || !next.Time.Time.Equal(paris(t, "2026-07-07 22:00")) {
		t.Fatalf("expected next transition = down edge 2026-07-07 22:00 Paris, got %+v", next)
	}
}

// A tick the controller slept through (restart, downtime) must be applied
// at the NEXT reconcile whenever it happens: the scheduled state derives
// from the most recent edge, never from having observed the tick fire.
func TestScheduleMissedTickIsRecovered(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, scheduledTemplate(), ws)

	reconcileAt(t, r, ws, paris(t, "2026-07-06 10:00"))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("expected 1 replica, got %d", got)
	}

	// No reconcile happened at 22:00 (controller was down). First wake-up
	// at 22:47 must still apply the downtime edge.
	reconcileAt(t, r, ws, paris(t, "2026-07-06 22:47"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("missed downtime tick must be recovered, got %d replicas", got)
	}
}

// Conflict rule B, pause side: a manual pause during an uptime window
// wins (scale to 0, phase Paused) until the next uptime edge, where the
// schedule regains control and brings the workspace back up even though
// spec.paused is still true.
func TestManualPauseWinsUntilNextUpEdge(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, scheduledTemplate(), ws)

	reconcileAt(t, r, ws, paris(t, "2026-07-06 10:00"))
	ws = setManualState(t, c, true, paris(t, "2026-07-06 10:30"))

	reconcileAt(t, r, ws, paris(t, "2026-07-06 11:00"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("manual pause during uptime must scale to 0, got %d", got)
	}
	if got := phaseOf(t, c); got != waasv1alpha1.PhasePaused {
		t.Fatalf("manual pause must report Paused (not Stopped), got %s", got)
	}

	// Next morning, past the 08:00 uptime edge: the schedule wins again.
	reconcileAt(t, r, ws, paris(t, "2026-07-07 08:30"))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("schedule must regain control at the up edge, got %d replicas", got)
	}
}

// Conflict rule B, resume side: a manual resume during a downtime window
// brings the workspace up until the next downtime edge.
func TestManualResumeWinsUntilNextDownEdge(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, scheduledTemplate(), ws)

	reconcileAt(t, r, ws, paris(t, "2026-07-06 23:00"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("expected 0 replicas in downtime window, got %d", got)
	}

	ws = setManualState(t, c, false, paris(t, "2026-07-06 23:10"))
	reconcileAt(t, r, ws, paris(t, "2026-07-06 23:30"))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("manual resume during downtime must scale to 1, got %d", got)
	}

	// Past the next 22:00 downtime edge, the schedule wins again.
	reconcileAt(t, r, ws, paris(t, "2026-07-07 22:05"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("schedule must regain control at the down edge, got %d replicas", got)
	}
}

// The schedule is evaluated in ITS timezone, wherever the controller runs
// (the reconciler clock here is plain UTC).
func TestScheduleHonorsTimezone(t *testing.T) {
	tpl := scheduledTemplate()
	tpl.Spec.Schedule.Timezone = "America/New_York"
	tpl.Spec.Schedule.Downtime = []string{"0 18 * * *"}
	ws := workspace()
	r, c := newFixture(t, tpl, ws)

	// 15:00 UTC = 11:00 New York (EDT): inside the uptime window.
	reconcileAt(t, r, ws, time.Date(2026, 7, 6, 15, 0, 0, 0, time.UTC))
	if got := replicasOf(t, c); got != 1 {
		t.Fatalf("11:00 New York must be up, got %d replicas", got)
	}

	// 23:30 UTC = 19:30 New York: past the 18:00 downtime edge.
	reconcileAt(t, r, ws, time.Date(2026, 7, 6, 23, 30, 0, 0, time.UTC))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("19:30 New York must be down, got %d replicas", got)
	}
}

// A workspace override replaces the template schedule entirely.
func TestScheduleOverrideReplacesTemplate(t *testing.T) {
	ws := workspace()
	ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
		Schedule: &waasv1alpha1.WorkspaceSchedule{
			Timezone: "Europe/Paris",
			Uptime:   []string{"0 6 * * *"},
			Downtime: []string{"0 20 * * *"},
		},
	}
	r, c := newFixture(t, scheduledTemplate(), ws)

	// 21:00 Paris: template says up (until 22:00), override says down
	// (since 20:00) — the override must win.
	reconcileAt(t, r, ws, paris(t, "2026-07-06 21:00"))
	if got := replicasOf(t, c); got != 0 {
		t.Fatalf("override downtime must win over template uptime, got %d replicas", got)
	}
}
