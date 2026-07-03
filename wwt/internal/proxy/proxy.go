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

	// 3. Only now dial guacd and run the handshake.
	conn, err := net.DialTimeout("tcp", h.GuacdAddr, h.DialTimeout)
	if err != nil {
		slog.ErrorContext(ctx, "dialing guacd", "error", err)
		http.Error(w, "desktop gateway unavailable", http.StatusBadGateway)
		return
	}

	params := guac.ConnectionParams{
		Protocol: info.Protocol,
		Hostname: info.Hostname,
		Port:     info.Port,
		Username: info.Username,
		Password: info.Password,
		Width:    intQuery(r, "width"),
		Height:   intQuery(r, "height"),
		DPI:      intQuery(r, "dpi"),
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
		"session", claims.SessionID, "workspace", claims.WorkspaceID, "guacdConnection", connID)
	h.pipe(ws, conn, guacdReader)

	// Best-effort bookkeeping once the stream closes.
	endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.API.EndSession(endCtx, claims.SessionID)
	slog.InfoContext(ctx, "session ended", "session", claims.SessionID)
}

func (h *Handler) validateToken(ctx context.Context, token string) (*auth.ConnectionClaims, error) {
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
	key, err := h.key(ctx, kid)
	if err != nil {
		return nil, err
	}
	return auth.VerifyConnectionToken(token, h.Issuer, key)
}

func (h *Handler) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	return h.Keys.Key(ctx, kid)
}

// pipe relays bytes in both directions until either side closes.
func (h *Handler) pipe(ws *websocket.Conn, guacd net.Conn, guacdReader *bufio.Reader) {
	done := make(chan struct{}, 2)

	// guacd → browser
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, err := guacdReader.Read(buf)
			if n > 0 {
				if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					return
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
			if _, err := guacd.Write(message); err != nil {
				return
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
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
