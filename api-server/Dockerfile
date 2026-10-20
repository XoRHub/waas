# Build from the repository root (the module replaces ../shared and
# ../operator):  docker build -f api-server/Dockerfile .
# Multi-arch, CGO-free (pure-Go SQLite driver), distroless: no shell, health
# checks are HTTP probes on /healthz.
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:957861185dbf29d93668ace41e011d091c6e779953d47f0316ef2cc58e28fe2b AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
WORKDIR /
COPY --from=builder /out/api-server /api-server
USER 65532:65532
ENTRYPOINT ["/api-server"]
