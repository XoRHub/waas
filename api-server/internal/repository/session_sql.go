package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
)

// SQLSessionRepository implements SessionRepository on PostgreSQL/SQLite.
type SQLSessionRepository struct {
	db *database.DB
}

func NewSQLSessionRepository(db *database.DB) *SQLSessionRepository {
	return &SQLSessionRepository{db: db}
}

const sessionColumns = "id, user_id, workspace_id, workspace_name, protocol, client_ip, started_at, ended_at"

func (r *SQLSessionRepository) Create(ctx context.Context, s *model.Session) error {
	query := r.db.Rebind(`INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, query,
		s.ID, s.UserID, s.WorkspaceID, s.WorkspaceName, s.Protocol, nullable(s.ClientIP),
		timeArg(s.StartedAt), timePtrArg(s.EndedAt))
	if err != nil {
		return fmt.Errorf("creating session %s: %w", s.ID, err)
	}
	return nil
}

func (r *SQLSessionRepository) FindByID(ctx context.Context, id string) (*model.Session, error) {
	query := r.db.Rebind(`SELECT ` + sessionColumns + ` FROM sessions WHERE id = ?`)
	s, err := scanSession(r.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding session %s: %w", id, err)
	}
	return s, nil
}

func (r *SQLSessionRepository) End(ctx context.Context, id string, at time.Time) error {
	query := r.db.Rebind(`UPDATE sessions SET ended_at = ? WHERE id = ? AND ended_at IS NULL`)
	if _, err := r.db.ExecContext(ctx, query, timeArg(at), id); err != nil {
		return fmt.Errorf("ending session %s: %w", id, err)
	}
	return nil
}

func (r *SQLSessionRepository) List(ctx context.Context, page, pageSize int) ([]model.Session, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting sessions: %w", err)
	}
	query := r.db.Rebind(`SELECT ` + sessionColumns + ` FROM sessions ORDER BY started_at DESC LIMIT ? OFFSET ?`)
	rows, err := r.db.QueryContext(ctx, query, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	sessions := []model.Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning session row: %w", err)
		}
		sessions = append(sessions, *s)
	}
	return sessions, total, rows.Err()
}

func scanSession(row rowScanner) (*model.Session, error) {
	var (
		s        model.Session
		clientIP sql.NullString
	)
	if err := row.Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.WorkspaceName, &s.Protocol,
		&clientIP, scanTime{&s.StartedAt}, scanNullTime{&s.EndedAt}); err != nil {
		return nil, err
	}
	s.ClientIP = clientIP.String
	return &s, nil
}
