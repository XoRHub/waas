// Package metrics holds the API server's Prometheus metrics, registered
// on the default registry (which also carries the standard Go/process
// collectors). The /metrics endpoint itself is only mounted when
// WAAS_METRICS_ENABLED is true — see internal/server.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequests counts requests by chi route pattern (bounded
	// cardinality — never the raw path), method and status class.
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "waas_api_http_requests_total",
		Help: "HTTP requests served, by route pattern, method and status code.",
	}, []string{"route", "method", "status"})

	// HTTPDuration is the request latency histogram, same route/method
	// dimensions (status stays on the counter to bound series count).
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "waas_api_http_request_duration_seconds",
		Help:    "HTTP request duration by route pattern and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	// SSEClients is the number of currently connected event streams
	// (EventHub subscriptions).
	SSEClients = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "waas_api_sse_clients",
		Help: "Connected SSE event streams.",
	})

	// ActiveSessions is the number of open desktop sessions, refreshed by
	// the session sweeper on its interval (WAAS_SESSION_SWEEP_INTERVAL).
	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "waas_api_active_sessions",
		Help: "Open desktop sessions (refreshed by the session sweeper).",
	})

	// AuditEvents counts audit-trail entries by action — the metric mirror
	// of the append-only audit log. Policy refusals are
	// action="workspace.denied"; the label set is the audit action
	// vocabulary, bounded by code.
	AuditEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "waas_api_audit_events_total",
		Help: "Audit trail entries recorded, by action (workspace.denied = policy refusals).",
	}, []string{"action"})
)
