package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/service"
)

// With OIDCOnly set, Login must 404 before touching the body or the auth
// service — even a valid credential pair never reaches password checking.
// The nil AuthService proves it: any service call would panic.
func TestLoginOIDCOnlyRejectsEveryone(t *testing.T) {
	h := NewAuthHandler(nil, nil, config.OIDCConfig{OIDCOnly: true}, nil)

	for name, body := range map[string]string{
		"valid credentials": `{"username":"admin","password":"correct-password"}`,
		"garbage body":      `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
			w := httptest.NewRecorder()
			h.Login(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthCookieRoundTrip(t *testing.T) {
	req := &service.AuthRequest{State: "st4te", Verifier: "v3rifier", Nonce: "n0nce"}
	state, verifier, nonce, ok := unpackAuthCookie(packAuthCookie(req))
	if !ok || state != req.State || verifier != req.Verifier || nonce != req.Nonce {
		t.Fatalf("round trip lost data: %q %q %q ok=%v", state, verifier, nonce, ok)
	}

	for name, value := range map[string]string{
		"empty":                   "",
		"legacy single-state":     "just-a-state",
		"missing part":            "state..nonce",
		"too many parts":          "a.b.c.d",
		"trailing separator only": "a.b.",
	} {
		if _, _, _, ok := unpackAuthCookie(value); ok {
			t.Fatalf("%s: malformed cookie %q must not unpack", name, value)
		}
	}
}

// The callback must reject a state mismatch (or a malformed/stale cookie)
// before any service call — the nil-safe check is the cookie itself; the
// OIDC service here would panic on use since it has no provider.
func TestOIDCCallbackRejectsBadState(t *testing.T) {
	h := NewAuthHandler(nil, &service.OIDCService{}, config.OIDCConfig{}, nil)

	for name, cookie := range map[string]string{
		"no cookie":     "",
		"legacy format": "only-state",
		"wrong state":   "other.verifier.nonce",
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?state=expected&code=x", nil)
			if cookie != "" {
				r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: cookie})
			}
			w := httptest.NewRecorder()
			h.OIDCCallback(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProvidersLocalFollowsOIDCOnly(t *testing.T) {
	for _, tc := range []struct {
		oidcOnly  bool
		wantLocal bool
	}{
		{oidcOnly: false, wantLocal: true},
		{oidcOnly: true, wantLocal: false},
	} {
		h := NewAuthHandler(nil, nil, config.OIDCConfig{OIDCOnly: tc.oidcOnly}, nil)
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil)
		w := httptest.NewRecorder()
		h.Providers(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("oidcOnly=%v: want 200, got %d", tc.oidcOnly, w.Code)
		}
		var resp struct {
			Data struct {
				Local bool `json:"local"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("oidcOnly=%v: bad JSON: %v", tc.oidcOnly, err)
		}
		if resp.Data.Local != tc.wantLocal {
			t.Fatalf("oidcOnly=%v: want local=%v, got %v", tc.oidcOnly, tc.wantLocal, resp.Data.Local)
		}
	}
}
