# CI/CD — GitLab pipeline and release procedure

A single entry point: `.gitlab-ci.yml` (root), factored into local
templates under `.gitlab/ci/`. CI **never** deploys to the cluster:
it produces versioned artifacts (images + a Git tag carrying the chart)
that ArgoCD consumes.

## Overview

```
MR (merge_request_event) — selective builds via rules:changes
├─ lint      go-lint ×5 (golangci-lint) · frontend-typecheck (tsc -b)
│            helm-render (lint+template) · crd-schemas → kubeconform
│            go-generated-drift (controller-gen/CRDs/docs regenerated == commit)
│            hadolint · shellcheck
├─ test      go-test ×4 (-race, cobertura coverage in the MR)
│            frontend-test (vitest + coverage)
├─ security  gitleaks (secrets) · trivy-deps (go.mod + package-lock) — BLOCKING
├─ build     per modified component: build-<c>-amd64 (runner `amd`)
│            + build-<c>-arm64 (runner `arm`) → merge-<c> (manifest list)
│            tags pushed: mr-<iid>-<short-sha>
├─ scan      scan-<c>: trivy image on the merged manifest — BLOCKING
└─ validate  smoke-connections (k3d + real guacd session per protocol)
             manual in MR but allow_failure:false → merge gate

main — same gates, build of ALL components (tags <short-sha> + main)
       automatic smoke-connections. Every main SHA is releasable.
       chart-oci-main: OCI push of the chart, version `X.Y.Z-main.<short-sha>`
       (SemVer prerelease, never collides with a vX.Y.Z tag).

tag vX.Y.Z — PROMOTION, zero rebuild
├─ release-verify   Chart.yaml bumped (version=X.Y.Z, appVersion="vX.Y.Z"),
│                   <short-sha> images present, vX.Y.Z tags still free
├─ release-images   imagetools create <short-sha> → vX.Y.Z (digests identical
│                   to what was tested/scanned) + mandatory cosign signature
├─ release-chart    helm package + OCI push of the chart (Chart.yaml of the tagged
│                   SHA, already verified == vX.Y.Z by release-verify)
├─ release-notes    git-cliff (cliff.toml) + digest table
└─ release-create   GitLab Release on the tag (needs: release-notes, release-chart)
```

`workflow:rules`: MR pipeline takes priority (never a branch+MR
duplicate), default branch, `v*` tags only. All jobs are in a DAG
(`needs`), stages are only there for readability.

## Helm chart as OCI

No dedicated Helm registry (no ChartMuseum/Harbor): the chart is
pushed as an OCI artifact into **the same Container Registry as the
images**, under the `charts/` subpath (`$CI_REGISTRY_IMAGE/charts/waas:<version>`).
Two jobs, `alpine/helm:3.17.3`, `helm registry login` with the
predefined `CI_REGISTRY_*` variables:

- `chart-oci-main` (stage `build`, main only): `helm package
  --version X.Y.Z-main.<short-sha>` — SemVer prerelease, never
  collides with a release tag, mirrors the images' mobile `main` tag.
- `release-chart` (stage `release`, tag `vX.Y.Z`): `helm package`
  **without override** — the `Chart.yaml` of the tagged commit already
  carries `version: X.Y.Z` (verified by `release-verify`), so packaging
  this file as-is IS the release, not a rebuild.

ArgoCD continues to deploy from the Git tag (`path: helm/waas`); this
OCI chart is for external `helm pull`/`helm install --version`
consumers.

## Multi-arch (amd64/arm64)

**Native per-architecture builds**: amd64 runners carry the `amd`
tag, arm64 runners (Turing RK1) the `arm` tag. Each job pushes
`<tag>-amd64` / `<tag>-arm64` (buildx registry cache per component and
per arch: `<registry>/cache:<component>-<arch>`), then `merge-<c>`
assembles the final manifest list via `docker buildx imagetools create`.
No QEMU: the Go Dockerfiles cross-compile from `$BUILDPLATFORM` and the
frontend builds natively on both sides.

Runner prerequisites: Docker executor with dind (privileged), `amd` and
`arm` tags set. Jobs with no specific arch need go on `amd` (the most
powerful fleet) via `default:tags`.

The desktop images (repo `waas-images`, **split from the monorepo since
2026-07-10**) follow the same strategy in their own pipeline: a native
build job per arch (smoke + Trivy scan on **each** arch, before push)
then a merge job that assembles the manifest list, publishes the
immutable `<version>` tag (main) and signs it. The `waas-images:`
trigger job of the root pipeline was removed with the split — no change
to that repo can affect these images anymore.

## Cutting a release

1. **Bump the chart** (it's the Git tag that ArgoCD deploys, the chart at
   the tag must reference itself):
   ```yaml
   # helm/waas/Chart.yaml
   version: 1.2.0        # X.Y.Z
   appVersion: "v1.2.0"  # vX.Y.Z — becomes the default image tag
   ```
   Commit via MR (`release: v1.2.0`), merge, **wait for the main
   pipeline to go green** (it produces the `<short-sha>` images that will
   be promoted).
2. **Tag the merge commit**:
   ```sh
   git tag v1.2.0 <merge-sha> && git push origin v1.2.0
   ```
3. The tag pipeline promotes, signs, generates the changelog + Release. If
   it fails at `release-verify`, nothing was pushed: fix and re-tag
   (a new version — never reuse a tag).
4. **On the ArgoCD side**: bump `targetRevision: v1.2.0` (Application
   pointing at this repo, path `helm/waas`) — the only GitOps action
   needed.

### Tag immutability

- `release-verify` fails if `vX.Y.Z` already exists in the registry.
- To configure on the GitLab side (once): **Settings → Repository →
  Protected tags → `v*`** (creation restricted to Maintainers, no
  deletion); the GitLab registry does not protect image tags against
  overwriting — it's the verify + protected Git tag pair that guarantees
  immutability.

## Required CI/CD variables (Settings → CI/CD → Variables)

| Variable | Type | Usage |
|---|---|---|
| `COSIGN_PRIVATE_KEY` | masked | signing key (same as the waas-images repo) — release **fails** without it |
| `COSIGN_PASSWORD` | masked | key passphrase |

Everything else (registry, tokens) goes through the predefined
`CI_REGISTRY_*` variables. `TRIVY_SEVERITY` / `TRIVY_EXIT_CODE` exist for
incident response (e.g. switching a scan to report-only while waiting
for an upstream fix) — never to be set permanently.

## Cleaning up ephemeral images

The `mr-<iid>-<sha>`, `<sha>` and `<sha>-{amd64,arm64}` tags are
ephemeral. To configure (once): **Settings → Packages and
registries → Container registry → Cleanup policies**:

- run: every week;
- **keep**: regex `^(v\d+\.\d+\.\d+|main|\d+\.\d+\.\d+.*)$` + the 5 most
  recent tags per image;
- **remove**: regex `.*`, older than 14 days.

Policies only delete tags; blob garbage collection is the GitLab
instance's job (`registry-garbage-collect`, admin side). The buildx
cache repo (`<registry>/cache`) overwrites itself continuously and does
not grow indefinitely.

## Debugging a job

- **Reproduce locally**: each job is an image + a short script.
  `go-lint` ≈ `cd <module> && golangci-lint run`; `kubeconform` ≈
  `python3 hack/ci/crd_to_jsonschema.py helm/waas/crds /tmp/s && helm
  template waas helm/waas | kubeconform -strict …`; builds ≈
  `sh .gitlab/ci/build-app-image.sh` with `COMPONENT/ARCH/BUILD_CONTEXT/
  APP_TAG` set.
- **Testing an MR on a cluster**: the `mr-<iid>-<short-sha>` images
  are in the project's registry; `helm upgrade --install waas
  helm/waas --set image.registry=$CI_REGISTRY_IMAGE --set image.tag=mr-…`.
- **Red `go-generated-drift`**: run `make generate manifests
  docs-params` and commit the result.
- **Red `release-verify`**: the message says what to fix (Chart.yaml
  not bumped, tag placed on a SHA without a green main pipeline, or an
  image tag that already exists).
- **Smoke**: see `docs/smoke-connections.md`.

## Renovate

`renovate.json` (root) covers: `FROM` in Dockerfiles, job images
in `.gitlab-ci.yml` + `.gitlab/ci/*.yml`, tool pins in
scripts (`buildkit`, `trivy`, `cosign`, `binfmt`), `go run tool@vX`,
`CONTROLLER_GEN_VERSION`, `git-cliff`. Since the 2026-07-10 split, the
`waas-images` repo again has its own `renovate.json` (the root config
had absorbed it in the monorepo era). Hygiene rule:
**no `latest`** — every new job image must be pinned (exact tag,
digest added by Renovate via `docker:pinDigests`).
