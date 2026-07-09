package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/xorhub/waas/api-server/internal/metrics"
)

// The observed route must be the chi PATTERN, never the raw path: label
// cardinality stays bounded by the route table.
func TestMetricsObservesRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(Metrics)
	r.Get("/api/v1/workspaces/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	counter := metrics.HTTPRequests.WithLabelValues("/api/v1/workspaces/{id}", http.MethodGet, "204")
	before := testutil.ToFloat64(counter)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/abc-123", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := testutil.ToFloat64(counter); got != before+1 {
		t.Fatalf("HTTPRequests = %v, want %v", got, before+1)
	}
}

// A handler that never calls WriteHeader is a 200 (net/http default), and
// an unrouted request must not leak its raw path into the labels.
func TestMetricsDefaultsStatusAndUnmatchedRoute(t *testing.T) {
	r := chi.NewRouter()
	r.Use(Metrics)
	r.Get("/ok", func(http.ResponseWriter, *http.Request) {})

	okCounter := metrics.HTTPRequests.WithLabelValues("/ok", http.MethodGet, "200")
	beforeOK := testutil.ToFloat64(okCounter)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ok", nil))
	if got := testutil.ToFloat64(okCounter); got != beforeOK+1 {
		t.Fatalf("HTTPRequests{200} = %v, want %v", got, beforeOK+1)
	}

	missCounter := metrics.HTTPRequests.WithLabelValues("unmatched", http.MethodGet, "404")
	beforeMiss := testutil.ToFloat64(missCounter)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/definitely/not/routed", nil))
	if got := testutil.ToFloat64(missCounter); got != beforeMiss+1 {
		t.Fatalf("HTTPRequests{unmatched} = %v, want %v", got, beforeMiss+1)
	}
}
