#!/bin/sh
# Seed the dev-ssh-credentials Secret for the dev-ssh template: an
# ephemeral keypair, generated locally, never committed. In production
# this Secret comes from External Secrets/Vault instead.
set -eu

NS="${1:-waas-workspaces}"

if kubectl -n "${NS}" get secret dev-ssh-credentials >/dev/null 2>&1; then
    echo "==> secret dev-ssh-credentials already present in ${NS}"
    exit 0
fi

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
ssh-keygen -q -t ed25519 -N '' -C waas-dev -f "${TMP}/id"

kubectl -n "${NS}" create secret generic dev-ssh-credentials \
    --from-literal=username=user \
    --from-file=private-key="${TMP}/id" \
    --from-file=authorized-keys="${TMP}/id.pub" \
    --from-literal=password=devpassword
echo "==> secret dev-ssh-credentials created in ${NS} (ephemeral dev keypair)"
