package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// newOIDCFixture wires an OIDCService against SQLite, seeded with the given
// users. Account resolution (subject binding, username linking, takeover
// refusal) is what these tests exercise — no IdP round-trip involved.
func newOIDCFixture(t *testing.T, cfg config.OIDCConfig, seed []model.User) (*OIDCService, repository.UserRepository) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	users := repository.NewSQLUserRepository(db)
	now := time.Now().UTC()
	for i := range seed {
		u := seed[i]
		if u.Role == "" {
			u.Role = auth.RoleUser
		}
		u.Active = true
		u.CreatedAt, u.UpdatedAt = now, now
		if err := users.Create(context.Background(), &u); err != nil {
			t.Fatalf("seeding user %s: %v", u.Username, err)
		}
	}
	audit := NewAuditService(repository.NewSQLAuditRepository(db))
	return NewOIDCService(cfg, users, audit, nil, "waas", time.Hour), users
}

func unauthorized(t *testing.T, err error) {
	t.Helper()
	var p *apierror.Problem
	if !errors.As(err, &p) || p.Status != 401 {
		t.Fatalf("want 401 Problem, got %v", err)
	}
}

func TestSyncUserProvisionsWithSubject(t *testing.T) {
	svc, users := newOIDCFixture(t, config.OIDCConfig{}, nil)
	user, err := svc.syncUser(context.Background(), "sub-1",
		oidcIdentity{Username: "alice", Email: "alice@example.com", Groups: []string{"dev"}}, "1.2.3.4")
	if err != nil {
		t.Fatalf("syncUser: %v", err)
	}
	if user.OIDCSubject != "sub-1" {
		t.Fatalf("subject not pinned at provisioning: %q", user.OIDCSubject)
	}
	if _, err := users.FindByOIDCSubject(context.Background(), "sub-1"); err != nil {
		t.Fatalf("provisioned user not resolvable by subject: %v", err)
	}
}

func TestSyncUserSubjectWinsOverUsername(t *testing.T) {
	// "mallory" renamed their IdP username to "victim". Their account is
	// bound by subject, so they get their OWN account back — never victim's.
	svc, _ := newOIDCFixture(t, config.OIDCConfig{}, []model.User{
		{ID: "u-victim", Username: "victim", PasswordHash: "argon2:x"},
		{ID: "u-mallory", Username: "mallory", OIDCSubject: "sub-mallory"},
	})
	user, err := svc.syncUser(context.Background(), "sub-mallory",
		oidcIdentity{Username: "victim"}, "1.2.3.4")
	if err != nil {
		t.Fatalf("syncUser: %v", err)
	}
	if user.ID != "u-mallory" {
		t.Fatalf("resolved %s, want the subject-bound account u-mallory", user.ID)
	}
}

func TestSyncUserRefusesUsernameCollision(t *testing.T) {
	// An unknown subject claiming an already-taken username is an
	// attempted takeover, never a match — the vector this binding closes.
	for name, seed := range map[string]model.User{
		"local account":                  {ID: "u-1", Username: "taken", PasswordHash: "argon2:x", Role: auth.RoleAdmin},
		"account bound to other subject": {ID: "u-1", Username: "taken", OIDCSubject: "sub-owner"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, users := newOIDCFixture(t, config.OIDCConfig{}, []model.User{seed})
			_, err := svc.syncUser(context.Background(), "sub-attacker", oidcIdentity{Username: "taken"}, "1.2.3.4")
			unauthorized(t, err)
			if stored, _ := users.FindByID(context.Background(), "u-1"); stored.OIDCSubject != seed.OIDCSubject {
				t.Fatalf("account subject must stay untouched, got %q", stored.OIDCSubject)
			}
		})
	}
}

func TestSyncUserRefreshesGroupsMirror(t *testing.T) {
	svc, users := newOIDCFixture(t, config.OIDCConfig{AdminGroups: []string{"platform-admins"}}, []model.User{
		{ID: "u-erin", Username: "erin", OIDCSubject: "sub-erin", Groups: []string{"old"}},
	})
	user, err := svc.syncUser(context.Background(), "sub-erin",
		oidcIdentity{Username: "erin", Groups: []string{"platform-admins", "dev"}}, "1.2.3.4")
	if err != nil {
		t.Fatalf("syncUser: %v", err)
	}
	if user.Role != auth.RoleAdmin {
		t.Fatalf("AdminGroups membership must grant admin, got %s", user.Role)
	}
	stored, _ := users.FindByID(context.Background(), "u-erin")
	if len(stored.Groups) != 2 || stored.Groups[0] != "platform-admins" {
		t.Fatalf("groups mirror not refreshed: %v", stored.Groups)
	}
}
