package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/handler"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

// ---- login contract ---------------------------------------------------

func TestLoginRejectsBadCredentialsAndIssuesWorkingTokens(t *testing.T) {
	h, _ := newTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "wrong",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password must 401, got %d", rec.Code)
	}
	var problem struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Status != 401 {
		t.Fatalf("errors must be problem+json shaped: %s", rec.Body.String())
	}
	// The 401 must not leak whether the user exists.
	recGhost := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "ghost", "password": "wrong",
	})
	if recGhost.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user must 401 too, got %d", recGhost.Code)
	}

	// A good login yields a token the protected routes accept…
	token := login(t, h)
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/auth/me", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("issued token rejected: %d %s", rec.Code, rec.Body.String())
	}
	// …and protected routes reject its absence and garbage.
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/auth/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token must 401, got %d", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/auth/me", "not-a-jwt", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token must 401, got %d", rec.Code)
	}
}

// ---- users PATCH: RBAC + groups contract --------------------------------

func TestUsersPatchIsAdminOnlyAndReplacesGroupsWholesale(t *testing.T) {
	h, _ := newTestServer(t)
	admin := login(t, h)

	// Admin creates a plain user with initial groups.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"username": "bob", "password": "bob-password1", "role": "user",
		"groups": []string{"dev", "ops"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating user: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID     string   `json:"id"`
			Groups []string `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// The plain user cannot edit accounts — authorization is SERVER-side.
	bobToken := loginAs(t, h, "bob", "bob-password1")
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/users/"+created.Data.ID, bobToken, map[string]any{
		"role": "admin",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin PATCH must 403, got %d %s", rec.Code, rec.Body.String())
	}

	// Admin PATCH sends the COMPLETE groups list: the result is exactly
	// that list — nothing merged, nothing silently kept.
	rec = doJSON(t, h, http.MethodPatch, "/api/v1/users/"+created.Data.ID, admin, map[string]any{
		"role": "user", "maxWorkspaces": 3, "groups": []string{"sec"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin PATCH: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/users/"+created.Data.ID, admin, nil)
	var got struct {
		Data struct {
			Groups []string `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Groups) != 1 || got.Data.Groups[0] != "sec" {
		t.Fatalf("groups must be replaced wholesale, got %v", got.Data.Groups)
	}
}

func loginAs(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": username, "password": password,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.AccessToken
}

// ---- connect contract ---------------------------------------------------

func TestConnectRequiresAuthAndScopesToOwner(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/workspaces/some-id/connect", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("connect without auth must 401, got %d", rec.Code)
	}
	token := login(t, h)
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/workspaces/nonexistent/connect", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("connect on a missing workspace must 404, got %d", rec.Code)
	}
}

// ---- OIDC callback against a real (stubbed) IdP --------------------------

// stubIdP is a minimal OIDC provider: discovery, JWKS and a token
// endpoint returning an RS256 id_token with the claims under test. It
// exercises the REAL go-oidc verification path — no client mocking.
type stubIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
}

func newStubIdP(t *testing.T) *stubIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &stubIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 idp.server.URL,
			"authorization_endpoint": idp.server.URL + "/authorize",
			"token_endpoint":         idp.server.URL + "/token",
			"jwks_uri":               idp.server.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		claims := jwt.MapClaims{
			"iss": idp.server.URL, "aud": "waas-client", "sub": "idp-sub-1",
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		}
		for k, v := range idp.claims {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test"
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "id_token": signed,
		})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func TestOIDCCallbackMirrorsGroupsAndSyncsAdminRole(t *testing.T) {
	idp := newStubIdP(t)
	idp.claims = map[string]any{
		"preferred_username": "carol",
		"email":              "carol@example.com",
		"groups":             []string{"idp:dev", "idp:admins"},
	}

	db, err := database.Open(filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		JWTIssuer: "waas-test", AccessTokenTTL: time.Hour, ConnectionTokenTTL: time.Minute,
		WorkspaceNamespace: "waas-workspaces", InternalToken: "x", AdminUsername: "admin",
		OIDC: config.OIDCConfig{
			IssuerURL: idp.server.URL, ClientID: "waas-client", ClientSecret: "s",
			RedirectURL: "http://waas.test/api/v1/auth/oidc/callback", FrontendURL: "/",
			Scopes: []string{"openid", "profile"}, UsernameClaim: "preferred_username",
			GroupsClaim: "groups", AdminGroups: []string{"idp:admins"},
		},
	}
	users := repository.NewSQLUserRepository(db)
	sessions := repository.NewSQLSessionRepository(db)
	audit := service.NewAuditService(repository.NewSQLAuditRepository(db))
	authSvc := service.NewAuthService(users, signer, audit, cfg.JWTIssuer, cfg.AccessTokenTTL)
	oidcSvc := service.NewOIDCService(cfg.OIDC, users, audit, signer, cfg.JWTIssuer, cfg.AccessTokenTTL)
	userSvc := service.NewUserService(users, audit)
	workspaceSvc := service.NewWorkspaceService(kube, cfg.WorkspaceNamespace, users, sessions, audit, signer,
		cfg.JWTIssuer, cfg.ConnectionTokenTTL)
	h := New(cfg, signer, Handlers{
		Auth:       handler.NewAuthHandler(authSvc, oidcSvc, cfg.OIDC, signer),
		Users:      handler.NewUserHandler(userSvc),
		Templates:  handler.NewTemplateHandler(service.NewTemplateService(kube, cfg.WorkspaceNamespace, audit)),
		Workspaces: handler.NewWorkspaceHandler(workspaceSvc),
		Admin:      handler.NewAdminHandler(audit, service.NewSessionService(sessions)),
		Internal:   handler.NewInternalHandler(workspaceSvc),
		Meta:       handler.NewMetaHandler(),
	})

	// 1. /oidc/start: capture the state cookie + the state in the URL.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/start", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("start must redirect to the IdP, got %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "state") || c.Value == state {
			stateCookie = c
		}
	}
	if state == "" || stateCookie == nil {
		t.Fatalf("state handshake incomplete: state=%q cookies=%v", state, rec.Result().Cookies())
	}

	// 2. State mismatch is rejected before any token exchange.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=c&state=WRONG", nil)
	req.AddCookie(stateCookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("state mismatch must 401, got %d", rec.Code)
	}

	// 3. The real callback: token exchange against the stub, id_token
	// verified by go-oidc, user provisioned with mirrored groups and the
	// admin role (idp:admins ∈ adminGroups).
	req = httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/auth/oidc/callback?code=c&state=%s", url.QueryEscape(state)), nil)
	req.AddCookie(stateCookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback must redirect to the frontend, got %d %s", rec.Code, rec.Body.String())
	}
	redirect := rec.Header().Get("Location")
	if strings.Contains(redirect, "error=") {
		t.Fatalf("callback failed: %s", redirect)
	}
	if !strings.Contains(redirect, "token=") {
		t.Fatalf("expected a token in the fragment redirect, got %s", redirect)
	}

	user, err := users.FindByUsername(context.Background(), "carol")
	if err != nil {
		t.Fatalf("SSO user must be provisioned: %v", err)
	}
	if len(user.Groups) != 2 || user.Groups[0] != "idp:dev" {
		t.Fatalf("groups must mirror the IdP claim, got %v", user.Groups)
	}
	if user.Role != auth.RoleAdmin {
		t.Fatalf("idp:admins membership must sync the admin role, got %s", user.Role)
	}
}
