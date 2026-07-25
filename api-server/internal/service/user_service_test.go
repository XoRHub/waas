package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/shared/auth"
)

// newUserFixture wires a UserService against SQLite, seeded with the given
// users — same shape as newOIDCFixture, for profile self-service tests.
func newUserFixture(t *testing.T, seed []model.User) (*UserService, repository.UserRepository) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "users.db"))
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
	return NewUserService(users, NewAuditService(repository.NewSQLAuditRepository(db))), users
}

// Two usernames the database accepts as distinct, but which resolve to
// one placement namespace, must not coexist: the second account would
// silently land in the first one's namespace, quota included. Refused at
// creation, where the admin can still pick another name.
func TestCreateRefusesPlacementNamespaceCollision(t *testing.T) {
	conflict := func(t *testing.T, err error) {
		t.Helper()
		var p *apierror.Problem
		if !errors.As(err, &p) || p.Status != 409 {
			t.Fatalf("want 409 Problem, got %v", err)
		}
	}
	for name, username := range map[string]string{
		"separator": "alice_smith",
		"case":      "ALICE.SMITH",
		"accent":    "álice.smith",
		"spacing":   "Alice Smith",
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newUserFixture(t, []model.User{{ID: "u-alice", Username: "alice.smith", PasswordHash: "argon2:x"}})
			_, err := svc.Create(context.Background(), Actor{Username: "admin"},
				CreateUserInput{Username: username, Password: "s3cret-password"})
			conflict(t, err)
		})
	}

	// An EXACT duplicate keeps its own, plainer message: the UNIQUE
	// constraint is what rejects it, not this guard.
	t.Run("exact duplicate keeps the taken message", func(t *testing.T) {
		svc, _ := newUserFixture(t, []model.User{{ID: "u-alice", Username: "alice.smith", PasswordHash: "argon2:x"}})
		_, err := svc.Create(context.Background(), Actor{Username: "admin"},
			CreateUserInput{Username: "alice.smith", Password: "s3cret-password"})
		conflict(t, err)
		if !strings.Contains(err.Error(), "already taken") {
			t.Fatalf("want the duplicate-username message, got %v", err)
		}
	})

	// Above the token budget the resolution truncates and appends a hash
	// of the raw value, so two usernames sharing a sanitized form land in
	// DIFFERENT namespaces. Comparing sanitized forms would refuse this
	// pair for a collision that does not exist.
	t.Run("same sanitized form, different namespaces", func(t *testing.T) {
		long := strings.Repeat("a-b-c-d-e-f-g-h-i-j-", 3)[:59]
		other := strings.ReplaceAll(long, "-", "_")
		if naming.Sanitize(long) != naming.Sanitize(other) {
			t.Fatalf("fixture no longer shares a sanitized form: %q vs %q", long, other)
		}
		if naming.PersonalNamespace(long, "") == naming.PersonalNamespace(other, "") {
			t.Fatalf("fixture no longer resolves apart: %q", naming.PersonalNamespace(long, ""))
		}
		svc, _ := newUserFixture(t, []model.User{{ID: "u-long", Username: long, PasswordHash: "argon2:x"}})
		if _, err := svc.Create(context.Background(), Actor{Username: "admin"},
			CreateUserInput{Username: other, Password: "s3cret-password"}); err != nil {
			t.Fatalf("distinct namespaces must not be refused: %v", err)
		}
	})

	t.Run("distinct normalizations stay allowed", func(t *testing.T) {
		svc, _ := newUserFixture(t, []model.User{{ID: "u-alice", Username: "alice.smith", PasswordHash: "argon2:x"}})
		for _, username := range []string{"alice.smith2", "alicesmith", "bob"} {
			if _, err := svc.Create(context.Background(), Actor{Username: "admin"},
				CreateUserInput{Username: username, Password: "s3cret-password"}); err != nil {
				t.Fatalf("creating %q must be allowed: %v", username, err)
			}
		}
	})
}

func forbidden(t *testing.T, err error) {
	t.Helper()
	var p *apierror.Problem
	if !errors.As(err, &p) || p.Status != 403 {
		t.Fatalf("want 403 Problem, got %v", err)
	}
}

func strptr(s string) *string { return &s }

// Security-relevant admin edits must revoke outstanding tokens; benign
// ones must not (a quota bump must never log the user out everywhere).
func TestAdminUpdateRevokesTokensOnlyOnSecurityChanges(t *testing.T) {
	boolptr := func(b bool) *bool { return &b }
	roleptr := func(r auth.Role) *auth.Role { return &r }
	intptr := func(i int) *int { return &i }

	for name, tc := range map[string]struct {
		in         UpdateUserInput
		wantRevoke bool
	}{
		"deactivation":       {UpdateUserInput{Active: boolptr(false)}, true},
		"role change":        {UpdateUserInput{Role: roleptr(auth.RoleAdmin)}, true},
		"password reset":     {UpdateUserInput{Password: strptr("new-password")}, true},
		"same role":          {UpdateUserInput{Role: roleptr(auth.RoleUser)}, false},
		"still active":       {UpdateUserInput{Active: boolptr(true)}, false},
		"email only":         {UpdateUserInput{Email: strptr("new@example.com")}, false},
		"quota only":         {UpdateUserInput{MaxWorkspaces: intptr(5)}, false},
		"groups only":        {UpdateUserInput{Groups: &[]string{"dev"}}, false},
		"reactivate account": {UpdateUserInput{Active: boolptr(true), Email: strptr("x@y")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			svc, users := newUserFixture(t, []model.User{{ID: "u-bob", Username: "bob"}})
			if _, err := svc.Update(context.Background(), Actor{ID: "u-admin"}, "u-bob", tc.in); err != nil {
				t.Fatalf("update: %v", err)
			}
			stored, _ := users.FindByID(context.Background(), "u-bob")
			if got := stored.TokensValidAfter != nil; got != tc.wantRevoke {
				t.Fatalf("revocation stamp: want %v, got bound %v", tc.wantRevoke, stored.TokensValidAfter)
			}
		})
	}
}

func TestUpdateProfilePasswordChangeRevokesTokens(t *testing.T) {
	hash, err := HashPassword("old-password")
	if err != nil {
		t.Fatalf("hashing seed password: %v", err)
	}
	svc, users := newUserFixture(t, []model.User{{ID: "u-bob", Username: "bob", PasswordHash: hash}})

	// Preference edits must not end sessions.
	in := UpdateProfileInput{Preferences: &model.UserPreferences{Theme: "dark"}}
	if _, err := svc.UpdateProfile(context.Background(), Actor{ID: "u-bob"}, in); err != nil {
		t.Fatalf("preferences update: %v", err)
	}
	stored, _ := users.FindByID(context.Background(), "u-bob")
	if stored.TokensValidAfter != nil {
		t.Fatalf("a preference edit must not revoke tokens, got %v", stored.TokensValidAfter)
	}

	in = UpdateProfileInput{CurrentPassword: "old-password", NewPassword: "new-password"}
	if _, err := svc.UpdateProfile(context.Background(), Actor{ID: "u-bob"}, in); err != nil {
		t.Fatalf("password change: %v", err)
	}
	stored, _ = users.FindByID(context.Background(), "u-bob")
	if stored.TokensValidAfter == nil {
		t.Fatal("a password change must revoke every outstanding token")
	}
}

func TestUpdateProfileSSOAccountLocksIdentityAndPassword(t *testing.T) {
	// An IdP-owned account must not edit identity or set a local password:
	// identity gets overwritten at next SSO login, and the password path
	// used to die on the empty hash with an opaque error.
	for name, in := range map[string]UpdateProfileInput{
		"display name": {DisplayName: strptr("New Name")},
		"email":        {Email: strptr("new@example.com")},
		"password":     {CurrentPassword: "whatever", NewPassword: "supersecret"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, users := newUserFixture(t, []model.User{
				{ID: "u-alice", Username: "alice", Email: "alice@example.com", OIDCSubject: "sub-alice"},
			})
			_, err := svc.UpdateProfile(context.Background(), Actor{ID: "u-alice"}, in)
			forbidden(t, err)
			stored, _ := users.FindByID(context.Background(), "u-alice")
			if stored.Email != "alice@example.com" || stored.DisplayName != "" || stored.PasswordHash != "" {
				t.Fatalf("SSO account must stay untouched, got %+v", stored)
			}
		})
	}
}

func TestUpdateProfileSSOAccountKeepsPreferencesEditable(t *testing.T) {
	svc, users := newUserFixture(t, []model.User{
		{ID: "u-alice", Username: "alice", OIDCSubject: "sub-alice"},
	})
	in := UpdateProfileInput{Preferences: &model.UserPreferences{Theme: "dark"}}
	if _, err := svc.UpdateProfile(context.Background(), Actor{ID: "u-alice"}, in); err != nil {
		t.Fatalf("preferences must stay self-service for SSO accounts: %v", err)
	}
	if stored, _ := users.FindByID(context.Background(), "u-alice"); stored.Preferences.Theme != "dark" {
		t.Fatalf("preferences not persisted, got %+v", stored.Preferences)
	}
}

func TestUpdateProfileLocalAccountStillEditable(t *testing.T) {
	hash, err := HashPassword("old-password")
	if err != nil {
		t.Fatalf("hashing seed password: %v", err)
	}
	svc, users := newUserFixture(t, []model.User{
		{ID: "u-bob", Username: "bob", PasswordHash: hash},
	})
	in := UpdateProfileInput{
		DisplayName:     strptr("Bob"),
		Email:           strptr("bob@example.com"),
		CurrentPassword: "old-password",
		NewPassword:     "new-password",
	}
	if _, err := svc.UpdateProfile(context.Background(), Actor{ID: "u-bob"}, in); err != nil {
		t.Fatalf("local account update: %v", err)
	}
	stored, _ := users.FindByID(context.Background(), "u-bob")
	if stored.DisplayName != "Bob" || stored.Email != "bob@example.com" {
		t.Fatalf("identity not applied, got %+v", stored)
	}
	if ok, _ := VerifyPassword("new-password", stored.PasswordHash); !ok {
		t.Fatal("new password not applied")
	}

	_, err = svc.UpdateProfile(context.Background(), Actor{ID: "u-bob"},
		UpdateProfileInput{CurrentPassword: "wrong", NewPassword: "another-one"})
	var p *apierror.Problem
	if !errors.As(err, &p) || p.Status != 400 {
		t.Fatalf("wrong current password: want 400 Problem, got %v", err)
	}
}
