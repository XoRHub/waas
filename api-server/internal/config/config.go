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
}

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

func durationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
