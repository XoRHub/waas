package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Surface tests for the routes the lifecycle tests don't reach: meta,
// template/user CRUD edges, remote workspaces (with their fail-closed
// policy gate) and the admin governance CRUD. All through the real
// router — status codes and payload contracts, not internals.

func decodeData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding response: %v (%s)", err, body)
	}
	return out.Data
}

func TestMetaEndpoints(t *testing.T) {
	h, _ := newTestServer(t)
	token := login(t, h)

	rec := doJSON(t, h, http.MethodGet, "/api/v1/meta/protocols", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("protocols: %d %s", rec.Code, rec.Body)
	}
	protos := decodeData[[]struct {
		Name   string            `json:"name"`
		Params []json.RawMessage `json:"params"`
	}](t, rec.Body.Bytes())
	if len(protos) == 0 {
		t.Fatal("protocols list must not be empty")
	}
	for _, p := range protos {
		// The contract that once crashed the param forms: params is
		// ALWAYS an array, even for kasmvnc (no registry entries).
		if p.Params == nil {
			t.Fatalf("protocol %s serialized params as null", p.Name)
		}
	}

	if rec := doJSON(t, h, http.MethodGet, "/api/v1/meta/placeholders", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("placeholders: %d", rec.Code)
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/meta/override-fields", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("override-fields: %d", rec.Code)
	}
	if fields := decodeData[[]map[string]string](t, rec.Body.Bytes()); len(fields) == 0 {
		t.Fatal("override-fields must enumerate the governable fields")
	}

	rec = doJSON(t, h, http.MethodGet, "/api/v1/meta/scaffold/workspacepolicy", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("scaffold: %d %s", rec.Code, rec.Body)
	}
	if s := decodeData[map[string]string](t, rec.Body.Bytes()); s["scaffold"] == "" {
		t.Fatal("scaffold payload must carry the YAML skeleton")
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/meta/scaffold/bogus", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown scaffold kind: want 404, got %d", rec.Code)
	}
}

func TestTemplateCRUD(t *testing.T) {
	h, _ := newTestServer(t)
	token := login(t, h)

	create := map[string]any{
		"name": "xfce", "displayName": "XFCE", "os": "linux",
		"image": "ghcr.io/xorhub/waas/desktop-xfce:latest", "homeSize": "10Gi",
	}
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/workspace-templates", token, create); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/workspace-templates", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/workspace-templates/xfce", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	create["displayName"] = "XFCE Desktop"
	rec := doJSON(t, h, http.MethodPut, "/api/v1/workspace-templates/xfce", token, create)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	got := decodeData[map[string]any](t, rec.Body.Bytes())
	if got["displayName"] != "XFCE Desktop" {
		t.Fatalf("update must persist displayName, got %v", got["displayName"])
	}

	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/workspace-templates/xfce", token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/workspace-templates/xfce", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", rec.Code)
	}
}

func TestUserAdminAndProfileRoutes(t *testing.T) {
	h, _ := newTestServer(t)
	admin := login(t, h)

	rec := doJSON(t, h, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"username": "dave", "password": "dave-password",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decodeData[struct {
		ID string `json:"id"`
	}](t, rec.Body.Bytes())

	if rec := doJSON(t, h, http.MethodGet, "/api/v1/users/"+created.ID, admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	// Self-service profile update goes through PATCH /me, not the admin
	// route, and needs no admin role.
	dave := loginAs(t, h, "dave", "dave-password")
	if rec := doJSON(t, h, http.MethodPatch, "/api/v1/me", dave, map[string]any{
		"preferences": map[string]any{"language": "fr"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("profile update: %d %s", rec.Code, rec.Body)
	}

	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/users/"+created.ID, admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/users/"+created.ID, admin, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", rec.Code)
	}
}

func TestRemoteWorkspacesPolicyGateAndLifecycle(t *testing.T) {
	h, _ := newTestServer(t)
	admin := login(t, h)

	// Fail closed: a user with no policy granting remoteWorkspaces is
	// refused, even for the list.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"username": "erin", "password": "erin-password", "groups": []string{"lab"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	erin := loginAs(t, h, "erin", "erin-password")
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/remote-workspaces", erin, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no-policy list: want 403, got %d %s", rec.Code, rec.Body)
	}

	// A policy granting the feature to erin's group opens the gate.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/admin/policies/lab", admin, map[string]any{
		"priority":         10,
		"subjects":         []map[string]any{{"kind": "Group", "name": "lab"}},
		"remoteWorkspaces": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert policy: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/remote-workspaces", erin, nil); rec.Code != http.StatusOK {
		t.Fatalf("policy-granted list: want 200, got %d %s", rec.Code, rec.Body)
	}

	// Admins always pass the gate: full lifecycle.
	rec = doJSON(t, h, http.MethodPost, "/api/v1/remote-workspaces", admin, map[string]any{
		"name": "lab box", "hostname": "10.0.0.5", "port": 22, "protocol": "ssh",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decodeData[struct {
		ID         string `json:"id"`
		MACAddress string `json:"macAddress"`
	}](t, rec.Body.Bytes())

	if rec := doJSON(t, h, http.MethodGet, "/api/v1/remote-workspaces/"+created.ID, admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	// Ownership scoping: another user's machine is invisible, not 403.
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/remote-workspaces/"+created.ID, erin, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other owner's get: want 404, got %d %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, h, http.MethodPut, "/api/v1/remote-workspaces/"+created.ID, admin, map[string]any{
		"name": "lab box", "hostname": "10.0.0.9", "port": 22, "protocol": "ssh",
		"macAddress": "aa:bb:cc:dd:ee:ff",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// Wake: MAC is set but no relay is configured on the platform.
	if rec := doJSON(t, h, http.MethodPost, "/api/v1/remote-workspaces/"+created.ID+"/wake", admin, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("wake without relay: want 503, got %d %s", rec.Code, rec.Body)
	}

	// Fleet view for admins.
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/remote-workspaces", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d %s", rec.Code, rec.Body)
	}

	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/remote-workspaces/"+created.ID, admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/remote-workspaces/"+created.ID, admin, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", rec.Code)
	}
}

func TestGovernanceAdminSurface(t *testing.T) {
	h, _ := newTestServer(t)
	admin := login(t, h)

	// Read views available to every authenticated user.
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/catalog", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/me/quota", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("quota: %d %s", rec.Code, rec.Body)
	}

	// Admin routes are role-gated.
	rec := doJSON(t, h, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"username": "frank", "password": "frank-password",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	frankID := decodeData[struct {
		ID string `json:"id"`
	}](t, rec.Body.Bytes()).ID
	frank := loginAs(t, h, "frank", "frank-password")
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/images", frank, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin on /admin/images: want 403, got %d", rec.Code)
	}

	// Image catalog entry: upsert, kill switch both ways, delete.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/admin/images/chrome", admin, map[string]any{
		"displayName": "Chrome", "image": "docker.io/kasmweb/chrome:1.16.0",
		"protocols": []string{"kasmvnc"}, "architectures": []string{"amd64"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert image: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, http.MethodGet, "/api/v1/admin/images", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list images: %d", rec.Code)
	}
	if imgs := decodeData[[]map[string]any](t, rec.Body.Bytes()); len(imgs) != 1 {
		t.Fatalf("want the one upserted image, got %d", len(imgs))
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/images/chrome/disable", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if img := decodeData[map[string]any](t, rec.Body.Bytes()); img["enabled"] != false {
		t.Fatalf("disable must flip enabled, got %v", img["enabled"])
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/admin/images/chrome/enable", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if img := decodeData[map[string]any](t, rec.Body.Bytes()); img["enabled"] != true {
		t.Fatalf("enable must flip enabled back, got %v", img["enabled"])
	}
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/admin/images/chrome", admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete image: %d %s", rec.Code, rec.Body)
	}

	// Policies + the debug/reporting views.
	rec = doJSON(t, h, http.MethodPut, "/api/v1/admin/policies/devs", admin, map[string]any{
		"priority": 5,
		"subjects": []map[string]any{{"kind": "Group", "name": "dev"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert policy: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/policies", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("list policies: %d", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/users/"+frankID+"/effective-policy", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("effective policy: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/usage", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/groups", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("groups: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/admin/policies/devs", admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
}

func TestWorkspaceAuxiliaryRoutes(t *testing.T) {
	h, _ := newTestServer(t)
	token := login(t, h)

	if rec := doJSON(t, h, http.MethodPost, "/api/v1/workspace-templates", token, map[string]any{
		"name": "xfce", "displayName": "XFCE", "os": "linux",
		"image": "ghcr.io/xorhub/waas/desktop-xfce:latest", "homeSize": "10Gi",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", rec.Code, rec.Body)
	}

	rec := doJSON(t, h, http.MethodGet, "/api/v1/workspaces/namespace-preview?template=xfce&displayName=My+Box", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("namespace preview: %d %s", rec.Code, rec.Body)
	}
	if ns := decodeData[map[string]string](t, rec.Body.Bytes()); ns["namespace"] == "" {
		t.Fatal("namespace preview must resolve a namespace")
	}

	rec = doJSON(t, h, http.MethodPost, "/api/v1/workspaces", token, map[string]string{"templateRef": "xfce"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: %d %s", rec.Code, rec.Body)
	}
	id := decodeData[struct {
		ID string `json:"id"`
	}](t, rec.Body.Bytes()).ID

	if rec := doJSON(t, h, http.MethodGet, "/api/v1/workspaces/"+id, token, nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if rec := doJSON(t, h, http.MethodPost, "/api/v1/workspaces/"+id+"/pause", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, h, http.MethodPost, "/api/v1/workspaces/"+id+"/resume", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body)
	}

	// Aggregated Kubernetes events: contract is an array plus the poll
	// hint, even when the fake cluster has recorded nothing.
	rec = doJSON(t, h, http.MethodGet, "/api/v1/workspaces/"+id+"/events", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}
	events := decodeData[struct {
		Events []json.RawMessage `json:"events"`
	}](t, rec.Body.Bytes())
	if events.Events == nil {
		t.Fatal("events must serialize as an array, never null")
	}

	if rec := doJSON(t, h, http.MethodGet, "/api/v1/volumes", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("volumes: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodGet, "/api/v1/admin/volumes", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin volumes: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, h, http.MethodDelete, "/api/v1/volumes/waas-workspaces/no-such-pvc", token, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown volume: want 404, got %d %s", rec.Code, rec.Body)
	}
}
