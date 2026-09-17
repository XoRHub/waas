# Build from the repository root (the module replaces ../shared and
# ../operator):  docker build -f api-server/Dockerfile .
# Multi-arch, CGO-free (pure-Go SQLite driver), distroless: no shell, health
# checks are HTTP probes on /healthz.
FROM --platform=$BUILDPLATFORM golang:1.27.0@sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146 AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /out/api-server /api-server
USER 65532:65532
ENTRYPOINT ["/api-server"]
