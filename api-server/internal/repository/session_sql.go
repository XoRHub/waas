package repository

import (
	"context"
	"database/sql"
	"encoding/json"
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

const sessionColumns = "id, user_id, workspace_id, workspace_name, protocol, client_ip, started_at, ended_at, params, kind"

func (r *SQLSessionRepository) Create(ctx context.Context, s *model.Session) error {
	kind := s.Kind
	if kind == "" {
		kind = model.SessionKindWorkspace
	}
	query := r.db.Rebind(`INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, query,
		s.ID, s.UserID, s.WorkspaceID, s.WorkspaceName, s.Protocol, nullable(s.ClientIP),
		timeArg(s.StartedAt), timePtrArg(s.EndedAt), marshalParams(s.Params), kind)
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

func (r *SQLSessionRepository) EndAllForWorkspace(ctx context.Context, workspaceID string, at time.Time) (int, error) {
	query := r.db.Rebind(`UPDATE sessions SET ended_at = ? WHERE workspace_id = ? AND ended_at IS NULL`)
	res, err := r.db.ExecContext(ctx, query, timeArg(at), workspaceID)
	if err != nil {
		return 0, fmt.Errorf("ending sessions of workspace %s: %w", workspaceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // engines without RowsAffected: the update still happened
	}
	return int(n), nil
}

func (r *SQLSessionRepository) ListOpen(ctx context.Context) ([]model.Session, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE ended_at IS NULL ORDER BY started_at`)
	if err != nil {
		return nil, fmt.Errorf("listing open sessions: %w", err)
	}
	defer rows.Close()

	sessions := []model.Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		sessions = append(sessions, *s)
	}
	return sessions, rows.Err()
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

// Activity aggregates sessions per workspace. Timestamps are stored as
// RFC3339 UTC strings on both engines, so MAX() compares correctly.
func (r *SQLSessionRepository) Activity(ctx context.Context) (map[string]WorkspaceActivity, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT workspace_id,
		       MAX(COALESCE(ended_at, started_at)) AS last_activity,
		       SUM(CASE WHEN ended_at IS NULL THEN 1 ELSE 0 END) AS active
		FROM sessions GROUP BY workspace_id`)
	if err != nil {
		return nil, fmt.Errorf("aggregating session activity: %w", err)
	}
	defer rows.Close()

	out := map[string]WorkspaceActivity{}
	for rows.Next() {
		var (
			id     string
			last   time.Time
			active int
		)
		if err := rows.Scan(&id, scanTime{&last}, &active); err != nil {
			return nil, fmt.Errorf("scanning activity row: %w", err)
		}
		out[id] = WorkspaceActivity{LastActivity: last, ActiveNow: active > 0}
	}
	return out, rows.Err()
}

func scanSession(row rowScanner) (*model.Session, error) {
	var (
		s        model.Session
		clientIP sql.NullString
		params   string
	)
	if err := row.Scan(&s.ID, &s.UserID, &s.WorkspaceID, &s.WorkspaceName, &s.Protocol,
		&clientIP, scanTime{&s.StartedAt}, scanNullTime{&s.EndedAt}, &params, &s.Kind); err != nil {
		return nil, err
	}
	s.ClientIP = clientIP.String
	s.Params = unmarshalParams(params)
	return &s, nil
}

// marshalParams serializes the connect-time params JSON column.
func marshalParams(p map[string]string) string {
	if len(p) == 0 {
		return "{}"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// unmarshalParams is tolerant: an empty or corrupt column yields nil
// instead of failing the whole session read.
func unmarshalParams(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
