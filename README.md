# WaaS — Kubernetes-native Workspace-as-a-Service

Coverage:

operator: [![operator](https://codecov.io/github/XoRHub/waas/graph/badge.svg?flag=operator)](https://codecov.io/github/XoRHub/waas/tree/main/operator)
api-server: [![api-server](https://codecov.io/github/XoRHub/waas/graph/badge.svg?flag=api-server)](https://codecov.io/github/XoRHub/waas/tree/main/api-server)
wwt: [![wwt](https://codecov.io/github/XoRHub/waas/graph/badge.svg?flag=wwt)](https://codecov.io/github/XoRHub/waas/tree/main/wwt)
frontend: [![frontend](https://codecov.io/github/XoRHub/waas/graph/badge.svg?flag=frontend)](https://codecov.io/github/XoRHub/waas/tree/main/frontend)

Open-source, Kubernetes-native Workspace-as-a-Service. `helm install` it on any cluster
(the only prerequisite is cert-manager) and give people a full remote desktop — Linux
(VNC) or Windows (RDP via KubeVirt, auto-detected) — accessible from any browser. See
[helm/waas/README.md](helm/waas/README.md) for install/upgrade instructions.

**Workspaces as code, GitOps-first:** a workspace is a Kubernetes resource
(`Workspace` / `WorkspaceTemplate` CRDs), created via `kubectl apply`, ArgoCD or Flux
like anything else in the cluster.

## Architecture

```
Browser ── HTTPS/WSS ──> API Server (chi) ──> PostgreSQL (users, quotas, sessions, audit)
                              │
                              ├──> Kubernetes API ──> Operator ──> Pod (Linux, VNC) / VM via KubeVirt (Windows, RDP)
                              │
                              └──> WebSocket Proxy (wwt) ──validates JWT first──> guacd (ClusterIP only) ──> pod/VM
```

| Component | Path | Role |
|---|---|---|
| Operator | `operator/` | Reconciles `Workspace`/`WorkspaceTemplate` CRDs into pods/PVCs (Linux) or KubeVirt VMs (Windows). K8s API only — never touches the DB. |
| API Server | `api-server/` | REST API, auth (local + OIDC), RBAC, business logic. Creates CRDs via the K8s API — never pods directly. |
| WebSocket Proxy | `wwt/` | Validates the JWT **before** opening any TCP connection to guacd. |
| Frontend | `frontend/` | React 19 admin dashboard + user portal. Only ever talks to the API Server. |
| Shared | `shared/` | JWT claims & auth primitives shared by API Server and proxy. |
| Helm chart | `helm/waas/` | Single-chart install: operator, API server, proxy, frontend, guacd, PostgreSQL — see [helm/waas/README.md](helm/waas/README.md). |

Hard boundaries (non-negotiable):

- Operator: K8s API only, never DB access.
- API Server: never creates pods/VMs directly — always through CRDs.
- Frontend: never calls the K8s API directly.
- guacd: ClusterIP only, never exposed outside the cluster; the proxy validates the JWT before connecting.
- Audit logs are append-only.

## Quickstart (local dev)

```sh
mise install            # Go 1.26 + Node
make build test         # all Go modules
make generate manifests # operator codegen (CRDs, RBAC, deepcopy)
make frontend-build

# Run the API server against SQLite (dev only) without a cluster:
cd api-server && WAAS_DEV=true go run ./cmd/api-server
```

Local cluster (k3d): `make dev-bootstrap` — creates the `waas-dev` cluster with
cert-manager, builds and imports every image (services + the desktops from the
[waas-images repo](https://github.com/XoRHub/waas-images), expected as a
sibling checkout — `WAAS_IMAGES_DIR` overrides the path),
deploys the chart and seeds the dev catalog; the URL and credentials are printed
at the end. After code changes: `make dev-reload` (services/frontend) or
`make dev-reload-all` (also rebuilds the desktop images). `make dev-down` tears
it down, `make smoke` validates real per-protocol sessions.

## CI/CD

GitHub Actions pipeline (`.github/workflows/ci.yml`): selective per-component
builds on PRs, native amd64/arm64 image builds merged into multi-arch
manifest lists, blocking security gates (gitleaks, Trivy), and releases by
**promotion** — a `vX.Y.Z` git tag re-tags and cosign-signs the exact
digests a green `main` pipeline already tested and scanned. ArgoCD deploys
the git tag (path `helm/waas`). Pipeline map, release procedure, registry
cleanup and debugging: see [docs/ci-github.md](docs/ci-github.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) — dev environment, pre-PR checks,
commit conventions. AI coding agents: start with [AGENTS.md](AGENTS.md).

## License

Apache-2.0 — community edition, free forever.
