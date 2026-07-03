package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
)

// UserHandler serves the admin user-management endpoints.
type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// List handles GET /api/v1/users.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pagination(r)
	users, total, err := h.svc.List(r.Context(), page, pageSize)
	if err != nil {
		fail(w, r, err)
		return
	}
	list(w, users, total, page, pageSize)
}

// Create handles POST /api/v1/users.
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in service.CreateUserInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	user, err := h.svc.Create(r.Context(), middleware.Actor(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	created(w, user)
}

// Get handles GET /api/v1/users/{id}.
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, user)
}

// Update handles PATCH /api/v1/users/{id}.
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in service.UpdateUserInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	user, err := h.svc.Update(r.Context(), middleware.Actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, user)
}

// Delete handles DELETE /api/v1/users/{id}.
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), middleware.Actor(r), chi.URLParam(r, "id")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}
