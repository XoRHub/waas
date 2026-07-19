package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
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

func forbidden(t *testing.T, err error) {
	t.Helper()
	var p *apierror.Problem
	if !errors.As(err, &p) || p.Status != 403 {
		t.Fatalf("want 403 Problem, got %v", err)
	}
}

func strptr(s string) *string { return &s }

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
