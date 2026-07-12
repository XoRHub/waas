# Fable 5 Prompt — Feature 2: Prometheus metrics + Grafana dashboards

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

WaaS has three Go components that run as pods in the cluster: `wwt/` (websocket proxy, single port `:8081`), `api-server/` (chi REST backend, single port `:8080`), `operator/` (controller-runtime, already scaffolded with a standard controller-runtime metrics endpoint on `:8080` — see below). **None of the three exposes application metrics today** — verified: zero `prometheus` import anywhere in the repo's Go module, zero `ServiceMonitor`/`PodMonitor` in `helm/waas/`, zero Grafana reference outside the historical brief. This is an entirely greenfield effort.

## What already exists (know this before coding)

**operator (`operator/cmd/main.go`)**: the controller-runtime manager is already configured with `metricsserver.Options{BindAddress: ":8080"}` (`ctrl.Options` line ~59) — this is the `metrics` port already declared in `helm/waas/templates/operator.yaml:176-177` on the Deployment. This already exposes, with no extra work, the standard controller-runtime/client-go metrics (`controller_runtime_reconcile_total`, `workqueue_*`, etc.) — **but there is no Kubernetes `Service` exposing this port** (only a webhook `Service` exists, `helm/waas/templates/operator-webhook.yaml:29-42`, port 443→`webhook`). Without a Service, a `ServiceMonitor` has nothing to target; a `PodMonitor` (selection by Pod labels, no Service needed) works directly — this is probably the right tool for the operator.

**api-server (`api-server/cmd/api-server/main.go`)**: a single `http.Server` with only `ReadHeaderTimeout` (a gap already noted in `docs/studies/audit-2026-07.md` §api-server — add `ReadTimeout`/`IdleTimeout` if you touch this, but that's not the point of this feature). The chi router (`api-server/internal/server/router.go`) mounts `/healthz`, `/readyz`, `/.well-known/jwks.json` outside `/api/v1`, then everything else under `/api/v1` behind `middleware.Auth`. The helm Service (`helm/waas/templates/api-server.yaml:242-255`) exposes the `http` port (8080) — a `ServiceMonitor` can target this Service directly.

**wwt (`wwt/cmd/main.go`)**: raw `http.ServeMux`, `/ws` + `/kasm/` + `/healthz` + `/readyz` on `:8081`. Helm Service (`helm/waas/templates/wwt.yaml:58-71`) exposes the `http` port (8081) — same remark, `ServiceMonitor` OK.

**Public exposure — verified, do not break anything:** `helm/waas/templates/ingress.yaml` and `httproute.yaml` only allow-list explicit paths (`/api`, `/.well-known/jwks.json`, `/ws`, `/kasm`, `/` for the frontend). A new `/metrics` endpoint mounted on the same port as application traffic **is already unreachable from the outside** as long as you don't add it to these files — do not add it. Prometheus scraping happens exclusively in-cluster (via the ClusterIP Service or directly via Pod IPs for the operator's PodMonitor), never through the public Ingress.

## What needs to be delivered

### A. Application metrics in the 3 components

Add `github.com/prometheus/client_golang` (already present transitively in `go.sum` via `operator`/`api-server` dependencies, to be promoted to a direct dependency) and a `/metrics` endpoint (`promhttp.Handler()`) on each component's existing port — no separate port to create, reuse `:8080`/`:8081`/`:8080` (operator).

No "free" metrics beyond what controller-runtime already gives the operator: each component must expose **business** metrics, grounded in real existing code rather than invented. Concrete leads (adjust names/labels to taste, keep the principle — one metric per signal that already has a trace in the code):

- **operator**: gauge of the number of workspaces per phase (the `WorkspaceReconciler` already knows `status.Phase` at every reconcile — see `operator/internal/controller/workspace_controller.go`), counter of drifted workspaces (`TemplateDrifted`, cf. `docs/adr/0001-template-boundary-convergence.md` and the Feature 1 prompt if you implement that too), counter of `NamespaceJanitor` reclamations (`operator/internal/controller/namespace_janitor.go`), counter of teardown failures (the `TeardownFailed` Event already exists, `docs/workspace-deletion.md` — mirror it as a metric), duration/errors per reconciler type if you want to go further than the already-free controller-runtime metrics.
- **api-server**: duration histogram + counter of HTTP requests by route/method/status (chi middleware to add in `router.go`, before or after `chimiddleware.Recoverer`), gauge of active sessions (the `SessionSweeper` — project memory: "WAAS_SESSION_SWEEP_INTERVAL goroutine" — already knows session state), gauge of connected SSE clients (`EventHub`, `api-server/internal/service/event_hub.go`), counter of policy denials (every denial already goes through `s.audit.Record(ctx, actor, "workspace.denied", ...)` in `workspace_service.go` — add a counter at the same call site rather than duplicating the detection logic).
- **wwt**: gauge of active tunnels (guacd + kasm, both handlers exist in `wwt/internal/proxy` and `wwt/internal/kasm`), counter of proxied bytes, counter of connection-token validation failures (`proxy.ValidateConnectionToken`, already exported and shared between the two paths), counter of `ClipboardFilter` blocks (`wwt/internal/guac`, already policy-aware).

Every counter/gauge added must have a test (the repo has a culture of systematic testing — `wwt` is at 87/86.8/62.2% coverage on `guac`/`kasm`/`proxy`, do not lower that).

### B. Helm chart: PodMonitor / ServiceMonitor / scrape annotations

Make all three mechanisms available as `values.yaml` toggles, disabled by default (consistent with the rest of the chart — e.g. `operator.webhook.enabled`, `apiServer.oidc.*` — everything is opt-in with an explanatory comment):

```yaml
metrics:
  enabled: false          # exposes /metrics on each component (always cluster-internal, never via the Ingress)
  scrapeAnnotations: false  # sets prometheus.io/scrape=true, /port, /path on the Pods (classic Prometheus without CRDs)
  podMonitor:
    enabled: false         # operator (no metrics Service) — extend to all 3 if you prefer uniformity
    labels: {}             # additional labels for the targeted Prometheus/PrometheusOperator selector
    interval: 30s
  serviceMonitor:
    enabled: false         # api-server + wwt (already have a ClusterIP Service)
    labels: {}
    interval: 30s
```

`metrics.enabled: false` must cut off the endpoint itself (serve nothing), not just hide the K8s manifest — avoid exposing an endpoint that wasn't requested by default. Add `metrics` as a named port on the relevant Deployments only when relevant for the targeted `PodMonitor`/`ServiceMonitor`.

The `PodMonitor`/`ServiceMonitor` CRDs come from prometheus-operator and are not necessarily installed on every target cluster — that's why they must remain opt-in and must never make `helm template`/`helm install` fail when disabled (no blocking `lookup`, no hard dependency on the CRDs). Check `helm lint` and `helm template` with the toggles both enabled AND disabled (the repo already has a CI job, `.github/workflows/ci.yml`, that lints/templates the chart — stay consistent with what `docs/ci-github.md` covers).

### C. Grafana dashboards

Provide at least one dashboard JSON per component (or a single multi-component dashboard if you prefer — panels on the metrics added in A) and two deployment modes, as `values.yaml` toggles:

```yaml
grafana:
  dashboards:
    enabled: false
    mode: configmap        # "configmap" | "operator"
    # configmap mode: grafana sidecar (official grafana/grafana Helm
    # chart convention) — grafana_dashboard: "1" label on the ConfigMap,
    # automatically picked up by the discovery sidecar.
    # operator mode: grafana-operator (grafana.integreatly.org/v1beta1
    # GrafanaDashboard) — requires targeting an existing Grafana instance.
    instanceSelector:       # used only in "operator" mode
      matchLabels: {}
    folder: WaaS
```

- `configmap` mode: one `ConfigMap` per dashboard with the `grafana_dashboard: "1"` label (convention of the `kiwigrid/k8s-sidecar` sidecar bundled by the official Grafana chart — most Prometheus/Grafana stack installs already use it, zero dependency on an operator).
- `operator` mode: one `GrafanaDashboard` CR (`grafana.integreatly.org/v1beta1`) per dashboard, `spec.instanceSelector` driven by `values.grafana.dashboards.instanceSelector`, `spec.json` = the dashboard content. Do not install the grafana-operator CRDs yourself (as with prometheus-operator, opt-in, never a hard dependency).
- Both modes must render the **same dashboard content** (not two diverging JSON files to maintain — factor the JSON into a single file/Helm `include` consumed by both templates).

## Constraints to respect

- Follow the principle already applied everywhere in this chart: every new capability is **opt-in by default**, documented with a comment in `values.yaml`, never a behavior that silently changes existing behavior.
- Clean `gofmt` across the 3 Go modules; no new `console.log`/`TODO`.
- Add the new doc (`docs/observability.md` or equivalent — check that such a file doesn't already exist before creating one) describing: the metrics exposed per component, how to enable PodMonitor/ServiceMonitor/annotations, how to enable the dashboards in both modes.
- Update `docs/studies/audit-2026-07.md` only if you touch it directly (it is not a living document to be maintained on every feature — leave it as-is unless one of its lines becomes false).
- The GitHub Actions CI (`docs/ci-github.md`) builds per component via a `go.work replace` graph — if you add `prometheus/client_golang` as a direct dependency to `operator`/`api-server`/`wwt`, verify that `go mod tidy` stays consistent in each module and that the root `go.work.sum` follows.

## Open points (your call)

- Standardize on `PodMonitor` everywhere (simpler, no dependency on a Service existing) vs. `ServiceMonitor` for api-server/wwt which already have a Service: both are defensible, choose and document the choice in `docs/observability.md`.
- Authentication of the `/metrics` endpoint (none today on api-server/wwt, unlike the rest of the API): since it is never exposed publicly (see above), staying unauthenticated in-cluster is consistent with standard Prometheus practice — confirm/document this rather than reconsidering it by default.
