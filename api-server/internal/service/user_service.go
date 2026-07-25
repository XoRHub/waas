package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

const defaultMaxWorkspaces = 3

// UserService manages platform accounts.
type UserService struct {
	users repository.UserRepository
	audit *AuditService
}

func NewUserService(users repository.UserRepository, audit *AuditService) *UserService {
	return &UserService{users: users, audit: audit}
}

// CreateUserInput is the admin-facing account creation payload.
type CreateUserInput struct {
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Password      string    `json:"password"`
	Role          auth.Role `json:"role"`
	MaxWorkspaces int       `json:"maxWorkspaces"`
	// Groups seeds the IdP group mirror at creation (drives policy
	// matching). Overwritten by the IdP claim at the first OIDC login when
	// SSO is enabled; empty = only subjects-less policies match (the
	// "default" policy).
	Groups []string `json:"groups"`
}

// UpdateUserInput carries optional field updates (nil = unchanged).
type UpdateUserInput struct {
	Email         *string    `json:"email"`
	Password      *string    `json:"password"`
	Role          *auth.Role `json:"role"`
	Active        *bool      `json:"active"`
	MaxWorkspaces *int       `json:"maxWorkspaces"`
	// Groups overrides the IdP group mirror. Temporary admin
	// affordance until OIDC login syncs it from the IdP.
	Groups *[]string `json:"groups"`
}

func (s *UserService) Create(ctx context.Context, actor Actor, in CreateUserInput) (*model.User, error) {
	if in.Username == "" || in.Password == "" {
		return nil, apierror.BadRequest("username and password are required")
	}
	if in.Role == "" {
		in.Role = auth.RoleUser
	}
	if in.Role != auth.RoleAdmin && in.Role != auth.RoleUser {
		return nil, apierror.BadRequest("role must be admin or user")
	}
	if in.MaxWorkspaces <= 0 {
		in.MaxWorkspaces = defaultMaxWorkspaces
	}

	// Before argon2id, so a rejected creation costs nothing.
	conflict, err := placementUsernameConflict(ctx, s.users, in.Username)
	if err != nil {
		return nil, err
	}
	if conflict != nil {
		return nil, apierror.Conflict(fmt.Sprintf(
			"username %q collides with the existing account %q: both resolve to the personal namespace %q — pick a username that differs by more than case, accents or separators",
			in.Username, conflict.Username, conflict.Namespace))
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}
	now := time.Now().UTC()
	user := &model.User{
		ID:            uuid.NewString(),
		Username:      in.Username,
		Email:         in.Email,
		PasswordHash:  hash,
		Role:          in.Role,
		Active:        true,
		MaxWorkspaces: in.MaxWorkspaces,
		Groups:        normalizeGroups(in.Groups),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierror.Conflict(fmt.Sprintf("username %q is already taken", in.Username))
		}
		return nil, err
	}
	detail := "username=" + user.Username
	if len(user.Groups) > 0 {
		detail += " groups=" + strings.Join(user.Groups, ",")
	}
	s.audit.Record(ctx, actor, "user.created", "user", user.ID, detail)
	return user, nil
}

// normalizeGroups trims, de-dups and drops blanks from a group list.
func normalizeGroups(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

func (s *UserService) Get(ctx context.Context, id string) (*model.User, error) {
	user, err := s.users.FindByID(ctx, id)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, apierror.NotFound("user not found")
	}
	return user, err
}

func (s *UserService) List(ctx context.Context, page, pageSize int) ([]model.User, int, error) {
	return s.users.List(ctx, page, pageSize)
}

// Update applies an admin edit. The bool reports whether the edit
// REVOKED the account's tokens — the caller needs it when an admin does
// this to their OWN account: their session dies with the edit, and the
// handler has to expire the cookie that carried it.
func (s *UserService) Update(ctx context.Context, actor Actor, id string, in UpdateUserInput) (*model.User, bool, error) {
	user, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	// revoke: a security-relevant change (deactivation, role change,
	// password reset) bounds the validity of every outstanding token to
	// now. Written by SetTokensValidAfter AFTER the row update, never as
	// part of it — see the Update doc in the repository: a full-row write
	// carries a copy read before this call and would clobber a concurrent
	// revocation.
	revoke := false
	// Captured before the fields are applied: the last-admin guard below
	// compares this account's state before and after the edit.
	wasActiveAdmin := user.Role == auth.RoleAdmin && user.Active
	if in.Email != nil {
		user.Email = *in.Email
	}
	if in.Password != nil {
		hash, err := HashPassword(*in.Password)
		if err != nil {
			return nil, false, fmt.Errorf("hashing password: %w", err)
		}
		user.PasswordHash = hash
		revoke = true
	}
	if in.Role != nil {
		if *in.Role != auth.RoleAdmin && *in.Role != auth.RoleUser {
			return nil, false, apierror.BadRequest("role must be admin or user")
		}
		revoke = revoke || *in.Role != user.Role
		user.Role = *in.Role
	}
	if in.Active != nil {
		revoke = revoke || (user.Active && !*in.Active)
		user.Active = *in.Active
	}
	// The platform must keep an administrator. Demoting or deactivating
	// the last active one locks everyone out of governance with no
	// in-product way back — it would take a database edit or a redeploy
	// against an empty database. Compared as two states rather than
	// per-field, so one request touching both role and active is judged
	// on what it actually leaves behind.
	stillActiveAdmin := user.Role == auth.RoleAdmin && user.Active
	if wasActiveAdmin && !stillActiveAdmin {
		admins, err := s.users.CountActiveAdmins(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("counting active admins: %w", err)
		}
		// This account is still counted (the write has not landed yet), so
		// 1 means it is the only one left.
		if admins <= 1 {
			return nil, false, apierror.BadRequest(
				"the platform must keep at least one active administrator — promote another account first")
		}
	}
	if in.MaxWorkspaces != nil {
		user.MaxWorkspaces = *in.MaxWorkspaces
	}
	if in.Groups != nil {
		user.Groups = *in.Groups
	}
	user.UpdatedAt = time.Now().UTC()
	if err := s.users.Update(ctx, user); err != nil {
		return nil, false, err
	}
	// role/active live outside the full-row Update (they are what
	// per-request revocation reads — see the repository's Update doc);
	// the admin's change is written targeted, and only when the request
	// actually carried the field, so a read-modify-write here can never
	// clobber a concurrent edit of either.
	if in.Role != nil {
		if err := s.users.SetRole(ctx, user.ID, *in.Role); err != nil {
			return nil, false, fmt.Errorf("setting role for %s: %w", user.ID, err)
		}
	}
	if in.Active != nil {
		if err := s.users.SetActive(ctx, user.ID, *in.Active); err != nil {
			return nil, false, fmt.Errorf("setting activation for %s: %w", user.ID, err)
		}
	}
	if revoke {
		now := user.UpdatedAt
		if err := s.users.SetTokensValidAfter(ctx, user.ID, now); err != nil {
			return nil, false, fmt.Errorf("revoking tokens for %s: %w", user.ID, err)
		}
		user.TokensValidAfter = &now
	}
	s.audit.Record(ctx, actor, "user.updated", "user", user.ID, "")
	return user, revoke, nil
}

// UpdateProfileInput is the self-service subset of a user record (nil =
// unchanged). Username, role, groups and quotas stay admin/OIDC-owned.
type UpdateProfileInput struct {
	DisplayName *string                `json:"displayName"`
	Email       *string                `json:"email"`
	Preferences *model.UserPreferences `json:"preferences"`
	// Password change requires proving knowledge of the current one.
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// UpdateProfile lets the authenticated user edit their own profile.
func (s *UserService) UpdateProfile(ctx context.Context, actor Actor, in UpdateProfileInput) (*model.User, error) {
	user, err := s.Get(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	// IdP-owned account: identity and credentials are managed by the
	// identity provider — only preferences are self-service.
	if user.OIDCSubject != "" {
		if in.DisplayName != nil || in.Email != nil {
			return nil, apierror.Forbidden("identity fields are managed by your identity provider")
		}
		if in.NewPassword != "" || in.CurrentPassword != "" {
			return nil, apierror.Forbidden("password sign-in is managed by your identity provider")
		}
	}
	if in.DisplayName != nil {
		user.DisplayName = *in.DisplayName
	}
	if in.Email != nil {
		user.Email = *in.Email
	}
	if in.Preferences != nil {
		user.Preferences = *in.Preferences
	}
	if in.NewPassword != "" {
		ok, err := VerifyPassword(in.CurrentPassword, user.PasswordHash)
		if err != nil {
			return nil, fmt.Errorf("verifying current password: %w", err)
		}
		if !ok {
			return nil, apierror.BadRequest("current password is incorrect")
		}
		hash, err := HashPassword(in.NewPassword)
		if err != nil {
			return nil, fmt.Errorf("hashing password: %w", err)
		}
		user.PasswordHash = hash
	}
	user.UpdatedAt = time.Now().UTC()
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	if in.NewPassword != "" {
		// A credential change ends every session, the current device
		// included: after a password change the only sound assumption is
		// that the old credential may have been compromised. Revoked after
		// the row write, never within it (see the repository's Update doc).
		now := user.UpdatedAt
		if err := s.users.SetTokensValidAfter(ctx, user.ID, now); err != nil {
			return nil, fmt.Errorf("revoking tokens for %s: %w", user.ID, err)
		}
		user.TokensValidAfter = &now
	}
	s.audit.Record(ctx, actor, "user.profile_updated", "user", user.ID, "")
	return user, nil
}

func (s *UserService) Delete(ctx context.Context, actor Actor, id string) error {
	if actor.ID == id {
		return apierror.BadRequest("you cannot delete your own account")
	}
	if err := s.users.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return apierror.NotFound("user not found")
		}
		return err
	}
	s.audit.Record(ctx, actor, "user.deleted", "user", id, "")
	return nil
}

// EnsureBootstrapAdmin creates the initial admin account on an empty
// database. If no password is configured, one is generated and logged once —
// zero external dependency to get started.
func (s *UserService) EnsureBootstrapAdmin(ctx context.Context, username, password string) error {
	count, err := s.users.Count(ctx)
	if err != nil {
		return fmt.Errorf("checking for existing users: %w", err)
	}
	if count > 0 {
		return nil
	}

	generated := false
	if password == "" {
		raw := make([]byte, 18)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generating admin password: %w", err)
		}
		password = base64.RawURLEncoding.EncodeToString(raw)
		generated = true
	}
	if _, err := s.Create(ctx, Actor{Username: "system"}, CreateUserInput{
		Username: username,
		Password: password,
		Role:     auth.RoleAdmin,
	}); err != nil {
		return fmt.Errorf("creating bootstrap admin: %w", err)
	}
	if generated {
		slog.Warn("bootstrap admin created with a generated password — change it immediately",
			"username", username, "password", password)
	} else {
		slog.Info("bootstrap admin created", "username", username)
	}
	return nil
}
