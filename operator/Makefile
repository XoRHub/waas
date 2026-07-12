# WaaS operator — codegen and build.
# Never edit zz_generated.deepcopy.go or config/ manifests by hand:
# run `make generate manifests` after any change under api/v1alpha1/.

CONTROLLER_GEN_VERSION ?= v0.19.0
CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

# envtest: real apiserver + etcd for test/envtest (CRD schema/CEL,
# webhooks end-to-end, finalizer lifecycle). SETUP_ENVTEST_VERSION is the
# downloader tool; ENVTEST_K8S_VERSION picks the control-plane binaries
# and should track the k8s.io/* minor in go.mod.
SETUP_ENVTEST_VERSION ?= v0.24.1
ENVTEST_K8S_VERSION ?= 1.35.x
SETUP_ENVTEST := go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)

.PHONY: build test test-envtest envtest-assets generate manifests docker-build

build: generate
	go build ./...

test: generate
	go test ./...

# Prints the local path of the envtest control-plane binaries,
# downloading them on first use. CI consumes this to set
# KUBEBUILDER_ASSETS for the whole operator test run.
envtest-assets:
	@$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path

test-envtest:
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test ./test/envtest/ -count=1

generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	# JSON Schema of the shared catalog.yaml wire-format, generated from
	# pkg/catalog so it can never diverge from the parser. Drift-gated by
	# the go-generated-drift CI job (its diff already covers operator/).
	go run ./hack/gen-catalog-schema

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
