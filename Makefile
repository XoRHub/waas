# WaaS — root Makefile. Each component keeps its own Makefile; this one fans out.

GO_MODULES := shared operator api-server wwt

# --- local k3d dev environment -------------------------------------------
CLUSTER_NAME     := waas-dev
DEV_NAMESPACE    := waas
IMAGE_TAG        := dev
DEV_IMAGES       := operator api-server wwt frontend

# Workspace desktop images. hack/dev/images-dev.yaml + templates-dev.yaml
# point at the published registry (docker.io/xorhub/*, catalog-waas-images.yaml)
# by default — no local build/import needed. For local iteration on a
# waas-images change, pass LOCAL_IMAGES (space-separated IMAGE= variants from
# that repo's own Makefile, e.g. "firefox devtools ubuntu-desktop-noble") to
# dev-load-images / dev-build-images. Its `build` target does not chain
# parents (FROM waas-local/<parent>:dev must already exist locally) — list
# the full chain in build order, e.g. LOCAL_IMAGES="core-ubuntu-noble
# core-ubuntu-noble-xfce firefox". The checkout is resolved via
# WAAS_IMAGES_DIR (default: sibling of this repo, since the 2026-07-10 split
# — git@github.com:XoRHub/waas-images.git).
WAAS_IMAGES_DIR ?= ../waas-images
LOCAL_IMAGES    ?=

.PHONY: all build test lint generate manifests docs-params frontend-build docker-build \
	dev-up dev-down dev-reset dev-bootstrap dev-build dev-load dev-deploy \
	dev-reload dev-reload-all dev-build-images dev-load-images \
	dev-status dev-logs dev-url tidy helm-docs helm-unittest

all: build

build:
	@for m in $(GO_MODULES); do \
		echo "==> build $$m"; \
		(cd $$m && go build ./...) || exit 1; \
	done

test:
	@for m in $(GO_MODULES); do \
		echo "==> test $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

tidy:
	@for m in $(GO_MODULES); do \
		echo "==> tidy $$m"; \
		(cd $$m && go mod tidy) || exit 1; \
	done

generate:
	$(MAKE) -C operator generate
	$(MAKE) -C shared generate

manifests:
	$(MAKE) -C operator manifests

# operator/docs/guacd-parameters.md is generated from the parameter
# registry so the docs can never drift from what the webhook enforces.
docs-params:
	cd operator && go run ./cmd/paramsdoc docs/guacd-parameters.md

# Generated TypeScript API models (tygo): api-server/internal/model is
# the single source of the frontend types (frontend/src/types.gen.ts,
# facade in types.ts). Drift-checked in CI like the CRDs.
generate-types:
	cd api-server && go run github.com/gzuidhof/tygo@v0.2.21 generate
	cd frontend && npx prettier --write src/types.gen.ts

# helm/waas/README.md is generated from Chart.yaml + values.yaml
# (README.md.gotmpl is the template). Drift-checked in CI like the CRDs.
# helm-docs itself is pinned in .mise.toml (mise install provides it).
# --sort-values-order file: keeps the table in values.yaml's own section
# order (image, secretsJob, workspaces, ...) instead of flattening it
# alphabetically.
helm-docs:
	cd helm/waas && helm-docs --sort-values-order file

helm-unittest:
	helm unittest helm/waas

frontend-build:
	cd frontend && npm ci && npm run build

# api-server and wwt build from the repo root (module replaces ../shared).
docker-build:
	docker build -t ghcr.io/xorhub/waas/operator:dev operator
	docker build -t ghcr.io/xorhub/waas/api-server:dev -f api-server/Dockerfile .
	docker build -t ghcr.io/xorhub/waas/wwt:dev -f wwt/Dockerfile .
	docker build -t ghcr.io/xorhub/waas/frontend:dev frontend

# --- local k3d dev environment --------------------------------------------
# Typical flow:
#   make dev-bootstrap          # blank machine -> full env (all the steps below, in order)
#   make dev-up                 # create cluster + cert-manager (once)
#   make dev-build dev-load dev-deploy
#   make dev-load-images        # workspace desktop images (after dev-deploy: needs the ns)
#   make dev-reload             # after service/frontend code changes: rebuild, reimport, restart
#   make dev-reload-all         # same, plus the desktop images (waas-images repo)
#   make dev-down               # tear down

# The whole flow above in one shot, from a blank machine. Safe to re-run on
# a partially built env: every step is idempotent (dev-up checks for the
# cluster, dev-deploy is `helm upgrade --install`, the ssh-secret seed
# re-applies). smoke stays a separate manual step — it takes several
# minutes and needs the pods to be Ready, which helm here does not wait for.
dev-bootstrap: dev-up dev-build dev-load dev-deploy dev-load-images
	@echo "==> dev environment ready. Validate real sessions with: make smoke"
	@$(MAKE) dev-url

dev-up:
	@command -v k3d >/dev/null 2>&1 || { echo "k3d not found: https://k3d.io/#installation"; exit 1; }
	@command -v helm >/dev/null 2>&1 || { echo "helm not found: https://helm.sh/docs/intro/install/"; exit 1; }
	k3d cluster list $(CLUSTER_NAME) >/dev/null 2>&1 || k3d cluster create --config hack/dev/k3d-config.yaml
	helm repo add jetstack https://charts.jetstack.io --force-update
	helm upgrade --install cert-manager jetstack/cert-manager \
		--namespace cert-manager --create-namespace \
		--set crds.enabled=true --wait
	@echo "==> cluster ready. Next: make dev-build dev-load dev-deploy"

dev-down:
	k3d cluster delete $(CLUSTER_NAME)

dev-reset: dev-down dev-up

dev-build: docker-build

dev-load:
	@for img in $(DEV_IMAGES); do \
		echo "==> import $$img:$(IMAGE_TAG)"; \
		k3d image import ghcr.io/xorhub/waas/$$img:$(IMAGE_TAG) -c $(CLUSTER_NAME) || exit 1; \
	done

dev-deploy:
	# Helm only installs CRDs on first install, never on upgrade — apply
	# them explicitly so schema changes (new fields, validation) always
	# reach the cluster.
	kubectl apply -f helm/waas/crds/
	helm upgrade --install waas helm/waas \
		--namespace $(DEV_NAMESPACE) --create-namespace \
		-f hack/dev/values-dev.yaml
	# Idempotent: the dev-ssh Secret must exist in BOTH the platform ns
	# and the default workloads ns (pods resolve secretKeyRef in their
	# own namespace) — re-run here so redeploys never leave them apart.
	sh hack/dev/seed-ssh-secret.sh $(DEV_NAMESPACE)
	# Governance seeds live here, not in dev-load-images: the chart's
	# bootstrap default policy is disabled in dev (values-dev.yaml) so
	# kubectl stays the SINGLE field manager of the policies — helm and
	# kubectl both applying `default` is how dev-deploy used to fail with
	# server-side-apply conflicts. Applied on every deploy so a plain
	# dev-reload never leaves the env without a default policy (fail-closed).
	kubectl -n $(DEV_NAMESPACE) apply -f gitops/governance/policies.yaml
	@$(MAKE) dev-url

# Rebuild, reimport, re-render the chart and restart — the inner loop
# while coding. dev-deploy is NOT optional here: a rollout restart alone
# reuses the manifests of the last helm upgrade, so chart-side changes
# (env wiring, RBAC) silently never reach the cluster — that drift is how
# the guacd netpol lockout shipped despite the fix being committed.
dev-reload: dev-build dev-load dev-deploy
	kubectl -n $(DEV_NAMESPACE) rollout restart \
		deploy/waas-operator deploy/waas-api-server deploy/waas-wwt deploy/waas-frontend

# dev-reload, plus a rebuild/reimport of the desktop images (waas-images
# repo) and a re-apply of the dev catalog. Use after touching the images
# repo; noticeably slower (full docker builds of the desktop stacks), so
# plain dev-reload stays the inner loop for service/frontend code. Pure
# composition — the restart lives in dev-reload only, and running it before
# the image import is fine: workspace images never run in the four service
# deployments.
dev-reload-all: dev-reload dev-load-images

# Build LOCAL_IMAGES locally (docker build via the waas-images repo's own
# Makefile). No-op when LOCAL_IMAGES is empty — the default flow needs no
# local checkout at all. The existence check is a make-time conditional so
# `make -n` graph checks still resolve without the clone.
dev-build-images:
ifneq ($(strip $(LOCAL_IMAGES)),)
ifeq ($(wildcard $(WAAS_IMAGES_DIR)/Makefile),)
	@echo "error: waas-images repo not found at '$(WAAS_IMAGES_DIR)' (split out of this monorepo 2026-07-10)." >&2
	@echo "  clone it first:  git clone git@github.com:XoRHub/waas-images.git $(WAAS_IMAGES_DIR)" >&2
	@echo "  or point WAAS_IMAGES_DIR at an existing checkout:  make dev-build-images WAAS_IMAGES_DIR=/path/to/waas-images" >&2
	@exit 1
else
	@for img in $(LOCAL_IMAGES); do \
		echo "==> build $$img"; \
		$(MAKE) -C $(WAAS_IMAGES_DIR) build IMAGE=$$img || exit 1; \
	done
endif
endif

# Seed the dev catalog (WorkspaceImage/WorkspaceTemplate pointed at the
# published registry by default). The WorkspacePolicy seeds are applied by
# dev-deploy (single field manager — see the note there). Requires the
# $(DEV_NAMESPACE) namespace, i.e. run after dev-deploy. Pass LOCAL_IMAGES to additionally
# build and k3d-import specific waas-local:dev tags on top of that — point
# the matching WorkspaceTemplate's `image:` at the tag yourself to run it:
#   make dev-load-images LOCAL_IMAGES="firefox devtools"
dev-load-images: dev-build-images
	sh hack/dev/seed-ssh-secret.sh $(DEV_NAMESPACE)
	kubectl -n $(DEV_NAMESPACE) apply \
		-f hack/dev/images-dev.yaml \
		-f hack/dev/templates-dev.yaml
	@for img in $(LOCAL_IMAGES); do \
		echo "==> import waas-local/$$img:dev"; \
		k3d image import waas-local/$$img:dev -c $(CLUSTER_NAME) || exit 1; \
	done
	@echo "==> workspace images + templates loaded into $(DEV_NAMESPACE)."

dev-status:
	kubectl -n $(DEV_NAMESPACE) get pods,svc,ingress

# make dev-logs C=api-server
dev-logs:
	kubectl -n $(DEV_NAMESPACE) logs -f deploy/waas-$(C)

dev-url:
	@echo "==> https://waas.127.0.0.1.nip.io:8443  (admin / admin123) — self-signed cert, accept the warning once; required for seamless clipboard"
	@echo "==> http://waas.127.0.0.1.nip.io:8080   (same login) — smoke tests; no seamless clipboard (not a secure context)"

# Per-protocol connection gate (delivery criterion): creates a workspace
# for each protocol, waits readiness and establishes a REAL guacd session
# through wwt. Run against the k3d dev env after every iteration — a
# change that breaks session establishment must fail here, not at the
# user's desk. See docs/smoke-connections.md.
smoke:
	WAAS_SMOKE_URL=$${WAAS_SMOKE_URL:-http://waas.127.0.0.1.nip.io:8080} \
		go test -count=1 -v -timeout 30m ./test/smoke/
