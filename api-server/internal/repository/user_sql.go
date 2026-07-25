package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/shared/auth"
)

// SQLUserRepository implements UserRepository on PostgreSQL/SQLite.
type SQLUserRepository struct {
	db *database.DB
}

func NewSQLUserRepository(db *database.DB) *SQLUserRepository {
	return &SQLUserRepository{db: db}
}

const userColumns = "id, username, email, password_hash, role, active, max_workspaces, created_at, updated_at, last_login_at, user_groups, display_name, preferences, oidc_subject, tokens_valid_after"

func (r *SQLUserRepository) Create(ctx context.Context, user *model.User) error {
	query := r.db.Rebind(`INSERT INTO users (` + userColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Username, nullable(user.Email), user.PasswordHash, string(user.Role),
		user.Active, user.MaxWorkspaces, timeArg(user.CreatedAt), timeArg(user.UpdatedAt), timePtrArg(user.LastLoginAt),
		strings.Join(user.Groups, ","), user.DisplayName, marshalPreferences(user.Preferences), user.OIDCSubject,
		timePtrArg(user.TokensValidAfter))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("creating user %s: %w", user.Username, ErrDuplicate)
		}
		return fmt.Errorf("creating user %s: %w", user.Username, err)
	}
	return nil
}

func (r *SQLUserRepository) FindByID(ctx context.Context, id string) (*model.User, error) {
	return r.findBy(ctx, "id", id)
}

func (r *SQLUserRepository) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	return r.findBy(ctx, "username", username)
}

// FindByOIDCSubject resolves an account by the IdP's stable subject. The
// empty subject never matches (it would hit every local account).
func (r *SQLUserRepository) FindByOIDCSubject(ctx context.Context, subject string) (*model.User, error) {
	if subject == "" {
		return nil, ErrUserNotFound
	}
	return r.findBy(ctx, "oidc_subject", subject)
}

func (r *SQLUserRepository) findBy(ctx context.Context, column, value string) (*model.User, error) {
	query := r.db.Rebind(`SELECT ` + userColumns + ` FROM users WHERE ` + column + ` = ?`)
	user, err := scanUser(r.db.QueryRowContext(ctx, query, value))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by %s: %w", column, err)
	}
	return user, nil
}

// ListUsernames returns every account's username. Unpaginated on
// purpose: the callers project each one into a DNS-1123 label (Unicode
// NFKD — no SQL equivalent) to detect a placement-namespace collision,
// so the whole column is the input. Both call sites are cold — creating
// an account, and the first login of an unknown SSO subject.
func (r *SQLUserRepository) ListUsernames(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT username FROM users`)
	if err != nil {
		return nil, fmt.Errorf("listing usernames: %w", err)
	}
	defer rows.Close()

	usernames := []string{}
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return nil, fmt.Errorf("scanning username row: %w", err)
		}
		usernames = append(usernames, username)
	}
	return usernames, rows.Err()
}

func (r *SQLUserRepository) List(ctx context.Context, page, pageSize int) ([]model.User, int, error) {
	total, err := r.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := r.db.Rebind(`SELECT ` + userColumns + ` FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`)
	rows, err := r.db.QueryContext(ctx, query, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	users := []model.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, *user)
	}
	return users, total, rows.Err()
}

// Update rewrites the mutable columns of a user row from an in-memory
// copy. It deliberately does NOT write tokens_valid_after, role or
// active: every caller here is a read-modify-write, and the read can
// predate a concurrent change by however long the operation takes —
// Login alone spends ~50-100ms in argon2id between its read and its
// write. Written back from that stale copy, tokens_valid_after would
// resurrect every token a concurrent logout had just revoked, and
// role/active — the two columns per-request revocation reads on every
// call (middleware.vetBearer) — would silently undo a concurrent
// demotion or deactivation. SetTokensValidAfter, SetRole and SetActive
// are their only writers.
func (r *SQLUserRepository) Update(ctx context.Context, user *model.User) error {
	query := r.db.Rebind(`UPDATE users SET email = ?, password_hash = ?,
		max_workspaces = ?, updated_at = ?, last_login_at = ?, user_groups = ?, display_name = ?, preferences = ?, oidc_subject = ? WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query,
		nullable(user.Email), user.PasswordHash,
		user.MaxWorkspaces, timeArg(user.UpdatedAt), timePtrArg(user.LastLoginAt),
		strings.Join(user.Groups, ","), user.DisplayName, marshalPreferences(user.Preferences), user.OIDCSubject,
		user.ID)
	if err != nil {
		return fmt.Errorf("updating user %s: %w", user.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// RecordLogin stamps a successful login with a single targeted UPDATE.
// The login paths must use it instead of Update: they hold a copy of the
// row read before password verification, and a full-row write from that
// copy would carry everything else back stale too.
func (r *SQLUserRepository) RecordLogin(ctx context.Context, id string, at time.Time) error {
	query := r.db.Rebind(`UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query, timeArg(at), timeArg(at), id)
	if err != nil {
		return fmt.Errorf("recording login for user %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetRole changes the account role with a single targeted UPDATE. Like
// SetTokensValidAfter it is the ONLY writer of its column: Update leaves
// role alone so that no read-modify-write on the rest of the row can
// race a demotion and write the old role back.
func (r *SQLUserRepository) SetRole(ctx context.Context, id string, role auth.Role) error {
	query := r.db.Rebind(`UPDATE users SET role = ? WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query, string(role), id)
	if err != nil {
		return fmt.Errorf("setting role for user %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetActive flips account activation with a single targeted UPDATE —
// same rationale as SetRole: a stale full-row write must never be able
// to reactivate an account a concurrent admin edit just disabled.
func (r *SQLUserRepository) SetActive(ctx context.Context, id string, active bool) error {
	query := r.db.Rebind(`UPDATE users SET active = ? WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query, active, id)
	if err != nil {
		return fmt.Errorf("setting activation for user %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetTokensValidAfter revokes the user's outstanding tokens with a single
// targeted UPDATE. It is the ONLY writer of tokens_valid_after: Update
// leaves the column alone precisely so that no read-modify-write on the
// rest of the row can race a revocation and write back a stale bound.
// Callers that also mutate the row must run their Update FIRST and revoke
// after, never the reverse.
func (r *SQLUserRepository) SetTokensValidAfter(ctx context.Context, id string, at time.Time) error {
	query := r.db.Rebind(`UPDATE users SET tokens_valid_after = ? WHERE id = ?`)
	res, err := r.db.ExecContext(ctx, query, timeArg(at), id)
	if err != nil {
		return fmt.Errorf("setting token bound for user %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *SQLUserRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM users WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("deleting user %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *SQLUserRepository) Count(ctx context.Context) (int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return total, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(row rowScanner) (*model.User, error) {
	var (
		user   model.User
		email  sql.NullString
		role   string
		groups string
		prefs  string
	)
	if err := row.Scan(&user.ID, &user.Username, &email, &user.PasswordHash, &role,
		&user.Active, &user.MaxWorkspaces, scanTime{&user.CreatedAt}, scanTime{&user.UpdatedAt},
		scanNullTime{&user.LastLoginAt}, &groups, &user.DisplayName, &prefs, &user.OIDCSubject,
		scanNullTime{&user.TokensValidAfter}); err != nil {
		return nil, err
	}
	user.Email = email.String
	user.Role = auth.Role(role)
	user.Groups = splitGroups(groups)
	user.Preferences = unmarshalPreferences(prefs)
	return &user, nil
}

// marshalPreferences serializes the preferences JSON column; a zero value
// round-trips as "{}" so the NOT NULL DEFAULT stays meaningful.
func marshalPreferences(p model.UserPreferences) string {
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// unmarshalPreferences is tolerant: an empty or corrupt column yields the
// zero preferences instead of failing the whole user read.
func unmarshalPreferences(s string) model.UserPreferences {
	var p model.UserPreferences
	if s != "" {
		_ = json.Unmarshal([]byte(s), &p)
	}
	return p
}

// splitGroups parses the comma-joined user_groups column.
func splitGroups(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, g := range strings.Split(s, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation matches unique-constraint errors across both dialects
// without importing driver-specific error types into the repository API.
func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}
