# Prompt Fable 5 — Full repo audit (organization, duplicates, security) + Trivy/CI triage

Paste this document as-is as a prompt. It assumes that you (Fable 5)
have no prior conversation context.

## Goal

Two distinct deliverables, not to be mixed:

1. **Track A (action, to do first)** — check the real state of the
   Trivy jobs in CI and fix what is trivially fixable (dependency
   bump). This is the only track where you modify code.
2. **Track B (report, no implementation)** — a complete qualitative
   audit of **all** components of the repo, including tests and the
   dev environment, with a complexity estimate **and** an explicit
   "is it worth doing" verdict for **every** finding raised — not just
   for some items at the end of the report.

The final report must be written to a new file
`docs/studies/NN-report-…md` (check `ls docs/studies` for the next
free number — at the time this prompt was written the last file is
`18-prompt-feature14-oidc-only-login.md`, so `19` is likely free, but
don't assume without checking).

## Starting point: an existing audit, to revalidate, not to copy

`docs/audit/audit-2026-07.md` (2026-07-08) is a complete audit of the
same kind, component by component, with measured coverage numbers and
findings sourced by file+line. **Don't start from scratch**: read it
first, and for every remaining "Important"/"Cosmetic" finding, check
whether it's still true before copying it into the new report.

**Proof that things have already moved since then**: the July 8 audit
lists as an "Important" Frontend finding — *"No eslint/prettier
(`tsc -b` is the only lint)"*. This is no longer true:
`frontend/eslint.config.js` exists, `frontend/package.json` has a
`"lint": "eslint ."` script and `.github/workflows/ci.yml:298` runs
`npm run lint` in GitHub CI (canonical according to that same audit).
The only place where it's still false is a **stale comment**,
`.gitlab/ci/frontend.yml:2`: *"There is no eslint config in the repo
(yet): tsc -b is the lint gate."* — itself a small finding worth
raising (a CI comment lying about the repo's state). Treat this as the
general operating procedure: every line of the July 8 audit is a
hypothesis to re-test, not an established fact. Explicitly note in the
new report what has changed since (fixed, still open, worsened).

Relevant commits since July 8 (`git log --oneline` on `main` will give
you the exact list): splitting `waas-images/` out of the monorepo,
guacd clipboard + HTTPS dev, KasmVNC clipboard, owner-only scope for
workspaces, YamlEditor unification, fleet grouping by owner, "direct
image deploy" feature, "OIDC-only login" feature, pushing the Helm
chart as an OCI artifact. Check the impact of each on the previous
audit's findings.

## Scope — ALL components, including tests and the dev environment

Treat each of these as a first-class component (no footnote treatment
for the last two, that's explicitly requested):

- `operator/` (`internal/`, `pkg/`, `api/`, **and `operator/test/` +
  `operator/hack/`**)
- `api-server/` (`internal/`, `migrations/`, `cmd/`)
- `frontend/` (`src/`, **and all existing `*.test.tsx`/`*.test.ts`
  files**)
- `wwt/`
- `shared/`
- `hack/` — **including `hack/dev/`** (k3d config, seeds, dev
  images/templates, `seed-ssh`): it's the dev environment, to be
  audited on the same footing as production code, not skipped because
  "it's just dev"
- `test/smoke/` (k3d e2e)
- `gitops/governance/`
- `helm/waas/`
- `.mise.toml` + `Makefile` (`dev-*` targets in particular) —
  toolchain pins, dev environment reproducibility
- **CI itself as an audited component**: `.gitlab-ci.yml` +
  `.gitlab/ci/*.yml`, `.github/workflows/ci.yml`, `renovate.json` —
  duplication between the two CIs (GitLab in transition according to
  the previous audit — check where that transition currently stands),
  comment/reality drift like the eslint example above
- `docs/` — spot orphaned or contradictory documentation versus the
  current code (e.g. an ADR or a `docs/studies/*` entry describing
  behavior that has since changed)

## Track A — Trivy / CI: verify and fix if trivial

### How to check the real state

`glab` is authenticated on this project in this environment. Use it
directly rather than guessing the CI state:

```
glab ci status -b main
glab api projects/:id/pipelines/<id>/jobs      # list of jobs + status
glab api projects/:id/jobs/<job-id>/trace       # logs of a specific job
```

(`glab ci view` doesn't work outside an interactive TTY — use `glab ci
status`/`glab api` as above in this context.)

### State observed at the time this prompt was written (2026-07-11)

Pipeline `#14` (sha `33da60999c92`, branch `main`): jobs **`trivy-deps`
(stage `security`) and `scan-frontend`/`scan-api-server`/`scan-operator`
(stage `scan`) all FAILED**. Identical cause across all 4: the gate
`trivy fs --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed`
(`.gitlab/ci/security.yml:29-32`) fails on CVEs **with an available
fix**:

- `golang.org/x/net` at `v0.47.0` in `api-server/go.mod:72`,
  `operator/go.mod:50`, `test/smoke/go.mod:33` (all on
  `// indirect` lines) — CVE-2026-25681, CVE-2026-27136,
  CVE-2026-33814, CVE-2026-39821, all HIGH, fix ≥ `0.55.0`.
- `golang.org/x/crypto` at `v0.45.0` in `api-server/go.mod:15`
  (direct dependency) — several HIGH CVEs in `x/crypto/ssh` (including
  CVE-2026-39828…39835, CVE-2026-42508, CVE-2026-46595, CVE-2026-46597),
  fix ≥ `0.52.0` for the first wave — **re-check the latest fixed
  version at the time you run this prompt**, the CVE landscape keeps
  moving and a new CVE may have appeared since.

`wwt/go.mod` and `shared/go.mod` don't reference these two libs as of
this writing — check that this stays true after a `go mod tidy` on the
affected modules (the dependency graph can change).

### Expected action

1. For each affected `go.mod` (`api-server`, `operator`,
   `test/smoke`, and any other `go.work` module that turns out to be
   affected): first check whether an **open Renovate MR** already
   exists for this bump (`renovate.json` has `config:recommended`, the
   `gomod` manager is active by default — this bump should normally be
   proposed automatically). If an MR exists, prefer picking it up/
   merging it rather than duplicating the work; if it doesn't exist or
   is blocked, do the bump yourself.
2. `go get -u golang.org/x/net golang.org/x/crypto@latest` (or a
   targeted version if `@latest` breaks a constraint elsewhere) then
   `go mod tidy` in each affected module. Regenerate `go.work.sum` if
   necessary.
3. Build + test the affected module (`go build ./...`, `go test ./...`)
   before considering the fix done — a `x/net`/`x/crypto` bump is
   usually API-non-breaking but check anyway (`x/crypto/ssh` has a
   history of shifting signatures).
4. Reproduce the scan locally to confirm
   (`trivy fs --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed
   --exit-code 1 .` at the root, image `aquasec/trivy:0.63.0` as in
   CI) or re-run the pipeline if you prefer to validate via GitLab
   directly.
5. The `trivy-deps` job also scans `package-lock.json`
   (`.gitlab/ci/security.yml:30`) — check whether there are npm
   HIGH/CRITICAL CVEs not captured by the run you just inspected (the
   run inspected at the time of writing only showed Go CVEs, but
   reconfirm).
6. Separate commit for this fix, distinct from the audit report
   (Track B) — these are two changes of a different nature, don't mix
   them into the same commit.

### What NOT to do

- Don't touch `TRIVY_SEVERITY`/`TRIVY_EXIT_CODE` or the scanners to
  silence the failure — the comment at the top of
  `.gitlab/ci/security.yml:1-3` is explicit: these variables are for
  incident management, never to be set in the YAML. The only
  acceptable fix is real remediation (the bump).
- If a CVE has **no** fix available as of the execution date, or if
  the bump breaks something non-trivial (API breaking, conflicting
  version constraint): **don't force it**. Log it as a normal Track B
  finding, with its complexity and "worth it" verdict, instead of
  hacking around it.

## Track B — Qualitative audit, method and format

Same rigor standard as `docs/audit/audit-2026-07.md`: every finding
is **sourced by file+line**, coverage numbers are **measured** (run
the real coverage tools per component), never estimated by eyeballing.
No implementation in this track, report only (the only code you touch
in this prompt is the Track A bump).

For each component in the scope above, look for:

- **Duplicates** — duplicated logic/data between files or between
  components (the previous audit found 5, with at least one case
  having already caused a real incident — check whether D1 through D5
  still hold and look for new ones, especially in code added since
  July 8).
- **Organization** — files/functions that are too large, questionable
  splits, conventions not respected relative to the rest of the
  component.
- **Security** — beyond Trivy (which only covers dependency CVEs):
  authorization, input validation, secrets, network timeouts,
  everything a normal application security audit would cover.
- **Tests** — real coverage measured per package/component, quality
  (tests that test the mock rather than the behavior?), known
  flakiness (the previous audit noted a `time.Sleep(100ms)` in
  `event_hub_test.go:77` — check if there are others), completely
  uncovered areas. **Treat the coverage gap as a full-fledged
  component of the report**, not a line buried in each section.
- **Dev environment** (`hack/dev/`, `.mise.toml`, `Makefile dev-*`
  targets) — reproducibility, version pins, undocumented scripts,
  divergence from what CI actually uses.
- **Documented but unaddressed debt** — the previous audit noted this
  repo has no TODO/FIXME (the debt lives in the docs). Check whether
  this discipline still holds, and whether the debt items it listed
  (uncorrected N+1, stale `registry.xorhub.io` refs, etc.) are
  resolved or not.

### Mandatory format for each finding

A table, one row per finding, **exactly these columns** — the "Worth
it?" column is filled in for **every** row, never left implicit or
deferred to a separate summary table:

| # | Finding | Component | Source (file:line) | Category | Severity/risk | Complexity | Worth it? |
|---|---|---|---|---|---|---|---|
| … | (one sentence, the concrete problem) | operator / api-server / frontend / … | `path/to/file.go:123` | duplicate / organization / security / refactor / test / CI-dev-env | low / medium / high + why | S (< 1 d) / M (1-3 d) / L (3-5 d) / XL (> 5 d) — rough estimate, not a precise costing | Yes / No / Debatable — **one sentence of justification mandatory**, not just the word |

"Worth it?" must decide, not describe — e.g. "No: cosmetic, no
incident experienced, the rename would break 4 call sites for zero
benefit" rather than "depends on the context".

### Final summary

After the detailed table, a sorted action plan section (quick wins
first) that **reuses** the Complexity/Worth-it columns already set row
by row — don't reinvent a second, contradictory estimate in this
section, as the "Plan" table of `audit-2026-07.md` §5 implicitly did.

## Constraints

- Track A = the only place where you modify production code; Track B
  = pure report.
- Never weaken a security gate (Trivy thresholds, scanners) to make CI
  pass.
- Don't duplicate an already-open Renovate MR for the same bump.
- Every finding in the report must be usable standalone by a future
  Fable session without re-reading this conversation — enough
  file:line detail so a future `docs/studies/NN-prompt-*.md` doesn't
  have to redo the search from scratch (this is explicitly the
  criterion for "report easily usable afterward").

## Open points (your judgment call)

- Exact number of the output file (`docs/studies/NN-report-…md`) —
  check the last free number at execution time.
- Component vs. sub-folder granularity for "organization" findings
  (e.g. does a 900-line service deserve a finding per service or a
  grouped "catch-all services" finding) — free, document the choice.
- If a Trivy CVE has no available fix as of the execution date: log it
  as a documented accepted risk in the report rather than blocking
  Track A on it.
