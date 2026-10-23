# Build from the repository root (the module replaces ../shared):
#   docker build -f operator/Dockerfile .
# Multi-arch, CGO-free, distroless. No shell in the final image: health
# checks are HTTP probes on :8081, never `exec`.
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY operator/go.mod operator/go.sum operator/
WORKDIR /src/operator
RUN go mod download
COPY operator/api/ api/
COPY operator/cmd/ cmd/
COPY operator/internal/ internal/
COPY operator/pkg/ pkg/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/operator ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /
COPY --from=builder /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
