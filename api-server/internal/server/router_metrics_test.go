package server

// WAAS_METRICS_ENABLED gates the ENDPOINT, not just a manifest: disabled
// must serve nothing at /metrics (the route does not exist).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/shared/auth"
)

func metricsRouter(t *testing.T, enabled bool) http.Handler {
	t.Helper()
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	// Empty Handlers: /metrics and /healthz never reach the nil handlers.
	return New(&config.Config{JWTIssuer: "waas-test", MetricsEnabled: enabled}, signer, Handlers{})
}

func TestMetricsEndpointDisabledByDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	metricsRouter(t, false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics with metrics disabled = %d, want 404", rec.Code)
	}
}

func TestMetricsEndpointServesWhenEnabled(t *testing.T) {
	rec := httptest.NewRecorder()
	metricsRouter(t, true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics with metrics enabled = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Fatal("expected the default registry (go collector) to be exposed")
	}
}
