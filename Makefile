# WaaS — root Makefile. Each component keeps its own Makefile; this one fans out.

GO_MODULES := shared operator api-server wwt

.PHONY: all build test lint generate manifests frontend-build docker-build tidy

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

frontend-build:
	cd frontend && npm ci && npm run build

docker-build:
	docker build -t ghcr.io/xorhub/waas/operator:dev operator
	docker build -t ghcr.io/xorhub/waas/api-server:dev api-server
	docker build -t ghcr.io/xorhub/waas/wwt:dev wwt
	docker build -t ghcr.io/xorhub/waas/frontend:dev frontend
