# syntax=docker/dockerfile:1.7
# waas ubuntu-xfce — lightweight full desktop on top of the base image.
#
# BASE_IMAGE is injected by CI as the exact ref built earlier in the same
# pipeline (registry/ubuntu-base-rdp:<version>-<sha>), so a layer change
# propagates through the whole tree in one pipeline run.
ARG BASE_IMAGE
# hadolint ignore=DL3006
FROM ${BASE_IMAGE}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER 0

# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        xfce4-session \
        xfce4-panel \
        xfce4-settings \
        xfdesktop4 \
        xfwm4 \
        xfce4-terminal \
        thunar \
        adwaita-icon-theme \
    && apt-get purge -y --auto-remove xterm && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /var/cache/* /var/log/*

# Re-assert suid stripping (new packages may bring some back). Separate
# RUN: a trailing "|| true" on the apt chain would mask install failures.
RUN find / -xdev -perm /6000 -type f -exec chmod a-s {} + 2>/dev/null || true

# Session contract with the base entrypoint: WAAS_STARTUP replaces the
# openbox fallback. dbus-launch is prepended by waas-session.
ENV WAAS_STARTUP=startxfce4

USER 1000:1000
