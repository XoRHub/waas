package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// The session cookie is a second transport for the SAME token, not a
// second credential: everything the header path enforces must hold on it
// — audience isolation included — plus the CSRF guard the header does not
// need (a cross-site page cannot set a header, but a cookie rides along
// on its own).
func TestAuthSessionCookieTransport(t *testing.T) {
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	const issuer = "waas-test"
	apiToken, err := signer.Sign(auth.NewAccessClaims(issuer, "user-1", auth.RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	streamToken, err := signer.Sign(auth.NewStreamClaims(issuer, "user-1", auth.RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Actor(r).ID != "user-1" {
			t.Errorf("claims must reach Actor, got %+v", Actor(r))
		}
		w.WriteHeader(http.StatusOK)
	})
	authed := Auth(signer, issuer, activeUser(auth.RoleUser))(next)

	for name, tc := range map[string]struct {
		header    string // Authorization bearer, if any
		cookie    string // waas_session value, if any
		fetchSite string // Sec-Fetch-Site, if any
		want      int
	}{
		// A non-browser client sends no Fetch Metadata; SameSite=Strict is
		// what governed the cookie there, so the guard must not reject it.
		"cookie, no fetch metadata":  {"", apiToken, "", http.StatusOK},
		"cookie, same-origin":        {"", apiToken, "same-origin", http.StatusOK},
		"cookie, cross-site":         {"", apiToken, "cross-site", http.StatusUnauthorized},
		"cookie, none (address bar)": {"", apiToken, "none", http.StatusUnauthorized},
		"cookie, sibling subdomain":  {"", apiToken, "same-site", http.StatusUnauthorized},
		"stream token in the cookie": {"", streamToken, "same-origin", http.StatusUnauthorized},
		"no credential at all":       {"", "", "same-origin", http.StatusUnauthorized},
		// The guard is scoped to the cookie: a header cannot be set by a
		// cross-site page, so rejecting it would break legitimate clients
		// for nothing.
		"header, cross-site":             {apiToken, "", "cross-site", http.StatusOK},
		"header wins over a junk cookie": {apiToken, "not-a-token", "same-origin", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/some-route", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", "Bearer "+tc.header)
			}
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: SessionCookie, Value: tc.cookie})
			}
			if tc.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			w := httptest.NewRecorder()
			authed.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

// The per-request user re-check (finding #2) is transport-agnostic: a
// cookie must not become a way around revocation, and a database failure
// on the cookie path must still read as 503 rather than 401 — a 401 makes
// the frontend drop its session, so a DB hiccup would sign the fleet out.
func TestAuthSessionCookieEnforcesUserState(t *testing.T) {
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	const issuer = "waas-test"
	token, err := signer.Sign(auth.NewAccessClaims(issuer, "user-1", auth.RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	for name, tc := range map[string]struct {
		users UserSource
		want  int
	}{
		"active account": {activeUser(auth.RoleUser), http.StatusOK},
		"deactivated": {userSourceFunc(func(_ context.Context, id string) (*model.User, error) {
			return &model.User{ID: id, Role: auth.RoleUser, Active: false}, nil
		}), http.StatusUnauthorized},
		"revoked by tokens_valid_after": {userSourceFunc(func(_ context.Context, id string) (*model.User, error) {
			bound := time.Now().UTC().Add(time.Minute)
			return &model.User{ID: id, Role: auth.RoleUser, Active: true, TokensValidAfter: &bound}, nil
		}), http.StatusUnauthorized},
		"database down answers 503, never 401": {userSourceFunc(func(_ context.Context, _ string) (*model.User, error) {
			return nil, context.DeadlineExceeded
		}), http.StatusServiceUnavailable},
		"account deleted": {userSourceFunc(func(_ context.Context, _ string) (*model.User, error) {
			return nil, repository.ErrUserNotFound
		}), http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/some-route", nil)
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			Auth(signer, issuer, tc.users)(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
