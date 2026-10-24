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

	"github.com/xorhub/waas/shared/auth"
	"github.com/xorhub/waas/shared/kasmagent"
	"github.com/xorhub/waas/wwt/internal/jwks"
	"github.com/xorhub/waas/wwt/internal/metrics"
	"github.com/xorhub/waas/wwt/internal/proxy"
)

const (
	routePrefix = "/kasm/"
	cookieName  = "waas_kasm"
	// agentPrefix is the subtree of /kasm/{sid}/ served by the kasm-agent
	// sidecar instead of KasmVNC (/kasm/{sid}/waas/files…): the policy-gated
	// file transfer, routed to the same host on the agent's port.
	agentPrefix = "/waas"
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
	// agentPort is kasmagent.Port; a field so tests can point at a fake.
	agentPort int32

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
		endGrace:  reconnectGrace,
		agentPort: kasmagent.Port,
		infos:     map[string]cachedInfo{},
		streams:   map[string]*sessionStreams{},
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
	// A NUL truncates the upstream's own percent-decoder: /Downloads%00x
	// decodes here to "/Downloads\x00x" — past isDownloadsPath's
	// exact/prefix match — but upstream reads it as "/Downloads" and
	// serves the symlinked listing anyway. Reject outright.
	if strings.ContainsRune(rest, 0) {
		http.Error(w, "invalid request path", http.StatusBadRequest)
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

	// File transfer goes through the kasm-agent sidecar, gated on the
	// token's policy grant — the governed path. Same host (the workspace
	// Service), the agent's port, the same injected Basic pair.
	if agentPath, ok := strings.CutPrefix(rest, agentPrefix); ok && (agentPath == "" || agentPath[0] == '/') {
		agentPath = path.Clean("/" + agentPath)
		if status, msg := fileRouteStatus(claims.FileTransfer, r.Method, agentPath); status != 0 {
			http.Error(w, msg, status)
			return
		}
		target := &url.URL{Scheme: "http", Host: net.JoinHostPort(info.Hostname, strconv.Itoa(int(h.agentPort)))}
		// 503 rather than 502: the sidecar is optional (operator.kasmAgent
		// off, or a workspace started before it was enabled).
		h.proxyTo(target, info, sid, agentPath, http.StatusServiceUnavailable, "file transfer is unavailable for this session").ServeHTTP(w, r)
		return
	}

	// KasmVNC's downloads API (/api/downloads) lists — and serves — files
	// from the session's downloads directory, and the kasmweb images
	// expose the same directory a second way: www/Downloads/Downloads is
	// a symlink to ~/Downloads, which the built-in HTTP server lists and
	// serves (verified on kasmweb 1.19.0). Both are the session→browser
	// file egress the guacd path gates behind TierPlatform for
	// enable-sftp/enable-drive ("until the file-transfer feature ships
	// with its own policy gate", operator/pkg/params/params.go). They
	// stay blocked outright even with a download grant: the governed
	// route is the sidecar above (one auditable surface, no symlinked
	// directory listings). The rest of the kasmvnc /api surface stays
	// open by design: a WaaS kasmvnc session is single-user and
	// self-owned (kasm_user holds the owner role), with no third party to
	// protect the user from — see docs/kasmvnc.md.
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
		// ServeHTTP can panic without unwinding past this point (e.g.
		// http.ErrAbortHandler, which net/http recovers silently): defer
		// the close accounting so the stream count never leaks.
		defer func() {
			metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm).Dec()
			h.streamClosed(sid, claims.WorkspaceID)
		}()
	}
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort(info.Hostname, strconv.Itoa(int(info.Port)))}
	h.proxyTo(target, info, sid, rest, http.StatusBadGateway, "desktop unreachable").ServeHTTP(w, r)
}

// fileRouteStatus gates a kasm-agent route on the connection token's
// file-transfer grant: 0 = allowed, otherwise the status and message to
// answer. Fail closed — an unknown route is 404 whatever the grant, the
// list needs either direction, download and upload their own, and each
// route has exactly one method (so a POST can never ride a download
// grant into the upload route).
func fileRouteStatus(grant auth.FileTransferGrant, method, p string) (int, string) {
	var allowed bool
	var want string
	switch p {
	case kasmagent.PathList:
		allowed, want = grant.Upload || grant.Download, http.MethodGet
	case kasmagent.PathDownload:
		allowed, want = grant.Download, http.MethodGet
	case kasmagent.PathUpload:
		allowed, want = grant.Upload, http.MethodPost
	default:
		return http.StatusNotFound, "not found"
	}
	if !allowed {
		return http.StatusForbidden, "file transfer is not enabled for this session"
	}
	if method != want {
		return http.StatusMethodNotAllowed, "method not allowed"
	}
	return 0, ""
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
// Pending ends live in memory only: a wwt restart or crash within the
// grace loses them, and the api-server's SessionSweeper is NOT a safety
// net for that — it only reaps sessions whose workspace was deleted, so
// a lost end leaves the row open (and idle-suspend blocked) until the
// workspace itself goes away.
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

// proxyTo relays one request to target (KasmVNC itself, or the kasm-agent
// sidecar on the same host) with the session's Basic pair injected;
// unreachableStatus/unreachableMsg is what the browser sees when the
// upstream does not answer.
func (h *Handler) proxyTo(target *url.URL, info *proxy.ConnectionInfo, sid, rest string, unreachableStatus int, unreachableMsg string) *httputil.ReverseProxy {
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
				pr.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
		},
		Transport: h.transport,
		// The desktop stream is latency-sensitive: flush every write.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("kasm upstream error", "session", sid, "upstream", target.Host, "error", err)
			http.Error(w, unreachableMsg, unreachableStatus)
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
