VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/kubecenter/kubecenter/pkg/version.Version=$(VERSION) \
  -X github.com/kubecenter/kubecenter/pkg/version.Commit=$(COMMIT) \
  -X github.com/kubecenter/kubecenter/pkg/version.BuildDate=$(BUILD_DATE)

.PHONY: dev dev-backend dev-frontend dev-db dev-db-stop \
       build build-backend build-frontend \
       test test-backend test-frontend test-e2e test-e2e-ui \
       lint lint-backend lint-frontend \
       clean docker-build docker-build-backend docker-build-frontend \
       helm-lint helm-template

# Development
dev: dev-backend

dev-db:
	docker compose up -d
	@echo "PostgreSQL: postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable"

dev-db-stop:
	docker compose down

dev-backend:
	cd backend && go run ./cmd/kubecenter --config ""

dev-frontend:
	cd frontend && deno task dev

# Build
build: build-backend build-frontend

build-backend:
	cd backend && go build -ldflags="$(LDFLAGS)" -o bin/kubecenter ./cmd/kubecenter

build-frontend:
	cd frontend && deno task build

# Testing
test: test-backend test-frontend

test-backend:
	cd backend && go test ./... -race -cover -count=1

test-frontend:
	cd frontend && deno task test

test-e2e:
	cd e2e && npx playwright test

test-e2e-ui:
	cd e2e && npx playwright test --ui

# Linting
lint: lint-backend lint-frontend

lint-backend:
	cd backend && go vet ./...

lint-frontend:
	cd frontend && deno lint && deno fmt --check

# Docker
docker-build: docker-build-backend docker-build-frontend

docker-build-backend:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t kubecenter-backend:$(VERSION) \
	  backend/

docker-build-frontend:
	docker build -t kubecenter-frontend:$(VERSION) frontend/

# Helm
helm-lint:
	helm lint helm/kubecenter

helm-template:
	helm template kubecenter helm/kubecenter

check-dashboards:
	@diff -r backend/internal/monitoring/dashboards/ helm/kubecenter/dashboards/ --exclude=embed.go > /dev/null 2>&1 && echo "Dashboard files in sync" || (echo "ERROR: Dashboard JSON files out of sync between backend/internal/monitoring/dashboards/ and helm/kubecenter/dashboards/"; diff -r backend/internal/monitoring/dashboards/ helm/kubecenter/dashboards/ --exclude=embed.go; exit 1)

# Clean
clean:
	rm -rf backend/bin frontend/_fresh
