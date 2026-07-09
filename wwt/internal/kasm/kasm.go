// Package kasm reverse-proxies browser sessions to a workspace's KasmVNC
// web endpoint (kasmweb/* images). Unlike the guacd path — one WebSocket
// per session — the KasmVNC client is a whole web app: its HTML, assets
// and /websockify WebSocket all flow through this handler under
// /kasm/{sessionID}/…
//
// The security invariant is the guacd path's, unchanged: nothing is
// proxied before the platform connection JWT is validated, and the
// upstream HTTP Basic credentials (VNC_PW) are injected server-side —
// they never reach the browser. The first request authenticates with
// ?token=… (what the iframe URL carries); the handler answers with a
// session-scoped cookie so the page's subresource and WebSocket requests
// are authenticated too. The cookie value IS the JWT: stateless, same
// expiry, and it never goes upstream (the Cookie header is stripped).
//
// Upstream TLS: KasmVNC images ship `require_ssl: true` with a
// self-signed certificate, so the transport skips verification. The
// hop is pod-to-pod inside the cluster and netpol-guarded; replacing
// the self-signed cert with a cert-manager one is the phase-4 hardening
// documented in docs/studies/kasm-images-feasibility.md.
package kasm

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xorhub/waas/wwt/internal/jwks"
	"github.com/xorhub/waas/wwt/internal/metrics"
	"github.com/xorhub/waas/wwt/internal/proxy"
)

const (
	routePrefix = "/kasm/"
	cookieName  = "waas_kasm"
	// infoTTL bounds how long a resolved ConnectionInfo is reused. Asset
	// bursts (one page load ≈ dozens of requests) must not hammer the
	// internal API, but a paused/deleted workspace must stop resolving
	// quickly.
	infoTTL = 20 * time.Second
)

// Handler serves /kasm/{sessionID}/… by proxying to that session's
// KasmVNC endpoint.
type Handler struct {
	Issuer string
	Keys   *jwks.Client
	API    proxy.APIClient

	transport http.RoundTripper

	mu    sync.Mutex
	infos map[string]cachedInfo
}

type cachedInfo struct {
	info    *proxy.ConnectionInfo
	expires time.Time
}

func NewHandler(issuer string, keys *jwks.Client, api proxy.APIClient) *Handler {
	return &Handler{
		Issuer: issuer,
		Keys:   keys,
		API:    api,
		transport: &http.Transport{
			// Self-signed upstream, in-cluster hop (see package comment).
			// Plain http.Transport with a custom TLSClientConfig also
			// pins the upstream to HTTP/1.1, which the WebSocket upgrade
			// requires.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402
			IdleConnTimeout: 90 * time.Second,
		},
		infos: map[string]cachedInfo{},
	}
}

// ServeHTTP handles GET /kasm/{sessionID}/{path…}?token=….
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sid, rest, ok := splitPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// 1. Authenticate: token query (first hit, from the iframe URL) or
	// the session-scoped cookie (every subsequent page request).
	token := r.URL.Query().Get("token")
	fromQuery := token != ""
	if token == "" {
		if c, err := r.Cookie(cookieName); err == nil {
			token = c.Value
		}
	}
	claims, err := proxy.ValidateConnectionToken(ctx, h.Keys, h.Issuer, token)
	if err != nil {
		slog.WarnContext(ctx, "rejected kasm request", "error", err, "remote", r.RemoteAddr)
		http.Error(w, "invalid connection token", http.StatusUnauthorized)
		return
	}
	// The token is minted for ONE session: a valid token for session A
	// must not reach session B's desktop.
	if claims.SessionID != sid {
		http.Error(w, "token does not match this session", http.StatusForbidden)
		return
	}

	// 2. Resolve the session (short cache: one page load is dozens of
	// asset requests).
	info, err := h.connectionInfo(r, sid)
	if err != nil {
		slog.ErrorContext(ctx, "resolving kasm session", "session", sid, "error", err)
		http.Error(w, "session cannot be connected", http.StatusConflict)
		return
	}
	// Mirror of the guard in the guacd path: each protocol family sticks
	// to its own data plane.
	if info.Protocol != "kasmvnc" {
		http.Error(w, "this session is not a kasmvnc session", http.StatusConflict)
		return
	}

	if fromQuery {
		// Scope the cookie to THIS session's subtree so concurrent
		// sessions (split view) don't clobber each other's token.
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     routePrefix + sid,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	// 3. Proxy. httputil.ReverseProxy relays WebSocket upgrades natively
	// and only returns once the tunnel closes — which is exactly the
	// session-end signal the platform needs.
	isDesktopStream := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if isDesktopStream {
		// The upgraded request only returns once the tunnel closes: its
		// whole lifetime is the desktop stream. Asset requests don't count.
		metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm).Inc()
	}
	h.proxyTo(info, sid, rest).ServeHTTP(w, r)

	if isDesktopStream {
		metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm).Dec()
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.API.EndSession(endCtx, sid)
		h.forget(sid)
		slog.InfoContext(ctx, "kasm session ended", "session", sid, "workspace", claims.WorkspaceID)
	}
}

func (h *Handler) proxyTo(info *proxy.ConnectionInfo, sid, rest string) *httputil.ReverseProxy {
	target := &url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(info.Hostname, strconv.Itoa(int(info.Port))),
	}
	username := info.Username
	if username == "" {
		username = "kasm_user"
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = rest
			pr.Out.Host = target.Host
			// The platform cookie/token must never reach the workspace
			// container; upstream auth is the injected Basic pair.
			pr.Out.Header.Del("Cookie")
			q := pr.Out.URL.Query()
			q.Del("token")
			pr.Out.URL.RawQuery = q.Encode()
			pr.Out.SetBasicAuth(username, info.Password)
			// KasmVNC's WebSocket handshake parser requires an Origin
			// header (browsers always send one, health probes don't).
			if pr.Out.Header.Get("Origin") == "" {
				pr.Out.Header.Set("Origin", "https://"+target.Host)
			}
		},
		Transport: h.transport,
		// The desktop stream is latency-sensitive: flush every write.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("kasm upstream error", "session", sid, "error", err)
			http.Error(w, "desktop unreachable", http.StatusBadGateway)
		},
	}
}

func (h *Handler) connectionInfo(r *http.Request, sid string) (*proxy.ConnectionInfo, error) {
	h.mu.Lock()
	if c, ok := h.infos[sid]; ok && time.Now().Before(c.expires) {
		h.mu.Unlock()
		return c.info, nil
	}
	h.mu.Unlock()

	info, err := h.API.ConnectionInfo(r.Context(), sid)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.infos[sid] = cachedInfo{info: info, expires: time.Now().Add(infoTTL)}
	h.mu.Unlock()
	return info, nil
}

func (h *Handler) forget(sid string) {
	h.mu.Lock()
	delete(h.infos, sid)
	h.mu.Unlock()
}

// splitPath extracts the session ID and the upstream path from
// /kasm/{sid}/{rest…}. A bare /kasm/{sid} (no trailing slash) maps to
// the client's index page.
func splitPath(p string) (sid, rest string, ok bool) {
	if !strings.HasPrefix(p, routePrefix) {
		return "", "", false
	}
	p = strings.TrimPrefix(p, routePrefix)
	sid, rest, _ = strings.Cut(p, "/")
	if sid == "" {
		return "", "", false
	}
	return sid, "/" + rest, true
}
