package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/metrics"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
)

// SessionSweeper closes session rows whose target no longer exists: a
// workspace CR deleted via kubectl or ArgoCD prune never goes through
// WorkspaceService.Delete, and a wwt crash can lose the end-of-session
// callback — either way the row stays "open" forever, counting as live
// activity in the idle sweeper and the admin dashboards.
//
// Like the IdleSweeper it is an internal mechanism of the api-server
// binary (a goroutine on a ticker), placed here for the same
// data-ownership reason: only the api-server may touch the database, so
// the operator's finalizer cannot do this cleanup.
type SessionSweeper struct {
	kube      client.Client
	namespace string
	sessions  repository.SessionRepository
	// remotes may be nil (deployments without the remote-workspace
	// feature): remote sessions are then left to their normal end path.
	remotes  repository.RemoteWorkspaceRepository
	audit    *AuditService
	interval time.Duration
}

// NewSessionSweeper builds the sweeper; interval <= 0 disables it.
func NewSessionSweeper(kube client.Client, namespace string, sessions repository.SessionRepository,
	remotes repository.RemoteWorkspaceRepository, audit *AuditService, interval time.Duration) *SessionSweeper {
	return &SessionSweeper{kube: kube, namespace: namespace, sessions: sessions,
		remotes: remotes, audit: audit, interval: interval}
}

// Run blocks until ctx is done, sweeping on every tick.
func (s *SessionSweeper) Run(ctx context.Context) {
	if s.interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	slog.Info("session sweeper started", "interval", s.interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sweep(ctx); err != nil {
				slog.Error("session sweep failed", "error", err)
			}
		}
	}
}

// sweep ends every open session whose target is gone. Idempotent: a
// session already ended by a concurrent path is simply not matched by
// the conditional UPDATE.
func (s *SessionSweeper) sweep(ctx context.Context) error {
	open, err := s.sessions.ListOpen(ctx)
	if err != nil {
		return err
	}
	// The sweeper is the one place that periodically knows the whole open
	// set: refresh the gauge here rather than duplicating that knowledge.
	metrics.ActiveSessions.Set(float64(len(open)))
	if len(open) == 0 {
		return nil
	}

	// One CR list serves the whole pass. Sessions reference workspaces by
	// UID, so a same-named recreation can never be mistaken for the
	// original.
	workspaces := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, workspaces, client.InNamespace(s.namespace)); err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	live := make(map[string]bool, len(workspaces.Items))
	for i := range workspaces.Items {
		live[string(workspaces.Items[i].UID)] = true
	}

	now := time.Now().UTC()
	for i := range open {
		sess := &open[i]
		orphaned := false
		switch sess.Kind {
		case model.SessionKindRemote:
			if s.remotes == nil {
				continue
			}
			if _, err := s.remotes.FindByID(ctx, sess.WorkspaceID); errors.Is(err, repository.ErrRemoteWorkspaceNotFound) {
				orphaned = true
			} else if err != nil {
				return fmt.Errorf("resolving remote workspace %s: %w", sess.WorkspaceID, err)
			}
		default:
			orphaned = !live[sess.WorkspaceID]
		}
		if !orphaned {
			continue
		}
		if err := s.sessions.End(ctx, sess.ID, now); err != nil {
			return fmt.Errorf("ending orphaned session %s: %w", sess.ID, err)
		}
		slog.Info("ended orphaned session (target deleted)",
			"session", sess.ID, "workspace", sess.WorkspaceName, "kind", sess.Kind, "startedAt", sess.StartedAt)
		s.audit.Record(ctx, Actor{Username: "session-sweeper"}, "session.orphan_ended", "session",
			sess.ID, fmt.Sprintf("workspace=%s kind=%s", sess.WorkspaceName, sess.Kind))
	}
	return nil
}
