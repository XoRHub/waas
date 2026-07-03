# WaaS — Build Briefing Prompt (for Fable)

You are joining the **WaaS** project as a builder. Your job is to implement features in this codebase, story by story, following the architecture and conventions below exactly. Read this whole brief before writing any code.

## 1. What WaaS is (the principle)

WaaS is an **open-source, Kubernetes-native Workspace-as-a-Service platform**. Any organization that already has a Kubernetes cluster can `helm install` it and give people (employees, contractors, trainees) a full remote desktop — Linux or Windows — accessible from any browser, in hours, with zero extra infrastructure beyond K8s + cert-manager.

**The problem it solves:** the workspace/VDI market is broken. KASM is proprietary and unauditable. Citrix is an aging, monolithic, per-vCPU-licensed nightmare that takes weeks to deploy. AWS WorkSpaces / Azure Virtual Desktop expose data to the US Cloud Act regardless of server location. Nothing on the market is K8s-native, GitOps-first, and production-ready with real fleet management.

**The solution, concretely:**
- A **Kubernetes Operator** owns two CRDs, `Workspace` and `WorkspaceTemplate`. A workspace is just a K8s resource — created via `kubectl apply`, ArgoCD, or Flux, like anything else in the cluster. This is the core differentiator: **workspaces as code, GitOps-first, day one.**
- Each workspace is a Linux pod (VNC) or — if KubeVirt is detected in the cluster — a Windows VM (RDP). Desktop environments (XFCE/KDE themed to look Windows-like) target non-technical users who've never heard of Kubernetes.
- **Apache Guacamole (guacd)** is the single gateway: HTML5 in the browser, no client install, VNC and RDP unified behind one proxy. `guacd` is never exposed outside the cluster — a Go WebSocket proxy validates the JWT *before* it ever opens a TCP connection to guacd.
- User state (home directory, files) lives on a PVC decoupled from the pod, so a workspace can be destroyed and recreated without losing anything the user left behind.
- Auth is local user/password by default (zero external dependency to get started), with OIDC (Keycloak, Authentik, Azure AD) and enforced TOTP 2FA as it matures.
- An admin dashboard gives fleet visibility (traffic-light health per workspace), template/user/group management, audit trail. A user-facing portal is deliberately invisible — the end user should never feel like they're "using WaaS," they just open a browser and their desktop is there.

**Who it's for (personas):**
- **Alex** — the sysadmin/DevOps who installs it. Already runs ArgoCD/Flux, hates tools that fight his GitOps workflow or reinvent Prometheus/Grafana. His "aha" moment: his first workspace shows up in ArgoCD like any other resource.
- **Sophie** — the CIO/decision-maker, driven by a compliance trigger (CNIL injunction, HDS audit) or a cost trigger (Citrix renewal). Doesn't touch the product, needs audit trails and data sovereignty.
- **Marc** — the end user. Zero K8s knowledge, any device, any network. Should never notice the platform exists. If Marc opens a support ticket, the product has failed.

**Model:** open core. Community edition free forever (everything in this brief). Enterprise features (SSO/LDAP-SCIM, Image Builder, Falco/eBPF, session export, shadow sessions, warm pools) come later, gated by license, same cluster, no migration.

## 2. System architecture

Five components, monorepo, strict boundaries between them:

```
Browser ── HTTPS/WSS ──> API Server (chi) ──> PostgreSQL (users, templates, quotas, sessions, audit)
                              │
                              ├──> Kubernetes API ──> Operator (kubebuilder) ──> Pod (Linux, VNC) / VM via KubeVirt (Windows, RDP)
                              │
                              └──> WebSocket Proxy (wwt/guac) ──validates JWT first──> guacd (ClusterIP only) ──> pod/VM
```

- **Operator** (`operator/`, kubebuilder v4.11.1, controller-runtime v0.23.1, K8s ≥1.26): reconciles `Workspace` and `WorkspaceTemplate` CRDs into pods/PVCs (Linux) or VMs (Windows, only if KubeVirt CRDs are detected at startup — otherwise Windows requests are rejected by a validating webhook, never silently ignored). **Never touches the DB. K8s API only.**
- **API Server** (`api-server/`, Go, chi v5, pgx/v5): REST API, auth (local + OIDC), RBAC, business logic. Talks to Postgres for platform config and to the K8s API (via client-go) to create/read CRDs — **never creates pods directly, always goes through the CRDs.**
- **WebSocket Proxy** (`wwt/guac`, gorilla/websocket): sits between the browser and guacd. Validates the JWT before opening any TCP connection to guacd. This is a hard security requirement, not optional.
- **Frontend** (`frontend/`, React 19 + Vite 6.3.2): admin dashboard + user portal. **Never calls the K8s API directly — always through the API Server.**
- **guacd** (guacamole/guacd:1.6.0, pinned): unified HTML5 gateway for VNC + RDP. **ClusterIP only, never exposed outside the cluster.** Has no native auth — its entire security model is "the Go proxy validated the JWT before connecting."

## 3. Pinned tech stack

- Go 1.26 (mise-managed), `go.mod` declares `go 1.25.3` (required by controller-runtime v0.23.1 + k8s 0.35.0)
- Operator: kubebuilder v4.11.1, controller-runtime v0.23.1, k8s libs 0.35.0
- API Server: chi v5, pgx/v5 (Postgres prod), modernc.org/sqlite (pure-Go, dev only — never mattn/go-sqlite3, it breaks CGO_ENABLED=0 multi-arch builds), golang-jwt/jwt v5, MicahParks/keyfunc (JWKS), golang-migrate with SQL files embedded via `embed.FS`
- Frontend: React 19, Vite 6.3.2, Tailwind CSS 4.2.1, shadcn/ui (components copied locally via `npx shadcn@latest add`, never `npm install shadcn-ui`), TanStack Query 5.90.21, Zustand, React Router 7.13.1 (import from `react-router`, not `react-router-dom`), i18next (EN/FR from v1), Recharts
- Infra: guacd 1.6.0, PostgreSQL 17, cert-manager (the *only* mandatory external prerequisite), KubeVirt (optional, auto-detected, never listed as a Helm prerequisite), Helm 3.17.0, k3d 5.8.3 for local dev, GitHub Actions CI/CD → ghcr.io, multi-arch (amd64/arm64), distroless final images (zero shell — health checks are HTTP probes only, never `exec`)

## 4. Non-negotiable architectural boundaries

- Operator: K8s API only, never DB access.
- API Server: never creates pods/VMs directly — always through Workspace/WorkspaceTemplate CRDs.
- Frontend: never calls K8s API directly — always through the API Server.
- guacd: ClusterIP only, never exposed outside the cluster.
- WebSocket proxy: always validates the JWT before connecting to guacd.
- JWT validated on every request via middleware — no bypass routes.
- Audit logs are append-only — never UPDATE/DELETE on `audit_logs`.
- TLS 1.3 everywhere between components, via cert-manager.

## 5. Coding conventions (follow exactly, don't improvise)

**Go**
- `context.Context` is always the first parameter of any I/O function; never stored in a struct.
- Wrap errors with context: `fmt.Errorf("reconciling workspace %s: %w", name, err)`. Never log-and-return (no double logging) — log only at the entry point.
- Sentinel errors: `var ErrWorkspaceNotFound = errors.New(...)`.
- Packages: lowercase single word (`controller`, `handler`, `middleware`). Files: `snake_case.go`. Tests colocated, same package.
- Handlers are methods on an injectable struct (`type WorkspaceHandler struct { svc WorkspaceService }`), never package-level vars — handlers call services, never repositories directly.
- Repository pattern: interfaces like `FindByID/Create/Update/Delete(ctx, ...)`.
- Operator reconciliation: always `GenerationChangedPredicate`; `r.Status().Update()` for status (never `r.Update()`, which bumps Generation and causes infinite reconcile loops); re-fetch fresh before any status update to avoid version conflicts; always check existence before creating a pod/PVC (idempotence); transient states return `ctrl.Result{RequeueAfter: 5s}, nil`. Never regenerate `zz_generated.deepcopy.go` by hand — run `make generate && make manifests` after any change under `api/v1alpha1/`. Never hand-write RBAC — it's generated from `+kubebuilder:rbac:` markers. Never read env vars/secrets in a global var or `init()` — only in `cmd/main.go` after the manager starts.

**TypeScript / React**
- `strict: true`, never `any` without a justifying comment.
- Absolute imports via `@/`, never relative `../../..`.
- Components `PascalCase.tsx`, hooks/utils `camelCase.ts`.
- Always guard TanStack Query state (`isPending`/`isError`) before touching `data` — never `data?.data` as a shortcut. `onError`/`onSuccess` were removed from `useQuery` in v5 — handle errors in `useMutation`.
- Every component with visible text uses `useTranslation()` — no hardcoded user-facing strings, including shared components.
- Navigation via `useNavigate()`, never `window.location`.

**API design**
- Every response wrapped in `{ "data": ... }` (lists also get `meta: { total, page, page_size }`); errors are RFC 7807 (`type`, `title`, `status`, `detail`).
- Routes: kebab-case plural (`/api/v1/workspace-templates`), non-CRUD actions as `POST /resource/{id}/action`.
- JSON fields `camelCase`, dates ISO 8601 UTC, IDs are UUID v4 strings (never auto-increment ints exposed).
- SSE events named `{resource}.{verb_past}` (e.g. `workspace.status_changed`).

**Database**
- Tables `snake_case` plural, columns `snake_case`, FKs `{table_singular}_id`, indexes `idx_{table}_{cols}`.
- Migrations: `{YYYYMMDDHHMMSS}_{description}.sql` in `api-server/migrations/`, embedded via `//go:embed`, applied automatically at binary startup.

**Git**
- Conventional Commits: `<type>(<scope>): <description>` — types `feat|fix|docs|test|refactor|chore|ci|build`, scopes `operator|api-server|frontend|helm|shared|planning`.
- One logical change per commit (one story or one bugfix), never a broken commit, never mix `feat` + `refactor`, breaking changes get `!` after the type.

## 6. Scope & source of truth

This is a 9-epic, 44-story plan: Platform Bootstrap, Identity & Access Management, Workspace Configuration, Workspace Lifecycle & Persistence, Remote Desktop Connectivity, User Self-Service Portal, Fleet Operations & Administration, GitOps & API Completeness, Production Hardening. The full breakdown with acceptance criteria per story lives in `_bmad-output/planning-artifacts/epics.md` — that file is the requirements source of truth.

Full technical rules live in `_bmad-output/project-context.md` — read it before implementing anything; it has more detail than fits here (testing patterns, anti-pattern lists, exact Makefile workflow).

## 7. How to work

1. Work story by story, in epic order, following `epics.md`.
2. Read the story's acceptance criteria before implementing it.
3. Implement it respecting every rule above — no shortcuts, no scope creep beyond the story's acceptance criteria.
4. Keep the components' boundaries intact even when it would be faster to cheat (e.g. don't let the frontend call K8s directly, don't let the API server touch pods directly).
5. Commit atomically with a Conventional Commit message scoped to that one story.
