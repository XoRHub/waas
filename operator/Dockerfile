# Build from the repository root (the module replaces ../shared):
#   docker build -f operator/Dockerfile .
# Multi-arch, CGO-free, distroless. No shell in the final image: health
# checks are HTTP probes on :8081, never `exec`.
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
WORKDIR /
COPY --from=builder /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
