# P2-11 (2026-05-22 audit): pin an exact patch release so stdlib fixes land
# deterministically; a floating minor tag would lag the patch on hosts with
# cached layers. 1.27.2 clears the release Trivy gate's stdlib HIGHs
# (CVE-2026-97031, CVE-2026-78667, CVE-2026-78669).
FROM golang:1.27.2-alpine AS builder

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
