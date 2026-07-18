#!/bin/sh
# Seed non-admin dev accounts so smoke tests exercise GOVERNED users,
# not the bootstrap admin (whose `admins` policy has no limits and who
# bypasses feature gates — testing quotas/policies as admin means
# patching policies first, which this seed makes unnecessary):
#   - dev / dev123   groups=[nymphe:dev] -> "power-user" gitops policy
#                    (workspace + running quotas, overrides, remotes)
#   - user / user123 no groups           -> "default" gitops policy
#                    (restrictive: 1 workspace)
# Idempotent: an already-existing username (409) is left untouched, so
# password or group edits made in the admin console survive redeploys.
# Dev only — production accounts come from OIDC.
set -eu

BASE_URL="${WAAS_DEV_URL:-http://waas.127.0.0.1.nip.io:8080}"
ADMIN_USER="${WAAS_DEV_ADMIN_USER:-admin}"
ADMIN_PASS="${WAAS_DEV_ADMIN_PASS:-admin123}"

# The api-server may still be rolling out (cold bootstrap): retry the
# login for a while, then give up SOFTLY — dev-deploy must not fail on
# a slow pod; `make dev-seed-users` re-runs just this step.
TOKEN=""
i=0
while [ $i -lt 30 ]; do
    TOKEN=$(curl -s -m 5 -X POST "${BASE_URL}/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" \
        | python3 -c 'import sys,json
try: print(json.load(sys.stdin)["data"]["accessToken"])
except Exception: pass' 2>/dev/null) || true
    [ -n "${TOKEN}" ] && break
    i=$((i + 1))
    sleep 3
done
if [ -z "${TOKEN}" ]; then
    echo "==> WARNING: api-server not reachable at ${BASE_URL} — dev users NOT seeded (re-run: make dev-seed-users)" >&2
    exit 0
fi

seed_user() {
    payload="$1"
    username="$2"
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE_URL}/api/v1/users" \
        -H "Authorization: Bearer ${TOKEN}" -H 'Content-Type: application/json' \
        -d "${payload}")
    case "${code}" in
    200 | 201) echo "==> dev user ${username} created" ;;
    409) echo "==> dev user ${username} already exists (left untouched)" ;;
    *)
        echo "==> WARNING: creating dev user ${username} failed (HTTP ${code})" >&2
        return 1
        ;;
    esac
}

seed_user '{"username":"dev","password":"dev123","role":"user","groups":["nymphe:dev"]}' dev
seed_user '{"username":"user","password":"user123","role":"user"}' user
