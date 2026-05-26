# P2-11 (2026-05-22 audit): pin to 1.26.3-alpine to clear the reachable
# govulncheck stdlib advisories (GO-2026-4918 et al.). The floating
# "1.26" tag would lag the patch on hosts with cached layers.
FROM golang:1.26.3-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w \
      -X github.com/kubecenter/kubecenter/pkg/version.Version=${VERSION} \
      -X github.com/kubecenter/kubecenter/pkg/version.Commit=${COMMIT} \
      -X github.com/kubecenter/kubecenter/pkg/version.BuildDate=${BUILD_DATE}" \
    -o kubecenter ./cmd/kubecenter

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/kubecenter /kubecenter

USER 65534:65534

EXPOSE 8080

ENTRYPOINT ["/kubecenter"]
