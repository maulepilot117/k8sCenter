VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/kubecenter/kubecenter/pkg/version.Version=$(VERSION) \
  -X github.com/kubecenter/kubecenter/pkg/version.Commit=$(COMMIT) \
  -X github.com/kubecenter/kubecenter/pkg/version.BuildDate=$(BUILD_DATE)

.PHONY: dev dev-backend build build-backend test test-backend lint clean docker-build helm-lint helm-template

# Development
dev: dev-backend

dev-backend:
	cd backend && go run ./cmd/kubecenter --config ""

# Build
build: build-backend

build-backend:
	cd backend && go build -ldflags="$(LDFLAGS)" -o bin/kubecenter ./cmd/kubecenter

# Testing
test: test-backend

test-backend:
	cd backend && go test ./... -race -cover -count=1

# Linting
lint:
	cd backend && go vet ./...

# Docker
docker-build:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t kubecenter-backend:$(VERSION) \
	  backend/

# Helm
helm-lint:
	helm lint helm/kubecenter

helm-template:
	helm template kubecenter helm/kubecenter

# Clean
clean:
	rm -rf backend/bin
