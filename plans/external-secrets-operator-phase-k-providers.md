---
title: "External Secrets Operator — Phase K: remaining provider creation paths"
type: feat
status: active
date: 2026-05-06
parent: plans/external-secrets-operator-integration.md
---

# External Secrets Operator — Phase K: remaining provider creation paths

## Overview

Phase 14 (Phases A–J) shipped 8 of the 12 originally-scoped per-provider SecretStore wizards. The remaining 4 wizard candidates (Akeyless, Bitwarden Secrets Manager, CyberArk Conjur, Infisical) currently render in the SecretStore wizard's provider picker as `coming soon` and are **non-clickable** — no creation path exists. Additionally, the niche-provider YAML template registry that Unit 20 of the parent plan was supposed to deliver (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, generic webhook) was never actually created — `frontend/lib/eso-yaml-templates.ts` and the `stores/new-from-template.tsx` route do not exist in the repo despite Unit 20 being ticked on the plan checklist.

R13 of the parent plan required: "all 12 providers have at least one creation path; not 12 wizards specifically." That contract is currently broken for 4 providers, and Unit 20's niche-template surface owes 7 more.

Phase K closes both gaps with a single template-driven path. No new wizards. No new backend validators. The 4 culled-from-Unit-19 providers + the 7 niche providers all land as YAML templates served from a single registry, accessible via a new `Create from template` route that's reachable from the SecretStore wizard's provider picker (in place of the current dead-end `coming soon` state).

## Scope

**In scope:**
- 11 YAML templates (4 culled wizard candidates + 7 niche providers) in a single TS registry.
- New route: `routes/external-secrets/stores/new-from-template.tsx` — Monaco editor pre-filled with the selected template, server-side apply.
- Picker rewire: clicking a `coming soon` row in `SecretStoreProviderPickerStep.tsx` navigates to the template route with `?template=<provider>` instead of being a dead button.
- Plan + docs flip: re-mark parent plan's Unit 20 as accurate; CLAUDE.md Phase 14 entry gains the "11 additional providers via YAML templates" line.

**Out of scope:**
- Promoting any of the 4 culled candidates to a full wizard (the L7.2 culling pass already concluded their wizard added little value over a YAML template).
- Path discovery for any of the 11 providers (matches the parent plan's v1 scope: Kubernetes-provider-only path discovery).
- Backend validators for the 11 providers (they ride the existing YAML-apply path: server-side validation by ESO + the cluster API, no k8sCenter-side schema check).
- Cost-tier rate-card entries for any of these 11 providers (cost tier is opt-in per provider; out of scope for template-only path).

## Requirements traceability

| ID | Requirement | Source | Phase K unit |
|---|---|---|---|
| R13 | All 12 providers have at least one creation path | parent plan, line 28 | K1, K2 |
| R14 | Niche-provider YAML templates available from the wizard surface | parent plan, line 29 | K1 |

No new requirements introduced; Phase K finishes parent plan's R13 + R14.

## Phase ordering

| Phase | Goal | Units | Depends on |
|---|---|---|---|
| K | Template registry + picker rewire | K1, K2, K3 | parent plan Phase H complete (Unit 18 picker, Unit 19 wizard scaffold) |

---

## Phase K — Remaining provider creation paths

### Unit K1: YAML template registry + Create-from-template route

**Goal:** Create a typed TS registry mapping each of the 11 unshipped provider keys to a curated, copy-paste-runnable SecretStore YAML template. Add a route that renders the selected template in Monaco with the existing server-side `POST /yaml/apply` pipeline.

**Requirements:** R13 (partial — 4 culled candidates), R14 (full — niche providers).

**Dependencies:** Parent plan Unit 18 (provider picker exists), Unit 19 (wizard scaffold exists), Unit 4 (YAML apply route under authenticated `ar.Group`).

**Files:**
- Create: `frontend/lib/eso-yaml-templates.ts` — typed `Record<SecretStoreProvider, ESOTemplate>` where `ESOTemplate = { yaml: string; docsURL: string; notes: string }`. Entries for: `akeyless`, `bitwardensecretsmanager`, `conjur`, `infisical`, `pulumi`, `passbolt`, `keeper`, `onboardbase`, `oraclevault`, `alibaba`, `webhook`.
- Modify: `frontend/lib/eso-types.ts` — extend `SecretStoreProvider` union with the 7 niche-provider keys (`pulumi`, `passbolt`, `keeper`, `onboardbase`, `oraclevault`, `alibaba`, `webhook`). Add a parallel `TEMPLATE_ONLY_PROVIDERS: Set<SecretStoreProvider>` exported alongside `READY_SECRET_STORE_PROVIDERS` so the picker can distinguish "wizard ready" from "template only" from "not supported."
- Create: `frontend/routes/external-secrets/stores/new-from-template.tsx` — query-param-driven route (`?template=<provider>`); SSR'd shell + island for the Monaco editor pre-filled with `templates[provider].yaml`, plus a header showing the provider name, the docs URL, and the notes string. Cancel + Apply buttons; Apply hits the existing `/api/v1/yaml/apply` endpoint and on success navigates to the resulting Store's detail page.
- Create: `frontend/islands/SecretStoreFromTemplateEditor.tsx` — Monaco editor island. Reuses the existing YAML editor primitives (`frontend/islands/YamlEditor.tsx` if it exists, otherwise the Monaco wrapper used by `IssuerWizard` per parent plan's Phase 11B reference).
- Modify: `frontend/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx` — add the 7 niche-provider entries to the `PROVIDERS` array. Render rule: `READY` → wizard (current behavior); `TEMPLATE_ONLY` → renders as clickable but navigates to `/external-secrets/stores/new-from-template?template=<id>` instead of triggering `onSelect`; otherwise (no path at all) → disabled `coming soon` (this state should no longer apply after Phase K but we keep the branch defensively).

**Approach:**
- Each template is a minimal, **valid** SecretStore YAML with placeholder values clearly marked (e.g., `# REPLACE: Akeyless access ID`). Templates target the provider versions documented in ESO `v1` API as of 2026-05-06; the template's `# Source:` comment links to the upstream ESO docs page so operators can verify the schema.
- No Go SDK pull-ins for any of the 11 providers (parent plan L7.1 — `map[string]any` for `ProviderSpec` everywhere, validators hand-rolled). Phase K reuses the apply path; ESO + cluster API are the schema authority.
- `TEMPLATE_ONLY_PROVIDERS` is the single edit point for re-classifying a provider in the future (e.g., promoting a template to a wizard once smoke-tested).
- The picker's `coming soon` branch is preserved for forward-compat (a provider could conceivably be in neither set during a future split-PR), but every entry currently in `PROVIDERS` will land in either `READY_SECRET_STORE_PROVIDERS` or `TEMPLATE_ONLY_PROVIDERS` after K1 lands.

**Test scenarios:**
- *Template registry shape*: every key in `TEMPLATE_ONLY_PROVIDERS` has a matching entry in `eso-yaml-templates.ts`; mismatch fails a unit test.
- *Template YAML parses*: each template parses as YAML (trivial deno-test using `parse` from `@std/yaml`).
- *Template kind/apiVersion correct*: each template's parsed root has `kind: SecretStore` (or `kind: ClusterSecretStore` for any cluster-scoped templates we choose to ship; v1 ships namespaced only) and `apiVersion: external-secrets.io/v1`.
- *Picker click → template route*: click on a `TEMPLATE_ONLY` row navigates to `new-from-template?template=<id>`; verified via Playwright if E2E coverage exists for the SecretStore wizard, otherwise manual smoke.
- *Apply flow*: server accepts the rendered YAML through `POST /yaml/apply` (post-edit by the operator filling in real values); 422 on schema-invalid edits surfaces the existing YAML-error pattern.

**Verification:**
- `cd frontend && deno task check`.
- `cd backend && go vet ./... && go test ./...` (unchanged backend; this is a regression check).
- Manual smoke: open the SecretStore wizard, select `Akeyless`, verify navigation to the template page; same for `Pulumi ESC`, `Custom webhook`, and one other. Apply a template after filling in placeholders against a homelab provider where feasible.

---

### Unit K2: Picker copy + nav-rail entry update

**Goal:** Refresh the picker step's intro paragraph to match the new tri-state (`wizard` / `template` / unsupported), drop the `coming soon` language for the 4 culled candidates, and ensure the nav-rail's ESO Stores entry surfaces the template route as a sibling of the wizard route (so operators can land directly on the template page from the sidebar).

**Requirements:** R13.

**Dependencies:** Unit K1.

**Files:**
- Modify: `frontend/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx` — update the intro `<p>` paragraph: replace the "coming soon" sentence with "Providers without a guided form open a pre-filled YAML template instead — fill the placeholders and apply."
- Modify: `frontend/lib/constants.ts` (or wherever the ESO nav-rail group is defined — grep `external-secrets/stores`) — add a `Create from template` sub-link under the existing Stores section that lands on `/external-secrets/stores/new-from-template` with no query param (the page renders a provider-picker grid when no `?template=` is present).
- Modify: `frontend/routes/external-secrets/stores/new-from-template.tsx` — when no `?template=` is in the URL, render a provider-grid identical in shape to `SecretStoreProviderPickerStep` but filtered to `TEMPLATE_ONLY_PROVIDERS` (no wizard providers in this view; operators wanting a wizard go via the existing `/stores/new` path).

**Approach:**
- Keep the nav-rail entry behind the existing ESO domain RBAC gate.
- Empty-state for the no-`?template=` view: list all 11 templates with their notes string as the description, click navigates with `?template=<id>`.

**Test scenarios:**
- *Intro paragraph*: snapshot test of the picker's intro text or a `deno test` regex assertion that `coming soon` no longer appears in the picker source.
- *Nav-rail entry*: link renders for users with ESO RBAC; hidden for users without.
- *No-template view*: visiting `/external-secrets/stores/new-from-template` (no query param) renders the 11-provider grid; clicking any entry navigates with the correct `?template=` query.

**Verification:**
- `cd frontend && deno task check`.
- Manual smoke: open the nav-rail link, verify the grid renders all 11; click `Pulumi ESC`, verify navigation + Monaco preload.

---

### Unit K3: Parent plan reconciliation + CLAUDE.md update

**Goal:** Reflect Phase K's completion in CLAUDE.md's Phase 14 entry; correct the parent plan's Unit 20 retroactive marker (it was ticked but never shipped — Phase K K1 delivers what Unit 20 originally specified); flip Phase K plan status to `complete` once K1 + K2 ship.

**Requirements:** None — pure docs reconciliation.

**Dependencies:** Units K1 and K2.

**Files:**
- Modify: `CLAUDE.md` — Phase 14 entry: extend the wizard sentence to read "8-provider wizards plus 11-provider YAML template registry covering remaining ESO v1 providers (Akeyless, Bitwarden Secrets Manager, CyberArk Conjur, Infisical, Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, generic webhook)."
- Modify: `plans/external-secrets-operator-integration.md` — append a `## Phase K — Remaining provider creation paths (post-completion follow-up)` section that points to this plan file. Replace the (incorrect) `[x] Unit 20 — Niche-provider YAML templates` line with `[x] Unit 20 — superseded by Phase K Unit K1 (see plans/external-secrets-operator-phase-k-providers.md)` so the historical record is honest about what shipped when.
- Modify: `plans/external-secrets-operator-phase-k-providers.md` (this file) — `status: active` → `status: complete`.
- Modify: `README.md` — Security & Governance ESO bullet: append "+ YAML templates for 11 additional providers" to the wizard list.

**Verification:**
- Visual diff inspection of CLAUDE.md, README.md, and the parent plan markdown.
- No code changes; no test run required.

---

## Files modified across Phase K (summary)

- `frontend/lib/eso-types.ts` — `SecretStoreProvider` union extended; `TEMPLATE_ONLY_PROVIDERS` exported.
- `frontend/lib/eso-yaml-templates.ts` — new file; 11 template entries.
- `frontend/components/wizard/secretstore/SecretStoreProviderPickerStep.tsx` — picker tri-state behavior.
- `frontend/routes/external-secrets/stores/new-from-template.tsx` — new route.
- `frontend/islands/SecretStoreFromTemplateEditor.tsx` — new island.
- `frontend/lib/constants.ts` — nav-rail entry.
- `CLAUDE.md`, `README.md`, `plans/external-secrets-operator-integration.md`, this file — docs reconciliation.

Total: ~5 new files, ~5 modified files. All frontend; no backend code changes.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Templates drift from upstream ESO schema (provider docs change between releases) | Each template carries a `# Source: <docs-URL>` header; quarterly refresh cadence noted in Phase K K3's CLAUDE.md update mirrors the existing cost-tier rate-card freshness convention. |
| Operators apply a template without filling placeholders, hit a runtime ESO error | Templates use `# REPLACE: <description>` markers on every required field; the route's intro panel calls out "Fill all `# REPLACE:` markers before applying." Existing `/yaml/apply` 422 path handles validation failures. |
| Re-classifying a provider later (template → wizard) requires touching both `READY_SECRET_STORE_PROVIDERS` and `TEMPLATE_ONLY_PROVIDERS` — easy to forget one | Add a `deno test` invariant that no provider key appears in both sets and every key is in exactly one set or the picker's "no path at all" state (the test fails noisily if Phase K's bookkeeping drifts). |
| Picker grid grows to 19 entries (8 wizard + 11 template), gets visually unwieldy | Acceptable for v1; if smoke tests show the picker becomes cumbersome, follow-up can split the picker into "Guided" vs "From template" tabs (out of scope for Phase K). |

## Checklist

### Phase K — Remaining provider creation paths
- [ ] Unit K1 — Template registry + Create-from-template route + picker tri-state
- [ ] Unit K2 — Picker copy + nav-rail entry + no-template grid view
- [ ] Unit K3 — Parent plan reconciliation + CLAUDE.md / README update + plan status → complete
