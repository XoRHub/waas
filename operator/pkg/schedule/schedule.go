// Package schedule evaluates a workspace's uptime/downtime cron schedule.
// It is shared by the operator (which enforces the resulting up/down
// state) and the api-server (which displays the next transition), so both
// agree by construction.
//
// A schedule is two sets of standard 5-field cron expressions in an
// explicit IANA timezone: uptime crons fire "start" edges, downtime crons
// fire "stop" edges. The workspace's scheduled state at any instant is
// decided by the most recent edge.
package schedule

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// Spec is a template's (or workspace's) schedule. Empty = no schedule.
type Spec struct {
	Timezone string
	Uptime   []string
	Downtime []string
}

// Edge is one scheduled transition.
type Edge struct {
	Time time.Time
	Up   bool // true = uptime/start edge, false = downtime/stop edge
}

// Decision is the resolved lifecycle intent at a given instant.
type Decision struct {
	// Down: should the workspace be scaled to 0?
	Down bool
	// Manual: is the current state driven by a still-winning manual
	// action (vs the schedule)? Drives Paused (manual) vs Stopped
	// (scheduled) in the operator.
	Manual bool
	// NextEdge is the next scheduled transition, if any.
	NextEdge *Edge
}

func (s Spec) IsZero() bool { return len(s.Uptime) == 0 && len(s.Downtime) == 0 }

// standardParser accepts the classic 5 fields (minute hour dom month dow).
var standardParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// scanHorizon bounds the backward scan for the last edge; 8 days covers
// weekly schedules. Next() jumps by real fire times, so sane schedules
// iterate only a handful of steps.
const scanHorizon = 8 * 24 * time.Hour

// Validate checks the timezone and every cron expression. A schedule with
// crons but no timezone is rejected (the controller must never fall back
// to its own TZ).
func (s Spec) Validate() error {
	if s.IsZero() {
		return nil
	}
	if _, err := s.location(); err != nil {
		return err
	}
	for _, expr := range append(append([]string{}, s.Uptime...), s.Downtime...) {
		if _, err := standardParser.Parse(expr); err != nil {
			return fmt.Errorf("invalid cron %q: %w", expr, err)
		}
	}
	return nil
}

func (s Spec) location() (*time.Location, error) {
	if s.Timezone == "" {
		return nil, fmt.Errorf("schedule.timezone is required when uptime/downtime crons are set")
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", s.Timezone, err)
	}
	return loc, nil
}

func parseAll(exprs []string) []cron.Schedule {
	out := make([]cron.Schedule, 0, len(exprs))
	for _, e := range exprs {
		if sc, err := standardParser.Parse(e); err == nil {
			out = append(out, sc)
		}
	}
	return out
}

// nextOf returns the earliest activation strictly after t across scheds.
func nextOf(scheds []cron.Schedule, t time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for _, sc := range scheds {
		n := sc.Next(t)
		if n.IsZero() {
			continue
		}
		if !found || n.Before(best) {
			best, found = n, true
		}
	}
	return best, found
}

// lastOf returns the latest activation at-or-before t across scheds,
// within scanHorizon (robfig cron exposes no Prev, so we scan forward).
func lastOf(scheds []cron.Schedule, t time.Time) (time.Time, bool) {
	var last time.Time
	found := false
	for _, sc := range scheds {
		cur := t.Add(-scanHorizon)
		for i := 0; i < 200000; i++ {
			n := sc.Next(cur)
			if n.IsZero() || n.After(t) {
				break
			}
			last, found, cur = n, true, n
		}
	}
	return last, found
}

// ScheduledDown reports whether the schedule wants the workspace DOWN at
// t: the most recent edge decides. ok=false when the schedule is empty or
// no edge falls within the scan window (no opinion → not down).
func (s Spec) ScheduledDown(t time.Time) (down bool, ok bool) {
	if s.IsZero() {
		return false, false
	}
	loc, err := s.location()
	if err != nil {
		return false, false
	}
	tt := t.In(loc)
	lastUp, okUp := lastOf(parseAll(s.Uptime), tt)
	lastDn, okDn := lastOf(parseAll(s.Downtime), tt)
	switch {
	case !okUp && !okDn:
		return false, false
	case okUp && !okDn:
		return false, true
	case !okUp && okDn:
		return true, true
	default:
		return lastDn.After(lastUp), true
	}
}

// NextEdge returns the next transition strictly after t.
func (s Spec) NextEdge(t time.Time) (Edge, bool) {
	if s.IsZero() {
		return Edge{}, false
	}
	loc, err := s.location()
	if err != nil {
		return Edge{}, false
	}
	tt := t.In(loc)
	nUp, okUp := nextOf(parseAll(s.Uptime), tt)
	nDn, okDn := nextOf(parseAll(s.Downtime), tt)
	switch {
	case !okUp && !okDn:
		return Edge{}, false
	case okUp && (!okDn || !nDn.Before(nUp)):
		return Edge{Time: nUp, Up: true}, true
	default:
		return Edge{Time: nDn, Up: false}, true
	}
}

// nextEdgeOfKind returns the next edge after t whose Up matches wantUp.
func (s Spec) nextEdgeOfKind(t time.Time, wantUp bool) (time.Time, bool) {
	loc, err := s.location()
	if err != nil {
		return time.Time{}, false
	}
	exprs := s.Downtime
	if wantUp {
		exprs = s.Uptime
	}
	return nextOf(parseAll(exprs), t.In(loc))
}

// Resolve applies conflict rule B: a manual action wins until the next
// scheduled edge OPPOSITE to it, after which the schedule regains control.
//
//	manualPaused: the user's current manual pause flag (spec.paused).
//	manualAt:     when the user last toggled it (nil = never; follow the schedule).
//
// The operator never mutates spec.paused: a stale manualPaused simply
// stops winning once its opposite edge passes.
func (s Spec) Resolve(now time.Time, manualPaused bool, manualAt *time.Time) Decision {
	var nextP *Edge
	if next, ok := s.NextEdge(now); ok {
		nextP = &next
	}

	schedDown, hasSched := s.ScheduledDown(now)
	if !hasSched {
		return Decision{Down: manualPaused, Manual: manualPaused, NextEdge: nextP}
	}
	if manualAt != nil {
		// Opposite edge kind: a manual pause (down) waits for an up edge,
		// a manual resume (up) waits for a down edge — wantUp == manualPaused.
		if opp, ok := s.nextEdgeOfKind(*manualAt, manualPaused); ok && now.Before(opp) {
			return Decision{Down: manualPaused, Manual: true, NextEdge: nextP}
		}
	}
	return Decision{Down: schedDown, Manual: false, NextEdge: nextP}
}
