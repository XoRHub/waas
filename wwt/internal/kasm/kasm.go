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
// the self-signed cert with a cert-manager one is the pending hardening
// documented in docs/kasmvnc.md.
package kasm

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
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
	// reconnectGrace is how long a session outlives its last desktop
	// stream: the KasmVNC client reconnects on its own after a network
	// blip, which only works while the api-server still resolves the
	// session (an ended one answers 409).
	reconnectGrace = 30 * time.Second
)

// Handler serves /kasm/{sessionID}/… by proxying to that session's
// KasmVNC endpoint.
type Handler struct {
	Issuer string
	Keys   *jwks.Client
	API    proxy.APIClient

	transport http.RoundTripper
	endGrace  time.Duration

	mu      sync.Mutex
	infos   map[string]cachedInfo
	streams map[string]*sessionStreams
}

// sessionStreams tracks one session's upgraded WebSockets: the client
// may hold several at once (multi-monitor screen.html windows), and the
// session only ends once the last one has been gone for endGrace.
type sessionStreams struct {
	live int
	// gen invalidates a pending end: bumped on every open and every
	// drop to zero, so a timer only fires the end it was armed for.
	gen     uint64
	pending *time.Timer
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
		endGrace: reconnectGrace,
		infos:    map[string]cachedInfo{},
		streams:  map[string]*sessionStreams{},
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

	// KasmVNC's downloads API (/api/downloads) lists — and serves — files
	// from the session's downloads directory, and the kasmweb images
	// expose the same directory a second way: www/Downloads/Downloads is
	// a symlink to ~/Downloads, which the built-in HTTP server lists and
	// serves (verified on kasmweb 1.19.0). Both are the session→browser
	// file egress the guacd path gates behind TierPlatform for
	// enable-sftp/enable-drive ("until the file-transfer feature ships
	// with its own policy gate", operator/pkg/params/params.go). No such
	// gate exists for kasmvnc yet, so both are blocked outright to keep
	// file egress governed identically across protocols. The rest of the
	// kasmvnc /api surface stays open by design: a WaaS kasmvnc session
	// is single-user and self-owned (kasm_user holds the owner role),
	// with no third party to protect the user from — see docs/kasmvnc.md.
	if isDownloadsPath(rest) {
		http.Error(w, "file transfer is not enabled for this session", http.StatusForbidden)
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
	// session-end signal the platform needs (after the reconnect grace).
	isDesktopStream := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if isDesktopStream {
		// The upgraded request only returns once the tunnel closes: its
		// whole lifetime is the desktop stream. Asset requests don't count.
		metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm).Inc()
		h.streamOpened(sid)
	}
	h.proxyTo(info, sid, rest).ServeHTTP(w, r)

	if isDesktopStream {
		metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm).Dec()
		h.streamClosed(sid, claims.WorkspaceID)
	}
}

// streamOpened counts a new desktop stream and cancels a pending end
// (the client reconnected within the grace).
func (h *Handler) streamOpened(sid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[sid]
	if s == nil {
		s = &sessionStreams{}
		h.streams[sid] = s
	}
	if s.pending != nil {
		s.pending.Stop()
		s.pending = nil
	}
	s.gen++
	s.live++
}

// streamClosed arms the session end once its last stream is gone.
// Pending ends live in memory only: a wwt restart within the grace
// loses them, the same exposure as a wwt crash mid-stream (see the
// api-server's SessionSweeper).
func (h *Handler) streamClosed(sid, workspaceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[sid]
	s.live--
	if s.live > 0 {
		return
	}
	s.gen++
	gen := s.gen
	s.pending = time.AfterFunc(h.endGrace, func() { h.endSession(sid, workspaceID, s, gen) })
}

// endSession ends the session unless a stream reopened since the timer
// was armed (Stop can lose the race against an already-firing timer).
func (h *Handler) endSession(sid, workspaceID string, s *sessionStreams, gen uint64) {
	h.mu.Lock()
	if h.streams[sid] != s || s.gen != gen {
		h.mu.Unlock()
		return
	}
	delete(h.streams, sid)
	delete(h.infos, sid)
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.API.EndSession(ctx, sid)
	slog.Info("kasm session ended", "session", sid, "workspace", workspaceID)
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

// isDownloadsPath reports whether an upstream path targets the session's
// downloads folder: KasmVNC's downloads API (/api/downloads) or the
// kasmweb images' www/Downloads symlink (/Downloads), with any subpath.
// The path arrives already URL-decoded from splitPath (leading slash
// guaranteed); it is normalized and lowercased before matching so
// case, trailing-slash, dot, and double-slash variants can't slip a
// request past the block.
func isDownloadsPath(p string) bool {
	clean := strings.ToLower(path.Clean(p))
	for _, prefix := range []string{"/api/downloads", "/downloads"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	return false
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
