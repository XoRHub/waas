package kasm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/xorhub/waas/shared/auth"
	"github.com/xorhub/waas/wwt/internal/jwks"
	"github.com/xorhub/waas/wwt/internal/proxy"
)

type fakeAPI struct {
	info  *proxy.ConnectionInfo
	ended atomic.Int32
}

func (f *fakeAPI) ConnectionInfo(context.Context, string) (*proxy.ConnectionInfo, error) {
	if f.info == nil {
		return nil, fmt.Errorf("no such session")
	}
	return f.info, nil
}

func (f *fakeAPI) EndSession(context.Context, string) { f.ended.Add(1) }

// newFakeKasmVNC mimics the kasmweb images' endpoint: HTTPS with a
// self-signed cert, HTTP Basic before anything else, an Origin-requiring
// /websockify that speaks one binary frame, static files elsewhere. It
// also asserts what must NEVER reach the workspace container: the
// platform cookie and the token query parameter.
func newFakeKasmVNC(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"binary"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if u, p, ok := r.BasicAuth(); !ok || u != "kasm_user" || p != "vnc-pw-1" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Websockify"`)
			http.Error(w, "401", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Cookie") != "" {
			t.Errorf("platform cookie leaked upstream: %q", r.Header.Get("Cookie"))
		}
		if r.URL.Query().Get("token") != "" {
			t.Error("token query parameter leaked upstream")
		}
		if r.URL.Path == "/websockify" {
			// KasmVNC's parse_handshake rejects Origin-less upgrades.
			if r.Header.Get("Origin") == "" {
				http.Error(w, "404", http.StatusNotFound)
				return
			}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n"))
			for { // echo until the client goes away
				mt, msg, err := ws.ReadMessage()
				if err != nil {
					return
				}
				if err := ws.WriteMessage(mt, msg); err != nil {
					return
				}
			}
		}
		fmt.Fprintf(w, "KASM_PAGE %s", r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestHandler(t *testing.T, api proxy.APIClient) (*Handler, *auth.Signer) {
	t.Helper()
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(signer.JWKS())
	}))
	t.Cleanup(jwksServer.Close)
	return NewHandler("waas-test", jwks.NewClient(jwksServer.URL), api), signer
}

func kasmInfo(t *testing.T, upstream *httptest.Server) *proxy.ConnectionInfo {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	return &proxy.ConnectionInfo{
		Protocol: "kasmvnc",
		Hostname: u.Hostname(),
		Port:     int32(port),
		Password: "vnc-pw-1",
	}
}

func connectionToken(t *testing.T, signer *auth.Signer, sid string) string {
	t.Helper()
	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", sid, "ws1", auth.ClipboardGrant{}, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return token
}

func TestRejectsBadTokens(t *testing.T) {
	upstream, hits := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	// No token at all.
	resp, err := http.Get(server.URL + "/kasm/sess-1/vnc.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Valid token, WRONG session: must not cross over.
	token := connectionToken(t, signer, "sess-1")
	resp, err = http.Get(server.URL + "/kasm/sess-2/vnc.html?token=" + token)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for session mismatch, got %d", resp.StatusCode)
	}

	if hits.Load() != 0 {
		t.Fatalf("upstream must never be touched on auth failure; got %d hits", hits.Load())
	}
}

func TestServesPageWithScopedCookieAndInjectedBasic(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	token := connectionToken(t, signer, "sess-1")
	resp, err := http.Get(server.URL + "/kasm/sess-1/vnc.html?token=" + token)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || body != "KASM_PAGE /vnc.html" {
		t.Fatalf("expected proxied page, got %d %q", resp.StatusCode, body)
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("expected the session cookie to be set on the token request")
	}
	if cookie.Path != "/kasm/sess-1" {
		t.Fatalf("cookie must be scoped to the session subtree, got path %q", cookie.Path)
	}

	// Subresource request: cookie only, no token — must still be proxied.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/assets/app.js", nil)
	req.AddCookie(cookie)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with cookie: %v", err)
	}
	body2 := readAll(t, resp2)
	if resp2.StatusCode != http.StatusOK || body2 != "KASM_PAGE /assets/app.js" {
		t.Fatalf("expected proxied asset via cookie auth, got %d %q", resp2.StatusCode, body2)
	}
}

func TestRejectsNonKasmSessions(t *testing.T) {
	upstream, hits := newFakeKasmVNC(t)
	info := kasmInfo(t, upstream)
	info.Protocol = "vnc" // a guacd session must stay on the guacd path
	handler, signer := newTestHandler(t, &fakeAPI{info: info})
	server := httptest.NewServer(handler)
	defer server.Close()

	token := connectionToken(t, signer, "sess-1")
	resp, err := http.Get(server.URL + "/kasm/sess-1/vnc.html?token=" + token)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for a non-kasmvnc session, got %d", resp.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream must not be touched for wrong-protocol sessions; got %d hits", hits.Load())
	}
}

func TestWebsocketPassthroughEndsSessionOnClose(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	api := &fakeAPI{info: kasmInfo(t, upstream)}
	handler, signer := newTestHandler(t, api)
	server := httptest.NewServer(handler)
	defer server.Close()

	token := connectionToken(t, signer, "sess-1")
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/kasm/sess-1/websockify?token=" + token
	dialer := websocket.Dialer{
		Subprotocols:    []string{"binary"},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	// Browsers always send an Origin on WebSocket requests.
	ws, _, err := dialer.Dial(wsURL, http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatalf("dialing through the proxy: %v", err)
	}

	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("reading banner: %v", err)
	}
	if string(msg) != "RFB 003.008\n" {
		t.Fatalf("expected the KasmVNC banner through the proxy, got %q", msg)
	}
	// Round-trip one frame to prove the browser→desktop direction too.
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("ping-1")); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, msg, err = ws.ReadMessage(); err != nil || string(msg) != "ping-1" {
		t.Fatalf("expected echo, got %q err=%v", msg, err)
	}

	ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for api.ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if api.ended.Load() != 1 {
		t.Fatalf("expected exactly one EndSession after the stream closed, got %d", api.ended.Load())
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String()
		}
	}
}
