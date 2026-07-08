// Package smoke is the per-protocol connection gate: for every desktop
// protocol the platform serves (vnc, rdp, ssh) it creates a real
// workspace through the public API, waits for readiness and establishes a
// REAL session through wwt/guacd — the test is green only when guacd's
// protocol client actually reached the desktop (first "sync" instruction
// received). It exists because "the workspace is Ready" proves nothing
// about the session path: a NetworkPolicy locking guacd out, a wrong
// Status.Address or broken credentials all pass readiness and only die at
// connection time.
//
// It drives the SAME ingress the browser uses, so it needs a running
// deployment (make dev-up dev-build dev-load dev-deploy dev-load-images,
// or any validation environment):
//
//	WAAS_SMOKE_URL=http://waas.127.0.0.1.nip.io:8080 go test ./test/smoke
//
// or simply `make smoke`. Without WAAS_SMOKE_URL the test skips, so plain
// `go test ./...` stays usable offline.
package smoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type client struct {
	base  string
	token string
	http  *http.Client
	t     *testing.T
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func TestProtocolConnections(t *testing.T) {
	base := os.Getenv("WAAS_SMOKE_URL")
	if base == "" {
		t.Skip("WAAS_SMOKE_URL not set — smoke test needs a running deployment (make smoke)")
	}
	c := &client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second}, t: t}
	c.login(env("WAAS_SMOKE_USER", "admin"), env("WAAS_SMOKE_PASSWORD", "admin123"))

	byProtocol := c.templatesByProtocol()
	protocols := strings.Split(env("WAAS_SMOKE_PROTOCOLS", "vnc,rdp,ssh,kasmvnc"), ",")
	for _, protocol := range protocols {
		protocol = strings.TrimSpace(protocol)
		// Sequential on purpose: parallel workspaces would race the
		// caller's aggregate quota and blur failure attribution.
		t.Run(protocol, func(t *testing.T) {
			tpl, ok := byProtocol[protocol]
			if !ok {
				t.Fatalf("no template serves protocol %q — the validation catalog must cover every protocol", protocol)
			}
			// Each subtest gets a client bound to ITS t: a failure must
			// abort the protocol under test, not the parent.
			sub := *c
			sub.t = t
			sub.connectOnce(t, tpl, protocol)
		})
	}
}

// connectOnce runs the full lifecycle for one protocol: create → ready →
// connect → real guacd session → delete.
func (c *client) connectOnce(t *testing.T, template, protocol string) {
	name := fmt.Sprintf("smoke %s %d", protocol, time.Now().UnixNano()%100000)
	var ws struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
	}
	c.do("POST", "/api/v1/workspaces", map[string]any{
		"templateRef": template,
		"displayName": name,
	}, &ws)
	// keepVolume=false: retained homes count against the caller's storage
	// quota, so a keep-by-default here makes runs poison each other (and
	// the next protocol in THIS run). Retention has its own coverage.
	defer c.do("DELETE", "/api/v1/workspaces/"+ws.ID+"?keepVolume=false", nil, nil)

	deadline := time.Now().Add(readinessTimeout())
	resumed := false
	for ws.Phase != "Running" {
		if time.Now().After(deadline) {
			t.Fatalf("workspace %s (%s) not Running after %s, last phase %q", ws.ID, template, readinessTimeout(), ws.Phase)
		}
		// A template with an uptime/downtime schedule may stamp the fresh
		// workspace straight into Stopped (created outside office hours).
		// Do what the portal does on open-desktop: resume once and wait.
		if !resumed && (ws.Phase == "Stopped" || ws.Phase == "Paused") {
			c.do("POST", "/api/v1/workspaces/"+ws.ID+"/resume", nil, nil)
			resumed = true
		}
		time.Sleep(3 * time.Second)
		c.do("GET", "/api/v1/workspaces/"+ws.ID, nil, &ws)
	}

	var conn struct {
		SessionID       string `json:"sessionId"`
		ConnectionToken string `json:"connectionToken"`
		Protocol        string `json:"protocol"`
	}
	c.do("POST", "/api/v1/workspaces/"+ws.ID+"/connect", map[string]any{"protocol": protocol}, &conn)
	if conn.Protocol != protocol {
		t.Fatalf("connect negotiated %q, wanted %q", conn.Protocol, protocol)
	}

	if protocol == "kasmvnc" {
		// kasmvnc bypasses guacd: the proof is the KasmVNC RFB banner
		// through wwt's reverse proxy.
		if err := establishKasmSession(c.base, conn.SessionID, conn.ConnectionToken); err != nil {
			t.Fatalf("kasmvnc session via wwt failed: %v", err)
		}
		t.Logf("kasmvnc session established through wwt (template %s)", template)
		return
	}
	if err := establishSession(c.base, conn.ConnectionToken); err != nil {
		t.Fatalf("%s session via guacd failed: %v", protocol, err)
	}
	t.Logf("%s session established through guacd (template %s)", protocol, template)
}

// establishSession opens the wwt WebSocket and reads the guacd stream
// until the desktop is proven up. wwt only upgrades after ITS handshake
// with guacd succeeded, but guacd dials the desktop asynchronously after
// "ready" — so an open socket is not success. Success is the first "sync"
// (the protocol client completed its own connection and flushed a frame);
// failure is an "error"/"disconnect" instruction or the stream closing.
func establishSession(base, token string) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws?token=%s&width=1280&height=800&dpi=96", scheme, u.Host, url.QueryEscape(token))
	dialer := websocket.Dialer{Subprotocols: []string{"guacamole"}, HandshakeTimeout: 15 * time.Second}
	sock, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("websocket dial: %w (status %d: %s)", err, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer sock.Close()

	deadline := time.Now().Add(20 * time.Second)
	for {
		sock.SetReadDeadline(deadline)
		_, message, err := sock.ReadMessage()
		if err != nil {
			return fmt.Errorf("stream closed before the desktop came up: %w", err)
		}
		instructions, err := parseInstructions(string(message))
		if err != nil {
			return err
		}
		for _, ins := range instructions {
			switch ins.opcode {
			case "sync":
				return nil // desktop reached: protocol client is streaming
			case "error", "disconnect":
				return fmt.Errorf("guacd refused the session: %s %v", ins.opcode, ins.args)
			}
		}
	}
}

type instruction struct {
	opcode string
	args   []string
}

// parseInstructions decodes Guacamole wire format: comma-separated
// length-prefixed elements, semicolon-terminated instructions
// ("4.sync,8.12345678;"). Lengths count Unicode code points, and element
// VALUES may contain commas or semicolons — splitting on separators would
// corrupt, so this walks the length prefixes.
func parseInstructions(s string) ([]instruction, error) {
	runes := []rune(s)
	var out []instruction
	var current instruction
	i := 0
	for i < len(runes) {
		dot := -1
		for j := i; j < len(runes); j++ {
			if runes[j] == '.' {
				dot = j
				break
			}
		}
		if dot == -1 {
			return nil, fmt.Errorf("malformed guacamole element at %d in %.80q", i, s)
		}
		var length int
		if _, err := fmt.Sscanf(string(runes[i:dot]), "%d", &length); err != nil {
			return nil, fmt.Errorf("bad element length %q: %w", string(runes[i:dot]), err)
		}
		end := dot + 1 + length
		if end >= len(runes) {
			return nil, fmt.Errorf("truncated guacamole element at %d in %.80q", i, s)
		}
		value := string(runes[dot+1 : end])
		if current.opcode == "" && len(current.args) == 0 {
			current.opcode = value
		} else {
			current.args = append(current.args, value)
		}
		switch runes[end] {
		case ',':
		case ';':
			out = append(out, current)
			current = instruction{}
		default:
			return nil, fmt.Errorf("bad element terminator %q at %d", runes[end], end)
		}
		i = end + 1
	}
	return out, nil
}

func readinessTimeout() time.Duration {
	if v := os.Getenv("WAAS_SMOKE_READY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 5 * time.Minute
}

func (c *client) login(user, password string) {
	var res struct {
		AccessToken string `json:"accessToken"`
	}
	c.do("POST", "/api/v1/auth/login", map[string]any{"username": user, "password": password}, &res)
	c.token = res.AccessToken
}

// templatesByProtocol maps each served protocol onto the first template
// declaring it, preferring templates whose DEFAULT protocol matches (a
// template listing vnc as a fallback should not shadow the vnc-first one).
func (c *client) templatesByProtocol() map[string]string {
	var templates []struct {
		Name      string `json:"name"`
		Protocols []struct {
			Name    string `json:"name"`
			Default bool   `json:"default"`
		} `json:"protocols"`
	}
	c.do("GET", "/api/v1/workspace-templates", nil, &templates)
	out := map[string]string{}
	preferred := map[string]bool{}
	for _, tpl := range templates {
		for _, p := range tpl.Protocols {
			if _, taken := out[p.Name]; !taken || (p.Default && !preferred[p.Name]) {
				out[p.Name] = tpl.Name
				preferred[p.Name] = p.Default
			}
		}
	}
	return out
}

// do performs an authenticated API call, failing the test on any error;
// the response's "data" envelope is decoded into out when non-nil.
func (c *client) do(method, path string, body any, out any) {
	c.t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, payload)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		c.t.Fatalf("%s %s: unexpected response shape: %.200s", method, path, raw)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		c.t.Fatalf("%s %s: decoding data: %v", method, path, err)
	}
}

// establishKasmSession opens wwt's kasm reverse proxy and reads the
// KasmVNC WebSocket until the RFB banner proves the desktop is up.
// Browsers always send an Origin header on WebSocket requests and
// KasmVNC's handshake parser requires one — the dialer must too.
func establishKasmSession(base, sessionID, token string) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/kasm/%s/websockify?token=%s", scheme, u.Host, sessionID, url.QueryEscape(token))
	dialer := websocket.Dialer{Subprotocols: []string{"binary"}, HandshakeTimeout: 15 * time.Second}
	sock, resp, err := dialer.Dial(wsURL, http.Header{"Origin": {base}})
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("kasm websocket dial: %w (status %d: %s)", err, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("kasm websocket dial: %w", err)
	}
	defer sock.Close()

	sock.SetReadDeadline(time.Now().Add(20 * time.Second))
	_, message, err := sock.ReadMessage()
	if err != nil {
		return fmt.Errorf("stream closed before the desktop came up: %w", err)
	}
	if !bytes.HasPrefix(message, []byte("RFB ")) {
		return fmt.Errorf("expected the KasmVNC RFB banner, got %q", message)
	}
	return nil
}
