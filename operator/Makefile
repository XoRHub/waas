# WaaS operator — codegen and build.
# Never edit zz_generated.deepcopy.go or config/ manifests by hand:
# run `make generate manifests` after any change under api/v1alpha1/.

CONTROLLER_GEN_VERSION ?= v0.19.0
CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: build test generate manifests docker-build

build: generate
	go build ./...

test: generate
	go test ./...

generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

manifests:
	$(CONTROLLER_GEN) rbac:roleName=waas-operator-role crd webhook paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac \
		output:webhook:artifacts:config=config/webhook
	# The Helm chart ships the CRDs; keep them in sync automatically.
	mkdir -p ../helm/waas/crds
	cp config/crd/bases/*.yaml ../helm/waas/crds/

docker-build:
	docker build -t ghcr.io/xorhub/waas/operator:dev .
