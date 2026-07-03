package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
)

// TemplateHandler serves /api/v1/workspace-templates.
type TemplateHandler struct {
	svc *service.TemplateService
}

func NewTemplateHandler(svc *service.TemplateService) *TemplateHandler {
	return &TemplateHandler{svc: svc}
}

// List handles GET /api/v1/workspace-templates.
func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	templates, err := h.svc.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, templates, len(templates), 1, len(templates))
}

// Get handles GET /api/v1/workspace-templates/{name}.
func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	tpl, err := h.svc.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, tpl)
}

// Create handles POST /api/v1/workspace-templates.
func (h *TemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in service.TemplateInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	tpl, err := h.svc.Create(r.Context(), middleware.Actor(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	created(w, tpl)
}

// Update handles PUT /api/v1/workspace-templates/{name}.
func (h *TemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in service.TemplateInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	tpl, err := h.svc.Update(r.Context(), middleware.Actor(r), chi.URLParam(r, "name"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, tpl)
}

// Delete handles DELETE /api/v1/workspace-templates/{name}.
func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "name")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}
