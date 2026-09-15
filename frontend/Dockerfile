# syntax=docker/dockerfile:1

# ─────────────────────────────────────────────────────────────────────────────
# Stage 1: install, build, and compile the launcher
#
# Runs on the BUILD platform, not the target: everything expensive here
# (bun install, astro build) produces architecture-independent JavaScript, and
# the one architecture-dependent artifact -- the compiled launcher -- is
# cross-compiled by Bun in a single flag. That is strictly better than the
# Deno image's `--platform=linux/amd64` pin, which existed because Deno's WASM
# loader crashed under QEMU ARM64: there is no emulation here to crash.
# ─────────────────────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM oven/bun:1.4.2-debian@sha256:4f6e31d1a54d6a3dd312daef655fc998101b5043d52e12592ac293ef04b9bc73 AS builder

ARG TARGETARCH

WORKDIR /app

# Dependency files first, for layer caching. bunfig.toml carries the
# machine-enforced 7-day supply-chain cooldown (KTD7); it must be present
# before `bun install` or the gate silently does not apply.
COPY package.json bun.lock bunfig.toml ./
RUN bun install --frozen-lockfile

# Source, then the Astro build: dist/client/ (browser assets) and
# dist/server/entry.mjs (the middleware-mode SSR handler).
COPY . .
RUN bun run build

# Pre-bundle the SSR entry in place so its externals are inlined (KTD6).
#
# Without this the launcher cannot load it at all: the emitted entry.mjs
# imports bare specifiers such as `preact` that resolve only against
# node_modules, and node_modules is exactly what must not reach the runtime
# layer. `--target=bun` inlines them. Built to a temp path first because
# `bun build` will not write over its own input.
RUN bun build dist/server/entry.mjs \
      --target=bun \
      --outfile /tmp/entry.bundled.mjs \
 && mv /tmp/entry.bundled.mjs dist/server/entry.mjs

# Compile the launcher.
#
# This is the half of the server that CAN be compiled. server/prod.ts and
# everything it imports statically (dispatch, static, ws-proxy, headers, ws)
# are embedded in the binary; dist/server/entry.mjs deliberately is not,
# because @astrojs/node resolves its own asset directory by walking
# import.meta.url for a literal `server` path segment and a compiled binary's
# virtual path has none. The launcher imports it from disk instead (KTD6).
#
# BUN_COMPILE_TARGET defaults to the baseline x64 build, which runs on any
# x86-64 CPU. The non-baseline `bun-linux-x64` build is faster but requires
# AVX2 (Intel Haswell, 2013, and later) and aborts with "Illegal instruction"
# on anything older. k8sCenter ships as a Helm chart onto clusters whose
# hardware we do not choose, so the default is the one that always starts; an
# operator who knows their nodes can pass --build-arg to take the faster path.
# arm64 has no baseline variant.
ARG BUN_COMPILE_TARGET=""
RUN set -eux; \
    target="${BUN_COMPILE_TARGET}"; \
    if [ -z "$target" ]; then \
      case "$TARGETARCH" in \
        amd64) target="bun-linux-x64-baseline" ;; \
        arm64) target="bun-linux-arm64" ;; \
        *) echo "ERROR: unsupported TARGETARCH '$TARGETARCH'" >&2; exit 1 ;; \
      esac; \
    fi; \
    echo "compiling launcher for $target"; \
    bun build --compile --target="$target" server/prod.ts --outfile k8scenter-frontend

# ─────────────────────────────────────────────────────────────────────────────
# Stage 2: security patch overlay
#
# Carried over from the Deno image unchanged in shape, re-anchored on the new
# base. Nothing is version-pinned: a static pin plus a static sed of the
# recorded version is what broke this stage twice before (PRs #426, #428). A
# pin only holds until the base catches up, and the base catching up is the
# normal case, not an exception.
#
# Per package:
#   1. Install unpinned, so a Debian patch roll cannot break the build.
#   2. 7-day cooldown gate -- abort if the installed version was uploaded less
#      than 7 days ago, matching the global application-layer cooldown.
#      Debian's signed security pocket is technically exempt, but 7 days of
#      community catch-time is cheap defense in depth. Source: the upload date
#      in changelog.Debian.gz, which Debian Policy mandates on every binary
#      .deb (--no-install-recommends does not skip it).
#   3. Compare installed against the base's recorded version with
#      dpkg --compare-versions, which understands epochs and ~-suffixes that a
#      string comparison would get wrong.
#   4. Ahead -> copy the shared objects into /patch and rewrite the status
#      entry. Level or behind -> pass the base's entry through untouched and
#      log a NOTE, so an operator reading the build log can see the stage is
#      currently doing nothing for that package.
#
# This is why the runtime base is not a free choice (KTD6): distroless/cc is
# Debian-derived, so it has dpkg status.d entries and changelog.Debian.gz to
# compare against. A non-Debian base would mean rebuilding this gate from
# scratch. distroless/base is not an option either -- it ships no libstdc++,
# and Bun will not start without it.
FROM gcr.io/distroless/cc-debian13@sha256:9b615fff20e1a4fad29c2b30562580b212c7dd5e2225236735cca0070ed11c78 AS runtime-base

# debian:trixie (NOT -slim) -- the slim variant ships
# /etc/dpkg/dpkg.cfg.d/docker-clean which path-excludes /usr/share/doc/*,
# stripping the changelog.Debian.gz files the cooldown gate reads. This is an
# intermediate build stage; the ~30 MB delta never reaches the final image.
FROM debian:trixie AS security-patch

COPY --from=runtime-base /var/lib/dpkg/status.d/libssl3t64 /tmp/libssl3t64-orig
COPY --from=runtime-base /var/lib/dpkg/status.d/libc6 /tmp/libc6-orig

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

# ─────────────────────────────────────────────────────────────────────────────
# Stage 3: production runtime
#
# gcr.io/distroless/cc-debian13, pinned by immutable digest rather than tag
# (R12). A tag-pinned base means rebuilding the same commit can resolve
# different runtime bits, and an upstream tag compromise would bypass the
# review every other dependency in this repo goes through. Digest bumps are
# reviewed like any other pin and must clear the same 7-day cooldown -- the
# digest below was published 2026-08-27.
#
# What the runtime layer contains: the compiled launcher, the pre-bundled SSR
# entry, the browser assets, and bun.lock. No package manager, no shell, no
# node_modules, no Bun toolchain. That is the same property the Deno image
# achieved by deleting /app/node_modules and /deno-dir/npm after building, and
# for the same reason -- shipping a toolchain the running container cannot
# load still hands Trivy a set of packages to raise release-blocking CVEs on.
# ─────────────────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/cc-debian13@sha256:9b615fff20e1a4fad29c2b30562580b212c7dd5e2225236735cca0070ed11c78

# Overlay patched libraries and their updated package metadata.
COPY --from=security-patch /patch/usr/lib/ /usr/lib/
COPY --from=security-patch /patch/libssl3t64-status /var/lib/dpkg/status.d/libssl3t64
COPY --from=security-patch /patch/libc6-status /var/lib/dpkg/status.d/libc6

WORKDIR /app

# The compiled launcher, and the two things it loads from disk rather than
# from inside itself: the pre-bundled SSR entry (see KTD6 above) and the
# browser assets, which are served dynamically by request path and so are a
# copy, not an embed.
COPY --from=builder /app/k8scenter-frontend ./k8scenter-frontend
COPY --from=builder /app/dist/server/entry.mjs ./dist/server/entry.mjs
COPY --from=builder /app/dist/client ./dist/client

# bun.lock is the manifest Trivy reads to enumerate application packages,
# mirroring the deno.lock copy it replaces. Without it the scan reports an
# empty package set and the release gate passes while proving nothing. Bun's
# text lockfile is what Trivy parses; the binary bun.lockb is not, which is
# why CI asserts it is never committed.
COPY --from=builder /app/bun.lock ./bun.lock

# Numeric UID, matching runAsUser/runAsGroup 65534 in
# helm/kubecenter/templates/deployment-frontend.yaml. Numeric rather than a
# name so it does not depend on an /etc/passwd entry the base may or may not
# carry, and explicit rather than inherited so a base image change cannot
# silently move it away from the chart's value.
USER 65534:65534

# Where the launcher finds dist/. A compiled Bun binary's import.meta.url
# points inside the binary, so the real root is passed in rather than derived
# (see getAppRoot in server/prod.ts).
ENV K8SCENTER_APP_ROOT=/app

EXPOSE 8000

ENTRYPOINT ["/app/k8scenter-frontend"]
