package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/handler"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

// newTestServer wires the full stack against SQLite and a fake cluster,
// exactly as dev mode does, and returns the HTTP handler plus admin creds.
func newTestServer(t *testing.T) (http.Handler, *auth.Signer) {
	t.Helper()

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}

	cfg := &config.Config{
		JWTIssuer:          "waas-test",
		AccessTokenTTL:     time.Hour,
		ConnectionTokenTTL: time.Minute,
		WorkspaceNamespace: "test-ns",
		InternalToken:      "internal-secret",
		AdminUsername:      "admin",
	}

	users := repository.NewSQLUserRepository(db)
	sessions := repository.NewSQLSessionRepository(db)
	remotes := repository.NewSQLRemoteWorkspaceRepository(db)
	catalogRepo := repository.NewSQLCatalogRepository(db)
	audit := service.NewAuditService(repository.NewSQLAuditRepository(db))
	authSvc := service.NewAuthService(users, signer, audit, cfg.JWTIssuer, cfg.AccessTokenTTL)
	userSvc := service.NewUserService(users, audit)
	templateSvc := service.NewTemplateService(kube, cfg.WorkspaceNamespace, audit)
	workspaceSvc := service.NewWorkspaceService(kube, cfg.WorkspaceNamespace, users, sessions, audit, signer,
		cfg.JWTIssuer, cfg.ConnectionTokenTTL).
		WithRemoteWorkspaces(remotes)
	remoteSvc := service.NewRemoteWorkspaceService(kube, cfg.WorkspaceNamespace, users, remotes, sessions,
		audit, signer, cfg.JWTIssuer, cfg.ConnectionTokenTTL).
		WithEvents(service.NewEventHub())
	governanceSvc := service.NewGovernanceService(kube, cfg.WorkspaceNamespace, users, audit, catalogRepo)

	if err := userSvc.EnsureBootstrapAdmin(context.Background(), "admin", "admin-password"); err != nil {
		t.Fatalf("bootstrapping admin: %v", err)
	}

	return New(cfg, signer, Handlers{
		Auth:             handler.NewAuthHandler(authSvc, nil, cfg.OIDC, signer),
		Users:            handler.NewUserHandler(userSvc),
		Templates:        handler.NewTemplateHandler(templateSvc),
		Workspaces:       handler.NewWorkspaceHandler(workspaceSvc),
		RemoteWorkspaces: handler.NewRemoteWorkspaceHandler(remoteSvc),
		Admin:            handler.NewAdminHandler(audit, service.NewSessionService(sessions)),
		Internal:         handler.NewInternalHandler(workspaceSvc),
		Governance:       handler.NewGovernanceHandler(governanceSvc),
		Meta:             handler.NewMetaHandler(),
	}), signer
}

func doJSON(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encoding body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "admin-password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding login response: %v", err)
	}
	return out.Data.AccessToken
}

func TestLoginAndProtectedRoutes(t *testing.T) {
	h, _ := newTestServer(t)

	// No token → 401 problem+json, on every protected route.
	rec := doJSON(t, h, http.MethodGet, "/api/v1/workspaces", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected RFC 7807 content type, got %s", ct)
	}

	// Bad credentials → 401.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "wrong",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad credentials, got %d", rec.Code)
	}

	token := login(t, h)
	rec = doJSON(t, h, http.MethodGet, "/api/v1/auth/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d: %s", rec.Code, rec.Body)
	}
}

func TestWorkspaceLifecycleThroughAPI(t *testing.T) {
	h, _ := newTestServer(t)
	token := login(t, h)

	// Template must exist before a workspace can be created via the API.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/workspaces", token, map[string]string{"templateRef": "xfce"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing template, got %d: %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/workspace-templates", token, map[string]any{
		"name": "xfce", "displayName": "XFCE Desktop", "os": "linux",
		"image": "ghcr.io/xorhub/waas/desktop-xfce:latest", "homeSize": "10Gi",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating template: expected 201, got %d: %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/workspaces", token, map[string]string{
		"templateRef": "xfce", "displayName": "Marc's desktop",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating workspace: expected 201, got %d: %s", rec.Code, rec.Body)
	}
	var created struct {
		Data struct {
			ID    string `json:"id"`
			Phase string `json:"phase"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding workspace: %v", err)
	}
	if created.Data.Phase != "Pending" {
		t.Fatalf("expected Pending phase, got %q", created.Data.Phase)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/workspaces", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing workspaces: expected 200, got %d", rec.Code)
	}
	var listed struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if listed.Meta.Total != 1 || len(listed.Data) != 1 {
		t.Fatalf("expected exactly one workspace, got %+v", listed)
	}

	// Connect must refuse while the workspace is not Running.
	rec = doJSON(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%s/connect", created.Data.ID), token, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 connecting to non-running workspace, got %d: %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, h, http.MethodDelete, "/api/v1/workspaces/"+created.Data.ID, token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting workspace: expected 204, got %d: %s", rec.Code, rec.Body)
	}
}

func TestRBACAndInternalRoutes(t *testing.T) {
	h, _ := newTestServer(t)
	adminToken := login(t, h)

	// Create a plain user, log in as them, verify admin routes are refused.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
		"username": "marc", "password": "marc-password",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating user: expected 201, got %d: %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "marc", "password": "marc-password",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("marc login: expected 200, got %d", rec.Code)
	}
	var out struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding login: %v", err)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/users", out.Data.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin on /users, got %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/audit-logs", out.Data.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin on /audit-logs, got %d", rec.Code)
	}

	// Internal API requires the shared token.
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/sessions/some-id/connection", nil)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on internal route without token, got %d", recorder.Code)
	}
	req.Header.Set("X-Internal-Token", "internal-secret")
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session with valid internal token, got %d", recorder.Code)
	}

	// Audit trail recorded the activity for the admin.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/audit-logs", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list: expected 200, got %d", rec.Code)
	}
	var audit struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &audit); err != nil {
		t.Fatalf("decoding audit list: %v", err)
	}
	if audit.Meta.Total == 0 {
		t.Fatal("expected audit entries to have been recorded")
	}
}
