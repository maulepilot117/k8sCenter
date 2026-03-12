FROM golang:1.26-alpine AS builder

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
