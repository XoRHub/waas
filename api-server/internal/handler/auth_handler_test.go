package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xorhub/waas/api-server/internal/config"
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
