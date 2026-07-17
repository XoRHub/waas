# AGENTS.md — working on WaaS as a coding agent

Monorepo: `operator/` (Go, controller-runtime), `api-server/` (Go, chi),
`wwt/` (Go websocket proxy), `frontend/` (React 19 + Vite), `shared/`
(Go), `helm/waas/` (single chart). Architecture overview: [README](README.md).

## Hard boundaries — never cross these

- **Operator**: talks to the Kubernetes API only. Never touches the database.
- **API Server**: never creates pods/VMs directly — always through the
  `Workspace`/`WorkspaceTemplate` CRDs.
- **Frontend**: only ever talks to the API Server, never the K8s API.
- **guacd**: ClusterIP only, never exposed outside the cluster; `wwt`
  validates the JWT **before** opening any TCP connection to it.
- **Audit logs**: append-only.

A change that needs to cross one of these boundaries is an architecture
change — stop and ask, don't route around it.

## Generated code — regenerate, never hand-edit

CRDs/RBAC/deepcopy (`operator/`, `helm/waas/crds/`),
`frontend/src/types.gen.ts` and `operator/docs/guacd-parameters.md` are
generated. CI fails on drift. After touching their sources:

```sh
make generate manifests docs-params generate-types
```

## Commits

Conventional Commits, atomic (one logical change, never a broken
commit): `<type>(<scope>): <description>` — types `feat` `fix` `docs`
`refactor` `chore` `ci` `style` `test`; scopes `operator` `api-server`
`frontend` `helm` `shared` (omit when none fits); `!` for breaking.
Real examples:

```
fix(operator): avoid gofmt doc-comment quote mangling in a CEL rule
ci: split release-please into app and chart packages
refactor!: split waas-images/ out into its own repository
```

## Code style (as practiced in this repo)

- Comments state a non-obvious **why** or a constraint the code can't
  show — not what the next line does. Match the density of the
  surrounding file.
- No premature abstraction: small targeted patches over rewrites;
  a helper is extracted when a second caller exists, not before.
- Docs are updated **in the same change** that makes them stale — the
  history is full of `docs: document X in Y.md` commits paired with the
  code they describe.

## References (don't duplicate, read when relevant)

| Topic | Where |
|---|---|
| Toolchain versions (single source — CI installs from it via mise-action) | `.mise.toml` |
| CI pipeline, gates, release-by-promotion | `docs/ci-github.md` |
| Test tiers: unit / envtest / smoke, what belongs where | `docs/testing.md` |
| Architecture decisions already made | `docs/adr/` |
| Contributor workflow, DCO | `CONTRIBUTING.md` |

## Definition of done

Before reporting a task finished, run what CI runs: `make check`
(Go + frontend lint/format/typecheck/tests + generated-code drift;
granular: `test-go`/`test-frontend`, `lint-go`/`lint-frontend`,
`format`, `generate-check`); for the chart: `helm lint helm/waas`,
`make helm-unittest`, `make helm-docs`.
For behavior changes, verify on the k3d dev env (`make dev-bootstrap`,
then `make dev-reload` as inner loop; `make smoke` gates real sessions).
