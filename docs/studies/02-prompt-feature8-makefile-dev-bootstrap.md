# Fable 5 Prompt — Feature 8: full bootstrap make target + reload that also rebuilds workspace images

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

The local dev environment runs on k3d, driven by the root `Makefile` (`/Makefile`). There is already a set of well-broken-down targets, but (1) no single target chains the whole bootstrap from scratch (cluster → build → load → deploy → workspace images) — today you have to invoke 4-5 `make` commands in an order that is only documented in a comment, and (2) `dev-reload`, the fast reload loop used during dev, only rebuilds/reloads the 4 service images (operator/api-server/wwt/frontend) — never the desktop images from `waas-images/`.

## What already exists (know this before coding — the full relevant current Makefile)

```
GO_MODULES     := shared operator api-server wwt
CLUSTER_NAME   := waas-dev
DEV_NAMESPACE  := waas
IMAGE_TAG      := dev
DEV_IMAGES     := operator api-server wwt frontend
WORKSPACE_BASE_IMAGES := ubuntu-base-vnc ubuntu-base-rdp   # build-only, never imported
WORKSPACE_IMAGES      := ubuntu-xfce ubuntu-firefox dev-ssh
```

- `all: build` (line 22) — Go build only, unrelated to k3d dev.
- `dev-up` (75-83): creates the k3d cluster (`hack/dev/k3d-config.yaml`) + installs cert-manager. Idempotent (`k3d cluster list ... || k3d cluster create ...`).
- `dev-build` (90) = `docker-build` (61-65): builds the 4 service images, tagged `ghcr.io/xorhub/waas/<img>:dev`.
- `dev-load` (92-96): `k3d image import` in a loop over `DEV_IMAGES`.
- `dev-deploy` (98-110): `kubectl apply -f helm/waas/crds/` (CRDs are never updated by a `helm upgrade`, only on install — hence this systematic explicit apply) + `helm upgrade --install waas ...` + ssh secret seed + `dev-url`.
- `dev-reload` (112-119): **already** `dev-build dev-load dev-deploy` + `kubectl rollout restart` on the 4 service deployments. The comment above it (112-116) explains that `dev-deploy` is intentionally non-optional there: a real incident (guacd netpol lockout) happened because a restart alone reused the previous Helm render without re-rendering the chart. **So the "reload that rebuilds images" requested here already exists for the 4 service images** — the real gap is that it never touches the desktop images.
- `dev-build-images` (122-126): loops `$(MAKE) -C waas-images build IMAGE=$$img` over `WORKSPACE_BASE_IMAGES` + `WORKSPACE_IMAGES`.
- `dev-load-images` (128-142, depends on `dev-build-images`): `k3d image import waas-local/$$img:dev` on `WORKSPACE_IMAGES` only (base images are never imported, they never run as a pod) + ssh secret reseed + `kubectl apply` on `images-dev.yaml`/`templates-dev.yaml`/`gitops/governance/policies.yaml`. Requires the namespace to already exist (hence after `dev-deploy`).
- The "typical" flow is documented only as a comment (68-73):
  ```
  make dev-up
  make dev-build dev-load dev-deploy
  make dev-load-images
  make dev-reload      # after a code change
  make dev-down
  ```
- `smoke` (159-161): runs `test/smoke/` against the dev URL (`WAAS_SMOKE_URL`, default `http://waas.127.0.0.1.nip.io:8080`).

`README.md:38-51` describes a simplified "Quickstart" flow (roughly `k3d cluster create waas`/`helm install waas helm/waas`) that **no longer matches** the real Makefile flow (wrong cluster name, no mention of the real targets) — this is a doc to fix at the same time if you touch it, but it's not the core of this feature.

## What needs to be delivered

### A. A full bootstrap target

Add a target (e.g. `dev-bootstrap`) that chains everything, in the correct order discovered above, in a single invocation from a totally clean environment:

```
dev-bootstrap: dev-up dev-build dev-load dev-deploy dev-load-images
	@echo "==> dev environment ready: $$(make dev-url)"
```

Verify that each listed target is indeed idempotent if the global target is re-run on a partially set-up environment (this is already broadly the case — `dev-up` checks for the cluster's existence, `dev-deploy` does an `upgrade --install`, `seed-ssh-secret.sh` is documented as idempotent) — you shouldn't need to rewrite anything in the existing targets for this, just verify that the chaining doesn't break anything. Add it to `.PHONY` (line 17-20) and to the "Typical flow" comment block (68-73) so it stays accurate.

### B. A reload that also rebuilds workspace images

`dev-reload` already covers the 4 service images (see above — don't duplicate this mechanism). Add a variant that also includes rebuilding/reloading the desktop images, for example:

```
dev-reload-all: dev-build dev-load dev-deploy dev-build-images dev-load-images
	kubectl -n $(DEV_NAMESPACE) rollout restart \
		deploy/waas-operator deploy/waas-api-server deploy/waas-wwt deploy/waas-frontend
```

Clearly document the difference in usage between `dev-reload` (fast, code of the 4 Go/frontend services) and `dev-reload-all` (slower, includes the `waas-images/` images — useful after a change in `waas-images/`) in a comment above each target, following the model of the comment already present for `dev-reload` (112-116).

## Constraints to respect

- Don't invent a new build/import mechanism: strictly reuse the existing atomic targets (`dev-build`, `dev-load`, `dev-deploy`, `dev-build-images`, `dev-load-images`) as `make` dependencies, don't duplicate their logic inline.
- Fix `README.md:38-51` so it points to `make dev-bootstrap` rather than the manual `k3d cluster create waas`/`helm install` sequence that no longer matches the real Makefile (different cluster name, missing targets).
- Add a lightweight CI test/check if the repo has one for the Makefile (check `.github/workflows/ci.yml` — otherwise, a simple `make -n dev-bootstrap` in CI to verify the target resolves without a circular-dependency/missing-target error is enough, no need to run a real k3d in CI if that isn't already done elsewhere).
- Document these two new targets in the "Typical flow" comment block (lines 68-73) and in any existing dev docs (`docs/*.md` mentioning k3d/dev, if found).

## Open points (your call)

- Target names (`dev-bootstrap`/`dev-reload-all` proposed) — pick a name consistent with the conventions already in place (`dev-*`) if you prefer a different name, document the choice.
- Should `dev-bootstrap` also call `smoke` at the end to validate that the environment actually works (real per-protocol connection), or leave that as a separate manual step as today — both are defensible; if you add it, make it opt-out (e.g. `SKIP_SMOKE=1` variable) since `smoke` takes several minutes.
