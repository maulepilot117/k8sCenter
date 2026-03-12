# Stage 1: Install dependencies and build
FROM denoland/deno:2.7.4 AS builder

WORKDIR /app

# Copy dependency files first for layer caching
COPY deno.json deno.lock* ./
RUN deno install

# Copy source and build
COPY . .
RUN deno task build

# Stage 2: Production runtime
FROM denoland/deno:2.7.4

WORKDIR /app

# Copy built output and config
COPY --from=builder /app/_fresh ./_fresh
COPY --from=builder /app/deno.json ./
COPY --from=builder /app/deno.lock* ./
COPY --from=builder /app/static ./static

# Run as non-root
USER deno

EXPOSE 8000

CMD ["serve", "--allow-net", "--allow-read=.", "--allow-env=BACKEND_URL", "--port", "8000", "_fresh/server.js"]
