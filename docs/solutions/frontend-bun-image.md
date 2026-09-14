---
title: Frontend container conventions — the Bun image shape and what replaced the Deno sandbox
date: 2026-09-14
last_updated: 2026-09-14
category: docs/solutions
module: frontend
problem_type: convention
component: infrastructure
severity: high
related_components: [helm_chart, ci_pipeline, service_layer]
tags: [bun, astro, distroless, container-hardening, supply-chain, networkpolicy, trivy, enable-service-links]
applies_when:
  - "Changing frontend/Dockerfile, its base image digest, or the compile target"
  - "Adding an environment variable, volume, or secret to the frontend pod"
  - "Changing the frontend NetworkPolicy or networkPolicy.enabled"
  - "Interpreting what the Trivy release gate does and does not cover for the frontend"
---

# Frontend Container Conventions — the Bun Image Shape

Written during U10 of the Bun + Astro migration
(`docs/plans/2026-09-14-0850-refactor-bun-astro-migration-plan.md`). It records
three things that are easy to undo by accident: why the image is split the way
it is, what replaced the Deno permission sandbox, and what the release gate
actually proves.

## The image is a compiled launcher plus a pre-bundled entry, not one binary

The obvious shape does not work. `bun build --compile` on the whole server
fails at runtime because `@astrojs/node` locates its own static-asset directory
by walking `import.meta.url` for a literal `server` path segment, and a
compiled binary's virtual path (`/$bunfs/...`) has none. Dynamically importing
the on-disk `dist/server/entry.mjs` instead fails too, on unresolvable external
imports such as `preact`.

What works, and what ships:

1. `astro build` emits `dist/client/` and `dist/server/entry.mjs`.
2. `bun build --target=bun` re-bundles `entry.mjs` in place so its externals
   are inlined. Without this step the launcher cannot load it at all, because
   the bare specifiers resolve only against `node_modules` — and
   `node_modules` is exactly what must not reach the runtime layer.
3. `bun build --compile` compiles `server/prod.ts` and everything it imports
   statically (dispatch, static, ws-proxy, headers, `ws`) into one binary.
4. The runtime image gets the binary, the pre-bundled entry, `dist/client/`
   and `bun.lock`. No package manager, no shell, no `node_modules`, no Bun
   toolchain.

Because a compiled binary's `import.meta.url` points inside itself,
`server/prod.ts` resolves `dist/` from `K8SCENTER_APP_ROOT` when that is set
and from `import.meta.url` otherwise. Both paths are exercised: `bun run start`
locally, the binary in the image. Changing one without the other is the way
this breaks.

**The x64 compile target is deliberately the baseline one.**
`bun-linux-x64-baseline` runs on any x86-64 CPU; plain `bun-linux-x64` is
faster but requires AVX2 (Intel Haswell, 2013, and later) and aborts with
"Illegal instruction" on older hardware. k8sCenter is a Helm chart installed
onto clusters whose nodes we do not choose, so the default is the one that
always starts. `--build-arg BUN_COMPILE_TARGET=bun-linux-x64` takes the faster
path when the operator knows their fleet. arm64 has no baseline variant.

## The base is pinned by digest, and not freely swappable

`gcr.io/distroless/cc-debian13`, by immutable digest. Tag-pinning means the
same commit can rebuild onto different runtime bits, and an upstream tag
compromise would bypass the review every other dependency here goes through.
Digest bumps clear the same 7-day cooldown as any other pin.

Two constraints on the choice:

- `distroless/base` ships no libstdc++ and **will not start Bun at all**.
- The base must be Debian-derived, because the security-patch stage compares
  `dpkg` `status.d` entries and reads `changelog.Debian.gz` to enforce its own
  7-day cooldown. A non-Debian base means rebuilding that gate from scratch.

The security-patch stage itself carried over from the Deno image unchanged in
shape. Nothing in it is version-pinned, and that is the point: a static pin
plus a static `sed` of the recorded version broke this stage twice (PRs #426,
#428). A pin holds only until the base catches up, and the base catching up is
the normal case.

## What replaced the Deno permission sandbox

The Deno image ran with `--allow-net --allow-read=. --allow-env=BACKEND_URL,LOG,PORT,HOSTNAME`.
Bun has no equivalent and no third-party shim is worth trusting for a security
boundary, so the runtime-level bound is gone. Three things carry that weight
now, and all three are asserted rather than assumed:

1. **The frontend NetworkPolicy** (`helm/kubecenter/templates/networkpolicy.yaml`)
   permits egress to kube-dns and the backend pod only. This is what actually
   stops a compromised dependency exfiltrating whatever it reads. It is
   operator-toggleable via `networkPolicy.enabled` (default true) and
   CNI-dependent, so it is a control to verify per cluster, not a guarantee.
2. **The pod is given nothing worth reading.** No volumes, no secrets, and one
   environment variable (`BACKEND_URL`). `automountServiceAccountToken: false`.
   The Security Checklist in `CLAUDE.md` carries this as a review item, and
   CODEOWNERS gates `helm/kubecenter/templates/deployment-frontend.yaml` so a
   PR adding a variable, volume, or secret cannot merge unreviewed. That
   CODEOWNERS entry is the enforcement; the checklist line alone is not.
3. **`enableServiceLinks: false`.** Kubernetes otherwise injects
   `<SVC>_SERVICE_HOST` and `<SVC>_SERVICE_PORT` for every Service in the
   namespace. The `--allow-env` allowlist is what made those unreadable before;
   now the injection is simply turned off. Nothing has ever depended on it.

## What the Trivy release gate proves — and what it does not

Measured on 2026-09-14 against the pinned action's Trivy (`aquasecurity/trivy-action`
0.36.0):

- The **OS package** scan is real. It reads `/var/lib/dpkg/status.d/*` from the
  distroless base and reports Debian findings; CRITICAL/HIGH block the release.
- The **application dependency** scan is not, in image mode. `trivy image`
  reports `Number of language-specific files num=0` for this image, and for the
  Deno image it replaces. It does not read `/app/bun.lock`, and it never read
  `/app/deno.lock` either — that copy has been decorative for as long as it has
  existed.
- `trivy fs` on the same `bun.lock` **does** parse it (`Target='bun.lock'
  type=bun`). So the fix is a separate filesystem scan of the lockfile in CI,
  not a change to the image.

The lockfile copy stays in the image regardless: it costs nothing, it is the
manifest an operator or a future Trivy version would read, and removing it
would quietly make the situation worse. But do not read a green image scan as
evidence that the frontend's npm dependency tree was checked. The `trivy fs`
step is what does that.

Related: `bun.lockb` (Bun's binary lockfile) is parsed by nothing here. The
text `bun.lock` is the committed format, and CI asserts the binary one is never
committed.
