# GitHub Actions CI

GitHub is the canonical repository; the GitLab pipeline (`docs/ci.md`)
stays as-is until it's retired.

`.github/workflows/ci.yml` is the entry point (triggers, path filtering,
concurrency) and orchestrates five `workflow_call` reusable workflows,
one per domain, so each is reviewable on its own:

| File | Owns |
|---|---|
| `ci.yml` | `changes` (path filter), `chart-oci`, `release-please`, `promote`, `promote-chart` |
| `ci-go.yml` | `go-lint`, `go-test`, `go-generated-drift` |
| `ci-frontend.yml` | `frontend` |
| `ci-helm.yml` | `helm-manifests` |
| `ci-security.yml` | `security` |
| `ci-images.yml` | `build-images`, `merge-manifests`, `scan-images` |

`chart-oci`, `release-please`, `promote` and `promote-chart` stay in
`ci.yml` rather than being delegated, for two reasons:

- **`chart-oci` must not gate `release-please`** (it isn't in its
  `needs`, on purpose — a failed dev chart push shouldn't block an app
  release). A `workflow_call` job only succeeds once every job inside
  it does, so bundling `chart-oci` with `helm-manifests` in
  `ci-helm.yml` would accidentally couple their success.
- **`promote`'s cosign signature and the `release-please` → `promote`
  chaining are both file-path-sensitive.** The chaining sidesteps the
  `GITHUB_TOKEN` anti-recursion rule by staying in the same run
  (explained below); moving `promote` into a separate reusable
  workflow would also change the OIDC `job_workflow_ref` claim cosign
  records, which the `--certificate-identity-regexp` below matches
  against `ci.yml` specifically.

## Pipeline map

```
PR — selective by path (job `changes`, dorny/paths-filter)
├─ ci-go.yml
│  ├─ go-lint / go-test     DYNAMIC matrix per module, follows the real graph:
│  │                        api-server ← operator + shared ; wwt ← shared ;
│  │                        test/smoke ← operator (lint only, tests = cluster)
│  └─ go-generated-drift    operator/** — regenerating must be a no-op
├─ ci-frontend.yml          typecheck + vitest (frontend/**)
├─ ci-helm.yml              lint + render + helm-docs drift + helm unittest
│                            + kubeconform vs CRDs of THIS commit
│                            + operator ClusterRole vs generated RBAC
├─ ci-security.yml          gitleaks, trivy fs, hadolint, shellcheck — ALWAYS
└─ ci-images.yml            build-images per impacted component × {amd64, arm64},
                            push:false, local trivy scan (amd64)

push main — EVERYTHING is built (invariant: every main SHA carries the
│           complete image set, a prerequisite for promotion)
├─ same gates (ci-go/ci-frontend/ci-helm/ci-security)
├─ ci-images.yml
│  ├─ build-images           push <short-sha>-<arch>
│  ├─ merge-manifests        <short-sha> manifest list + mobile `edge` tag
│  └─ scan-images            blocking trivy on the manifest list
├─ chart-oci (in ci.yml)     OCI push of the chart, version `X.Y.Z-main.<short-sha>`
│                            (SemVer prerelease, never collides with vX.Y.Z)
│                            + mobile `0.0.0-edge` tag on the chart OCI artifact
└─ release-please (in ci.yml)  AT THE END of the pipeline (needs on the 5 call jobs)
   ├─ promote        (if "." release_created)          ZERO rebuild:
   │     verify (Chart.yaml appVersion = tag, sources present, tags free)
   │     → retag <short-sha> ⇒ vX.Y.Z → cosign keyless → mobile `latest`
   │       tag → digest table added to the Release notes
   └─ promote-chart   (if "helm/waas" release_created)  independent of promote:
         helm package (Chart.yaml of the tagged SHA, no override) + OCI push
         + mobile `latest` tag, using the chart's OWN SemVer
```

`edge` vs `latest`, on both images and the chart OCI artifact: `edge` is
the mobile pointer for every green **main** build (dev clusters only,
never a deploy reference); `latest` only ever moves on an **official
release** (the `promote`/`promote-chart` jobs). The two never point at
the same digest at the same time unless a release SHA happens to be
`main`'s tip.

On the chart artifact specifically, the moving dev pointer is tagged
`0.0.0-edge`, not a bare `edge`: Helm (and kustomize's `helmCharts`,
which validates through the same Helm SDK) requires OCI tags used with
`--version` to parse as SemVer, and `edge` alone doesn't. `0.0.0-edge`
is a valid SemVer prerelease with a name that stays fixed across
`Chart.yaml` version bumps — images have no such constraint (pulled by
plain tag, no `--version`), so they keep the bare `edge` tag.

## Helm CLI: v4

CI installs Helm **v4** from `.mise.toml` via `jdx/mise-action` — the
same `v4.2.3` pin `mise install` uses locally, one source for both
(previously `azure/setup-helm` with the version duplicated in the
workflows). Chart files (`Chart.yaml`,
`values.yaml`, templates) needed no changes: Helm v2 chart APIs stay
compatible on v4. The one thing that broke moving off v3: `helm plugin
install`'s `--verify` flag and plugin-signature verification are v4-only
(`helm-unittest`'s `plugin.yaml` uses the matching `platformHooks`
schema starting at `v1.1.0`, which a v3 plugin loader rejects outright —
hence `--verify=false` on that install). `helm registry login` already
took a bare host (`ghcr.io`, no `https://`), which v4.1+ requires, so
that needed no change.

## Helm chart as OCI

No dedicated Helm registry: the chart is pushed as an OCI artifact into
**ghcr.io**, the same registry as the images, under the `charts/`
subpath (`ghcr.io/<owner>/<repo>/charts/waas:<version>`). Helm (from
`.mise.toml` via `mise-action`) + `helm registry login` with
`GITHUB_TOKEN`; the extra mobile
tags (`0.0.0-edge`/`latest`) are added with `docker buildx imagetools create`
(same trick as the images), which needs a plain `docker/login-action`
login alongside the Helm one — the two CLIs keep separate credential
stores.

- job `chart-oci` (push main): `helm package --version
  X.Y.Z-main.<short-sha>`, then re-tags that push as `0.0.0-edge` — a
  SemVer-valid stand-in for the images' mobile `edge` tag (see above).
- job `promote-chart` (tag `waas-chart-X.Y.Z`, from the `helm/waas`
  release-please package): `helm package` **without override** — the
  `Chart.yaml` of the tagged commit already carries the released
  `version: X.Y.Z`, so packaging this file as-is IS the release, not a
  rebuild. Then re-tags that push as `latest`. Independent of the app's
  `promote` job/tag.

ArgoCD continues to deploy from the Git tag (`path: helm/waas`); this
OCI chart is for external `helm pull`/`helm install --version`
consumers.

## Chart docs and unit tests (`ci-helm.yml`)

- **`helm/waas/README.md`** is generated by `helm-docs` from
  `Chart.yaml` + `values.yaml`, using `helm/waas/README.md.gotmpl` as
  the template (badges, requirements and the values table are
  auto-generated; install/upgrade/production notes stay hand-written in
  the template). `make helm-docs` regenerates it locally; the
  `helm-docs drift check` step in `ci-helm.yml` re-runs the same
  pinned version (`helm-docs`, pinned in `.mise.toml` — one pin for CI
  and local) and fails the build if the committed `README.md` doesn't
  match — same drift-gate pattern as the CRDs and the generated
  TypeScript models.
- **`helm/waas/tests/*_test.yaml`** are `helm-unittest` suites. The
  plugin is installed by `.mise.toml`'s `postinstall` hook (a helm
  *plugin* has no mise registry entry, so it can't sit in `[tools]`),
  pinned to `v1.1.1` there for CI and local alike; `make helm-unittest`
  runs the suites locally.
  Rule: a test **never** asserts on a `values.yaml` default or on
  `Chart.yaml`'s `version`/`appVersion` — every input a test cares
  about is set explicitly in that test's `set:` block, and the one
  place that touches the `appVersion` fallback (`waas.tag` in
  `_helpers.tpl`) is checked with a `matchRegex` that only proves
  *something* was substituted, never the literal released version.
  Otherwise a routine release-please version bump would fail unrelated
  chart tests.

## Release (release-please, SemVer, conventional commits)

- Manifest mode: `release-please-config.json` + `.release-please-manifest.json`,
  **two independent packages** (like
  `DrummyFloyd/crunchy-userinit-controller`):
  - `"."` — the app: images, the `vX.Y.Z` git tag ArgoCD tracks
    (`targetRevision`, deploys straight from git — this tag is the real
    deployable-state contract), and `Chart.yaml`'s `appVersion` (default
    image tag, bumped via `extra-files`).
  - `"helm/waas"` — the chart's **own** SemVer (`Chart.yaml`'s
    `version` field, release-type `helm`), used only for the OCI chart
    artifact published for external consumers. It bumps only on commits
    touching `helm/`, independently of the app version — normal Helm
    practice, not a bug. `exclude-paths: ["helm/waas/"]` on the root
    package stops `helm/`-only changes from also cutting an app release.
  - Desktop images are out of scope (separate `waas-images` repo since
    2026-07-10, versioned per image).
- Each package gets its own release PR (`separate-pull-requests: true`).
  The app's release PR bumps `CHANGELOG.md` and `helm/waas/Chart.yaml`'s
  `appVersion` (`x-release-please-start-version`/`end` marker scoped to
  that one line; the `v` prefix survives because the updater only
  matches the numeric part). The chart's release PR bumps its own
  `CHANGELOG.md` and `Chart.yaml`'s `version` field natively (helm
  strategy) — comments and the `appVersion` line are untouched.
- **Procedure: merge either release PR, that's it.** The subsequent main
  run rebuilds/scans the merged SHA, creates that package's tag + Release,
  then runs the matching promotion job.
- **No PAT / GitHub App**: tags created with `GITHUB_TOKEN` trigger no
  workflow (GitHub anti-loop) — this is intentional. Promotion is
  chained in the SAME run via the `release_created` /
  `chart_release_created` outputs, which also guarantees that the
  promoted `<short-sha>` images were just pushed by this run (a separate
  `release.yml` on push main would race with the builds). A PAT would
  only be needed to re-trigger a separate `on: push: tags` workflow.
- Idempotent retry: `promote` tolerates a release tag that already
  exists **if the digest is identical** (re-run after a partial failure)
  and refuses otherwise (immutability).

## Multi-arch: QEMU vs native runners

Repository variable `WAAS_BUILD_STRATEGY` (Settings → Variables):

| value | arm64 leg |
|---|---|
| `native` | `ubuntu-24.04-arm` (free for public repos) |
| other / absent | `ubuntu-latest` + QEMU (safe default, private repo) |

The amd64 leg is always native. Switch to `native` once the repo is public.

## Cosign signing — divergence from GitLab

GitLab signs with a **key** (`COSIGN_PRIVATE_KEY`); GitHub signs with
**keyless OIDC** (Fulcio/Rekor, `id-token: write`). Verification:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/<owner>/<repo>/\.github/workflows/ci\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  <image>@<digest>
```

Consequences: no more secret to rotate, but the repo's identity is
recorded in the public Rekor log (fine, the repo is becoming public),
and a cluster policy-controller must verify **certificate identity**
on the GitHub side vs the **public key** on the GitLab side while both
coexist.

## Per-component coverage (Codecov)

`ci-go.yml`'s `go-test` job (one flag per Go module: `operator`,
`api-server`, `wwt`, `shared`) and `ci-frontend.yml`'s `frontend` job
each upload their existing coverage output (`coverage.out` / vitest's
`lcov` reporter) to Codecov via `codecov/codecov-action`, tagged with
a `flags:` matching the module/component name. `codecov.yml` at the repo
root declares those flags with `carryforward: true` — required because
this pipeline only tests the modules a commit actually touched (the
`changes` job): without carryforward, an untouched component's coverage
would read as 0%/missing on every PR that doesn't happen to touch it.
Codecov posts its own PR comment (`comment.layout` in `codecov.yml`)
breaking coverage down by flag, and the root `README.md` embeds one
badge per component (`?flag=<name>` on the badge URL).

Upload uses `use_oidc: true` (`permissions: id-token: write`, passed
through the `go`/`frontend` reusable-workflow calls in `ci.yml` — GitHub
only lets a reusable workflow **keep or reduce** permissions from its
caller, never elevate them, so both the caller job and the callee job
need the grant) instead of a stored `CODECOV_TOKEN`: same posture as the
`promote` job's keyless cosign, and it keeps working for fork PRs, where
repo secrets aren't available.

**Manual one-time setup this doc can't cover**: the repo must be added
on [codecov.io](https://about.codecov.io/) (sign in with GitHub) before
uploads/badges work — that's an external account action, not a config
file.

## Renovate pinning

Actions pinned by commit SHA (`helpers:pinGitHubActionDigests`),
`docker run` images in workflows covered by a regex customManager
(gitleaks, kubeconform, hadolint). Toolchain versions (go, node, helm,
golangci-lint, k3d, helm-docs, uv) live in `.mise.toml` only — CI
installs from it via `jdx/mise-action`, so the formerly manual bumps
(`version:` of golangci-lint-action and setup-helm, `node-version`)
no longer exist as separate pins — and Renovate's built-in `mise`
manager (enabled by default, resolves mise-registry short names) bumps
them there. The one pin that manager can't see is the `helm-unittest`
helm *plugin*, installed by `.mise.toml`'s postinstall hook: a regex
customManager in `renovate.json` covers it (github-releases
datasource).

## Accepted gaps / not ported (yet)

- **waas-images**: GitLab only, no GitHub equivalent. Since the
  2026-07-10 split, the topic belongs to the `waas-images` repo (see
  its `docs/RECIPE-STUDY.md`, § GitHub Actions CI).
- **smoke-connections** (k3d + real guacd sessions): too heavy for a
  hosted 7 GB runner; to be ported to a self-hosted runner or kept on
  GitLab.
- PR images **not pushed** (fork PR tokens cannot write to
  ghcr) — GitLab pushes `mr-*` tags; the PR scan runs on the locally
  loaded image (amd64).
- `merge-manifests` waits for ALL build pairs (GitHub does not do
  `needs` per matrix leg, unlike the per-component GitLab DAG).
- GitLab `release-verify` greps `appVersion`: the release-please
  markers are on separate lines, the existing greps remain valid.
