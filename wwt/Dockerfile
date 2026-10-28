# Build from the repository root (the module replaces ../shared):
#   docker build -f wwt/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:6f1acc898d064a4e9d5773ee574a9af6a9b786306939ac14d7fd806bf62046db AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY wwt/ wwt/
WORKDIR /src/wwt
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/wwt ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /
COPY --from=builder /out/wwt /wwt
USER 65532:65532
ENTRYPOINT ["/wwt"]
