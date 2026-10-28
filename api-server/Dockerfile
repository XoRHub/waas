# Build from the repository root (the module replaces ../shared and
# ../operator):  docker build -f api-server/Dockerfile .
# Multi-arch, CGO-free (pure-Go SQLite driver), distroless: no shell, health
# checks are HTTP probes on /healthz.
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /
COPY --from=builder /out/api-server /api-server
USER 65532:65532
ENTRYPOINT ["/api-server"]
