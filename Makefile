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
       helm-lint helm-template check-themes theme-gen \
       mobile-analyze mobile-test

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
test: test-backend test-frontend mobile-test

test-backend:
	cd backend && go test ./... -race -cover -count=1

test-frontend:
	cd frontend && deno task test

# Mobile (Flutter) — analyze + test. Skipped silently when the Flutter SDK
# is not on PATH so backend/frontend devs without Flutter can still run
# the rest of the suite.
mobile-analyze:
	@command -v flutter >/dev/null 2>&1 || { echo "flutter not installed; skipping mobile-analyze"; exit 0; }
	cd mobile && flutter analyze

mobile-test:
	@command -v flutter >/dev/null 2>&1 || { echo "flutter not installed; skipping mobile-test"; exit 0; }
	cd mobile && flutter test

test-e2e:
	cd e2e && npx playwright test

test-e2e-ui:
	cd e2e && npx playwright test --ui

# Theme generator — emits frontend/assets/themes.generated.css and
# mobile/lib/theme/themes.g.dart from shared/themes/*.json. The canonical
# source for both web and mobile colour tokens.
theme-gen:
	deno run --allow-read --allow-write tools/theme-gen/main.ts

# Fail if the committed generated theme files don't match what the generator
# would emit from shared/themes/*.json. Run as part of CI lint.
check-themes:
	deno run --allow-read tools/theme-gen/main.ts --check

# Linting
lint: lint-backend lint-frontend mobile-analyze check-themes

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
