package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

// AuthHandler serves login (local + optional SSO), profile and the JWKS
// document.
type AuthHandler struct {
	svc     *service.AuthService
	oidc    *service.OIDCService // nil when SSO is not configured
	oidcCfg config.OIDCConfig
	signer  *auth.Signer
}

func NewAuthHandler(svc *service.AuthService, oidcSvc *service.OIDCService, oidcCfg config.OIDCConfig, signer *auth.Signer) *AuthHandler {
	return &AuthHandler{svc: svc, oidc: oidcSvc, oidcCfg: oidcCfg, signer: signer}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if h.oidcCfg.OIDCOnly {
		fail(w, r, apierror.NotFound("local login is disabled — sign in via SSO"))
		return
	}
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

// Providers handles GET /api/v1/auth/providers (public): which login
// methods the login page should offer.
func (h *AuthHandler) Providers(w http.ResponseWriter, _ *http.Request) {
	type oidcInfo struct {
		Enabled  bool   `json:"enabled"`
		Name     string `json:"name,omitempty"`
		StartURL string `json:"startUrl,omitempty"`
	}
	payload := struct {
		Local bool     `json:"local"`
		OIDC  oidcInfo `json:"oidc"`
	}{Local: !h.oidcCfg.OIDCOnly}
	if h.oidc != nil {
		payload.OIDC = oidcInfo{Enabled: true, Name: h.oidcCfg.ProviderName, StartURL: "/api/v1/auth/oidc/start"}
	}
	ok(w, payload)
}

const oidcStateCookie = "waas_oidc_state"

// OIDCStart handles GET /api/v1/auth/oidc/start: pins the state in a
// browser cookie and redirects to the IdP.
func (h *AuthHandler) OIDCStart(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		fail(w, r, apierror.NotFound("SSO login is not configured"))
		return
	}
	authURL, state, err := h.oidc.AuthURL(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     "/api/v1/auth/oidc",
		MaxAge:   int((10 * time.Minute).Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallback handles GET /api/v1/auth/oidc/callback: verifies the state,
// completes the code exchange and hands the platform token to the SPA via
// the URL fragment (never sent to servers or logged by proxies).
func (h *AuthHandler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidc == nil {
		fail(w, r, apierror.NotFound("SSO login is not configured"))
		return
	}
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		fail(w, r, apierror.Unauthorized("SSO state mismatch — restart the login"))
		return
	}
	// One-shot: clear the state cookie whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Path: "/api/v1/auth/oidc", MaxAge: -1, HttpOnly: true})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectToFrontend(w, r, url.Values{"error": {errParam}})
		return
	}
	result, err := h.oidc.Callback(r.Context(), r.URL.Query().Get("code"), middleware.Actor(r).ClientIP)
	if err != nil {
		// Only Problem details are user-facing; anything else stays generic.
		msg := "SSO login failed"
		var p *apierror.Problem
		if errors.As(err, &p) {
			msg = p.Detail
		}
		h.redirectToFrontend(w, r, url.Values{"error": {msg}})
		return
	}
	h.redirectToFrontend(w, r, url.Values{
		"token":     {result.AccessToken},
		"expiresAt": {result.ExpiresAt.Format(time.RFC3339)},
	})
}

// redirectToFrontend sends the browser to the SPA's /auth/callback route
// with the payload in the fragment.
func (h *AuthHandler) redirectToFrontend(w http.ResponseWriter, r *http.Request, values url.Values) {
	base := strings.TrimSuffix(h.oidcCfg.FrontendURL, "/")
	http.Redirect(w, r, base+"/auth/callback#"+values.Encode(), http.StatusFound)
}

// JWKS handles GET /.well-known/jwks.json — the public verification keys
// used by the WebSocket proxy (and any external validator).
func (h *AuthHandler) JWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(h.signer.JWKS())
}
