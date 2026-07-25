package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// userSourceFunc doubles UserSource with a plain func.
type userSourceFunc func(ctx context.Context, id string) (*model.User, error)

func (f userSourceFunc) FindByID(ctx context.Context, id string) (*model.User, error) {
	return f(ctx, id)
}

// activeUser returns a UserSource serving one active account matching the
// tokens minted by the tests.
func activeUser(role auth.Role) UserSource {
	return userSourceFunc(func(_ context.Context, id string) (*model.User, error) {
		return &model.User{ID: id, Username: "user", Role: role, Active: true}, nil
	})
}

// The API/stream separation, end to end at the middleware level. Auth only
// reads the Authorization header (a token in the query string must never
// authenticate a normal route — that is how bearers end up in access logs),
// StreamAuth only reads the access_token query parameter, and each accepts
// only its own audience.
func TestAuthAndStreamAuthTransportAndAudience(t *testing.T) {
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
	users := activeUser(auth.RoleUser)
	authed := Auth(signer, issuer, users)(next)
	streamed := StreamAuth(signer, issuer, users)(next)

	for name, tc := range map[string]struct {
		handler http.Handler
		header  string // Authorization bearer, if any
		query   string // access_token query parameter, if any
		want    int
	}{
		"api token in header on a normal route":    {authed, apiToken, "", http.StatusOK},
		"api token in query on a normal route":     {authed, "", apiToken, http.StatusUnauthorized},
		"api token in query on the stream":         {streamed, "", apiToken, http.StatusUnauthorized},
		"stream token in query on the stream":      {streamed, "", streamToken, http.StatusOK},
		"stream token in query on a normal route":  {authed, "", streamToken, http.StatusUnauthorized},
		"stream token in header on a normal route": {authed, streamToken, "", http.StatusUnauthorized},
		"stream token in header on the stream":     {streamed, streamToken, "", http.StatusUnauthorized},
		"no credentials on a normal route":         {authed, "", "", http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			target := "/api/v1/some-route"
			if tc.query != "" {
				target += "?access_token=" + tc.query
			}
			r := httptest.NewRequest(http.MethodGet, target, nil)
			if tc.header != "" {
				r.Header.Set("Authorization", "Bearer "+tc.header)
			}
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

// A cryptographically valid token is not enough: the bearer is re-checked
// against current user state on every request, so deactivation, demotion,
// deletion and logout take effect immediately instead of at token expiry
// (security audit 2026-07-20, finding #2). Same gate on the SSE stream —
// a revoked user must not open a NEW stream with an unexpired token.
func TestAuthEnforcesUserState(t *testing.T) {
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	const issuer = "waas-test"

	// iat is second-truncated by JWT; recover the exact instant the token
	// carries so the bound cases are deterministic.
	claims := auth.NewAccessClaims(issuer, "user-1", auth.RoleUser, time.Minute)
	iat := claims.IssuedAt.Time
	apiToken, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	user := func(mutate func(*model.User)) UserSource {
		return userSourceFunc(func(_ context.Context, id string) (*model.User, error) {
			u := &model.User{ID: id, Username: "user", Role: auth.RoleUser, Active: true}
			mutate(u)
			return u, nil
		})
	}
	sameSecondBound := iat.Add(300 * time.Millisecond)
	pastBound := iat.Add(-time.Hour)

	for name, tc := range map[string]struct {
		users UserSource
		want  int
	}{
		"active user passes": {activeUser(auth.RoleUser), http.StatusOK},
		"deleted account is 401": {userSourceFunc(func(context.Context, string) (*model.User, error) {
			return nil, repository.ErrUserNotFound
		}), http.StatusUnauthorized},
		"disabled account is 401": {user(func(u *model.User) { u.Active = false }), http.StatusUnauthorized},
		"demoted role is 401":     {activeUser(auth.RoleAdmin), http.StatusUnauthorized},
		"iat before the bound is 401": {user(func(u *model.User) {
			bound := iat.Add(time.Hour)
			u.TokensValidAfter = &bound
		}), http.StatusUnauthorized},
		// The clock trap: a bound set within the SAME second as issuance
		// must still revoke — iat truncation must err toward revocation,
		// or a logout right after a login leaves the token alive until
		// the next second boundary.
		"bound in the same second as iat is 401": {user(func(u *model.User) {
			u.TokensValidAfter = &sameSecondBound
		}), http.StatusUnauthorized},
		"bound older than iat passes": {user(func(u *model.User) {
			u.TokensValidAfter = &pastBound
		}), http.StatusOK},
		// A database hiccup must NOT read as a revocation: the frontend
		// drops its auth state on every 401, so an outage answering 401
		// would log out the whole fleet.
		"database error is 503, never 401": {userSourceFunc(func(context.Context, string) (*model.User, error) {
			return nil, errors.New("connection refused")
		}), http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			r := httptest.NewRequest(http.MethodGet, "/api/v1/some-route", nil)
			r.Header.Set("Authorization", "Bearer "+apiToken)
			w := httptest.NewRecorder()
			Auth(signer, issuer, tc.users)(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("Auth: want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}

	// The stream middleware runs the exact same gate: spot-check the
	// revocation case with a valid, unexpired stream token.
	streamToken, err := signer.Sign(auth.NewStreamClaims(issuer, "user-1", auth.RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	revokedUsers := user(func(u *model.User) {
		bound := time.Now().UTC().Add(time.Hour)
		u.TokensValidAfter = &bound
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodGet, "/api/v1/events?access_token="+streamToken, nil)
	w := httptest.NewRecorder()
	StreamAuth(signer, issuer, revokedUsers)(next).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("StreamAuth must reject a revoked user: want 401, got %d: %s", w.Code, w.Body.String())
	}
}
