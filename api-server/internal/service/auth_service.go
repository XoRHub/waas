package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// AuthService implements local username/password authentication and access
// token issuance. OIDC providers plug in beside it later.
type AuthService struct {
	users  repository.UserRepository
	signer *auth.Signer
	audit  *AuditService

	issuer         string
	accessTokenTTL time.Duration
}

func NewAuthService(users repository.UserRepository, signer *auth.Signer, audit *AuditService, issuer string, accessTokenTTL time.Duration) *AuthService {
	return &AuthService{users: users, signer: signer, audit: audit, issuer: issuer, accessTokenTTL: accessTokenTTL}
}

// LoginResult is returned to the frontend after a successful login.
type LoginResult struct {
	AccessToken string      `json:"accessToken"`
	ExpiresAt   time.Time   `json:"expiresAt"`
	User        *model.User `json:"user"`
}

// Login verifies credentials and issues an access token.
func (s *AuthService) Login(ctx context.Context, username, password, clientIP string) (*LoginResult, error) {
	user, err := s.users.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apierror.Unauthorized("invalid credentials")
		}
		return nil, fmt.Errorf("looking up user %s: %w", username, err)
	}
	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("verifying password for %s: %w", username, err)
	}
	if !ok || !user.Active {
		s.audit.Record(ctx, Actor{Username: username, ClientIP: clientIP}, "user.login_failed", "user", user.ID, "")
		return nil, apierror.Unauthorized("invalid credentials")
	}

	claims := auth.NewAccessClaims(s.issuer, user.ID, user.Role, s.accessTokenTTL)
	token, err := s.signer.Sign(claims)
	if err != nil {
		return nil, fmt.Errorf("issuing access token for %s: %w", username, err)
	}

	now := time.Now().UTC()
	user.LastLoginAt = &now
	user.UpdatedAt = now
	if err := s.users.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("recording last login for %s: %w", username, err)
	}
	s.audit.Record(ctx, Actor{ID: user.ID, Username: user.Username, ClientIP: clientIP}, "user.logged_in", "user", user.ID, "")

	return &LoginResult{
		AccessToken: token,
		ExpiresAt:   claims.ExpiresAt.Time,
		User:        user,
	}, nil
}

// Me returns the authenticated user's profile.
func (s *AuthService) Me(ctx context.Context, userID string) (*model.User, error) {
	user, err := s.users.FindByID(ctx, userID)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, apierror.Unauthorized("account no longer exists")
	}
	return user, err
}
