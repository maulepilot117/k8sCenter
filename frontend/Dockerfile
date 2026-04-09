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

# Stage 2: Production runtime (uses target platform — arm64 or amd64)
# Use distroless variant to minimize OS-level CVEs (no shell, no package manager)
FROM denoland/deno:distroless-2.7.11

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
