package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/service"
)

// InternalHandler serves the service-to-service API consumed by the
// WebSocket proxy. It is guarded by the internal token middleware and must
// never be exposed through the public ingress.
type InternalHandler struct {
	workspaces *service.WorkspaceService
}

func NewInternalHandler(workspaces *service.WorkspaceService) *InternalHandler {
	return &InternalHandler{workspaces: workspaces}
}

// ConnectionInfo handles GET /internal/v1/sessions/{id}/connection: resolves
// a validated session into guacd connection parameters (host, port,
// protocol, desktop credentials).
func (h *InternalHandler) ConnectionInfo(w http.ResponseWriter, r *http.Request) {
	info, err := h.workspaces.ConnectionInfo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, info)
}

// EndSession handles POST /internal/v1/sessions/{id}/end, called by the
// proxy when the desktop stream closes.
func (h *InternalHandler) EndSession(w http.ResponseWriter, r *http.Request) {
	if err := h.workspaces.EndSession(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}
