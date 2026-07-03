package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	expired, err := signer.Sign(auth.NewConnectionClaims("waas-test", "u", "s", "w", -time.Minute))
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

	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "user-1", "sess-1", "ws-1", time.Minute))
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
