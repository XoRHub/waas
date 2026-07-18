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
// redeploying without the flag (see docs/governance.md, "OIDC-only
// login").
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

// AuthRequest is one authorization round-trip's browser-side secrets: the
// handler pins them in a cookie and hands them back at the callback. All
// three are base64url (no separator collisions when packed).
type AuthRequest struct {
	URL      string // the IdP authorization redirect
	State    string // CSRF binding (RFC 6749 §10.12)
	Verifier string // PKCE code verifier (RFC 7636, S256)
	Nonce    string // id_token replay binding (OIDC Core §3.1.2.1)
}

// AuthURL builds the authorization redirect and the per-request secrets to
// pin in a browser cookie.
func (s *OIDCService) AuthURL(ctx context.Context) (*AuthRequest, error) {
	provider, err := s.ensureProvider(ctx)
	if err != nil {
		return nil, apierror.Unavailable("SSO provider is unreachable")
	}
	req := &AuthRequest{
		State:    randomToken(),
		Verifier: oauth2.GenerateVerifier(),
		Nonce:    randomToken(),
	}
	req.URL = s.oauthConfig(provider).AuthCodeURL(req.State,
		oauth2.S256ChallengeOption(req.Verifier), oidc.Nonce(req.Nonce))
	return req, nil
}

// randomToken returns 24 bytes of CSPRNG entropy, base64url-encoded.
func randomToken() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("reading crypto/rand: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Callback exchanges the authorization code (proving possession of the
// PKCE verifier), verifies the ID token against the pinned nonce, syncs
// the local account (groups above all) and issues a platform access token.
func (s *OIDCService) Callback(ctx context.Context, code, verifier, nonce, clientIP string) (*LoginResult, error) {
	provider, err := s.ensureProvider(ctx)
	if err != nil {
		return nil, apierror.Unavailable("SSO provider is unreachable")
	}
	token, err := s.oauthConfig(provider).Exchange(ctx, code, oauth2.VerifierOption(verifier))
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
	if idToken.Nonce != nonce {
		// The id_token was minted for a different authorization request
		// than the one this browser started (token injection).
		return nil, apierror.Unauthorized("SSO nonce mismatch — restart the login")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decoding id_token claims: %w", err)
	}
	if idToken.Subject == "" {
		// "sub" is REQUIRED by OIDC Core; without it there is no durable
		// identity to bind the account to.
		return nil, apierror.Unauthorized("id_token carries no subject")
	}
	identity := s.identityFromClaims(idToken.Subject, claims)
	if identity.Username == "" {
		return nil, apierror.Unauthorized(fmt.Sprintf("id_token carries no usable %q claim", s.cfg.UsernameClaim))
	}

	user, err := s.syncUser(ctx, idToken.Subject, identity, clientIP)
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
func (s *OIDCService) syncUser(ctx context.Context, subject string, id oidcIdentity, clientIP string) (*model.User, error) {
	now := time.Now().UTC()
	role := auth.RoleUser
	if s.adminByGroups(id.Groups) {
		role = auth.RoleAdmin
	}

	user, err := s.resolveUser(ctx, subject, id, clientIP)
	if err != nil {
		return nil, err
	}
	if user == nil {
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
			OIDCSubject:   subject,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.users.Create(ctx, user); err != nil {
			return nil, fmt.Errorf("provisioning SSO user %s: %w", id.Username, err)
		}
		s.audit.Record(ctx, Actor{ID: user.ID, Username: user.Username, ClientIP: clientIP},
			"user.provisioned_oidc", "user", user.ID, "username="+user.Username)
	} else {
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

// resolveUser maps the verified IdP identity to a platform account, or nil
// when a fresh one must be provisioned. The durable key is the OIDC
// subject — the username claim never selects an account: a collision with
// an existing username (local or bound to another subject) is treated as
// an attempted takeover — many IdPs let users pick their own username
// claim — and fails closed.
func (s *OIDCService) resolveUser(ctx context.Context, subject string, id oidcIdentity, clientIP string) (*model.User, error) {
	user, err := s.users.FindByOIDCSubject(ctx, subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, repository.ErrUserNotFound) {
		return nil, fmt.Errorf("looking up SSO subject: %w", err)
	}

	user, err = s.users.FindByUsername(ctx, id.Username)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("looking up SSO user %s: %w", id.Username, err)
	}

	s.audit.Record(ctx, Actor{Username: id.Username, ClientIP: clientIP}, "user.sso_link_conflict",
		"user", user.ID, "username claim collides with an existing account")
	return nil, apierror.Unauthorized("SSO login failed for this account — contact an administrator")
}

func (s *OIDCService) adminByGroups(groups []string) bool {
	for _, g := range s.cfg.AdminGroups {
		if slices.Contains(groups, g) {
			return true
		}
	}
	return false
}
