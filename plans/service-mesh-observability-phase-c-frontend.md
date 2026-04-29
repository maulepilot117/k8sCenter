---
title: feat — Service Mesh Observability Phase C (frontend)
type: feat
status: active
date: 2026-04-28
origin: plans/service-mesh-observability.md
---

# feat: Service Mesh Observability Phase C (frontend)

## Overview

Build the four frontend surfaces that consume the already-shipped Phase A + B `/mesh/*` backend endpoints: a mesh status dashboard, a unified traffic-routing list and detail view across Istio + Linkerd, and an mTLS posture page with per-namespace cards. All three pages live under a new `/networking/mesh/*` URL space, with a new "Service Mesh" tab group in the Networking section's SubNav and Cmd+K command-palette entries.

This phase ships the **read** surface only. Mesh wizards (apply / edit) are deferred to a separate plan, per the parent plan's Phase D scope split. Topology overlay and golden-signal display are also deferred to Phase D.

Closes Phase C of `plans/service-mesh-observability.md` (roadmap item #6, partial).

## Problem Frame

Phase A (`PR #199`) shipped mesh detection + traffic-routing inventory; Phase B (`PR #200`, follow-ups `#203`) shipped mTLS posture and golden-signal endpoints. Both phases are agent-callable today, but no GUI exists — operators reach the data via `gh api` or the WebSocket explorer rather than a dashboard. Phase C closes that gap with read-only views that mirror the established Policy and GitOps frontend precedents.

The work is well-bounded: the wire shapes are stable, the patterns to follow are named, and there are no architectural decisions to make at the frontend layer that the backend hasn't already framed.

## Requirements Trace

Carried from the parent plan (`plans/service-mesh-observability.md`). Each item is the frontend-consumption side of the corresponding origin requirement.

- **R1.** Surface mesh installation, control-plane namespace, and version. Empty state when neither mesh is installed. (Origin R1)
- **R2.** List traffic-routing resources (VirtualService, DestinationRule, Gateway, ServiceProfile, etc.) with a unified filterable table. (Origin R2)
- **R3.** Detail view per route with normalized fields, raw YAML viewer, and cross-links to referenced k8s resources. (Origin R3)
- **R4.** Per-namespace mTLS posture cards (red / yellow / green) plus a per-workload drill-down table. (Origin R4)
- **R7.** Honor backend RBAC: the frontend never assumes data the backend filtered out is recoverable. Partial-failure error keys surface as user-facing banners. (Origin R7)
- **R9.** Non-functional: `deno lint`, `deno fmt --check`, `deno task build` clean. Zero hardcoded Tailwind color classes (Phase 6C compliance). All semantic color use goes through CSS custom properties or the Tailwind semantic aliases (`text-success`, `bg-bg-elevated`, etc.). All seven themes render without regression. (Origin R9)

R5 (topology overlay) and R6 (golden signals UI) are explicitly **out of scope for Phase C** — they belong to Phase D.

## Scope Boundaries

- **No mesh wizards in this phase.** Apply / edit / delete still go through `/yaml/apply`. Mesh-specific creation wizards are a separate post-Phase-D plan.
- **No topology overlay.** `?overlay=mesh` on `/observability/topology` is Phase D.
- **No golden-signal display.** `GET /mesh/golden-signals` is wired in `mesh-api.ts` but not consumed by any island in this phase. The endpoint is reserved for Phase D's service-detail integration.
- **No new backend changes.** This phase consumes the existing wire shapes verbatim. If a wire-shape gap is discovered (e.g., a missing field), it is recorded as a Phase D pre-req, not patched here.
- **No mesh-specific WebSocket subscriptions.** The wire `/ws/resources` topic already covers any kubectl-equivalent watches mesh views need; the islands re-fetch on a soft refresh signal rather than introducing a new WS topic.
- **No frontend-side caching.** Backend already caches routing / policies for 30s via singleflight; mTLS is computed per-request and cheap. Islands fetch on mount and on explicit refresh.
- **No Istio Ambient-mode-specific UI.** Ambient pods classify as unmeshed per the parent plan's documented v1 boundary (see `plans/service-mesh-observability.md` line 45). The mTLS table renders them as `unmeshed`, not as a separate Ambient column.

### Deferred to Separate Tasks

- **Topology overlay + frontend toggle** — Phase D Unit D1/D2 (parent plan).
- **Golden-signal display on service detail pages** — Phase D Unit D3 (parent plan).
- **Mesh creation wizards** — separate plan after Phase D lands and the read surface is stable.
- **Distributed tracing integration** — separate roadmap item.

## Context & Research

### Relevant Code and Patterns

Frontend precedents to mirror, in priority order:

- **`frontend/islands/PolicyDashboard.tsx`** — the canonical "engine status banner + empty-state install links + filtered list" island. C2 (MeshDashboard) follows this almost line-for-line.
- **`frontend/islands/GitOpsApplications.tsx`** — list with mesh/namespace/search filters, summary count badges, paginated table, row-click navigation via `encodeURIComponent`. C3 (MeshRoutingList) follows this.
- **`frontend/islands/GitOpsAppDetail.tsx`** — detail island that takes a decoded `id` prop, re-encodes for the API call, renders sectioned `<dl>` panels, handles 404 / error with a back link. C3 (MeshRouteDetail) follows this.
- **`frontend/islands/ComplianceDashboard.tsx`** — `GaugeRing` + per-namespace cards + drill-down table, all using `var(--success)` / `var(--warning)` / `var(--error)` semantics. C4 (MTLSPosture) follows this.
- **`frontend/islands/YamlEditor.tsx`** — thin island wrapper around `frontend/components/ui/MonacoEditor.tsx`. Used by C3's detail view in `readOnly` mode for the raw-YAML fallback.
- **`frontend/components/ui/PolicyBadges.tsx`** + **`frontend/components/ui/GitOpsBadges.tsx`** — per-feature badge files; precedent says Phase C gets its own `MeshBadges.tsx`. Severity colors come from `frontend/lib/badge-colors.ts` — do not redeclare a color map.

Shared infrastructure to consume, not extend:

- **`frontend/lib/api.ts`** — `apiGet<T>`, `apiPost<T>`, `api<T>` helpers. Auto-refresh on 401, X-Cluster-ID injection, CSRF header, throws `ApiError` on non-2xx.
- **`frontend/lib/k8s-links.ts`** — `resourceHref(kind, ns, name)` for cross-links from routing detail to the referenced Service / Deployment / etc.
- **`frontend/lib/health-score.ts`** — `scoreColor(value)` returns `var(--success/warning/error)` based on numeric thresholds. Useful for mTLS posture coloring if we score namespaces.
- **`frontend/lib/badge-colors.ts`** — `SEVERITY_COLORS` map. Re-import; do not duplicate.
- **`frontend/lib/constants.ts`** — `DOMAIN_SECTIONS`, `RESOURCE_API_KINDS`. C5 modifies the `network` section's tabs.
- **`frontend/islands/SubNav.tsx`** — already supports tabs without counts (omit `count` field).
- **`frontend/islands/CommandPalette.tsx`** — quick-action `actions` array around lines 66–109.
- **`frontend/utils.ts`** — `define.page(...)` route wrapper.
- **`frontend/assets/styles.css`** — authoritative source for CSS custom property tokens.

Backend wire shapes (Phase B current state, post-PR #203):

- **`GET /mesh/status`** returns `MeshStatusResponse = { status: MeshStatus }` — a thin envelope, NOT a bare `MeshStatus`. The TS API client must be `apiGet<MeshStatusResponse>` and read `res.data.status`. (`backend/internal/servicemesh/handler.go` ~line 296.)
- **`GET /mesh/routing`** returns `routingResponse = { status: MeshStatus, routes: TrafficRoute[], errors?: { [key: string]: string } }`. `TrafficRoute` carries `id, mesh, kind, name, namespace, hosts[], gateways[], subsets[], selector, matchers[], destinations[], raw` — `raw` is the unstructured map for the YAML viewer.
- **`GET /mesh/routing/{id}`** returns a **bare `TrafficRoute`** (no `{ status, ... }` envelope) — the detail handler writes the route directly. The TS API client must be `apiGet<TrafficRoute>`, not the routing-list response shape. (`backend/internal/servicemesh/handler.go` ~line 486.) This asymmetry is intentional but worth noting; the implementer should not assume an envelope by analogy to the list endpoint.
- **`GET /mesh/mtls`** returns `MTLSPostureResponse = { status: MeshStatus, workloads: WorkloadMTLS[], errors?: { [key: string]: string } }`. Optional `?namespace=<ns>` query parameter scopes to one namespace; omitted = cluster-wide.
- `WorkloadMTLS` — fields: `namespace, workload, workloadKind, mesh, state ('active'|'inactive'|'mixed'|'unmeshed'), source ('policy'|'metric'|'default'), istioMode (Istio only), sourceDetail (Istio only), workloadKindConfident: boolean` (the `workloadKindConfident` field is new in PR #203 — `false` means the workload kind was inferred via the RS-name alphabet heuristic, `true` means owner-reference confirmed).
- **`Errors` map keys** for mTLS: `pods`, `policies`, `truncated`, `prometheus-cross-check`, plus per-CRD fetch errors keyed `istio/VirtualService` etc. The error message strings (e.g., the truncation copy "result capped at N pods; metric cross-check covered only visible workloads…") are fully formatted by the backend; the frontend renders them verbatim and does not template them. Updates to the copy go through the backend, not the frontend.
- Composite-ID format: `"{mesh}:{namespace}:{kindCode}:{name}"` — colon-delimited four-part. Kind codes: Istio `vs/dr/gw/pa/ap`, Linkerd `sp/srv/hr/ap/mtls`. The `:` becomes `%3A` in URLs.
- `MeshStatus` — `{ detected: 'istio'|'linkerd'|'both'|'', istio?: MeshInfo, linkerd?: MeshInfo, lastChecked: string }`.

### Institutional Learnings

From the consolidated learnings research over `plans/`, `.context/compound-engineering/ce-review/`, `memory/`:

- **Two-point IS_BROWSER guard** — every island must have both `useEffect: if (!IS_BROWSER) return;` AND a render-level `if (!IS_BROWSER) return null;`. Missing either one produces silent hydration mismatches in Fresh 2.x.
- **Signals-only, no useState** — established convention across all 109 islands. `const x = useSignal(initial); read x.value; write x.value = next`.
- **Three-signal request lifecycle** — `loading`, `error`, `data`. Empty state branches *after* `!loading.value && !error.value`, never during loading.
- **Composite-ID encoding round-trip** — `encodeURIComponent` in the list island when building hrefs; `decodeURIComponent` in the route file's `ctx.params.id`; `encodeURIComponent` again in the detail island when calling the API. Three points; never forget the third.
- **CSS-token asymmetry trap** — CSS variable is `--error`, but the Tailwind class is `text-danger` / `bg-danger`. Don't mix forms.
- **WorkloadKindConfident UI rule (PR #203)** — `workloadKindConfident: false` rows render with a visual uncertainty indicator (asterisk + tooltip), not a suppressed row. Heuristic-derived workload kinds remain visible but flagged.
- **Mesh ambient-mode is a documented v1 scope boundary** — pods without sidecars classify as `unmeshed`. Do not flag this as a frontend bug if the table shows ambient pods in the unmeshed section. See parent plan `plans/service-mesh-observability.md` line 45.
- **`lib/themes.ts` is client-only** — never import it in a route file; route files are SSR.

### External References

External research was not run for this phase — local patterns are extensive, the wire shapes are stable, and the frontend tech stack (Fresh 2.x + Preact signals + Tailwind v4) has well-established internal conventions. The Istio and Linkerd CRD docs were already consumed during Phase A; this phase consumes the normalized output, not the upstream APIs directly.

## Key Technical Decisions

- **Mesh API client lives in its own file (`frontend/lib/mesh-api.ts`)** — not appended to `lib/api.ts` like `notifApi` / `limitsApi`. Rationale: the mesh surface has more endpoints than those domains (status, routing, routing/:id, policies, mtls, golden-signals) and a parallel surface in Phase D for topology overlay. Future split would have been forced; better to start split. Internal discipline: the file imports only from `lib/api.ts` (the underlying HTTP helpers), nothing else.
- **TypeScript types are string-union discriminators, not const enums.** `type Mesh = 'istio' | 'linkerd' | 'both' | ''`, `type MTLSState = 'active' | 'inactive' | 'mixed' | 'unmeshed'`, `type MTLSSource = 'policy' | 'metric' | 'default'`. Matches JSON serialization without a mapping layer; matches existing `policy-types.ts` / `gitops-types.ts` precedent.
- **`workloadKindConfident: false` rows render with a tooltip-bearing asterisk on the workload name**, not a separate column. Rationale: minimal visual noise; users only need to disambiguate when they care about the kind, and a tooltip surfaces the explanation on demand. The asterisk uses `text-text-muted` (no semantic color). **Accessibility pattern (mandatory):** the asterisk is a focusable trigger, not bare text — render as `<button type="button" class="text-text-muted cursor-help" aria-label="Workload kind inferred from ReplicaSet name (heuristic)">` with the explanation as both `aria-label` and a visible tooltip on hover/focus, so the row is comprehensible to screen-reader and keyboard-only users (not hover-only).
- **`MeshBadges.tsx` is a new file in `frontend/components/ui/`** — same pattern as `PolicyBadges.tsx`, `GitOpsBadges.tsx`. Imports `SEVERITY_COLORS` from `lib/badge-colors.ts`; does not redeclare. Exports: `MeshBadge` (mesh type chip), `MTLSStateBadge` (active/inactive/mixed/unmeshed), `MTLSSourceBadge` (policy/metric/default), `KindBadge` (composite-ID kind code → human label).
- **The `errors` map from the backend is surfaced as a banner above the table**, not silenced. Each present key produces one human-readable line. The truncation message and the prometheus-cross-check message are the most important user-facing strings; both come fully formatted from the backend (the frontend renders them verbatim, no string templating). **Banners are non-dismissible** — operators reload the page or fix the underlying cause; precedent from `PolicyDashboard.tsx` and `ComplianceDashboard.tsx`. Severity classification: `prometheus-cross-check` and `truncated` render as warning-level (`var(--warning)`); per-CRD fetch failures (`istio/...`, `linkerd/...`) and `pods` / `policies` render as error-level (`var(--error)`).
- **Unmeshed workloads on the mTLS page get a separate "Not in mesh" collapsible section**, not a red card. Rationale (carried from origin plan Q): distinguishes opted-out from broken; avoids alarming operators about pods that are deliberately outside the mesh.
- **Routing detail YAML viewer uses `YamlEditor` island, not `MonacoEditor` directly.** `YamlEditor` is the established island wrapper; using `MonacoEditor` from inside another island would violate the islands-architecture guideline (one island per interactive surface). `YamlEditor` already supports `readOnly`.
- **Route files are SSR-only — no `lib/themes.ts` imports in routes.** Theme handling is done globally via the `_app.tsx` inline script. Islands may use `lib/themes.ts` if needed, but C2–C4 islands don't need it.
- **"Service Mesh" lands in the Networking section's SubNav, not its own top-level section.** Three new tabs added to `DOMAIN_SECTIONS[network].tabs`: Service Mesh (dashboard), Mesh Routing, mTLS Posture. The mesh CRDs are not in `RESOURCE_API_KINDS` (they're not informer-cached), so tabs omit the `count` field.
- **The `workloadKind` heuristic uncertainty surfaces only in the mTLS table, not in routing.** Routing items don't carry `workloadKindConfident` — only mTLS workload entries do. The field is computed during *workload aggregation* (mTLS posture infers each pod's owning Deployment via the ReplicaSet → owner-ref walk that landed in PR #203); routing endpoints *enumerate* CRDs from the API directly without an aggregation step, so there is no heuristic to flag. Exposing the field on `TrafficRoute` would be cargo-culting.
- **Routing detail page uses an explicit back `<a>` link, not a breadcrumb.** Matches `frontend/islands/GitOpsAppDetail.tsx` precedent: `<a href="/networking/mesh/routing" class="text-sm text-brand hover:underline mb-4 inline-block">&larr; Back to Routing</a>`. Keeps the surface consistent with neighbouring detail pages.
- **Filter state on back navigation resets to defaults.** When the user clicks a row, navigates to detail, and returns via the back link or browser back button, the routing list reverts to the unfiltered, search-cleared, page-1 state. Matches the GitOpsApplications precedent (signal-driven state, no URL persistence). If a future iteration wants shareable filter state, it adds URL query params then; this phase keeps it simple.
- **Card expand / collapse defaults to collapsed for every state on the mTLS page.** Green (all-active), yellow (mixed), and the "Not in mesh" section all start collapsed. Click expands to the per-workload drill-down. Avoids overwhelming the operator on first view; expansion is one click away.
- **Routing table relies on horizontal-scroll on narrow viewports** rather than column-priority hiding. Wraps the table in `<div class="overflow-x-auto">` per the `GitOpsApplications.tsx` precedent. Column priority is implicit (left-to-right): name, mesh, kind, namespace, hosts (truncated), destinations.
- **mTLS namespace cards use a responsive 1/2/3-column grid** — `grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4` (Tailwind v4 default breakpoints). Aligns with the existing `ComplianceDashboard.tsx` per-namespace card layout.

## Open Questions

### Resolved During Planning

- **Q: Append mesh wrappers to `lib/api.ts` (notifApi / limitsApi precedent) or new file (`mesh-api.ts`)?** — Resolved: new file. The mesh surface is large enough (six endpoints minimum, more coming in Phase D) that splitting now is cheaper than splitting later.
- **Q: Render `workloadKindConfident: false` as a separate column, suppress the row, or attach a tooltip?** — Resolved: tooltip on the workload name with an asterisk indicator. Minimal visual noise, on-demand explanation.
- **Q: Mesh tabs at top level of nav or inside Networking?** — Resolved: inside Networking. Mesh is data-plane networking; the parent plan's decision stands.
- **Q: Do mesh tabs show resource counts (the `?count=true` SubNav pattern)?** — Resolved: no. Mesh CRDs aren't in the informer-cache `RESOURCE_API_KINDS` registry; counts would require backend changes. Tabs omit the count field.
- **Q: One badges file or per-feature?** — Resolved: per-feature, matches `PolicyBadges` / `GitOpsBadges` precedent. Color map imported from `lib/badge-colors.ts`.
- **Q: Re-encode composite IDs at every layer or once?** — Resolved: re-encode at the URL boundary every time. List island encodes when building hrefs, route file decodes when reading `ctx.params.id`, detail island encodes when calling the API. Three explicit points.
- **Q: Route file convention — `define.page` or older Fresh 1.x export-default-function?** — Resolved: `define.page`. Matches the post-Phase-6B convention; older route files have been migrated.
- **Q: Show "Source: default" rows differently from "Source: policy"?** — Resolved: yes, but as a sub-label (smaller text under the source badge), not a separate badge color. "default" means Linkerd-default-on or no PA applied; "policy" means an explicit PA. Both are policy-side; the distinction is informational.
- **Q: mTLS posture card visualization — status pill or `GaugeRing`?** — Resolved: **status pill**. Each namespace card renders a colored pill (green / yellow / neutral) plus an "X of Y active" label. Namespace name is the visual headline; the ratio is supporting detail. Rationale: mTLS posture is binary-leaning (active / inactive / mixed), so a circular gauge would mostly read 100% or 0% — a pill conveys the same signal with denser scanning and a simpler implementation. The `GaugeRing` precedent from `ComplianceDashboard.tsx` applies better to truly continuous metrics like compliance scores.
- **Q: mTLS namespace filter — local signal or URL query param?** — Resolved: **`?namespace=` URL query param**. The filter signal in `MTLSPosture` syncs to the URL on change (via `globalThis.history.replaceState`) and reads the URL on mount so deep links land on the correct filtered view. Symmetric with the backend's existing `?namespace=` contract on `GET /mesh/mtls`. Enables future cross-page entry points (e.g., a "View mTLS for this namespace" link from workload detail pages) without a follow-up redesign. Search and other ephemeral filters stay as local signals; only namespace gets URL state.

### Deferred to Implementation

- **Exact tooltip text for `workloadKindConfident: false`** — implementer to draft a one-line explanation that references the heuristic in plain English. Rough cut: "Workload kind inferred from ReplicaSet name (no owner-reference lookup)." Refine during C4. (Used as both the `aria-label` and the visible tooltip text per the accessibility pattern in Key Technical Decisions.)
- **Visual-regression Playwright snapshots: existing dashboards already have them — do mesh views need new ones, or do existing screenshot routes cover the layout pattern?** — decide during C2 by checking what's already in `e2e/`.
- **Final pagination size for routing list (`PAGE_SIZE = 100` is the GitOps default)** — keep 100 unless homelab smoke shows scrolling issues.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

**URL space and SubNav placement:**

```
/networking/                                 (existing — Networking dashboard)
├─ /services
├─ /ingresses
├─ /policies                                  (existing CNI policies)
├─ /cilium-policies
├─ /flows
├─ /endpoints
├─ /endpoint-slices
├─ /gateway-api
└─ /mesh/                                     (NEW)
   ├─ index.tsx           → redirect → /networking/mesh/dashboard
   ├─ dashboard.tsx       → MeshDashboard island
   ├─ routing.tsx         → MeshRoutingList island
   ├─ routing/[id].tsx    → MeshRouteDetail island
   └─ mtls.tsx            → MTLSPosture island
```

Three new tabs in `DOMAIN_SECTIONS[network].tabs`: `Service Mesh` (→ `/networking/mesh`), `Mesh Routing` (→ `/networking/mesh/routing`), `mTLS Posture` (→ `/networking/mesh/mtls`).

**Standard island anatomy (every island in this phase follows this shape):**

```ts
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { meshApi } from "@/lib/mesh-api.ts";
import type { MeshStatus, /* ... */ } from "@/lib/mesh-types.ts";

export default function MeshSomething() {
  const status = useSignal<MeshStatus | null>(null);
  const items = useSignal<...[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  // ...filter signals...

  async function fetchData() {
    try {
      // Promise.all when there are multiple parallel reads
      const [statusRes, itemsRes] = await Promise.all([
        meshApi.status(),
        meshApi.routes(/* ... */),
      ]);
      status.value = statusRes.data;
      items.value = itemsRes.data ?? [];
      error.value = null;
    } catch {
      error.value = "Failed to load mesh data";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => { loading.value = false; });
  }, []);

  if (!IS_BROWSER) return null;
  if (loading.value) return <Spinner />;
  if (error.value) return <ErrorBanner message={error.value} />;
  // empty state
  // main content
}
```

**Composite-ID round-trip:**

```
list island                              route file                         detail island
-----------                              ----------                         -------------
href = "/networking/mesh/routing/" +
       encodeURIComponent(route.id)
                                         id = decodeURIComponent(
                                                ctx.params.id
                                              )
                                         <MeshRouteDetail id={id} />
                                                                            apiGet(
                                                                              "/v1/mesh/routing/" +
                                                                              encodeURIComponent(id)
                                                                            )
```

`route.id` looks like `istio:shop:vs:cart-vs`; on the wire it becomes `istio%3Ashop%3Avs%3Acart-vs`. The route file's `ctx.params.id` is the encoded form; `decodeURIComponent` restores it before passing to the island.

**mTLS posture render decision tree (per namespace):**

```
sum visible workloads, count by state
├─ all 'active'       → green card, "X of X active"
├─ any 'inactive' or
   any 'mixed'        → yellow card, "X of Y active", drill-down
├─ all 'unmeshed'     → "Not in mesh" section (separate, neutral color)
└─ mixed (some
   unmeshed, some
   active/inactive)   → yellow card with a sub-line "M not in mesh"
```

`source` field surfaces as a small badge within the drill-down row: `policy` / `metric` / `default`. `workloadKindConfident: false` adds an asterisk after the workload name with a tooltip.

## Output Structure

```
frontend/
├── lib/
│   ├── mesh-types.ts                                  (NEW)
│   └── mesh-api.ts                                    (NEW)
├── components/
│   └── ui/
│       └── MeshBadges.tsx                             (NEW)
├── islands/
│   ├── MeshDashboard.tsx                              (NEW)
│   ├── MeshRoutingList.tsx                            (NEW)
│   ├── MeshRouteDetail.tsx                            (NEW)
│   └── MTLSPosture.tsx                                (NEW)
├── routes/networking/mesh/
│   ├── index.tsx                                      (NEW — redirects to dashboard)
│   ├── dashboard.tsx                                  (NEW)
│   ├── routing.tsx                                    (NEW)
│   ├── routing/
│   │   └── [id].tsx                                   (NEW)
│   └── mtls.tsx                                       (NEW)

Modified:
├── frontend/lib/constants.ts                          (add Mesh tabs to DOMAIN_SECTIONS[network])
└── frontend/islands/CommandPalette.tsx                (add 3 mesh quick actions)
```

## Implementation Units

- [ ] **Unit C1: TypeScript types + API client + mesh badges**

**Goal:** Single shared type module + thin typed API client + display-only badges, all consumed by C2–C4. Land separately so the four UI units have a stable foundation to import from.

**Requirements:** R1–R4, R7 (consumption layer)

**Dependencies:** None.

**Files:**
- Create: `frontend/lib/mesh-types.ts`
- Create: `frontend/lib/mesh-api.ts`
- Create: `frontend/components/ui/MeshBadges.tsx`

**Approach:**
- `mesh-types.ts` mirrors the backend shapes documented in Context & Research. String-union discriminators for `MeshType`, `MTLSState`, `MTLSSource`. Tagged union `TrafficRoute = IstioRoute | LinkerdRoute` with the `mesh` field as the tag — but in practice the consumer rarely narrows because the table renders fields generically.
- `WorkloadMTLS` includes the new `workloadKindConfident: boolean` field from PR #203.
- `mesh-api.ts` exports a `meshApi` namespace object. Function shapes (return-type `Promise<APIResponse<T>>` from `apiGet`):
  - `meshApi.status(): Promise<APIResponse<MeshStatusResponse>>` (envelope: read `res.data.status`)
  - `meshApi.routes(opts?: { namespace?: string }): Promise<APIResponse<RoutingResponse>>`
  - `meshApi.route(id: string): Promise<APIResponse<TrafficRoute>>` (bare `TrafficRoute`, no envelope — see Context & Research)
  - `meshApi.policies(opts?: { namespace?: string }): Promise<APIResponse<PoliciesResponse>>`
  - `meshApi.mtls(opts?: { namespace?: string }): Promise<APIResponse<MTLSPostureResponse>>`
  - `meshApi.goldenSignals(opts: { namespace: string; service: string; mesh?: 'istio' | 'linkerd' }): Promise<APIResponse<GoldenSignalsResponse>>` — `namespace` and `service` are required; `mesh` is optional and only meaningful when both meshes are installed
  Each wraps `apiGet<T>` from `lib/api.ts`. Query strings are built via the `URLSearchParams` pattern from `notifQueryString`. `goldenSignals` is wired but not consumed in this phase (Phase D will use it).
- `MeshBadges.tsx` exports `MeshBadge`, `MTLSStateBadge`, `MTLSSourceBadge`, `KindBadge`. Color sourcing: state and source come from `lib/badge-colors.ts`'s `SEVERITY_COLORS`-style map; mesh-type badges use neutral `text-text-muted` with a mesh-name string. `KindBadge` maps Istio + Linkerd kind codes to human-readable labels (`vs` → "VirtualService", `sp` → "ServiceProfile", etc.).

**Patterns to follow:**
- `frontend/lib/policy-types.ts`, `frontend/lib/gitops-types.ts` (string-union shapes)
- `frontend/lib/api.ts` (api helper signatures, `URLSearchParams` query-string pattern from `notifQueryString`)
- `frontend/components/ui/PolicyBadges.tsx`, `frontend/components/ui/GitOpsBadges.tsx` (badge-component file shape)
- `frontend/lib/badge-colors.ts` (re-import; do not redeclare)

**Test scenarios:**
- Test expectation: none — pure type definitions, thin API wrappers, and display-only badges with no behavior beyond network plumbing and string mapping. Covered indirectly by C2–C4 island tests + existing visual regression (when the badges render). The implementer should not write Deno unit tests for this unit; if the API contract changes, the C2–C4 tests will catch it.

**Verification:**
- `deno check frontend/lib/mesh-types.ts frontend/lib/mesh-api.ts frontend/components/ui/MeshBadges.tsx` clean
- `deno lint` clean on all three files
- `deno fmt --check` clean
- Spot-import each export from a scratch file to confirm the exported names match what C2–C4 will need (or land C1 + C2 together if importing into a stub island is the easiest verification)

---

- [ ] **Unit C2: Mesh dashboard island + routes**

**Goal:** Status banner showing detected mesh + version + control-plane namespace; empty state with install links when no mesh is detected; partial-failure banner when the `errors` map carries any keys. The page acts as a *health-check landing*: the operator confirms the mesh is installed and reachable, then navigates to **Mesh Routing** or **mTLS Posture** for actionable work. Deliberately read-only and light — no list of routes here.

**Requirements:** R1, R7

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MeshDashboard.tsx`
- Create: `frontend/routes/networking/mesh/index.tsx`
- Create: `frontend/routes/networking/mesh/dashboard.tsx`

**Approach:**
- `index.tsx` is a thin redirect to `/networking/mesh/dashboard` (matches the existing redirect pattern in `routes/security/index.tsx`).
- `dashboard.tsx` wires SubNav from `DOMAIN_SECTIONS[network]` + the `MeshDashboard` island, using `define.page`.
- `MeshDashboard` calls `meshApi.status()` on mount. Renders one banner card per detected mesh (Istio + Linkerd render as siblings when both installed). Each card shows mesh name, version, control-plane namespace, "last checked" timestamp.
- Empty state: `status.detected === ""` (or both `installed: false`) and no error → "No service mesh detected" with two `<a>` links to upstream install docs (`https://istio.io/latest/docs/setup/`, `https://linkerd.io/2/getting-started/`).
- The dashboard does NOT load routing or mTLS data (those are separate pages); it stays light.

**Patterns to follow:**
- `frontend/islands/PolicyDashboard.tsx` for the banner + empty-state structure (the engine-status pattern is the closest analog)
- `frontend/routes/security/policies.tsx` for the `define.page` wiring and SubNav inclusion

**Test scenarios:**
- Happy path: both meshes installed → both banner cards render with respective versions / namespaces.
- Happy path: only Istio installed → only the Istio card renders; no Linkerd card.
- Happy path: only Linkerd installed → only the Linkerd card renders.
- Empty state: neither installed → "No service mesh detected" with two install links. Renders only after `loading.value === false`.
- Edge: backend returns 200 with `errors: { 'istio': 'discovery failed' }` (CRD probe transient failure) → banner card still renders with available data, error banner above with the human message.
- Theme: rendering smoke in all 7 themes — uses `var(--success)` / `var(--warning)` / `var(--bg-elevated)` only; zero hardcoded Tailwind colors.
- Hydration: SSR + island handoff — the `if (!IS_BROWSER) return null` render guard prevents Fresh hydration mismatch.

**Verification:**
- `deno check`, `deno lint`, `deno fmt --check` clean
- `deno task build` clean
- Manual: navigate to `/networking/mesh/dashboard` against the homelab; verify both meshes (or one) render correctly
- Optional: a Playwright smoke test under `e2e/` that asserts the dashboard route loads without console errors

---

- [ ] **Unit C3: Mesh routing list + detail islands + routes**

**Goal:** Unified, filterable, paginated table of traffic-routing resources from both meshes; row click navigates to a detail view with normalized fields and a raw-YAML fallback.

**Requirements:** R2, R3, R7

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MeshRoutingList.tsx`
- Create: `frontend/islands/MeshRouteDetail.tsx`
- Create: `frontend/routes/networking/mesh/routing.tsx`
- Create: `frontend/routes/networking/mesh/routing/[id].tsx`

**Approach:**
- `routing.tsx` wires SubNav + `MeshRoutingList` island. `routing/[id].tsx` decodes `ctx.params.id` and passes it as a prop to `MeshRouteDetail`.
- `MeshRoutingList` calls `meshApi.routes()` on mount. Renders a filter row (mesh select, namespace input or select, search by name), a summary count strip ("X Istio • Y Linkerd • Z total"), and a paginated table (PAGE_SIZE = 100).
- Each row: name, mesh badge, kind badge, namespace, hosts (truncated), destination count. Click → `/networking/mesh/routing/<encodeURIComponent(id)>`.
- Filters are signal-driven; computed `filtered` array is a `const filtered = items.value.filter(...)` inside the render (no `useComputed`).
- `MeshRouteDetail` calls `meshApi.route(id)` on mount. Renders sectioned `<dl>` panels: header (mesh + kind + name + namespace), Source (hosts, gateways, destinations as a list with cross-links via `resourceHref` where applicable), Spec details (matchers, subsets), and a collapsible Raw YAML section using `YamlEditor` in `readOnly` mode with the route's `raw` field serialized to YAML.
- Detail 404 path: backend returns 404 on unknown id → island renders empty state with "Resource not found" + back link to `/networking/mesh/routing`.

**Patterns to follow:**
- `frontend/islands/GitOpsApplications.tsx` for the list (filter signals, pagination, summary badges, row-click pattern)
- `frontend/islands/GitOpsAppDetail.tsx` for the detail (`id` prop, sectioned `<dl>`, error/404 with back link)
- `frontend/islands/YamlEditor.tsx` for the raw-YAML viewer
- `frontend/lib/k8s-links.ts` for cross-links to Service / Deployment / etc.

**Test scenarios:**
- Happy path (list): mixed Istio + Linkerd routes → table shows merged result with correct mesh badges; summary row shows correct counts.
- Filter (list): mesh = Istio → only Istio routes; mesh = Linkerd → only Linkerd; mesh = All → both.
- Filter (list): namespace filter narrows table; search filter (substring on name) narrows table; combined filters compose.
- Empty state (list): zero routes match filters → "No routes match your filters" message; renders only after `!loading.value && !error.value`.
- Empty state (list): cluster has no mesh routes at all → "No routes found. Routes will appear once a mesh is installed and resources exist." (different copy from "no matches").
- Error path (list): backend returns 500 → error banner; no broken table.
- Edge (list): backend returns `errors: { 'istio/VirtualService': '...' }` with partial data → table renders available rows, banner above lists which CRD failed.
- Pagination (list): >100 routes → page controls render; navigating pages preserves filter state.
- Happy path (detail): valid composite ID → detail renders with all spec sections; raw YAML section collapsible.
- Edge (detail): composite ID with `%3A` URL-encoding decodes correctly and loads the right resource.
- Edge (detail): unknown composite ID → 404 → "Resource not found" empty state with back link, no crash.
- Edge (detail): cross-link click on a destination → navigates to the referenced resource via `resourceHref`; null `resourceHref` (unknown kind) renders as plain text.
- Edge (detail): YamlEditor failure (CDN load failed) → fallback textarea renders the YAML; `readOnly` is honored on the textarea path too.
- Theme: list and detail render in all 7 themes; no hardcoded colors.
- Hydration: both islands have the IS_BROWSER guards.

**Verification:**
- `deno check`, `deno lint`, `deno fmt --check` clean
- `deno task build` clean
- Manual: list page loads, click a row, detail loads, raw YAML expands, back link returns to list with filter state intact (or reset — either is acceptable; document which)
- Composite-ID URL-encoding round-trip verified by visiting `/networking/mesh/routing/istio%3Ashop%3Avs%3Acart-vs` directly

---

- [ ] **Unit C4: mTLS posture page**

**Goal:** Per-namespace cards (red / yellow / green / neutral) with a per-workload drill-down table. Surface partial-failure banners for `prometheus-cross-check` and `truncated`. Render `workloadKindConfident: false` rows with a tooltip-bearing asterisk.

**Requirements:** R4, R7

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MTLSPosture.tsx`
- Create: `frontend/routes/networking/mesh/mtls.tsx`

**Approach:**
- `mtls.tsx` wires SubNav + `MTLSPosture` island.
- `MTLSPosture` calls `meshApi.mtls()` on mount (cluster-wide; namespace filter is a signal-driven re-fetch). Renders an error/notice banner if `errors` map is non-empty (one line per key).
- Aggregates the flat `workloads` array by namespace into card data. Card color (per Render Decision Tree above):
  - All `active` → `var(--success)` accent, "X of X active"
  - Any `inactive` or `mixed` → `var(--warning)` accent, "X of Y active", expandable
  - All `unmeshed` → neutral, surfaced as a separate "Not in mesh" collapsed section at the bottom
  - Mixed unmeshed + meshed → `var(--warning)` accent with sub-line "(M of Y not in mesh)"
- Drill-down per card: collapsible row showing per-workload table with columns: Workload (kind + name; asterisk + tooltip if `workloadKindConfident: false`), State badge, Source badge, IstioMode (Istio rows only, blank for Linkerd), SourceDetail (Istio rows only, "—" otherwise).
- The asterisk tooltip text: "Workload kind inferred from ReplicaSet name (no owner-reference lookup)." (Implementer can revise wording.)
- "Source: default" rows render the `MTLSSourceBadge` plus a sub-line in `text-text-muted` explaining the source ("Default-on (Linkerd)" or "No explicit policy").

**URL state:** the namespace filter syncs with the `?namespace=` URL query parameter — read on mount via `new URL(globalThis.location.href).searchParams.get('namespace')`, write on change via `globalThis.history.replaceState`. Re-fetch on URL change so deep-links land on the correctly filtered view. Search and other ephemeral filters remain local signals (no URL state).

**Patterns to follow:**
- `frontend/islands/ComplianceDashboard.tsx` for the per-namespace card layout (cards on a 1/2/3-column grid, drill-down expand pattern)
- `frontend/components/ui/PolicyBadges.tsx` for the per-row badge layout
- `frontend/lib/badge-colors.ts` for posture pill colors (do not redeclare)

**Test scenarios:**
- Happy path: all-active namespace → green card with no drill-down expanded by default; click expands.
- Edge: mixed namespace (some active, some inactive) → yellow card with expandable drill-down; per-workload state badges colored correctly.
- Edge: all-unmeshed namespace → appears in "Not in mesh" section (separate from main grid), neutral color, not red.
- Edge: namespace with both meshed and unmeshed pods → yellow card; drill-down shows both `unmeshed` rows and `active`/`inactive` rows; sub-line summarizes count.
- Edge: `workloadKindConfident: false` workload → asterisk renders next to the kind/name; hover shows the tooltip.
- Edge: `source: 'metric'` row → source badge color reflects metric origin; sub-line explains.
- Edge: `source: 'default'` (Linkerd default-on) → sub-line "Default-on (Linkerd)" renders; not styled identically to `policy`.
- Edge: backend response carries `errors: { 'prometheus-cross-check': '...' }` → notice banner above cards: "metric cross-check unavailable; posture derived from policies only".
- Edge: backend response carries `errors: { 'truncated': '...' }` (cluster-wide, capped at meshListCap) → notice banner: "result capped at N pods; metric cross-check covered only visible workloads (pass ?namespace= to scope the request)".
- Edge: empty workloads array (no mesh installed, or no pods returned) → empty state "No workloads in mesh"; only after `!loading.value`.
- Edge: `mesh: 'linkerd'` row → IstioMode and SourceDetail columns render "—" not blank or null.
- URL state: visiting `/networking/mesh/mtls?namespace=shop` directly → namespace filter pre-populated to "shop" before first paint; re-fetch fires with `?namespace=shop` on the wire.
- URL state: changing the namespace filter in the UI → URL updates to `?namespace=<new>` without a page reload (`history.replaceState`).
- Theme: cards render in all 7 themes; semantic colors via `var(--success)` / `var(--warning)` / `var(--error)` only.
- Hydration: IS_BROWSER guard pair present.

**Verification:**
- `deno check`, `deno lint`, `deno fmt --check` clean
- `deno task build` clean
- Manual: navigate to `/networking/mesh/mtls` against the homelab; expand a yellow card; verify the asterisk + tooltip on a heuristic-derived workload (homelab should have at least one pod with `workloadKindConfident: false` if there are user-named ReplicaSets present)
- Manual: temporarily disable Prometheus on the homelab → reload → confirm the prometheus-cross-check banner appears with the correct copy

---

- [ ] **Unit C5: Navigation + command palette wiring**

**Goal:** Make the new `/networking/mesh/*` pages reachable from the existing UI surfaces.

**Requirements:** R1–R4 (discoverability)

**Dependencies:** C2, C3, C4. (Fresh returns a graceful 404 if a tab is added before its route file exists, but landing nav last keeps the SubNav from showing dead tabs in any merged-but-not-yet-completed state.)

**Files:**
- Modify: `frontend/lib/constants.ts` (add three tabs to `DOMAIN_SECTIONS[network].tabs`)
- Modify: `frontend/islands/CommandPalette.tsx` (append three quick-action entries to the `actions` array)

**Approach:**
- Insert the three mesh tabs into `DOMAIN_SECTIONS[network].tabs` after the `Gateway API` tab. Each tab is a plain `{ label, href }` object — no `count` field (mesh CRDs are not in `RESOURCE_API_KINDS`). Order: `Service Mesh` → `Mesh Routing` → `mTLS Posture`.
- Append to `CommandPalette.tsx`'s `actions` array (around lines 66–109) three navigation entries with the same labels and hrefs.
- Verify the SubNav renders correctly on each new page (the `currentPath` prop matches the active tab).

**Patterns to follow:**
- `frontend/lib/constants.ts` existing tab definitions in `DOMAIN_SECTIONS[network].tabs`
- `frontend/islands/CommandPalette.tsx` existing entries (lines 66–109)

**Test scenarios:**
- Happy path: navigate to each new page directly via URL → SubNav highlights the correct tab.
- Happy path: open Cmd+K → type "mesh" → all three quick actions appear → click one → navigate to the right page.
- Visual smoke: SubNav layout doesn't break on the new tab labels (no overflow / wrap regressions in any of the 7 themes).

**Verification:**
- `deno check`, `deno lint`, `deno fmt --check` clean
- `deno task build` clean
- Manual: SubNav active-state on each mesh page; Cmd+K → search "mesh" → three results

## System-Wide Impact

- **Interaction graph:** The mesh pages are new and self-contained — they do not subscribe to existing WebSocket topics, do not modify the resource cache, and do not register additional informers. The only cross-cutting touch is the SubNav tab additions (C5) which other parts of the Networking section will see in the tab list.
- **Error propagation:** The `errors` map from the backend surfaces as user-facing banners — no error toast, no console-only errors. `apiGet` already throws `ApiError` on non-2xx; islands catch and set a generic message. Partial-failure semantics (Phase B) are preserved end-to-end.
- **State lifecycle risks:** None — all data is fetched fresh on island mount and on explicit refresh. No frontend caching, no stale-data hazards.
- **API surface parity:** No new public-facing endpoints. No backend changes. The frontend consumes the existing `/mesh/*` surface verbatim.
- **Integration coverage:** The composite-ID round-trip (encode in list → decode in route → encode in detail API call) is the most fragile cross-layer behavior; C3's test scenarios specifically exercise the `%3A` encoding path. Partial-failure banner rendering exercises the `errors` map propagation.
- **Unchanged invariants:** No backend changes. No changes to other frontend surfaces (Cilium, Hubble, Gateway API). No changes to existing themes, signals, or hydration patterns. The `RESOURCE_API_KINDS` registry is unchanged (mesh CRDs deliberately stay out of it because they aren't informer-cached).

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| **Mesh CRDs not present in homelab during smoke tests.** | Verify Istio + Linkerd are installed on the homelab before C2 lands; the parent plan's Documentation/Operational Notes already calls this out as a pre-Phase-A requirement and should still apply. If not, install both before merging C2. |
| **Composite-ID encoding regression silently breaks the detail page.** | C3 test scenarios explicitly cover `%3A` round-trip. The route file's `decodeURIComponent` + the detail island's `encodeURIComponent` must both run. Add a Playwright smoke that loads `/networking/mesh/routing/istio%3Ashop%3Avs%3Acart-vs` directly. |
| **`workloadKindConfident: false` rows mis-render** (asterisk missing or tooltip broken across themes). | C4 test scenario exercises this; the implementer should also check tooltip rendering in at least 2 themes (one light if a light theme exists, otherwise two distinct dark themes). |
| **`Source: default` vs `Source: policy` distinction is lost in the UI.** | C4 renders a sub-label, not just the badge color. The Linkerd default-on case is the most likely to be conflated with Istio UNSET; both surface as `default` from the backend. The sub-label disambiguates by mesh. |
| **YamlEditor CDN load fails in CI** (no network). | `MonacoEditor` (which `YamlEditor` wraps) already has a fallback textarea path; pass `readOnly={true}` so the textarea is also non-editable. C3 test scenario covers this. |
| **`workloadKindConfident` field absent from older backend builds.** | Field was added in PR #203 (already merged at planning time). Frontend treats missing field as `undefined` → renders as `false` (heuristic) which is the safer default. The TS type uses `workloadKindConfident: boolean` (required); null-coerce on read in the island if needed. |
| **SubNav tab list overflows in narrow viewports** with three new tabs added to Networking. | Visual smoke during C5; existing SubNav already handles overflow with horizontal scroll. If the new labels cause issues, abbreviate ("Mesh", "Routing", "mTLS"). |
| **CLAUDE.md Rule 5 (sub-agent swarming for >5 files).** | This phase touches roughly 11 frontend files in five units. Each unit's file count is well under five; sub-agent swarming is not necessary. Phased execution per Rule 2 means C1 lands, then C2, etc. |

## Documentation / Operational Notes

- **CLAUDE.md update:** No roadmap change yet — item #6 stays unchecked until Phase D lands. The Build Progress section gets a "Phase 12 (Service Mesh Phase C — frontend): COMPLETE" entry on merge.
- **Homelab smoke (per CLAUDE.md pre-merge rule):** Each unit smoke-tests against the homelab. The most informative flow is end-to-end: dashboard → routing list → click a row → detail → back → mTLS posture → expand a card.
- **Theme regression check:** All 7 themes (Nexus, Dracula, Tokyo Night, Catppuccin, Nord, One Dark, Gruvbox). Phase 6C compliance verified: zero hardcoded Tailwind color classes (`grep -rE '(bg|text|border)-(red|blue|green|yellow|gray|slate|zinc|stone|orange|amber|lime|emerald|teal|cyan|sky|indigo|violet|purple|fuchsia|pink|rose)-[0-9]'` should return zero hits in the new files).
- **Pre-merge ce-review (per memory):** Run `/ce:review` BEFORE pushing each unit's branch. Apply safe-auto fixes and resolve any findings before opening the PR.

## Sources & References

- **Origin document:** [plans/service-mesh-observability.md](./service-mesh-observability.md) — Phase A and B merged; this plan refines Phase C (the Frontend section, lines 440–545 of the origin).
- **Phase A PR:** #199
- **Phase B PR:** #200; follow-ups #203
- **Frontend precedents:** `frontend/islands/PolicyDashboard.tsx`, `frontend/islands/GitOpsApplications.tsx`, `frontend/islands/GitOpsAppDetail.tsx`, `frontend/islands/ComplianceDashboard.tsx`
- **Shared infrastructure:** `frontend/lib/api.ts`, `frontend/lib/constants.ts`, `frontend/lib/k8s-links.ts`, `frontend/lib/badge-colors.ts`, `frontend/islands/SubNav.tsx`, `frontend/islands/CommandPalette.tsx`, `frontend/islands/YamlEditor.tsx`, `frontend/components/ui/MonacoEditor.tsx`
- **Backend wire shapes:** `backend/internal/servicemesh/types.go`, `backend/internal/servicemesh/mtls.go`, `backend/internal/servicemesh/handler.go`, `backend/internal/servicemesh/metrics.go`
- **Ce-review artifact for Phase B follow-up (workloadKindConfident origin):** `.context/compound-engineering/ce-review/20260428-192817-781f6a43/synthesis.md`
- **Scope boundaries:** `plans/service-mesh-observability.md` lines 42–55 (parent plan's Scope Boundaries section, including the Ambient-mode and Linkerd-default-domain v1 limits)
