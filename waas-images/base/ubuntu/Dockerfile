# syntax=docker/dockerfile:1.7
#
# waas ubuntu base — headless graphical session, VNC always, RDP opt-in.
#
# Display server: TigerVNC's Xvnc. It IS the X server (no Xvfb + x11vnc
# double-buffering), supports RandR resize server-side, and speaks plain
# RFB 3.8 that guacd's libvncclient understands. KasmVNC was rejected
# because its transport is websocket-first and recent releases dropped the
# standard RFB compatibility guacd needs. Wayland headless was rejected
# because there is no mature RDP export path and wayvnc lacks the
# multi-client/auth features we get from TigerVNC for free.
#
# RDP: xrdp running WITHOUT sesman/PAM. xrdp's libvnc backend bridges the
# RDP client straight onto the local Xvnc session (password=ask forwards
# the RDP password as the VNC password). This keeps xrdp fully non-root
# (no PAM, no setuid) and means VNC and RDP always show the SAME session.
# Trade-off (documented in README): no chansrv on the RDP path, so RDP
# clipboard/audio are unavailable — VNC is the recommended protocol for
# Linux workspaces; RDP is a compatibility option.
#
# Supervisor: tini (PID 1, zombie reaping) + supervisord. supervisord runs
# entirely unprivileged, restarts crashed services, and reads config
# fragments the entrypoint renders at runtime (s6-overlay wants to own
# /init as root for its full feature set; a hand-rolled script gives no
# restart/ordering semantics).

ARG UBUNTU_VERSION=24.04
FROM ubuntu:${UBUNTU_VERSION}

# Fixed, overridable identity. /home/user is the contract with the
# operator: the Workspace home PVC is mounted there.
ARG WAAS_USER=user
ARG WAAS_UID=1000
ARG WAAS_GID=1000
# Bake xrdp into the image (1) or ship VNC-only (0). Runtime toggles
# (WAAS_VNC_ENABLED / WAAS_RDP_ENABLED) can only enable what is installed.
ARG INSTALL_RDP=0

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        tigervnc-standalone-server \
        tigervnc-tools \
        supervisor \
        tini \
        openbox \
        xterm \
        xauth \
        x11-xserver-utils \
        dbus-x11 \
        fonts-dejavu-core \
        xfonts-base \
        ca-certificates \
        openssl \
    && if [ "${INSTALL_RDP}" = "1" ]; then \
        apt-get install -y --no-install-recommends xrdp && \
        # xrdp config + keys are rendered at runtime into the (writable)
        # runtime dir; symlink the hardcoded paths there so the rootfs can
        # stay read-only.
        rm -f /etc/xrdp/xrdp.ini /etc/xrdp/rsakeys.ini /etc/xrdp/cert.pem /etc/xrdp/key.pem && \
        ln -s /tmp/waas/xrdp/xrdp.ini    /etc/xrdp/xrdp.ini && \
        ln -s /tmp/waas/xrdp/rsakeys.ini /etc/xrdp/rsakeys.ini && \
        ln -s /tmp/waas/xrdp/cert.pem    /etc/xrdp/cert.pem && \
        ln -s /tmp/waas/xrdp/key.pem     /etc/xrdp/key.pem; \
    fi && \
    # dbus-launch (session bus) wants a machine id; bake one so it works
    # on a read-only rootfs. Identical across containers — acceptable, it
    # is not used as an identity by anything we run.
    dbus-uuidgen > /etc/machine-id && \
    apt-get purge -y --auto-remove && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /var/cache/* /var/log/*

# Non-root user with fixed UID/GID. Ubuntu 24.04 ships a default "ubuntu"
# user squatting UID 1000 — remove it first.
RUN if id ubuntu >/dev/null 2>&1; then userdel -r ubuntu; fi && \
    groupadd -g "${WAAS_GID}" "${WAAS_USER}" && \
    # Home is /home/user regardless of the user name: it is the mount
    # path the operator uses for the persistent home PVC.
    useradd -l -m -u "${WAAS_UID}" -g "${WAAS_GID}" -d /home/user -s /bin/bash "${WAAS_USER}"

# Drop every setuid/setgid bit: nothing we run needs privilege escalation,
# and this neutralises su/passwd/chfn/... even if a shell is reached.
RUN find / -xdev -perm /6000 -type f -exec chmod a-s {} + 2>/dev/null || true

COPY rootfs/ /

RUN chmod 0755 /usr/local/bin/waas-entrypoint /usr/local/bin/waas-session /usr/local/bin/waas-resize

# ---- Runtime contract (consumed by the Workspace operator) ----
# Port 5901: RFB/VNC (guacd protocol "vnc"). TCP-open == session ready.
# Port 3389: RDP via xrdp, only when INSTALL_RDP=1 and WAAS_RDP_ENABLED=1.
EXPOSE 5901 3389

ENV WAAS_USER=${WAAS_USER} \
    HOME=/home/user \
    DISPLAY=:1 \
    WAAS_RUNTIME_DIR=/tmp/waas \
    WAAS_VNC_ENABLED=1 \
    WAAS_RDP_ENABLED=${INSTALL_RDP} \
    VNC_RESOLUTION=1920x1080 \
    VNC_COL_DEPTH=24 \
    WAAS_STARTUP=

USER ${WAAS_UID}:${WAAS_GID}
WORKDIR /home/user

# tini is PID 1 (signal forwarding + zombie reaping); the entrypoint
# renders runtime config then execs supervisord in the foreground.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/waas-entrypoint"]
