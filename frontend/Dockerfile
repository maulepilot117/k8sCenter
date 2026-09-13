# Stage 1: Install dependencies and build (always on amd64 — Deno WASM loader
# crashes under QEMU ARM64 emulation, but build output is arch-independent JS)
FROM --platform=linux/amd64 denoland/deno:2.9.6 AS builder

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
#   libssl3t64 3.5.6-1~deb13u1 → 3.5.6-1~deb13u2   [pinned]
#     Restored 2026-06-10, hours after being removed: the base caught up
#     to deb13u1, then Debian rolled deb13u2 in trixie-security fixing
#     CVE-2026-45447 (HIGH) + 14 lower CVEs, and the Trivy CRITICAL,HIGH
#     gate failed on the now-stale base. Pin is bumped by hand whenever
#     Debian rolls a new patch; the base-version assertion below fires
#     when distroless catches up (= update or drop the patch). Debian's
#     signed security pocket is exempt from the 7-day supply-chain
#     cooldown (it IS the defense), so a fresh deb13uN pin is fine.
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
FROM denoland/deno:distroless-2.9.6 AS runtime-base
# debian:trixie (NOT -slim) — the slim variant ships
# /etc/dpkg/dpkg.cfg.d/docker-clean which path-excludes /usr/share/doc/*,
# stripping changelog.Debian.gz files. The 7-day cooldown gate below
# reads libc6's changelog to determine its upload date, so we need the
# changelogs intact. This is an intermediate build stage; the ~30 MB
# size delta does not affect the final runtime image.
FROM debian:trixie AS security-patch

# Pull the base's dpkg status entries first so the consolidated RUN
# below can read them. Single RUN keeps shell variables (version
# strings, upload dates) reachable across the install + assertion +
# sed-substitution steps.
COPY --from=runtime-base /var/lib/dpkg/status.d/libssl3t64 /tmp/libssl3t64-orig
COPY --from=runtime-base /var/lib/dpkg/status.d/libc6 /tmp/libc6-orig

# Consolidated RUN — install + cooldown gate + conditional patch, applied
# uniformly to every package in the overlay.
#
# Nothing is version-pinned here, deliberately. A static pin plus a static
# sed of the recorded version is what broke this stage before: the pin named
# libssl3t64 3.5.6-1~deb13u2, the distroless base moved on to 3.5.7-1~deb13u2,
# and the guard that noticed correctly refused to build rather than let the
# sed record a downgrade. A pin only holds until the base catches up, and the
# base catching up is the normal case, not an exception.
#
# So each package is handled the way libc6 always was: install whatever
# Debian currently ships, compare it against what the base already has, and
# patch only when Debian is genuinely ahead. When the base is level or ahead
# the overlay is a no-op and the base's own status entry is passed through
# unchanged, so the final image never records a version it does not have.
#
# Per package:
#   1. Install unpinned, so a Debian patch roll cannot break the build.
#   2. 7-day cooldown gate — abort if the installed version was uploaded less
#      than 7 days ago, matching the global application-layer cooldown.
#      Debian's signed security pocket is technically exempt, but 7 days of
#      community catch-time is cheap defense in depth. Source: the upload
#      date in changelog.Debian.gz, which Debian Policy mandates on every
#      binary .deb (--no-install-recommends does not skip it).
#   3. Compare installed against the base's recorded version with
#      dpkg --compare-versions, which understands epochs and ~-suffixes that
#      a string comparison would get wrong.
#   4. Ahead  -> copy the shared objects into /patch and rewrite the status
#      entry to the installed version.
#      Level or behind -> pass the base's status entry through untouched and
#      log a NOTE, so an operator reading the build log can see the stage is
#      currently doing nothing for that package.
RUN set -e && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      libssl3t64 \
      libc6 && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /patch/usr/lib && \
    for pkg in libssl3t64 libc6; do \
      INSTALLED_VER=$(dpkg-query -W -f='${Version}' "$pkg") && \
      [ -n "$INSTALLED_VER" ] || { echo "ERROR: dpkg-query returned empty version for $pkg" >&2; exit 1; } && \
      [ -f "/usr/share/doc/$pkg/changelog.Debian.gz" ] || \
          { echo "ERROR: /usr/share/doc/$pkg/changelog.Debian.gz missing — the patch stage's base image is stripping docs (slim variant?). The 7-day cooldown gate needs the changelog to read the upload date." >&2; exit 1; } && \
      UPLOAD_DATE=$(zcat "/usr/share/doc/$pkg/changelog.Debian.gz" | grep -m1 '^ -- ' | sed -E 's/.*>  //') && \
      [ -n "$UPLOAD_DATE" ] || { echo "ERROR: failed to extract $pkg upload date from changelog" >&2; exit 1; } && \
      UPLOAD_TS=$(date -d "$UPLOAD_DATE" +%s) && \
      [ -n "$UPLOAD_TS" ] || { echo "ERROR: 'date -d' failed on '$UPLOAD_DATE'" >&2; exit 1; } && \
      AGE_DAYS=$(( ($(date +%s) - UPLOAD_TS) / 86400 )) && \
      if [ "$AGE_DAYS" -lt 7 ]; then \
          echo "ERROR: $pkg $INSTALLED_VER uploaded $AGE_DAYS days ago (< 7-day supply-chain cooldown)." >&2; \
          echo "  Wait until $(date -d "@$((UPLOAD_TS + 7*86400))" -Iseconds) before rebuilding," >&2; \
          echo "  or temporarily pin $pkg to the previous release." >&2; \
          exit 1; \
      fi && \
      echo "$pkg $INSTALLED_VER uploaded $AGE_DAYS days ago — passes 7-day cooldown" && \
      BASE_VER=$(grep '^Version:' "/tmp/$pkg-orig" | awk '{print $2}') && \
      [ -n "$BASE_VER" ] || { echo "ERROR: failed to extract $pkg Version from /tmp/$pkg-orig" >&2; exit 1; } && \
      if dpkg --compare-versions "$INSTALLED_VER" gt "$BASE_VER"; then \
        echo "PATCH: $pkg $BASE_VER (base) -> $INSTALLED_VER (Debian)"; \
        for f in $(dpkg -L "$pkg" | grep '/usr/lib'); do \
          if [ -f "$f" ]; then mkdir -p "/patch$(dirname "$f")" && cp -a "$f" "/patch$f"; fi; \
        done; \
        sed "s|${BASE_VER}|${INSTALLED_VER}|g" "/tmp/$pkg-orig" > "/patch/$pkg-status"; \
      else \
        echo "NOTE: distroless base ships $pkg $BASE_VER, Debian offers $INSTALLED_VER — overlay is a no-op for $pkg"; \
        cp "/tmp/$pkg-orig" "/patch/$pkg-status"; \
      fi; \
    done

# Stage 2: Production runtime (uses target platform — arm64 or amd64)
# Use distroless variant to minimize OS-level CVEs (no shell, no package manager)
FROM denoland/deno:distroless-2.9.6

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
