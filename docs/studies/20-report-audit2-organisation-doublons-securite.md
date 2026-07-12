# Audit 2 — organization, duplicates, security, tests (2026-07-11)

Second full audit of the repo, revalidating
`docs/studies/audit-2026-07.md` (2026-07-08): every finding from the
previous audit has been re-tested against the real state of the code,
coverage numbers are **measured** (commands in §3), and every finding
in the §4 table carries its complexity and a "worth it" verdict.
Scope: all components, including `operator/test/`, `hack/dev/`, CI
itself, and `docs/`.

**Granularity choice** (a point left open by the prompt): a finding =
one actionable problem. "Catch-all services" are grouped into a single
row (same remedy, same judgment call); divergences between the two CIs
are split into separate rows (independent remedies, different
complexities).

## 0. Track A — done (commit `83ff30752b16`)

The 4 red jobs on the main pipeline (`trivy-deps`, `scan-frontend`,
`scan-api-server`, `scan-operator`, pipeline 2668804747) are remediated
without touching the thresholds:

| Dependency | Before → after | CVEs closed | Modules |
|---|---|---|---|
| `golang.org/x/net` | v0.47.0 → v0.57.0 | CVE-2026-25681, -27136, -33814, -39821 (HIGH) | api-server, operator, test/smoke (+ wwt via `go work sync`) |
| `golang.org/x/crypto` | v0.45.0 → v0.54.0 | CVE-2026-39828…39835, -42508, -46595, -46597 (HIGH, x/crypto/ssh) | api-server |
| `github.com/jackc/pgx/v5` | v5.7.6 → v5.10.0 | CVE-2026-33815, -33816 (**CRITICAL**, appeared after the prompt was written) | api-server |
| `github.com/moby/spdystream` | v0.5.0 → v0.5.1 | CVE-2026-35469 (HIGH) | api-server |
| Frontend base image | `nginx-unprivileged:1.27-alpine` → `1.30-alpine` + `apk upgrade` | 35 Alpine OS CVEs (openssl CRITICAL ×2, libpng, libexpat, c-ares…) | frontend/Dockerfile |

Verified: `go build`/`go test` green on all 5 modules; `trivy fs` at
the root and `trivy image` on the rebuilt frontend both **exit 0**
with the exact flags used in CI (`aquasec/trivy:0.63.0`,
HIGH/CRITICAL, `--ignore-unfixed`); nginx still serves as user 101
(HTTP 200). `package-lock.json`: 0 CVE (reconfirmed). **No remaining
CVE without a fix** → no accepted risk to log. No Renovate MR existed
for these bumps — see C3, that's a finding in itself.

## 1. State of the 2026-07-08 audit's findings

### Resolved since July 8 (verified in the code)

| July finding | Proof of resolution |
|---|---|
| D1 — protocols in ×4 copies | The `AdminUpsertImage` switch is now a `params.Protocols()` lookup (`api-server/internal/service/governance_service.go:325`) and `operator/pkg/params/protocols_sync_test.go` keeps the sync with the CRD enums |
| D2 — hand-written TS types | tygo generates `frontend/src/types.gen.ts` from `api-server/internal/model` (`Makefile:57`), drift-checked in GitHub CI (`.github/workflows/ci.yml:278`). **Residue: see C7** |
| No envtest | `operator/test/envtest/` covers exactly the 3 requested paths: `crd_validation_test.go` (CEL), `webhook_admission_test.go`, `finalizer_lifecycle_test.go`. **But it only runs on GitHub CI: see C2** |
| Implicit create-only podTemplate doctrine | `docs/adr/0001-template-boundary-convergence.md`, referenced in `workload.go:245` |
| No CRD evolution strategy | `docs/adr/0002-crd-evolution.md` |
| `http.Server` without timeouts | `api-server/cmd/api-server/main.go:167-181`: ReadTimeout 30s, IdleTimeout 120s, WriteTimeout 0 commented (SSE) — exactly what the audit asked for |
| Dual-backend repository at 15.5% | 69.3% local, **77.7%** cross-package (measured); `docs/testing.md` documents the PostgreSQL tier |
| Handlers at 2.2% | 55.3% cross-package: exercised through the real router (`internal/server/*_test.go`) + ratchet `hack/ci/coverage-ratchet.sh` (handler ≥ 40, repository ≥ 50) — **ratchet GitHub-only, see C2** |
| Frontend 7.9%, no eslint | 40.9% all-files measured (143 tests, 26 files); `frontend/eslint.config.js` + `lint` script + GitHub job. **eslint absent from GitLab CI: see C2; misleading CI figure: see C14** |
| Flaky sleep `event_hub_test.go:77` | Rewritten: the sync now happens "WITHOUT a time.Sleep race" (`event_hub_test.go:38`). Remaining `time.Sleep`s (wwt, smoke, envtest) are bounded poll loops, not races |
| Documented but uncorrected N+1 `usageOf` | Fixed: a single LIST templates + `policy.OwnerLoads` (`governance_service.go:224-228`), the comment even traces the old divergence |
| Ungoverned kasmvnc clipboard | Feature 11 (DLP derived from policy, `/api/downloads` blocked at the `wwt/internal/kasm/kasm.go:145` proxy) + fixes 13/14 verified e2e |
| PortalPage 1617 lines | Split up: 100 lines + `sections/` + dialogs. The weight has moved to TemplatesPage (881 l., see C17) |

### Still open (carried over into the §4 table)

D4 (secret-copy ×2), D5 (`envOr` ×2, CSS const — **went from 4 to 6
copies**), `registry.xorhub.io` refs, dev vs. CI toolchain pin,
`guacamole-common-js ^1.5` vs. guacd 1.6.0, a11y, OIDC never tested
against a real IdP (the `internal/server` stub IdP tests the flow, not
a real Keycloak/Entra).

### Reversed or outdated since July

- **"GitHub Actions is the canonical CI, GitLab is being phased out"**
  (audit §intro): the observable reality is the opposite — the local
  git remote is GitLab only, the pipeline that actually runs (and
  releases: cosign, OCI chart, smoke) is GitLab, and `docs/ci.md` says
  "a single entry point: `.gitlab-ci.yml`" while `docs/ci-github.md:3`
  says "GitHub is the canonical repository". See C1.
- **`cliff.toml` as a transition leftover**: false as long as GitLab is
  alive — it's consumed by `release-notes` (`.gitlab/ci/release.yml`,
  git-cliff). To be removed only if C1 decides against GitLab.
- **`waas-images/` in the monorepo**: split out on 2026-07-10 and
  **pushed** (`gitlab.com/drummyjohn/waas-images`, active as of 11/07);
  `smoke-connections` clones it (`.gitlab-ci.yml:80`) — dependency OK.

## 2. Map of what's new since July 8

New surfaces audited here for the first time:
`api-server/internal/service/remote_workspace_service.go` (600 l.,
out-of-cluster machines via guacd, policy-gated fail-closed,
credentials in a Secret never in DB — sound design, tested),
`operator/internal/kubevirt/` (40 l., KubeVirt detection for Windows
VMs, trivial), OIDC-only login (fail-closed at Load, 404 handler guard
`auth_handler.go:38-41`), self-signed HTTPS dev
(`hack/dev/k3d-config.yaml` 8443, justified by the clipboard's secure
context requirement), fleet grouped by owner, unified YamlEditor,
fr/en i18n.

## 3. Measured coverage (2026-07-11)

Commands: `go test -cover ./...` per module;
`go test -coverpkg=./... ./...` + `hack/ci/coverage-ratchet.sh` for
api-server cross-package; vitest
`--coverage.all --coverage.include='src/**/*.{ts,tsx}'` (excluding
tests, `types*.ts`, `.d.ts`) for the real frontend figure.

| Zone | 08/07 | 11/07 | Reading |
|---|---|---|---|
| operator `internal/controller` | 70.6% | 72.7% | stable |
| operator `internal/webhook` | 85.2% | 86.5% | stable |
| operator `pkg/policy` | 84% | **78.7%** | the repo's only drop — the quota/remote-workspaces code added is less covered than the existing code |
| operator `pkg/params` / `naming` / `schedule` / `kasmcfg` | 94 / 97.3 / 90.1 / — | 97.0 / 97.3 / 90.1 / 90.5 | good |
| operator `test/envtest` | didn't exist | dedicated suite | **skips without `KUBEBUILDER_ASSETS`** (C2) |
| api-server `handler` (cross-pkg) | 2.2% | **55.3%** | via router tests + ratchet ≥ 40 |
| api-server `repository` (cross-pkg) | 15.5% | **77.7%** | ratchet ≥ 50 |
| api-server `service` / `middleware` (cross-pkg) | 31.9 / — | 65.3 / 76.6% | good |
| wwt `guac` / `kasm` / `proxy` | 87 / 86.8 / 62.2% | 87.4 / 88.1 / 65.0% | stable; `cmd` 7.3% but `main_test.go:71` now exercises the TLS path (July finding resolved) |
| shared `auth` | 69.4% | 69.4% | stagnant (C19) |
| **frontend (all of src/)** | **7.9%** | **40.9%** (926/2265 stmts) | CI shows 62.4% — naive figure, see C14 |
| e2e | smoke, 4 protocols | + `vnc-audio` subtest, zero-orphan | `test/smoke/smoke_test.go:84` |

Notable frontend zeros (stmts): `DesktopPane.tsx` 171,
`GovernancePage.tsx` 124, `SplitViewPage.tsx` 83, `ProfilePage.tsx` 73,
`UsersPage.tsx` 48; `TemplatesPage.tsx` 31.4% for 881 lines.

## 4. Findings

Categories: duplicate / organization / security / test / CI-dev-env.
Complexity: S < 1 d, M 1-3 d, L 3-5 d, XL > 5 d (rough).

| # | Finding | Component | Source (file:line) | Category | Severity/risk | Complexity | Worth it? |
|---|---|---|---|---|---|---|---|
| C1 | Two full CIs maintained in parallel with contradictory doctrines: `docs/ci.md` ("a single entry point: .gitlab-ci.yml") vs `docs/ci-github.md:3` ("GitHub is the canonical repository"); the local remote is GitLab only, the release (cosign, OCI chart, blocking smoke) is GitLab | CI | `docs/ci.md:1`, `docs/ci-github.md:3`, `.gitlab-ci.yml`, `.github/workflows/ci.yml` | CI-dev-env | high — every added gate must be added twice, and forgetting is silent (C2 is proof of this ×3) | M (decide, port the missing gates, rewrite the 2 docs) | **Yes**: it's the root cause of C2; until this is settled, the CI "that counts" doesn't run the repo's best gates |
| C2 | Three gates run ONLY on GitHub while the live CI is GitLab: envtest (`go.yml:40-44` doesn't set `KUBEBUILDER_ASSETS` → the `operator/test/envtest` suite silently skips, cf. `suite_test.go:12-13`), eslint (no job in `.gitlab/ci/frontend.yml`), coverage ratchet (`ci.yml:263` GitHub-only) | CI | `.gitlab/ci/go.yml:40`, `.gitlab/ci/frontend.yml`, `.github/workflows/ci.yml:242,263,298` | CI-dev-env | high — envtest was written precisely because the fake client already masked a real bug once, and it doesn't run on the CI actually in use | S (3 independent YAML additions: `make envtest-assets` before go-test, an eslint job, a ratchet call) | **Yes**: a quick win that restores already-paid-for gates to the real CI; doable without waiting for the C1 decision |
| C3 | Renovate is inert: `renovate.json` (well-crafted, 8 custom managers) but **zero MR ever opened** on the GitLab project — the x/net CVEs with a fix available since 0.53.0 piled up until they turned main red (Track A) | CI | `renovate.json:1`, `glab mr list --all` empty | CI-dev-env / security | high — without a bot, the blocking trivy gate becomes the ONLY CVE signal, and it blocks main instead of proposing an MR upstream | S (activate the Renovate app on the GitLab project, or a self-hosted `renovate/renovate` CI job) | **Yes**: the Track A outage is exactly the scenario this file was supposed to prevent |
| C4 | CI comment lying ×2: "There is no eslint config in the repo (yet)" in `.gitlab/ci/frontend.yml:2` **and** `.github/workflows/ci.yml:281` — the latter 17 lines above the `npm run lint` (l.298) that contradicts it | CI | `.gitlab/ci/frontend.yml:2`, `.github/workflows/ci.yml:281` | CI-dev-env | low — but a false comment costs diagnostic time on every read | S (2 lines) | **Yes**: a minutes-long fix, to be folded into C2 |
| C5 | Trivy pin `0.63.0` (the scanner's vulnerability DB is aging) while 0.72.0 is published | CI | `.gitlab/ci/security.yml:20`, `.gitlab/ci/docker.yml:73` | CI-dev-env | low-medium — a lagging scanner misses recent CVEs | S | **Yes via C3**: exactly the kind of bump Renovate would do on its own; by hand only if C3 drags on |
| C6 | Floating dev toolchain vs. pinned CI: `.mise.toml` says `go = "1.26"`, `node = "lts"`, `k3d = "latest"` while CI pins `golang:1.26.4` and `node:22.17.0`; golangci-lint absent from mise while the July audit already documented a local/CI divergence incident; `hack/dev/k3d-config.yaml` doesn't pin the k3s image | env-dev | `.mise.toml:1-4`, `.gitlab/ci/go.yml:33`, `.gitlab/ci/frontend.yml:5`, `hack/dev/k3d-config.yaml:12` | CI-dev-env | medium — "works on my machine, not in CI" already experienced once | S (pin mise to the CI versions + add golangci-lint; k3s image in the k3d config) | **Yes**: half an hour to eliminate an already-encountered incident class |
| C7 | D2 residue: `types.ts` hand-redefines `RetainedVolume` (l.123) and `RemoteWorkspaceAdmin` (l.265) even though tygo already generates them (`types.gen.ts:271,295`) — the facade exports the manual copy, the generated version is ignored, and the CI drift-check sees nothing since the generated file itself is up to date | frontend | `frontend/src/types.ts:123,265`, `frontend/src/types.gen.ts:271,295` | duplicate | medium — this is the exact resurgence of the `params: null` class of bug generation was meant to kill; the next Go field added will only be visible in the ignored copy | S (remove the 2 manual interfaces, re-export from types.gen; sweep the rest of types.ts's 362 lines for any type present in model.go) | **Yes**: an hour, and the "generated types" promise becomes true again |
| C8 | D3 residue: the TS `WorkspacePhase` union (7 literals, `types.ts:82`) re-copies the Go enum with no sync guard — tygo generates `phase: string` (`types.gen.ts:198`), so the drift-check doesn't cover phases | frontend / operator | `frontend/src/types.ts:82`, `operator/api/v1alpha1/workspace_types.go:13-25` | duplicate | low-medium — a renamed phase on the Go side silently breaks the cards; same bug class as the kasmvnc protocols incident | S (a sync test like `protocols_sync_test.go`, or a tygo frontmatter to emit the union) | **Yes**: the repo already has the exact model to replicate (protocols_sync), it's a copy-and-adapt |
| C9 | D4 unchanged: two implementations of "ensure a Secret copied into the target ns" (`kasm_credentials.go` 178 l., `pull_secret.go` 144 l.), different semantics, identical skeleton | operator | `operator/internal/controller/kasm_credentials.go:1`, `pull_secret.go:1` | duplicate | low — no incident, both are tested | S-M | **No**: July's verdict still holds — factor at the 3rd occurrence, not before; a generic helper would cost more in readability than it returns |
| C10 | D5 worsened: the CSS const `'mt-1 w-full rounded-md…'` went from 4 to **6** copies (ProfilePage:61, UsersPage:138 and 246, GovernancePage:449, TemplatesPage:61, RemoteWorkspaceDialog:135); `envOr` still ×2 (`wwt/cmd/main.go:82`, `config.go:205`) | frontend / wwt | `frontend/src/pages/ProfilePage.tsx:61` (+5), `wwt/cmd/main.go:82` | duplicate | low — cosmetic, but the CSS copy grows with every new page | S (a shared `<Input>` or a const in `lib/`) | **Debatable**: no dedicated effort — impose it as a rule at the next UI refactor (C17 is the occasion); `envOr`: No, 6 lines, a shared package would cost more |
| C11 | Local login has no rate-limit or lockout: `AuthService.Login` checks argon2id and audits failures, but nothing throttles online brute-force (no per-IP or per-account limit, no backoff) | api-server | `api-server/internal/service/auth_service.go:38`, `handler/auth_handler.go:37` | security | medium — mitigated by the argon2id cost (~50ms/attempt), the audit trail, and the OIDC-only option that closes this path; high if the portal is exposed to the internet in local-login mode | S (an `httprate` middleware by IP+username on `/api/v1/auth/login`) | **Yes**: an hour of middleware to close the most banal attack vector in the product; to be done before any internet exposure in local-login mode |
| C12 | Timing oracle on account enumeration: non-existent user → immediate return (`auth_service.go:40-42`) without going through argon2id, existing user → ~50ms of hashing; the gap is remotely measurable | api-server | `api-server/internal/service/auth_service.go:40` | security | low — you already need to want to enumerate usernames, and the audit trail doesn't track this path (no ID) | S (a constant dummy hash on the not-found path) | **Debatable**: 10 lines if done alongside C11, otherwise the benefit alone doesn't justify a separate iteration — also note the not-found path emits **no** audit event, unlike the other 3 failure paths |
| C13 | Helm secrets are generated via `lookup` (`secrets.yaml:1-4`: postgres password, internal-token, JWT key regenerated if absent from the cluster) — yet `docs/ci.md` names ArgoCD as the chart's consumer, and ArgoCD renders with `helm template` **without** cluster access: `lookup` renders empty → every sync would regenerate the 3 secrets (invalidated sessions, DB password diverging from the PVC) | helm | `helm/waas/templates/secrets.yaml:1-4`, `docs/ci.md:5` | security | high **if** ArgoCD renders the chart natively; nil if the real deployment goes through `helm install` or a plugin. Not settled in the repo — no versioned ArgoCD manifest | S (verify the real rendering mode) then M (pre-provision via ESO/SealedSecrets or `argocd.argoproj.io/sync-options: Skip` on the Secret) | **Yes for the verification (S)**: if the ArgoCD path is real, it's a guaranteed prod incident on the first sync; the full fix awaits the answer |
| C14 | The frontend coverage figure shown in CI is the "naive" figure the July audit already flagged: `frontend-test` (`frontend.yml:33`) runs vitest without `--coverage.all` → 62.4% (1483 stmts of only the imported files) versus the **real 40.9%** (2265 stmts) | frontend / CI | `.gitlab/ci/frontend.yml:33`, `.github/workflows/ci.yml:283+` | test | medium — a metric overstating by 21 points misdirects testing effort | S (add `--coverage.all --coverage.include='src/**'` to both CIs) | **Yes**: two flags; without this the future frontend ratchet would be built on a false figure |
| C15 | `DesktopPane.tsx`: 171 statements at 0% coverage for the frontend's most delicate code (Guacamole canvas, bidirectional clipboard, resize, fullscreen) — 3 clipboard fixes shipped on it in one week, all manually verified | frontend | `frontend/src/components/DesktopPane.tsx:1` | test | medium-high — every clipboard/resize fix has to be re-verified manually for lack of a safety net | M (extract the effect logic into testable hooks: `useClipboardBridge`, `useSessionResize` — `lib/sessionResize.ts` and `lib/clipboard.ts` show the pattern already exists and tests well) | **Yes**: this is THE zone of the frontend where regressions have already happened; extracting into hooks makes it testable without jsdom-canvas |
| C16 | Remaining 0% frontend zones: GovernancePage (124 stmts), SplitViewPage (83), ProfilePage (73), UsersPage (48) | frontend | `frontend/src/pages/admin/GovernancePage.tsx`, `pages/SplitViewPage.tsx`, `pages/ProfilePage.tsx`, `pages/admin/UsersPage.tsx` | test | low-medium — standard admin CRUD, less treacherous than C15 | M | **Debatable**: GovernancePage yes (it edits the policy, a silent regression touches governance); the other 3 after C15, along the way of future edits |
| C17 | `TemplatesPage.tsx` = 881 lines at 31% coverage: the largest frontend file, multi-tab forms + protocol logic in a single component — same trajectory as July's PortalPage (1617 l.) which ended up split | frontend | `frontend/src/pages/admin/TemplatesPage.tsx:1` | organization / test | medium — every protocol feature blindly touches this file | M (split by tab like PortalPage → sections/, test the extracted forms) | **Yes**: the PortalPage precedent proves the split works and makes it testable; to do before the next templates feature |
| C18 | `workspace_controller.go` 1106 l. (+185 since July) and `workspace_service.go` 1189 l. + `remote_workspace_service.go` 600 l.: the catch-all files keep growing despite well-split neighbors (`workload.go`, `placement.go`) | operator / api-server | `operator/internal/controller/workspace_controller.go:1`, `api-server/internal/service/workspace_service.go:1` | organization | low — everything is tested and DI is clean; it's a navigation cost, not a correctness one | M | **Debatable**: no dedicated effort — impose "no new responsibility in these files" and extract along the way of the next feature (lifecycle/status for the controller) |
| C19 | `shared/auth` stagnates at 69.4%: the JWT/JWKS brick shared by 3 components, error paths (unknown key, missing kid, expired vs. not-yet-valid token) partially covered; `wwt/internal/jwks` at 0% (JWKS HTTP fetch client) | shared / wwt | `shared/auth/`, `wwt/internal/jwks/` | test | medium — this is the component ALL inter-service authentication depends on | S (table-driven tests on the claims/keys error paths; an httptest for jwks) | **Yes**: small effort, maximum blast-radius component |
| C20 | `operator/pkg/policy` is the repo's only coverage regression (84% → 78.7%): the quota/remote-workspaces code added since July arrived less tested than the existing code | operator | `operator/pkg/policy/` | test | low-medium — pkg/policy is the "best architecture decision in the repo" (portal and webhook decide the same way); its coverage should be exemplary | S | **Yes**: bring it back to the prior level + include it in the ratchet (it protects the central invariant) |
| C21 | 4 dead refs to `registry.xorhub.io/waas/waas-images/...` in the gitops catalog — the real registry is `registry.gitlab.com/drummyjohn/waas-images` since the 10/07 split; flagged "pending the final path" since July, but the final path now exists | gitops | `gitops/governance/images.yaml:11,29,44,60` | organization | medium — a `kubectl apply` of this catalog references unreachable images; the Renovate custom manager (`renovate.json`, kasmweb images) ignores these lines | S (4 lines + digest re-resolution) | **Yes**: July's blocker is lifted, there's no reason left to wait |
| C22 | `docs/openapi-governance.yaml`: manual, partial spec, consumed by **nothing** (no reference outside docs/) and redundant since tygo — the July audit already called it a "duplicate that drifts" | docs | `docs/openapi-governance.yaml:1` | organization | low — but a lying spec is worse than no spec | S (delete, or decide to generate it — deletion is the right default while no consumer exists) | **Yes**: deletion takes minutes; keeping a dead doc contradicts the repo's docs discipline |
| C23 | Root-level leftover `fable-waas-build-prompt.md` (historical bootstrap brief), already flagged in July | root | `fable-waas-build-prompt.md:1` | organization | low | S | **Yes**: `git rm`, one minute — or move it into `docs/studies/` if it has archival value |
| C24 | Git hygiene of the docs: `docs/clipboard.md` + 2 modified studies not committed, **8 untracked studies** (11 through 17, 19) — the "debt lives in the docs" discipline only holds if the docs are in git | docs | `git status` (11-prompt… to 19-prompt…) | organization | low-medium — a future session doesn't see these documents from a fresh clone | S (a docs commit) | **Yes**: immediate, and it's the condition for this very report to be usable by future sessions |
| C25 | a11y still unaddressed: icon buttons without `aria-label` (UserMenu.tsx: 0 aria-label; SessionCard.tsx: 1) — unchanged cosmetic July finding, now partially tool-assistable via eslint-plugin-jsx-a11y (eslint now exists) | frontend | `frontend/src/components/UserMenu.tsx:1`, `SessionCard.tsx:1` | organization | low — internal portal, no identified legal requirement | S (enable jsx-a11y as warn + fix along the way) | **Debatable**: enabling the eslint rule costs one line (yes); a dedicated corrective pass, no, unless a user need pulls for it |
| C26 | `guacamole-common-js ^1.5.0` versus guacd 1.6.0: compatible in practice, but the version gap remains undocumented in the repo (the "official Apache source only, no third-party npm mirror" constraint is written nowhere except in a study) | frontend | `frontend/package.json:18`, `helm/waas/values.yaml:160` | organization | low — no bug attributed to the gap | S (a paragraph in `docs/templates-and-protocols.md` or a package.json comment stating the version policy) | **No** for a bump (1.6 not published on npm by Apache); **Yes** to document the constraint — 15 minutes that prevent a future "helpful" bump from installing a third-party mirror |
| C27 | OIDC never tested against a real IdP: the stub (`internal/server`, `stubIdP`) covers the contract, not the quirks of a real Keycloak/Entra (mapped claims, refresh, clock skew); OIDC-only mode (Feature 14) increases reliance on this path | api-server | `api-server/internal/server/` (stubIdP), `docs/ci.md` | test | medium — in OIDC-only mode, a poorly integrated IdP means nobody can log in anymore | M (an optional smoke job with Keycloak in a container, or a documented manual recipe checklist) | **Debatable**: Yes if OIDC-only deployments are imminent (which Feature 14 suggests), otherwise the documented manual checklist is enough short-term |

### Findings examined and dismissed (to trace what was NOT found)

- **wwt**: `CheckOrigin: true` is sound here — the real gate is the
  signed connection token, in the query string for `/ws`
  (`proxy.go:67-80`) and in a `SameSite=Strict`+`HttpOnly`+
  path-scoped-per-session cookie for `/kasm` (`kasm.go:153-160`),
  cookie stripped before reaching the upstream.
- **Tokens compared in constant time** everywhere it matters
  (`middleware.go:79`, `password.go:57`).
- **`sslmode=disable`** on the in-cluster postgres URL
  (`secrets.yaml:19`): acceptable for a postgres in the same
  namespace; only becomes a topic with `postgres.externalURL`.
- **Remaining sleeps in tests**: all bounded poll loops (envtest,
  smoke, wwt metrics) — not July's flaky class.
- **`hack/`** (including `hack/dev/`): nothing to report — scripts
  `set -eu` shellchecked in CI, `seed-ssh-secret.sh` precisely
  documents its double-namespace invariant, seeds are still gated by
  the real webhook, `k3d-config.yaml` even explains how to migrate an
  existing cluster. Only the k3s non-pin (C6) concerns it.
- **TODO/FIXME**: still **zero** in the code — the discipline holds.

## 5. Action plan (reuses the §4 columns, quick wins first)

| Order | Findings | Cumulative effort | Why this order |
|---|---|---|---|
| 1 | C24 (docs commit) + C4 (2 comments) + C23 (leftover) + C22 (dead openapi) | S | A morning of cleanup; a condition for the rest to be usable from a fresh clone |
| 2 | C2 (envtest+eslint+ratchet on GitLab) + C14 (coverage.all) | S | Restores already-written gates to the real CI — the best ratio in the plan |
| 3 | C3 (activate Renovate) then C5 (trivy bump, absorbed by C3) | S | Closes the root cause of Track A |
| 4 | C7 (duplicated manual types) + C8 (phase guard) | S | Completes D2/D3 with patterns already present in the repo |
| 5 | C13 (verify ArgoCD's secret rendering) | S (verify) → M (fix if confirmed) | Potential prod incident; the verification is trivial, to do before any ArgoCD deployment |
| 6 | C11 (login rate-limit) + C12 (timing, along the way) + C6 (toolchain pins) | S | Low-cost security/reproducibility |
| 7 | C19 (shared/auth) + C20 (pkg/policy in the ratchet) | S | Coverage of the two highest-blast-radius bricks |
| 8 | C21 (gitops refs) | S | Unblocked since the waas-images split |
| 9 | C1 (decide GitLab vs. GitHub) | M | The structuring decision; steps 2-3 don't depend on it, but all double-maintenance stops here |
| 10 | C15 (DesktopPane into tested hooks) then C17 (TemplatesPage) | M each | The two real frontend test/refactor efforts, in order of experienced risk |
| — | C16, C18, C25, C26 (doc), C27: along the way | — | "Debatable" verdicts: to attach to the features that touch them, not a dedicated effort |

Not carried forward: C9, C10-envOr (verdict **No** — the status quo is
the documented right choice).
