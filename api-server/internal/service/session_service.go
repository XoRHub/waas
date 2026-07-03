package service

import (
	"context"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
)

// SessionService exposes the session history to the admin API.
type SessionService struct {
	sessions repository.SessionRepository
}

func NewSessionService(sessions repository.SessionRepository) *SessionService {
	return &SessionService{sessions: sessions}
}

// List returns sessions, newest first.
func (s *SessionService) List(ctx context.Context, page, pageSize int) ([]model.Session, int, error) {
	return s.sessions.List(ctx, page, pageSize)
}
