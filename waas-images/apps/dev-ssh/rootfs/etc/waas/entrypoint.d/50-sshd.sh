# shellcheck shell=bash
# 50-sshd.sh — render the unprivileged sshd config + supervisor fragment.
# Sourced by waas-entrypoint (strict mode, log/fail/RUNDIR available).
#
# Secrets are runtime-only, matching the base image contract:
#   WAAS_SSH_AUTHORIZED_KEYS       inline authorized_keys content (env from
#                                  a Secret), or
#   WAAS_SSH_AUTHORIZED_KEYS_FILE  path to a mounted Secret file.
#   WAAS_SSH_HOST_KEY_FILE         optional mounted host private key for a
#                                  stable host identity; generated per boot
#                                  otherwise.

if [[ "${WAAS_SSH_ENABLED:-1}" == "1" ]]; then
    SSH_DIR="${RUNDIR}/ssh"
    mkdir -p "${SSH_DIR}"

    # ---- authorized keys: injected, never baked -------------------------
    AUTH_KEYS="${SSH_DIR}/authorized_keys"
    if [[ -n "${WAAS_SSH_AUTHORIZED_KEYS:-}" ]]; then
        printf '%s\n' "${WAAS_SSH_AUTHORIZED_KEYS}" > "${AUTH_KEYS}"
    elif [[ -n "${WAAS_SSH_AUTHORIZED_KEYS_FILE:-}" && -r "${WAAS_SSH_AUTHORIZED_KEYS_FILE}" ]]; then
        cp "${WAAS_SSH_AUTHORIZED_KEYS_FILE}" "${AUTH_KEYS}"
    else
        fail "SSH enabled but no authorized keys: set WAAS_SSH_AUTHORIZED_KEYS(_FILE) from a Secret, or WAAS_SSH_ENABLED=0"
    fi
    chmod 0600 "${AUTH_KEYS}"
    unset WAAS_SSH_AUTHORIZED_KEYS
    export WAAS_SSH_AUTHORIZED_KEYS=''

    # ---- host key: mounted (stable) or per-boot (ephemeral) -------------
    HOST_KEY="${SSH_DIR}/host_ed25519_key"
    if [[ -n "${WAAS_SSH_HOST_KEY_FILE:-}" && -r "${WAAS_SSH_HOST_KEY_FILE}" ]]; then
        cp "${WAAS_SSH_HOST_KEY_FILE}" "${HOST_KEY}"
        chmod 0600 "${HOST_KEY}"
    else
        ssh-keygen -q -t ed25519 -N '' -f "${HOST_KEY}"
    fi

    # ---- sshd_config: unprivileged single-user server -------------------
    # PasswordAuthentication is off AND unenforceable anyway: without root,
    # sshd cannot read /etc/shadow. Public key only, no PAM, no forwarding
    # surprises beyond what the workspace user could do locally anyway.
    cat > "${SSH_DIR}/sshd_config" <<EOF
Port ${WAAS_SSH_PORT:-2222}
HostKey ${HOST_KEY}
PidFile none
UsePAM no
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AuthorizedKeysFile ${AUTH_KEYS}
# The runtime dir is tmpfs with entrypoint-owned perms; the default
# StrictModes walk would reject the non-standard location.
StrictModes no
AllowUsers ${WAAS_USER:-user}
X11Forwarding no
PrintMotd no
ClientAliveInterval 60
ClientAliveCountMax 3
EOF

    cat > "${RUNDIR}/supervisor.d/50-sshd.conf" <<EOF
[program:sshd]
command=/usr/sbin/sshd -D -e -f ${SSH_DIR}/sshd_config
priority=50
autorestart=true
stdout_logfile=/dev/stdout
stdout_logfile_maxbytes=0
redirect_stderr=true
EOF
    log "sshd enabled on port ${WAAS_SSH_PORT:-2222} (publickey only, non-root)"
fi
