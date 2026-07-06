// Package config loads the API server configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	// ListenAddr is the HTTP(S) bind address.
	ListenAddr string
	// DatabaseURL selects the store: postgres://… in production, a plain
	// file path (SQLite, pure-Go driver) in dev mode only.
	DatabaseURL string
	// DevMode swaps Postgres for SQLite and the real cluster for an
	// in-memory fake, so the API can run on a laptop with zero deps.
	DevMode bool
	// WorkspaceNamespace is where Workspace/WorkspaceTemplate CRs live.
	WorkspaceNamespace string

	JWTIssuer          string
	JWTPrivateKeyPath  string
	AccessTokenTTL     time.Duration
	ConnectionTokenTTL time.Duration

	// InternalToken authenticates service-to-service calls from the
	// WebSocket proxy (mounted from the same Secret in both pods).
	InternalToken string

	// Bootstrap admin, created on first boot when the users table is empty.
	AdminUsername string
	AdminPassword string

	TLSCertFile string
	TLSKeyFile  string

	// CORSAllowedOrigins is only needed in dev (Vite on another port).
	CORSAllowedOrigins []string

	// IdleSweepInterval is how often the idle sweeper looks for
	// workspaces to auto-pause (policy lifecycle.idleSuspendAfter).
	// Zero disables the sweeper.
	IdleSweepInterval time.Duration

	// OIDC configures the optional SSO login (Authentik or any OIDC
	// provider). Enabled when IssuerURL and ClientID are both set; local
	// username/password login always remains available (bootstrap admin,
	// break-glass when the IdP is down).
	OIDC OIDCConfig

	// WoL configures the Wake-on-LAN relay used to power on remote
	// workspaces. Disabled when RelayURL is empty.
	WoL WoLConfig
}

// WoLConfig points at an external relay that emits the magic packet on
// the target's L2 network (a K8s pod cannot broadcast onto the office
// LAN). See docs/remote-workspaces.md for the network contract.
type WoLConfig struct {
	// RelayURL is the relay's HTTP endpoint. The api-server POSTs
	// {"mac": "..."} to it; the relay broadcasts the magic packet.
	RelayURL string
	// AuthToken is sent as a Bearer token to the relay when set.
	AuthToken string
}

// Enabled reports whether a WoL relay is configured.
func (w WoLConfig) Enabled() bool { return w.RelayURL != "" }

// OIDCConfig is the SSO provider wiring.
type OIDCConfig struct {
	// IssuerURL is the OIDC discovery issuer, e.g.
	// https://authentik.example.com/application/o/waas/
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// RedirectURL is this api-server's public callback URL, e.g.
	// https://waas.example.com/api/v1/auth/oidc/callback
	RedirectURL string
	// Scopes requested at authorization. Authentik ships the groups claim
	// in its default profile scope mapping.
	Scopes []string
	// UsernameClaim maps the IdP identity to the platform username.
	UsernameClaim string
	// GroupsClaim carries the Authentik group names mirrored into
	// users.user_groups at every login (drives WorkspacePolicy matching).
	GroupsClaim string
	// AdminGroups grant the platform admin role; when set, role is synced
	// at every login (membership lost = demoted). Empty = roles stay
	// admin-managed.
	AdminGroups []string
	// ProviderName labels the SSO button in the login page.
	ProviderName string
	// FrontendURL is where the browser lands after the callback (the SPA
	// origin). Default "/" works when the frontend shares the origin.
	FrontendURL string
}

// Enabled reports whether SSO login is configured.
func (o OIDCConfig) Enabled() bool { return o.IssuerURL != "" && o.ClientID != "" }

// Load reads configuration from WAAS_* environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:         envOr("WAAS_LISTEN_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("WAAS_DATABASE_URL"),
		DevMode:            os.Getenv("WAAS_DEV") == "true",
		WorkspaceNamespace: envOr("WAAS_WORKSPACE_NAMESPACE", "waas-workspaces"),
		JWTIssuer:          envOr("WAAS_JWT_ISSUER", "waas"),
		JWTPrivateKeyPath:  os.Getenv("WAAS_JWT_PRIVATE_KEY_FILE"),
		AccessTokenTTL:     durationOr("WAAS_ACCESS_TOKEN_TTL", 8*time.Hour),
		ConnectionTokenTTL: durationOr("WAAS_CONNECTION_TOKEN_TTL", 5*time.Minute),
		InternalToken:      os.Getenv("WAAS_INTERNAL_TOKEN"),
		AdminUsername:      envOr("WAAS_ADMIN_USERNAME", "admin"),
		AdminPassword:      os.Getenv("WAAS_ADMIN_PASSWORD"),
		TLSCertFile:        os.Getenv("WAAS_TLS_CERT_FILE"),
		TLSKeyFile:         os.Getenv("WAAS_TLS_KEY_FILE"),
		IdleSweepInterval:  durationOr("WAAS_IDLE_SWEEP_INTERVAL", 5*time.Minute),
	}
	if origins := os.Getenv("WAAS_CORS_ALLOWED_ORIGINS"); origins != "" {
		cfg.CORSAllowedOrigins = strings.Split(origins, ",")
	}

	cfg.OIDC = OIDCConfig{
		IssuerURL:     os.Getenv("WAAS_OIDC_ISSUER"),
		ClientID:      os.Getenv("WAAS_OIDC_CLIENT_ID"),
		ClientSecret:  os.Getenv("WAAS_OIDC_CLIENT_SECRET"),
		RedirectURL:   os.Getenv("WAAS_OIDC_REDIRECT_URL"),
		Scopes:        splitList(envOr("WAAS_OIDC_SCOPES", "openid,profile,email")),
		UsernameClaim: envOr("WAAS_OIDC_USERNAME_CLAIM", "preferred_username"),
		GroupsClaim:   envOr("WAAS_OIDC_GROUPS_CLAIM", "groups"),
		AdminGroups:   splitList(os.Getenv("WAAS_OIDC_ADMIN_GROUPS")),
		ProviderName:  envOr("WAAS_OIDC_PROVIDER_NAME", "SSO"),
		FrontendURL:   envOr("WAAS_OIDC_FRONTEND_URL", "/"),
	}
	cfg.WoL = WoLConfig{
		RelayURL:  os.Getenv("WAAS_WOL_RELAY_URL"),
		AuthToken: os.Getenv("WAAS_WOL_RELAY_TOKEN"),
	}

	if cfg.OIDC.Enabled() {
		if cfg.OIDC.ClientSecret == "" {
			return nil, fmt.Errorf("WAAS_OIDC_CLIENT_SECRET is required when OIDC is enabled")
		}
		if cfg.OIDC.RedirectURL == "" {
			return nil, fmt.Errorf("WAAS_OIDC_REDIRECT_URL is required when OIDC is enabled")
		}
	}

	if cfg.DatabaseURL == "" {
		if !cfg.DevMode {
			return nil, fmt.Errorf("WAAS_DATABASE_URL is required outside dev mode")
		}
		cfg.DatabaseURL = "waas-dev.db"
	}
	if cfg.InternalToken == "" && !cfg.DevMode {
		return nil, fmt.Errorf("WAAS_INTERNAL_TOKEN is required outside dev mode")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitList parses a comma-separated list, trimming blanks.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func durationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
