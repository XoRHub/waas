# Build from the repository root (the module replaces ../shared):
#   docker build -f kasm-agent/Dockerfile .
# Lot 4 (audio) will add PulseAudio tooling to this same sidecar and
# switch the base image then — keep this one plain.
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY kasm-agent/ kasm-agent/
WORKDIR /src/kasm-agent
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/kasm-agent ./cmd

# The operator sets runAsUser/runAsGroup to the desktop's uid/gid
# (kasmweb: 1000) so uploads land owned by the session user.
FROM gcr.io/distroless/static:nonroot@sha256:d62067879de890a1def9da0e7766b7d20810a5e5555cef8506b752437d7a3e88
WORKDIR /
COPY --from=builder /out/kasm-agent /kasm-agent
USER 65532:65532
ENTRYPOINT ["/kasm-agent"]
