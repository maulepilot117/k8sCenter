---
date: 2026-04-29
topic: external-secrets-operator
---

# External Secrets Operator integration

## Summary

A feature-complete External Secrets Operator integration under `/security/external-secrets/*`: observatory + per-provider wizards + chain topology graph + Notification Center alerting + four operational lenses (drift detection, sync diff history, bulk refresh, per-store rate / quota awareness). End state covers everything popular operators actually use; phased delivery is acceptable.

---

## Problem Frame

Production secrets in real clusters rarely live in raw `Secret` resources — they're synced from HashiCorp Vault, AWS Secrets Manager / Parameter Store, Azure Key Vault, GCP Secret Manager, or one of a long tail of SaaS / on-prem stores via the External Secrets Operator (ESO). Today, an operator answering "is this secret healthy?" or "when did it last sync?" has to drop to `kubectl get externalsecret -A`, decode the `status.conditions` array by hand, then `kubectl describe` to chase the SecretStore reference, then trace which Pods mount the synced k8s Secret to know what's at risk.

When something flaps — an auth method expires, a Vault path moves, a provider rate-limits — the only feedback channel is a CRD condition that nobody is reading until a workload errors out. There's no observatory surface, no alerting wired to anything operators actually watch, no creation UX that doesn't require hand-rolling a YAML manifest, no way to see at a glance which workloads break if a particular store goes down, and no audit history that survives a CRD reconcile.

Two existing observatory phases give us the pattern: cert-manager (Phase 11A: CRD discovery, normalized types, RBAC, 30s cache; Phase 11B: per-provider creation wizards; Phase 13: per-resource annotation overrides) and service mesh (Phase 12: per-CRD-group RBAC, sync-state notifications, topology overlay). ESO observatory is the natural next member of that family — same pipeline, expanded entity set, and one new pattern (persistent sync history) that this feature establishes for future observatory work.

---

## Actors

- A1. **Platform admin** — full-cluster visibility. Creates and maintains `ClusterSecretStore` resources for the org's source systems, configures default alert thresholds, audits credential rotations, runs bulk refresh actions during failover events.
- A2. **Tenant operator** — per-namespace ESO operations. Creates `ExternalSecret` resources for their workloads, picks from the `SecretStore` / `ClusterSecretStore` resources made available by the platform admin, sees alerts for their own namespace's sync failures, drills the chain graph to understand workload impact.
- A3. **ESO controller** — the system actor we observe. Performs source-system authentication and sync; produces `status.conditions`, `status.refreshTime`, `status.syncedResourceVersion`. We never replace its work or hold its credentials.

---

## Key Flows

- F1. **Create an ExternalSecret via wizard**
  - **Trigger:** Tenant operator clicks "New ExternalSecret" on the list page.
  - **Actors:** A2.
  - **Steps:** Pick a target namespace; pick a `SecretStore` or `ClusterSecretStore` from the visible (RBAC-filtered) list; pick which keys to sync (with type-ahead against the store's available paths when supported by the provider); set the target `Secret` name and refresh interval; preview the rendered manifest in Monaco; apply via server-side apply.
  - **Outcome:** ExternalSecret is created; the first sync attempt is observable on the detail page on the next ESO reconcile (typically within the resource's refresh interval).
  - **Covered by:** R12, R13, R14.

- F2. **Investigate a sync failure**
  - **Trigger:** Notification Center alert fires (`externalsecret.sync_failed`) and the operator clicks through.
  - **Actors:** A2.
  - **Steps:** Land on the ExternalSecret detail page; see the failure reason from `status.conditions`; review the persistent sync history timeline (last N attempts with outcomes); follow the linkout to the source system's UI for deeper audit; identify whether the failure is auth (store-side), data (key not found), or transient (timeout).
  - **Outcome:** Operator has enough signal to decide between "fix the store config", "fix the ExternalSecret spec", or "wait for the next retry".
  - **Covered by:** R6, R7, R20, R21.

- F3. **Bulk refresh after a Vault failover**
  - **Trigger:** Platform admin completes a Vault failover and wants to force-resync every ExternalSecret backed by the affected store.
  - **Actors:** A1.
  - **Steps:** Open the SecretStore (or ClusterSecretStore) detail page; click "Refresh all dependent ExternalSecrets"; confirm via the standard destructive-action dialog; observe the fan-out as each ExternalSecret in the result set transitions through `Refreshing` to `Synced` (or surfaces a fresh failure).
  - **Outcome:** Every ExternalSecret backed by the store has been force-synced via the ESO `force-sync` annotation; the action is recorded in the audit log with the admin's identity.
  - **Covered by:** R26, R27.

- F4. **Drill the chain to find affected workloads**
  - **Trigger:** A SecretStore goes unhealthy; operator wants to know which workloads break.
  - **Actors:** A1, A2.
  - **Steps:** Open the SecretStore detail page; switch to the "Chain" tab to see the topology graph; trace upstream to the auth Secret and downstream to ExternalSecrets → synced k8s Secrets → consuming workloads (Pods / Deployments / StatefulSets / DaemonSets); identify the blast radius.
  - **Outcome:** Operator has a definitive list of workloads at risk and can prioritize remediation.
  - **Covered by:** R9, R10, R11.

---

## Requirements

**Observatory (read paths)**
- R1. The backend lists every ESO entity in scope: `ExternalSecret`, `ClusterExternalSecret`, `SecretStore`, `ClusterSecretStore`, and `PushSecret` (read-only — see Scope Boundaries) — across all namespaces visible to the requesting user.
- R2. CRD presence is auto-detected; if ESO is not installed, the `/security/external-secrets/*` routes render an "ESO not detected" empty state with installation guidance, never 5xx.
- R3. List and detail responses are RBAC-filtered using the same `CanAccessGroupResource` permissive-read model as Phase 11A — k8s RBAC governs visibility; spec details (including auth method config) are visible to anyone with CRD-list permission. Note: auth-method spec references a k8s `Secret` for credentials; the credentials themselves stay behind that Secret's own RBAC and are never returned by these endpoints.
- R4. Read paths use a singleflight + short cache (matching the Phase 11A 30s default) so a request fan-out doesn't multiply load on the API server.
- R5. Each entity surfaces a normalized status enum (`Synced` / `SyncFailed` / `Refreshing` / `Stale`) plus the underlying ESO condition reason and message, last-successful-sync timestamp, and source-store reference.

**Sync history (persistent timeline)**
- R6. Per-`ExternalSecret` sync attempts are persisted in PostgreSQL with outcome (`success` / `failure` / `partial`), reason, message, timestamp, and the diff key-set (see R21). `partial` denotes a sync where some keys were retrieved successfully and others were rejected by the provider (e.g., partial path-permission denial). Retention default matches the existing audit log (90 days).
- R7. The detail page renders the history as a reverse-chronological timeline. Each entry links to the source system's UI for the same time window when the provider supports a deep-link shape (Vault path URL, AWS console, Azure portal, GCP console, 1Password item).
- R8. The timeline survives an ExternalSecret renaming or namespace move via UID-stable storage; restoring a deleted ExternalSecret does not resurrect old history (UID changes).

**Chain visualization (topology)**
- R9. A `Chain` view (per ExternalSecret, per SecretStore, and as a standalone page) renders the secret chain as a graph: auth `Secret` → `SecretStore` / `ClusterSecretStore` → `ExternalSecret` → synced `Secret` → consuming workloads (Pods / Deployments / StatefulSets / DaemonSets / Jobs / CronJobs).
- R10. The chain reuses the Phase 7B topology layout / SVG renderer; new edge types extend the existing `EdgeType` enum without changing default-response semantics for callers who don't request the chain. When a chain would exceed the Phase 7B 2000-node cap (e.g., a widely-shared `ClusterSecretStore`), the response truncates and surfaces a `truncated: true` flag with the omitted-node count so the UI can render a "showing N of M" notice rather than a silently-clipped graph.
- R11. Chain rendering is RBAC-aware — nodes the user can't see are omitted; edges that would cross an invisible node are dropped silently rather than rendered as orphans.

**Wizards**
- R12. A universal `ExternalSecret` wizard creates new ExternalSecrets against any visible SecretStore / ClusterSecretStore — provider-agnostic. The wizard supports cert-manager-style preview-then-apply with Monaco YAML preview.
- R13. Per-provider `SecretStore` and `ClusterSecretStore` wizards exist for the top + medium tier (12 providers): HashiCorp Vault (token / kubernetes / approle / jwt / cert auth methods), AWS Secrets Manager, AWS Parameter Store, Azure Key Vault (managed identity / service principal), GCP Secret Manager (workload identity / service account key), Akeyless, Doppler, 1Password Connect, Bitwarden Secrets Manager, CyberArk Conjur, Kubernetes provider (cross-namespace secret syncing within the cluster, distinct from the k8s API itself), Infisical.
- R14. Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, custom webhook) are creatable via the existing YAML editor with provider-templates seeded for convenience; no per-provider wizard.

**Alerting**
- R15. Notification Center events fire by default for failure transitions: `externalsecret.sync_failed`, `externalsecret.stale` (sync older than threshold), `secretstore.unhealthy`, `clustersecretstore.unhealthy`.
- R16. Recovery transitions also fire by default: `externalsecret.recovered`, `secretstore.recovered`, `clustersecretstore.recovered` (separate dedupe keys from the failure events so recovery alerts aren't suppressed by a recently-cleared failure).
- R17. Per-resource annotation overrides on `ExternalSecret`, `SecretStore`, and `ClusterSecretStore` adjust the defaults: `kubecenter.io/eso-stale-after-minutes` (default: 2× refresh interval; falls back to 2h when refresh interval is unset, matching the ESO controller default), `kubecenter.io/eso-alert-on-recovery` (default: true). Resolution chain matches Phase 13 (resource > store > clusterstore > package default).
- R18. Lifecycle events (`created`, `deleted`, `first_synced`) are OFF by default but can be opted in per resource via `kubecenter.io/eso-alert-on-lifecycle`.
- R19. Invalid annotation values log and silently fall through to the next resolution layer (no `thresholdConflict` flag — that condition is reserved for resolved warn-vs-crit ordering conflicts where warn ≥ crit, matching Phase 13 parity).

**Drift detection + sync diff (key-level)**
- R20. The detail page surfaces a tri-state drift status: `Drifted` when the synced k8s Secret's `resourceVersion` differs from `status.syncedResourceVersion` (the Secret was edited out-of-band since the last sync), `InSync` when they match, and `Unknown` when `status.syncedResourceVersion` is empty (the provider implementation does not populate it; drift cannot be determined). `Drifted` surfaces in the dashboard drift tile with a note that the next successful sync will overwrite the local edit; `Unknown` surfaces with a note that the provider does not expose drift state.
- R21. Each persisted history entry carries a key-level diff: which keys were `added`, `removed`, or `changed` between the previous synced data and this sync's data. Values are never stored or surfaced — only key names.

**Bulk refresh actions**
- R22. The SecretStore / ClusterSecretStore detail page exposes "Refresh all dependent ExternalSecrets" — fans out the ESO `force-sync` annotation to every ExternalSecret backed by the store. Result set is RBAC-filtered to the requesting user's visible ExternalSecrets, and the confirmation dialog renders the resolved target list (count + namespaces) before execution so the user sees exactly what scope they're acting on. The fan-out reports per-resource outcome as `{succeeded, failed, skipped}` with the failure reason for each (RBAC-rejected at apply time, optimistic-lock conflict, transient API error, etc.); the audit log captures both the request scope and the post-fan-out outcome — not just the action trigger.
- R23. The namespace overview page exposes "Refresh all ExternalSecrets in this namespace" with the same fan-out behavior.
- R24. Bulk actions go through the impersonating client; the action and the result set are written to the existing audit log with the requesting user identity.

**Per-store rate / quota awareness**
- R25. Per-store access counts are tracked from ESO's Prometheus metrics (`externalsecret_sync_calls_total`, `externalsecret_sync_calls_error`, and the per-provider request metrics ESO publishes) via the existing Prometheus proxy — rate-counter only, no source-system API calls. Surfaces on the SecretStore detail and the dashboard. When Prometheus is unavailable, the panel degrades to "rate metrics offline" rather than fabricating zero-counts.
- R26. For paid-tier providers (AWS Secrets Manager, AWS Parameter Store, GCP Secret Manager, Azure Key Vault), a cost-tier estimate is computed from static rate cards baked into the codebase (refreshed periodically). Displayed as "estimated $X.XX in the last 24h" with caveats. For self-hosted / flat-rate providers (Vault, Kubernetes provider), the estimate is omitted and only request-rate is shown.

**Dashboard**
- R27. A dashboard page at `/security/external-secrets` aggregates: total ExternalSecrets cluster-wide with health histogram (Synced / Refreshing / SyncFailed / Stale / Drifted), SecretStore inventory by provider type (donut), recent sync failures (last 24h table), top failure-reason groupings, and per-store cost-tier estimate cards.
- R28. The dashboard is RBAC-filtered using the same model as the list pages — non-admins see counts scoped to namespaces they can read.

**Helm + docs**
- R29. The Helm `ClusterRole` gains explicit `list` / `watch` grants for `external-secrets.io/v1` CRDs (the 5 entities in R1), matching the Phase 11A / Phase 12 pattern. No new write verbs; bulk actions use the impersonating user client.
- R30. CLAUDE.md gains a Phase entry covering the integration; README.md "Security & Governance" features list mentions the ESO observatory; roadmap item #8 is checked off.

**Persistence migration**
- R31. A `golang-migrate` step adds `external_secrets` to the Notification Center event-source enum and creates the `eso_sync_history` table (`uid`, `namespace`, `name`, `attempt_at`, `outcome`, `reason`, `message`, `diff_keys_added`, `diff_keys_removed`, `diff_keys_changed`) with the appropriate indexes for the timeline-by-uid and the recent-failures-cluster-wide queries.

---

## Acceptance Examples

- AE1. **Covers R5, R15.** Given an ExternalSecret in `apps` namespace currently `Synced`, when the source store starts returning `permission denied` and ESO records `status.conditions[Ready] = False`, the next observatory poll flips the normalized status to `SyncFailed` and emits a single `externalsecret.sync_failed` event to Notification Center with the reason from the condition. Dedupe is keyed by `(uid, kind="externalsecret.sync_failed")`; a subsequent reason change while still failing does not re-fire.
- AE2. **Covers R16.** Given an ExternalSecret currently `SyncFailed` with a notification already dispatched, when the next reconcile produces `Ready = True`, the observatory emits a single `externalsecret.recovered` event using a separate dedupe key — not suppressed by the prior failure dedupe entry.
- AE3. **Covers R17, R19.** Given an ExternalSecret with annotation `kubecenter.io/eso-stale-after-minutes: "5"` and the referenced SecretStore with annotation `kubecenter.io/eso-stale-after-minutes: "60"`, when the ExternalSecret hasn't synced for 6 minutes, the stale event fires (resource-level annotation wins). Given the resource sets `kubecenter.io/eso-alert-on-recovery: "potato"`, the parser rejects it and the resource silently falls through to the store-level value; the detail response carries no `thresholdConflict` flag because that flag is reserved for resolved warn-vs-crit ordering conflicts (warn ≥ crit), not for invalid-value rejection.
- AE4. **Covers R20.** Given an ExternalSecret has synced and produced a k8s Secret at resourceVersion `12345`, when an operator runs `kubectl edit secret` and bumps it to `12346`, the next observatory poll sees `syncedResourceVersion=12345` ≠ live `resourceVersion=12346` and flags the ExternalSecret as `Drifted` on the detail page. The next successful sync clears the flag.
- AE5. **Covers R22, R24.** Given a SecretStore with 47 dependent ExternalSecrets across 6 namespaces and a tenant operator who can list ExternalSecrets in only 2 of those namespaces, when the operator triggers "Refresh all dependent ExternalSecrets" on the store detail page, the fan-out targets only the ExternalSecrets in the 2 namespaces the operator can see; the audit log records the action with that scoped result set.

---

## Success Criteria

- An operator can open `/security/external-secrets` and answer "is anything broken right now and which workloads are affected?" in under 30 seconds without dropping to `kubectl`.
- An operator can create a working ExternalSecret pointing at a Vault / AWS / Azure / GCP store via wizard without writing YAML by hand.
- A `secretstore.unhealthy` notification reaches the operator's chosen channel (in-app, Slack, email, webhook per Notification Center config) within one polling cycle of the underlying Ready transition.
- After a Vault failover, the platform admin can force-resync every dependent ExternalSecret via one click, with the action audit-logged.
- The chain graph correctly renders the auth Secret → store → ExternalSecret → synced Secret → consuming workloads relationship for at least the four most common shapes (Vault token auth + Pod env mount; AWS IAM auth + Deployment env mount; GCP workload identity + StatefulSet volume mount; ClusterSecretStore + cross-namespace consumer).
- An operator can see per-store request rate on the SecretStore detail and the dashboard, plus a cost-tier estimate where the underlying store is paid-tier (AWS Secrets Manager, AWS Parameter Store, Azure Key Vault, GCP Secret Manager). The estimate degrades cleanly to "rate metrics offline" when Prometheus is unavailable.
- Per-provider creation wizards exist for the top + medium tier (12 stores covering the ~95% real-world install base). Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, custom webhook) are creatable via the YAML editor with provider-templates seeded.
- Downstream `ce-plan` can break this requirements doc into implementation phases without inventing product behavior, scope boundaries, or success criteria.

---

## Scope Boundaries

- We do NOT authenticate to source stores (Vault, AWS, GCP, Azure) on the user's behalf — only the ESO controller does. The integration consumes ESO's status; it never holds source-store credentials.
- We do NOT install, upgrade, or manage the ESO controller itself. Out of scope for this feature; would be a separate "operator lifecycle management" initiative.
- We do NOT provide a cross-cluster aggregated "fleet view" of ESO state. The existing X-Cluster-ID routing applies (the integration works correctly per cluster), but a "all clusters at once" dashboard is a separate feature.
- We do NOT ship dedicated wizards for the niche provider tail (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, custom webhook). YAML editor only.
- We do NOT integrate with source-system billing APIs for live cost data. Cost-tier estimates use static rate cards refreshed periodically.
- We do NOT proxy or re-render source-system audit logs. Linkout only.
- We do NOT include a `PushSecret` write/edit wizard in v1 — read-only observability is sufficient. PushSecret is uncommon in production and the wizard work doesn't earn its keep until usage signals demand.

---

## Key Decisions

- **Permissive-read multi-tenancy (cert-manager parity).** The simplest contract; mirrors Phase 11A. Operators with a stricter tenant story can layer Kubernetes RBAC; we don't need a second redaction model.
- **Persistent sync-history in PostgreSQL.** ESO's CRD status carries only the last-attempt outcome. A persisted timeline is the only way to surface flapping patterns and key-level diffs across syncs without re-fetching the source. Follows the existing Phase 8B daily compliance-snapshot precedent and the audit log's UID-stable-history precedent — no new persistence pattern.
- **Linkout, not embed, for source-system context.** Authenticating to Vault / AWS / GCP / Azure on the user's behalf would replicate ESO's job badly. Deep-linking to the source UI gives operators full audit power with zero new auth surface.
- **Top + medium tier wizards (12 providers).** Covers the ~95% real-world install base without exploding into per-provider work for niche stores. Niche providers ship via YAML editor with templates.
- **Failure + recovery alerts with per-resource overrides (Phase 13 parity).** Recovery alerts confirm fixes worked without polling. Per-resource annotations let operators mute noise on chatty integrations or tighten thresholds on critical secrets. Single canonical resolution chain matches Phase 13's pattern.
- **Topology-style chain visualization, not a separate UI primitive.** Reuses Phase 7B's topology builder + renderer; new edge types extend the existing enum. No parallel graph-rendering library introduced.
- **No PushSecret wizard in v1.** PushSecret reverses ESO's flow (k8s Secret → external store). Uncommon enough to defer; observability is sufficient.

---

## Dependencies / Assumptions

- ESO is installed as a CRD-providing controller in the target cluster; its `external-secrets.io/v1` CRDs are the source of truth (v1 went GA in ESO v0.14, mid-2025). `v1beta1` remains served for compatibility but is no longer the storage version; older clusters running only v1alpha1 are out of scope (could be added later).
- The Notification Center (roadmap item #1, shipped) accepts new event source `external_secrets` and rule-routing for the new event kinds.
- The audit logger (existing) accepts the bulk-refresh action and result-set shape.
- The PostgreSQL migration framework (`golang-migrate`, existing) accepts the new `eso_sync_history` table without conflict.
- The Phase 7B topology builder accepts new `EdgeType` constants without breaking existing consumers (Phase D mesh overlay set the precedent).
- The frontend Monaco editor is available for the wizard preview step (existing).
- The frontend toast / dialog / wizard-stepper components exist and are theme-token-only (existing).
- Helm chart can grow read-only ClusterRole entries for ESO CRDs without disrupting the existing wildcard or per-feature grants (Phase 12 set the precedent).

---

## Outstanding Questions

### Resolve Before Planning

- [Affects R12, R13][Product/UX] Path-discovery contract for the wizard type-ahead step. The 12-provider wizard surface needs a single product contract — does the wizard offer type-ahead only when the provider supports listing (Vault `LIST`, AWS `ListSecrets`, GCP `projects.secrets.list`, etc.), or always offer it and degrade silently? Recommended pre-decision: type-ahead opt-in per provider, free-text fallback whenever a provider can't list, with a small inline "this provider doesn't expose path discovery — enter the path manually" hint. Resolve before planning so wizard work doesn't fork halfway through.

### Deferred to Planning

- [Affects R6, R7][Technical] Storage shape for the persisted sync-history table — schema columns, partitioning, retention enforcement (TTL job vs cron). Audit-log retention pattern is the closest precedent. R31 sets the column outline; planning resolves indexes, partitioning, and retention enforcement.
- [Affects R9, R10][Technical] How exactly the chain graph composes with the existing Phase 7B topology — single shared graph builder with a new `?include=eso-chain` overlay, or a parallel builder under `internal/externalsecrets/topology.go`. Both have precedent (Phase D mesh overlay vs separate builder).
- [Affects R26][Needs research] Reasonable rate-card data shape and refresh cadence for the cost-tier estimate. AWS / GCP / Azure publish JSON pricing APIs; embedding a snapshot vs. fetching at runtime is a maintenance trade-off.
- [Affects R20][Technical] Per-provider coverage of `status.syncedResourceVersion`. R20's `Unknown` state absorbs the gap; planning enumerates which providers populate it so the dashboard's drift tile can show "tracked / not-tracked-by-provider" honestly.
- [Affects R22][Technical] Bulk refresh implementation: whether to fan out the `force-sync` annotation patch directly or use ESO's `Refresh()` SDK if/when available. Patch is provider-agnostic.

### FYI / Planning-stage details

- Frontend route + SubNav layout (`/security/external-secrets/{index,external-secrets,stores,cluster-stores,push-secrets,chain}`), empty states for each list view, wizard interaction-state matrix, and the notification-routing table for the new event kinds — design-lens flagged. Planning owns the artifacts; brainstorm doesn't need to lock them.
- Multi-cluster (X-Cluster-ID) routing applies automatically via the existing ClusterRouter pattern; no per-cluster work surfaces in this requirements doc.
- The "permissive-read" decision (KD-1) inherits the same caveat as Phase 11A: if a tenant operator can list a `ClusterSecretStore`, they can read its `spec.provider.*` config (which references an auth Secret but never the credentials). Stricter tenancy is layered via Kubernetes RBAC.
