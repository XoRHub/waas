# Testing strategy

The platform's tests are organized in three tiers. Each tier exists
because the one below it **cannot** see a class of bugs — not because
more layers are better. Before writing a test, pick the lowest tier
that can actually catch the failure you care about.

| Tier | Harness | What only it can catch | Where |
|---|---|---|---|
| Unit / component | fake client, sqlite, `httptest` | logic: reconcile decisions, policy math, handler contracts | `internal/**/*_test.go` in every module |
| Integration | envtest (real apiserver + etcd), PostgreSQL service container | the **contracts of the real backends**: CRD schema/CEL, admission through the apiserver, SQL dialect divergence | `operator/test/envtest/`, `api-server/internal/repository/` |
| End-to-end | kind cluster, deployed images | the assembled system: GC cascades, RBAC, webhook certs, networking | `test/smoke/` |

## Tier 1 — unit and component tests

The bulk of the tests, and the only tier that runs on a bare
`go test ./...` / `npm test` with no toolchain downloads.

- **Operator**: reconciler and webhook logic against
  `fake.NewClientBuilder`. Right for "given this Workspace, the built
  Deployment looks like X" — wrong for anything involving schema
  validation or admission wiring (the fake client applies neither).
- **api-server**: handlers exercised **through the real router**
  (`internal/server/*_test.go`) against sqlite and a fake kube client —
  status codes, payload contracts, RBAC and ownership scoping. OIDC is
  tested against a stub IdP (`stubIdP`), never by mocking the client.
- **Frontend**: vitest + testing-library.

## Tier 2a — envtest (operator)

`operator/test/envtest/` starts a real kube-apiserver + etcd
(controller-runtime envtest) and runs the exact wiring of
`cmd/main.go`: both validating webhooks plus the workspace reconciler.

It exists for the three things nothing else evaluates:

1. **CRD schema as committed** (`crd_validation_test.go`) — the CEL
   one-of on WorkspaceImage, every enum, required fields, defaults.
   This is a *behavioral* drift check on `config/crd/bases`: the CI
   drift job proves the YAML is regenerated, this proves the YAML
   *does* what the markers promise.
2. **Admission end-to-end** (`webhook_admission_test.go`) — the request
   travels client → apiserver → ValidatingWebhookConfiguration →
   webhook server. One accept and representative rejects per webhook;
   the full validation matrix stays in `internal/webhook` unit tests.
3. **Finalizer lifecycle** (`finalizer_lifecycle_test.go`) — real
   deletionTimestamp semantics with the reconciler running, including
   the data-safety default (home PVC detached and labeled retained,
   never deleted implicitly).

**Known limits — do not test these here:**

- envtest has **no kube-controller-manager**: ownerReference garbage
  collection never runs, namespace deletion never completes. Assert
  that ownerRefs are *set*; the cascade belongs to the smoke test.
- envtest has no scheduler and no kubelet: pods never run, PVCs stay
  Pending. Readiness-dependent behavior is unit-tested with injected
  probes instead.

Run it with `make test-envtest` (wraps `make -C operator test-envtest`;
first run downloads the control-plane binaries via `setup-envtest`, pins
live in `operator/Makefile`). Without `KUBEBUILDER_ASSETS` the suite
skips itself, so plain `go test ./...` stays fast. `make check` chains
it; in CI the operator leg of `go-test` exports `KUBEBUILDER_ASSETS`,
which makes the ordinary `go test ./...` include the suite.

## Tier 2b — dual-backend repository suites (api-server)

`api-server/internal/repository/` runs every suite against **both**
storage backends via `forEachBackend`: sqlite always, PostgreSQL when
`WAAS_TEST_PG_URL` is set (one throwaway database per suite). The
dual-backend divergences are exactly where past bugs lived — RFC3339
timestamp scanners, JSON columns, NULL handling. A repository test that
only runs on sqlite proves nothing about production.

Locally, `make test-go-pg` runs the whole api-server module against a
throwaway postgres container (same pinned image as CI) and tears it
down. By hand:

```sh
docker run -d --rm -e POSTGRES_PASSWORD=pg -p 5432:5432 postgres:17-alpine
WAAS_TEST_PG_URL="postgres://postgres:pg@localhost:5432/postgres" \
  go test ./internal/repository/ -count=1 -race
```

In CI the `go-test` job provides a postgres service container and sets
the URL for the api-server leg.

## Tier 3 — smoke tests

`test/smoke/` runs against a kind cluster with the real images: the GC
cascade, RBAC as deployed, webhook certificates, wwt connectivity. See
`docs/smoke-connections.md`.

## Guard rails

- **Coverage ratchet** (api-server): `hack/ci/coverage-ratchet.sh`
  fails CI when per-package coverage drops below the floor
  (handler ≥ 40 %, repository ≥ 50 %; measured 56 % / 78 % when set).
  Raise the floors as coverage grows; never lower them.
- **Registry + exhaustiveness tests**: enumerations that exist in two
  representations (Go registry vs CRD enum, API model vs generated TS)
  are guarded by sync tests (`pkg/params/protocols_sync_test.go`,
  `internal/model/omitempty_guard_test.go`) so additions cannot drift.
- **Generated-artifact drift**: CI regenerates code, CRDs, docs and TS
  types and fails on any diff (`go-generated-drift` job).
- `-race` everywhere: every Go test invocation in CI runs with the race
  detector.
