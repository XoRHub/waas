# GitHub Actions CI

GitHub is the canonical repository; the GitLab pipeline (`docs/ci.md`)
stays as-is until it's retired. A single workflow: `.github/workflows/ci.yml`.

## Pipeline map

```
PR — selective by path (job `changes`, dorny/paths-filter)
├─ go-lint / go-test        DYNAMIC matrix per module, follows the real graph:
│                           api-server ← operator + shared ; wwt ← shared ;
│                           test/smoke ← operator (lint only, tests = cluster)
├─ go-generated-drift       operator/** — regenerating must be a no-op
├─ frontend                 typecheck + vitest (frontend/**)
├─ helm-manifests           lint + render + kubeconform vs CRDs of THIS commit
├─ security                 gitleaks, trivy fs, hadolint, shellcheck — ALWAYS
└─ build-images             per impacted component × {amd64, arm64},
                            push:false, local trivy scan (amd64)

push main — EVERYTHING is built (invariant: every main SHA carries the
│           complete image set, a prerequisite for promotion)
├─ same gates + build-images → push <short-sha>-<arch>
├─ merge-manifests           <short-sha> manifest list + mobile `main` tag
├─ scan-images               blocking trivy on the manifest list
├─ chart-oci                 OCI push of the chart, version `X.Y.Z-main.<short-sha>`
│                            (SemVer prerelease, never collides with vX.Y.Z)
└─ release-please            AT THE END of the pipeline (needs on everything else)
   ├─ promote        (if "." release_created)          ZERO rebuild:
   │     verify (Chart.yaml appVersion = tag, sources present, tags free)
   │     → retag <short-sha> ⇒ vX.Y.Z → cosign keyless → digest table
   │       added to the Release notes
   └─ promote-chart   (if "helm/waas" release_created)  independent of promote:
         helm package (Chart.yaml of the tagged SHA, no override) + OCI push,
         using the chart's OWN SemVer
```

## Helm chart as OCI

No dedicated Helm registry: the chart is pushed as an OCI artifact into
**ghcr.io**, the same registry as the images, under the `charts/`
subpath (`ghcr.io/<owner>/<repo>/charts/waas:<version>`). `azure/setup-helm`
(v3.17.3) + `helm registry login` with `GITHUB_TOKEN`:

- job `chart-oci` (push main): `helm package --version
  X.Y.Z-main.<short-sha>`, mirroring the images' mobile `main` tag.
- job `promote-chart` (tag `waas-chart-X.Y.Z`, from the `helm/waas`
  release-please package): `helm package` **without override** — the
  `Chart.yaml` of the tagged commit already carries the released
  `version: X.Y.Z`, so packaging this file as-is IS the release, not a
  rebuild. Independent of the app's `promote` job/tag.

ArgoCD continues to deploy from the Git tag (`path: helm/waas`); this
OCI chart is for external `helm pull`/`helm install --version`
consumers.

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

## Renovate pinning

Actions pinned by commit SHA (`helpers:pinGitHubActionDigests`),
`docker run` images in workflows covered by a regex customManager
(gitleaks, kubeconform, hadolint). Pins not managed automatically
(manual bump): `version:` of golangci-lint-action and setup-helm,
`node-version`.

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
