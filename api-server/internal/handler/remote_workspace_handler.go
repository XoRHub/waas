package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
)

// RemoteWorkspaceHandler serves /api/v1/remote-workspaces — the
// out-of-cluster machines a user registered. Distinct from /workspaces
// on purpose: different entity, different lifecycle, policy-gated.
type RemoteWorkspaceHandler struct {
	svc *service.RemoteWorkspaceService
}

func NewRemoteWorkspaceHandler(svc *service.RemoteWorkspaceService) *RemoteWorkspaceHandler {
	return &RemoteWorkspaceHandler{svc: svc}
}

// List handles GET /api/v1/remote-workspaces.
func (h *RemoteWorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context(), middleware.Actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, items, len(items), 1, len(items))
}

// Create handles POST /api/v1/remote-workspaces.
func (h *RemoteWorkspaceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in service.RemoteWorkspaceInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	rw, err := h.svc.Create(r.Context(), middleware.Actor(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	created(w, rw)
}

// Get handles GET /api/v1/remote-workspaces/{id}.
func (h *RemoteWorkspaceHandler) Get(w http.ResponseWriter, r *http.Request) {
	rw, err := h.svc.Get(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, rw)
}

// Update handles PUT /api/v1/remote-workspaces/{id}.
func (h *RemoteWorkspaceHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in service.RemoteWorkspaceInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	rw, err := h.svc.Update(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, rw)
}

// Delete handles DELETE /api/v1/remote-workspaces/{id}.
func (h *RemoteWorkspaceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "id")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// Connect handles POST /api/v1/remote-workspaces/{id}/connect.
func (h *RemoteWorkspaceHandler) Connect(w http.ResponseWriter, r *http.Request) {
	var in service.ConnectInput
	if r.ContentLength > 0 {
		if err := decode(r, &in); err != nil {
			fail(w, r, err)
			return
		}
	}
	res, err := h.svc.Connect(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, res)
}
