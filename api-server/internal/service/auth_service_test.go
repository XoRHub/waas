package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// newAuthFixture wires an AuthService against SQLite, seeded with one
// active local account.
func newAuthFixture(t *testing.T, seed *model.User) (*AuthService, repository.UserRepository) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	users := repository.NewSQLUserRepository(db)
	if seed != nil {
		now := time.Now().UTC()
		seed.Active = true
		seed.CreatedAt, seed.UpdatedAt = now, now
		if seed.Role == "" {
			seed.Role = auth.RoleUser
		}
		if err := users.Create(context.Background(), seed); err != nil {
			t.Fatalf("seeding user: %v", err)
		}
	}
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	audit := NewAuditService(repository.NewSQLAuditRepository(db))
	return NewAuthService(users, signer, audit, "waas-test", time.Hour, time.Minute), users
}

func TestLogoutStampsTokenBound(t *testing.T) {
	svc, users := newAuthFixture(t, &model.User{ID: "u1", Username: "alice"})
	before := time.Now().UTC()
	if err := svc.Logout(context.Background(), Actor{ID: "u1", Username: "alice"}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	stored, err := users.FindByID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.TokensValidAfter == nil || stored.TokensValidAfter.Before(before) {
		t.Fatalf("logout must stamp tokens_valid_after >= %v, got %v", before, stored.TokensValidAfter)
	}
}

// Logging out an account that no longer exists must stay a no-op: the
// client's local state is already gone, there is nothing left to revoke.
func TestLogoutOfMissingUserIsIdempotent(t *testing.T) {
	svc, _ := newAuthFixture(t, nil)
	if err := svc.Logout(context.Background(), Actor{ID: "ghost"}); err != nil {
		t.Fatalf("logout of a missing user must not fail: %v", err)
	}
}

// Login stamps last_login_at; a past revocation bound must survive it —
// otherwise a fresh login would resurrect every token stolen before the
// previous logout.
func TestLoginPreservesTokenBound(t *testing.T) {
	hash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("hashing seed password: %v", err)
	}
	bound := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	svc, users := newAuthFixture(t, &model.User{
		ID: "u1", Username: "alice", PasswordHash: hash, TokensValidAfter: &bound,
	})
	if _, err := svc.Login(context.Background(), "alice", "secret-password", "10.0.0.1"); err != nil {
		t.Fatalf("login: %v", err)
	}
	stored, err := users.FindByID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.TokensValidAfter == nil || !stored.TokensValidAfter.Equal(bound) {
		t.Fatalf("login must preserve the token bound %v, got %v", bound, stored.TokensValidAfter)
	}
}

// demotingUsers lands an admin deactivation + demotion right after Login's
// read returns — deterministically inside the argon2id window the F5 race
// (audit 2026-07-25) needs.
type demotingUsers struct {
	repository.UserRepository
	t *testing.T
}

func (r *demotingUsers) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	user, err := r.UserRepository.FindByUsername(ctx, username)
	if err == nil {
		if err := r.SetActive(ctx, user.ID, false); err != nil {
			r.t.Fatalf("concurrent deactivation: %v", err)
		}
		if err := r.SetRole(ctx, user.ID, auth.RoleUser); err != nil {
			r.t.Fatalf("concurrent demotion: %v", err)
		}
	}
	return user, err
}

// A deactivation or demotion that lands while Login verifies the password
// (~50-100ms of argon2id) must survive the login's own row write: Login
// holds a copy read before the change, and writing it back whole would
// silently undo it. The login itself still succeeds — it was checked
// against a then-valid copy — but per-request revocation catches the
// account on its next call, so the row must keep the truth.
func TestLoginPreservesConcurrentDeactivationAndDemotion(t *testing.T) {
	hash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("hashing seed password: %v", err)
	}
	svc, users := newAuthFixture(t, &model.User{
		ID: "u1", Username: "alice", PasswordHash: hash, Role: auth.RoleAdmin,
	})
	svc.users = &demotingUsers{UserRepository: users, t: t}
	if _, err := svc.Login(context.Background(), "alice", "secret-password", "10.0.0.1"); err != nil {
		t.Fatalf("login against a then-valid copy: %v", err)
	}
	stored, err := users.FindByID(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Active {
		t.Fatal("login must not reactivate an account deactivated during password verification")
	}
	if stored.Role != auth.RoleUser {
		t.Fatalf("login must not undo a demotion landed during password verification, got role %q", stored.Role)
	}
	if stored.LastLoginAt == nil {
		t.Fatal("the login stamp itself must still land")
	}
}
