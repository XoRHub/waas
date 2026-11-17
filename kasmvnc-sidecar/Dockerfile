# Build from the repository root (the module replaces ../shared):
#   docker build -f kasmvnc-sidecar/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY kasmvnc-sidecar/ kasmvnc-sidecar/
WORKDIR /src/kasmvnc-sidecar
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/kasmvnc-sidecar ./cmd

# PulseAudio for the audio routes (ADR 0004): the daemon, the three
# modules the sidecar loads and parec, with the shared libraries they
# resolve — copied into the same base below instead of installing the
# package there, because Debian's pulseaudio drags gstreamer, ffmpeg and
# the ALSA plugins (≈190 MB) that nothing here loads. Alpine's build is
# not an option: its module-pipe-source aborts on every FIFO read
# (pa_frame_aligned assertion, pulseaudio 17.0-r5 on 3.23).
# One upgraded layer shared by pulse and the runtime: Debian security
# fixes the digest does not carry yet, and the libraries copied from pulse
# (libc among them) always match the runtime's own.
FROM debian:13-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS base
RUN apt-get update \
 && apt-get upgrade -y \
 && rm -rf /var/lib/apt/lists/*

FROM base AS pulse
# Unpinned on purpose: the stage is a throwaway source of binaries and
# Debian's point releases retire the old version, which a pin would turn
# into a build failure at every base digest bump.
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends pulseaudio pulseaudio-utils \
 && rm -rf /var/lib/apt/lists/*
# Debian is merged-usr (/lib -> usr/lib): resolve each directory to its
# real path so COPY below never writes through the symlink.
RUN set -eu; \
    take() { d=$(readlink -f "$(dirname "$1")"); mkdir -p "/out$d"; cp -L "$1" "/out$d/"; echo "$1" >> /tmp/taken.txt; }; \
    for d in /usr/lib/pulse-*/modules; do mods="$d"; done; \
    for f in /usr/bin/pulseaudio /usr/bin/parec \
             "$mods/module-native-protocol-unix.so" "$mods/libprotocol-native.so" \
             "$mods/module-null-sink.so" "$mods/module-pipe-source.so"; do \
        take "$f"; \
        ldd "$f" > /tmp/ldd.txt; \
        awk '$2 == "=>" && $3 ~ /^\// { print $3 }' /tmp/ldd.txt > /tmp/libs.txt; \
        while read -r lib; do take "$lib"; done < /tmp/libs.txt; \
    done

# The copied binaries are (L)GPL: redistributing them needs their licence
# text and a pointer to the source (GPL-2.0 s.1 and s.3). Resolve each
# copied file to its owning package (dpkg only knows the real /usr/lib
# path, not the merged-usr /lib symlink), then ship that package's
# copyright file, a manifest (binary package=version plus its source
# package and version) and a NOTICE.
RUN set -eu; \
    doc=/out/usr/share/doc/waas-kasmvnc-sidecar; mkdir -p "$doc"; \
    sort -u /tmp/taken.txt > /tmp/files.txt; \
    while read -r f; do dpkg -S "$(readlink -f "$f")"; done < /tmp/files.txt > /tmp/owners.txt; \
    sed -e 's/: .*//' -e 's/, /\n/g' /tmp/owners.txt > /tmp/owners-pkgs.txt; \
    sort -u /tmp/owners-pkgs.txt > /tmp/pkgs.txt; \
    while read -r pkg; do \
        name=${pkg%%:*}; mkdir -p "/out/usr/share/doc/$name"; \
        cp -L "/usr/share/doc/$name/copyright" "/out/usr/share/doc/$name/"; \
        dpkg-query -W -f '${Package}=${Version} (source: ${source:Package} ${source:Version})\n' "$name" >> "$doc/packages.txt"; \
    done < /tmp/pkgs.txt; \
    sort -u -o "$doc/packages.txt" "$doc/packages.txt"; \
    printf '%s\n' \
      'The PulseAudio binaries and shared libraries in this image are unmodified' \
      'Debian packages, listed in packages.txt as package=version (source: source' \
      'package and version). Their licences are in' \
      '/usr/share/doc/<package>/copyright. The corresponding source of those exact' \
      'versions is at https://snapshot.debian.org/ (or: apt-get source' \
      '<source package>=<source version>).' > "$doc/NOTICE"

# The operator sets runAsUser/runAsGroup to the desktop's uid/gid
# (kasmweb: 1000) so uploads land owned by the session user and the
# PulseAudio socket is reachable from the desktop.
FROM base
WORKDIR /
COPY --from=pulse /out /
COPY --from=builder /out/kasmvnc-sidecar /kasmvnc-sidecar
USER 65532:65532
ENTRYPOINT ["/kasmvnc-sidecar"]
