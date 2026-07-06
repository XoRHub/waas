package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
)

// SQLAuditRepository implements AuditRepository. Inserts and reads only —
// the audit trail is append-only by contract.
type SQLAuditRepository struct {
	db *database.DB
}

func NewSQLAuditRepository(db *database.DB) *SQLAuditRepository {
	return &SQLAuditRepository{db: db}
}

const auditColumns = "id, occurred_at, actor_id, actor_username, action, resource_type, resource_id, detail, client_ip"

func (r *SQLAuditRepository) Insert(ctx context.Context, e *model.AuditLog) error {
	query := r.db.Rebind(`INSERT INTO audit_logs (` + auditColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, query,
		e.ID, timeArg(e.OccurredAt), nullable(e.ActorID), nullable(e.ActorUsername), e.Action,
		e.ResourceType, nullable(e.ResourceID), nullable(e.Detail), nullable(e.ClientIP))
	if err != nil {
		return fmt.Errorf("inserting audit log: %w", err)
	}
	return nil
}

func (r *SQLAuditRepository) List(ctx context.Context, filter AuditFilter, page, pageSize int) ([]model.AuditLog, int, error) {
	where, args := auditWhere(filter)
	var total int
	if err := r.db.QueryRowContext(ctx, r.db.Rebind(`SELECT COUNT(*) FROM audit_logs`+where), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting audit logs: %w", err)
	}
	query := r.db.Rebind(`SELECT ` + auditColumns + ` FROM audit_logs` + where + ` ORDER BY occurred_at DESC LIMIT ? OFFSET ?`)
	rows, err := r.db.QueryContext(ctx, query, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing audit logs: %w", err)
	}
	defer rows.Close()

	entries, err := scanAuditRows(rows)
	return entries, total, err
}

// auditWhere renders the filter as a WHERE clause; the count and page
// queries share it so total always matches the filtered rows.
func auditWhere(filter AuditFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.Actor != "" {
		clauses = append(clauses, "actor_username LIKE ?")
		args = append(args, "%"+filter.Actor+"%")
	}
	if filter.Action != "" {
		clauses = append(clauses, "action LIKE ?")
		args = append(args, filter.Action+"%")
	}
	if !filter.From.IsZero() {
		clauses = append(clauses, "occurred_at >= ?")
		args = append(args, timeArg(filter.From))
	}
	if !filter.To.IsZero() {
		clauses = append(clauses, "occurred_at <= ?")
		args = append(args, timeArg(filter.To))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanAuditRows(rows *sql.Rows) ([]model.AuditLog, error) {
	entries := []model.AuditLog{}
	for rows.Next() {
		var (
			e                                         model.AuditLog
			actorID, actorUsername, resID, detail, ip sql.NullString
		)
		if err := rows.Scan(&e.ID, scanTime{&e.OccurredAt}, &actorID, &actorUsername, &e.Action,
			&e.ResourceType, &resID, &detail, &ip); err != nil {
			return nil, fmt.Errorf("scanning audit row: %w", err)
		}
		e.ActorID, e.ActorUsername, e.ResourceID, e.Detail, e.ClientIP =
			actorID.String, actorUsername.String, resID.String, detail.String, ip.String
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
