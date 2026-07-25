package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// CORS only matters in split-origin setups (the Vite dev server), which is
// why it went untested — but it is also the only place that decides what a
// browser on another origin is allowed to READ of a response. A session
// this middleware forgot to expose would be a session the SPA cannot see
// ending, so the policy is worth pinning even where it is rarely used.
func TestCORSPolicy(t *testing.T) {
	const dev = "http://localhost:5173"
	reached := false
	handler := CORS([]string{dev, " http://127.0.0.1:5173 "})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))

	t.Run("allowed origin gets the policy", func(t *testing.T) {
		reached = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
		req.Header.Set("Origin", dev)
		handler.ServeHTTP(rec, req)

		if !reached {
			t.Fatal("an allowed origin must still reach the handler")
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != dev {
			t.Fatalf("Allow-Origin: got %q", got)
		}
		// The one that is not merely conventional: without it the browser
		// hides the header the SPA reads to learn its session just ended.
		if got := rec.Header().Get("Access-Control-Expose-Headers"); got != SessionEndedHeader {
			t.Fatalf("Expose-Headers: got %q, want %q", got, SessionEndedHeader)
		}
	})

	t.Run("configured origins are trimmed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
		req.Header.Set("Origin", "http://127.0.0.1:5173")
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "" {
			t.Fatal("surrounding spaces in the configured list must not disable an origin")
		}
	})

	t.Run("unlisted origin gets nothing", func(t *testing.T) {
		reached = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("an unlisted origin must get no CORS grant, got %q", got)
		}
		if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "" {
			t.Fatalf("nor anything else, got %q", got)
		}
		// The request itself is NOT refused here: the browser enforces the
		// absence of the grant. Refusing would break non-browser clients,
		// which send no Origin and are authenticated by the bearer.
		if !reached {
			t.Fatal("the middleware must not reject the request itself")
		}
	})

	t.Run("preflight answers without reaching the handler", func(t *testing.T) {
		reached = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/workspaces", nil)
		req.Header.Set("Origin", dev)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight: want 204, got %d", rec.Code)
		}
		if reached {
			t.Fatal("a preflight must not reach the handler")
		}
	})

	t.Run("no Origin at all is left alone", func(t *testing.T) {
		reached = false
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil))

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("a same-origin request needs no grant, got %q", got)
		}
		if !reached {
			t.Fatal("it must still reach the handler")
		}
	})
}
