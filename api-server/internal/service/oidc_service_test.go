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
	"github.com/xorhub/waas/operator/pkg/naming"
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

// A username the IdP considers distinct, but which projects onto an
// existing account's placement namespace, is refused at provisioning:
// admitting it would put two accounts in one namespace, sharing its
// ownership label and its ResourceQuota.
func TestSyncUserRefusesPlacementNamespaceCollision(t *testing.T) {
	for name, claimed := range map[string]string{
		"separator": "alice_smith",
		"case":      "ALICE.SMITH",
		"accent":    "álice.smith",
		"spacing":   "Alice Smith",
	} {
		t.Run(name, func(t *testing.T) {
			svc, users := newOIDCFixture(t, config.OIDCConfig{}, []model.User{
				{ID: "u-alice", Username: "alice.smith", OIDCSubject: "sub-alice"},
			})
			_, err := svc.syncUser(context.Background(), "sub-newcomer", oidcIdentity{Username: claimed}, "1.2.3.4")
			unauthorized(t, err)
			if _, err := users.FindByUsername(context.Background(), claimed); !errors.Is(err, repository.ErrUserNotFound) {
				t.Fatalf("the colliding account must not be provisioned, got %v", err)
			}
		})
	}
}

// Usernames that leave no DNS-1123 character all sanitize to "x", but
// they resolve through the account id, so they never collide and must
// never be refused — a Cyrillic or CJK directory would otherwise get
// exactly one working account.
func TestSyncUserProvisionsUnrepresentableUsernames(t *testing.T) {
	svc, users := newOIDCFixture(t, config.OIDCConfig{}, nil)
	seen := map[string]bool{}
	for _, claimed := range []string{"иван", "王五", "Ωμέγα", "علي"} {
		user, err := svc.syncUser(context.Background(), "sub-"+claimed, oidcIdentity{Username: claimed}, "1.2.3.4")
		if err != nil {
			t.Fatalf("provisioning %q must be allowed: %v", claimed, err)
		}
		ns := naming.PersonalNamespace(user.Username, user.ID)
		if ns == "" || seen[ns] {
			t.Fatalf("account %q got a shared or unresolvable namespace: %q", claimed, ns)
		}
		seen[ns] = true
	}
	if _, _, err := users.List(context.Background(), 1, 10); err != nil {
		t.Fatalf("listing users: %v", err)
	}
}

// The guard must not fire on names that merely look alike: only an
// IDENTICAL projection is a collision.
func TestSyncUserProvisionsDistinctNormalizations(t *testing.T) {
	svc, _ := newOIDCFixture(t, config.OIDCConfig{}, []model.User{
		{ID: "u-alice", Username: "alice.smith", OIDCSubject: "sub-alice"},
	})
	for _, claimed := range []string{"alice.smith2", "alicesmith", "alice.smyth", "bob"} {
		if _, err := svc.syncUser(context.Background(), "sub-"+claimed, oidcIdentity{Username: claimed}, "1.2.3.4"); err != nil {
			t.Fatalf("provisioning %q must be allowed: %v", claimed, err)
		}
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
	// The promotion must be persisted, not just returned: role is written
	// through its targeted setter, and an in-memory-only promotion would
	// mint admin claims that vetBearer rejects on the very next request.
	if stored.Role != auth.RoleAdmin {
		t.Fatalf("IdP-driven promotion must reach the row, got %s", stored.Role)
	}
}
