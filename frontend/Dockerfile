# Stage 1: Install dependencies and build (always on amd64 — Deno WASM loader
# crashes under QEMU ARM64 emulation, but build output is arch-independent JS)
FROM --platform=linux/amd64 denoland/deno:2.8.1 AS builder

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
#
#   libssl3t64 — REMOVED 2026-06-10. The distroless base caught up to the
#     3.5.6-1~deb13u1 pin (its status.d entry no longer carries 3.5.5),
#     which tripped the stage's base-version assertion — the designed
#     cleanup signal. If a future OpenSSL CVE needs patching ahead of the
#     base again, restore the pinned install + status.d sed from git
#     history (pre 2026-06-10).
#
#   libc6                       → latest in trixie  [unpinned + 7-day gate]
#     Pinning libc6 was the recurring failure source — Debian rolls
#     glibc patches faster than we bump pins, and the missing version
#     fails apt-get with exit 100. Unpinned install + a build-time gate
#     that aborts if the installed version was uploaded <7 days ago
#     (matching the global CLAUDE.md application-layer cooldown, even
#     though Debian's signed security pocket is technically exempt —
#     7 days of community catch-time is cheap defense in depth).
#     The sed substitution that updates the distroless dpkg status.d
#     entry is now dynamic: extracts base + installed versions at
#     build time.
FROM denoland/deno:distroless-2.8.1 AS runtime-base
# debian:trixie (NOT -slim) — the slim variant ships
# /etc/dpkg/dpkg.cfg.d/docker-clean which path-excludes /usr/share/doc/*,
# stripping changelog.Debian.gz files. The 7-day cooldown gate below
# reads libc6's changelog to determine its upload date, so we need the
# changelogs intact. This is an intermediate build stage; the ~30 MB
# size delta does not affect the final runtime image.
FROM debian:trixie AS security-patch

# Pull the base's dpkg status entry first so the consolidated RUN
# below can read it. Single RUN keeps shell variables (version
# strings, upload dates) reachable across the install + assertion +
# sed-substitution steps.
COPY --from=runtime-base /var/lib/dpkg/status.d/libc6 /tmp/libc6-orig

# Consolidated RUN — install + cooldown gate + patch + sed substitution.
# Single RUN so shell variables (LIBC6_VER, BASE_LIBC6_VER, upload-date
# math) reach every step. The /tmp/libc6-orig file was COPYed from
# runtime-base just above. Step-by-step rationale:
#   1. apt-get install — libc6 is unpinned so Debian patch rolls don't
#      break the build.
#   2. 7-day cooldown — abort if apt grabbed a libc6 release uploaded
#      <7 days ago. Source: changelog.Debian.gz upload-date line, which
#      Debian Policy mandates on every binary .deb (--no-install-
#      recommends does not skip it).
#   3. libc6 dynamic-version extraction — no static assertion; if base
#      already matches the patch target, log a NOTE and continue.
#   4. Copy patched binaries into /patch/.
#   5. sed the dpkg status.d entry (dynamic substitution from extracted
#      versions).
RUN set -e && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      libc6 && \
    rm -rf /var/lib/apt/lists/* && \
    LIBC6_VER=$(dpkg-query -W -f='${Version}' libc6) && \
    [ -n "$LIBC6_VER" ] || { echo "ERROR: dpkg-query returned empty libc6 version" >&2; exit 1; } && \
    [ -f /usr/share/doc/libc6/changelog.Debian.gz ] || \
        { echo "ERROR: /usr/share/doc/libc6/changelog.Debian.gz missing — the patch stage's base image is stripping docs (slim variant?). The 7-day cooldown gate needs the changelog to read the upload date." >&2; exit 1; } && \
    LIBC6_DATE=$(zcat /usr/share/doc/libc6/changelog.Debian.gz | grep -m1 '^ -- ' | sed -E 's/.*>  //') && \
    [ -n "$LIBC6_DATE" ] || { echo "ERROR: failed to extract libc6 upload date from changelog" >&2; exit 1; } && \
    LIBC6_TS=$(date -d "$LIBC6_DATE" +%s) && \
    [ -n "$LIBC6_TS" ] || { echo "ERROR: 'date -d' failed on '$LIBC6_DATE'" >&2; exit 1; } && \
    AGE_DAYS=$(( ($(date +%s) - LIBC6_TS) / 86400 )) && \
    if [ "$AGE_DAYS" -lt 7 ]; then \
        echo "ERROR: libc6 $LIBC6_VER uploaded $AGE_DAYS days ago (< 7-day supply-chain cooldown)." >&2; \
        echo "  Wait until $(date -d "@$((LIBC6_TS + 7*86400))" -Iseconds) before rebuilding," >&2; \
        echo "  or temporarily re-introduce a libc6 pin to the previous release." >&2; \
        exit 1; \
    fi && \
    echo "libc6 $LIBC6_VER uploaded $AGE_DAYS days ago — passes 7-day cooldown" && \
    BASE_LIBC6_VER=$(grep '^Version:' /tmp/libc6-orig | awk '{print $2}') && \
    [ -n "$BASE_LIBC6_VER" ] || { echo "ERROR: failed to extract libc6 Version from /tmp/libc6-orig" >&2; exit 1; } && \
    if [ "$BASE_LIBC6_VER" = "$LIBC6_VER" ]; then \
        echo "NOTE: distroless base already ships libc6 $LIBC6_VER — the security-patch stage is a no-op for libc6 and can be dropped"; \
    fi && \
    mkdir -p /patch/usr/lib && \
    dpkg -L libc6 | grep '/usr/lib' | while read f; do \
      [ -f "$f" ] && mkdir -p "/patch$(dirname "$f")" && cp -a "$f" "/patch$f"; \
    done; \
    sed "s|${BASE_LIBC6_VER}|${LIBC6_VER}|g" /tmp/libc6-orig > /patch/libc6-status

# Stage 2: Production runtime (uses target platform — arm64 or amd64)
# Use distroless variant to minimize OS-level CVEs (no shell, no package manager)
FROM denoland/deno:distroless-2.8.1

# Overlay patched libraries (libc6) and updated package metadata
COPY --from=security-patch /patch/usr/lib/ /usr/lib/
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
