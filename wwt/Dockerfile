# Build from the repository root (the module replaces ../shared):
#   docker build -f wwt/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY wwt/ wwt/
WORKDIR /src/wwt
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/wwt ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/wwt /wwt
USER 65532:65532
ENTRYPOINT ["/wwt"]
