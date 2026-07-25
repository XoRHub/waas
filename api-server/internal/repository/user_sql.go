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

// CountActiveAdmins counts the accounts that can still administer the
// platform. Excludes deactivated ones: a disabled admin cannot sign in,
// so it is no help against locking everyone out.
func (r *SQLUserRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	query := r.db.Rebind(`SELECT COUNT(*) FROM users WHERE role = ? AND active = ?`)
	var total int
	if err := r.db.QueryRowContext(ctx, query, string(auth.RoleAdmin), true).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting active admins: %w", err)
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

// isSerializationFailure reports a transaction the engine refused rather
// than let it interleave into an inconsistent result (SQLSTATE 40001).
// Only PostgreSQL produces it: the SQLite pool is capped at one
// connection, so its transactions never interleave in the first place.
func isSerializationFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "40001") || strings.Contains(msg, "could not serialize")
}

// withAdminFloor runs write in a transaction that is rolled back unless at
// least one active administrator remains afterwards.
//
// The count is taken INSIDE the transaction and AFTER the write, so it
// judges the state the caller is actually about to commit. Serializable is
// what makes that judgement hold under concurrency: two admins demoting
// themselves at the same moment each write a DIFFERENT row, so row locks
// alone would let both pass a count that still sees the other — the
// classic write skew, and here it ends in a platform nobody can
// administer. One retry, because the engine's refusal means the other
// transaction won: the retry re-counts and refuses on its own terms.
func (r *SQLUserRepository) withAdminFloor(ctx context.Context, id string, write func(*sql.Tx) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		err := r.tryWithAdminFloor(ctx, id, write)
		if isSerializationFailure(err) && attempt == 0 {
			continue
		}
		return err
	}
	return nil
}

func (r *SQLUserRepository) tryWithAdminFloor(ctx context.Context, id string, write func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	// Hold the admin seats for the duration of the decision. Without this
	// two transactions demoting two DIFFERENT admins never touch the same
	// row, so neither blocks the other and both count a seat the other is
	// about to vacate — write skew, ending with nobody able to administer
	// the platform. Locking blocks the second one until the first commits,
	// which is deterministic where serializable isolation would instead
	// abort it and need a retry that can conflict again.
	//
	// ORDER BY id fixes the lock order, so two concurrent guards cannot
	// deadlock on each other. SQLite has no FOR UPDATE and needs none: its
	// pool is capped at a single connection, so writers never interleave.
	if r.db.Dialect == database.DialectPostgres {
		lock := `SELECT id FROM users WHERE role = $1 AND active = $2 ORDER BY id FOR UPDATE`
		rows, err := tx.QueryContext(ctx, lock, string(auth.RoleAdmin), true)
		if err != nil {
			return fmt.Errorf("locking the administrator seats: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("locking the administrator seats: %w", err)
		}
	}

	// Read inside the transaction, not from whatever the caller fetched
	// earlier: only an account that IS an active admin can take the last
	// one away, and that has to be judged against the state being written.
	var role string
	var active bool
	read := r.db.Rebind(`SELECT role, active FROM users WHERE id = ?`)
	switch err := tx.QueryRowContext(ctx, read, id).Scan(&role, &active); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrUserNotFound
	case err != nil:
		return fmt.Errorf("reading account %s: %w", id, err)
	}
	wasActiveAdmin := role == string(auth.RoleAdmin) && active

	if err := write(tx); err != nil {
		return err
	}
	// A write by anyone else cannot reduce the admin count, so the floor
	// only applies to the account that was holding a seat.
	if wasActiveAdmin {
		count := r.db.Rebind(`SELECT COUNT(*) FROM users WHERE role = ? AND active = ?`)
		var admins int
		if err := tx.QueryRowContext(ctx, count, string(auth.RoleAdmin), true).Scan(&admins); err != nil {
			return fmt.Errorf("counting active admins: %w", err)
		}
		if admins == 0 {
			return ErrLastAdmin
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// SetRoleUnlessLastAdmin is SetRole for the admin API: it refuses to leave
// the platform with no active administrator (ErrLastAdmin). The OIDC role
// sync deliberately uses the unguarded SetRole — with adminGroups
// configured the IdP owns the role, and an IdP-driven demotion is undone
// by re-adding the group, unlike this path which has no way back.
func (r *SQLUserRepository) SetRoleUnlessLastAdmin(ctx context.Context, id string, role auth.Role) error {
	return r.withAdminFloor(ctx, id, func(tx *sql.Tx) error {
		query := r.db.Rebind(`UPDATE users SET role = ? WHERE id = ?`)
		_, err := tx.ExecContext(ctx, query, string(role), id)
		if err != nil {
			return fmt.Errorf("setting role for %s: %w", id, err)
		}
		return nil
	})
}

// SetActiveUnlessLastAdmin is SetActive under the same floor.
func (r *SQLUserRepository) SetActiveUnlessLastAdmin(ctx context.Context, id string, active bool) error {
	return r.withAdminFloor(ctx, id, func(tx *sql.Tx) error {
		query := r.db.Rebind(`UPDATE users SET active = ? WHERE id = ?`)
		_, err := tx.ExecContext(ctx, query, active, id)
		if err != nil {
			return fmt.Errorf("setting activation for %s: %w", id, err)
		}
		return nil
	})
}
