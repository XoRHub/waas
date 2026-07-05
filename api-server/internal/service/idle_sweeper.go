package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/policy"

	"github.com/xorhub/waas/api-server/internal/repository"
)

// IdleSweeper enforces lifecycle.idleSuspendAfter. The split with the
// operator is by data ownership: only the api-server knows about desktop
// sessions (its database), so IT pauses idle workspaces; the operator —
// which owns the CR lifecycle — handles maxLifetime deletion.
//
// Pausing is deliberately gentle enforcement: compute is freed, the home
// volume stays, and the user resumes with one click (subject to policy
// re-validation at that moment).
type IdleSweeper struct {
	kube      client.Client
	namespace string
	sessions  repository.SessionRepository
	audit     *AuditService
	interval  time.Duration
}

// NewIdleSweeper builds the sweeper; interval <= 0 disables it.
func NewIdleSweeper(kube client.Client, namespace string, sessions repository.SessionRepository, audit *AuditService, interval time.Duration) *IdleSweeper {
	return &IdleSweeper{kube: kube, namespace: namespace, sessions: sessions, audit: audit, interval: interval}
}

// Run blocks until ctx is done, sweeping on every tick.
func (s *IdleSweeper) Run(ctx context.Context) {
	if s.interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	slog.Info("idle sweeper started", "interval", s.interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sweep(ctx); err != nil {
				slog.Error("idle sweep failed", "error", err)
			}
		}
	}
}

func (s *IdleSweeper) sweep(ctx context.Context) error {
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return fmt.Errorf("listing policies: %w", err)
	}
	if len(policies.Items) == 0 {
		return nil
	}
	workspaces := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, workspaces, client.InNamespace(s.namespace)); err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	activity, err := s.sessions.Activity(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	for i := range workspaces.Items {
		ws := &workspaces.Items[i]
		if ws.Spec.Paused || !ws.DeletionTimestamp.IsZero() {
			continue
		}
		pol, _, denial := policy.Resolve(policies.Items, policy.IdentityOf(ws))
		if denial != nil || pol.Spec.Lifecycle == nil || pol.Spec.Lifecycle.IdleSuspendAfter == nil {
			continue
		}
		idleAfter := pol.Spec.Lifecycle.IdleSuspendAfter.Duration
		if idleAfter <= 0 {
			continue
		}

		// Idle clock: last session activity, or creation for a
		// workspace nobody ever connected to. An open session always
		// counts as active.
		act, seen := activity[string(ws.UID)]
		if seen && act.ActiveNow {
			continue
		}
		last := ws.CreationTimestamp.Time
		if seen && act.LastActivity.After(last) {
			last = act.LastActivity
		}
		if now.Sub(last) < idleAfter {
			continue
		}

		ws.Spec.Paused = true
		if err := s.kube.Update(ctx, ws); err != nil {
			slog.Error("pausing idle workspace failed", "workspace", ws.Name, "error", err)
			continue
		}
		slog.Info("paused idle workspace",
			"workspace", ws.Name, "owner", ws.Spec.Owner, "idle_since", last, "policy", pol.Name)
		s.audit.Record(ctx, Actor{Username: "idle-sweeper"}, "workspace.auto_paused", "workspace",
			string(ws.UID), fmt.Sprintf("name=%s policy=%s idleSince=%s", ws.Name, pol.Name, last.Format(time.RFC3339)))
	}
	return nil
}
