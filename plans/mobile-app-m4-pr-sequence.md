---
title: "Mobile M4 PR Sequence — Observability + Advanced Surfaces"
type: feat
status: completed
date: 2026-05-09
origin: plans/mobile-app.md
---

# Mobile M4 PR Sequence — Observability + Advanced Surfaces

## Summary

Land M4 of `plans/mobile-app.md` as ten reviewable PRs (PR-4a → PR-4j) that bring read-side observability surfaces and full per-domain detail screens to the existing Flutter app. PR-4a stands up the shared chart, time-range, gauge, and domain-list primitives plus a `RefreshableController` base for re-pollable read state. PR-4b ports the per-resource Metrics tab over `GET /v1/monitoring/query_range`. PR-4c ships the LogQL editor + label browser over the existing Loki REST endpoints (no new live-tail; the M1 single-pod WebSocket tail stays). PR-4d ports the diagnostics blast-radius surface as a checklist + flat-list pair (no graph render). PR-4e–PR-4j port read-side parity for GitOps (Argo + Flux app detail beyond M2's verb actions, plus AppSets), service mesh (mTLS posture + golden signals + routing rules), cert-manager (observatory + expiry timeline + issuers list — wizards already shipped in PR-3e), External Secrets Operator (live drift detail + per-store metrics + cluster-scoped variants — wizards already shipped in PR-3f), policy (compliance score + violations browser), and Trivy + Kubescape vulnerability reports. Every wizard hits an endpoint that already exists from web phases 7–14 — **no new backend**. Topology graph is explicitly out of scope.

---

## Problem Frame

M3 closed the read/write asymmetry on resource creation by shipping all 28 wizards. M2 closed the verb-action asymmetry. The remaining mobile gap is **observability and CRD-detail parity** — when an oncall sees a workload alert, today they can browse the workload, see live events, tail logs from a single pod, and edit YAML — but they cannot:

- Plot CPU / memory / network metrics over a time window without opening Grafana on a desktop
- Run an ad-hoc LogQL search across multiple pods (only single-pod tail today)
- Run the diagnostics rules engine to see why a Pod is failing (CrashLoopBackOff, ImagePullBackOff, Pending PVC, etc.) and what it impacts
- Inspect Argo CD or Flux CD application sync state, history, and managed resources beyond the verb-action sheet
- Read service mesh routing, mTLS posture, or golden signals
- Browse expiring certificates with their resolved annotation thresholds
- See ExternalSecret live drift status, sync state per store, or per-store rate / cost-tier
- Review Kyverno + Gatekeeper compliance score, violations browser, severity breakdown
- See Trivy or Kubescape vulnerability reports

Each of these is part of an oncall's normal "find the root cause and stabilize" loop. Every gap that pushes them to a laptop adds minutes to MTTR for incidents that are commonly diagnosed in 30 seconds with the right view.

---

## Requirements

- R1. **Per-resource Metrics tab on every workload detail screen.** Pod, Deployment, StatefulSet, DaemonSet, Node, and PVC detail screens render a Metrics tab with `fl_chart`-rendered LineCharts over `GET /v1/monitoring/query_range`. Time presets `1h / 6h / 24h / 7d`. Step computed via web's exact formula `max(round(rangeSec / 1000), 15)` so backend cache hits match.
- R2. **LogQL editor + label browser.** A "Logs" surface separate from the M1 single-pod live tail. Two modes — search (cascading namespace → pod → container dropdowns + free-text contains + severity filter) and raw LogQL. Server-side namespace RBAC enforced; mobile UX makes namespace selection visible (non-admin users always see a namespace dropdown bound to label-values). 4096-char query limit surfaced inline. Volume histogram via `fl_chart` BarChart. **No live tail in this surface** — the existing M1 WebSocket pod tail covers that case.
- R3. **Diagnostics blast-radius.** "Diagnose" entry point on every supported workload (Deployment / StatefulSet / DaemonSet / Pod / Service / PersistentVolumeClaim — the six kinds the backend accepts). Renders the rules-engine checklist + a two-tier blast-radius flat list (`directlyAffected` / `potentiallyAffected`). Failed items expanded; passed collapsed. Optional `GET /v1/diagnostics/{ns}/summary` for namespace-level "failing pods" tile.
- R4. **GitOps detail parity.** Detail screens for Argo Applications + Flux Kustomizations + Flux HelmReleases keyed on the `tool:namespace:name` composite ID. Tabs: Overview / Resources / History / Events. Flux HelmRelease hides the History + Resources tabs (backend doesn't track them). Async commit enrichment via `GET /v1/gitops/commits` is informational and may be deferred to PR-4j polish if blocked. ApplicationSet detail screen for the Argo-only AppSet read endpoint. Verb actions (sync / suspend / rollback) already shipped in M2 — M4 only adds the rich detail tabs.
- R5. **Service mesh detail surfaces.** Mesh status dashboard (Istio + Linkerd side-by-side cards), routing list + detail keyed on the `mesh:ns:kindCode:name` composite ID, mTLS posture per namespace (requires `?namespace=`), golden signals 4-up LineChart per Service (requires `?namespace=&service=&mesh=`). Golden signals are rendered on **Service detail screens only** in M4 (the `service=` parameter is unambiguous there). On Pods/Deployments, no golden-signals tab — service derivation belongs in M5.
- R6. **Cert-manager observatory.** Certificates list (with `?status=expiring` pre-filter URL param), Certificate detail (with resolved-threshold attribution + sub-resource sections for CertificateRequests / Orders / Challenges), Issuers + ClusterIssuers lists. Wizards already shipped in PR-3e; M4 is read-side only, plus surfacing the renew / reissue actions when RBAC permits.
- R7. **ESO advanced read surfaces.** Live ExternalSecret detail (uses live `driftStatus`, not the list-only `lastObservedDriftStatus`), SecretStore + ClusterSecretStore detail, per-store metrics panel (`GET /v1/externalsecrets/stores/{ns}/{name}/metrics`), ClusterExternalSecret list + detail, PushSecret list + detail (read-only on backend). Drift tri-state (`InSync` / `Drifted` / `Unknown`) renders as 3-color pill — `Unknown` is **never red**; it means the provider doesn't populate `SyncedResourceVersion`. Wizards from PR-3f cover create.
- R8. **Policy compliance browser.** Compliance score dashboard (gauge + severity breakdown by engine), policies list, violations list with namespace/severity filters. Compliance history endpoint (`GET /v1/policies/compliance/history`) is admin-only and returns 503 with body `requires a database` when PostgreSQL isn't configured — distinct "feature not configured" state, not a retry-able error.
- R9. **Trivy + Kubescape vulnerability reports.** Status dashboard, vulnerability list (REQUIRES `?namespace=` — namespace-scoped to avoid OOM on 6000+ VulnerabilityReport CRDs), per-workload detail with image breakdown and per-CVE drill-down. Kubescape reports surface in the same `/v1/scanning/*` endpoint family, discriminated by the `scanners` field. Severity filter chips (Critical / High / Medium / Low / None). 7-day stale threshold on cached scans surfaced inline.
- R10. **Web/Dart isomorphism extends.** Every M4 screen is a port of an existing `frontend/islands/*.tsx` surface. Wire-format models mirror backend struct JSON tags. Drift between the two sides counts as a bug.
- R11. **Cluster-pinning discipline carries.** Every new `FutureProvider` or `RefreshableController` keys on `clusterId` as a field, threads it as `clusterIdOverride` through the repository layer, and re-checks pin at result arrival. The PR-3c interceptor fix (only injects header when absent) is the live invariant; M4 must not regress.
- R12. **Read-only posture preserved.** No new write actions in M4. Existing M2 actions (sync / suspend / rollback / scale / restart / delete) and existing M3 wizards (create) cover writes. Drift Revert (a write action) renders as **disabled** with a "Use desktop to revert drift" tooltip — write paths defer to a future PR.

**Origin actors:** A1 (oncall operator), A2 (cluster admin — the only audience for compliance history + cluster-scoped log queries).

**Origin flows:** F1 (open workload from notification → see metrics + recent events without leaving phone), F2 (alert fires → run LogQL across affected namespace → identify root cause), F3 (Argo app degraded → open Flutter detail → see managed resources + last sync error), F4 (cert expiring soon → tap notification → see resolved threshold attribution + linked issuer).

---

## Scope Boundaries

- **Topology graph** — phone-sized rendering of the 2000-node-capped namespace graph is too much surface for too little oncall value. Operators who need topology pull out a desktop. The web's `?overlay=mesh` and `?overlay=eso-chain` overlays stay desktop-only.
- **Cluster-wide LogQL search WebSocket** (`/ws/logs-search`). The M1 single-pod live tail covers the common "watch this pod" case; cluster-wide live tail at phone size adds complexity without clear oncall value. Defer to M5 if real users ask.
- **Grafana dashboard iframe rendering on phone.** The `/grafana/proxy/*` endpoint stays desktop-only. Mobile shows a "Open in Grafana" deep-link CTA.
- **PromQL textarea / raw query authoring on phone.** Mobile only renders backend-curated metrics from `GET /v1/monitoring/templates/query`. Operators who want ad-hoc PromQL pull out a desktop. The M4 metrics tab is for the canonical CPU/memory/network/latency views, not exploratory query construction.
- **Drift Revert + ESO bulk-refresh trigger** (`POST` actions). Both render as disabled with a tooltip pointing to desktop. Write parity is M5+ scope.
- **Compliance history time-range picker.** When the compliance store is configured (admin only), M4 ships the score and trend over a fixed 7-day default. Custom ranges deferred to M5 polish.
- **Background auto-refresh / polling.** All M4 surfaces refresh on `RefreshIndicator` pull-to-refresh or on `ref.invalidate(...)` from a navigation event. No background polling — would create unbounded Prometheus load.
- **Interactive chart zoom / pan.** The `fl_chart` time-series LineCharts are read-only with fixed time-range presets. Pinch-to-zoom is deferred to M5 if real operators report friction.
- **OIDC mobile flow.** Tracked as a separate follow-up; orthogonal to M4.

### Deferred to Follow-Up Work

- **ESO sync history surface** — the `eso_sync_history` PostgreSQL table is poller-internal; **no read HTTP endpoint exists** in `backend/internal/externalsecrets/handler.go`. Adding `GET /v1/externalsecrets/sync-history` is a backend addendum out of M4 scope. M4 ships the per-store metrics panel instead, which IS exposed (`/v1/externalsecrets/stores/{ns}/{name}/metrics`). If real operators need historical sync visibility on phone, surface it as a backend PR + M5 mobile work.
- **Async commit enrichment** for GitOps history (`GET /v1/gitops/commits`). Functional but informational — if it blocks PR-4e, ship the History tab without commit subjects and add enrichment in PR-4j polish.
- **Cluster-scoped LogQL queries for admins** (the `{job="kubelet"}` no-namespace-label case). M4's editor always binds a namespace selector; admin's "all namespaces" mode is M5 polish.
- **Per-resource service-name autoderivation** for golden signals on Pod / Deployment screens. M4 surfaces golden signals on Service detail only; cross-resource service derivation is M5 polish.

---

## Context & Research

### Relevant Code and Patterns

- `mobile/lib/api/dio_client.dart` — Dio + 4 interceptors (`Cluster`/`CSRF`/`Auth`/`ErrorMapping`). M4 inherits the stack unchanged.
- `mobile/lib/api/resource_repository.dart` — `FutureProvider.autoDispose.family<T, Key>` shape with `clusterIdOverride` threading. Every M4 domain repo follows this.
- `mobile/lib/wizards/wizard_controller.dart` — race-protection patterns (`_dispatchId`, `_disposed`, cluster-pin re-check at result arrival). M4's `RefreshableController` base lifts these into a reusable mixin.
- `mobile/lib/widgets/resource_detail_scaffold.dart` — `DefaultTabController(length: 3)` (Overview/YAML/Events). M4 extends with an optional `extraTabs: List<({String label, Widget body})>` parameter rather than building a v2 scaffold.
- `mobile/lib/wizards/widgets/named_resource_picker.dart` — `FutureProvider`-backed cascading dropdown over `resourceListProvider`. Reused for the LogQL filter bar's namespace/pod/container cascade and the Trivy namespace picker.
- `mobile/lib/wizards/widgets/issuer_picker.dart` (PR-3e) + `store_picker.dart` (PR-3f) — combined namespaced + cluster-scoped picker pattern. M4's certificate / store / external-secret list screens reuse the underlying providers (`issuerListProvider`, `storeListProvider`).
- `mobile/lib/theme/kube_theme_builder.dart` — `KubeColors` ThemeExtension. Every chart color reads from this; banned: `Colors.red`, `Color(0xFF...)` literals in `LineChartBarData`.
- `mobile/pubspec.yaml:43` — `fl_chart: ^0.69.0` already pinned. M4 is its first consumer.

### Institutional Learnings

- **Cluster-pinning at result arrival, not just request initiation** (PR-3c, codified in `WizardController._clusterStillPinned(_PinPhase.postEmission)`). M4's `RefreshableController` base inherits this so chart polls / log queries that switch clusters mid-fetch don't write stale state.
- **`FutureProvider.autoDispose.family<T, Key>` keyed on `clusterId`** (PR-3e `issuerListProvider`, PR-3f `storeListProvider`, PR-3f `policyEngineStatusProvider`). M4's per-domain status providers (`monitoringStatusProvider`, `lokiStatusProvider`, `meshStatusProvider`, etc.) all follow the same shape.
- **Status endpoint per CRD-discovered family.** Eight surfaces (monitoring, Loki, cert-manager, ESO, policy, mesh, GitOps, scanning) all expose a `GET /v1/<family>/status` returning a `detected: ...` field with empty data on `false`. M4 ships **one shared `FeatureUnavailableState` widget** parameterized by `{featureName, helpMessage}` so the install-message UX is consistent across all eight.
- **Drift tri-state colouring** — `Unknown` is informational, not an error. Rendering it red is a UX bug that confuses operators on every ESO store that doesn't populate `SyncedResourceVersion`. M4 maps `InSync` → `KubeColors.success`, `Drifted` → `KubeColors.warning`, `Unknown` → `KubeColors.textMuted`.
- **Backend partial-failure pattern.** `routingResponse.errors` (mesh) and `policiesResponse.errors` (policy) carry per-CRD fetch failures alongside successful results. M4 must surface these as banners, not error states for the whole screen — same defense as PR-3f's `_PartialFetchWarning`.
- **Vault-namespace router collision lesson** (PR-3f). For any M4 provider-specific field that shares a name with a k8s concept (e.g., `namespace` on a Vault store config), errors must route disambiguously. PR-3f's `errorRouter` special-case is the precedent.
- **CronJob @-shorthand vs strict 5-field cron lesson** (PR-3d). Doesn't apply directly to M4, but the underlying lesson does — match each backend handler's exact validation regime, do not assume a "common" library/format.

### External References

- `fl_chart` 0.69.x — https://pub.dev/packages/fl_chart (LineChart + BarChart cover all M4 chart needs; PieChart NOT used for the gauge — custom painter instead).
- LogQL syntax — https://grafana.com/docs/loki/latest/logql/ (mobile builds string templates, no parser; backend tokenizer is the authority).
- PromQL `query_range` — Prometheus library caps datapoints at ~11000 per response; mobile pre-computes `step` to keep `(rangeSec / step) ≤ 1000`.
- `cron.ParseStandard` semantics (Go) — referenced from PR-3d lessons; no direct M4 application but the lesson generalizes.

---

## Key Technical Decisions

- **No new backend.** Every endpoint M4 needs already exists from web phases 7–14. The plan does not add Go code, migrations, or new routes. The single confirmed gap (ESO sync history) is deferred per Scope Boundaries above.
- **`fl_chart` 0.69.0 stays pinned.** Already in `pubspec.yaml` from PR-1a; M4 is its first real consumer. No version bump.
- **One shared `FeatureUnavailableState` widget** in PR-4a. Parameterized by feature name + optional install hint. Used by all 8 per-domain "not detected" empty states. Eliminates the drift mode where each surface invents its own copy.
- **`RefreshableController<TState>` base mixin** in PR-4a. Ports the wizard controller's `_dispatchId` / `_disposed` / cluster-pin re-check into a re-pollable read controller. Every M4 controller that re-fetches on input change (time-range swap, query edit, severity filter) extends this.
- **Step strategy for `query_range`:** `max(ceil(rangeSec / 1000), 15)` rounded to nearest preset (`15s / 30s / 1m / 5m / 15m / 1h`). Mirrors the web's `PromQLQuery.tsx` formula exactly so backend Prometheus cache hits match.
- **LogQL editor builds queries via string template, not a parser.** Web has no LogQL parser; mobile's port is ~30 lines of Dart string assembly. Server tokenizer is the authority on validity. Mobile surfaces backend 400 inline; 4096-char limit gated client-side before POST.
- **Namespace selector is mandatory** on the LogQL editor for non-admin users (backend hard-403s without it). UI presents a `NamedResourcePicker`-style dropdown bound to `resourceListProvider(kind:'namespaces')`; admin users see an "all namespaces" option.
- **Diagnostics renders as a checklist + flat list** (no graph). Failed rules expanded; passed collapsed. Blast radius split into two `ListView` sections (`directlyAffected` / `potentiallyAffected`) sorted by severity then health.
- **Diagnostics entry point gated on the 6 supported kinds.** A const `kDiagnosticsKinds = {Deployment, StatefulSet, DaemonSet, Pod, Service, PersistentVolumeClaim}` lives in PR-4a; the "Diagnose" button on `ResourceDetailScaffold` only renders when `kind ∈ kDiagnosticsKinds`.
- **Golden signals tab on Service detail only** in M4. Pod/Deployment screens skip the golden-signals tab. M5 may add a service-derivation widget (find Services whose `selector` matches the resource's labels) — out of M4 scope.
- **Flux HelmRelease hides History + Resources tabs** at the route-builder level. Backend's `AppDetail.History` and `AppDetail.Resources` are populated only for Argo apps and Flux Kustomizations.
- **Drift Revert renders disabled with a tooltip** pointing to desktop. M4 read-only; write parity for ESO drift is M5+.
- **Trivy/Kubescape virtual scroll, no pagination.** Backend returns all RBAC-filtered findings in one response; mobile uses `ListView.builder` with severity filter chips + a namespace picker bottom-sheet. Pre-fetch summary counts from `/v1/cluster/dashboard-summary` to render a "Loading 1,247 vulnerabilities…" spinner.
- **Compliance history 503 distinguished from network 503.** When `error.message` contains "requires a database", show a permanent "Compliance history requires database storage" empty state (not retry-able). Other 503s stay retry-able.
- **No background polling.** All surfaces refresh on `RefreshIndicator` pull-to-refresh or on navigation-driven `ref.invalidate(...)`. No timers.
- **`CancelToken` on every Dio call from a `RefreshableController`.** Cancelled in `ref.onDispose` so cluster switches mid-fetch don't write stale state. PR-4a establishes the pattern via the base mixin.
- **Composite ID URL encoding.** GitOps + mesh + policy IDs contain colons; `Uri.encodeComponent(id)` everywhere these flow into go_router path segments. PR-4a ships the helper.
- **Per-domain repo pattern** mirrors `ResourceRepository` exactly: domain class with Dio injected, ApiError unwrapping, `clusterIdOverride` threading. PR-4b–PR-4j each ship one (`MonitoringRepository`, `LokiRepository`, `MeshRepository`, etc.).

---

## Open Questions

### Resolved During Planning

- **Step strategy for query_range:** mirror the web formula exactly (`max(ceil(rangeSec/1000), 15)` rounded to preset). Resolved via repo research §2.1.
- **LogQL editor namespace enforcement:** mandatory namespace selector for non-admin; admin sees "all namespaces". Resolved via flow analysis Q2 + backend `enforceQueryNamespaces` confirmation.
- **ESO Revert action:** disabled in M4 with desktop tooltip; defer write parity to M5+. Resolved via R12 read-only posture.
- **Flux history tab:** hide on `tool == 'fluxcd'`. Resolved via flow analysis I2.
- **Golden-signals service source:** Service detail screens only in M4. Resolved via flow analysis Q5.
- **Compliance history 503 handling:** check `error.message` for `requires a database` → permanent empty state. Resolved via flow analysis Q7.
- **Trivy/Kubescape pagination:** virtual scroll + severity chips, no backend paging. Resolved via flow analysis Q8.
- **CancelToken pattern:** mandate via `RefreshableController` base. Resolved via flow analysis Q9.
- **`FeatureUnavailableState` widget:** PR-4a ships once, used by 8 surfaces. Resolved via flow analysis Q10 + repo research §1.4.
- **ESO sync-history endpoint:** confirmed absent. Surface deferred per Scope Boundaries.

### Deferred to Implementation

- **Resource-dashboard endpoint shape per kind.** `GET /v1/monitoring/resource-dashboard?kind=...&namespace=...&name=...` — exact response shape per kind not enumerated in repo research. PR-4b reads the live response and builds the metric-tile model from it.
- **Mesh `errors` map rendering.** Backend's `routingResponse.errors` is a free-form `map[string]string` (e.g. `{"istio/VirtualService": "forbidden"}`). PR-4f decides the banner format once the actual error strings are sampled.
- **Curated metrics templates.** `GET /v1/monitoring/templates/query?kind=pod` returns the backend-curated query set per resource kind. PR-4b reads the live response — curated templates may evolve without coordinated mobile changes.
- **Per-store metrics panel detail shape.** `GET /v1/externalsecrets/stores/{ns}/{name}/metrics` returns rate + cost-tier data; PR-4h reads the live response and decides chart vs. KV table per field.
- **Trivy/Kubescape report page-load size on real clusters.** PR-4j tests against homelab; if a 500-image cluster's response is too large for first-paint, add a backend addendum for namespace-paged scanning in M5.
- **AppSet child resource list expansion.** ApplicationSet detail may want to expand inline to show generated Applications. PR-4e ships the AppSet detail screen with a tap-to-list-children pattern; full inline expansion deferred if the response shape is awkward.

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Shared primitives (PR-4a)

```
RefreshableController<TState> (mixin)
  ├─ _dispatchId, _disposed (race protection — ported from WizardController)
  ├─ _safeSet(state) — no-op when disposed
  ├─ refresh(): bumps _dispatchId, re-fires the fetch
  ├─ cancelInflight(): CancelToken.cancel
  └─ _clusterStillPinned(phase) — pre/post-emission re-check

TimeRangePicker
  └─ SegmentedButton<TimePreset>{15m, 1h, 6h, 24h, 7d}
       + "Custom..." → showDateRangePicker

KubeLineChart
  ├─ takes List<MetricsSeries> (typed record per series)
  ├─ KubeColors token per severity
  └─ no zoom/pan (M5 polish)

KubeBarChart (used by log volume + ESO sync rate)

KubeGaugeRing (custom painter)
  └─ used by ESO drift dashboard, policy compliance score

DomainListScaffold<T>
  └─ FutureProvider<List<T>> + itemBuilder
  └─ RefreshIndicator + LoadingState + ErrorStateView

FeatureUnavailableState({featureName, helpMessage})
  └─ shared empty state for all 8 not-detected surfaces

CompositeId helper
  └─ encode/decode for "tool:ns:name", "engine:ns:kind:name", "mesh:ns:kindCode:name"
```

### Per-domain composition (PR-4b through PR-4j)

```
For each domain (monitoring, loki, diagnostics, gitops, mesh, certmanager, eso, policy, scanning):
  api/<domain>_repository.dart
    └─ <Domain>Repository class — Dio injected, clusterIdOverride threading
    └─ <domain>StatusProvider FutureProvider — drives FeatureUnavailableState
    └─ per-list / per-detail FutureProvider.family<...>

  features/<domain>/...
    └─ <Domain>ListScreen (DomainListScaffold composition)
    └─ <Domain>DetailScreen (per-domain tabs / sections)
    └─ widgets/ (domain-specific cards)

  routing/<domain>_routes.dart (List<GoRoute>)
    └─ /clusters/:clusterId/<domain>/...
    └─ RBAC route guard (mirrors _WizardRouteGuard)

  test/<domain>/... (controller + widget tests per existing pattern)
```

### Detail-screen tab extension

`ResourceDetailScaffold` gets one new optional parameter:

```
ResourceDetailScaffold(
  ...existing args...,
  extraTabs: [
    if (kind ∈ {Pod, Deployment, StatefulSet, DaemonSet, Node})
      (label: 'Metrics', body: MetricsTab(...)),
    if (kind == 'Pod')
      (label: 'Logs', body: PodLogsTabRedirect(...)),  // links to LogQL editor with pre-filled filter
  ],
)
```

This avoids a v2 scaffold and keeps Overview/YAML/Events stable.

---

## Implementation Units

### U1. PR-4a — Shared primitives + `RefreshableController` + chart widgets

**Goal:** Land everything PR-4b through PR-4j depends on so subsequent PRs are pure feature ports.

**Requirements:** R10 (isomorphism foundation), R11 (cluster pinning), R12 (read-only base).

**Dependencies:** None (extends M3 infrastructure).

**Files:**
- Create: `mobile/lib/widgets/refreshable_controller.dart` — mixin/base class with `_dispatchId`, `_disposed`, `_clusterStillPinned(phase)`, `cancelInflight`, `refresh`. Lifts the wizard controller's race-protection patterns into a reusable read controller.
- Create: `mobile/lib/widgets/time_range_picker.dart` — `SegmentedButton<TimePreset>` over `{15m, 1h, 6h, 24h, 7d}` with a "Custom..." escape hatch. Emits typed `({DateTime start, DateTime end})` records.
- Create: `mobile/lib/widgets/kube_line_chart.dart` — `fl_chart` LineChart wrapper over a typed `MetricsSeries` record. Reads colors from `KubeColors` exclusively. Default no-zoom; horizontal time axis with formatted tick labels.
- Create: `mobile/lib/widgets/kube_bar_chart.dart` — same shape for `BarChart`. Used by log volume histogram + ESO per-store rate panel.
- Create: `mobile/lib/widgets/kube_gauge_ring.dart` — custom-painted donut gauge for the ESO drift dashboard hero + policy compliance score. Single number in center, ring fills proportional. Color from `KubeColors` per severity bucket.
- Create: `mobile/lib/widgets/domain_list_scaffold.dart` — `<T>` generic list scaffold over `FutureProvider<List<T>>`. `RefreshIndicator` + `LoadingState` + `ErrorStateView` + truncation banner. Mirrors `ResourceListScaffold` but for non-`/v1/resources/` domains.
- Create: `mobile/lib/widgets/feature_unavailable_state.dart` — shared "feature not installed" empty state. Used by all 8 status-driven surfaces.
- Create: `mobile/lib/util/composite_id.dart` — encode/decode helpers for `tool:ns:name` / `engine:ns:kind:name` / `mesh:ns:kindCode:name`. URL-encoding wrapper for go_router path safety.
- Modify: `mobile/lib/widgets/resource_detail_scaffold.dart` — add optional `extraTabs: List<({String label, Widget body})>?` parameter. Backwards-compatible default keeps the 3-tab Overview/YAML/Events layout.
- Test: `mobile/test/widgets/refreshable_controller_test.dart` — race protection + cluster-pin re-check + dispose-during-fetch.
- Test: `mobile/test/widgets/time_range_picker_test.dart` — preset selection + custom range.
- Test: `mobile/test/widgets/kube_line_chart_test.dart` — KubeColors token use; no hardcoded colors.
- Test: `mobile/test/util/composite_id_test.dart` — encode/decode round-trip for all 3 ID schemes.

**Approach:**
- `RefreshableController` is a mixin (not a base class) so per-domain controllers can extend `AutoDisposeFamilyNotifier<TState, TKey>` and mix in race protection.
- `KubeLineChart` accepts `List<MetricsSeries>` where `MetricsSeries = ({String label, List<({DateTime t, double v})> points, KubeSeverity severity})`. Severity drives color via a `KubeColors` lookup table.
- `FeatureUnavailableState` ships with 8 named factory constructors (`.monitoring()`, `.loki()`, `.certManager()`, etc.) so per-surface call sites stay one-liner.
- `CompositeId` encode/decode is two static methods; the URL-encoding step matters because go_router path segments otherwise mangle colons.

**Patterns to follow:**
- `mobile/lib/wizards/wizard_controller.dart` — race-protection patterns to lift into `RefreshableController`.
- `mobile/lib/widgets/resource_list_scaffold.dart` — list scaffold conventions for `DomainListScaffold`.
- `mobile/lib/theme/kube_theme_builder.dart` — color tokens; banned `Colors.red` etc.
- `mobile/lib/wizards/widgets/named_resource_picker.dart` — autocomplete-style dropdown pattern (reused in PR-4c).

**Test scenarios:**
- Happy path: `RefreshableController` fires fetch, captures `_dispatchId`, async result lands and writes state.
- Race: `refresh()` mid-fetch bumps `_dispatchId`; original Future's late completion drops on identity mismatch.
- Dispose: `ref.onDispose` fires while Future in-flight; post-await `_safeSet` no-ops; no `StateError`.
- Cluster pin: pre-emission mismatch aborts cleanly; post-emission mismatch surfaces "request landed on pinned cluster" message.
- `TimeRangePicker`: tap preset → emits typed range; tap custom → opens date picker → emits operator-chosen range.
- `KubeLineChart`: render with a 2-series `MetricsSeries` list; verify each line's color is from `KubeColors.success / .warning / .error` and not a hardcoded literal.
- `CompositeId.encode('argocd', 'argocd', 'my-app')` round-trips through `decode` → same tuple. Same for `engine:ns:kind:name`. URL-encoded form survives go_router path matching.
- Edge case: `RefreshableController.cancelInflight()` cancels the active CancelToken; the awaiting controller observes `DioException(type: cancel)` and treats it as no-op (not as a failure).

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean (~10 new tests; no pre-existing regressions).
- `make check-themes` clean.

---

### U2. PR-4b — Per-resource Metrics tab + `MonitoringRepository`

**Goal:** Operator opens a Pod/Deployment/StatefulSet/Node/PVC detail screen, taps Metrics tab, and sees CPU/memory/network charts over a selectable time range.

**Requirements:** R1, R10, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/monitoring_repository.dart` — `MonitoringRepository` class wrapping `GET /v1/monitoring/{status,query,query_range,templates/query,resource-dashboard}`. `clusterIdOverride` threading. Step computation helper (`_computeStep(rangeSec) → seconds`).
- Create: `mobile/lib/features/observability/metrics/metrics_controller.dart` — `RefreshableController`-extending notifier. State: `({TimeRange range, List<MetricsSeries> series, AsyncStatus status, Object? error})`. Re-fires fetch on time-range change.
- Create: `mobile/lib/features/observability/metrics/metrics_tab.dart` — `Widget` rendered as the Metrics tab body. `TimeRangePicker` at top + 4-up `KubeLineChart` grid (CPU / Memory / Network in / Network out, or per-kind variants).
- Modify: `mobile/lib/widgets/resource_detail_scaffold.dart` — wire the `extraTabs` parameter (already added in U1) at the call sites for Pod, Deployment, StatefulSet, DaemonSet, Node, PVC detail screens.
- Test: `mobile/test/features/observability/metrics/metrics_controller_test.dart` — happy path, 502 on excessive range, time-range swap mid-fetch, monitoring-not-detected status flow.
- Test: `mobile/test/features/observability/metrics/metrics_tab_test.dart` — widget test pumping into a `ResourceDetailScaffold` with mock dio.
- Test: `mobile/test/api/monitoring_repository_test.dart` — `_computeStep` formula matches web exactly (15s / 30s / 1m / 5m / 15m / 1h tier boundaries).

**Approach:**
- `_computeStep(rangeSec)` returns `max(ceil(rangeSec / 1000), 15)` rounded to nearest of `{15, 30, 60, 300, 900, 3600}`. Web's `PromQLQuery.tsx` formula. Same formula → same Prometheus cache hits.
- Resource-dashboard endpoint (`GET /v1/monitoring/resource-dashboard?kind=&namespace=&name=`) returns the curated metric set per kind. Mobile reads the live response and builds the chart panel from it — no hardcoded per-kind PromQL.
- Metrics tab gracefully handles "monitoring not detected" via the shared `FeatureUnavailableState.monitoring()`.
- Empty-vector result (`result: []`) renders a "No data for this time range" banner instead of a flat-zero chart. All-zero non-empty result renders a chart with the zero line — operator can distinguish.

**Patterns to follow:**
- `frontend/islands/PromQLQuery.tsx` — step formula, time presets, empty-vector handling.
- `frontend/islands/MetricCard.tsx` — animated counter UX (deferred polish; M4 ships static numeric labels).
- `mobile/lib/wizards/wizard_controller.dart` — race-protection inheritance via `RefreshableController`.
- `mobile/lib/api/resource_repository.dart` — repo class shape.

**Test scenarios:**
- Happy path: tap Metrics tab on a Pod detail, default 1h range, mock `/v1/monitoring/query_range` returns 4 series → 4 charts render with `KubeColors`-themed lines.
- Edge case: empty vector (`result: []`) → "No data for this time range" banner, no chart artifact.
- Edge case: 7d range with default step → step is `1h` (3600s); `(7*86400)/3600 = 168` datapoints, well under 1000.
- Error path: `502 Bad Gateway` from Prometheus timeout → `ErrorStateView` with retry; retry re-fires with same range.
- Error path: `503 Service Unavailable` (monitoring not configured) → `FeatureUnavailableState.monitoring()`.
- Race: time-range swap (1h → 7d) while initial fetch in-flight; original 1h response drops via `_dispatchId`; final state matches 7d.
- Cluster-pin: switch active cluster mid-fetch → request landed on pinned cluster (X-Cluster-ID set explicitly); post-emission message surfaces; no stale state.
- Integration: `_computeStep(60)` = 15, `_computeStep(3600)` = 15, `_computeStep(21600)` = 30, `_computeStep(86400)` = 300, `_computeStep(604800)` = 1800. Matches web.

**Verification:**
- Above checks clean.
- Smoke against homelab: open a busy Pod's Metrics tab, verify CPU/memory chart renders with non-zero data over 1h.

---

### U3. PR-4c — LogQL editor + label browser (`LokiRepository`)

**Goal:** Operator runs an ad-hoc log search across multiple pods in a namespace, with autocomplete-driven label/value selection or raw LogQL.

**Requirements:** R2, R10, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/loki_repository.dart` — wraps `GET /v1/logs/{status,query,labels,labels/{name}/values,volume}`. Mandatory namespace param threading for non-admin.
- Create: `mobile/lib/features/observability/logs/log_search_controller.dart` — `RefreshableController` for the editor state. Holds `({String? namespace, String mode (search|logql), String query, TimeRange range, AsyncValue<LogQueryResult>})`. Re-fires on Submit.
- Create: `mobile/lib/features/observability/logs/log_search_screen.dart` — top-level surface; not a tab, but a drawer entry under "Observability". Composes `LogFilterBar` + volume histogram + result list.
- Create: `mobile/lib/features/observability/logs/log_filter_bar.dart` — namespace dropdown (mandatory non-admin), pod dropdown (cascading), container dropdown (cascading), severity filter chips, free-text contains, mode toggle (Search / LogQL). Builds the LogQL string on Submit.
- Create: `mobile/lib/features/observability/logs/log_volume_histogram.dart` — `KubeBarChart` over `/v1/logs/volume` response. Best-effort; if 503, hide the panel.
- Create: `mobile/lib/features/observability/logs/log_results_list.dart` — virtual-scroll list of stream lines. 5000-line cap surfaced inline as a banner when reached.
- Modify: `mobile/lib/routing/app_router.dart` — wire `/clusters/:clusterId/logs` (new top-level route).
- Modify: `mobile/lib/routing/domain_sections.dart` — add Observability section + Logs entry.
- Test: `mobile/test/features/observability/logs/log_filter_bar_test.dart` — search-mode query construction (matchers + contains + severity), mode swap clears query, namespace cascade refetches pod list.
- Test: `mobile/test/features/observability/logs/log_search_controller_test.dart` — happy path; 403 on missing namespace (non-admin); 400 on backend tokenizer reject; 4096-char client-side gate.
- Test: `mobile/test/api/loki_repository_test.dart` — endpoint path correctness; namespace param injection.

**Approach:**
- LogQL building is string template. `search` mode: build `{namespace="X",pod=~"Y.*",container="Z"}` + `|= "free-text"` + `| level=~"(?i)error"` (severity). `logql` mode: pass operator's raw text through.
- Label browser: `GET /v1/logs/labels` for keys, `GET /v1/logs/labels/{name}/values?namespace=X` for values. Cascade triggers refetch on namespace change.
- Volume histogram step is from backend allowlist `15s/30s/1m/5m/15m/30m/1h/6h/1d` (Loki handler enforces). Mobile picks the largest step that produces ≤120 buckets for the selected range.
- 5000-line response cap: Loki backend default; mobile renders `LogResults` with a "Showing 5000 of N lines (refine query for full results)" banner when count == 5000.

**Patterns to follow:**
- `frontend/islands/LogFilterBar.tsx` — query-construction logic; cascading dropdown timing.
- `frontend/islands/LogExplorer.tsx` — orchestrator composition; status-gated surface visibility.
- `frontend/islands/LogVolumeHistogram.tsx` — bucket-step strategy.
- `mobile/lib/wizards/widgets/named_resource_picker.dart` — cascading dropdown pattern.

**Test scenarios:**
- Happy path: namespace=`app`, pod=`web-`, container=`web`, severity=`error`, free-text=`timeout` → query body `{namespace="app",pod=~"web-.*",container="web"} |= "timeout" | level=~"(?i)error"`.
- Edge case: empty result `{streams: []}` → "No log lines for this query" banner.
- Edge case: 5000-line cap hit → banner with refine hint.
- Error path: backend 403 on missing namespace (non-admin) → "Namespace required for log queries" inline error.
- Error path: backend 400 on invalid LogQL → surface backend error message verbatim.
- Edge case: 4096-char query → client-side gate before POST.
- Edge case: Loki not detected (status `false`) → `FeatureUnavailableState.loki()` instead of editor.
- Integration: namespace swap → pod dropdown clears + refetches; container dropdown clears.
- Mode toggle: switching search → LogQL preserves the constructed query as the editable raw text; switching back drops raw edits.

**Verification:**
- Smoke against homelab: search for `error` lines in `kube-system` over 6h; verify result count + volume histogram align with `kubectl logs` spot-check.

---

### U4. PR-4d — Diagnostics blast-radius surface

**Goal:** Operator taps "Diagnose" on a failing workload, sees the rules-engine checklist (CrashLoopBackOff / ImagePullBackOff / Pending Pod / etc.) and a flat list of directly + potentially affected resources.

**Requirements:** R3, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/diagnostics_repository.dart` — wraps `GET /v1/diagnostics/{ns}/{kind}/{name}` and `GET /v1/diagnostics/{ns}/summary`.
- Create: `mobile/lib/features/observability/diagnostics/diagnostics_controller.dart` — `RefreshableController`; state: `({Target target, AsyncValue<DiagnosticResponse>})`.
- Create: `mobile/lib/features/observability/diagnostics/diagnostics_screen.dart` — composes `DiagnosticChecklist` + `BlastRadiusPanel`. Reachable from `ResourceDetailScaffold`'s "Diagnose" button.
- Create: `mobile/lib/features/observability/diagnostics/diagnostic_checklist.dart` — failed rules expanded with `message` + `detail` + `remediation` + linked-resource chips; passed rules collapsed.
- Create: `mobile/lib/features/observability/diagnostics/blast_radius_panel.dart` — two `ListView` sections (`Directly affected` / `Potentially affected`) sorted by health then severity.
- Create: `mobile/lib/features/observability/diagnostics/namespace_summary_screen.dart` — optional namespace-level "Failing Pods in namespace" tile reachable from a namespace detail. Labels make pod-only scope explicit.
- Modify: `mobile/lib/widgets/resource_detail_scaffold.dart` — add a "Diagnose" button rendered only when `kind ∈ kDiagnosticsKinds` (`{Deployment, StatefulSet, DaemonSet, Pod, Service, PersistentVolumeClaim}`).
- Modify: `mobile/lib/routing/app_router.dart` — wire `/clusters/:clusterId/diagnostics/:namespace/:kind/:name`.
- Test: `mobile/test/features/observability/diagnostics/diagnostics_controller_test.dart` — happy path; 400 on unsupported kind; 15s timeout handling; 404 on missing resource.
- Test: `mobile/test/features/observability/diagnostics/blast_radius_panel_test.dart` — sorting; empty blast radius (rules passed); 100+ items render with `ListView.builder`.

**Approach:**
- "Diagnose" button visibility gated on the const set of 6 supported kinds. CronJob / Ingress / HPA detail screens never show it.
- Blast radius is two flat `ListView` sections. No graph render. Sorting: failed health first, then critical severity, then alphabetical.
- Namespace summary endpoint (`/diagnostics/{ns}/summary`) is **pod-only** per backend implementation. Label the surface "Failing Pods in namespace" not "Failing Resources" to avoid confusing operators expecting Deployments.
- Linked-resource chips on a failed rule (`Pod → owning Deployment`) tap-deep-link via `kindDetailPath` in `domain_sections.dart`.

**Patterns to follow:**
- `frontend/islands/DiagnosticWorkspace.tsx` — input + auto-run on URL params.
- `frontend/islands/DiagnosticChecklist.tsx` — expand/collapse + linked-resource chips.
- `frontend/islands/BlastRadiusPanel.tsx` — two-tier list rendering (NOT the graph; web has both, mobile takes only the list).

**Test scenarios:**
- Happy path: Pod with CrashLoopBackOff → checklist shows 1 failed (CrashLoopBackOff) + 5 passed; blast radius shows owning Deployment as `directlyAffected`.
- Edge case: All rules pass + empty blast radius → "No issues detected" success state.
- Edge case: 100+ items in `directlyAffected` → `ListView.builder` virtual scroll, no perf hit.
- Error path: tap Diagnose on a Service that doesn't exist → 404 → `ErrorStateView` with retry.
- Error path: backend 15s timeout fires → 500 with timeout message → `ErrorStateView` with retry; remind operator the diagnostics surface is best-effort.
- Edge case: kind not in the supported 6 → button hidden at `ResourceDetailScaffold` level; route deep-link 400 with "Diagnostics not supported for this kind" message.
- Integration: tap a linked-resource chip on a failed rule → navigates to that resource's detail via `kindDetailPath`.

**Verification:**
- Smoke against homelab: tap Diagnose on a known-failing pod; verify checklist fires + blast radius matches the topology overlay (cross-checked against web).

---

### U5. PR-4e — GitOps detail (Argo + Flux + AppSets)

**Goal:** Operator opens a GitOps Application from a list, sees Overview / Resources / History / Events tabs (Resources + History hidden for Flux HelmRelease).

**Requirements:** R4, R10, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/gitops_repository.dart` — `GET /v1/gitops/{status,applications,applications/{id},commits,applicationsets,applicationsets/{id}}`.
- Create: `mobile/lib/features/gitops/gitops_status_provider.dart` — `FutureProvider.autoDispose.family<GitOpsStatus, ClusterIdKey>`.
- Create: `mobile/lib/features/gitops/applications_list_screen.dart` — `DomainListScaffold<NormalizedApp>`; filter chips (Argo / Flux / Both) + sync-state chips (Healthy / Degraded / Synced / OutOfSync).
- Create: `mobile/lib/features/gitops/application_detail_screen.dart` — composite-ID-driven detail. Tabs: Overview, Resources (Argo + Flux Kustomization only), History (Argo + Flux Kustomization only), Events.
- Create: `mobile/lib/features/gitops/applicationsets_list_screen.dart` — Argo-only AppSet list; gated on `argoCD.appSetsAvailable`.
- Create: `mobile/lib/features/gitops/applicationset_detail_screen.dart` — AppSet detail with tap-to-list-children link (full inline expansion deferred per Open Questions).
- Modify: `mobile/lib/routing/app_router.dart` — wire `/clusters/:clusterId/gitops/{applications,applications/:id,applicationsets,applicationsets/:id}`.
- Modify: `mobile/lib/routing/domain_sections.dart` — add GitOps section + entries.
- Test: per-screen widget tests + repo test.

**Approach:**
- Composite ID (`tool:ns:name`) parsed via `CompositeId` helper from PR-4a; URL-encoded in route params.
- Tab visibility gated on `app.tool`: HelmRelease hides History + Resources. Kustomization shows all 4 tabs. Argo apps show all 4 tabs.
- Async commit enrichment (`GET /v1/gitops/commits?shas=`) is informational; PR-4e ships history without commit subjects, PR-4j adds enrichment if smooth.
- Verb actions (sync / suspend / rollback) already shipped in M2; this PR only adds the Detail tab views.

**Patterns to follow:**
- `frontend/islands/GitOpsAppDetail.tsx` — tab structure, `useWsRefetch` pattern (mobile uses `RefreshIndicator` instead).
- `frontend/islands/GitOpsApplications.tsx` — list filtering.
- `frontend/islands/GitOpsAppSetDetail.tsx` — AppSet shape.
- `frontend/lib/gitops-types.ts` — wire types to mirror.

**Test scenarios:**
- Happy path: Argo app `argocd:argocd:my-app` → all 4 tabs render; Resources lists managed resources; History lists revisions.
- Edge case: Flux HelmRelease `flux-hr:flux-system:my-release` → History + Resources tabs hidden; Overview + Events render.
- Edge case: Flux Kustomization `flux-ks:flux-system:my-ks` → all 4 tabs render; Resources from `status.inventory.entries`.
- Edge case: AppSet detail with 0 child apps → empty state.
- Error path: GitOps not detected → `FeatureUnavailableState.gitops()`.
- Composite ID: `argocd:argocd:my-app` URL-encodes to `argocd%3Aargocd%3Amy-app` for the route segment; decodes back correctly on detail-screen mount.
- Integration: tap a managed resource chip → deep-link to that resource's detail via `kindDetailPath`.

**Verification:**
- Smoke against homelab: open the existing demo Argo app + Flux Kustomization, walk all 4 tabs.

---

### U6. PR-4f — Service mesh (routing + mTLS + golden signals)

**Goal:** Operator opens the mesh dashboard, sees Istio + Linkerd side-by-side; drills into routing rules; views mTLS posture per namespace; sees golden signals 4-up chart on Service detail screens.

**Requirements:** R5, R10, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/mesh_repository.dart` — `GET /v1/mesh/{status,routing,routing/{id},policies,mtls,golden-signals}`.
- Create: `mobile/lib/features/mesh/mesh_dashboard_screen.dart` — top-level mesh status. Two cards (Istio / Linkerd); empty state when neither installed.
- Create: `mobile/lib/features/mesh/routing_list_screen.dart` — filter by mesh type / namespace.
- Create: `mobile/lib/features/mesh/route_detail_screen.dart` — composite-ID-driven (`mesh:ns:kindCode:name`); Overview + raw YAML + Effect annotation.
- Create: `mobile/lib/features/mesh/mtls_posture_screen.dart` — namespace selector mandatory; renders the three-source attribution.
- Create: `mobile/lib/features/mesh/golden_signals_tab.dart` — 4-up `KubeLineChart` (request rate / success rate / p50 latency / p99 latency). Rendered as an extra tab on **Service detail screens only**.
- Modify: `mobile/lib/widgets/resource_detail_scaffold.dart` call site for Service detail — wire the golden-signals tab when mesh is detected.
- Modify: `mobile/lib/routing/app_router.dart` + `domain_sections.dart` — Mesh section.
- Test: per-screen widget tests + repo test.

**Approach:**
- `mesh:ns:kindCode:name` composite ID via `CompositeId` helper.
- mTLS posture screen: namespace selector at top, mTLS posture matrix below. Backend hard-requires `?namespace=`; if not selected, show namespace picker prompt.
- Golden signals: 2-second backend timeout means partial-success is normal (`MissingQueries` non-empty). Render with a "Some metrics unavailable" banner when partial.
- `routingResponse.errors` partial-failure map surfaced as a banner on the routing list.

**Patterns to follow:**
- `frontend/islands/MeshDashboard.tsx` — two-engine status layout.
- `frontend/islands/MeshRoutingList.tsx`, `MeshRouteDetail.tsx`.
- `frontend/islands/MTLSPosture.tsx` — three-source attribution rendering.
- PR-3f's `_PartialFetchWarning` — error-map banner pattern.

**Test scenarios:**
- Happy path: Istio + Linkerd both installed → two cards on dashboard; routing list mixes both; route detail by ID.
- Edge case: only Istio installed → Linkerd card shows "not installed"; routing list filtered.
- Edge case: golden signals partial (`MissingQueries: [success_rate]`) → 3 charts render + banner "1 metric unavailable".
- Error path: mTLS posture without `?namespace=` → 400 → namespace picker prompt.
- Error path: `routingResponse.errors: {"istio/VirtualService": "forbidden"}` → routing list renders successful kinds + banner.
- Integration: tap a Service from the resource list → Service detail; golden-signals tab visible because mesh detected; tap tab → 4 charts.
- Cluster-pin: switch active cluster mid-fetch on golden signals → request landed on pinned cluster.

**Verification:**
- Smoke against homelab (Istio installed): walk routing list + tap a route + view mTLS posture for `app` namespace + golden signals on a known service.

---

### U7. PR-4g — Cert-manager observatory

**Goal:** Operator browses certificates with expiry-state badges, taps a cert to see resolved threshold attribution + sub-resources, browses Issuers / ClusterIssuers.

**Requirements:** R6, R10, R11, R12.

**Dependencies:** U1. Reuses `issuerListProvider` from PR-3e (mobile).

**Files:**
- Create: `mobile/lib/api/certmanager_repository.dart` — `GET /v1/certificates/{status,certificates,certificates/{ns}/{name},issuers,clusterissuers,expiring}`.
- Create: `mobile/lib/features/certmanager/certificates_list_screen.dart` — filter chips (All / Expiring / Failed); search by name + namespace + issuer.
- Create: `mobile/lib/features/certmanager/certificate_detail_screen.dart` — tabs: Overview (with `expiresAt` / `notBefore` / `renewBefore` + threshold attribution), Sub-Resources (CertificateRequests / Orders / Challenges), Events. Renew + Reissue buttons rendered when RBAC permits.
- Create: `mobile/lib/features/certmanager/issuers_list_screen.dart` — combined Issuers + ClusterIssuers list; reuses existing `issuerListProvider`.
- Create: `mobile/lib/features/certmanager/expiring_screen.dart` — top-level "expiring soon" view from `/certificates/expiring`.
- Modify: `mobile/lib/routing/app_router.dart` + `domain_sections.dart` — Certificates section.
- Test: per-screen widget + repo tests.

**Approach:**
- `?status=expiring` URL param on certificates list pre-filters to the warn/critical buckets.
- Expiry badge color: `KubeColors.success` (>warn days), `.warning` (warn–critical), `.error` (≤critical days). Threshold values come from backend's resolved annotations, not hardcoded.
- Sub-resource sections: CertificateRequests / Orders / Challenges may be empty due to RBAC. Detail screen shows a "Some sub-resources unavailable (insufficient permissions)" hint when the parent is in `Issuing` or `Failed` state but no CRs are visible.
- Renew + Reissue: existing M2 actions wired through `executeAction`. M4 only surfaces them; doesn't add new write paths.

**Patterns to follow:**
- `frontend/islands/CertificatesList.tsx` — list filtering + search.
- `frontend/islands/CertificateDetail.tsx` — threshold attribution rendering.
- Past learning #9 — drift-status `Unknown` parallel; cert-manager has its own "no provider data" states to handle similarly (sub-resource empty + parent issuing).

**Test scenarios:**
- Happy path: 50 certs across 5 namespaces, 3 expiring → list renders + filter chip shows 3.
- Edge case: cert in `Issuing` with no CertificateRequests visible → hint banner about RBAC.
- Edge case: cert thresholds resolve to `crit >= warn` (`thresholdConflict: true`) → resolved-thresholds badge shows "Conflict — using defaults" + tooltip.
- Edge case: `status=expiring` URL param pre-filters list on mount.
- Integration: tap an Issuer chip on cert detail → deep-link to issuer detail (or list if cluster).
- Edge case: cert-manager not detected → `FeatureUnavailableState.certManager()`.

**Verification:**
- Smoke against homelab: walk the certs list with the homelab's existing `web-tls` + `letsencrypt-staging` certs; verify expiry badge colors.

---

### U8. PR-4h — ESO read-side parity

**Goal:** Operator browses ExternalSecrets with live drift status, taps a store to see metrics + cluster-scoped variants.

**Requirements:** R7, R10, R11, R12.

**Dependencies:** U1. Reuses `storeListProvider` from PR-3f.

**Files:**
- Create: `mobile/lib/api/eso_repository.dart` — full read surface: `externalsecrets`, `externalsecrets/{ns}/{name}` (live drift), `clusterexternalsecrets[/{name}]`, `stores[/{ns}/{name}]`, `stores/{ns}/{name}/metrics`, `clusterstores[/{name}]`, `clusterstores/{name}/metrics`, `pushsecrets[/{ns}/{name}]`.
- Create: `mobile/lib/features/eso/dashboard_screen.dart` — `KubeGaugeRing` synced/total + 4 secondary cards (SyncFailed / Stale / Drifted / Unknown) + failure table.
- Create: `mobile/lib/features/eso/external_secrets_list_screen.dart` — uses `lastObservedDriftStatus` from list response.
- Create: `mobile/lib/features/eso/external_secret_detail_screen.dart` — uses live `driftStatus` from detail. Revert button **disabled** with "Use desktop to revert drift" tooltip per R12.
- Create: `mobile/lib/features/eso/cluster_external_secrets_list_screen.dart` + detail — cluster-scoped variant.
- Create: `mobile/lib/features/eso/stores_list_screen.dart` + detail (with metrics panel sub-tab).
- Create: `mobile/lib/features/eso/cluster_stores_list_screen.dart` + detail + metrics panel.
- Create: `mobile/lib/features/eso/push_secrets_list_screen.dart` + detail (read-only).
- Create: `mobile/lib/features/eso/store_metrics_panel.dart` — `KubeBarChart` for rate + KV table for cost-tier (PR reads live response shape).
- Modify: routing + drawer.
- Test: per-screen widget + repo tests.

**Approach:**
- Drift tri-state colour: `InSync` → `KubeColors.success`, `Drifted` → `KubeColors.warning`, `Unknown` → `KubeColors.textMuted`. **`Unknown` is never red.**
- List screens render `lastObservedDriftStatus` (poller hint, fast). Detail screens fetch live `driftStatus` (impersonated user, slower but authoritative).
- Per-store metrics endpoint: rate (chart) + cost-tier (KV). PR reads live response and decides chart-vs-table per field.
- ChainPanel / `?overlay=eso-chain` topology view explicitly **out of M4 scope** (Scope Boundaries).
- Push secrets are read-only on the backend; mobile mirrors this (no PR scope).

**Patterns to follow:**
- `frontend/islands/ESODashboard.tsx` — gauge + secondary cards layout.
- `frontend/islands/ESOExternalSecretsList.tsx` + `ESOExternalSecretDetail.tsx`.
- `frontend/islands/ESOStoreMetricsPanel.tsx`.
- PR-3f's `store_picker.dart` partial-fetch handling — pattern carries when M4 composes multiple ESO endpoints.

**Test scenarios:**
- Happy path: 100 ExternalSecrets, 90 InSync / 5 Drifted / 5 Unknown → dashboard gauge shows 90% sync; secondary cards render counts.
- Drift tri-state: render verifies `Unknown` uses `KubeColors.textMuted` (not error).
- Edge case: list screen shows `lastObservedDriftStatus`; tap → detail fetches live `driftStatus` (which may differ).
- Edge case: ESO not detected → `FeatureUnavailableState.eso()`.
- Action: Revert button rendered disabled; tap shows "Use desktop to revert drift" snackbar.
- Cluster-pin: switch cluster mid-fetch on a list refresh → pinned-cluster discipline holds.
- Integration: tap a SecretStore chip on an ExternalSecret detail → deep-link to the store's detail (existing `storeListProvider` reused).

**Verification:**
- Smoke against homelab: walk dashboard + ES list + drill into a store with the metrics panel.

---

### U9. PR-4i — Policy compliance + violations

**Goal:** Operator views compliance score, browses policies with severity filter, browses violations.

**Requirements:** R8, R10, R11, R12.

**Dependencies:** U1. Reuses `policyEngineStatusProvider` from PR-3f.

**Files:**
- Create: `mobile/lib/api/policy_repository.dart` — `GET /v1/policies/{status,/(list),violations,compliance,compliance/history}`.
- Create: `mobile/lib/features/policy/dashboard_screen.dart` — compliance score (`KubeGaugeRing`) + by-engine breakdown + severity breakdown.
- Create: `mobile/lib/features/policy/policies_list_screen.dart` — filter chips (engine / severity / blocking).
- Create: `mobile/lib/features/policy/violations_list_screen.dart` — namespace + severity filters; virtual scroll.
- Create: `mobile/lib/features/policy/violation_detail_screen.dart` — single-violation context (target resource link + remediation hint).
- Create: `mobile/lib/features/policy/compliance_history_screen.dart` — admin-only; uses `KubeLineChart` over `/policies/compliance/history`. 503 with `requires a database` → permanent empty state.
- Modify: routing + drawer (admin-only entry for compliance history).
- Test: per-screen tests; **dedicated test for the 503 distinguished-error path**.

**Approach:**
- Composite ID (`engine:ns:kind:name`) via `CompositeId` helper.
- Violations have no server-side `id`; mobile derives a stable key from `(policy, rule, namespace, kind, name)` for `ListView.builder`.
- Engine intersection — apply the same lesson from PR-3f: if Kyverno-only template policies show on a Gatekeeper-only cluster, render with engine availability check.
- Compliance history: 503 distinguished error path is a dedicated test. Body string match (`requires a database`) → permanent empty state.

**Patterns to follow:**
- `frontend/islands/PolicyDashboard.tsx` + `ComplianceDashboard.tsx` + `ComplianceTrendChart.tsx`.
- PR-3f's `pickTemplate` engine intersection — recurs as a render-time check.

**Test scenarios:**
- Happy path: 50 policies / 200 violations → dashboard renders gauge + breakdowns.
- Edge case: Kyverno-only policy on cluster with only Gatekeeper installed → policy detail surfaces "Engine not installed" badge.
- Error path: compliance history 503 with "requires a database" body → permanent "Compliance history requires database storage" empty state, no retry button.
- Error path: compliance history 503 from network → retry-able `ErrorStateView`.
- Performance: 1000-violation render via `ListView.builder` virtual scroll, no perf hit.
- Cluster-pin: refresh mid-fetch on violations list, verify discipline.

**Verification:**
- Smoke against homelab (Kyverno installed): walk dashboard + violations + drill into one.

---

### U10. PR-4j — Trivy + Kubescape vulnerability reports + final wire-up

**Goal:** Operator views vulnerability dashboard, drills into a workload's CVE breakdown.

**Requirements:** R9, R10, R11, R12.

**Dependencies:** U1.

**Files:**
- Create: `mobile/lib/api/scanning_repository.dart` — `GET /v1/scanning/{status,vulnerabilities,vulnerabilities/{namespace}/{kind}/{name}}`. Mandatory `?namespace=` on the list endpoint.
- Create: `mobile/lib/features/scanning/dashboard_screen.dart` — overall status + summary counts pre-fetched from `/v1/cluster/dashboard-summary`.
- Create: `mobile/lib/features/scanning/vulnerabilities_list_screen.dart` — namespace picker bottom-sheet (mandatory before list loads); severity filter chips; virtual scroll over `WorkloadVulnSummary[]`.
- Create: `mobile/lib/features/scanning/vulnerability_detail_screen.dart` — per-workload images + CVE list; tap a CVE → external link.
- Modify: routing + drawer.
- Test: per-screen tests; large-payload virtual-scroll perf check.
- **Wire-up:** `mobile/lib/widgets/resource_detail_scaffold.dart` — add the optional Metrics + Logs + Diagnose entry points from PR-4b/c/d at the right call sites if any were deferred. Final integration pass.
- **Async commit enrichment for GitOps history** (deferred from PR-4e) — if scope allows.
- **CLAUDE.md Build Progress** — append M4 entry summarizing all 10 PRs.

**Approach:**
- Namespace required on list endpoint — namespace picker bottom-sheet on first list visit; selection persists to `activeNamespaceProvider` (reused from M1).
- 7-day stale threshold surfaced as a banner when latest scan timestamp older than 7 days.
- Severity filter chips: Critical / High / Medium / Low / None. State persists across screen visits.
- Virtual scroll with `ListView.builder` — no backend pagination (out of M4 scope).
- Kubescape compliance: same `/v1/scanning/*` family, discriminated by `scanners` field. Same screens with a different filter chip set when `scanners == kubescape`.

**Patterns to follow:**
- `frontend/islands/VulnerabilityDashboard.tsx` + `VulnerabilityDetail.tsx`.
- M3 PR-3f's `_NoEngineEmpty` empty state pattern (reused via `FeatureUnavailableState.scanning()`).

**Test scenarios:**
- Happy path: namespace `app` selected, 50 workloads, 1247 vulnerabilities → list renders virtual; severity filter switches counts.
- Edge case: scanner not installed → `FeatureUnavailableState.scanning()`.
- Edge case: stale scan (>7 days) → banner.
- Performance: 5000-vulnerability response renders without dropped frames.
- Edge case: namespace not selected → bottom-sheet prompt on first list visit.
- Integration: Trivy + Kubescape both installed → discriminator chips switch the data source.

**Verification:**
- Smoke against homelab (Trivy installed): walk dashboard + namespace picker + drill into a vulnerable image's CVEs.
- Final M4 integration: open every M4 surface from the drawer, confirm RBAC gating, confirm cluster pinning, confirm `make check-themes` clean.

---

## System-Wide Impact

- **Interaction graph:** New drawer sections (Observability, GitOps, Mesh, Certificates, External Secrets, Policy, Vulnerabilities). Every M4 screen routes through the existing dio interceptor stack with `X-Cluster-ID` pinning. `ResourceDetailScaffold` gains an optional `extraTabs` parameter — backwards-compatible default is the existing 3-tab layout. M2's verb actions and M3's wizards remain unchanged.
- **Error propagation:** Per-domain status providers gate every surface — feature-not-detected renders `FeatureUnavailableState`, never a confusing empty list. Backend partial-failure maps (`routingResponse.errors`, `policiesResponse.errors`, ESO `_PartialFetchWarning` carryover) surface as inline banners, not whole-screen failures. Compliance history 503 with "requires a database" body distinguishes from network 503.
- **State lifecycle risks:** `RefreshableController` race protection (`_dispatchId` / `_disposed` / cluster-pin re-check at result arrival) is the only line of defense against cluster-switch-mid-fetch, time-range-swap-mid-poll, and dispose-during-await StateErrors. Every M4 controller MUST extend the base mixin. CancelToken cancellation in `ref.onDispose` is mandatory.
- **API surface parity:** Every endpoint M4 consumes already exists. Web, CLI, mobile all share the same backend audit + RBAC + impersonation. Adding mobile as a third caller doesn't introduce a new write path. **No backend changes.**
- **Integration coverage:** Tests cover happy/empty/error paths per surface, cluster-pin discipline per controller, virtual-scroll perf for large lists (Trivy reports, policy violations), partial-failure banner rendering for mesh + policy, and the 503-distinguished-error path for compliance history.
- **Unchanged invariants:** PR-1b's auth interceptor stack, PR-1c's cluster context + cluster-pinning discipline, PR-1d/1e's read-side resource fetching (`resourceListProvider` / `resourceGetProvider`), PR-1f's WebSocket log tail, PR-1g's CI, M2's action infrastructure and YAML apply controller, M3's wizard infrastructure, theme generator pipeline. M4 adds read surfaces; nothing alters existing reads or writes.
- **Web/Dart isomorphism extends.** Each M4 surface mirrors a `frontend/islands/*.tsx` component at the wire-format level. Future drift counts as a bug.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Step-strategy drift between web and mobile causes Prometheus cache misses (slower charts on mobile only). | `_computeStep(rangeSec)` mirrors web's `PromQLQuery.tsx` formula exactly. Unit-test all 5 preset boundaries. PR-3f's R10 isomorphism enforcement carries. |
| LogQL editor's namespace selector regression on a backend update (e.g., backend stops requiring namespace param) | Loki backend `enforceQueryNamespaces` is a documented critical security boundary. If backend behavior changes, mobile UX simplification is straightforward. Server enforcement is the source of truth. |
| Diagnostics 15s timeout fires on a slow cluster | Surface backend timeout error verbatim; offer retry. Operator workaround is to scope diagnostics narrower (use specific kind, not summary). |
| GitOps "errors map" backend partial-failure produces unfamiliar error strings on mobile | PR-4f reads sample real backend errors from homelab + production-like cluster; banner format finalized in PR-4j polish. |
| Trivy/Kubescape report payload size exceeds phone parse capacity on a large cluster | Pre-fetch summary counts from `/v1/cluster/dashboard-summary`; render "Loading 1247 vulnerabilities…" spinner. If real reports lag, add a backend `?page=` parameter in M5 + mobile pagination. |
| ESO sync history surface user-expected but absent | Scope Boundaries already defers this; if real operators ask, file a backend issue for the read endpoint and revisit in M5. The per-store metrics panel ships a partial signal (rate per refresh interval). |
| Compliance history 503 string match fragile (relies on `requires a database` body wording) | Dedicated test asserts the distinction. If backend changes the body string, the test fires immediately. Backend addendum to add a structured error code is a polish item, not a blocker. |
| Cluster-pin race protection missed in a per-domain repo | `RefreshableController` mixin enforces the discipline at the base class. Per-domain repo tests explicitly test "cluster switch mid-fetch → pinned cluster header" pattern (mirrors PR-3c's regression test). |
| Drift-status `Unknown` rendered as red (UX bug from PR-3f learnings #9) | PR-4h dedicated test asserts color tokens per drift state. Code review explicitly checks. |
| Per-domain `errorRouter` namespace collision (PR-3f Vault-namespace lesson) | M4 read-side has fewer cases (no provider-spec forms), but if any per-domain detail screen exposes a field whose name collides with a k8s concept, follow PR-3f's special-case routing pattern. |
| `fl_chart` 0.69.x bug surfaces only at scale | Pinned version unchanged; PR-4j integration smoke tests against a cluster with realistic data volume. M5 polish bumps if needed. |
| Background polling regressions (introduced silently) | Plan explicitly bans background polling. PR review checklist enforces. Retry is operator-driven via `RefreshIndicator`. |

---

## Documentation / Operational Notes

- `CLAUDE.md` "Build Progress" appended after each PR-4a → PR-4j merge.
- `mobile/README.md` gets a "Observability" section explaining the metrics tab pattern, the LogQL editor, and the diagnostics workflow. Not a per-surface manual — operators learn each surface by using it (web parity).
- No backend operational changes. Every endpoint M4 consumes was shipped in earlier phases. The deferred ESO sync-history endpoint is documented in Scope Boundaries; tracked for backend addendum + M5 mobile work.
- No Helm chart changes. FCM / Universal Link / signing infrastructure from PR-1g is unchanged.
- `plans/mobile-app.md` "What lands in PR-1+" section gets a one-line append noting M4 complete after PR-4j merges.
- Per-surface smoke-test against homelab is part of each PR's verification (mirrors M3 PR sequence).

---

## Sources & References

- **Origin document:** [plans/mobile-app.md](mobile-app.md) — master plan; M4 scope is the "M4 (4–6 wk)" line, with topology graph dropped per recent decision.
- **Sibling plans:**
  - [plans/mobile-app-m1-pr-sequence.md](mobile-app-m1-pr-sequence.md) — M1 foundation (auth, cluster context, resource list/detail, single-pod log tail).
  - [plans/mobile-app-m2-pr-sequence.md](mobile-app-m2-pr-sequence.md) — M2 verb actions + YAML editor + cluster-pinning hardening.
  - [plans/mobile-app-m3-pr-sequence.md](mobile-app-m3-pr-sequence.md) — M3 all-28-wizards.
- **Related backend phase plans:**
  - [plans/phase-7-advanced-observability.md](phase-7-advanced-observability.md) — Loki + diagnostics surface + endpoints (M4 consumes).
  - [plans/phase-8-policy-governance.md](phase-8-policy-governance.md) — Kyverno + Gatekeeper compliance scoring + endpoints.
  - [plans/phase-9-gitops.md](phase-9-gitops.md) — Argo + Flux endpoints + composite IDs.
  - [plans/phase-10-security-scanning.md](phase-10-security-scanning.md) — Trivy + Kubescape endpoints.
  - [plans/cert-manager-integration.md](cert-manager-integration.md) — observatory + endpoints.
  - [plans/external-secrets-operator-integration.md](external-secrets-operator-integration.md) — ESO read surface + drift semantics.
  - [plans/service-mesh-observability.md](service-mesh-observability.md) — mesh endpoints + composite IDs + golden signals.
- **Related code (mobile):**
  - `mobile/lib/wizards/wizard_controller.dart` — race-protection patterns to lift into `RefreshableController`.
  - `mobile/lib/api/resource_repository.dart` — repo class shape mirrored by every M4 domain repo.
  - `mobile/lib/widgets/resource_detail_scaffold.dart` — extension point for Metrics + Logs + Diagnose tabs.
  - `mobile/lib/wizards/widgets/named_resource_picker.dart` — cascading-dropdown pattern for LogQL filter bar.
  - `mobile/lib/wizards/widgets/issuer_picker.dart`, `store_picker.dart` — list providers reused by certificate + ESO list screens.
  - `mobile/lib/theme/kube_theme_builder.dart` — `KubeColors` token contract (banned: hardcoded chart colors).
  - `mobile/pubspec.yaml:43` — `fl_chart: ^0.69.0` already pinned.
- **Related code (web — port targets):**
  - `frontend/islands/PromQLQuery.tsx`, `MetricCard.tsx` — metrics surfaces.
  - `frontend/islands/LogExplorer.tsx`, `LogFilterBar.tsx`, `LogVolumeHistogram.tsx` — logs.
  - `frontend/islands/DiagnosticWorkspace.tsx`, `DiagnosticChecklist.tsx`, `BlastRadiusPanel.tsx` — diagnostics.
  - `frontend/islands/GitOpsAppDetail.tsx`, `GitOpsApplications.tsx`, `GitOpsAppSetDetail.tsx` — GitOps.
  - `frontend/islands/MeshDashboard.tsx`, `MeshRoutingList.tsx`, `MeshRouteDetail.tsx`, `MTLSPosture.tsx` — service mesh.
  - `frontend/islands/CertificatesList.tsx`, `CertificateDetail.tsx`, `IssuersList.tsx` — cert-manager.
  - `frontend/islands/ESODashboard.tsx`, `ESOExternalSecretsList.tsx`, `ESOExternalSecretDetail.tsx`, `ESOStoreMetricsPanel.tsx` — ESO.
  - `frontend/islands/PolicyDashboard.tsx`, `ComplianceDashboard.tsx`, `ComplianceTrendChart.tsx` — policy.
  - `frontend/islands/VulnerabilityDashboard.tsx`, `VulnerabilityDetail.tsx` — Trivy + Kubescape.
- **Related backend handlers (read-side, all `X-Cluster-ID`-aware):**
  - `backend/internal/server/routes.go` — endpoint registration map.
  - `backend/internal/monitoring/handler.go` — PromQL query/range, templates, resource-dashboard.
  - `backend/internal/loki/handler.go` — LogQL + label endpoints + namespace enforcement.
  - `backend/internal/loki/security.go` — `enforceQueryNamespaces` (the documented security boundary).
  - `backend/internal/diagnostics/diagnostics.go` — rules engine + 6-kind allowlist.
  - `backend/internal/gitops/handler.go` — Argo + Flux + AppSet detail.
  - `backend/internal/servicemesh/types.go` + `handler.go` — routing + mTLS + golden signals.
  - `backend/internal/certmanager/types.go` + `handler.go` — observatory + expiring + threshold attribution.
  - `backend/internal/externalsecrets/handler.go` — full ESO read surface.
  - `backend/internal/policy/types.go` + `handler.go` — engine status + violations + compliance.
  - `backend/internal/scanning/types.go` + `handler.go` — Trivy + Kubescape.
- **External docs:**
  - `fl_chart` 0.69.x — https://pub.dev/packages/fl_chart
  - LogQL — https://grafana.com/docs/loki/latest/logql/
  - PromQL `query_range` — https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries
- **Related PRs/issues:** M1 series (PR-1a … PR-1g), M2 series (PR-2a, PR-2b), M3 series (PR-3a … PR-3f). M4 builds on all of them.
