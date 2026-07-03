package handler

import (
	"net/http"

	"github.com/xorhub/waas/api-server/internal/service"
)

// AdminHandler serves the admin-only observability endpoints (session
// history, audit trail).
type AdminHandler struct {
	audit    *service.AuditService
	sessions *service.SessionService
}

func NewAdminHandler(audit *service.AuditService, sessions *service.SessionService) *AdminHandler {
	return &AdminHandler{audit: audit, sessions: sessions}
}

// AuditList handles GET /api/v1/audit-logs.
func (h *AdminHandler) AuditList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pagination(r)
	entries, total, err := h.audit.List(r.Context(), page, pageSize)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, entries, total, page, pageSize)
}

// SessionList handles GET /api/v1/sessions.
func (h *AdminHandler) SessionList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pagination(r)
	sessions, total, err := h.sessions.List(r.Context(), page, pageSize)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, sessions, total, page, pageSize)
}
