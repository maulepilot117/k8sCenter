# Stage 1: Install dependencies and build (always on amd64 — Deno WASM loader
# crashes under QEMU ARM64 emulation, but build output is arch-independent JS)
FROM --platform=linux/amd64 denoland/deno:2.7.11 AS builder

WORKDIR /app

# Copy dependency files first for layer caching
COPY deno.json deno.lock* package.json* ./
RUN deno install

# Copy source and build
COPY . .
RUN deno task build

# Cache all runtime dependencies (ensures no network fetches at startup)
RUN deno cache _fresh/server.js || true

# Remove esbuild native binaries — build-only tool, not needed at runtime.
# These contain an embedded Go binary that triggers Go stdlib CVEs in Trivy.
RUN find /app/node_modules -path '*/esbuild/bin/esbuild' -delete 2>/dev/null; \
    find /app/node_modules -path '*/@esbuild/*/bin/esbuild' -delete 2>/dev/null; \
    find /deno-dir -path '*/esbuild/bin/esbuild' -delete 2>/dev/null; \
    find /deno-dir -path '*/@esbuild/*/bin/esbuild' -delete 2>/dev/null; \
    true

# Security patch stage: overlay patched OS libraries onto distroless so Trivy
# clears the open CVEs. Uses target platform so library arch paths match.
#   libssl3t64 3.5.5-1~deb13u1 → 3.5.5-1~deb13u2
#     CVE-2026-31790, CVE-2026-2673, CVE-2026-28387, CVE-2026-28388,
#     CVE-2026-28389, CVE-2026-28390, CVE-2026-31789
#   libc6 2.41-12+deb13u2 → 2.41-12+deb13u3
#     CVE-2026-4046, CVE-2026-4437, CVE-2026-4438
FROM denoland/deno:distroless-2.7.11 AS runtime-base
FROM debian:trixie-slim AS security-patch
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      libssl3t64=3.5.5-1~deb13u2 \
      libc6=2.41-12+deb13u3 && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /patch/usr/lib && \
    for pkg in libssl3t64 libc6; do \
      dpkg -L "$pkg" | grep '/usr/lib' | while read f; do \
        [ -f "$f" ] && mkdir -p "/patch$(dirname "$f")" && cp -a "$f" "/patch$f"; \
      done; \
    done
# Update the distroless dpkg status.d entries so Trivy sees the patched versions.
# TEMPORARY: remove this entire security-patch stage once the distroless base
# ships with libssl3t64 >= 3.5.5-1~deb13u2 AND libc6 >= 2.41-12+deb13u3.
COPY --from=runtime-base /var/lib/dpkg/status.d/libssl3t64 /tmp/libssl3t64-orig
COPY --from=runtime-base /var/lib/dpkg/status.d/libc6 /tmp/libc6-orig
RUN grep -q '3\.5\.5-1~deb13u1' /tmp/libssl3t64-orig || \
    (echo "ERROR: base image no longer contains libssl3t64 3.5.5-1~deb13u1 — drop libssl3t64 from the security-patch stage" && exit 1)
RUN grep -qF '2.41-12+deb13u2' /tmp/libc6-orig || \
    (echo "ERROR: base image no longer contains libc6 2.41-12+deb13u2 — drop libc6 from the security-patch stage" && exit 1)
RUN sed 's/3\.5\.5-1~deb13u1/3.5.5-1~deb13u2/g' /tmp/libssl3t64-orig > /patch/libssl3t64-status && \
    sed 's/2\.41-12+deb13u2/2.41-12+deb13u3/g' /tmp/libc6-orig > /patch/libc6-status

# Stage 2: Production runtime (uses target platform — arm64 or amd64)
# Use distroless variant to minimize OS-level CVEs (no shell, no package manager)
FROM denoland/deno:distroless-2.7.11

# Overlay patched libraries (libssl3t64, libc6) and updated package metadata
COPY --from=security-patch /patch/usr/lib/ /usr/lib/
COPY --from=security-patch /patch/libssl3t64-status /var/lib/dpkg/status.d/libssl3t64
COPY --from=security-patch /patch/libc6-status /var/lib/dpkg/status.d/libc6

WORKDIR /app

# Copy built output, config, and cached dependencies
COPY --from=builder /app/_fresh ./_fresh
COPY --from=builder /app/deno.json ./
COPY --from=builder /app/deno.lock* ./
COPY --from=builder /app/package.json* ./
COPY --from=builder /app/static ./static
COPY --from=builder /app/node_modules ./node_modules

# Copy Deno cache from builder (includes ESM modules like Monaco)
COPY --from=builder /deno-dir /deno-dir

EXPOSE 8000

CMD ["serve", "--allow-net", "--allow-read=.", "--allow-env=BACKEND_URL,LOG,PORT,HOSTNAME", "--port", "8000", "_fresh/server.js"]
