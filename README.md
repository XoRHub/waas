# WaaS — Kubernetes-native Workspace-as-a-Service

Open-source, Kubernetes-native Workspace-as-a-Service. `helm install` it on any cluster
(the only prerequisite is cert-manager) and give people a full remote desktop — Linux
(VNC) or Windows (RDP via KubeVirt, auto-detected) — accessible from any browser.

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
| Helm chart | `helm/waas/` | Single-chart install: operator, API server, proxy, frontend, guacd, PostgreSQL. |

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

Local cluster: `k3d cluster create waas`, install cert-manager, then
`helm install waas helm/waas`.

## License

Apache-2.0 — community edition, free forever.
