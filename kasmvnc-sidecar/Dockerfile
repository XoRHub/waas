# Build from the repository root (the module replaces ../shared):
#   docker build -f kasmvnc-sidecar/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS builder
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
FROM debian:13-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad AS pulse
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
    take() { d=$(readlink -f "$(dirname "$1")"); mkdir -p "/out$d"; cp -L "$1" "/out$d/"; }; \
    for d in /usr/lib/pulse-*/modules; do mods="$d"; done; \
    for f in /usr/bin/pulseaudio /usr/bin/parec \
             "$mods/module-native-protocol-unix.so" "$mods/libprotocol-native.so" \
             "$mods/module-null-sink.so" "$mods/module-pipe-source.so"; do \
        take "$f"; \
        ldd "$f" > /tmp/ldd.txt; \
        awk '$2 == "=>" && $3 ~ /^\// { print $3 }' /tmp/ldd.txt > /tmp/libs.txt; \
        while read -r lib; do take "$lib"; done < /tmp/libs.txt; \
    done

# The operator sets runAsUser/runAsGroup to the desktop's uid/gid
# (kasmweb: 1000) so uploads land owned by the session user and the
# PulseAudio socket is reachable from the desktop.
FROM debian:13-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad
WORKDIR /
COPY --from=pulse /out /
COPY --from=builder /out/kasmvnc-sidecar /kasmvnc-sidecar
USER 65532:65532
ENTRYPOINT ["/kasmvnc-sidecar"]
