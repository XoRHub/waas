package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/xorhub/waas/api-server/internal/database"
)

// SQLCatalogRepository implements CatalogRepository on PostgreSQL/SQLite.
type SQLCatalogRepository struct {
	db *database.DB
}

func NewSQLCatalogRepository(db *database.DB) *SQLCatalogRepository {
	return &SQLCatalogRepository{db: db}
}

const catalogEntryColumns = "image, os, app, version, icon, display_name, profile, recommended, synced_at"

// ReplaceEntries deletes every existing row of workspaceImageName and
// inserts entries in the same transaction, so a picker read never
// observes an empty catalog mid-sync.
func (r *SQLCatalogRepository) ReplaceEntries(ctx context.Context, workspaceImageName string, entries []CatalogEntry) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning catalog replace tx for %s: %w", workspaceImageName, err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, r.db.Rebind(`DELETE FROM catalog_entries WHERE workspace_image_name = ?`), workspaceImageName); err != nil {
		return fmt.Errorf("clearing catalog entries of %s: %w", workspaceImageName, err)
	}

	insert := r.db.Rebind(`INSERT INTO catalog_entries (workspace_image_name, ` + catalogEntryColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	for _, e := range entries {
		if _, err := tx.ExecContext(ctx, insert,
			workspaceImageName, e.Image, nullable(e.OS), nullable(e.App), nullable(e.Version), nullable(e.Icon), nullable(e.DisplayName),
			nullable(e.Profile), nullableJSON(e.Recommended), timeArg(e.SyncedAt)); err != nil {
			return fmt.Errorf("inserting catalog entry %s/%s: %w", workspaceImageName, e.Image, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing catalog replace tx for %s: %w", workspaceImageName, err)
	}
	return nil
}

func (r *SQLCatalogRepository) ListEntries(ctx context.Context, workspaceImageName string) ([]CatalogEntry, error) {
	query := r.db.Rebind(`SELECT ` + catalogEntryColumns + ` FROM catalog_entries WHERE workspace_image_name = ? ORDER BY image`)
	rows, err := r.db.QueryContext(ctx, query, workspaceImageName)
	if err != nil {
		return nil, fmt.Errorf("listing catalog entries of %s: %w", workspaceImageName, err)
	}
	defer rows.Close()

	out := []CatalogEntry{}
	for rows.Next() {
		e, err := scanCatalogEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning catalog entry row of %s: %w", workspaceImageName, err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func scanCatalogEntry(row rowScanner) (*CatalogEntry, error) {
	var (
		e                                     CatalogEntry
		os, app, version, icon, name, profile sql.NullString
		recommended                           sql.NullString
	)
	if err := row.Scan(&e.Image, &os, &app, &version, &icon, &name, &profile, &recommended, scanTime{&e.SyncedAt}); err != nil {
		return nil, err
	}
	e.OS, e.App, e.Version, e.Icon, e.DisplayName = os.String, app.String, version.String, icon.String, name.String
	e.Profile = profile.String
	if recommended.Valid {
		e.Recommended = json.RawMessage(recommended.String)
	}
	return &e, nil
}

// nullableJSON stores an empty/nil json.RawMessage as SQL NULL, never
// an empty string — keeps "no recommendation" distinguishable from "an
// empty JSON object" on read.
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
