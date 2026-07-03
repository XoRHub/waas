package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
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
}

// UpdateUserInput carries optional field updates (nil = unchanged).
type UpdateUserInput struct {
	Email         *string    `json:"email"`
	Password      *string    `json:"password"`
	Role          *auth.Role `json:"role"`
	Active        *bool      `json:"active"`
	MaxWorkspaces *int       `json:"maxWorkspaces"`
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
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierror.Conflict(fmt.Sprintf("username %q is already taken", in.Username))
		}
		return nil, err
	}
	s.audit.Record(ctx, actor, "user.created", "user", user.ID, "username="+user.Username)
	return user, nil
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

func (s *UserService) Update(ctx context.Context, actor Actor, id string, in UpdateUserInput) (*model.User, error) {
	user, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Email != nil {
		user.Email = *in.Email
	}
	if in.Password != nil {
		hash, err := HashPassword(*in.Password)
		if err != nil {
			return nil, fmt.Errorf("hashing password: %w", err)
		}
		user.PasswordHash = hash
	}
	if in.Role != nil {
		if *in.Role != auth.RoleAdmin && *in.Role != auth.RoleUser {
			return nil, apierror.BadRequest("role must be admin or user")
		}
		user.Role = *in.Role
	}
	if in.Active != nil {
		user.Active = *in.Active
	}
	if in.MaxWorkspaces != nil {
		user.MaxWorkspaces = *in.MaxWorkspaces
	}
	user.UpdatedAt = time.Now().UTC()
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actor, "user.updated", "user", user.ID, "")
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
