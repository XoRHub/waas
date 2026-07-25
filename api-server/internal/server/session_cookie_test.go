package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionCookieOf returns the waas_session cookie a response set, or nil.
func sessionCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "waas_session" {
			return c
		}
	}
	return nil
}

// withCookie replays a request authenticated by cookie alone — no
// Authorization header — the way a browser will once the frontend
// switches.
func withCookie(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Login must hand the browser a session cookie carrying the same token the
// body returns, with the attributes that make it useless to an attacker:
// HttpOnly (out of JavaScript's reach — finding #13) and SameSite=Strict
// (never attached to a cross-site request in the first place).
func TestLoginSetsTheSessionCookie(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", rec.Code, rec.Body)
	}
	c := sessionCookieOf(rec)
	if c == nil {
		t.Fatal("login must set the waas_session cookie")
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly: the whole point is that JavaScript cannot read it")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie must be SameSite=Strict, got %v", c.SameSite)
	}
	if c.Path != "/api" {
		t.Errorf("cookie must be scoped to /api (wwt's /ws and /kasm use connection tokens), got %q", c.Path)
	}
	if c.Value == "" {
		t.Error("cookie must carry the access token")
	}

	// And it authenticates on its own, with no Authorization header.
	if rec := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", c); rec.Code != http.StatusOK {
		t.Fatalf("cookie-only /auth/me: expected 200, got %d: %s", rec.Code, rec.Body)
	}
}

// Login CSRF: a cross-site page must never be able to make the browser
// store a session cookie. The attacker submits their OWN valid
// credentials, so authentication succeeding is precisely the problem —
// the victim's browser would silently operate as the attacker's account.
// enctype="text/plain" is what lets a plain HTML form reach a JSON
// endpoint with no preflight: json.Decoder stops after the first value,
// so the trailing "=" the form appends is ignored.
func TestCrossSiteLoginIsRejected(t *testing.T) {
	h, _ := newTestServer(t)

	for name, tc := range map[string]struct {
		fetchSite string
		want      int
	}{
		"cross-site form post": {"cross-site", http.StatusForbidden},
		"sibling subdomain":    {"same-site", http.StatusForbidden},
		"typed in the URL bar": {"none", http.StatusForbidden},
		// The SPA's own fetch, and any non-browser client (no Fetch
		// Metadata at all — the CLI reads the token from the body).
		"the SPA itself":    {"same-origin", http.StatusOK},
		"no fetch metadata": {"", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			body := strings.NewReader(`{"username":"admin","password":"admin-password"}=`)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
			req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, rec.Code, rec.Body)
			}
			if tc.want != http.StatusOK && sessionCookieOf(rec) != nil {
				t.Fatal("a rejected login must not plant a session cookie")
			}
		})
	}
}

// Secure cannot be hardcoded: the api-server sits behind an ingress that
// terminates TLS, so the forwarded-proto header is the only signal it has
// — and that header is a list, not a word, as soon as a second proxy is
// in the path. Getting this wrong hands out a bearer-equivalent cookie
// without Secure to a user who is on https, leaving the browser free to
// send it in cleartext.
func TestSessionCookieSecureFollowsForwardedProto(t *testing.T) {
	h, _ := newTestServer(t)

	for name, tc := range map[string]struct {
		proto string
		want  bool
	}{
		"single proxy":            {"https", true},
		"chained proxies":         {"https, http", true},
		"chained, no space":       {"https,http", true},
		"uppercase (RFC 9110)":    {"HTTPS", true},
		"plain http":              {"http", false},
		"no header (bare go run)": {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			var body bytes.Buffer
			if err := json.NewEncoder(&body).Encode(map[string]string{
				"username": "admin", "password": "admin-password",
			}); err != nil {
				t.Fatalf("encoding body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", &body)
			req.Header.Set("Content-Type", "application/json")
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("login: expected 200, got %d: %s", rec.Code, rec.Body)
			}
			c := sessionCookieOf(rec)
			if c == nil {
				t.Fatal("login must set the waas_session cookie")
			}
			if c.Secure != tc.want {
				t.Fatalf("X-Forwarded-Proto %q: want Secure=%v, got %v", tc.proto, tc.want, c.Secure)
			}
		})
	}
}

// A browser holding a token the server no longer honors must not be left
// carrying it around. The route that would clear it — logout — sits behind
// the auth middleware, so by the time the session is dead the handler is
// unreachable: the middleware has to do the expiring itself, or the
// browser keeps sending a revoked credential on every request until the
// cookie's own Expires date.
func TestRejectedSessionCookieIsExpired(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	c := sessionCookieOf(rec)
	if c == nil {
		t.Fatal("login must set the waas_session cookie")
	}
	if out := withCookie(t, h, http.MethodPost, "/api/v1/auth/logout", c); out.Code != http.StatusNoContent {
		t.Fatalf("logout: expected 204, got %d: %s", out.Code, out.Body)
	}

	// The cookie is now dead. Whatever the browser does next with it —
	// including clicking Logout again, which never reaches its handler —
	// must come back with the cookie expired.
	for _, call := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/logout"},
	} {
		out := withCookie(t, h, call.method, call.path, c)
		if out.Code != http.StatusUnauthorized {
			t.Fatalf("%s with a revoked cookie: expected 401, got %d: %s", call.path, out.Code, out.Body)
		}
		cleared := sessionCookieOf(out)
		if cleared == nil || cleared.MaxAge >= 0 {
			t.Fatalf("%s must expire the rejected cookie, got %+v", call.path, cleared)
		}
	}
}

// Logout revokes server-side (finding #2) AND expires the cookie, so the
// browser is not left holding a dead credential.
func TestLogoutExpiresTheSessionCookie(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	c := sessionCookieOf(rec)
	if c == nil {
		t.Fatal("login must set the waas_session cookie")
	}

	out := withCookie(t, h, http.MethodPost, "/api/v1/auth/logout", c)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout: expected 204, got %d: %s", out.Code, out.Body)
	}
	cleared := sessionCookieOf(out)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout must expire the cookie, got %+v", cleared)
	}

	// Revocation is what actually matters: the old cookie value is dead
	// even for a client that ignores Set-Cookie.
	if rec := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie: expected 401, got %d: %s", rec.Code, rec.Body)
	}
}
