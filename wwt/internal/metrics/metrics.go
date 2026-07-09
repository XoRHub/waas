// Package metrics holds the WebSocket proxy's Prometheus metrics,
// registered on the default registry (which also carries the standard
// Go/process collectors). The /metrics endpoint itself is only mounted
// when WWT_METRICS_ENABLED is true — see cmd/main.go.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Tunnel protocol label values: the two data planes of the proxy.
const (
	ProtocolGuacd = "guacd"
	ProtocolKasm  = "kasmvnc"
)

// Byte direction label values, seen from the tunnel.
const (
	DirectionToBrowser = "to_browser"
	DirectionToDesktop = "to_desktop"
)

var (
	// ActiveTunnels is the number of live desktop streams per data plane
	// (guacd WebSocket tunnels, kasmvnc websockify streams).
	ActiveTunnels = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "waas_wwt_active_tunnels",
		Help: "Live desktop streams, by protocol data plane (guacd | kasmvnc).",
	}, []string{"protocol"})

	// ProxiedBytes counts payload bytes relayed through the guacd tunnel
	// (the kasmvnc plane flows through httputil.ReverseProxy and is not
	// byte-counted).
	ProxiedBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "waas_wwt_proxied_bytes_total",
		Help: "Bytes relayed through the guacd tunnel, by direction.",
	}, []string{"direction"})

	// TokenValidationFailures counts rejected connection tokens (both the
	// /ws and /kasm paths share the same validator). A spike is either an
	// expiry misconfiguration or someone probing with forged tokens.
	TokenValidationFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "waas_wwt_token_validation_failures_total",
		Help: "Connection tokens rejected by the shared validator (/ws and /kasm).",
	})

	// ClipboardBlocked counts clipboard streams dropped by policy or by
	// the user's own overlay toggle (copy = remote→local, paste =
	// local→remote).
	ClipboardBlocked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "waas_wwt_clipboard_blocked_total",
		Help: "Clipboard streams blocked by the policy filter, by direction (copy | paste).",
	}, []string{"direction"})
)
