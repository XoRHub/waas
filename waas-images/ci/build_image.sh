#!/bin/sh
# build_image.sh — build, smoke-test, scan and push ONE image for ONE
# architecture, natively on a runner of that arch (runner tags amd/arm —
# no QEMU anywhere). ci/merge_image.sh then assembles the arch tags into
# the final manifest list. Driven by IMG_* / SMOKE_* variables emitted
# by ci/generate_pipeline.py.
#
# Flow: native build (--load) -> smoke test against a real container ->
# trivy gate -> push of the arch tag <version>-g<sha>-<arch>. Nothing
# reaches the registry (cache aside) before the smoke and scan gates.
set -eu

: "${IMG_NAME:?}" "${IMG_CONTEXT:?}" "${IMG_VERSION:?}" "${IMG_ARCH:?}"
REGISTRY="${CI_REGISTRY_IMAGE:?}"
ARCH="${IMG_ARCH#linux/}"
SHA_TAG="${IMG_VERSION}-g${CI_COMMIT_SHORT_SHA:?}"
ARCH_TAG="${SHA_TAG}-${ARCH}"
IMAGE="${REGISTRY}/${IMG_NAME}"
CACHE_REF="${REGISTRY}/cache:${IMG_NAME}-${ARCH}"

log() { printf '\n=== %s\n' "$*"; }

# ---------------------------------------------------------------- setup
log "docker login + buildx builder"
docker login -u "${CI_REGISTRY_USER}" -p "${CI_REGISTRY_PASSWORD}" "${CI_REGISTRY}"
# Container driver: required for the registry cache export. BuildKit
# pinned; renovate keeps it fresh.
docker buildx create --use --name waas \
    --driver-opt image=moby/buildkit:v0.21.0 >/dev/null

# QEMU fallback: when this job lands on a runner of another arch (the
# generator routes everything to the amd fleet when
# WAAS_IMAGES_BUILD_STRATEGY=qemu), install the binfmt handlers and
# build emulated. Nominal case is native — this block is a no-op.
RUNNER_ARCH=$(uname -m)
case "${RUNNER_ARCH}" in
    x86_64) RUNNER_ARCH=amd64 ;;
    aarch64) RUNNER_ARCH=arm64 ;;
esac
if [ "${ARCH}" != "${RUNNER_ARCH}" ]; then
    log "non-native build (${RUNNER_ARCH} runner -> ${ARCH}): installing QEMU binfmt"
    docker run --privileged --rm tonistiigi/binfmt:qemu-v9.2.2 --install all >/dev/null
fi

BUILD_ARG_FLAGS=""
for kv in ${IMG_BUILD_ARGS:-}; do
    BUILD_ARG_FLAGS="${BUILD_ARG_FLAGS} --build-arg ${kv}"
done
# Derived images consume the parent's SAME-ARCH tag pushed earlier in
# this very pipeline (IMG_FROM_REF is "<name>:<version>"; the same-commit
# sha suffix pins it).
if [ -n "${IMG_FROM_REF:-}" ]; then
    BUILD_ARG_FLAGS="${BUILD_ARG_FLAGS} --build-arg BASE_IMAGE=${REGISTRY}/${IMG_FROM_REF}-g${CI_COMMIT_SHORT_SHA}-${ARCH}"
fi

# ---------------------------------------------------------------- build
log "build (${IMG_ARCH}) ${IMAGE}:${ARCH_TAG}"
# shellcheck disable=SC2086
docker buildx build \
    --platform "${IMG_ARCH}" \
    --load \
    --provenance=false \
    --cache-from "type=registry,ref=${CACHE_REF}" \
    --cache-to "type=registry,ref=${CACHE_REF},mode=max" \
    ${BUILD_ARG_FLAGS} \
    -t "${IMAGE}:${ARCH_TAG}" \
    "${IMG_CONTEXT}"

# ---------------------------------------------------------------- smoke
log "smoke test"
SMOKE_IMAGE="${IMAGE}:${ARCH_TAG}" sh ci/smoke_test.sh

# ----------------------------------------------------------------- scan
log "trivy scan (gate: ${TRIVY_SEVERITY:-HIGH,CRITICAL})"
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v trivy-cache:/root/.cache/trivy \
    aquasec/trivy:0.63.0 image \
    --severity "${TRIVY_SEVERITY:-HIGH,CRITICAL}" \
    --ignore-unfixed="${TRIVY_IGNORE_UNFIXED:-true}" \
    --exit-code "${TRIVY_EXIT_CODE:-1}" \
    --scanners vuln,secret \
    "${IMAGE}:${ARCH_TAG}"

# ----------------------------------------------------------------- push
log "push ${IMAGE}:${ARCH_TAG}"
docker push "${IMAGE}:${ARCH_TAG}"
