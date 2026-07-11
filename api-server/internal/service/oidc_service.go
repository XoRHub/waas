package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// OIDCService implements SSO login against any OIDC IdP (Authentik,
// Keycloak, Okta, Zitadel…). It exists NEXT TO local auth by default:
// local login stays available for the bootstrap admin and as break-glass
// when the IdP is down. The one documented, opt-in exception is
// WAAS_LOGIN_OIDC_ONLY (config.OIDCConfig.OIDCOnly), which disables local
// login for everyone — bootstrap admin included; the break-glass then is
// redeploying without the flag (see
// docs/studies/18-prompt-feature14-oidc-only-login.md).
//
// The one non-negotiable job of this service is the group mirror: at every
// SSO login, the IdP's groups claim overwrites users.user_groups, which is
// what WorkspacePolicy subject matching runs on. Stale groups here were the
// root cause of "policy priorities don't work" — an empty mirror silently
// downgrades everyone to the subjects-less default policy.
type OIDCService struct {
	cfg   config.OIDCConfig
	users repository.UserRepository
	audit *AuditService

	signer         *auth.Signer
	issuer         string
	accessTokenTTL time.Duration

	// Discovery is lazy so the api-server still boots (and local login
	// still works) when the IdP is unreachable.
	mu       sync.Mutex
	provider *oidc.Provider
}

// NewOIDCService wires SSO login. Callers must check cfg.Enabled() first.
func NewOIDCService(cfg config.OIDCConfig, users repository.UserRepository, audit *AuditService,
	signer *auth.Signer, issuer string, accessTokenTTL time.Duration) *OIDCService {
	return &OIDCService{
		cfg: cfg, users: users, audit: audit,
		signer: signer, issuer: issuer, accessTokenTTL: accessTokenTTL,
	}
}

// ensureProvider performs (or reuses) OIDC discovery.
func (s *OIDCService) ensureProvider(ctx context.Context) (*oidc.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, nil
	}
	provider, err := oidc.NewProvider(ctx, s.cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery against %s: %w", s.cfg.IssuerURL, err)
	}
	s.provider = provider
	return provider, nil
}

func (s *OIDCService) oauthConfig(provider *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		RedirectURL:  s.cfg.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       s.cfg.Scopes,
	}
}

// AuthURL builds the authorization redirect and the state to pin in a
// browser cookie.
func (s *OIDCService) AuthURL(ctx context.Context) (authURL, state string, err error) {
	provider, err := s.ensureProvider(ctx)
	if err != nil {
		return "", "", apierror.Unavailable("SSO provider is unreachable")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generating state: %w", err)
	}
	state = base64.RawURLEncoding.EncodeToString(raw)
	return s.oauthConfig(provider).AuthCodeURL(state), state, nil
}

// Callback exchanges the authorization code, verifies the ID token, syncs
// the local account (groups above all) and issues a platform access token.
func (s *OIDCService) Callback(ctx context.Context, code, clientIP string) (*LoginResult, error) {
	provider, err := s.ensureProvider(ctx)
	if err != nil {
		return nil, apierror.Unavailable("SSO provider is unreachable")
	}
	token, err := s.oauthConfig(provider).Exchange(ctx, code)
	if err != nil {
		return nil, apierror.Unauthorized("SSO code exchange failed")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, apierror.Unauthorized("SSO provider returned no id_token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return nil, apierror.Unauthorized("SSO id_token verification failed")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decoding id_token claims: %w", err)
	}
	identity := s.identityFromClaims(idToken.Subject, claims)
	if identity.Username == "" {
		return nil, apierror.Unauthorized(fmt.Sprintf("id_token carries no usable %q claim", s.cfg.UsernameClaim))
	}

	user, err := s.syncUser(ctx, identity, clientIP)
	if err != nil {
		return nil, err
	}

	accessClaims := auth.NewAccessClaims(s.issuer, user.ID, user.Role, s.accessTokenTTL)
	accessToken, err := s.signer.Sign(accessClaims)
	if err != nil {
		return nil, fmt.Errorf("issuing access token for %s: %w", user.Username, err)
	}
	return &LoginResult{AccessToken: accessToken, ExpiresAt: accessClaims.ExpiresAt.Time, User: user}, nil
}

// oidcIdentity is what the platform consumes from the IdP.
type oidcIdentity struct {
	Username    string
	Email       string
	DisplayName string
	Groups      []string
}

func (s *OIDCService) identityFromClaims(subject string, claims map[string]any) oidcIdentity {
	id := oidcIdentity{}
	if v, ok := claims[s.cfg.UsernameClaim].(string); ok && v != "" {
		id.Username = v
	} else if subject != "" {
		id.Username = subject
	}
	if v, ok := claims["email"].(string); ok {
		id.Email = v
	}
	if v, ok := claims["name"].(string); ok {
		id.DisplayName = v
	}
	// Group claims arrive as []any of strings (JSON array).
	if raw, ok := claims[s.cfg.GroupsClaim].([]any); ok {
		for _, g := range raw {
			if name, ok := g.(string); ok && name != "" {
				id.Groups = append(id.Groups, name)
			}
		}
	}
	return id
}

// syncUser provisions the account on first SSO login and refreshes the
// IdP-owned fields (groups, email, role when AdminGroups is configured) on
// every subsequent one.
func (s *OIDCService) syncUser(ctx context.Context, id oidcIdentity, clientIP string) (*model.User, error) {
	now := time.Now().UTC()
	role := auth.RoleUser
	if s.adminByGroups(id.Groups) {
		role = auth.RoleAdmin
	}

	user, err := s.users.FindByUsername(ctx, id.Username)
	switch {
	case errors.Is(err, repository.ErrUserNotFound):
		user = &model.User{
			ID:       uuid.NewString(),
			Username: id.Username,
			Email:    id.Email,
			// No password hash: SSO-only account, local login refuses it.
			Role:          role,
			Active:        true,
			MaxWorkspaces: defaultMaxWorkspaces,
			Groups:        id.Groups,
			DisplayName:   id.DisplayName,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.users.Create(ctx, user); err != nil {
			return nil, fmt.Errorf("provisioning SSO user %s: %w", id.Username, err)
		}
		s.audit.Record(ctx, Actor{ID: user.ID, Username: user.Username, ClientIP: clientIP},
			"user.provisioned_oidc", "user", user.ID, "username="+user.Username)
	case err != nil:
		return nil, fmt.Errorf("looking up SSO user %s: %w", id.Username, err)
	default:
		if !user.Active {
			s.audit.Record(ctx, Actor{Username: id.Username, ClientIP: clientIP}, "user.login_failed", "user", user.ID, "provider=oidc inactive")
			return nil, apierror.Unauthorized("account is deactivated")
		}
		// IdP-owned fields: the mirror refreshes at every login.
		user.Groups = id.Groups
		if id.Email != "" {
			user.Email = id.Email
		}
		if user.DisplayName == "" && id.DisplayName != "" {
			user.DisplayName = id.DisplayName
		}
		if len(s.cfg.AdminGroups) > 0 {
			user.Role = role
		}
		user.UpdatedAt = now
	}

	user.LastLoginAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("recording SSO login for %s: %w", id.Username, err)
	}
	s.audit.Record(ctx, Actor{ID: user.ID, Username: user.Username, ClientIP: clientIP},
		"user.logged_in", "user", user.ID, "provider=oidc")
	return user, nil
}

func (s *OIDCService) adminByGroups(groups []string) bool {
	for _, g := range s.cfg.AdminGroups {
		if slices.Contains(groups, g) {
			return true
		}
	}
	return false
}
