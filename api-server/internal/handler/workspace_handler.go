package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
)

// WorkspaceHandler serves /api/v1/workspaces.
type WorkspaceHandler struct {
	svc *service.WorkspaceService
}

func NewWorkspaceHandler(svc *service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
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

// List handles GET /api/v1/workspaces.
func (h *WorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaces, err := h.svc.List(r.Context(), middleware.Actor(r))
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

// Delete handles DELETE /api/v1/workspaces/{id}.
func (h *WorkspaceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "id")); err != nil {
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
