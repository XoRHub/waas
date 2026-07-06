# Local developer loop. CI uses ci/*.sh directly; these targets wrap the
# same scripts so local == CI.
#
#   make build IMAGE=ubuntu-base-vnc
#   make run   IMAGE=ubuntu-base-vnc     # then connect a VNC client to :15901
#   make smoke IMAGE=ubuntu-base-vnc
#   make lint

IMAGE      ?= ubuntu-base-vnc
REGISTRY   ?= waas-local
TAG        := $(REGISTRY)/$(IMAGE):dev

# Map variant -> context + build args (local mirror of the manifests).
ifeq ($(IMAGE),ubuntu-base-vnc)
  CTX := base/ubuntu
  ARGS := --build-arg INSTALL_RDP=0
else ifeq ($(IMAGE),ubuntu-base-rdp)
  CTX := base/ubuntu
  ARGS := --build-arg INSTALL_RDP=1
else ifeq ($(IMAGE),ubuntu-xfce)
  CTX := desktop/xfce
  ARGS := --build-arg BASE_IMAGE=$(REGISTRY)/ubuntu-base-rdp:dev
else ifeq ($(IMAGE),ubuntu-firefox)
  CTX := apps/firefox
  ARGS := --build-arg BASE_IMAGE=$(REGISTRY)/ubuntu-xfce:dev
else ifeq ($(IMAGE),dev-ssh)
  CTX := apps/dev-ssh
  ARGS := --build-arg BASE_IMAGE=$(REGISTRY)/ubuntu-base-vnc:dev
endif

.PHONY: build run smoke lint clean

build:
	docker build $(ARGS) -t $(TAG) $(CTX)

run: build
	docker run --rm -it \
		--read-only --cap-drop ALL --security-opt no-new-privileges \
		--tmpfs /tmp --tmpfs /run --tmpfs /home/user:mode=1777 \
		-p 15901:5901 -p 13389:3389 \
		-e VNC_PW=devpassword -e WAAS_RDP_ENABLED=$(if $(findstring rdp,$(IMAGE)),1,0) \
		$(TAG)

smoke: build
	SMOKE_IMAGE=$(TAG) SMOKE_HOST=localhost SMOKE_VNC=1 sh ci/smoke_test.sh

lint:
	hadolint --failure-threshold warning $$(find . -name Dockerfile)
	shellcheck ci/*.sh base/*/rootfs/usr/local/bin/* $$(find . -path '*/entrypoint.d/*.sh')

clean:
	docker rmi -f $(TAG) 2>/dev/null || true
