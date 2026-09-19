# Build from the repository root (the module replaces ../shared):
#   docker build -f wwt/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.1@sha256:1cfcdb11f37fce9429f617100f39e0251748bbaba454bd431275155701765058 AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY wwt/ wwt/
WORKDIR /src/wwt
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/wwt ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /out/wwt /wwt
USER 65532:65532
ENTRYPOINT ["/wwt"]
