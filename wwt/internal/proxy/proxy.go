// Package proxy bridges browser WebSockets to guacd. Security invariant:
// the connection JWT is fully validated BEFORE any TCP connection to guacd
// is opened — guacd itself has no authentication.
package proxy

import (
	"bufio"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/xorhub/waas/shared/auth"
	"github.com/xorhub/waas/wwt/internal/guac"
	"github.com/xorhub/waas/wwt/internal/jwks"
)

// APIClient is the slice of the API server's internal API the proxy needs.
type APIClient interface {
	ConnectionInfo(ctx context.Context, sessionID string) (*ConnectionInfo, error)
	EndSession(ctx context.Context, sessionID string)
}

// ConnectionInfo mirrors the API server's internal payload.
type ConnectionInfo struct {
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Port     int32  `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Params are extra guacd connection parameters (template + vetted
	// user overrides), resolved server-side.
	Params map[string]string `json:"params,omitempty"`
}

// Handler upgrades /ws requests and relays them to guacd.
type Handler struct {
	Issuer      string
	Keys        *jwks.Client
	API         APIClient
	GuacdAddr   string
	DialTimeout time.Duration

	upgrader websocket.Upgrader
}

func NewHandler(issuer string, keys *jwks.Client, api APIClient, guacdAddr string) *Handler {
	return &Handler{
		Issuer:      issuer,
		Keys:        keys,
		API:         api,
		GuacdAddr:   guacdAddr,
		DialTimeout: 10 * time.Second,
		upgrader: websocket.Upgrader{
			Subprotocols: []string{"guacamole"},
			// The browser origin is enforced upstream (same-origin ingress);
			// the real gate is the signed connection token below.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// ServeHTTP handles GET /ws?token=…&width=…&height=…&dpi=….
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Validate the token first. No valid token, no guacd connection —
	// this check is the entire security model of guacd.
	claims, err := h.validateToken(ctx, r.URL.Query().Get("token"))
	if err != nil {
		slog.WarnContext(ctx, "rejected connection", "error", err, "remote", r.RemoteAddr)
		http.Error(w, "invalid connection token", http.StatusUnauthorized)
		return
	}

	// 2. Resolve the session into connection parameters (server-side only;
	// desktop credentials never transit through the browser).
	info, err := h.API.ConnectionInfo(ctx, claims.SessionID)
	if err != nil {
		slog.ErrorContext(ctx, "resolving session", "session", claims.SessionID, "error", err)
		http.Error(w, "session cannot be connected", http.StatusConflict)
		return
	}

	// kasmvnc sessions are reverse-proxied by the /kasm endpoint — guacd
	// cannot speak to a KasmVNC server. Refusing here keeps a stolen
	// token from ever confusing one data path for the other.
	if info.Protocol == "kasmvnc" {
		http.Error(w, "kasmvnc sessions connect through /kasm, not /ws", http.StatusConflict)
		return
	}

	// 3. Only now dial guacd and run the handshake.
	conn, err := net.DialTimeout("tcp", h.GuacdAddr, h.DialTimeout)
	if err != nil {
		slog.ErrorContext(ctx, "dialing guacd", "error", err)
		http.Error(w, "desktop gateway unavailable", http.StatusBadGateway)
		return
	}

	params := guac.ConnectionParams{
		Protocol:     info.Protocol,
		Hostname:     info.Hostname,
		Port:         info.Port,
		Username:     info.Username,
		Password:     info.Password,
		Extra:        info.Params,
		Width:        intQuery(r, "width"),
		Height:       intQuery(r, "height"),
		DPI:          intQuery(r, "dpi"),
		ClientLayout: r.URL.Query().Get("layout"),
	}
	connID, guacdReader, err := guac.Handshake(conn, params)
	if err != nil {
		conn.Close()
		slog.ErrorContext(ctx, "guacd handshake failed", "session", claims.SessionID, "error", err)
		http.Error(w, "desktop connection failed", http.StatusBadGateway)
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		conn.Close()
		return
	}

	slog.InfoContext(ctx, "session connected",
		"session", claims.SessionID, "workspace", claims.WorkspaceID, "guacdConnection", connID,
		"clipboardCopy", claims.Clipboard.Copy, "clipboardPaste", claims.Clipboard.Paste)
	h.pipe(ws, conn, guacdReader, guac.NewClipboardFilter(claims.Clipboard.Copy, claims.Clipboard.Paste))

	// Best-effort bookkeeping once the stream closes.
	endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.API.EndSession(endCtx, claims.SessionID)
	slog.InfoContext(ctx, "session ended", "session", claims.SessionID)
}

func (h *Handler) validateToken(ctx context.Context, token string) (*auth.ConnectionClaims, error) {
	return ValidateConnectionToken(ctx, h.Keys, h.Issuer, token)
}

// ValidateConnectionToken verifies a connection JWT against the API
// server's JWKS. Shared by the guacd tunnel and the kasm reverse proxy —
// both paths gate on the exact same token semantics.
func ValidateConnectionToken(ctx context.Context, keys *jwks.Client, issuer, token string) (*auth.ConnectionClaims, error) {
	if token == "" {
		return nil, fmt.Errorf("missing token")
	}
	// Peek at the (unverified) header to select the key, then verify
	// signature and claims for real with the shared validator.
	parser := jwt.NewParser()
	unverified, _, err := parser.ParseUnverified(token, &auth.ConnectionClaims{})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}
	kid, _ := unverified.Header["kid"].(string)
	var key *rsa.PublicKey
	if key, err = keys.Key(ctx, kid); err != nil {
		return nil, err
	}
	return auth.VerifyConnectionToken(token, issuer, key)
}

// pipe relays both directions until either side closes.
//
// guacd → browser is re-framed on instruction boundaries: the JS tunnel
// parses each WebSocket message as complete instructions, while TCP reads
// split anywhere. Forwarding raw chunks corrupts big frames (black artifact
// regions) and eventually closes the tunnel, dropping all user input.
//
// The clipboard filter enforces the token's policy grant on both
// directions and handles the overlay's live toggles (tunnel-internal
// "waas-clipboard" controls).
func (h *Handler) pipe(ws *websocket.Conn, guacd net.Conn, guacdReader *bufio.Reader, clipboard *guac.ClipboardFilter) {
	done := make(chan struct{}, 2)

	// Both goroutines write to the WebSocket (frames, ping echoes, acks)
	// and gorilla/websocket forbids concurrent writers.
	var wsMu sync.Mutex
	writeWS := func(data []byte) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		return ws.WriteMessage(websocket.TextMessage, data)
	}

	// guacd → browser
	go func() {
		defer func() { done <- struct{}{} }()
		var framer guac.Framer
		buf := make([]byte, 32*1024)
		for {
			n, err := guacdReader.Read(buf)
			if n > 0 {
				frame, ferr := framer.Push(buf[:n])
				if ferr != nil {
					slog.Error("guacd stream corrupted", "error", ferr)
					return
				}
				if len(frame) > 0 {
					frame, ferr = clipboard.FilterToBrowser(frame)
					if ferr != nil {
						slog.Error("filtering guacd stream", "error", ferr)
						return
					}
				}
				if len(frame) > 0 {
					if err := writeWS(frame); err != nil {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// browser → guacd
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				return
			}
			// Tunnel-internal messages (zero-length opcode) are for the
			// endpoint, not guacd: answer pings with an identical ping,
			// process WaaS controls, swallow anything else.
			if guac.IsInternalMessage(message) {
				var reply []byte
				if guac.IsInternalPing(message) {
					reply = message
				} else {
					reply = clipboard.HandleControl(message)
				}
				if reply != nil {
					if err := writeWS(reply); err != nil {
						return
					}
				}
				continue
			}
			forward, reply, ferr := clipboard.FilterToGuacd(message)
			if ferr != nil {
				slog.Error("filtering browser stream", "error", ferr)
				return
			}
			if reply != nil {
				if err := writeWS(reply); err != nil {
					return
				}
			}
			if len(forward) > 0 {
				if _, err := guacd.Write(forward); err != nil {
					return
				}
			}
		}
	}()

	<-done
	ws.Close()
	guacd.Close()
	<-done
}

func intQuery(r *http.Request, name string) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(name))
	return v
}

// HTTPAPIClient talks to the API server's internal endpoints with the shared
// internal token.
type HTTPAPIClient struct {
	BaseURL       string
	InternalToken string
	Client        *http.Client
}

func NewHTTPAPIClient(baseURL, internalToken string) *HTTPAPIClient {
	return &HTTPAPIClient{
		BaseURL:       baseURL,
		InternalToken: internalToken,
		Client:        &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPAPIClient) ConnectionInfo(ctx context.Context, sessionID string) (*ConnectionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/internal/v1/sessions/%s/connection", c.BaseURL, sessionID), nil)
	if err != nil {
		return nil, fmt.Errorf("building connection-info request: %w", err)
	}
	req.Header.Set("X-Internal-Token", c.InternalToken)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching connection info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching connection info: status %d", resp.StatusCode)
	}
	var out struct {
		Data ConnectionInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding connection info: %w", err)
	}
	return &out.Data, nil
}

func (c *HTTPAPIClient) EndSession(ctx context.Context, sessionID string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/internal/v1/sessions/%s/end", c.BaseURL, sessionID), nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Internal-Token", c.InternalToken)
	resp, err := c.Client.Do(req)
	if err != nil {
		slog.Warn("ending session", "session", sessionID, "error", err)
		return
	}
	// Drain so the connection can be reused; nothing to do on error.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
