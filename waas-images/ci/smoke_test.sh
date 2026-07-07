#!/bin/sh
# smoke_test.sh — prove the image actually serves its protocols.
#
# The container is deliberately run the hard way — read-only rootfs,
# every capability dropped, no-new-privileges, tmpfs home — so the
# hardening contract is enforced by CI, not just documented. If someone
# adds a service that needs to write outside /tmp|/run|$HOME, this fails.
#
# Checks (retried, because published ports accept connections before the
# in-container service is up — docker-proxy answers first):
#   VNC: read the RFB banner ("RFB 003.008") from port 5901.
#   RDP: send an X.224 Connection Request, expect a TPKT (0x03) reply.
#   SSH: read the protocol banner ("SSH-2.0-...") from port 2222.
set -eu

: "${SMOKE_IMAGE:?}"
HOST="${SMOKE_HOST:-localhost}"   # Kubernetes executor: pod-shared netns with the dind service
NAME="smoke-$$"

ENV_FLAGS="-e VNC_PW=smoketest-$$"
for kv in ${SMOKE_ENV:-}; do
    ENV_FLAGS="${ENV_FLAGS} -e ${kv}"
done

cleanup() {
    docker logs "${NAME}" 2>&1 | tail -n 40 || true
    docker rm -f "${NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# shellcheck disable=SC2086
docker run -d --name "${NAME}" \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --tmpfs /tmp \
    --tmpfs /run \
    --tmpfs /home/user:mode=1777 \
    -p 15901:5901 -p 13389:3389 -p 12222:2222 \
    ${ENV_FLAGS} \
    "${SMOKE_IMAGE}" >/dev/null

# retry "<label>" <fn>: run <fn> up to 60 times, 2s apart, failing early
# if the container died. The probe itself is the readiness check.
retry() {
    label="$1"; shift
    i=0
    while [ $i -lt 60 ]; do
        if "$@"; then return 0; fi
        [ "$(docker inspect -f '{{.State.Running}}' "${NAME}")" = "true" ] \
            || { echo "FAIL: container exited during ${label} check"; return 1; }
        i=$((i + 1)); sleep 2
    done
    echo "FAIL: ${label} not answering after 120s"
    return 1
}

probe_vnc() {
    BANNER=$(nc -w 5 "${HOST}" 15901 </dev/null 2>/dev/null | dd bs=1 count=12 2>/dev/null || true)
    case "${BANNER}" in "RFB "*) return 0 ;; *) return 1 ;; esac
}

probe_rdp() {
    # TPKT + X.224 CR + RDP negotiation request (TLS|RDP security).
    REPLY=$(printf '\003\000\000\023\016\340\000\000\000\000\000\001\000\010\000\013\000\000\000' \
        | nc -w 5 "${HOST}" 13389 2>/dev/null | dd bs=1 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
    [ "${REPLY}" = "03" ]
}

if [ "${SMOKE_VNC:-0}" = "1" ]; then
    retry "VNC" probe_vnc
    echo "OK: VNC answered with '${BANNER}'"
fi

if [ "${SMOKE_RDP:-0}" = "1" ]; then
    retry "RDP" probe_rdp
    echo "OK: RDP answered with a TPKT header"
fi

probe_ssh() {
    SSH_BANNER=$(nc -w 5 "${HOST}" 12222 </dev/null 2>/dev/null | dd bs=1 count=8 2>/dev/null || true)
    case "${SSH_BANNER}" in "SSH-2.0"*) return 0 ;; *) return 1 ;; esac
}

if [ "${SMOKE_SSH:-0}" = "1" ]; then
    retry "SSH" probe_ssh
    echo "OK: SSH answered with '${SSH_BANNER}'"
fi

echo "smoke test passed for ${SMOKE_IMAGE}"
