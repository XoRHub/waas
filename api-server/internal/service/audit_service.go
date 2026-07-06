package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
)

// Actor identifies who performed an audited action.
type Actor struct {
	ID       string
	Username string
	Role     string
	ClientIP string
}

// AuditService records append-only audit trail entries. Recording failures
// are logged but never fail the audited action itself.
type AuditService struct {
	repo repository.AuditRepository
}

func NewAuditService(repo repository.AuditRepository) *AuditService {
	return &AuditService{repo: repo}
}

// Record appends one audit entry.
func (s *AuditService) Record(ctx context.Context, actor Actor, action, resourceType, resourceID, detail string) {
	entry := &model.AuditLog{
		ID:            uuid.NewString(),
		OccurredAt:    time.Now().UTC(),
		ActorID:       actor.ID,
		ActorUsername: actor.Username,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		Detail:        detail,
		ClientIP:      actor.ClientIP,
	}
	if err := s.repo.Insert(ctx, entry); err != nil {
		slog.ErrorContext(ctx, "recording audit entry", "action", action, "error", err)
	}
}

// List returns audit entries, newest first, optionally filtered.
func (s *AuditService) List(ctx context.Context, filter repository.AuditFilter, page, pageSize int) ([]model.AuditLog, int, error) {
	return s.repo.List(ctx, filter, page, pageSize)
}
