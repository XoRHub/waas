# Build from the repository root (the module replaces ../shared):
#   docker build -f wwt/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared/ shared/
COPY wwt/ wwt/
WORKDIR /src/wwt
RUN go mod download
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/wwt ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087
WORKDIR /
COPY --from=builder /out/wwt /wwt
USER 65532:65532
ENTRYPOINT ["/wwt"]
