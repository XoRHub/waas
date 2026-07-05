// Package repository defines the persistence interfaces and their SQL
// implementations. Services depend on the interfaces only.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/xorhub/waas/api-server/internal/model"
)

var (
	ErrUserNotFound    = errors.New("user not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrDuplicate       = errors.New("duplicate record")
)

// UserRepository persists platform accounts.
type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByID(ctx context.Context, id string) (*model.User, error)
	FindByUsername(ctx context.Context, username string) (*model.User, error)
	List(ctx context.Context, page, pageSize int) ([]model.User, int, error)
	Update(ctx context.Context, user *model.User) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

// SessionRepository persists desktop connection sessions.
type SessionRepository interface {
	Create(ctx context.Context, session *model.Session) error
	FindByID(ctx context.Context, id string) (*model.Session, error)
	End(ctx context.Context, id string, at time.Time) error
	List(ctx context.Context, page, pageSize int) ([]model.Session, int, error)
	// Activity summarizes desktop usage per workspace for the idle
	// sweeper: last observed activity and whether a session is open now.
	Activity(ctx context.Context) (map[string]WorkspaceActivity, error)
}

// WorkspaceActivity is one workspace's session summary.
type WorkspaceActivity struct {
	LastActivity time.Time
	ActiveNow    bool
}

// AuditRepository is append-only: it deliberately exposes no update or
// delete operation, matching the audit_logs table contract.
type AuditRepository interface {
	Insert(ctx context.Context, entry *model.AuditLog) error
	List(ctx context.Context, page, pageSize int) ([]model.AuditLog, int, error)
}
