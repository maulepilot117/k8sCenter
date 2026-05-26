#!/bin/bash
# Build and push k8sCenter container images to GHCR
# Usage: ./scripts/build-push.sh <tag>
# Requires: docker login ghcr.io -u maulepilot117
#
# P3-9 (2026-05-22 audit): the script no longer defaults to \`latest\`.
# Pass an explicit tag — typically a \`v<chartAppVersion>\` release tag
# or a \`sha-<git-short-sha>\` build tag — so the resulting image is
# immutable from the moment it leaves the build host. CI handles
# the floating \`latest\` tag separately and only on the release path.

set -euo pipefail

if [ -z "${1:-}" ]; then
  echo "error: tag argument is required (e.g. v0.42.0 or sha-\$(git rev-parse --short HEAD))" >&2
  echo "       avoid \`latest\` — see P3-9 of the 2026-05-22 security audit" >&2
  exit 2
fi
TAG="$1"
REGISTRY="ghcr.io/maulepilot117"
BACKEND_IMAGE="${REGISTRY}/k8scenter-backend:${TAG}"
FRONTEND_IMAGE="${REGISTRY}/k8scenter-frontend:${TAG}"

echo "Building k8sCenter images (tag: ${TAG})..."

# Build backend (Go, distroless)
echo "==> Building backend..."
docker build \
  --platform linux/arm64 \
  --build-arg VERSION="${TAG}" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t "${BACKEND_IMAGE}" \
  -f backend/Dockerfile \
  backend/

# Build frontend (Deno/Fresh)
echo "==> Building frontend..."
docker build \
  --platform linux/arm64 \
  -t "${FRONTEND_IMAGE}" \
  -f frontend/Dockerfile \
  frontend/

# Push
echo "==> Pushing images..."
docker push "${BACKEND_IMAGE}"
docker push "${FRONTEND_IMAGE}"

echo ""
echo "Images pushed:"
echo "  ${BACKEND_IMAGE}"
echo "  ${FRONTEND_IMAGE}"
echo ""
echo "Deploy with:"
echo "  helm install k8scenter ./helm/kubecenter \\"
echo "    --set backend.image.repository=${REGISTRY}/k8scenter-backend \\"
echo "    --set backend.image.tag=${TAG} \\"
echo "    --set frontend.image.repository=${REGISTRY}/k8scenter-frontend \\"
echo "    --set frontend.image.tag=${TAG} \\"
echo "    --set postgresql.enabled=true \\"
echo "    --set postgresql.auth.password=YOUR_PASSWORD"
