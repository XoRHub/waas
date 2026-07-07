#!/bin/sh
# merge_image.sh — assemble the per-arch tags pushed by build_image.sh
# into the final manifest list <version>-g<sha>, publish the immutable
# <version> tag from the default branch, and cosign-sign the digest.
# Driven by IMG_* variables emitted by ci/generate_pipeline.py.
set -eu

: "${IMG_NAME:?}" "${IMG_VERSION:?}" "${IMG_ARCHS:?}"
REGISTRY="${CI_REGISTRY_IMAGE:?}"
SHA_TAG="${IMG_VERSION}-g${CI_COMMIT_SHORT_SHA:?}"
IMAGE="${REGISTRY}/${IMG_NAME}"

log() { printf '\n=== %s\n' "$*"; }

log "docker login"
docker login -u "${CI_REGISTRY_USER}" -p "${CI_REGISTRY_PASSWORD}" "${CI_REGISTRY}"

SOURCES=""
for arch in $(echo "${IMG_ARCHS}" | tr ',' ' '); do
    SOURCES="${SOURCES} ${IMAGE}:${SHA_TAG}-${arch#linux/}"
done

# Immutable tags: <version>-g<sha> is always unique and pushed from every
# branch. The clean <version> tag is published from the default branch
# only, once, and never moved (ArgoCD/templates pin it, or better, the
# digest).
TAG_FLAGS="-t ${IMAGE}:${SHA_TAG}"
if [ "${CI_COMMIT_BRANCH:-}" = "${CI_DEFAULT_BRANCH:-main}" ]; then
    if docker buildx imagetools inspect "${IMAGE}:${IMG_VERSION}" >/dev/null 2>&1; then
        log "WARNING: ${IMG_NAME}:${IMG_VERSION} already published — bump 'version' in the manifest; pushing only ${SHA_TAG}"
    else
        TAG_FLAGS="${TAG_FLAGS} -t ${IMAGE}:${IMG_VERSION}"
    fi
fi

log "manifest list ${IMAGE}:${SHA_TAG} <-${SOURCES}"
# shellcheck disable=SC2086
docker buildx imagetools create ${TAG_FLAGS} ${SOURCES}

DIGEST=$(docker buildx imagetools inspect "${IMAGE}:${SHA_TAG}" --format '{{.Manifest.Digest}}')
log "published ${IMAGE}@${DIGEST}"

# ----------------------------------------------------------------- sign
# Optional keyed signing: set COSIGN_PRIVATE_KEY (+ COSIGN_PASSWORD) as
# masked CI variables. Skipped when absent — branch tags are throwaway;
# platform releases have their own mandatory signing gate.
if [ -n "${COSIGN_PRIVATE_KEY:-}" ] && [ "${CI_COMMIT_BRANCH:-}" = "${CI_DEFAULT_BRANCH:-main}" ]; then
    log "cosign sign"
    # Registry auth via flags: the job's ~/.docker is not visible to a
    # container started on the dind daemon, so mounting it cannot work.
    docker run --rm -e COSIGN_PRIVATE_KEY -e COSIGN_PASSWORD \
        ghcr.io/sigstore/cosign/cosign:v2.5.0 sign --yes \
        --key env://COSIGN_PRIVATE_KEY \
        --registry-username "${CI_REGISTRY_USER}" \
        --registry-password "${CI_REGISTRY_PASSWORD}" \
        -a revision="${CI_COMMIT_SHA}" \
        "${IMAGE}@${DIGEST}"
fi
