# syntax=docker/dockerfile:1.7
# waas ubuntu-firefox — XFCE desktop with a managed Firefox.
#
# Firefox comes from the Mozilla APT repository (packages.mozilla.org):
# Ubuntu 24.04 only ships Firefox as a snap, which cannot run in an
# unprivileged container. The repo is pinned by its signing-key
# fingerprint and an apt preference so nothing can shadow it.
ARG BASE_IMAGE
# hadolint ignore=DL3006
FROM ${BASE_IMAGE}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
USER 0

# Renovate-friendly pin of the package version (see renovate.json).
# renovate: datasource=custom.mozilla-apt depName=firefox
ARG FIREFOX_VERSION=*

# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends wget gpg && \
    install -d -m 0755 /etc/apt/keyrings && \
    wget -qO- https://packages.mozilla.org/apt/repo-signing-key.gpg \
        | gpg --dearmor -o /etc/apt/keyrings/packages.mozilla.org.gpg && \
    # --with-colons: machine-readable output, fingerprint unspaced.
    gpg --show-keys --with-colons /etc/apt/keyrings/packages.mozilla.org.gpg \
        | grep -q "^fpr:.*35BAA0B33E9EB396F59CA838C0BA5CE6DC6315A3" && \
    echo "deb [signed-by=/etc/apt/keyrings/packages.mozilla.org.gpg] https://packages.mozilla.org/apt mozilla main" \
        > /etc/apt/sources.list.d/mozilla.list && \
    printf 'Package: firefox*\nPin: origin packages.mozilla.org\nPin-Priority: 1000\n' \
        > /etc/apt/preferences.d/mozilla && \
    apt-get update && \
    if [ "${FIREFOX_VERSION}" = "*" ]; then \
        apt-get install -y --no-install-recommends firefox; \
    else \
        apt-get install -y --no-install-recommends "firefox=${FIREFOX_VERSION}"; \
    fi && \
    apt-get purge -y --auto-remove wget gpg && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /var/cache/* /var/log/* /home/user/.gnupg

# Separate RUN: a trailing "|| true" on the apt chain above would mask
# install failures (the suid sweep may legitimately find nothing).
RUN find / -xdev -perm /6000 -type f -exec chmod a-s {} + 2>/dev/null || true

# Enterprise policies: hardened defaults, applied system-wide and
# non-overridable by the user (Mozilla debs read /etc/firefox/policies).
COPY policies.json /etc/firefox/policies/policies.json

# Auto-start Firefox in the XFCE session (XDG autostart).
COPY waas-app.desktop /etc/xdg/autostart/waas-app.desktop

USER 1000:1000
