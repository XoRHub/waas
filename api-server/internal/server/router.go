// Package server assembles the chi router from the injected handlers.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/handler"
	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/shared/auth"
)

// Handlers groups the injectable handlers the router mounts.
type Handlers struct {
	Auth       *handler.AuthHandler
	Users      *handler.UserHandler
	Templates  *handler.TemplateHandler
	Workspaces *handler.WorkspaceHandler
	Admin      *handler.AdminHandler
	Internal   *handler.InternalHandler
	Governance *handler.GovernanceHandler
}

// New builds the full route tree. Every /api/v1 route except login sits
// behind the JWT middleware — no bypass routes.
func New(cfg *config.Config, signer *auth.Signer, h Handlers) http.Handler {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	if len(cfg.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(cfg.CORSAllowedOrigins))
	}

	// Liveness/readiness (HTTP probes only; the image has no shell).
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Get("/.well-known/jwks.json", h.Auth.JWKS)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", h.Auth.Login)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(signer, cfg.JWTIssuer))

			r.Get("/auth/me", h.Auth.Me)
			r.Patch("/me", h.Users.UpdateProfile)

			r.Route("/workspaces", func(r chi.Router) {
				r.Get("/", h.Workspaces.List)
				r.Post("/", h.Workspaces.Create)
				r.Get("/{id}", h.Workspaces.Get)
				r.Delete("/{id}", h.Workspaces.Delete)
				r.Post("/{id}/pause", h.Workspaces.Pause)
				r.Post("/{id}/resume", h.Workspaces.Resume)
				r.Post("/{id}/connect", h.Workspaces.Connect)
			})

			// Governance, user side: catalog filtered to what the
			// caller may deploy, and their applied policy/quota.
			r.Get("/catalog", h.Governance.Catalog)
			r.Get("/me/quota", h.Governance.Quota)

			r.Route("/workspace-templates", func(r chi.Router) {
				r.Get("/", h.Templates.List)
				r.Get("/{name}", h.Templates.Get)
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireAdmin)
					r.Post("/", h.Templates.Create)
					r.Put("/{name}", h.Templates.Update)
					r.Delete("/{name}", h.Templates.Delete)
				})
			})

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Route("/users", func(r chi.Router) {
					r.Get("/", h.Users.List)
					r.Post("/", h.Users.Create)
					r.Get("/{id}", h.Users.Get)
					r.Patch("/{id}", h.Users.Update)
					r.Delete("/{id}", h.Users.Delete)
				})
				r.Get("/audit-logs", h.Admin.AuditList)
				r.Get("/sessions", h.Admin.SessionList)

				// Governance, admin side: catalog and policy CRUD write
				// straight to the CRDs (ArgoCD only bootstraps them).
				r.Route("/admin", func(r chi.Router) {
					r.Get("/images", h.Governance.AdminListImages)
					r.Put("/images/{name}", h.Governance.AdminUpsertImage)
					r.Post("/images/{name}/enable", h.Governance.AdminToggleImage(true))
					r.Post("/images/{name}/disable", h.Governance.AdminToggleImage(false))
					r.Delete("/images/{name}", h.Governance.AdminDeleteImage)
					r.Get("/policies", h.Governance.AdminListPolicies)
					r.Put("/policies/{name}", h.Governance.AdminUpsertPolicy)
					r.Delete("/policies/{name}", h.Governance.AdminDeletePolicy)
					r.Get("/usage", h.Governance.AdminUsage)
				})
			})
		})
	})

	// Service-to-service API for the WebSocket proxy. Cluster-internal only.
	r.Route("/internal/v1", func(r chi.Router) {
		r.Use(middleware.Internal(cfg.InternalToken))
		r.Get("/sessions/{id}/connection", h.Internal.ConnectionInfo)
		r.Post("/sessions/{id}/end", h.Internal.EndSession)
	})

	return r
}
