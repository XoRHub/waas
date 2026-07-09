package kasm

// Only the upgraded websockify stream moves the kasm tunnel gauge —
// asset requests are not desktop streams.

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/xorhub/waas/wwt/internal/metrics"
)

func TestKasmTunnelGauge(t *testing.T) {
	upstream, _ := newFakeKasmVNC(t)
	api := &fakeAPI{info: kasmInfo(t, upstream)}
	handler, signer := newTestHandler(t, api)
	server := httptest.NewServer(handler)
	defer server.Close()

	gauge := metrics.ActiveTunnels.WithLabelValues(metrics.ProtocolKasm)
	base := testutil.ToFloat64(gauge)

	token := connectionToken(t, signer, "sess-1")
	dialer := websocket.Dialer{
		Subprotocols:    []string{"binary"},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	ws, _, err := dialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/kasm/sess-1/websockify?token="+token,
		http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatalf("dialing through the proxy: %v", err)
	}
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatalf("reading banner: %v", err)
	}
	if got := testutil.ToFloat64(gauge); got != base+1 {
		t.Fatalf("ActiveTunnels{kasmvnc} while connected = %v, want %v", got, base+1)
	}

	ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for testutil.ToFloat64(gauge) != base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := testutil.ToFloat64(gauge); got != base {
		t.Fatalf("ActiveTunnels{kasmvnc} after close = %v, want %v", got, base)
	}
}
