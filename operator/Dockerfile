# Build from the repository root (the module replaces ../shared):
#   docker build -f operator/Dockerfile .
# Multi-arch, CGO-free, distroless. No shell in the final image: health
# checks are HTTP probes on :8081, never `exec`.
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /
COPY --from=builder /out/operator /operator
USER 65532:65532
ENTRYPOINT ["/operator"]
