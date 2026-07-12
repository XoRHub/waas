# Prompt Fable 5 — Audit 2: remediation of the "Small" findings (orders 1, 2, 4, 5, 6, 7, 8)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Source

`docs/studies/20-report-audit2-organisation-doublons-securite.md` is
the full audit report (2026-07-11). Read it first in its entirety,
especially the §4 findings table and the §5 action plan — this prompt
saves you from redoing the file:line research for each finding, but §4
gives the full context and reasoning behind each one.

## Scope of this prompt

The action plan (§5) sorts the findings into 10 orders, from quick win
to structuring effort. This prompt covers **orders 1, 2, 4, 5, 6, 7 and
8** — every order of effort "S" except one, with one order handled
differently from what the report says:

- **Order 3 (C3 activate Renovate + C5 trivy bump) — EXCLUDED from
  this prompt.** Activating the Renovate app is a GitLab action
  (project settings), not a code change; don't do it as part of this
  prompt.
- **Order 5 (C13, Helm secrets via `lookup`) — handled as a FIX, not a
  verification.** The report proposes "S (verify ArgoCD's rendering) →
  M (fix if confirmed)". **This is already confirmed**: Helm's
  `lookup` function doesn't work under ArgoCD (the repo-server renders
  the chart with no access to the destination cluster, `lookup`
  always returns an empty map there) — don't waste time re-verifying
  this, go straight to implementing a proper fix (details below,
  Order 5 section).
- **Orders 9, 10 and the last line of the plan ("along the way") are
  out of scope** — these are the M/debatable efforts, left for a
  dedicated prompt later.

Each section below corresponds to one order of the plan. Handle them
in order (some touch the same CI files, see the sequencing notes). One
commit per order (or per finding if you prefer finer grain), never a
single catch-all commit — these are changes of a different nature
(doc cleanup, CI, generated types, security, GitOps).

---

## Order 1 — Cleanup (C24, C4, C23, C22)

- **C24 (docs git hygiene)**: at the time this prompt was written,
  `git status` is already clean — this finding appears already
  resolved. Verify anyway (`git status --short`); if there are
  untracked/modified files under `docs/`, commit them before
  continuing, otherwise do nothing for this point.
- **C23 (root leftover)**: `fable-waas-build-prompt.md` at the repo
  root is the historical bootstrap brief, with no current use. By
  default `git rm` (the report prefers this: "one minute"). If you
  judge it has archival value, move it into `docs/studies/` instead of
  deleting it — document your choice in the commit message. You may
  delete it entirely from the tree.
- **C22 (dead spec)**: `docs/openapi-governance.yaml` is a manual,
  partial OpenAPI spec, not consumed (grep the repo to confirm nothing
  references it outside `docs/`) and redundant since tygo generates
  types. Delete it.
- **C4 (lying CI comments about eslint)** — **don't do this in
  isolation here**: handle it in Order 2 below, at the same time as
  adding the GitLab eslint job. If you fix the comment before the job
  actually exists on GitLab, it becomes false again between the two
  commits. Note it as deferred to Order 2, don't commit it twice.

---

## Order 2 — CI: restore already-written gates to GitLab (C2, C14) + C4

`.gitlab/ci/go.yml` and `.gitlab/ci/frontend.yml` are the CI that
actually counts (the local remote is GitLab only, `docs/ci.md:1`: "A
single entry point: `.gitlab-ci.yml`"). Three gates already exist on
GitHub (`.github/workflows/ci.yml`) but not on GitLab:

### 2.1 — envtest on GitLab (`go-test`, `operator` module)

`.gitlab/ci/go.yml:30-44` (job `go-test`, matrix
`MODULE: [shared, operator, api-server, wwt]`) never sets
`KUBEBUILDER_ASSETS`: the `operator/test/envtest/` suite silently
skips (`suite_test.go:12-13` — check the exact line, the report places
it there). The GitHub mechanism to replicate
(`.github/workflows/ci.yml:242-245`):

```yaml
- name: Install envtest control plane
  if: matrix.module == 'operator'
  working-directory: operator
  run: echo "KUBEBUILDER_ASSETS=$(make -s envtest-assets)" >> "$GITHUB_ENV"
```

The `make envtest-assets` target already exists
(`operator/Makefile:27`). Adapt it to the GitLab `script:` (no
`$GITHUB_ENV`, just an `export` before calling `go test`), conditioned
on `$MODULE = "operator"`:

```yaml
script:
  - cd "$MODULE"
  - |
    if [ "$MODULE" = "operator" ]; then
      export KUBEBUILDER_ASSETS="$(make -s envtest-assets)"
    fi
  - go test -race -covermode=atomic -coverprofile=coverage.out ./...
  - go tool cover -func=coverage.out | tail -n 1
```

### 2.2 — eslint job on GitLab (`.gitlab/ci/frontend.yml`)

The file only has `frontend-typecheck` (tsc -b) and `frontend-test`
(vitest). GitHub has a `npm run lint` (`.github/workflows/ci.yml:298`,
`frontend` job) that doesn't exist anywhere on GitLab. Add a
`frontend-lint` job (stage `lint`, `extends: [.frontend-base,
.rules-frontend]`, `script: [npm run lint]`) — same script as GitHub,
don't replicate `format:check` (not identified as a gap by the audit,
keep this finding's scope tight).

### 2.3 — fix the 2 lying comments (C4), now that they're true

- `.gitlab/ci/frontend.yml:2`: `# There is no eslint config in the
repo (yet): tsc -b is the lint gate.` → replace with a correct comment
  now that the `frontend-lint` job exists (e.g. "TypeScript build
  check + eslint + vitest.").
- `.github/workflows/ci.yml:281`: mirror comment ("Frontend gates
  (tsc -b is the lint gate — no eslint config yet).") — same fix on
  the GitHub side, it's just as false since `npm run lint` runs at
  line 298.

### 2.4 — coverage ratchet on GitLab (`go-test`, `api-server` module) — WATCH OUT, a trap not explicitly listed in the report

The report says "a ratchet call" as if it were enough to add the line
`hack/ci/coverage-ratchet.sh …`. **That's NOT enough as-is**:
`hack/ci/coverage-ratchet.sh:1-9` expects a profile produced with
`-coverpkg=./...` (the comment at the top of the script is explicit:
"Reads a merged -coverpkg profile, where every test binary emits
blocks for EVERY package"). But the current GitLab job
(`.gitlab/ci/go.yml:42`) runs `go test -race -covermode=atomic
-coverprofile=coverage.out ./...` **without** `-coverpkg` — the
measured figure would then be the low local coverage (2-31%, July's
old figure), not the cross-package figure (55.3%/77.7%) the
`internal/handler:40`/`internal/repository:50` thresholds are
calibrated against. Adding the ratchet without `-coverpkg` would fail
GitLab CI immediately on a false negative.

So replicate, for `MODULE == "api-server"` only, the same mechanism as
GitHub (`.github/workflows/ci.yml:230-263`):

- A Postgres service (`postgres:` image, healthcheck) for the
  dual-backend test (`WAAS_TEST_PG_URL`, see
  `api-server/internal/repository/backends_test.go`).
- `-coverpkg=./...` instead of no `-coverpkg`, for this module only
  (keep plain `./...` for the other 3 modules — unnecessary and
  potentially slower for them).
- The ratchet call afterward: `hack/ci/coverage-ratchet.sh
api-server/coverage.out internal/handler:40 internal/repository:50`.

GitLab CI doesn't have a `services:` mechanism identical to GitHub
Actions at a simple matrix job level — check the exact GitLab CI
`services:` syntax (image `postgres:16-alpine` or equivalent,
`POSTGRES_PASSWORD`/`POSTGRES_DB` variables) and adapt
`WAAS_TEST_PG_URL` to point at the service's hostname (`postgres` by
default in GitLab CI, not `localhost` as on the GitHub runner).

### 2.5 — frontend coverage.all (C14), on BOTH CIs

`.gitlab/ci/frontend.yml:31-33` and
`.github/workflows/ci.yml:301-306` both run `npx vitest
run --coverage.enabled --coverage.provider=v8 …` **without**
`--coverage.all`: coverage only counts files already imported by a
test (62.4%, an inflated figure) instead of the real "all of `src/`"
total (40.9% measured in the report). Add to both commands:

```
--coverage.all --coverage.include='src/**/*.{ts,tsx}'
```

plus the exclusions needed so tests themselves and generated/
declarative files aren't counted: `--coverage.exclude` for
`src/**/*.test.{ts,tsx}`, `src/types*.ts`, `**/*.d.ts` (check the
exact vitest exclusion syntax — several repeated `--coverage.exclude`
flags or a list, depending on the vitest version pinned in
`package.json`).

---

## Order 4 — Residual D2/D3: hand-duplicated types (C7, C8)

### 4.1 — C7: `RetainedVolume` and `RemoteWorkspaceAdmin` hand-copied

`frontend/src/types.ts` defines `interface RetainedVolume` (line 123)
and `interface RemoteWorkspaceAdmin` (line 265) **by hand**, even
though `frontend/src/types.gen.ts` already generates identical types
(lines 271 and 295) from `api-server/internal/model` via tygo. The
file already has exactly the pattern to follow for the rest of the
file (lines 15-35): import from `./types.gen`, then re-export via
`export type { … }`. Apply this same pattern:

1. Remove the two manual `interface`s (lines 123-131 and 265-278 at
   the time this prompt was written — check the exact lines, they may
   have moved).
2. Add `RetainedVolume` and `RemoteWorkspaceAdmin` to the
   `from './types.gen'` import (lines 15-22) and to the
   `export type { … }` block (lines 23-35).
3. `cd frontend && npm run typecheck` to confirm no consumer breaks on
   a slight shape gap between the manual copy and the generated
   version (there shouldn't be any, but check).

The report also asks to "sweep the rest of types.ts's 362 lines for
any type present in model.go" — do this pass: for every remaining
`interface`/`type` in `types.ts` not already re-exported from
`types.gen.ts` or `types.manual.ts`, check whether an equivalent
generated definition exists in `types.gen.ts` (same name or identical
structure) and migrate it the same way if so. Do NOT migrate a type
that has no generated equivalent (e.g. purely frontend types like
`Theme`, `WorkspaceConnectionPrefs`) — that's not duplication, leave
them in place.

### 4.2 — C8: sync guard for `WorkspacePhase`

`frontend/src/types.ts:82-83` hand-copies the literal union of the 7
phases (`'Pending' | 'Provisioning' | … | 'Terminating'`). The source
of truth is `operator/api/v1alpha1/workspace_types.go:9-25`
(kubebuilder marker
`+kubebuilder:validation:Enum=Pending;Provisioning;…` on the
`WorkspacePhase` type). tygo generates this field as `phase: string`
(too wide a type to carry the union, `types.gen.ts:198`) — so the CI
drift-check doesn't cover a phase desync.

The repo already has exactly the model to replicate:
`operator/pkg/params/protocols_sync_test.go` compares an enum read
from a generated CRD (`operator/config/crd/bases/*.yaml`) to a
reference Go list, with a helper that locates the target enum by its
stable values (`hasVNC`/`hasRDP`) rather than a fixed JSON path. The
same phase enum already exists, generated, in
`operator/config/crd/bases/waas.xorhub.io_workspaces.yaml` (locate it
by a stable value like `Pending`+`Running`, same logic as
`findProtocolEnums`).

Write a Go test (next to `protocols_sync_test.go`, or in a new file
`operator/pkg/params/phase_sync_test.go` if the `params` package has
no natural dependency on phases — pick the most coherent location)
that:

1. Reads the phase enum in `waas.xorhub.io_workspaces.yaml`.
2. Reads the `WorkspacePhase` literal union in
   `frontend/src/types.ts` via a simple regex on the declaration
   `export type WorkspacePhase =\n  'A' | 'B' | …;` — no need for a
   real TS parser, a regex extract is enough, the same as
   `findProtocolEnums` does on the YAML side.
3. Compares the two sorted lists, failing with an explicit message if
   they diverge (same style as `protocols_sync_test.go`'s error
   message).

Watch out for the relative path to `frontend/src/types.ts` from the
`operator/` module (`operator/go.mod` is a separate Go module from the
rest of the repo, but nothing stops you from reading a text file
outside the module via a plain relative path) — verify the path by
running the test locally rather than guessing it.

---

## Order 5 — Helm secrets: dropping `lookup`, a proper fix for ArgoCD (C13)

### Context, already settled — no need to re-verify

`helm/waas/templates/secrets.yaml:1-4` generates `postgres-password`,
`internal-token` and `jwt-private-key` via `lookup "v1" "Secret" …` so
they aren't regenerated on every `helm upgrade` (reuses the value
already in the cluster if it exists, otherwise generates a new random
one). `docs/ci.md:5-6` confirms ArgoCD really is this chart's consumer
("ArgoCD keeps deploying from the Git tag (`path:
helm/waas`)", `docs/ci.md:62`) — not just a hypothetical possibility.

**This is confirmed: `lookup` doesn't work under ArgoCD.** ArgoCD's
repo-server renders Helm charts via a server-side `helm template`,
with no access to the destination cluster — `lookup` there always
returns an empty map, whether the resource exists in the target
cluster or not. Concrete consequence if nothing changes: **on every
ArgoCD sync**, the 3 secrets would be regenerated with fresh random
values — Postgres password diverging from the one already written on
the PVC (broken DB connection), `internal-token` changed (wwt→api-server
calls rejected), `jwt-private-key` changed (all user sessions
instantly invalidated). It's a guaranteed prod incident on the first
sync, not a theoretical risk.

**Don't use `lookup` anywhere in this fix.** Don't settle for
verifying/documenting the problem either — implement the fix.

### What's expected

A mechanism that guarantees, **identically whether the chart is
installed via direct `helm install`/`helm upgrade` (dev) or via ArgoCD
(prod, rendered outside the cluster)**:

- On first deployment: the 3 values are randomly generated and
  persisted in the `{{ .Release.Name }}-secrets` Secret.
- On every subsequent deployment (upgrade / sync): the 3 **existing**
  values are never regenerated or overwritten if the Secret already
  exists with those keys.
- `database-url` (currently computed in the same template from
  `$pgPassword`, line 19) must keep reflecting the actually active
  Postgres password (whether it comes from `.Values.postgres.password`
  or from the once-generated value).
- `admin-password` (line 23-25, derived directly from
  `.Values.postgres.adminPassword`... — re-check the exact line, it's
  `.Values.apiServer.adminPassword`) remains a simple pass-through of
  Values, no change required there, it isn't generated.
- No new external dependency (no External Secrets Operator, no Sealed
  Secrets) — the report cites them as a possible alternative, but
  adding another operator just for this one problem is
  disproportionate; the fix must stay within the chart itself.

**Recommended approach** (you may choose another if it better respects
the constraints above, document your choice): a **Helm hook Job**
(`helm.sh/hook: pre-install,pre-upgrade`,
`helm.sh/hook-delete-policy: before-hook-creation`, a weight low
enough to run before the Deployments that consume the Secret) which,
being a Job, runs **in the real cluster** (whether the manifest was
produced by ArgoCD's `helm template` or applied directly via
`helm install`, once scheduled as a Job it's a Pod running with real
API server access) and does, idempotently: check whether the
`{{ .Release.Name }}-secrets` Secret already exists with the right
keys; if not, generate the missing values and create/patch the
Secret; if yes, don't touch anything (except
`database-url`/`admin-password` if those parts stay Values-driven and
therefore need to be kept up to date even when the Secret already
exists — your call whether a partial patch is needed or whether
everything should be Job-managed for consistency).

This Job needs a ServiceAccount + Role scoped to the release
namespace, with only `get`/`create`/`update` on `secrets`, restricted
if possible to the exact name `{{ .Release.Name }}-secrets` (no broad
`create`/`update` on all secrets in the namespace).

### Verification constraints

- `helm template` alone (no cluster, exactly what the ArgoCD
  repo-server does) must produce NO secret value at all — the
  rendered chart must contain no password or token in plaintext
  produced by a `lookup` call or template-time generation.
- Simulate a repeated `helm upgrade` (`helm install` then `helm upgrade`
  twice in a row on a test cluster, e.g. local k3d) and confirm that
  `postgres-password`/`internal-token`/`jwt-private-key` are identical
  between the first install and the second upgrade (the Job must not
  have regenerated them).
- `helm lint` and `helm template` must stay green (already gated in
  CI, `helm-render` — check that your new Job template passes this
  lint).

### Open points (your judgment call)

- Job image: the repo has no existing Helm Job/hook precedent in
  `helm/waas/templates/` to replicate. Choose a minimal image with
  Kubernetes API access (`bitnami/kubectl`, `rancher/kubectl`, or any
  image already used elsewhere in this repo if it fits) — document
  your choice, especially if you need to add it to the CI's
  Trivy/scan catalog. Use bitnami/kubectl.
- Whether `database-url`/`admin-password` should be managed by the
  same Job or remain separately templated keys in the same Secret —
  both work as long as the final Secret has all the keys expected by
  `api-server`/`wwt` (check the consumers, e.g.
  `helm/waas/templates/api-server.yaml`, `postgres.yaml`, for the
  exact list of keys referenced by `secretKeyRef`).

---

## Order 6 — Low-cost security/reproducibility (C11, C12, C6)

### 6.1 — C11: rate-limit on local login

`api-server/internal/service/auth_service.go:37` (`Login`) checks
argon2id and audits failures, but nothing throttles online
brute-force — no per-IP or per-account limit. The router
(`api-server/internal/server/router.go:34-58`) already uses
`go-chi/v5` with `chimiddleware.RealIP` mounted globally (line 36), so
`r.RemoteAddr` is already reliable for a per-IP rate-limit even behind
a proxy.

Add a rate-limit scoped **only to the `POST /api/v1/auth/login`
route** (`router.go:58`) — not a global middleware, the other routes
don't have this problem. The report suggests `httprate`:
`github.com/go-chi/httprate` is the natural fit given the router is
already `go-chi`, and avoids reinventing a hand-rolled limiter. A
reasonable per-IP limit (e.g. 10 attempts/minute) is enough for this
finding; combining IP+username (as the report suggests) is possible
but requires reading the JSON body before it reaches the handler (so
buffering it to avoid consuming it twice) — if you do this, document
how you avoid the double body read, otherwise an IP-only limit is a
sufficient fix for this finding.

### 6.2 — C12: timing oracle on account enumeration

Same file, `auth_service.go:38-44`: a non-existent username returns
immediately with `apierror.Unauthorized` (line 41) without ever going
through `VerifyPassword`/argon2id, while an existing username takes
~50ms (the argon2id cost). The gap is remotely measurable.

Fix: on the `errors.Is(err, repository.ErrUserNotFound)` branch (line
40), still call `VerifyPassword(password, dummyHash)` with a constant
PHC argon2id hash (precomputed once, doesn't need to be secret — just
in the right format so the computation cost is comparable) before
returning the error, so both paths take a similar amount of time.
Ignore the result of `VerifyPassword` (it will always be `false` since
it's a dummy hash), only the elapsed time matters.

**Do it along the way** (the report explicitly notes it): this
`ErrUserNotFound` path emits **no audit event**, unlike the other 3
login failure paths (SSO-only, wrong password, inactive account — all
three call `s.audit.Record(…, "user.login_failed", …)`, see lines
47-48 and 55-56 at the time of writing). Add the same audit call on
this path for consistency — use an empty/zero target identifier since
there's no `user.ID` for a non-existent account (look at how
`Actor{Username: username, ClientIP: clientIP}` is already used on the
other 3 paths to stay consistent in the call's shape).

### 6.3 — C6: dev vs. CI toolchain pins

`.mise.toml` (root):

```
[tools]
go = "1.26"
node = "lts"
k3d = "latest"
```

while CI pins `golang:1.26.4` (`.gitlab/ci/go.yml:33`,
`.github/workflows/ci.yml`) and `node:22.17.0-alpine`
(`.gitlab/ci/frontend.yml:5`). Pin both versions exactly to the same
values as CI (`go = "1.26.4"`, `node = "22.17.0"`).

`golangci-lint` isn't in `.mise.toml` at all, while CI uses it via a
dedicated image (`golangci/golangci-lint:v2.12.2`,
`.gitlab/ci/go.yml:21`) — add `golangci-lint = "2.12.2"` (check that
mise has a plugin/registry for this tool, otherwise document the
alternative, e.g. a `mise run` script or a Makefile note).

`k3d = "latest"`: pin to an exact version (check the version currently
installed/used in dev via `k3d version`, or choose the latest known
stable version at execution time — document your choice).

`hack/dev/k3d-config.yaml:12-20` (`kind: Simple`, `servers: 1`)
doesn't pin the k3s image (no `image:` field at the manifest root) —
add `image: rancher/k3s:vX.Y.Z-k3s1` with a version consistent with
the pinned `k3d` above (a given k3d version installs a default k3s
version if unspecified; choose a k3s version explicitly compatible
and document the choice, as the file already does for other decisions
— see the existing comment on lines 1-9 about port mapping).

---

## Order 7 — Coverage of the highest-blast-radius components (C19, C20)

### 7.1 — C19: `shared/auth` stagnant (69.4%), `wwt/internal/jwks` at 0%

`shared/auth/keys_test.go` (76 lines) only covers the happy path +
wrong audience + expiration + PEM roundtrip
(`TestSignAndVerifyRoundTrip`, `TestVerifyRejectsWrongAudience`,
`TestVerifyRejectsExpired`, `TestPEMRoundTrip`). Error paths not
covered in `shared/auth/keys.go`: wrong signature method (alg
confusion, e.g. a token signed with `none`/HS256 rejected by
`jwt.WithValidMethods`), wrong key (invalid signature), missing/unknown
`kid`, `ParseSignerPEM` with an invalid PEM or a non-RSA key (line
41-58), wrong issuer. Add table-driven tests for these paths, in the
style already present in `keys_test.go`.

`wwt/internal/jwks/jwks.go` has **no tests** (0% coverage, 73 lines).
Write `wwt/internal/jwks/jwks_test.go` with an `httptest.Server`
serving a valid JWKS JSON document, covering:

- Successful initial fetch + `Key()` returns the right key for a known
  `kid`.
- Cache: a second call to `Key()` within the `cacheTTL` window (5 min,
  line 28) doesn't make another HTTP request (count the requests
  received by the test server).
- Rotation: an unknown `kid` triggers a `refreshLocked` (line 46).
- Network error/non-200 HTTP on refresh: if a key is already cached,
  `Key()` serves the stale value instead of propagating the error
  (line 47-50, intentional behavior — test it explicitly, it's the
  kind of choice that breaks silently during a refactor).
- `kid` totally absent from the JWKS document and empty cache: error
  propagated (line 54-56).

### 7.2 — C20: `operator/pkg/policy` regressed (84% → 78.7%) + ratchet

This is the repo's only coverage drop: the quota/remote-workspaces
code added since July arrived less tested than the existing code in
`operator/pkg/policy/` (`overrides.go`, `policy.go`). Run
`go test -coverprofile=coverage.out ./pkg/policy/...` then
`go tool cover -html=coverage.out` (or `-func=`) to precisely find the
recently added uncovered functions/branches (compare against
`git log -p` on this package since July 8 if you need context on
what's new), and add the missing tests to climb back above 84%.

Once back up, wire this package into the ratchet mechanism so the
regression can't silently reoccur — same scheme as the api-server
ratchet added in Order 2.4, but for the `operator` module
(`MODULE == "operator"` in `go-test`, both CIs):
`hack/ci/coverage-ratchet.sh operator/coverage.out pkg/policy:84`.
Is the `operator/coverage.out` profile produced by `go test
-covermode=atomic -coverprofile=coverage.out ./...` (already in
place, `.gitlab/ci/go.yml:42`) compatible with the format expected by
the script (`-coverpkg=./...` required, cf. Order 2.4)? Check: if the
`pkg/policy` tests are internal package tests (not cross-package
integration tests like api-server's), a plain `-coverprofile`
**without** `-coverpkg` may be enough, provided `go test`'s output
format stays compatible with the script's awk parsing (one line per
block, `file:line.col,line.col nstmts hits`) — test the script
locally against the real profile before considering the job done,
don't assume it works without trying it.

---

---

## General constraints

- Never weaken an existing gate (Trivy thresholds, coverage ratchets,
  security gates) to make CI pass more easily.
- Each order above is independent of the others except where noted
  (2.3 depends on 2.2; 2.4 and 7.2 touch the same `go-test` job but
  different modules — check they don't step on each other if you
  handle them in the same commit).
- Line numbers cited date from when this prompt was written
  (2026-07-12) — check them before editing, they may have shifted by
  a few lines since.
- Green build/tests before considering an order done:
  `go build ./... && go test ./...` per touched Go module,
  `cd frontend && npm run typecheck && npm test` if the frontend is
  touched, `helm lint`/`helm template` if `helm/waas/` is touched.

## Open points (your judgment call, beyond those already noted per order)

- Commit granularity (one per order vs. one per finding) — both are
  acceptable, document your choice in the commit messages. Judgment
  call => one per order.
- If a finding turns out to already be resolved at execution time (as
  C24 seems to already be), note it and move to the next one without
  trying to produce an artificial change.
