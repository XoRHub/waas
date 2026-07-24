package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xorhub/waas/shared/auth"
)

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
	authed := Auth(signer, issuer)(next)
	streamed := StreamAuth(signer, issuer)(next)

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
