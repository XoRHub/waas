#!/bin/sh
# Seed the dev-ssh-credentials Secret for the dev-ssh template: an
# ephemeral keypair, generated locally, never committed. In production
# this Secret comes from External Secrets/Vault instead.
#
# The SAME Secret must exist in two namespaces:
#   - the platform namespace (CR namespace): connect-time resolution of
#     credentialsSecretRef always reads it there;
#   - the default workloads namespace ("waas-workspace", the built-in
#     placement fallback): the pods' env secretKeyRef resolves in the
#     POD's namespace, and new dev-ssh workspaces land there by default.
# The workloads copy is cloned from the platform one — two different
# keypairs would break auth (resolver offers key A, pod authorizes key B).
set -eu

PLATFORM_NS="${1:-waas-workspaces}"
WORKLOADS_NS="${2:-waas-workspace}"

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT

if ! kubectl -n "${PLATFORM_NS}" get secret dev-ssh-credentials >/dev/null 2>&1; then
    ssh-keygen -q -t ed25519 -N '' -C waas-dev -f "${TMP}/id"
    kubectl -n "${PLATFORM_NS}" create secret generic dev-ssh-credentials \
        --from-literal=username=user \
        --from-file=private-key="${TMP}/id" \
        --from-file=authorized-keys="${TMP}/id.pub" \
        --from-literal=password=devpassword
    echo "==> secret dev-ssh-credentials created in ${PLATFORM_NS} (ephemeral dev keypair)"
else
    echo "==> secret dev-ssh-credentials already present in ${PLATFORM_NS}"
fi

# The workloads namespace may not exist before the first placed
# workspace; create it so the secret can land (the operator bootstrap is
# create-only and will leave it as is).
kubectl get namespace "${WORKLOADS_NS}" >/dev/null 2>&1 \
    || kubectl create namespace "${WORKLOADS_NS}"

if ! kubectl -n "${WORKLOADS_NS}" get secret dev-ssh-credentials >/dev/null 2>&1; then
    for key in username private-key authorized-keys password; do
        kubectl -n "${PLATFORM_NS}" get secret dev-ssh-credentials \
            -o "jsonpath={.data['${key}']}" | base64 -d > "${TMP}/${key}"
    done
    kubectl -n "${WORKLOADS_NS}" create secret generic dev-ssh-credentials \
        --from-file=username="${TMP}/username" \
        --from-file=private-key="${TMP}/private-key" \
        --from-file=authorized-keys="${TMP}/authorized-keys" \
        --from-file=password="${TMP}/password"
    echo "==> secret dev-ssh-credentials cloned into ${WORKLOADS_NS}"
else
    echo "==> secret dev-ssh-credentials already present in ${WORKLOADS_NS}"
fi
