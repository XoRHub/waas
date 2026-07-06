package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
)

// GovernanceHandler serves the catalog/quota views and the admin
// governance CRUD. Read paths are identity-filtered server-side: the
// portal cannot ask for more than the webhook would allow anyway.
type GovernanceHandler struct {
	svc *service.GovernanceService
}

func NewGovernanceHandler(svc *service.GovernanceService) *GovernanceHandler {
	return &GovernanceHandler{svc: svc}
}

// Catalog handles GET /api/v1/catalog — images the caller may deploy.
func (h *GovernanceHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	images, err := h.svc.Catalog(r.Context(), middleware.Actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, images)
}

// Quota handles GET /api/v1/me/quota — applied policy and remaining room.
func (h *GovernanceHandler) Quota(w http.ResponseWriter, r *http.Request) {
	quota, err := h.svc.Quota(r.Context(), middleware.Actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, quota)
}

// AdminListImages handles GET /api/v1/admin/images.
func (h *GovernanceHandler) AdminListImages(w http.ResponseWriter, r *http.Request) {
	images, err := h.svc.AdminListImages(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, images)
}

// AdminUpsertImage handles PUT /api/v1/admin/images/{name}.
func (h *GovernanceHandler) AdminUpsertImage(w http.ResponseWriter, r *http.Request) {
	var in service.UpsertImageInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	img, err := h.svc.AdminUpsertImage(r.Context(), middleware.Actor(r), chi.URLParam(r, "name"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, img)
}

// AdminToggleImage handles POST /api/v1/admin/images/{name}/enable and
// /disable — the one-click kill switch.
func (h *GovernanceHandler) AdminToggleImage(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		img, err := h.svc.AdminSetImageEnabled(r.Context(), middleware.Actor(r), chi.URLParam(r, "name"), enabled)
		if err != nil {
			fail(w, r, err)
			return
		}
		ok(w, img)
	}
}

// AdminDeleteImage handles DELETE /api/v1/admin/images/{name}.
func (h *GovernanceHandler) AdminDeleteImage(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.AdminDeleteImage(r.Context(), middleware.Actor(r), chi.URLParam(r, "name")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// AdminListPolicies handles GET /api/v1/admin/policies.
func (h *GovernanceHandler) AdminListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.svc.AdminListPolicies(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, policies)
}

// AdminUpsertPolicy handles PUT /api/v1/admin/policies/{name}.
func (h *GovernanceHandler) AdminUpsertPolicy(w http.ResponseWriter, r *http.Request) {
	var in service.UpsertPolicyInput
	if err := decode(r, &in); err != nil {
		fail(w, r, err)
		return
	}
	pol, err := h.svc.AdminUpsertPolicy(r.Context(), middleware.Actor(r), chi.URLParam(r, "name"), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, pol)
}

// AdminDeletePolicy handles DELETE /api/v1/admin/policies/{name}.
func (h *GovernanceHandler) AdminDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.AdminDeletePolicy(r.Context(), middleware.Actor(r), chi.URLParam(r, "name")); err != nil {
		fail(w, r, err)
		return
	}
	noContent(w)
}

// AdminEffectivePolicy handles GET /api/v1/admin/users/{id}/effective-policy
// — the "why is this user governed by that policy" debug view.
func (h *GovernanceHandler) AdminEffectivePolicy(w http.ResponseWriter, r *http.Request) {
	report, err := h.svc.AdminEffectivePolicy(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, report)
}

// AdminUsage handles GET /api/v1/admin/usage — consumption per user.
func (h *GovernanceHandler) AdminUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := h.svc.AdminUsage(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, usage)
}
