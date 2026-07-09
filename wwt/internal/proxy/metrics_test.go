package proxy

// Tunnel metrics ride the real data path: an accepted connection moves
// the gauge up then back down, relayed frames feed the byte counters, a
// rejected token feeds the failure counter. Deltas only — the metrics
// are process-global.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/xorhub/waas/shared/auth"
	"github.com/xorhub/waas/wwt/internal/metrics"

	"net/http/httptest"
)

func TestValidateConnectionTokenCountsFailures(t *testing.T) {
	before := testutil.ToFloat64(metrics.TokenValidationFailures)
	if _, err := ValidateConnectionToken(context.Background(), nil, "waas-test", ""); err == nil {
		t.Fatal("expected the empty token to be rejected")
	}
	if _, err := ValidateConnectionToken(context.Background(), nil, "waas-test", "not-a-jwt"); err == nil {
		t.Fatal("expected the malformed token to be rejected")
	}
	if got := testutil.ToFloat64(metrics.TokenValidationFailures); got != before+2 {
		t.Fatalf("TokenValidationFailures = %v, want %v", got, before+2)
	}
}

func TestTunnelGaugeAndByteCounters(t *testing.T) {
	guacdAddr, _ := newFakeGuacd(t)
	api := &fakeAPI{info: &ConnectionInfo{Protocol: "vnc", Hostname: "ws-marc", Port: 5901, Password: "pw"}}
	handler, signer := newTestHandler(t, api, guacdAddr)
	server := httptest.NewServer(handler)
	defer server.Close()

	gauge := metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolGuacd)
	toBrowser := metrics.ProxiedBytes.WithLabelValues(metrics.DirectionToBrowser)
	baseGauge := testutil.ToFloat64(gauge)
	baseBytes := testutil.ToFloat64(toBrowser)

	token, err := signer.Sign(auth.NewConnectionClaims("waas-test", "user-1", "sess-1", "ws-1",
		auth.ClipboardGrant{Copy: true, Paste: true}, time.Minute))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	ws, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatalf("dialing ws: %v", err)
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatalf("reading the sync frame: %v", err)
	}
	if got := testutil.ToFloat64(gauge); got != baseGauge+1 {
		t.Fatalf("ActiveTunnels{guacd} while connected = %v, want %v", got, baseGauge+1)
	}
	// The byte counter increments right after the frame is written; the
	// client read can slightly precede it.
	waitFor(t, "proxied bytes to be counted", func() bool {
		return testutil.ToFloat64(toBrowser) > baseBytes
	})

	ws.Close()
	waitFor(t, "the tunnel gauge to fall back", func() bool {
		return testutil.ToFloat64(gauge) == baseGauge
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
