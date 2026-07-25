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
	return withCookieJSON(t, h, method, path, cookie, nil)
}

// withCookieJSON is withCookie with a JSON body.
func withCookieJSON(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encoding body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
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
// A self-service password change revokes every token of the account —
// this browser's included. The cookie must die in the SAME response:
// leaving it in the jar makes the UI look signed in until some later
// request 401s, which is how the user discovers it (audit 3, F9).
func TestPasswordChangeExpiresTheSessionCookie(t *testing.T) {
	h, _ := newTestServer(t)

	login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	c := sessionCookieOf(login)
	if c == nil {
		t.Fatal("login must set the waas_session cookie")
	}

	// A profile edit that touches no credential must leave the session
	// alone — the control that keeps this from over-firing.
	if rec := withCookieJSON(t, h, http.MethodPatch, "/api/v1/me", c,
		map[string]string{"displayName": "Admin"}); rec.Code != http.StatusOK {
		t.Fatalf("plain profile edit: expected 200, got %d: %s", rec.Code, rec.Body)
	} else if got := sessionCookieOf(rec); got != nil {
		t.Fatalf("a profile edit must not touch the cookie, got %+v", got)
	}

	rec := withCookieJSON(t, h, http.MethodPatch, "/api/v1/me", c, map[string]string{
		"currentPassword": "admin-password", "newPassword": "a-brand-new-password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("password change: expected 200, got %d: %s", rec.Code, rec.Body)
	}
	cleared := sessionCookieOf(rec)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("a password change must expire the cookie, got %+v", cleared)
	}
	// And the value is genuinely dead, not merely dropped from the jar.
	if out := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", c); out.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie: expected 401, got %d: %s", out.Code, out.Body)
	}
}

// Same class as the password change, on the admin path: an admin who
// demotes, deactivates or resets the password of their OWN account
// revokes their own session doing it. Editing someone else's must leave
// this browser's session strictly alone.
func TestAdminSelfEditExpiresTheSessionCookie(t *testing.T) {
	h, _ := newTestServer(t)

	login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	c := sessionCookieOf(login)
	if c == nil {
		t.Fatal("login must set the waas_session cookie")
	}
	self := userIDOf(t, h, c)

	// A second admin, or the last-admin guard would refuse the demotion
	// below — and rightly so, that is its own test.
	if rec := withCookieJSON(t, h, http.MethodPost, "/api/v1/users", c, map[string]any{
		"username": "second-admin", "password": "another-password", "role": "admin",
	}); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("seeding a second admin: got %d: %s", rec.Code, rec.Body)
	}

	// An edit that revokes nothing must not touch the session — the
	// control that keeps this from signing an admin out for a quota bump.
	if rec := withCookieJSON(t, h, http.MethodPatch, "/api/v1/users/"+self, c,
		map[string]any{"maxWorkspaces": 7}); rec.Code != http.StatusOK {
		t.Fatalf("quota edit: expected 200, got %d: %s", rec.Code, rec.Body)
	} else if got := sessionCookieOf(rec); got != nil {
		t.Fatalf("a non-revoking edit must not touch the cookie, got %+v", got)
	} else if h := rec.Header().Get("X-Waas-Session-Ended"); h != "" {
		t.Fatalf("a non-revoking edit must not announce a session end, got %q", h)
	}

	// Demoting yourself revokes your own tokens: the cookie must die with
	// them, in this very response, and the SPA must be told why — it
	// cannot see the cookie go.
	rec := withCookieJSON(t, h, http.MethodPatch, "/api/v1/users/"+self, c,
		map[string]any{"role": "user"})
	if rec.Code != http.StatusOK {
		t.Fatalf("self-demotion: expected 200, got %d: %s", rec.Code, rec.Body)
	}
	cleared := sessionCookieOf(rec)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("a self-revoking edit must expire the cookie, got %+v", cleared)
	}
	if got := rec.Header().Get("X-Waas-Session-Ended"); got != "rights-changed" {
		t.Fatalf("expected the rights-changed reason, got %q", got)
	}
	if out := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", c); out.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie: expected 401, got %d: %s", out.Code, out.Body)
	}
}

// Losing the last administrator has no in-product way back: it would take
// a database edit or a redeploy against an empty one. The platform refuses
// rather than letting an admin strand it.
func TestLastAdminCannotDropTheirOwnRights(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"demotion":     {"role": "user"},
		"deactivation": {"active": false},
		"both at once": {"role": "user", "active": false},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newTestServer(t)
			login := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
				"username": "admin", "password": "admin-password",
			})
			c := sessionCookieOf(login)
			self := userIDOf(t, h, c)

			rec := withCookieJSON(t, h, http.MethodPatch, "/api/v1/users/"+self, c, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body)
			}
			// A refused edit must leave the session strictly alone.
			if got := sessionCookieOf(rec); got != nil {
				t.Fatalf("a refused edit must not touch the cookie, got %+v", got)
			}
			if out := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", c); out.Code != http.StatusOK {
				t.Fatalf("session must survive a refused edit: got %d", out.Code)
			}
		})
	}
}

// userIDOf reads the caller's own account id through /auth/me.
func userIDOf(t *testing.T, h http.Handler, cookie *http.Cookie) string {
	t.Helper()
	rec := withCookie(t, h, http.MethodGet, "/api/v1/auth/me", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/me: expected 200, got %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /auth/me: %v", err)
	}
	if body.Data.ID == "" {
		t.Fatalf("/auth/me carried no id: %s", rec.Body)
	}
	return body.Data.ID
}

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
