package config

import (
	"strings"
	"testing"
	"time"
)

// setBaseEnv makes Load() pass its unrelated required-var checks and pins
// every OIDC variable so a developer's shell can't leak into the test.
func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("WAAS_DEV", "true")
	for _, key := range []string{
		"WAAS_OIDC_ISSUER", "WAAS_OIDC_CLIENT_ID", "WAAS_OIDC_CLIENT_SECRET",
		"WAAS_OIDC_REDIRECT_URL", "WAAS_LOGIN_OIDC_ONLY",
	} {
		t.Setenv(key, "")
	}
}

// WAAS_LOGIN_OIDC_ONLY without a configured IdP must refuse to start:
// letting it through would lock every account out with no login path left.
func TestLoadOIDCOnlyRequiresOIDC(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("WAAS_LOGIN_OIDC_ONLY", "true")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted WAAS_LOGIN_OIDC_ONLY without OIDC configured")
	}
	if !strings.Contains(err.Error(), "WAAS_LOGIN_OIDC_ONLY") {
		t.Fatalf("error should name the offending variable, got: %v", err)
	}
}

func TestLoadOIDCOnlyWithOIDCConfigured(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("WAAS_OIDC_ISSUER", "https://idp.example.com/application/o/waas/")
	t.Setenv("WAAS_OIDC_CLIENT_ID", "waas")
	t.Setenv("WAAS_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("WAAS_OIDC_REDIRECT_URL", "https://waas.example.com/api/v1/auth/oidc/callback")
	t.Setenv("WAAS_LOGIN_OIDC_ONLY", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !cfg.OIDC.OIDCOnly {
		t.Fatal("cfg.OIDC.OIDCOnly should be true")
	}
}

func TestLoadOIDCOnlyDefaultsToFalse(t *testing.T) {
	setBaseEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.OIDC.OIDCOnly {
		t.Fatal("cfg.OIDC.OIDCOnly should default to false")
	}
}

func TestLoadCatalogSyncIntervalDefault(t *testing.T) {
	setBaseEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if want := 6 * time.Hour; cfg.CatalogSyncInterval != want {
		t.Fatalf("cfg.CatalogSyncInterval = %v, want %v", cfg.CatalogSyncInterval, want)
	}
}

// An invalid WAAS_CATALOG_SYNC_INTERVAL must NOT stop the api-server —
// unlike the operator's historical fail-closed behavior for this same
// setting, it only degrades a purely cosmetic sync cadence. It must
// still fall back to the default rather than e.g. parsing as zero.
func TestLoadCatalogSyncIntervalInvalidFallsBack(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("WAAS_CATALOG_SYNC_INTERVAL", "not-a-duration")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() must not fail on an invalid catalog sync interval: %v", err)
	}
	if want := 6 * time.Hour; cfg.CatalogSyncInterval != want {
		t.Fatalf("cfg.CatalogSyncInterval = %v, want fallback %v", cfg.CatalogSyncInterval, want)
	}
}

func TestLoadCatalogSyncIntervalValid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("WAAS_CATALOG_SYNC_INTERVAL", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if want := 30 * time.Minute; cfg.CatalogSyncInterval != want {
		t.Fatalf("cfg.CatalogSyncInterval = %v, want %v", cfg.CatalogSyncInterval, want)
	}
}
