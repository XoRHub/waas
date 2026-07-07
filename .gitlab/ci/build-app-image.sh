#!/bin/sh
# build-app-image.sh — build ONE component for ONE architecture and push
# the arch-suffixed tag. Runs natively on a runner of the target arch
# (runner tags amd/arm), so no QEMU is involved anywhere; a merge job
# assembles the two arch tags into the final manifest list.
#
# Inputs (job variables): COMPONENT, ARCH (amd64|arm64), BUILD_CONTEXT,
# DOCKERFILE (optional, defaults to $BUILD_CONTEXT/Dockerfile), APP_TAG
# (computed in before_script: mr-<iid>-<sha> or <sha>).
set -eu

: "${COMPONENT:?}" "${ARCH:?}" "${BUILD_CONTEXT:?}" "${APP_TAG:?}"
IMAGE="${CI_REGISTRY_IMAGE}/${COMPONENT}"
CACHE_REF="${CI_REGISTRY_IMAGE}/cache:${COMPONENT}-${ARCH}"

# Container driver: required for registry cache export. BuildKit pinned
# (renovate keeps it fresh).
docker buildx create --use --name waas \
    --driver-opt image=moby/buildkit:v0.21.0 >/dev/null

# shellcheck disable=SC2046
docker buildx build \
    --platform "linux/${ARCH}" \
    --provenance=false \
    --push \
    --cache-from "type=registry,ref=${CACHE_REF}" \
    --cache-to "type=registry,ref=${CACHE_REF},mode=max" \
    --label "org.opencontainers.image.source=${CI_PROJECT_URL}" \
    --label "org.opencontainers.image.revision=${CI_COMMIT_SHA}" \
    --label "org.opencontainers.image.version=${APP_TAG}" \
    --label "org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -f "${DOCKERFILE:-${BUILD_CONTEXT}/Dockerfile}" \
    -t "${IMAGE}:${APP_TAG}-${ARCH}" \
    "${BUILD_CONTEXT}"

echo "pushed ${IMAGE}:${APP_TAG}-${ARCH}"
