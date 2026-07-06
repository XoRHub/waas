package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
)

// SQLRemoteWorkspaceRepository implements RemoteWorkspaceRepository on
// PostgreSQL/SQLite (same dual-engine conventions as the other repos:
// Rebind placeholders, RFC3339 time args).
type SQLRemoteWorkspaceRepository struct {
	db *database.DB
}

func NewSQLRemoteWorkspaceRepository(db *database.DB) *SQLRemoteWorkspaceRepository {
	return &SQLRemoteWorkspaceRepository{db: db}
}

const remoteWorkspaceColumns = "id, owner_id, name, hostname, port, protocol, mac_address, params, secret_name, credential_keys, created_at, updated_at"

func (r *SQLRemoteWorkspaceRepository) Create(ctx context.Context, rw *model.RemoteWorkspace) error {
	query := r.db.Rebind(`INSERT INTO remote_workspaces (` + remoteWorkspaceColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, query,
		rw.ID, rw.OwnerID, rw.Name, rw.Hostname, rw.Port, rw.Protocol, rw.MACAddress,
		marshalParams(rw.Params), rw.SecretName, marshalKeys(rw.CredentialKeys),
		timeArg(rw.CreatedAt), timeArg(rw.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("creating remote workspace %s: %w", rw.ID, err)
	}
	return nil
}

func (r *SQLRemoteWorkspaceRepository) FindByID(ctx context.Context, id string) (*model.RemoteWorkspace, error) {
	query := r.db.Rebind(`SELECT ` + remoteWorkspaceColumns + ` FROM remote_workspaces WHERE id = ?`)
	rw, err := scanRemoteWorkspace(r.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRemoteWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding remote workspace %s: %w", id, err)
	}
	return rw, nil
}

func (r *SQLRemoteWorkspaceRepository) ListByOwner(ctx context.Context, ownerID string) ([]model.RemoteWorkspace, error) {
	query := r.db.Rebind(`SELECT ` + remoteWorkspaceColumns + ` FROM remote_workspaces WHERE owner_id = ? ORDER BY name`)
	rows, err := r.db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("listing remote workspaces: %w", err)
	}
	defer rows.Close()

	out := []model.RemoteWorkspace{}
	for rows.Next() {
		rw, err := scanRemoteWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning remote workspace row: %w", err)
		}
		out = append(out, *rw)
	}
	return out, rows.Err()
}

func (r *SQLRemoteWorkspaceRepository) Update(ctx context.Context, rw *model.RemoteWorkspace) error {
	query := r.db.Rebind(`UPDATE remote_workspaces
		SET name = ?, hostname = ?, port = ?, protocol = ?, mac_address = ?, params = ?, credential_keys = ?, updated_at = ?
		WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query,
		rw.Name, rw.Hostname, rw.Port, rw.Protocol, rw.MACAddress,
		marshalParams(rw.Params), marshalKeys(rw.CredentialKeys), timeArg(rw.UpdatedAt), rw.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("updating remote workspace %s: %w", rw.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRemoteWorkspaceNotFound
	}
	return nil
}

func (r *SQLRemoteWorkspaceRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM remote_workspaces WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("deleting remote workspace %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRemoteWorkspaceNotFound
	}
	return nil
}

func scanRemoteWorkspace(row rowScanner) (*model.RemoteWorkspace, error) {
	var (
		rw     model.RemoteWorkspace
		params string
		keys   string
	)
	if err := row.Scan(&rw.ID, &rw.OwnerID, &rw.Name, &rw.Hostname, &rw.Port, &rw.Protocol, &rw.MACAddress,
		&params, &rw.SecretName, &keys, scanTime{&rw.CreatedAt}, scanTime{&rw.UpdatedAt}); err != nil {
		return nil, err
	}
	rw.Params = unmarshalParams(params)
	rw.CredentialKeys = unmarshalKeys(keys)
	return &rw, nil
}

func marshalKeys(keys []string) string {
	if len(keys) == 0 {
		return "[]"
	}
	b, err := json.Marshal(keys)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalKeys(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
