package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/xorhub/waas/shared/auth"
	"github.com/xorhub/waas/wwt/internal/guac"
	"github.com/xorhub/waas/wwt/internal/jwks"
)

type fakeAPI struct {
	info  *ConnectionInfo
	ended atomic.Int32
}

func (f *fakeAPI) ConnectionInfo(context.Context, string) (*ConnectionInfo, error) {
	if f.info == nil {
		return nil, fmt.Errorf("no such session")
	}
	return f.info, nil
}

func (f *fakeAPI) EndSession(context.Context, string) { f.ended.Add(1) }

// newJWKSServer serves the signer's public keys like the API server does.
func newJWKSServer(t *testing.T, signer *auth.Signer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(signer.JWKS())
	}))
}

// newFakeGuacd listens on a real TCP port, counts connections, and speaks
// the server side of the handshake, then echoes one instruction.
func newFakeGuacd(t *testing.T) (addr string, dials *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				if _, err := guac.ReadInstruction(r); err != nil { // select
					return
				}
				args := guac.Instruction{Opcode: "args", Args: []string{"VERSION_1_5_0", "hostname", "port", "password"}}
				conn.Write([]byte(args.Encode()))
				for {
					inst, err := guac.ReadInstruction(r)
					if err != nil {
						return
					}
					if inst.Opcode == "connect" {
						break
					}
				}
				ready := guac.Instruction{Opcode: "ready", Args: []string{"$c1"}}
				conn.Write([]byte(ready.Encode()))
				// Stream one frame so the test can observe the pipe.
				sync := guac.Instruction{Opcode: "sync", Args: []string{"12345"}}
				conn.Write([]byte(sync.Encode()))
				// Then keep reading until the client closes.
				buf := make([]byte, 1024)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), &count
}

func newTestHandler(t *testing.T, api APIClient, guacdAddr string) (*Handler, *auth.Signer) {
	t.Helper()
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("generating signer: %v", err)
	}
	jwksServer := newJWKSServer(t, signer)
	t.Cleanup(jwksServer.Close)
	return NewHandler("waas-test", jwks.NewClient(jwksServer.URL), api, guacdAddr), signer
}

func TestRejectsWithoutValidTokenBeforeDialingGuacd(t *testing.T) {
	guacdAddr, dials := newFakeGuacd(t)
	handler, signer := newTestHandler(t, &fakeAPI{}, guacdAddr)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Missing token.
	resp, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong audience: a valid *access* token must not open desktops.
	accessToken, err := signer.Sign(auth.NewAccessClaims("waas-test", "user-1", auth.RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	resp, err = http.Get(server.URL + "/ws?token=" + accessToken)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for access token, got %d", resp.StatusCode)
	}

	// Expired connection token.
	expired, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u", "s", "w", auth.ClipboardGrant{}, -time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	resp, err = http.Get(server.URL + "/ws?token=" + expired)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", resp.StatusCode)
	}

	if got := dials.Load(); got != 0 {
		t.Fatalf("guacd must never be dialed for invalid tokens; got %d dials", got)
	}
}

func TestProxiesValidConnection(t *testing.T) {
	guacdAddr, dials := newFakeGuacd(t)
	api := &fakeAPI{info: &ConnectionInfo{Protocol: "vnc", Hostname: "ws-marc", Port: 5901, Password: "pw"}}
	handler, signer := newTestHandler(t, api, guacdAddr)
	server := httptest.NewServer(handler)
	defer server.Close()

	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "user-1", "sess-1", "ws-1", auth.ClipboardGrant{Copy: true, Paste: true}, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token + "&width=1280&height=720"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dialing ws: %v (resp=%v)", err, resp)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("reading from ws: %v", err)
	}
	if !strings.Contains(string(message), "sync") {
		t.Fatalf("expected guacd sync frame, got %q", message)
	}
	if dials.Load() != 1 {
		t.Fatalf("expected exactly one guacd connection, got %d", dials.Load())
	}

	ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for api.ended.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if api.ended.Load() == 0 {
		t.Fatal("expected the session to be ended after disconnect")
	}
}

// newDribblingGuacd handshakes, then streams a large instruction in tiny TCP
// writes followed by a sync — reproducing the arbitrary segmentation that
// corrupted VNC sessions. It also records everything the proxy sends it.
func newDribblingGuacd(t *testing.T, payload guac.Instruction) (addr string, received *syncBuffer) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	received = &syncBuffer{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		if _, err := guac.ReadInstruction(r); err != nil { // select
			return
		}
		args := guac.Instruction{Opcode: "args", Args: []string{"VERSION_1_5_0", "hostname", "port", "password"}}
		conn.Write([]byte(args.Encode()))
		for {
			inst, err := guac.ReadInstruction(r)
			if err != nil {
				return
			}
			if inst.Opcode == "connect" {
				break
			}
		}
		conn.Write([]byte(guac.Instruction{Opcode: "ready", Args: []string{"$c1"}}.Encode()))

		// Dribble a big instruction 100 bytes at a time.
		wire := []byte(payload.Encode() + guac.Instruction{Opcode: "sync", Args: []string{"1"}}.Encode())
		for i := 0; i < len(wire); i += 100 {
			end := min(i+100, len(wire))
			if _, err := conn.Write(wire[i:end]); err != nil {
				return
			}
		}
		// Record whatever the proxy forwards from the browser.
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				received.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return ln.Addr().String(), received
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestFramesEndOnInstructionBoundaries is the regression test for the broken
// VNC sessions: every WebSocket message must parse as complete instructions,
// however guacd segments its TCP writes.
func TestFramesEndOnInstructionBoundaries(t *testing.T) {
	big := guac.Instruction{Opcode: "png", Args: []string{"0", "3", "0", "0", strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 3000)}}
	guacdAddr, received := newDribblingGuacd(t, big)
	api := &fakeAPI{info: &ConnectionInfo{Protocol: "vnc", Hostname: "ws-x", Port: 5901, Password: "pw"}}
	handler, signer := newTestHandler(t, api, guacdAddr)
	server := httptest.NewServer(handler)
	defer server.Close()

	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "user-1", "sess-1", "ws-1", auth.ClipboardGrant{Copy: true, Paste: true}, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatalf("dialing ws: %v", err)
	}
	defer ws.Close()

	var stream bytes.Buffer
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !strings.Contains(stream.String(), "4.sync") {
		_, message, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("reading from ws: %v (got %d bytes so far)", err, stream.Len())
		}
		// The invariant guacamole-common-js relies on: each message is a
		// standalone sequence of complete instructions.
		r := bufio.NewReader(bytes.NewReader(message))
		for r.Buffered() > 0 || !atEOF(r) {
			if _, err := guac.ReadInstruction(r); err != nil {
				t.Fatalf("WebSocket message does not end on an instruction boundary: %v", err)
			}
			if atEOF(r) {
				break
			}
		}
		stream.Write(message)
	}
	want := big.Encode() + guac.Instruction{Opcode: "sync", Args: []string{"1"}}.Encode()
	if stream.String() != want {
		t.Fatalf("reassembled stream corrupted: got %d bytes, want %d", stream.Len(), len(want))
	}

	// Tunnel-internal ping: echoed verbatim to the browser, never to guacd.
	ping := "0.,4.ping,13.1751791234567;"
	if err := ws.WriteMessage(websocket.TextMessage, []byte(ping)); err != nil {
		t.Fatalf("sending ping: %v", err)
	}
	_, echo, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("reading ping echo: %v", err)
	}
	if string(echo) != ping {
		t.Fatalf("expected identical ping echo, got %q", echo)
	}

	// A real instruction still reaches guacd; the ping never does.
	key := "3.key,5.65307,1.1;"
	if err := ws.WriteMessage(websocket.TextMessage, []byte(key)); err != nil {
		t.Fatalf("sending key: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(received.String(), key) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := received.String(); !strings.Contains(got, key) {
		t.Fatalf("key instruction never reached guacd (got %q)", got)
	}
	if strings.Contains(received.String(), "ping") {
		t.Fatal("tunnel-internal ping must never reach guacd")
	}
}

func atEOF(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	return err != nil
}
