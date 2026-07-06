package schedule

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing time %q: %v", s, err)
	}
	return ts
}

// Business-hours schedule: up at 08:00, down at 20:00, Europe/Paris.
func businessHours() Spec {
	return Spec{
		Timezone: "Europe/Paris",
		Uptime:   []string{"0 8 * * *"},
		Downtime: []string{"0 20 * * *"},
	}
}

func TestValidate(t *testing.T) {
	if err := (Spec{}).Validate(); err != nil {
		t.Fatalf("empty schedule must be valid: %v", err)
	}
	if err := businessHours().Validate(); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	if err := (Spec{Uptime: []string{"0 8 * * *"}}).Validate(); err == nil {
		t.Fatal("crons without timezone must be rejected")
	}
	if err := (Spec{Timezone: "Europe/Paris", Uptime: []string{"nope"}}).Validate(); err == nil {
		t.Fatal("invalid cron must be rejected")
	}
	if err := (Spec{Timezone: "Mars/Olympus", Uptime: []string{"0 8 * * *"}}).Validate(); err == nil {
		t.Fatal("invalid timezone must be rejected")
	}
}

func TestScheduledDown(t *testing.T) {
	s := businessHours()
	// 10:00 Paris -> within uptime window -> up.
	down, ok := s.ScheduledDown(mustTime(t, "2026-07-06T10:00:00+02:00"))
	if !ok || down {
		t.Fatalf("10:00 must be scheduled up, got down=%v ok=%v", down, ok)
	}
	// 22:00 Paris -> after downtime edge -> down.
	down, ok = s.ScheduledDown(mustTime(t, "2026-07-06T22:00:00+02:00"))
	if !ok || !down {
		t.Fatalf("22:00 must be scheduled down, got down=%v ok=%v", down, ok)
	}
	// 06:00 Paris -> after last night's downtime, before morning uptime -> down.
	down, ok = s.ScheduledDown(mustTime(t, "2026-07-06T06:00:00+02:00"))
	if !ok || !down {
		t.Fatalf("06:00 must be scheduled down, got down=%v ok=%v", down, ok)
	}
}

func TestNextEdge(t *testing.T) {
	s := businessHours()
	// From 10:00 the next edge is the 20:00 downtime.
	e, ok := s.NextEdge(mustTime(t, "2026-07-06T10:00:00+02:00"))
	if !ok || e.Up || e.Time.In(time.UTC).Hour() != 18 { // 20:00 Paris == 18:00 UTC
		t.Fatalf("next edge from 10:00 must be the 20:00 downtime, got %+v", e)
	}
	// From 22:00 the next edge is tomorrow's 08:00 uptime.
	e, ok = s.NextEdge(mustTime(t, "2026-07-06T22:00:00+02:00"))
	if !ok || !e.Up {
		t.Fatalf("next edge from 22:00 must be an uptime edge, got %+v", e)
	}
}

// Rule B: a manual action wins until the next OPPOSITE scheduled edge.
func TestResolveConflictRuleB(t *testing.T) {
	s := businessHours()

	// Manual resume at 22:00 (during scheduled downtime): stays up until
	// the next downtime edge (tomorrow 20:00), not just the next tick.
	manualAt := mustTime(t, "2026-07-06T22:00:00+02:00")
	d := s.Resolve(mustTime(t, "2026-07-06T23:00:00+02:00"), false, &manualAt)
	if d.Down || !d.Manual {
		t.Fatalf("manual resume must keep it up (manual), got %+v", d)
	}
	// Next morning 09:00 (a scheduled uptime edge passed, but that AGREES
	// with the manual up — the opposite downtime edge has not passed yet).
	d = s.Resolve(mustTime(t, "2026-07-07T09:00:00+02:00"), false, &manualAt)
	if d.Down {
		t.Fatalf("still up the morning after a manual resume, got %+v", d)
	}
	// After tomorrow's 20:00 downtime edge, the schedule regains control.
	d = s.Resolve(mustTime(t, "2026-07-07T21:00:00+02:00"), false, &manualAt)
	if !d.Down || d.Manual {
		t.Fatalf("schedule must reclaim control after the opposite edge, got %+v", d)
	}

	// Manual pause at 10:00 (during uptime): stays down until the next
	// uptime edge (tomorrow 08:00).
	pauseAt := mustTime(t, "2026-07-06T10:00:00+02:00")
	d = s.Resolve(mustTime(t, "2026-07-06T12:00:00+02:00"), true, &pauseAt)
	if !d.Down || !d.Manual {
		t.Fatalf("manual pause must hold it down (manual), got %+v", d)
	}
	// After tomorrow's 08:00 uptime edge, schedule reclaims -> up.
	d = s.Resolve(mustTime(t, "2026-07-07T09:00:00+02:00"), true, &pauseAt)
	if d.Down {
		t.Fatalf("schedule must reclaim and start it after the up edge, got %+v", d)
	}
}

func TestResolveNoSchedule(t *testing.T) {
	// No schedule: purely manual.
	var s Spec
	d := s.Resolve(time.Now(), true, nil)
	if !d.Down || !d.Manual || d.NextEdge != nil {
		t.Fatalf("no schedule must be manual-only, got %+v", d)
	}
	d = s.Resolve(time.Now(), false, nil)
	if d.Down {
		t.Fatalf("no schedule, not paused -> up, got %+v", d)
	}
}
