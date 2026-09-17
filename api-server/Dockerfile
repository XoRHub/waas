# Build from the repository root (the module replaces ../shared and
# ../operator):  docker build -f api-server/Dockerfile .
# Multi-arch, CGO-free (pure-Go SQLite driver), distroless: no shell, health
# checks are HTTP probes on /healthz.
FROM --platform=$BUILDPLATFORM golang:1.27.1@sha256:f44f6e88636cfb311f9ebace870ded69d943f227bb3cb27d32ffd84ea18c43ea AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY operator/go.mod operator/go.sum operator/
COPY operator/api/ operator/api/
COPY operator/pkg/ operator/pkg/
COPY api-server/ api-server/
WORKDIR /src/api-server
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/api-server ./cmd/api-server

FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7
WORKDIR /
COPY --from=builder /out/api-server /api-server
USER 65532:65532
ENTRYPOINT ["/api-server"]
