package handler

import (
	"encoding/json"
	"net/http"

	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

// AuthHandler serves login, profile and the JWKS document.
type AuthHandler struct {
	svc    *service.AuthService
	signer *auth.Signer
}

func NewAuthHandler(svc *service.AuthService, signer *auth.Signer) *AuthHandler {
	return &AuthHandler{svc: svc, signer: signer}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		fail(w, r, err)
		return
	}
	result, err := h.svc.Login(r.Context(), req.Username, req.Password, middleware.Actor(r).ClientIP)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, result)
}

// Me handles GET /api/v1/auth/me.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, err := h.svc.Me(r.Context(), middleware.Actor(r).ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, user)
}

// JWKS handles GET /.well-known/jwks.json — the public verification keys
// used by the WebSocket proxy (and any external validator).
func (h *AuthHandler) JWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(h.signer.JWKS())
}
