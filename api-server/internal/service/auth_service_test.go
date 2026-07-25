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

// Login rewrites the whole user row (last_login_at); a past revocation
// bound must survive it — otherwise a fresh login would resurrect every
// token stolen before the previous logout.
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
