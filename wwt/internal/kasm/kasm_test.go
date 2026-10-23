package kasm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
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
	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", sid, "ws1", auth.ClipboardGrant{}, auth.FileTransferGrant{}, auth.AudioGrant{}, time.Minute))
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

func TestBlocksDownloadsPaths(t *testing.T) {
	upstream, hits := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	token := connectionToken(t, signer, "sess-1")
	// Prime the session cookie the way the iframe's first hit does.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(server.URL + "/kasm/sess-1/vnc.html?token=" + token)
	if err != nil {
		t.Fatalf("priming GET: %v", err)
	}
	resp.Body.Close()
	priming := hits.Load()

	// Every shape of the downloads path — listing, subdirectory, and the
	// normalization tricks (case, trailing slash, dot, double slash,
	// embedded NUL) — must be refused, and the workspace container must
	// never be reached.
	cases := []struct {
		path string
		want int
	}{
		{"/api/downloads", http.StatusForbidden},
		{"/api/downloads/", http.StatusForbidden},
		{"/api/downloads?path=sub", http.StatusForbidden},
		{"/api/downloads/secret.txt", http.StatusForbidden},
		{"/API/Downloads", http.StatusForbidden},
		{"/api/./downloads", http.StatusForbidden},
		{"/api//downloads", http.StatusForbidden},
		// The kasmweb images' www/Downloads/Downloads → ~/Downloads symlink.
		{"/Downloads", http.StatusForbidden},
		{"/Downloads/", http.StatusForbidden},
		{"/Downloads/Downloads/secret.txt", http.StatusForbidden},
		{"/downloads/downloads/", http.StatusForbidden},
		{"/./Downloads//Downloads", http.StatusForbidden},
		// An embedded NUL truncates the upstream's own percent-decoder
		// past isDownloadsPath's exact/prefix match ("/Downloads\x00x" is
		// neither "/downloads" nor "/downloads/..."); rejected outright.
		{"/Downloads%00x", http.StatusBadRequest},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1"+tc.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("expected %d for %q, got %d", tc.want, tc.path, resp.StatusCode)
		}
	}
	if hits.Load() != priming {
		t.Fatalf("downloads API must never reach upstream; %d extra hits", hits.Load()-priming)
	}

	// A sibling /api endpoint must still proxy: the block is scoped to
	// downloads, not the whole API surface.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/api/get_screenshot", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET get_screenshot: %v", err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || body != "KASM_PAGE /api/get_screenshot" {
		t.Fatalf("expected sibling /api endpoint to proxy, got %d %q", resp.StatusCode, body)
	}
}

func TestWebsocketPassthroughEndsSessionOnClose(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	api := &fakeAPI{info: kasmInfo(t, upstream)}
	handler, signer := newTestHandler(t, api)
	handler.endGrace = 10 * time.Millisecond
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

// The session only ends once its LAST stream has been gone for the
// whole grace: a reconnect within it (KasmVNC's auto-reconnect) keeps
// it alive. A long endGrace makes this a pure negative assertion on
// in-memory state (no timer can fire during the test), instead of a
// sleep racing the real timer — flaky under -race/load.
func TestSessionEndCancelledByReconnect(t *testing.T) {
	api := &fakeAPI{}
	handler, _ := newTestHandler(t, api)
	handler.endGrace = time.Hour

	handler.streamOpened("sess-1")
	handler.streamClosed("sess-1", "ws1")
	handler.streamOpened("sess-1")

	handler.mu.Lock()
	s := handler.streams["sess-1"]
	live, pending := s.live, s.pending
	handler.mu.Unlock()
	if live != 1 {
		t.Fatalf("expected live == 1 after the reconnect, got %d", live)
	}
	if pending != nil {
		t.Fatal("a reconnect within the grace must cancel the pending end")
	}
	if n := api.ended.Load(); n != 0 {
		t.Fatalf("a reconnect within the grace must not end the session, got %d EndSession", n)
	}
}

// A still-open second stream (multi-monitor screen.html) keeps the
// session alive when one of the two closes — same determinism rationale
// as above: assert the state, don't sleep past a timer that must not
// have been armed at all.
func TestSessionEndWaitsForLastStream(t *testing.T) {
	api := &fakeAPI{}
	handler, _ := newTestHandler(t, api)
	handler.endGrace = time.Hour

	handler.streamOpened("sess-1")
	handler.streamOpened("sess-1")
	handler.streamClosed("sess-1", "ws1")

	handler.mu.Lock()
	s := handler.streams["sess-1"]
	live, pending := s.live, s.pending
	handler.mu.Unlock()
	if live != 1 {
		t.Fatalf("expected live == 1 with one stream still open, got %d", live)
	}
	if pending != nil {
		t.Fatal("a still-open stream must not arm a pending end")
	}
}

// endSession's gen fence is what actually matters: Stop() can lose the
// race against an already-firing timer, so a stale generation must
// still be rejected even when called directly, bypassing timing
// altogether.
func TestEndSessionIgnoresStaleGeneration(t *testing.T) {
	api := &fakeAPI{}
	handler, _ := newTestHandler(t, api)

	handler.streamOpened("sess-1")
	handler.streamClosed("sess-1", "ws1") // live 0, arms a pending end
	handler.streamOpened("sess-1")        // reconnect: cancels it, bumps gen again

	handler.mu.Lock()
	s := handler.streams["sess-1"]
	staleGen := s.gen - 1 // the generation the (stopped) timer was armed for
	handler.mu.Unlock()

	handler.endSession("sess-1", "ws1", s, staleGen)

	if n := api.ended.Load(); n != 0 {
		t.Fatalf("a stale generation must not end the session, got %d EndSession", n)
	}
	handler.mu.Lock()
	_, tracked := handler.streams["sess-1"]
	handler.mu.Unlock()
	if !tracked {
		t.Fatal("the session must still be tracked after a stale-generation call")
	}
}

// The one case that genuinely needs the timer to fire: a short grace,
// polled rather than slept on.
func TestSessionEndFiresOnceGraceElapses(t *testing.T) {
	api := &fakeAPI{}
	handler, _ := newTestHandler(t, api)
	handler.endGrace = time.Millisecond

	handler.streamOpened("sess-1")
	handler.streamClosed("sess-1", "ws1")

	deadline := time.Now().Add(3 * time.Second)
	for api.ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := api.ended.Load(); n != 1 {
		t.Fatalf("expected exactly one EndSession once the grace elapsed, got %d", n)
	}
	handler.mu.Lock()
	_, tracked := handler.streams["sess-1"]
	handler.mu.Unlock()
	if tracked {
		t.Fatal("an ended session must not stay tracked")
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

// The cookie IS the connection JWT: once it expires, every page/asset/WS
// request must be rejected without touching the upstream.
func TestExpiredCookieIsRejected(t *testing.T) {
	upstream, hits := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	expired, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", "sess-1", "ws1", auth.ClipboardGrant{}, auth.FileTransferGrant{}, auth.AudioGrant{}, -time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/assets/app.js", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: expired})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an expired cookie, got %d", resp.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream must not be touched with an expired cookie; got %d hits", hits.Load())
	}
}

// Reconnection path: the frontend opens a new iframe with a FRESH query
// token while the browser still holds the stale cookie — the query must
// win and re-set the cookie.
func TestFreshQueryTokenBeatsExpiredCookie(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	expired, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", "sess-1", "ws1", auth.ClipboardGrant{}, auth.FileTransferGrant{}, auth.AudioGrant{}, -time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fresh := connectionToken(t, signer, "sess-1")

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/vnc.html?token="+fresh, nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: expired})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || body != "KASM_PAGE /vnc.html" {
		t.Fatalf("fresh query token must win over the stale cookie, got %d %q", resp.StatusCode, body)
	}
	refreshed := ""
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			refreshed = c.Value
		}
	}
	if refreshed != fresh {
		t.Fatal("the session cookie must be re-set from the fresh token")
	}
}

// newFakeAgent mimics the kasm-agent sidecar: plain HTTP, the same Basic
// pair as KasmVNC, echoing method and path. Like newFakeKasmVNC it
// asserts nothing of the platform's leaks upstream.
func newFakeAgent(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if u, p, ok := r.BasicAuth(); !ok || u != "kasm_user" || p != "vnc-pw-1" {
			http.Error(w, "401", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Cookie") != "" {
			t.Errorf("platform cookie leaked to the agent: %q", r.Header.Get("Cookie"))
		}
		fmt.Fprintf(w, "AGENT %s %s", r.Method, r.URL.RequestURI())
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func filesToken(t *testing.T, signer *auth.Signer, sid string, grant auth.FileTransferGrant) string {
	t.Helper()
	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", sid, "ws1", auth.ClipboardGrant{}, grant, auth.AudioGrant{}, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return token
}

// Every file route is gated on the token's grant, each with exactly one
// method, and only an allowed request reaches the sidecar — with the
// injected Basic pair and its query intact.
func TestFileRoutesGatedOnTheGrant(t *testing.T) {
	upstream, kasmHits := newFakeKasmVNC(t)
	agent, agentHits := newFakeAgent(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	agentURL, _ := url.Parse(agent.URL)
	port, _ := strconv.Atoi(agentURL.Port())
	handler.agentPort = int32(port)
	server := httptest.NewServer(handler)
	defer server.Close()

	none := auth.FileTransferGrant{}
	down := auth.FileTransferGrant{Download: true}
	up := auth.FileTransferGrant{Upload: true}
	both := auth.FileTransferGrant{Upload: true, Download: true}
	cases := []struct {
		grant  auth.FileTransferGrant
		method string
		path   string
		want   int
		body   string
	}{
		// No grant: everything 403, nothing reaches the sidecar.
		{none, http.MethodGet, "/waas/files", http.StatusForbidden, ""},
		{none, http.MethodGet, "/waas/files/download?path=a.txt", http.StatusForbidden, ""},
		{none, http.MethodPost, "/waas/files/upload", http.StatusForbidden, ""},
		// Download only: list and download, no upload.
		{down, http.MethodGet, "/waas/files?path=Documents", http.StatusOK, "AGENT GET /files?path=Documents"},
		{down, http.MethodGet, "/waas/files/download?path=a.txt", http.StatusOK, "AGENT GET /files/download?path=a.txt"},
		{down, http.MethodPost, "/waas/files/upload", http.StatusForbidden, ""},
		// Upload only: list and upload, no download.
		{up, http.MethodGet, "/waas/files", http.StatusOK, "AGENT GET /files"},
		{up, http.MethodPost, "/waas/files/upload?dir=in", http.StatusOK, "AGENT POST /files/upload?dir=in"},
		{up, http.MethodGet, "/waas/files/download?path=a.txt", http.StatusForbidden, ""},
		// One method per route, whatever the grant.
		{both, http.MethodPost, "/waas/files", http.StatusMethodNotAllowed, ""},
		{both, http.MethodPost, "/waas/files/download?path=a.txt", http.StatusMethodNotAllowed, ""},
		{both, http.MethodGet, "/waas/files/upload", http.StatusMethodNotAllowed, ""},
		{both, http.MethodDelete, "/waas/files/download?path=a.txt", http.StatusMethodNotAllowed, ""},
		// Unknown agent routes, including normalization tricks that must
		// not resolve to a gated one.
		{both, http.MethodGet, "/waas", http.StatusNotFound, ""},
		{both, http.MethodGet, "/waas/", http.StatusNotFound, ""},
		{both, http.MethodGet, "/waas/other", http.StatusNotFound, ""},
		{both, http.MethodGet, "/waas/files/../files/download/../../../api/downloads", http.StatusNotFound, ""},
		// Trailing-slash and dot variants normalize onto the gated route.
		{down, http.MethodGet, "/waas/files/", http.StatusOK, "AGENT GET /files"},
		{none, http.MethodGet, "/waas//files/./download?path=a.txt", http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		agentBefore := agentHits.Load()
		req, _ := http.NewRequest(tc.method, server.URL+"/kasm/sess-1"+tc.path, nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: filesToken(t, signer, "sess-1", tc.grant)})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		body := readAll(t, resp)
		if resp.StatusCode != tc.want {
			t.Errorf("%+v %s %s: expected %d, got %d (%s)", tc.grant, tc.method, tc.path, tc.want, resp.StatusCode, strings.TrimSpace(body))
		}
		if tc.body != "" && body != tc.body {
			t.Errorf("%s %s: expected upstream %q, got %q", tc.method, tc.path, tc.body, body)
		}
		if reached := agentHits.Load() > agentBefore; reached != (tc.want == http.StatusOK) {
			t.Errorf("%+v %s %s: agent reached=%v for status %d", tc.grant, tc.method, tc.path, reached, resp.StatusCode)
		}
	}
	if kasmHits.Load() != 0 {
		t.Fatalf("no file route may reach KasmVNC itself; got %d hits", kasmHits.Load())
	}

	// A sibling path that merely starts with the prefix letters is KasmVNC's.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/waasx.js", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: filesToken(t, signer, "sess-1", none)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK || body != "KASM_PAGE /waasx.js" {
		t.Fatalf("expected the KasmVNC page for /waasx.js, got %d %q", resp.StatusCode, body)
	}
}

// A download grant lifts nothing on KasmVNC's own egress routes: the
// governed path is the sidecar, /api/downloads and /Downloads stay 403.
func TestDownloadsPathsStayBlockedWithGrant(t *testing.T) {
	upstream, hits := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	server := httptest.NewServer(handler)
	defer server.Close()

	token := filesToken(t, signer, "sess-1", auth.FileTransferGrant{Upload: true, Download: true})
	for _, p := range []string{"/api/downloads", "/Downloads/Downloads/secret.txt"} {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1"+p, nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("expected 403 for %s even with a full grant, got %d", p, resp.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("downloads paths must never reach upstream; got %d hits", hits.Load())
	}
}

// The cookie refresh: a fresh query token re-sets the session cookie and
// answers 204 without touching KasmVNC or the sidecar; a bad or
// mismatched token is refused exactly like any kasm request.
func TestSessionRouteRefreshesTheCookie(t *testing.T) {
	upstream, kasmHits := newFakeKasmVNC(t)
	agent, agentHits := newFakeAgent(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	agentURL, _ := url.Parse(agent.URL)
	port, _ := strconv.Atoi(agentURL.Port())
	handler.agentPort = int32(port)
	server := httptest.NewServer(handler)
	defer server.Close()

	expired, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u1", "sess-1", "ws1", auth.ClipboardGrant{}, auth.FileTransferGrant{}, auth.AudioGrant{}, -time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fresh := connectionToken(t, signer, "sess-1")
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/waas/session?token="+fresh, nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: expired})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != fresh || cookie.Path != "/kasm/sess-1" || !cookie.HttpOnly {
		t.Fatalf("expected the scoped session cookie re-set from the fresh token, got %+v", cookie)
	}
	if kasmHits.Load() != 0 || agentHits.Load() != 0 {
		t.Fatalf("the refresh must touch nothing upstream; kasm=%d agent=%d", kasmHits.Load(), agentHits.Load())
	}

	for _, tc := range []struct {
		token string
		want  int
	}{
		{expired, http.StatusUnauthorized},
		{"garbage", http.StatusUnauthorized},
		{connectionToken(t, signer, "sess-2"), http.StatusForbidden},
	} {
		resp, err := http.Get(server.URL + "/kasm/sess-1/waas/session?token=" + tc.token)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("expected %d, got %d", tc.want, resp.StatusCode)
		}
		if len(resp.Cookies()) != 0 {
			t.Error("a refused token must not set a cookie")
		}
	}
}

// Sidecar absent (operator.kasmAgent off, or a workspace started before
// it was enabled): 503, not the desktop's 502.
func TestFileRoutesAnswer503WithoutTheSidecar(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	handler, signer := newTestHandler(t, &fakeAPI{info: kasmInfo(t, upstream)})
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL, _ := url.Parse(closed.URL)
	closed.Close()
	port, _ := strconv.Atoi(closedURL.Port())
	handler.agentPort = int32(port)
	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/kasm/sess-1/waas/files", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: filesToken(t, signer, "sess-1", auth.FileTransferGrant{Download: true})})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without the sidecar, got %d", resp.StatusCode)
	}
}
