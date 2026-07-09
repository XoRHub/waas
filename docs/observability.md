# Observability — Prometheus metrics and Grafana dashboards

Every WaaS component can expose Prometheus metrics, and the chart ships
Grafana dashboards over them. **Everything is opt-in and disabled by
default**, like every other chart capability: with the default values the
`/metrics` endpoints are not merely unadvertised — they are not served at
all.

## Enabling metrics

```yaml
metrics:
  enabled: true
```

This makes each component serve `/metrics` on its existing port — no new
port, no new Service:

| Component  | Endpoint                | How it is switched                                        |
| ---------- | ----------------------- | --------------------------------------------------------- |
| api-server | `:8080/metrics`         | `WAAS_METRICS_ENABLED=true` (chi route only mounted then) |
| wwt        | `:8081/metrics`         | `WWT_METRICS_ENABLED=true` (mux route only mounted then)  |
| operator   | `:8080/metrics`         | controller-runtime metrics server; disabled via `--metrics-bind-address=0` when off |

Note for existing deployments: before this feature the operator's
controller-runtime metrics server was always listening (nothing scraped
it — it had neither Service nor monitor). It now follows the same opt-in
rule as everything else; set `metrics.enabled: true` to get it back.

### Never exposed publicly

`helm/waas/templates/ingress.yaml` and `httproute.yaml` allow-list
explicit paths only (`/api`, `/ws`, `/kasm`, `/.well-known/jwks.json`,
`/`). `/metrics` is deliberately **not** in those lists and must never be
added: scraping happens in-cluster, via the ClusterIP Services or the pod
IPs. This is also why the endpoint carries no authentication of its own —
it is unreachable from outside and cluster-internal unauthenticated
`/metrics` is the standard Prometheus practice. Add a NetworkPolicy in
front of the platform namespace if your threat model includes in-cluster
scrape access.

## Scrape wiring — three mechanisms, pick one

### 1. Scrape annotations (plain Prometheus, no CRDs)

```yaml
metrics:
  enabled: true
  scrapeAnnotations: true
```

Stamps `prometheus.io/scrape: "true"`, `prometheus.io/port`,
`prometheus.io/path` on the three pods, for `kubernetes_sd`
annotation-based relabel configs.

### 2. PodMonitor — operator (prometheus-operator)

```yaml
metrics:
  enabled: true
  podMonitor:
    enabled: true
    labels: {}        # e.g. release: prometheus (kube-prometheus-stack)
    interval: 30s
```

The operator gets a **PodMonitor** and not a ServiceMonitor on purpose:
it has no metrics Service (only the webhook one), and a PodMonitor
selects pods directly, so no Service is invented just for scraping.

### 3. ServiceMonitor — api-server + wwt (prometheus-operator)

```yaml
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    labels: {}
    interval: 30s
```

Both components already have a ClusterIP Service carrying the shared
`http` port; the ServiceMonitors ride it with `path: /metrics`.

The `monitoring.coreos.com` CRDs are **not** installed by this chart and
are never a hard dependency: with the toggles off (the default), `helm
template`/`helm install` succeed on clusters without them.

If your Prometheus uses selector-based discovery (kube-prometheus-stack
defaults), set `metrics.podMonitor.labels` / `metrics.serviceMonitor.labels`
to whatever its `podMonitorSelector`/`serviceMonitorSelector` matches.

## Exposed metrics

All series are prefixed `waas_<component>_`. The operator additionally
exposes the standard controller-runtime/client-go metrics
(`controller_runtime_reconcile_total`, `workqueue_*`, `rest_client_*`, …)
and every component exposes the Go/process collectors.

### operator (`operator/internal/metrics`)

| Metric | Type | Meaning |
| ------ | ---- | ------- |
| `waas_operator_workspaces{phase}` | gauge | Workspaces by `status.phase`, computed at scrape time from the manager's cache (all known phases zero-filled). |
| `waas_operator_workspaces_drifted` | gauge | Workspaces whose `TemplateDrifted` condition is True (docs/adr/0001). |
| `waas_operator_namespace_reclaims_total` | counter | Namespaces reclaimed by the janitor (`DeleteWhenEmpty`, provably empty). |
| `waas_operator_teardown_failures_total` | counter | Failed teardown finalizer passes — the metric mirror of the `TeardownFailed` Event (each retry counts; see docs/workspace-deletion.md). |

### api-server (`api-server/internal/metrics`)

| Metric | Type | Meaning |
| ------ | ---- | ------- |
| `waas_api_http_requests_total{route,method,status}` | counter | Requests by **chi route pattern** (`/api/v1/workspaces/{id}` — never the raw path; unrouted requests aggregate under `route="unmatched"`). |
| `waas_api_http_request_duration_seconds{route,method}` | histogram | Request latency, same pattern labels. |
| `waas_api_sse_clients` | gauge | Connected SSE event streams (EventHub subscriptions). |
| `waas_api_active_sessions` | gauge | Open desktop sessions, refreshed by the session sweeper each `WAAS_SESSION_SWEEP_INTERVAL`. |
| `waas_api_audit_events_total{action}` | counter | Audit-trail entries by action — the metric mirror of the append-only audit log. Policy refusals are `action="workspace.denied"`; alert on that series rather than re-deriving refusal detection. |

### wwt (`wwt/internal/metrics`)

| Metric | Type | Meaning |
| ------ | ---- | ------- |
| `waas_wwt_active_tunnels{protocol}` | gauge | Live desktop streams per data plane: `guacd` (WebSocket tunnels) and `kasmvnc` (websockify streams; asset requests don't count). |
| `waas_wwt_proxied_bytes_total{direction}` | counter | Bytes relayed through the **guacd** tunnel (`to_browser` / `to_desktop`). The kasmvnc plane flows through `httputil.ReverseProxy` and is not byte-counted. |
| `waas_wwt_token_validation_failures_total` | counter | Connection JWTs rejected by the shared validator (`/ws` and `/kasm` gate on the same one). A spike is a TTL misconfiguration or someone probing with forged tokens. |
| `waas_wwt_clipboard_blocked_total{direction}` | counter | Clipboard streams dropped by the policy filter (`copy` = remote→local, `paste` = local→remote), one increment per blocked stream. |

## Grafana dashboards

One JSON per component under `helm/waas/dashboards/` (operator,
api-server, wwt), consumed **identically** by both deployment modes —
there is a single source of truth, never fork the JSON per mode. Each
dashboard carries a `datasource` template variable, so any Prometheus
data source works.

### Mode `configmap` (default) — sidecar discovery

```yaml
grafana:
  dashboards:
    enabled: true
    mode: configmap
    folder: WaaS
```

Renders one ConfigMap per dashboard labeled `grafana_dashboard: "1"` and
annotated `grafana_folder`, the convention of the discovery sidecar
shipped by the official grafana chart and kube-prometheus-stack. No
operator required.

### Mode `operator` — grafana-operator CRs

```yaml
grafana:
  dashboards:
    enabled: true
    mode: operator
    folder: WaaS
    instanceSelector:
      matchLabels:
        dashboards: grafana   # must match your Grafana CR's labels
```

Renders one `GrafanaDashboard` (`grafana.integreatly.org/v1beta1`) per
dashboard. The grafana-operator CRDs are not installed by this chart —
same opt-in rule as the prometheus-operator ones.

## Testing conventions

Every business gauge/counter has a test riding the real code path (a
blocked clipboard stream, a swept session, a janitor reclaim, …), using
`prometheus/testutil` **deltas** — the metrics are process-global, so
tests never assert absolute values except where they own the whole state.
