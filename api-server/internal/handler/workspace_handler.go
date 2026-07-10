package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/service"
)

// WorkspaceHandler serves /api/v1/workspaces.
type WorkspaceHandler struct {
	svc *service.WorkspaceService
	// eventsPollSeconds is handed to the events panel so its refresh
	// cadence is server-configured (WAAS_EVENTS_POLL_INTERVAL).
	eventsPollSeconds int
}

func NewWorkspaceHandler(svc *service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc, eventsPollSeconds: 10}
}

// WithEventsPollInterval overrides the events panel refresh cadence.
func (h *WorkspaceHandler) WithEventsPollInterval(d time.Duration) *WorkspaceHandler {
	if d > 0 {
		h.eventsPollSeconds = int(d.Seconds())
	}
	return h
}

// Events handles GET /api/v1/workspaces/{id}/events: the aggregated
// Kubernetes events of the workspace and its children, authorization
// enforced by the service (owner or admin).
func (h *WorkspaceHandler) Events(w http.ResponseWriter, r *http.Request) {
	events, err := h.svc.Events(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	if events == nil {
		events = []model.WorkspaceEvent{}
	}
	ok(w, map[string]any{"events": events, "pollIntervalSeconds": h.eventsPollSeconds})
}

// NamespacePreview handles GET /api/v1/workspaces/namespace-preview
// ?template=&displayName= — the namespace a creation WOULD land in for
// the caller, resolved server-side (the UI displays, never computes).
func (h *WorkspaceHandler) NamespacePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	namespace, err := h.svc.NamespacePreview(r.Context(), middleware.Actor(r), q.Get("template"), q.Get("displayName"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, map[string]string{"namespace": namespace})
}

// List handles GET /api/v1/workspaces — the caller's OWN workspaces,
// whatever their role (the admin fleet has its own route).
func (h *WorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaces, err := h.svc.List(r.Context(), middleware.Actor(r), false)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, workspaces, len(workspaces), 1, len(workspaces))
}

// AdminList handles GET /api/v1/admin/workspaces — every user's
// workspaces, OwnerUsername resolved (the fleet view groups by owner).
func (h *WorkspaceHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	workspaces, err := h.svc.List(r.Context(), middleware.Actor(r), true)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, workspaces, len(workspaces), 1, len(workspaces))
}

// Create handles POST /api/v1/workspaces.
func (h *WorkspaceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in service.CreateWorkspaceInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	ws, err := h.svc.Create(r.Context(), middleware.Actor(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	created(w, ws)
}

// Get handles GET /api/v1/workspaces/{id}.
func (h *WorkspaceHandler) Get(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.Get(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, ws)
}

// Delete handles DELETE /api/v1/workspaces/{id}?keepVolume=true|false.
// Absent = keep: the home volume is only deleted on an explicit opt-out
// (the frontend dialog always sends the parameter).
func (h *WorkspaceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	keepVolume := r.URL.Query().Get("keepVolume") != "false"
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), keepVolume, false); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// AdminDelete handles DELETE /api/v1/admin/workspaces/{id}?keepVolume=…
// — fleet cleanup of any user's workspace, audited with via=admin. The
// fleet UI always sends keepVolume=true: destroying user data goes
// through the volumes tab.
func (h *WorkspaceHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	keepVolume := r.URL.Query().Get("keepVolume") != "false"
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), keepVolume, true); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// ListVolumes handles GET /api/v1/volumes — the caller's retained
// volumes (name, size, origin workspace, retained date).
func (h *WorkspaceHandler) ListVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := h.svc.ListRetainedVolumes(r.Context(), middleware.Actor(r), false)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, vols, len(vols), 1, len(vols))
}

// DeleteVolume handles DELETE /api/v1/volumes/{namespace}/{name}.
func (h *WorkspaceHandler) DeleteVolume(w http.ResponseWriter, r *http.Request) {
	err := h.svc.DeleteRetainedVolume(r.Context(), middleware.Actor(r),
		chi.URLParam(r, "namespace"), chi.URLParam(r, "name"), false)
	if err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// AdminListVolumes handles GET /api/v1/admin/volumes — every user's
// retained volumes.
func (h *WorkspaceHandler) AdminListVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := h.svc.ListRetainedVolumes(r.Context(), middleware.Actor(r), true)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, vols, len(vols), 1, len(vols))
}

// AdminDeleteVolume handles DELETE /api/v1/admin/volumes/{namespace}/{name}
// (audited with via=admin).
func (h *WorkspaceHandler) AdminDeleteVolume(w http.ResponseWriter, r *http.Request) {
	err := h.svc.DeleteRetainedVolume(r.Context(), middleware.Actor(r),
		chi.URLParam(r, "namespace"), chi.URLParam(r, "name"), true)
	if err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// Pause handles POST /api/v1/workspaces/{id}/pause.
func (h *WorkspaceHandler) Pause(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.SetPaused(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), true)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, ws)
}

// Resume handles POST /api/v1/workspaces/{id}/resume.
func (h *WorkspaceHandler) Resume(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.SetPaused(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), false)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, ws)
}

// UpdateOverrides handles PATCH /api/v1/workspaces/{id}/overrides: the
// runtime reconfiguration of an instantiated workspace (env, node
// placement, resources). Each provided field replaces the stored
// override wholesale; the admission webhook is the single judge of what
// the caller may override.
func (h *WorkspaceHandler) UpdateOverrides(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateOverridesInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	ws, err := h.svc.UpdateOverrides(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, ws)
}

// Reload handles POST /api/v1/workspaces/{id}/reload: one immediate
// convergence boundary — the desktop restarts on its up-to-date
// configuration. Never touches the pause intent or the schedule.
func (h *WorkspaceHandler) Reload(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.Reload(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, ws)
}

// Resize handles POST /api/v1/workspaces/{id}/resize: change the RUNNING
// desktop's resolution by executing the image's waas-resize helper in
// the pod. WaaS-specific mechanism, not guacd's native resize — see
// docs/session-resize.md.
func (h *WorkspaceHandler) Resize(w http.ResponseWriter, r *http.Request) {
	var in service.ResizeInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	if err := h.svc.Resize(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// KasmVNCConfig handles GET /api/v1/workspaces/{id}/kasmvnc-config: the
// effective kasmvnc.yaml the operator materialized for this workspace
// (admin template config + policy clipboard layer), read-only — there is
// deliberately no write counterpart, editing stays admin-only on the
// template.
func (h *WorkspaceHandler) KasmVNCConfig(w http.ResponseWriter, r *http.Request) {
	config, err := h.svc.EffectiveKasmVNCConfig(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, map[string]string{"config": config})
}

// Connect handles POST /api/v1/workspaces/{id}/connect: records a session
// and returns the short-lived connection token for the WebSocket proxy.
// The body is optional: {protocol, params} picks a non-default protocol
// and overrides the template's user-tunable guacd parameters.
func (h *WorkspaceHandler) Connect(w http.ResponseWriter, r *http.Request) {
	var in service.ConnectInput
	if r.ContentLength > 0 {
		if err := decode(r, &in); err != nil {
			fail(w, r, err)
			return
		}
	}
	result, err := h.svc.Connect(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	created(w, result)
}
