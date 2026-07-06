package handler

import (
	"net/http"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/repository"
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

// AuditList handles GET /api/v1/audit-logs with server-side pagination
// and optional filters: ?actor= (username substring), ?action= (prefix,
// e.g. "workspace."), ?from=/?to= (RFC3339 or YYYY-MM-DD).
func (h *AdminHandler) AuditList(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pagination(r)
	q := r.URL.Query()
	filter := repository.AuditFilter{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
	}
	var err error
	if filter.From, err = parseAuditTime(q.Get("from"), false); err != nil {
		fail(w, r, err)
		return
	}
	if filter.To, err = parseAuditTime(q.Get("to"), true); err != nil {
		fail(w, r, err)
		return
	}
	entries, total, err := h.audit.List(r.Context(), filter, page, pageSize)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, entries, total, page, pageSize)
}

// parseAuditTime accepts RFC3339 or a plain date; a plain "to" date is
// pushed to end-of-day so the range is inclusive.
func parseAuditTime(v string, endOfDay bool) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts, nil
	}
	ts, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, apierror.BadRequest("time filters accept RFC3339 or YYYY-MM-DD, got " + v)
	}
	if endOfDay {
		ts = ts.Add(24*time.Hour - time.Nanosecond)
	}
	return ts, nil
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
