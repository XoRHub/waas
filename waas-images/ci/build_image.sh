#!/bin/sh
# build_image.sh — build, smoke-test, scan, push and sign one image.
# Runs in the generated child pipeline (docker:dind), driven by IMG_* /
# SMOKE_* variables emitted by ci/generate_pipeline.py.
#
# Flow: build native-arch and load it -> smoke test against a real
# container -> trivy gate -> (default branch only) multi-arch build+push
# with immutable tags -> cosign sign.
set -eu

: "${IMG_NAME:?}" "${IMG_CONTEXT:?}" "${IMG_VERSION:?}" "${IMG_ARCHS:?}"
REGISTRY="${CI_REGISTRY_IMAGE:?}"
SHA_TAG="${IMG_VERSION}-g${CI_COMMIT_SHORT_SHA:?}"
IMAGE="${REGISTRY}/${IMG_NAME}"
CACHE_REF="${REGISTRY}/cache:${IMG_NAME}"

log() { printf '\n=== %s\n' "$*"; }

# ---------------------------------------------------------------- setup
log "docker login + buildx builder"
docker login -u "${CI_REGISTRY_USER}" -p "${CI_REGISTRY_PASSWORD}" "${CI_REGISTRY}"
# qemu binfmt handlers for the non-native architectures (arm64 on the
# amd64 runners). Pinned; renovate keeps it fresh.
docker run --privileged --rm tonistiigi/binfmt:qemu-v9.2.2 --install all >/dev/null
docker buildx create --use --name waas >/dev/null

BUILD_ARG_FLAGS=""
for kv in ${IMG_BUILD_ARGS:-}; do
    BUILD_ARG_FLAGS="${BUILD_ARG_FLAGS} --build-arg ${kv}"
done
# Derived images consume the parent pushed earlier in this very pipeline
# (IMG_FROM_REF is "<name>:<version>"; the same-commit sha suffix pins it).
if [ -n "${IMG_FROM_REF:-}" ]; then
    BUILD_ARG_FLAGS="${BUILD_ARG_FLAGS} --build-arg BASE_IMAGE=${REGISTRY}/${IMG_FROM_REF}-g${CI_COMMIT_SHORT_SHA}"
fi

# ---------------------------------------------------------------- build
log "build (native arch) ${IMAGE}:${SHA_TAG}"
# shellcheck disable=SC2086
docker buildx build \
    --load \
    --provenance=false \
    --cache-from "type=registry,ref=${CACHE_REF}" \
    ${BUILD_ARG_FLAGS} \
    -t "${IMAGE}:${SHA_TAG}" \
    "${IMG_CONTEXT}"

# ---------------------------------------------------------------- smoke
log "smoke test"
SMOKE_IMAGE="${IMAGE}:${SHA_TAG}" sh ci/smoke_test.sh

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
    "${IMAGE}:${SHA_TAG}"

# ----------------------------------------------------------------- push
# Immutable tags: <version>-g<sha> is always unique and pushed from every
# branch (derived images in this pipeline pull the parent through it).
# The clean <version> tag is published from the default branch only, once,
# and never moved (ArgoCD templates pin it, or better, the digest).
TAG_FLAGS="-t ${IMAGE}:${SHA_TAG}"
if [ "${CI_COMMIT_BRANCH:-}" = "${CI_DEFAULT_BRANCH:-main}" ]; then
    if docker buildx imagetools inspect "${IMAGE}:${IMG_VERSION}" >/dev/null 2>&1; then
        log "WARNING: ${IMG_NAME}:${IMG_VERSION} already published — bump 'version' in ${IMG_CONTEXT}/manifest.yaml; pushing only ${SHA_TAG}"
    else
        TAG_FLAGS="${TAG_FLAGS} -t ${IMAGE}:${IMG_VERSION}"
    fi
fi

log "multi-arch build+push (${IMG_ARCHS})"
# shellcheck disable=SC2086
docker buildx build \
    --platform "${IMG_ARCHS}" \
    --provenance=false \
    --push \
    --cache-from "type=registry,ref=${CACHE_REF}" \
    --cache-to "type=registry,ref=${CACHE_REF},mode=max" \
    ${BUILD_ARG_FLAGS} \
    ${TAG_FLAGS} \
    "${IMG_CONTEXT}"

DIGEST=$(docker buildx imagetools inspect "${IMAGE}:${SHA_TAG}" --format '{{json .Manifest}}' | sed -n 's/.*"digest":"\(sha256:[a-f0-9]*\)".*/\1/p' | head -n1)
log "pushed ${IMAGE}@${DIGEST}"

# ----------------------------------------------------------------- sign
# Optional keyed signing: set COSIGN_PRIVATE_KEY (+ COSIGN_PASSWORD) as
# masked CI variables. Skipped silently when absent.
if [ -n "${COSIGN_PRIVATE_KEY:-}" ] && [ "${CI_COMMIT_BRANCH:-}" = "${CI_DEFAULT_BRANCH:-main}" ]; then
    log "cosign sign"
    docker run --rm \
        -e COSIGN_PRIVATE_KEY -e COSIGN_PASSWORD \
        -e DOCKER_CONFIG=/docker \
        -v "${HOME}/.docker:/docker:ro" \
        ghcr.io/sigstore/cosign/cosign:v2.5.0 \
        sign --yes --key env://COSIGN_PRIVATE_KEY "${IMAGE}@${DIGEST}"
fi
