# WaaS — root Makefile. Each component keeps its own Makefile; this one fans out.

GO_MODULES := shared operator api-server wwt

# --- local k3d dev environment -------------------------------------------
CLUSTER_NAME     := waas-dev
DEV_NAMESPACE    := waas
WORKSPACE_NS     := waas-workspaces
IMAGE_TAG        := dev
DEV_IMAGES       := operator api-server wwt frontend

# Workspace desktop images (waas-images/). Base images are build-time-only
# (FROM layers); only the leaf images are ever scheduled as pods, so only
# those get imported into k3d.
WORKSPACE_BASE_IMAGES := ubuntu-base-vnc ubuntu-base-rdp
WORKSPACE_IMAGES      := ubuntu-xfce ubuntu-firefox dev-ssh

.PHONY: all build test lint generate manifests docs-params frontend-build docker-build \
	dev-up dev-down dev-reset dev-build dev-load dev-deploy dev-reload \
	dev-build-images dev-load-images \
	dev-status dev-logs dev-url tidy

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

generate manifests:
	$(MAKE) -C operator $@

# docs/guacd-parameters.md is generated from the parameter registry so the
# docs can never drift from what the webhook enforces.
docs-params:
	cd operator && go run ./cmd/paramsdoc ../docs/guacd-parameters.md

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
#   make dev-up                 # create cluster + cert-manager (once)
#   make dev-build dev-load dev-deploy
#   make dev-load-images        # workspace desktop images (after dev-deploy: needs the ns)
#   make dev-reload             # after code changes: rebuild, reimport, restart
#   make dev-down                # tear down

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
	sh hack/dev/seed-ssh-secret.sh $(WORKSPACE_NS)
	@$(MAKE) dev-url

# Rebuild, reimport, re-render the chart and restart — the inner loop
# while coding. dev-deploy is NOT optional here: a rollout restart alone
# reuses the manifests of the last helm upgrade, so chart-side changes
# (env wiring, RBAC) silently never reach the cluster — that drift is how
# the guacd netpol lockout shipped despite the fix being committed.
dev-reload: dev-build dev-load dev-deploy
	kubectl -n $(DEV_NAMESPACE) rollout restart \
		deploy/waas-operator deploy/waas-api-server deploy/waas-wwt deploy/waas-frontend

# Build waas-images/ locally (docker build via its own Makefile).
dev-build-images:
	@for img in $(WORKSPACE_BASE_IMAGES) $(WORKSPACE_IMAGES); do \
		echo "==> build $$img"; \
		$(MAKE) -C waas-images build IMAGE=$$img || exit 1; \
	done

# Import the leaf workspace images into k3d and seed the dev catalog
# (WorkspaceImage/WorkspaceTemplate pointed at waas-local:dev tags) plus the
# real WorkspacePolicy seeds (image names match, no dev variant needed).
# Requires the $(WORKSPACE_NS) namespace, i.e. run after dev-deploy.
dev-load-images: dev-build-images
	@for img in $(WORKSPACE_IMAGES); do \
		echo "==> import waas-local/$$img:dev"; \
		k3d image import waas-local/$$img:dev -c $(CLUSTER_NAME) || exit 1; \
	done
	sh hack/dev/seed-ssh-secret.sh $(WORKSPACE_NS)
	kubectl -n $(WORKSPACE_NS) apply \
		-f hack/dev/images-dev.yaml \
		-f hack/dev/templates-dev.yaml \
		-f gitops/governance/policies.yaml
	@echo "==> workspace images + templates loaded into $(WORKSPACE_NS)."

dev-status:
	kubectl -n $(DEV_NAMESPACE) get pods,svc,ingress

# make dev-logs C=api-server
dev-logs:
	kubectl -n $(DEV_NAMESPACE) logs -f deploy/waas-$(C)

dev-url:
	@echo "==> http://waas.127.0.0.1.nip.io:8080  (admin / admin123)"

# Per-protocol connection gate (delivery criterion): creates a workspace
# for each protocol, waits readiness and establishes a REAL guacd session
# through wwt. Run against the k3d dev env after every iteration — a
# change that breaks session establishment must fail here, not at the
# user's desk. See docs/smoke-connections.md.
smoke:
	WAAS_SMOKE_URL=$${WAAS_SMOKE_URL:-http://waas.127.0.0.1.nip.io:8080} \
		go test -count=1 -v -timeout 30m ./test/smoke/
