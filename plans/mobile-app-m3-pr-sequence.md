---
title: "Mobile App M3 — wizards (PR sequence)"
type: feat
status: active
date: 2026-05-07
origin: plans/mobile-app.md
---

# Mobile App M3 — wizards (PR sequence)

## Summary

Land M3 of `plans/mobile-app.md` as five reviewable PRs (PR-3a → PR-3e) that bring all 28 routable web wizards to the Flutter app, grouped into ~18 logical wizard units when scope variants are folded in (Issuer/ClusterIssuer, SecretStore/ClusterSecretStore, scoped Policy variants). PR-3a stands up the shared `WizardStepperMobile` shell, per-wizard Riverpod form-state pattern, preview/apply pipeline, and three proof-of-concept simple wizards (ConfigMap, Secret, Service). PR-3b ports the workloads cluster (Deployment / Job / CronJob / DaemonSet / StatefulSet). PR-3c covers networking, scaling, RBAC, storage class, and the multi-resource NamespaceLimits wizard. PR-3d ports the storage/backup family (PVC, Snapshot, ScheduledSnapshot, RestoreSnapshot picker, VeleroBackup, VeleroRestore, VeleroSchedule). PR-3e absorbs the CRD-backed wizards with the highest UX risk (Certificate, Issuer/ClusterIssuer, ExternalSecret, SecretStore/ClusterSecretStore, Policy with engine auto-detect). Every wizard hits the existing `POST /v1/wizards/:type/preview` for YAML preview and the existing `POST /v1/yaml/apply` for SSA apply — no new backend.

---

## Problem Frame

M2 closed the read/write asymmetry for verb actions and ConfigMap/Secret YAML edits. The remaining mobile gap is **resource creation** — every flow that produces a *new* resource still hands the operator off to a laptop. An oncall who wants to spin a one-shot Job to drain a queue, scale up a DaemonSet by adding it, drop a NetworkPolicy that quarantines a misbehaving namespace, or roll a Certificate renewal still has to pull out a desktop.

M3 closes that gap by porting every web wizard the operator needs. The web wizards are mature and well-trodden (some shipped in Phase 4, Phase 8, Phase 11B, Phase 14), and the backend `/v1/wizards/:type/preview` registry is stable across all 28 types. M3 is mostly faithful Dart ports of well-trodden TypeScript form components, with the mobile-specific work concentrated in three places: a `WizardStepperMobile` shell that mirrors `frontend/components/wizard/WizardStepper.tsx`, a per-wizard Riverpod state-machine that owns form fields plus preview/apply transitions, and a small library of mobile-friendly form widgets (key-value table, kind picker, namespace picker, repeating row group) that the per-wizard forms compose.

---

## Requirements

- R1. **All 28 web wizards reachable on mobile.** Every routable wizard component under `frontend/islands/*Wizard.tsx` (excluding `SetupWizard` and `UserWizard`) has a mobile counterpart producing the same kind(s) of resource. Scope variants (Issuer/ClusterIssuer, SecretStore/ClusterSecretStore) reuse a single mobile form with a `scope: WizardScope` discriminator, mirroring the web pattern in `frontend/islands/IssuerWizard.tsx` and `frontend/islands/SecretStoreWizard.tsx`.
- R2. **No new backend endpoints.** Mobile calls `POST /v1/wizards/:type/preview` (JSON request, JSON `{data: {yaml}}` response) and `POST /v1/yaml/apply` (YAML request, JSON apply-result response). Both endpoints already serve the web frontend. If a wizard appears to need a new endpoint, that's a scope error — surface it and split a separate backend PR.
- R3. **Every wizard is RBAC-gated client-side.** The wizard route shows up in nav only when the operator's `RBACSummary` (loaded by PR-1b's `/v1/auth/me`) carries `create` for the produced kind in at least the active namespace. Reuses `mobile/lib/auth/permissions.dart:canPerform` from M2 with the `create` verb. Backend remains the final authority via 403.
- R4. **YAML preview is the last step before apply.** Every wizard ends with a `Review` step that triggers `/v1/wizards/:type/preview`, renders the returned YAML in a read-only `code_text_field` (the same widget M2's editor uses), and exposes an Apply button that posts the preview YAML to `/v1/yaml/apply`. Operators can swipe back to earlier steps to fix fields and re-preview — round-tripping through the full validate-on-server cycle, exactly like the web.
- R5. **Field-level validation errors surface inline.** When `/v1/wizards/:type/preview` returns 422 with `error.detail` carrying `[{field, message}, …]`, the wizard navigates back to the step that owns the offending field and renders the message under the relevant input. Other steps stay untouched. The mapping from server `field` paths (e.g., `spec.template.spec.containers[0].image`) to mobile form fields lives per-wizard in a small `errorRouter` function.
- R6. **Wizard form state is per-wizard, ephemeral, and survives orientation changes.** A `Riverpod AutoDisposeFamilyNotifier` keyed on `(wizardType, draftId)` owns form state. The notifier auto-disposes when the wizard route pops. No persistence across app restarts — incomplete drafts are not recovered. (A future "saved drafts" feature is M5+ polish.)
- R7. **Active cluster pinning at every wizard step.** The cluster ID captured at wizard-open time is pinned for the lifetime of the wizard. If the operator switches clusters mid-wizard via the cluster pill, the wizard surfaces a clear "Cluster changed — discard or stay" sheet (mirrors M2's pinning discipline from `mobile/lib/api/resource_actions.dart` and the YAML controller). On apply, the controller re-checks the active cluster matches the pinned cluster and aborts on mismatch.
- R8. **Each PR ships demonstrable surface.** PR-3a end: an operator creates a ConfigMap from a phone. PR-3b end: an operator creates a Deployment with probes and resources. PR-3c end: an operator drops a NetworkPolicy quarantining a namespace. PR-3d end: an operator schedules a Velero backup of a single namespace. PR-3e end: an operator creates an ExternalSecret pulling from a Vault store and a Certificate signed by an existing ClusterIssuer.
- R9. **Theme parity holds.** Every wizard step renders correctly under each of the 7 themes from `mobile/lib/theme/themes.g.dart`. Golden tests cover at least Nexus (light) and Dracula (dark) for the stepper shell + one form step per PR.
- R10. **Web/Dart wizard list isomorphism.** A `mobile/lib/wizards/wizard_registry.dart` catalogue lists every supported wizard type, mirroring the implicit catalogue of `frontend/islands/*Wizard.tsx`. PR description for any future wizard addition references both files; CI doesn't enforce isomorphism (deferred unless drift actually happens).

---

## Scope Boundaries

- **Out of scope:** M4 (advanced observability — `fl_chart`, LogQL editor, topology, diagnostics), M5 (polish + public store launch). Each gets its own plan.
- **Out of scope:** Wizard kinds the web doesn't have. Mobile is a parity port; M3 doesn't invent new resource creation flows. If operators ask for a new wizard during M3 hands-on, route it to a *web-first* feature so web stays the source of truth.
- **Out of scope:** Saved drafts / wizard resume. R6 deliberately keeps state ephemeral. Persisting drafts across app restarts is M5+ polish if operators report pain.
- **Out of scope:** Bulk apply (one wizard producing N similar resources, e.g., "deploy this Deployment to 5 namespaces"). Web doesn't have it; introducing it on mobile is a scope inversion.
- **Out of scope:** Custom YAML edit before apply. The Review step renders preview YAML read-only; if the operator wants to tweak, they go back and edit form fields. Inline YAML hand-editing during a wizard breaks the form/server-validation contract and is M5+ polish at most.
- **Out of scope:** Provider credential validators for SecretStore. Web's Phase H deferred provider-specific credential validation server-side; mobile inherits the deferral. Wizard accepts whatever the operator types and surfaces backend rejection on apply.
- **Out of scope:** SetupWizard (`/setup` onboarding) and UserWizard (`/settings/users/new` admin). Neither is a resource-creation wizard. Setup is deliberately desktop-only; user management lands in mobile only when M5+ adds an admin surface.

### Deferred to Follow-Up Work

- **Wizard from a notification deep-link.** PR-1g's deep-link router handles `k8scenter://resources/...` and `/notifications/...`. Adding `k8scenter://wizards/<type>/new?prefill=<...>` to land directly in a pre-filled wizard is genuinely useful (e.g., "scale-up via wizard from an HPA alert") but each wizard's prefill schema is its own design. Defer until at least one operator workflow demands it.
- **Step-level analytics.** No telemetry on wizard step completion / drop-off. M5 polish if the team decides product analytics matter.
- **Inline schema-validation hints.** Web doesn't show "must match DNS-1123" hints proactively either — both rely on server validation. M5+ if operators say the round-trip is too slow.
- **Wizard golden-testing matrix.** Per-PR goldens cover the stepper shell + one representative form step. Comprehensive per-wizard goldens for every step under every theme is M5 polish.

---

## Context & Research

### Relevant code and patterns (in-repo)

- `frontend/components/wizard/WizardStepper.tsx` — **the** canonical step-progress shell. Stateless: takes `steps[]`, `currentStep`, optional `onStepClick`. Future steps disabled, completed steps clickable, current step highlighted. Mobile's `WizardStepperMobile` mirrors this shape verbatim — no validation, no form state, just step rendering and back-nav.
- `frontend/islands/DeploymentWizard.tsx` — most complex 4-step wizard (Basics, Networking, Resources, Review). Source-of-truth pattern for: useSignal-based form state, validateStep gate before advance, preview-on-Review-entry, apply-on-button-tap, dirty guard. Mobile's `DeploymentWizardScreen` ports the state-shape into Riverpod (`deploymentWizardProvider`).
- `frontend/islands/ConfigMapWizard.tsx`, `frontend/islands/SecretWizard.tsx` — minimal 2-step wizards with key-value form bodies. PR-3a's proof points; smallest possible end-to-end demonstration of the wizard pipeline.
- `frontend/islands/PolicyWizard.tsx` — engine auto-detect via `GET /v1/policies/status`; template registry with per-template param schemas. PR-3e's most layered wizard. Mobile mirrors the engine-detect call and the template-picker shape.
- `frontend/islands/SecretStoreWizard.tsx` — provider picker over 10+ providers with provider-specific auth schemas. PR-3e port reuses the same provider list (`vault`, `aws`, `azurekv`, `gcpsm`, `kubernetes`, `doppler`, `onepassword`, `bitwarden`, `akeyless`, `conjur`, `infisical`) and a dynamic key-value table for untyped credential fields.
- `frontend/islands/IssuerWizard.tsx` — same component reused for namespaced and cluster-scoped issuers via a `scope` prop. ACME solver config is a nested form. Mobile's `IssuerWizardScreen` takes the same `scope` prop and routes to `:type=issuer` or `:type=cluster-issuer`.
- `frontend/islands/CertificateWizard.tsx` — depends on a runtime fetch of available Issuers (`GET /v1/certmanager/issuers`). Mobile pre-fetches via a `FutureProvider` keyed on the active cluster.
- `frontend/islands/NamespaceLimitsWizard.tsx` — produces ResourceQuota + LimitRange in a single multi-doc YAML response. Apply result panel renders **both** rows; mobile result widget already handles arrays from M2's YAML editor.
- `frontend/islands/RestoreSnapshotWizard.tsx` — picker UX (select an existing snapshot, then confirm restore). Mobile reuses the `RollbackPicker` widget pattern from M2 PR-2b for the listing/selection step.
- `backend/internal/wizard/handler.go:HandlePreview` — generic preview handler. Decodes JSON into a `WizardInput`, calls `Validate()` then `ToYAML()`. Field errors return 422 with detail array. Mobile's preview client maps that array into per-step inline messages.
- `backend/internal/server/routes.go` — wizard route group registers all 28 types. Endpoint pattern is stable; no new wiring required for M3.
- `mobile/lib/api/yaml_apply_controller.dart` (PR-2b) — already implements the `validate → apply → invalidate` state machine and parses the `{results, summary}` response. M3's wizard apply step calls into this same controller (or shares its response-parsing helpers) instead of re-implementing apply-result rendering.
- `mobile/lib/widgets/confirm_sheet.dart` (PR-2a) — reused for wizard "Cluster changed — discard or stay" sheet (R7) and apply-confirmation when the wizard produces destructive-adjacent resources (e.g., NetworkPolicy that could lock out a namespace).
- `mobile/lib/widgets/cluster_picker_sheet.dart` (PR-1c) — the cluster pill / picker sheet that M2 already pins. Wizard cluster pinning hooks into the same `activeClusterProvider` invalidation contract.
- `mobile/lib/auth/permissions.dart` (PR-2a) — `canPerform(rbac, verb, kind, namespace)` predicate, called with `verb: 'create'` to drive R3.
- `mobile/lib/api/dio_client.dart` (PR-1b) — Dio instance with the `Cluster`/`CSRF`/`Auth`/`ErrorMapping` interceptor stack. Wizard preview/apply piggyback on the same stack — no new HTTP plumbing.
- `mobile/lib/widgets/empty_states.dart` (PR-1a) — `LoadingState`, `EmptyState`, `ErrorState`. Wizard preview spinner uses `LoadingState`; preview-failure banner uses `ErrorState`.
- `mobile/lib/routing/app_router.dart` (PR-1c) — go_router config; M3 adds the `/clusters/:clusterId/wizards/:type/new` route group.

### Institutional learnings

- **CLAUDE.md Rule 2 (PHASED EXECUTION, ≤5 files per phase):** PR-3a and PR-3e touch many files. Per-PR commit sequence respects the rule — first commit is shared infrastructure (`wizard_stepper.dart`, `wizard_form_state.dart`, `wizard_preview_client.dart`, ≤5 files), then per-wizard commits split each wizard into its own ≤5-file batch.
- **CLAUDE.md Rule 4 (FORCED VERIFICATION):** every PR runs `cd mobile && flutter analyze && flutter test` and `cd frontend && deno task check` (no web changes expected, but verify the wizard route additions don't break web build) before push. `make check-themes` confirms no theme drift.
- **CLAUDE.md Rule 5 (sub-agent swarming):** PR-3b through PR-3e port multiple wizards per PR. Each PR uses parallel Sonnet sub-agents (one per 1–2 wizards) for the form-port work after PR-3a's infrastructure is in place. PR-3a stays single-context to land the shared shell coherently.
- **Auto-memory feedback (workflow):** run `/ce:review` BEFORE pushing each branch (not at PR-creation time). All five PRs honor this.
- **M2 isomorphism discipline carries forward.** `mobile/lib/api/resource_actions.dart` mirrors `frontend/lib/action-handlers.ts` 1:1; M3 establishes the same discipline for wizards via `mobile/lib/wizards/wizard_registry.dart`. New wizards added in either codebase must update both, ideally in the same PR.
- **M2's pinning lesson (per CLAUDE.md "Mobile invariants"):** all writes pin the active cluster; mismatch aborts. M3 inherits this — every wizard captures the cluster ID at open and aborts apply on mismatch.

### External references

- Flutter `code_text_field` 0.7.x — already pinned in M2 for the YAML editor; M3 reuses for read-only YAML preview rendering on the Review step.
- `flutter_riverpod` 2.x `AutoDisposeFamilyNotifier` — same pattern M2 used for the YAML apply controller; M3 uses one notifier per wizard type, family-keyed on `(wizardType, draftId)`.
- `go_router` typed routes via `go_router_builder` — already in pubspec from PR-1c. M3 adds typed routes for each wizard type so that intra-app deep-links to wizards (R-deferred follow-up) won't require a router refactor later.
- Kubernetes Server-Side Apply — wizard apply uses the same SSA path as M2's YAML editor; conflict semantics are identical.

---

## Key Technical Decisions

- **Five PRs grouped by domain, not by complexity tier.** PR-3a (infrastructure + 3 simple wizards), PR-3b (workloads), PR-3c (networking + scaling + RBAC + storage class + namespace limits), PR-3d (storage/backup family), PR-3e (CRD wizards). Domain grouping makes each PR's `/ce:review` pass coherent (one reviewer can hold "all the workload wizards" in their head); complexity-tier grouping would scatter related wizards across PRs and force the reviewer to context-switch. The trade-off: PR-3e holds the four most complex wizards together, accepted because they share the "depends on a runtime CRD list" pattern (issuer list, secret store list, policy template list).
- **One Riverpod notifier per wizard type, family-keyed on `(wizardType, draftId)`, holding the entire form state plus the preview/apply state machine.** Each wizard's notifier extends a shared `WizardController<TForm>` base (with `validate()`, `preview()`, `apply()`, transitions) and supplies its own typed form record. Family-keying by `draftId` means a future "two wizards open simultaneously on tablet" scenario gets isolated state for free.
- **`WizardStepperMobile` is stateless and pure — same contract as the web component.** Inputs: `steps: List<WizardStep>`, `currentStep: int`, optional `onStepClick: void Function(int)?`. Renders horizontal step indicators on tablet, vertical on phone (single 768px breakpoint, same as M1). No validation, no form coupling, no preview hooks — those live in the per-wizard screen.
- **Form-state for each step is held in the wizard's notifier, not in the step widget.** Step widgets are pure — they take a `WizardController` ref and an `update` callback. This mirrors the web's pattern of all signals living in the wizard island and step components being stateless. The benefit: the operator can navigate back without losing state, and the controller can validate cross-step constraints on advance.
- **Mobile-friendly form widgets ship in PR-3a and are composed by every per-wizard form afterwards.** `KeyValueTable` (ConfigMap, Secret, untyped provider creds), `KindPicker` (target kind for HPA / RoleBinding subjects), `NamespacePicker` (already exists from M2's resource list — refactored into a reusable widget if it isn't already), `RepeatingRowGroup` (Ingress rules, NetworkPolicy peers, container env vars, secret data items). Each is stateless and parameterized — no per-wizard fork.
- **Preview client centralizes the `/v1/wizards/:type/preview` POST and field-error parsing.** `WizardPreviewClient.preview(type, body) → Future<PreviewResult>` returns either `PreviewYaml(yaml)` or `PreviewErrors(List<FieldError>)`. Each wizard's notifier consumes this same client and supplies a `errorRouter: Map<String, int>` mapping server field paths to mobile step indices. The router is the only per-wizard piece of error-mapping code; everything else is shared.
- **Apply uses the existing `yaml_apply_controller.dart` from M2 unchanged.** The Review step's Apply button calls into the same `apply(yaml)` helper M2's editor uses. This keeps the wizard apply path byte-identical to direct YAML edit, which means the audit log, RBAC enforcement, and SSA-conflict UX behave identically.
- **Routes are typed via `go_router_builder` and live under `/clusters/:clusterId/wizards/:type/new`.** Each wizard registers its own typed route extension in `mobile/lib/routing/wizard_routes.dart`. This buys two things: compile-time check that every registered wizard type has a route, and an obvious extension point for the deferred deep-link follow-up.
- **Cluster pinning is enforced inside the controller, not the screen.** `WizardController.apply()` re-reads the active cluster ID at apply time, compares to the pinned ID captured at construction, and aborts with a `ClusterMismatchException` on drift — surfaced as the "Cluster changed — discard or stay" `ConfirmSheet`. Same shape as M2's `_resourceBase` pinning discipline.
- **PolicyWizard's engine auto-detect runs once at wizard open, not on every step.** PR-3e's `policyWizardProvider` calls `GET /v1/policies/status` in its constructor and caches the engine list for the lifetime of the wizard. Re-fetching on each step transition is wasteful and racy.
- **CertificateWizard's issuer list is a `FutureProvider.autoDispose` keyed on `(clusterId, namespace)`.** Picker re-runs the fetch on namespace change. Same pattern for RoleBinding's role picker and ExternalSecret's store picker.
- **NamespaceLimits is one wizard producing two resources, not two wizards.** Mirrors web. Apply result panel renders both rows under one summary, identical to how M2's YAML editor handled multi-resource applies.
- **RestoreSnapshot reuses M2's `RollbackPicker` widget structure.** A list of available snapshots, tap to select, confirm sheet to restore. Splitting the reusable picker into a generic `ListPickerScreen<T>` is a refactor that lands as part of PR-3d if it pays for itself; otherwise PR-3d copies the M2 widget shape.
- **Wizard nav lives in the existing left-rail / drawer's "Create" submenu.** PR-3a adds a `Create` entry that expands into a list of wizard types, RBAC-gated by R3. No new top-level nav surface — wizards are an action verb, not a destination.

---

## Open Questions

### Resolved during planning

- **Q: Should mobile re-implement client-side field validation, or rely entirely on server validation?** Resolved: server-only. Web does the same — client-side `validateStep` only checks "required fields present" / "ints parse"; everything semantic is server-side. Mobile mirrors. No client-side k8s schema validation worth porting.
- **Q: Should the YAML preview be editable inline?** Resolved: no. The preview is the contract; if the operator wants to edit, they go back to the form. Inline edit-during-wizard is M5+ polish if operators report pain.
- **Q: How are YAML previews longer than the screen rendered?** Resolved: a scrollable `code_text_field` with `readOnly: true` and YAML grammar. `code_text_field` is already pinned by M2 — same widget, different mode. No truncation, no pagination.
- **Q: Do any wizards need file uploads (e.g., a TLS cert PEM)?** Resolved: none. All 28 wizards take typed JSON inputs. Issuer's ACME solver and ExternalSecret's auth secrets reference *existing* Secrets by name — they don't accept inline cert blobs.
- **Q: Should the wizard "Apply" button confirm before firing for destructive-adjacent kinds (NetworkPolicy that quarantines a namespace, ClusterIssuer that affects all namespaces)?** Resolved: no extra confirmation step in M3. Web doesn't confirm either; the YAML preview *is* the confirmation. Operators can review the diff visually before tapping Apply. M5 polish revisits if a real footgun appears.
- **Q: What's the right granularity for the wizard registry?** Resolved: one entry per `(type, scope)` tuple, where `scope ∈ {namespaced, cluster}` only matters for Issuer/ClusterIssuer and SecretStore/ClusterSecretStore. Registry exposes `enabled(rbac)` predicate to drive nav RBAC-gating cleanly.
- **Q: Should wizards remember the last-used namespace?** Resolved: yes — defaults to the cluster's currently-active namespace at wizard open (read from `activeNamespaceProvider`). Doesn't persist across app restarts. This is the same default web uses (`defaultNamespace` prop on each wizard island).
- **Q: How do error messages from preview map back to specific form fields?** Resolved: a per-wizard `errorRouter: Map<String, int>` mapping server field path to mobile step index. The wizard's notifier navigates back to the appropriate step and surfaces the message under the relevant input via a per-step `Map<String, String>` of field-id → error.

### Deferred to implementation

- **Exact UX for the cluster-changed sheet.** Whether "Stay" silently re-pins to the new cluster (data loss for fields that depend on cluster context like Issuer picker) or aborts with a discard prompt is a judgement call deferred to first-hands-on test in PR-3a. The R7 contract is satisfied either way.
- **Whether the SecretStore provider picker uses an alphabetical list or groups by category.** Web's order is "popular first" (vault, aws, gcp, azure) with the rest alphabetical. PR-3e copies that order; if operators report scrolling pain on phone, M5 polish revisits.
- **Whether NamespaceLimits's two-resource apply renders the result panel as a single card with sub-rows or two stacked cards.** Both shapes work; pick whichever reads better in PR-3c hands-on.
- **Per-provider credential field hints for SecretStore.** Web shows a small placeholder for each field (e.g., `vault-token-secret-name`). Whether PR-3e ports the placeholder map verbatim or reads it from a shared JSON file is deferred — pick the simpler option once the generic provider form is in place.
- **Whether to share `RollbackPicker` and `RestoreSnapshotPicker` via a generic `ListPickerScreen<T>`.** Refactor pays off if a third picker emerges in M4; if not, copy the widget shape and revisit.

---

## Output Structure

```
mobile/lib/
├── wizards/
│   ├── wizard_controller.dart              # generic <TForm> controller base + state machine
│   ├── wizard_preview_client.dart          # POST /v1/wizards/:type/preview wrapper
│   ├── wizard_registry.dart                # catalogue of (type, scope, label, requires-rbac)
│   ├── wizard_step.dart                    # WizardStep model + per-step error map
│   ├── widgets/
│   │   ├── wizard_stepper_mobile.dart      # stateless step indicator
│   │   ├── wizard_screen_scaffold.dart     # shell: stepper + body + back/next/apply bar
│   │   ├── key_value_table.dart            # repeating key-value rows (ConfigMap / Secret / creds)
│   │   ├── kind_picker.dart                # target kind dropdown
│   │   ├── namespace_picker.dart           # namespace dropdown (refactor of existing if any)
│   │   ├── repeating_row_group.dart        # generic repeating sub-form (rules, peers, env)
│   │   └── yaml_preview_panel.dart         # read-only code_text_field for Review step
│   └── types/
│       ├── configmap/
│       │   ├── configmap_wizard_screen.dart
│       │   ├── configmap_wizard_controller.dart
│       │   └── steps/
│       │       ├── configmap_configure_step.dart
│       │       └── configmap_review_step.dart
│       ├── secret/                         # parallel to configmap/
│       ├── service/                        # parallel to configmap/
│       ├── deployment/                     # 4 steps
│       │   ├── deployment_wizard_screen.dart
│       │   ├── deployment_wizard_controller.dart
│       │   └── steps/
│       │       ├── deployment_basics_step.dart
│       │       ├── deployment_networking_step.dart
│       │       ├── deployment_resources_step.dart
│       │       └── deployment_review_step.dart
│       ├── job/  cronjob/  daemonset/  statefulset/
│       ├── ingress/  networkpolicy/  hpa/  pdb/  rolebinding/  storageclass/  namespace_limits/
│       ├── pvc/  snapshot/  scheduled_snapshot/  restore_snapshot/
│       │   velero_backup/  velero_restore/  velero_schedule/
│       ├── certificate/  issuer/           # issuer/ shared between scope=namespaced and scope=cluster
│       ├── external_secret/  secret_store/ # secret_store/ shared between scope=namespaced and scope=cluster
│       └── policy/                         # engine auto-detect + template picker
└── routing/
    └── wizard_routes.dart                  # typed go_router extensions per wizard type
mobile/test/
└── wizards/
    ├── wizard_controller_test.dart
    ├── wizard_preview_client_test.dart
    ├── widgets/
    │   ├── wizard_stepper_mobile_test.dart
    │   ├── key_value_table_test.dart
    │   └── yaml_preview_panel_test.dart
    └── types/                              # one folder per wizard mirroring lib/wizards/types/
```

The implementer may adjust this layout if implementation reveals a better split (e.g., flattening `types/<wizard>/steps/` into a single file if the steps stay small). The per-unit `**Files:**` sections below are authoritative for what each PR creates.

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```mermaid
flowchart TD
  Nav[Drawer / Rail "Create" submenu] --> Registry[wizard_registry.dart RBAC-gated list]
  Registry -- tap type --> Route[go_router /clusters/:c/wizards/:type/new]
  Route --> Screen[WizardScreenScaffold]
  Screen --> Stepper[WizardStepperMobile stateless]
  Screen --> Body[Per-step form widget]
  Body -- update --> Controller[Per-wizard Riverpod controller]
  Controller -- next --> Validate[Client-side required-fields gate]
  Validate -- last step --> Preview[WizardPreviewClient POST /v1/wizards/:type/preview]
  Preview -- 200 yaml --> ReviewStep[YamlPreviewPanel read-only]
  Preview -- 422 errors --> ErrorRouter[map field path to step index]
  ErrorRouter --> Body
  ReviewStep -- Apply --> ApplyCtrl[yaml_apply_controller.apply]
  ApplyCtrl -- POST /v1/yaml/apply --> Result[Apply result panel]
  Result -- success --> Pop[Nav back to resource list / detail]
  Result -- failure --> ReviewStep
  Controller -. cluster mismatch on apply .-> Mismatch[ConfirmSheet: discard or stay]
```

The wizard pipeline is layered: registry + RBAC drives nav; routing lands on a generic scaffold; the scaffold composes a stateless stepper + per-step form; form widgets push updates into a per-wizard Riverpod controller; the controller owns the preview + apply state machine and shares `WizardPreviewClient` and `yaml_apply_controller` across all 28 wizards. The only per-wizard code is the form steps, the typed form record, and the field-error router map.

---

## Implementation Units

### U1. PR-3a — Wizard infrastructure + ConfigMap, Secret, Service

**Goal:** An operator opens the drawer's new "Create" submenu, taps "ConfigMap", fills two key-value pairs, advances to the Review step, sees the YAML preview rendered, and taps Apply — backend creates the ConfigMap and the operator returns to the namespace's ConfigMap list with the new entry visible. Same flow works for Secret (key-value with values base64-encoded server-side via `stringData`) and Service (port + targetPort + selector).

**Requirements:** R1 (3 of 28 wizards), R2, R3, R4, R5, R6, R7, R8, R9, R10.

**Dependencies:** M2 complete (PR-2a + PR-2b shipped). No external prerequisites. PR-2b's `yaml_apply_controller.dart` is consumed unchanged.

**Files:**
- Create: `mobile/lib/wizards/wizard_controller.dart` — generic `WizardController<TForm>` base extending `AutoDisposeFamilyNotifier<WizardState<TForm>, WizardKey>`. Methods: `updateForm(TForm Function(TForm))`, `goToStep(int)`, `next()`, `back()`, `preview()`, `apply()`, `discard()`. State: `currentStep`, `form: TForm`, `previewState: idle|loading|ready(yaml)|errors(List<FieldError>)`, `applyState: idle|applying|applied|failed(error)`, `pinnedClusterId: String`.
- Create: `mobile/lib/wizards/wizard_preview_client.dart` — Dio wrapper. `Future<PreviewResult> preview(String type, Map<String, dynamic> body)`. Returns `PreviewYaml(yaml)` (200) or `PreviewErrors(List<FieldError>)` (422). Surfaces other status codes as `DioException` for the global error mapper.
- Create: `mobile/lib/wizards/wizard_registry.dart` — `WizardEntry { type, scope, label, kind, group, createVerb, requiresNamespace }` records. Catalogue of all 28 entries (PR-3a registers the full list even though only ConfigMap/Secret/Service have screens; the rest map to a "Coming soon" placeholder route until PR-3b–PR-3e fill them).
- Create: `mobile/lib/wizards/wizard_step.dart` — `WizardStep { title, description? }` model + `StepFieldErrors` typedef.
- Create: `mobile/lib/wizards/widgets/wizard_stepper_mobile.dart` — stateless stepper. `StatelessWidget` with `steps`, `currentStep`, `onStepClick`. Horizontal layout on tablet (≥768), vertical on phone.
- Create: `mobile/lib/wizards/widgets/wizard_screen_scaffold.dart` — generic shell: `AppBar` with title, body slotting the current step widget, footer with Back/Next/Apply buttons wired to the controller. Renders the cluster pill so the operator sees the pinned cluster.
- Create: `mobile/lib/wizards/widgets/key_value_table.dart` — `KeyValueTable` widget. Repeating rows of (key, value) `TextField`s with add/remove. Used by ConfigMap, Secret, and later untyped credential forms.
- Create: `mobile/lib/wizards/widgets/yaml_preview_panel.dart` — read-only `code_text_field` with YAML grammar. Loading + error states.
- Create: `mobile/lib/wizards/types/configmap/configmap_wizard_screen.dart` + `configmap_wizard_controller.dart` + `steps/configmap_configure_step.dart` + `steps/configmap_review_step.dart`.
- Create: `mobile/lib/wizards/types/secret/secret_wizard_screen.dart` + controller + steps (mirror ConfigMap; values surface via `stringData` for the Configure step's UX, server handles base64 encoding).
- Create: `mobile/lib/wizards/types/service/service_wizard_screen.dart` + controller + steps.
- Create: `mobile/lib/routing/wizard_routes.dart` — typed go_router routes for `/clusters/:clusterId/wizards/configmap/new`, `/wizards/secret/new`, `/wizards/service/new`. Placeholder routes for the other 25 types pointing at a `WizardComingSoonScreen`.
- Modify: `mobile/lib/routing/app_router.dart` — register wizard route group.
- Modify: `mobile/lib/widgets/main_drawer.dart` (or whichever drawer/rail file PR-1c shipped) — add a "Create" submenu listing wizards filtered by RBAC `create` verb on each kind.
- Modify: `mobile/lib/auth/permissions.dart` — confirm `canPerform(rbac, 'create', kind, namespace)` covers the wizard nav-gating case (no code change expected; just verify call site).
- Test: `mobile/test/wizards/wizard_controller_test.dart` — state machine transitions: form update → next → preview-loading → preview-ready → apply-applying → apply-applied. Cluster-mismatch abort on apply.
- Test: `mobile/test/wizards/wizard_preview_client_test.dart` — happy path returns `PreviewYaml`; 422 with `error.detail` parses into `PreviewErrors`; other errors bubble up.
- Test: `mobile/test/wizards/widgets/wizard_stepper_mobile_test.dart` — back-nav on completed steps; future steps disabled; current step highlighted; phone-vs-tablet layout golden.
- Test: `mobile/test/wizards/widgets/key_value_table_test.dart` — add/remove rows; trailing empty row auto-removed; duplicate key warning surface.
- Test: `mobile/test/wizards/widgets/yaml_preview_panel_test.dart` — read-only; YAML grammar applied; loading/error states.
- Test: `mobile/test/wizards/types/configmap/configmap_wizard_test.dart` — happy path: fill 2 KV pairs → preview → apply → success; field-error on duplicate key surfaces under Configure step.
- Test: `mobile/test/wizards/types/secret/secret_wizard_test.dart` — same as ConfigMap; server-side encoding via `stringData`.
- Test: `mobile/test/wizards/types/service/service_wizard_test.dart` — port + targetPort + selector → preview YAML round-trip.
- Modify: `CLAUDE.md` — append a "Build Progress" line noting M3 PR-3a shipped (infrastructure + 3 wizards).

**Approach:**
- Land the shared infrastructure (`wizard_controller.dart`, `wizard_preview_client.dart`, `wizard_registry.dart`, `wizard_step.dart`, the four shared widgets) as the first commit. Keep the surface generic — no per-wizard knowledge bleeds into the base.
- Land ConfigMap as the second commit. It's the smallest end-to-end consumer and validates the controller/scaffold contract. Reviewer reads the ConfigMap files and forms a mental model that applies to every later wizard.
- Land Secret + Service as the third commit. They reuse ConfigMap's pattern; the diff is mostly form fields. This commit also validates the registry's RBAC-gating against three different kinds.
- Drawer's "Create" submenu is RBAC-gated: each wizard entry shows only when `canPerform(rbac, 'create', entry.kind, activeNamespace || '')` returns true. For cluster-scoped entries (ClusterIssuer, ClusterSecretStore — landing in PR-3e), the namespace argument is `''` and `canPerform` checks cluster-scoped permissions.
- The placeholder routes for un-ported wizards land a `WizardComingSoonScreen` showing "This wizard ships in PR-3b/c/d/e — open k8scenter on a desktop for now". This avoids a half-broken drawer where some entries 404. Each subsequent PR replaces the placeholder route's target with the real screen.
- Cluster pinning: `WizardController` constructor reads `ref.read(activeClusterProvider)` and stores it. Apply checks current vs pinned and aborts via `ConfirmSheet` if drift. The "discard or stay" sheet is a thin wrapper over M2's `ConfirmSheet`.
- Preview round-trip: tap Next on the last form step → controller transitions to `previewState: loading` → calls `WizardPreviewClient.preview(type, form.toJson())` → on success transitions to `previewState: ready(yaml)` and advances `currentStep` → on errors sets `previewState: errors(...)`, runs `errorRouter` over the field paths to find the lowest-index step that owns any error, and rewinds `currentStep` to it. The errored step's widget reads the per-step error map via the controller and surfaces messages under the relevant inputs.
- Field-error mapping for ConfigMap: `data` errors map to step 0 (Configure). For Secret: same. For Service: `spec.ports[*]` and `spec.selector` map to step 0. Each wizard exposes a tiny `Map<String, int> errorRouter` constant that the controller consumes — mappable in 5 lines per wizard.

**Patterns to follow:**
- `frontend/components/wizard/WizardStepper.tsx` — stateless contract for `WizardStepperMobile`.
- `frontend/islands/ConfigMapWizard.tsx`, `frontend/islands/SecretWizard.tsx`, `frontend/islands/ServiceWizard.tsx` — form bodies + step structure.
- `frontend/components/wizard/WizardReviewStep.tsx` (if it exists; otherwise the inline review step in each island) — preview-then-apply UX.
- `mobile/lib/api/yaml_apply_controller.dart` (PR-2b) — state machine shape; reused for the apply leg of the wizard.
- `mobile/lib/widgets/confirm_sheet.dart` (PR-2a) — cluster-mismatch sheet.

**Test scenarios:**
- Happy path: create a ConfigMap with 3 key-value pairs in namespace `default`. Configure step renders; Next triggers preview; preview YAML renders on Review step; Apply triggers `POST /v1/yaml/apply`; result panel shows `1 created`; navigation pops to ConfigMap list and the new entry is visible. Covers F1 / R1 / R2 / R4 / R8.
- Happy path: create a Secret with 2 key-value pairs (`username`, `password`). Form uses `stringData` so the operator types raw values; backend encodes to base64. Preview YAML shows `stringData:`; apply succeeds; secret detail shows the values redacted (revealable per M1).
- Happy path: create a Service with one port (`80→8080`) and a selector (`app=web`). Preview shows `spec.ports[0]` and selector; apply succeeds.
- Edge case: ConfigMap with duplicate keys client-side. Configure step auto-merges (last one wins) before submission, with an inline warning under the duplicate row. Backend never sees the duplicate.
- Edge case: ConfigMap with empty data block (operator advances without filling anything). Server returns 422 `error.detail = [{field: 'data', message: 'must contain at least one entry'}]`. Controller routes back to Configure step; inline message renders under the table; operator fills a row and advances again.
- Edge case: Service without selector. Server returns 422; same flow as above.
- Error path: preview returns 5xx (backend error). Wizard surfaces `ErrorState` widget on the Review step transition and offers a Retry button. State stays at `previewState: idle` so the operator can re-tap Next. No data lost.
- Error path: apply returns 409 SSA conflict (someone applied the same name concurrently). Result panel shows the backend message verbatim; operator stays on Review step with the YAML still rendered. Tapping Apply again hits the conflict again — operator must Back, change the name, and re-preview.
- Error path: 401 mid-preview (token expired). Dio's `AuthInterceptor` (PR-1b) refreshes and retries once. If refresh fails, app routes to `/login`; on return the wizard is gone (auto-disposed) and the operator restarts. Acceptable cost — wizard drafts are ephemeral by R6.
- RBAC: operator without `create configmaps` in the active namespace doesn't see ConfigMap in the Create drawer. Without `create *` (cluster-wide), the entry is hidden entirely.
- Cluster mismatch: operator switches clusters mid-wizard via the cluster pill. On tapping Apply, controller detects mismatch and surfaces the "Cluster changed — discard or stay" ConfirmSheet. Discard → wizard pops; Stay → controller does not re-pin (operator must explicitly switch back to the original cluster to apply).
- Integration: drawer "Create" submenu shows only the three wizards from this PR plus the 25 placeholder entries marked "Coming soon" until later PRs replace them.
- Integration: theme parity. Stepper + ConfigMap Configure step golden-tested against Nexus and Dracula.
- Covers AE2 (operator creates a ConfigMap from a phone — R8 first PR demonstrable surface).

**Verification:**
- `cd mobile && flutter analyze` produces zero warnings, zero errors.
- `cd mobile && flutter test` passes, including all wizard infrastructure + ConfigMap + Secret + Service tests.
- `cd frontend && deno task check` passes (no web changes expected, but verify nothing in `frontend/lib/api.ts` accidentally regressed).
- `make check-themes` passes.
- Smoke against homelab: log in, drawer → Create → ConfigMap, fill 2 entries, apply. Verify with `kubectl get cm -n default <name> -o yaml`. Verify audit log row exists.
- Smoke against homelab: same flow for Secret and Service.

---

### U2. PR-3b — Workloads wizards (Deployment, Job, CronJob, DaemonSet, StatefulSet)

**Goal:** Operator creates a Deployment with image, replicas, port, resources requests/limits, and probes; a Job with backoff and parallelism; a CronJob with a schedule and a Job template; a DaemonSet (probes, no replicas); a StatefulSet (serviceName + volumeClaimTemplates). All from a phone or tablet.

**Requirements:** R1 (5 of 28 wizards land), R2, R3, R4, R5, R6, R7, R8, R9, R10.

**Dependencies:** U1 (PR-3a). All workload wizards consume the shared infrastructure and the YAML apply controller from M2.

**Files:**
- Create: `mobile/lib/wizards/types/deployment/` — `deployment_wizard_screen.dart`, `deployment_wizard_controller.dart`, `steps/deployment_basics_step.dart`, `steps/deployment_networking_step.dart`, `steps/deployment_resources_step.dart`, `steps/deployment_review_step.dart`. Mirrors the 4-step web wizard.
- Create: `mobile/lib/wizards/types/job/` — 2-step (Configure + Review). Configure carries: image, command, args, restartPolicy, backoffLimit, parallelism, completions, env vars (via `RepeatingRowGroup`).
- Create: `mobile/lib/wizards/types/cronjob/` — 2-step. Configure carries: schedule (cron string + a small "common patterns" picker), concurrencyPolicy, suspend, plus the embedded job template (image, command, restartPolicy).
- Create: `mobile/lib/wizards/types/daemonset/` — 2-step. Configure carries: container basics + probes (RepeatingRowGroup for env, single-pick for liveness/readiness/startup probe shape: HTTP / TCP / Exec).
- Create: `mobile/lib/wizards/types/statefulset/` — 2-step. Configure carries: serviceName (required), replicas, container basics, volumeClaimTemplates (RepeatingRowGroup over `name + storageClass + accessModes + size`).
- Create: `mobile/lib/wizards/widgets/repeating_row_group.dart` — generic repeating sub-form widget. Used by Job env, CronJob job-template env, DaemonSet probes, StatefulSet volumeClaimTemplates, and later Ingress rules + NetworkPolicy peers in PR-3c.
- Create: `mobile/lib/wizards/widgets/probe_form.dart` — sub-widget for liveness/readiness/startup probes (handler picker + shared fields: initialDelay, period, timeout, failureThreshold, successThreshold). Reused by Deployment, DaemonSet, StatefulSet.
- Create: `mobile/lib/wizards/widgets/resources_form.dart` — sub-widget for `resources.requests.{cpu,memory}` + `resources.limits.{cpu,memory}` with unit suffix hints. Reused by Deployment, DaemonSet, StatefulSet.
- Modify: `mobile/lib/routing/wizard_routes.dart` — replace placeholder routes for these 5 types with their real screens.
- Modify: `mobile/lib/wizards/wizard_registry.dart` — confirm registry entries match (no new entries; PR-3a registered all 28).
- Test: `mobile/test/wizards/types/deployment/deployment_wizard_test.dart` — full 4-step happy path; field-error routing (e.g., bad image string returns 422 → routes back to Basics step); probe form validation.
- Test: `mobile/test/wizards/types/job/job_wizard_test.dart` — happy path; backoffLimit edge cases; env-var KV entry.
- Test: `mobile/test/wizards/types/cronjob/cronjob_wizard_test.dart` — happy path with `0 */6 * * *` schedule; common-patterns picker fills the field.
- Test: `mobile/test/wizards/types/daemonset/daemonset_wizard_test.dart` — happy path; probe form integration.
- Test: `mobile/test/wizards/types/statefulset/statefulset_wizard_test.dart` — happy path including a volumeClaimTemplate; missing serviceName surfaces 422 → routes back.
- Test: `mobile/test/wizards/widgets/repeating_row_group_test.dart` — add/remove/reorder; empty trailing row auto-removed; passes form values up.
- Test: `mobile/test/wizards/widgets/probe_form_test.dart` — handler switch; field hide/show per handler.
- Test: `mobile/test/wizards/widgets/resources_form_test.dart` — unit suffix parsing (`100m`, `512Mi`).
- Modify: `CLAUDE.md` — append a "Build Progress" line noting M3 PR-3b shipped.

**Approach:**
- Sub-agent swarming: dispatch 2–3 Sonnet agents in parallel after PR-3b's first commit lands the shared `repeating_row_group.dart`, `probe_form.dart`, `resources_form.dart`. One agent ports Deployment, one ports Job + CronJob, one ports DaemonSet + StatefulSet. Each agent produces ≤5-file commits per CLAUDE.md Rule 2.
- Deployment is the most complex (4 steps, probes, resources, networking via Service-side-by-side). Land it first to validate the shared widgets carry their weight.
- Job + CronJob share the embedded job-template structure. CronJob's job-template form should compose Job's controller's typed form record verbatim — this enforces structural consistency.
- DaemonSet and StatefulSet diff Deployment's Basics step by removing replicas (DaemonSet) or adding serviceName + volumeClaimTemplates (StatefulSet). Reuse Deployment's Basics step widget where possible; introduce per-wizard variants only where the form genuinely differs.
- Common cron patterns picker (CronJob): hard-coded list of `["@hourly", "@daily", "@weekly", "@monthly", "0 */6 * * *", "*/15 * * * *"]`. Tap a pattern → schedule field fills. Operator can edit afterwards. Same UX web uses.
- Field-error router: each wizard's `errorRouter` maps `spec.template.spec.containers[0].image → 0` (Basics), `spec.template.spec.containers[0].ports → 1` (Networking, Deployment only), `spec.template.spec.containers[0].resources → 2` (Resources, Deployment only), `spec.template.spec.containers[0].livenessProbe → 0` (Basics for DaemonSet/StatefulSet/Job; Resources step doesn't exist there). Per-wizard maps, ≤10 lines each.

**Patterns to follow:**
- `frontend/islands/DeploymentWizard.tsx` — 4-step state shape, validateStep, preview-on-Review-entry.
- `frontend/islands/JobWizard.tsx`, `frontend/islands/CronJobWizard.tsx` — 2-step shape with embedded job template.
- `frontend/islands/DaemonSetWizard.tsx`, `frontend/islands/StatefulSetWizard.tsx` — 2-step variants.
- M3 PR-3a infrastructure (`wizard_controller.dart`, `wizard_screen_scaffold.dart`, `key_value_table.dart`, `yaml_preview_panel.dart`).

**Test scenarios:**
- Happy path: Deployment with image `nginx:1.27`, 3 replicas, port 80, resources `requests.cpu=100m / requests.memory=128Mi / limits.cpu=500m / limits.memory=512Mi`, HTTP liveness probe at `/healthz:80`. Preview shows full nested YAML; apply succeeds.
- Happy path: Job with `parallelism: 3`, `completions: 6`, `backoffLimit: 4`, `restartPolicy: OnFailure`. Preview round-trip succeeds.
- Happy path: CronJob with schedule `0 2 * * *`, concurrencyPolicy `Forbid`, embedded Job template. Preview shows `spec.jobTemplate.spec.template.spec.containers[0]`.
- Happy path: DaemonSet on a `node-role.kubernetes.io/worker` selector. Form's nodeSelector key-value table expresses it; preview YAML carries `spec.template.spec.nodeSelector`.
- Happy path: StatefulSet with `serviceName: web`, 3 replicas, single volumeClaimTemplate (`name: data, storageClass: standard, size: 5Gi`). Preview shows the template; apply succeeds.
- Edge case: Deployment with replicas=0 (paused on creation). Preview accepts; apply succeeds; deployment shows 0/0 ready.
- Edge case: CronJob with malformed cron (`* * * *` — only 4 fields). Server returns 422 `field: spec.schedule`. Router rewinds to Configure step; inline message renders under the schedule input.
- Edge case: StatefulSet without serviceName. Server returns 422 `field: spec.serviceName`. Routes back to Configure; inline message renders.
- Error path: Deployment with image referencing a private registry the cluster can't pull. Apply succeeds (k8s accepts the spec); pod ImagePullBackOff is visible in subsequent resource detail. Wizard's responsibility ends at successful SSA — operator monitors via the resource list.
- Integration: cluster-mismatch on Deployment's 4-step wizard. Operator advances through 3 steps, switches clusters, taps Apply. ConfirmSheet fires. Discard pops; Stay leaves operator with the form intact but blocked from applying until the active cluster matches the pinned cluster.
- Integration: Field-error round-trip. Operator submits Deployment without image. Preview returns 422; controller rewinds to Basics step; "Image is required" renders under the image input. Operator fills, taps Next, preview succeeds, advances to Review.
- Covers AE3 (Deployment apply demonstrable per R8 PR-3b end).

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean across all five wizards plus shared widgets.
- `make check-themes` passes.
- Smoke against homelab: create Deployment, scale via M2 actions, restart, delete. Verify lifecycle.
- Smoke against homelab: create Job → watch it complete → delete. Same for CronJob (verify next-scheduled-time appears in detail).

---

### U3. PR-3c — Networking, scaling, RBAC, storage class, namespace limits (Ingress, NetworkPolicy, HPA, PDB, RoleBinding, StorageClass, NamespaceLimits)

**Goal:** Operator drops a NetworkPolicy that quarantines a misbehaving namespace, configures an HPA on a flapping deployment, sets up a RoleBinding to grant a teammate temporary access, applies a NamespaceLimits (ResourceQuota + LimitRange) to a noisy namespace, and creates an Ingress for a new public service. All from mobile.

**Requirements:** R1 (7 of 28 wizards), R2, R3, R4, R5, R6, R7, R8, R9, R10. Plus: NamespaceLimits exercises multi-resource apply (ResourceQuota + LimitRange in one preview YAML).

**Dependencies:** U1 (PR-3a). U2 (PR-3b) is independent — could merge in either order, but PR-3c assumes PR-3b's `repeating_row_group.dart` is available (used for Ingress rules, NetworkPolicy peers, and RoleBinding subjects).

**Files:**
- Create: `mobile/lib/wizards/types/ingress/` — `ingress_wizard_screen.dart`, controller, steps. Configure step composes `RepeatingRowGroup` for rules (each rule has host + RepeatingRowGroup of paths, each path has path + pathType + service + port).
- Create: `mobile/lib/wizards/types/networkpolicy/` — same shape. Configure carries policyTypes (Ingress / Egress checkboxes), podSelector (matchLabels via KeyValueTable), ingress rules (RepeatingRowGroup), egress rules.
- Create: `mobile/lib/wizards/types/hpa/` — Configure carries scaleTargetRef (KindPicker over Deployment/StatefulSet/ReplicaSet, name picker scoped to active namespace), minReplicas, maxReplicas, metrics (RepeatingRowGroup of `type + resource.name + target.type + target.averageUtilization`).
- Create: `mobile/lib/wizards/types/pdb/` — Configure carries selector (KeyValueTable), policy (radio: minAvailable vs maxUnavailable), value (int or `%` percentage).
- Create: `mobile/lib/wizards/types/rolebinding/` — Configure carries roleRef (kind picker: Role / ClusterRole, then a name picker fed by `GET /v1/resources/roles?namespace=<ns>` or `/clusterroles`), subjects (RepeatingRowGroup of `kind + name + namespace?` where kind is User / Group / ServiceAccount).
- Create: `mobile/lib/wizards/types/storageclass/` — Configure carries provisioner (text input — provisioner names are CSI driver–dependent), parameters (KeyValueTable), reclaimPolicy (radio), volumeBindingMode (radio).
- Create: `mobile/lib/wizards/types/namespace_limits/` — Configure carries resourceQuota fields (`hard.cpu`, `hard.memory`, `hard.pods`) plus limitRange defaults/maxes (RepeatingRowGroup of `type + resource + default + defaultRequest + max + min`). Preview returns multi-doc YAML; apply parses both results.
- Create: `mobile/lib/wizards/widgets/kind_picker.dart` — generic `KindPicker` widget. Takes a list of allowed kinds, returns selection. Used by HPA and RoleBinding.
- Create: `mobile/lib/wizards/widgets/named_resource_picker.dart` — picker widget that fetches a list of resources of a given kind in a given namespace and presents them. Used by HPA's scaleTargetRef name and RoleBinding's roleRef name.
- Modify: `mobile/lib/routing/wizard_routes.dart` — replace placeholder routes for these 7 types with their real screens.
- Modify: `mobile/lib/wizards/widgets/yaml_preview_panel.dart` — confirm it renders multi-doc YAML cleanly (PR-3a left this for PR-3c when NamespaceLimits actually exercises it). Likely no change needed — `code_text_field` handles multi-doc YAML by default. Verify and add a test if needed.
- Test: `mobile/test/wizards/types/ingress/ingress_wizard_test.dart` — happy path with one rule, two paths; missing service-name validation; TLS section.
- Test: `mobile/test/wizards/types/networkpolicy/networkpolicy_wizard_test.dart` — quarantine policy (ingress=`[]`, egress=`[]` with `policyTypes: [Ingress, Egress]`); allow-from-namespace policy; podSelector validation.
- Test: `mobile/test/wizards/types/hpa/hpa_wizard_test.dart` — happy path with `Deployment/web` target, min=2 max=10, CPU 80%; min > max validation.
- Test: `mobile/test/wizards/types/pdb/pdb_wizard_test.dart` — minAvailable=2; maxUnavailable=`50%`; both set is mutually exclusive.
- Test: `mobile/test/wizards/types/rolebinding/rolebinding_wizard_test.dart` — happy path; ServiceAccount subject requires namespace; User/Group subject's namespace field hidden.
- Test: `mobile/test/wizards/types/storageclass/storageclass_wizard_test.dart` — happy path with provisioner `kubernetes.io/aws-ebs`, params `type=gp3`, reclaim=Retain.
- Test: `mobile/test/wizards/types/namespace_limits/namespace_limits_wizard_test.dart` — happy path; multi-doc preview YAML (`---` separator) + apply result panel renders both ResourceQuota and LimitRange rows.
- Test: `mobile/test/wizards/widgets/kind_picker_test.dart` — selection callback fires; disabled kinds greyed.
- Test: `mobile/test/wizards/widgets/named_resource_picker_test.dart` — fetch on namespace change; loading state; empty state ("No Roles in this namespace").
- Modify: `CLAUDE.md` — append a "Build Progress" line noting M3 PR-3c shipped.

**Approach:**
- NetworkPolicy and Ingress are the most complex per-form (nested RepeatingRowGroups). Land them first to validate the nested-repeating pattern carries.
- HPA and RoleBinding both need runtime resource list fetches (target deployment list for HPA, roles list for RoleBinding). Use `FutureProvider.autoDispose.family((clusterId, namespace, kind))` so the picker re-fetches on namespace change. Cache for the lifetime of the wizard.
- StorageClass is the smallest of this batch; lands last as a sanity check that the infrastructure handles cluster-scoped resources correctly (no namespace required).
- NamespaceLimits is the multi-resource case. Preview YAML has two docs separated by `---`. Apply hits `/v1/yaml/apply` which already handles multi-doc input (PR-2b's controller parses an array of results). Result panel renders both rows under one summary card.
- PDB's mutually-exclusive minAvailable/maxUnavailable: form uses a radio to pick one, then renders a single value field. Both can't be set client-side, so the server-side conflict never arises.
- RoleBinding's subject form: when subject `kind == ServiceAccount`, the namespace field is required and renders; for User/Group, the namespace field is hidden. Server validates regardless.

**Patterns to follow:**
- `frontend/islands/IngressWizard.tsx`, `frontend/islands/NetworkPolicyWizard.tsx` — nested rule structures.
- `frontend/islands/HPAWizard.tsx` — scaleTargetRef picker pattern.
- `frontend/islands/PDBWizard.tsx` — minAvailable / maxUnavailable mutual exclusion.
- `frontend/islands/RoleBindingWizard.tsx` — subjects array pattern.
- `frontend/islands/StorageClassWizard.tsx` — provisioner + parameters.
- `frontend/islands/NamespaceLimitsWizard.tsx` — multi-resource output structure.
- M3 PR-3a + PR-3b infrastructure.

**Test scenarios:**
- Happy path: NetworkPolicy quarantining `default` namespace — `policyTypes: [Ingress, Egress]`, `podSelector: {}`, no rules. Preview shows `spec.ingress: []`, `spec.egress: []`. Apply succeeds; pods in `default` lose all network access (verify with smoke test on a single test namespace, not actually default).
- Happy path: Ingress with one rule, host `app.example.com`, path `/`, service `web`, port 80. Preview shows `spec.rules[0].http.paths[0]`. Apply succeeds.
- Happy path: HPA on `Deployment/web`, min=2, max=10, CPU 80% utilization. Preview shows `spec.scaleTargetRef.kind: Deployment`. Apply succeeds; HPA detail later shows current/target replica counts.
- Happy path: RoleBinding granting Role `view` to ServiceAccount `default/test-sa`. Subject namespace required and rendered. Preview shows `subjects[0]` with kind/name/namespace. Apply succeeds.
- Happy path: NamespaceLimits — ResourceQuota `cpu=4 memory=8Gi pods=100` + LimitRange Container default `cpu=200m memory=256Mi`. Preview YAML has two docs separated by `---`. Apply result panel shows two rows: `ResourceQuota/<name>: created` and `LimitRange/<name>: created`.
- Edge case: HPA min > max. Server returns 422; routes back to Configure; inline message under min/max inputs.
- Edge case: RoleBinding with User kind and a namespace value. Server ignores the namespace (User is cluster-scoped). Preview YAML reflects the server's interpretation; apply succeeds. Acceptable — operator sees the truth in the preview.
- Edge case: Ingress without TLS. TLS section is collapsed; preview YAML omits `spec.tls`. Apply succeeds.
- Edge case: StorageClass with provisioner that doesn't exist on the cluster. Apply succeeds (k8s accepts unknown provisioners); PVCs binding to the class will fail later. Wizard's responsibility ends at SSA. Operator monitors via subsequent PVC creation attempts.
- Error path: NamespaceLimits where one document succeeds and the other fails (e.g., LimitRange has a malformed default unit). Apply result panel shows `1 created, 1 failed` and renders the LimitRange's error inline. Operator can Back, fix, re-preview, re-apply. The created ResourceQuota stays — operator must delete it manually if they want to start over. Acceptable cost; matches web behavior.
- Integration: HPA's named-resource picker on a namespace with no Deployments. Picker shows EmptyState ("No Deployments in this namespace"). Operator can switch namespace via the namespace picker; named-resource picker re-fetches.
- Integration: NetworkPolicy quarantine demonstrable per R8 PR-3c end.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- `make check-themes` passes.
- Smoke against homelab: create a NetworkPolicy in a test namespace, verify `kubectl describe netpol -n test`. Apply HPA on a test deployment, verify `kubectl get hpa`. Apply NamespaceLimits, verify both `kubectl get quota` and `kubectl get limitrange` show the new resources.
- Smoke against homelab: create a RoleBinding granting `view` to a test ServiceAccount, then verify the SA can `kubectl get pods` (impersonation test).

---

### U4. PR-3d — Storage / backup family (PVC, Snapshot, ScheduledSnapshot, RestoreSnapshot, VeleroBackup, VeleroRestore, VeleroSchedule)

**Goal:** Operator provisions a PVC for a stateful workload, takes an ad-hoc volume snapshot, schedules nightly snapshots, restores from a snapshot via a picker, schedules a Velero backup of a critical namespace, restores from a Velero backup, and configures a recurring Velero schedule. All from mobile.

**Requirements:** R1 (7 of 28 wizards land), R2, R3, R4, R5, R6, R7, R8, R9, R10. Plus: RestoreSnapshot exercises the picker pattern (no form fill, just select-and-confirm).

**Dependencies:** U1 (PR-3a). Independent of U2 and U3 in terms of code; benefits from U3's `named_resource_picker.dart` if it landed first (RestoreSnapshot picker reuses the pattern). Can merge after U3 to consume the picker, or before with a one-off picker that the next PR refactors away.

**Files:**
- Create: `mobile/lib/wizards/types/pvc/` — Configure carries: storageClass picker (uses `named_resource_picker` over StorageClass kind), accessModes (multi-checkbox), size (int + unit suffix), volumeMode (radio: Filesystem / Block).
- Create: `mobile/lib/wizards/types/snapshot/` — Configure carries: source PVC picker (named_resource_picker over PVCs in active namespace), volumeSnapshotClassName (text or picker if VSCs are queryable).
- Create: `mobile/lib/wizards/types/scheduled_snapshot/` — 3-step (Source & Schedule, Retention, Review). Source step picks PVC + cron schedule; Retention step sets `keep: int` and optional `maxAgeDays: int`.
- Create: `mobile/lib/wizards/types/restore_snapshot/` — picker-style, 2-step (Select Snapshot + Review). First step lists snapshots in active namespace via `GET /v1/resources/volumesnapshots?namespace=<ns>` (mobile reuses M2's `RollbackPicker` widget shape, parameterized for snapshots). Tap selects; Review shows the resulting PVC YAML preview; Apply creates the PVC bound to the snapshot.
- Create: `mobile/lib/wizards/types/velero_backup/` — Configure carries: includedNamespaces (multi-namespace picker via KeyValueTable or RepeatingRowGroup), excludedNamespaces, includeClusterResources (toggle), storageLocation (text), TTL (duration string).
- Create: `mobile/lib/wizards/types/velero_restore/` — Configure carries: backupName picker (`named_resource_picker` over Velero Backup CRDs), namespaceMapping (KeyValueTable: source-ns → target-ns), restorePVs (toggle).
- Create: `mobile/lib/wizards/types/velero_schedule/` — Configure carries: schedule (cron), template (full Velero Backup spec via the same form as VeleroBackup), TTL.
- Create: `mobile/lib/wizards/widgets/list_picker_screen.dart` — refactor of M2's `RollbackPicker` into a generic `ListPickerScreen<T>` widget. Used by RestoreSnapshot and VeleroRestore's backup picker. **Decision deferred to implementation:** if the refactor proves too disruptive to M2's RollbackPicker, copy the widget shape and revisit in M5 polish.
- Create: `mobile/lib/wizards/widgets/multi_namespace_picker.dart` — multi-select namespace picker using `Chip`s. Reused by VeleroBackup/Schedule.
- Create: `mobile/lib/wizards/widgets/duration_input.dart` — text field with parsing for Velero-style duration strings (`24h`, `7d`, `30m`).
- Modify: `mobile/lib/routing/wizard_routes.dart` — replace placeholder routes for these 7 types.
- Test: `mobile/test/wizards/types/pvc/pvc_wizard_test.dart` — happy path; access mode validation (RWO/ROX/RWX/RWOP); size unit parsing.
- Test: `mobile/test/wizards/types/snapshot/snapshot_wizard_test.dart` — happy path; missing source PVC validation.
- Test: `mobile/test/wizards/types/scheduled_snapshot/scheduled_snapshot_wizard_test.dart` — 3-step happy path; retention validation (keep > 0).
- Test: `mobile/test/wizards/types/restore_snapshot/restore_snapshot_wizard_test.dart` — picker renders snapshots, tap confirms, apply creates PVC.
- Test: `mobile/test/wizards/types/velero_backup/velero_backup_wizard_test.dart` — happy path; namespace inclusion/exclusion mutual exclusion.
- Test: `mobile/test/wizards/types/velero_restore/velero_restore_wizard_test.dart` — picker over backups; namespace mapping.
- Test: `mobile/test/wizards/types/velero_schedule/velero_schedule_wizard_test.dart` — happy path; embedded backup template.
- Test: `mobile/test/wizards/widgets/list_picker_screen_test.dart` — empty state; loading state; tap dispatches selection.
- Test: `mobile/test/wizards/widgets/multi_namespace_picker_test.dart` — chip add/remove; select-all toggle.
- Test: `mobile/test/wizards/widgets/duration_input_test.dart` — `24h`, `7d`, `30m`, `0s`, invalid `xyz`.
- Modify: `CLAUDE.md` — append a "Build Progress" line noting M3 PR-3d shipped.

**Approach:**
- Land PVC and Snapshot first as smallest. They validate the named-resource picker works for storage CRDs.
- ScheduledSnapshot is the only 3-step wizard in this batch; lands second.
- RestoreSnapshot is the picker outlier. Decide at implementation whether to refactor M2's `RollbackPicker` into a shared `ListPickerScreen<T>` (cleaner) or copy the shape (faster, lower risk). Pick whichever costs less in the moment.
- Velero trio (Backup, Restore, Schedule) shares structure: VeleroBackup has the form; VeleroSchedule embeds Backup's form as a template; VeleroRestore picks an existing Backup. Implement Backup first; Schedule reuses Backup's controller's form record; Restore reuses the picker pattern.
- `multi_namespace_picker.dart` introduces the Chip-based multi-select widget. Phone UX: tap a chip in a horizontal scrolling list to toggle. Tablet UX: same widget, just more chips visible at once. No phone-specific layout.
- Velero backup status (success/failed/in-progress) is *not* shown by the wizard — it's only shown in the resource detail (M2 showed it). The wizard's job ends at apply.

**Patterns to follow:**
- `frontend/islands/PVCWizard.tsx`, `frontend/islands/SnapshotWizard.tsx`, `frontend/islands/ScheduledSnapshotWizard.tsx`, `frontend/islands/RestoreSnapshotWizard.tsx`.
- `frontend/islands/VeleroBackupWizard.tsx`, `frontend/islands/VeleroRestoreWizard.tsx`, `frontend/islands/VeleroScheduleWizard.tsx`.
- `mobile/lib/features/resources/rollback_picker_screen.dart` (M2 PR-2b) — picker widget shape that RestoreSnapshot mirrors.
- M3 PR-3a + PR-3c infrastructure.

**Test scenarios:**
- Happy path: PVC `data` in namespace `app`, storageClass `standard`, accessMode `ReadWriteOnce`, size `5Gi`. Preview YAML shows `spec.resources.requests.storage: 5Gi`. Apply succeeds.
- Happy path: Snapshot of `data` PVC. Apply succeeds; snapshot CR appears in `kubectl get volumesnapshot`.
- Happy path: ScheduledSnapshot at `0 2 * * *`, keep=7, source `data` PVC. Apply succeeds; verify with `kubectl get schedule.velero.io`.
- Happy path: RestoreSnapshot picker shows 3 snapshots. Tap one → Review shows PVC YAML with `dataSource.kind: VolumeSnapshot`. Apply creates a new PVC bound to the snapshot.
- Happy path: VeleroBackup of `production` namespace, TTL `168h`. Apply succeeds; backup appears in `kubectl get backup.velero.io`.
- Happy path: VeleroRestore from a backup, no namespace mapping. Apply triggers a restore.
- Happy path: VeleroSchedule at `0 1 * * *` with the backup template carrying `production` namespace. Apply succeeds; schedule appears.
- Edge case: PVC with no available StorageClasses (cluster has none). Picker shows EmptyState; operator can type a class name freely. Apply succeeds (k8s accepts unknown class) but PVC pending; wizard's job done.
- Edge case: VeleroRestore backup picker with no backups. EmptyState ("No backups available — create one via the Backup wizard"). Operator can dismiss and navigate to backup wizard.
- Edge case: VeleroBackup with both included and excluded namespaces specified — server returns 422 (mutually exclusive). Routes back to Configure.
- Error path: Snapshot of a PVC that doesn't support snapshots (CSI driver lacks the feature). Apply succeeds at the API layer but snapshot status will eventually show failure. Wizard's responsibility ends at SSA. Operator monitors snapshot detail.
- Integration: pre-fetch on RestoreSnapshot's first step. Loading state shows briefly; if 401, AuthInterceptor refreshes and retries; if refresh fails, app routes to login.
- Integration: VeleroSchedule's embedded backup template form is structurally the same form as VeleroBackup. Refactor in implementation if duplication is painful; otherwise duplicate and revisit.
- Covers AE4 (Velero schedule demonstrable per R8 PR-3d end).

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- `make check-themes` passes.
- Smoke against homelab (assumes Velero installed): create a PVC, take a snapshot, schedule nightly snapshots, restore from snapshot, create a Velero backup, restore from it, schedule a recurring backup. Verify each via `kubectl get` of the appropriate kind.

---

### U5. PR-3e — CRD wizards (Certificate, Issuer/ClusterIssuer, ExternalSecret, SecretStore/ClusterSecretStore, Policy)

**Goal:** Operator creates a Certificate signed by an existing ClusterIssuer; creates a new ACME Issuer in the active namespace; creates an ExternalSecret pulling a key from a Vault SecretStore; creates a SecretStore for AWS Secrets Manager; creates a Kyverno policy from a registered template (or Gatekeeper if Kyverno isn't installed). All from mobile.

**Requirements:** R1 (final 11 of 28 wizards), R2, R3, R4, R5, R6, R7, R8, R9, R10. Plus: scope-variant handling (Issuer vs ClusterIssuer, SecretStore vs ClusterSecretStore), runtime CRD discovery (Policy engine auto-detect), and runtime issuer/store list fetches.

**Dependencies:** U1 (PR-3a) for infrastructure. U3 (PR-3c) for `named_resource_picker.dart`. U4 (PR-3d) is independent.

**Files:**
- Create: `mobile/lib/wizards/types/certificate/` — Configure carries: secretName, dnsNames (RepeatingRowGroup of strings), commonName, duration (DurationInput from PR-3d), renewBefore, issuerRef (kind picker: Issuer / ClusterIssuer, then named_resource_picker scoped to namespace for Issuer or cluster-wide for ClusterIssuer; uses `GET /v1/certmanager/issuers?namespace=<ns>` and `GET /v1/certmanager/clusterissuers`), privateKey (algorithm radio: RSA / ECDSA, size).
- Create: `mobile/lib/wizards/types/issuer/` — 3-step (Type, Configure, Review). Type step picks SelfSigned / ACME. Configure step shows different fields per type: SelfSigned has none; ACME has server URL (radio: LetsEncrypt-Prod, LetsEncrypt-Staging, Custom), email, privateKeySecretRef, solvers (RepeatingRowGroup of HTTP01 ingress class or DNS01 provider). Same component used for ClusterIssuer with `scope: WizardScope.cluster` flipping the registered type to `cluster-issuer`.
- Create: `mobile/lib/wizards/types/external_secret/` — Configure carries: secretStoreRef (kind picker: SecretStore / ClusterSecretStore, then named_resource_picker), refreshInterval (DurationInput), target.name + target.template (optional), data (RepeatingRowGroup of `secretKey + remoteRef.key + remoteRef.property?`), dataFrom (RepeatingRowGroup with extract / find variants).
- Create: `mobile/lib/wizards/types/secret_store/` — 3-step (Identity, Provider, Review). Identity step: name + namespace (or none for cluster-scoped). Provider step: provider picker over `[vault, aws, azurekv, gcpsm, kubernetes, doppler, onepassword, bitwarden, akeyless, conjur, infisical]`, then a provider-specific form. Same component used for ClusterSecretStore with `scope: WizardScope.cluster`.
- Create: `mobile/lib/wizards/types/secret_store/providers/` — one file per provider with the auth field schema:
  - `vault_provider_form.dart` — server URL, path, version (KV v1 / v2), auth method (token via secretRef, kubernetes via role+serviceAccountRef, appRole via roleId+secretIdRef).
  - `aws_provider_form.dart` — region, service (SecretsManager / ParameterStore), auth (accessKey via secretRef, role-via-IRSA).
  - `azurekv_provider_form.dart` — vaultUrl, tenantId, authType (managedIdentity / servicePrincipal).
  - `gcpsm_provider_form.dart` — projectId, auth (workloadIdentity / serviceAccountKey via secretRef).
  - `kubernetes_provider_form.dart` — server URL, auth (serviceAccountRef / cert via secretRef).
  - `doppler_provider_form.dart` — auth (token via secretRef), config + project (optional).
  - `onepassword_provider_form.dart` — connectHost, vaults, auth (token via secretRef).
  - `bitwarden_provider_form.dart` — auth (apiKey via secretRef), serverURL, organizationID.
  - `akeyless_provider_form.dart` — akeylessGwApiUrl, auth (accessId + accessType).
  - `conjur_provider_form.dart` — url, auth (apiKey / jwt / kubernetes).
  - `infisical_provider_form.dart` — hostAPI, auth (universalAuth via secretRef), workspaceId.
- Create: `mobile/lib/wizards/types/policy/` — 3-step (Template picker, Configure, Review). Engine auto-detect via `GET /v1/policies/status` at wizard open; pulls available templates from the policy template registry. Template picker step shows template name + description; Configure step renders the per-template param schema; Review step shows the rendered Kyverno ClusterPolicy or Gatekeeper ConstraintTemplate YAML.
- Create: `mobile/lib/wizards/widgets/issuer_picker.dart` — wrapper around `named_resource_picker` that shows both Issuer (namespace-scoped) and ClusterIssuer (cluster-scoped) entries in one list, distinguished by an icon. Used by Certificate.
- Create: `mobile/lib/wizards/widgets/store_picker.dart` — same shape for SecretStore + ClusterSecretStore. Used by ExternalSecret.
- Create: `mobile/lib/wizards/widgets/provider_picker.dart` — provider-name picker for SecretStore. Sorted as web (popular first then alphabetical).
- Modify: `mobile/lib/routing/wizard_routes.dart` — replace placeholder routes for these 11 types (Certificate, Issuer, ClusterIssuer, ExternalSecret, SecretStore, ClusterSecretStore, Policy plus the implicit completion of all 28).
- Test: `mobile/test/wizards/types/certificate/certificate_wizard_test.dart` — happy path with ClusterIssuer ref; issuer picker fetch; field-error for missing dnsNames.
- Test: `mobile/test/wizards/types/issuer/issuer_wizard_test.dart` — SelfSigned happy path; ACME LetsEncrypt-Staging happy path; HTTP01 solver; scope variant routes to different registered types.
- Test: `mobile/test/wizards/types/external_secret/external_secret_wizard_test.dart` — happy path; data and dataFrom variants; refreshInterval parsing.
- Test: `mobile/test/wizards/types/secret_store/secret_store_wizard_test.dart` — Vault provider happy path; AWS provider happy path; provider switch resets per-provider form state cleanly.
- Test: `mobile/test/wizards/types/secret_store/providers/vault_provider_form_test.dart` — auth method switches show correct fields.
- Test: `mobile/test/wizards/types/secret_store/providers/aws_provider_form_test.dart` — IRSA vs accessKey auth switch.
- Test: `mobile/test/wizards/types/policy/policy_wizard_test.dart` — engine auto-detect; template selection; parameter form renders per template.
- Test: `mobile/test/wizards/widgets/issuer_picker_test.dart` — combined list; icon distinguishes scope.
- Test: `mobile/test/wizards/widgets/provider_picker_test.dart` — sort order; selection.
- Modify: `CLAUDE.md` — append a "Build Progress" line noting M3 PR-3e shipped, M3 complete.

**Approach:**
- Land Certificate first. It's the smallest of the cert-manager triplet and exercises the issuer picker, which Issuer/ClusterIssuer don't need.
- Issuer (3 steps with type-conditional Configure step) lands second. ClusterIssuer is the same component with a `scope` prop — no separate codebase, just a route variant. Mirrors web's `IssuerWizard.tsx`.
- Land ExternalSecret third. Reuses the named-resource picker pattern from PR-3c. Smallest of the ESO trio.
- Land SecretStore fourth. This is the most complex single screen — provider picker plus 11 per-provider forms. Sub-agent swarming: dispatch parallel agents (one per 2–3 providers) for the per-provider form ports after the shared infrastructure (provider picker, store form scaffold) lands.
- Each provider form is its own file in `providers/` so the diffs are bounded. The shared form scaffold owns the form-state contract; each provider form is a `Widget` that takes a provider-specific typed record and renders the inputs.
- ClusterSecretStore reuses SecretStore's component with `scope: WizardScope.cluster`. The only diff is no namespace field on the Identity step, and the registered type becomes `cluster-secret-store`.
- Policy lands last. Engine auto-detect runs once at wizard open via a `FutureProvider` calling `/v1/policies/status`. If neither Kyverno nor Gatekeeper is installed, the wizard renders an `EmptyState` ("No policy engine installed — install Kyverno or Gatekeeper first") and a Cancel button. If both are installed, the template picker step shows templates from both engines; the operator picks one and the rest of the wizard adapts to that engine's CRD shape.
- Per-template param schemas: PR-3e ports the existing template registry from `frontend/lib/policy-templates.ts` (or wherever the canonical list lives) into a Dart equivalent at `mobile/lib/wizards/types/policy/policy_templates.dart`. Each template has `engine`, `name`, `description`, `paramSchema: List<PolicyParamSpec>`. PolicyConfigure step renders the param form generically by walking the schema.

**Patterns to follow:**
- `frontend/islands/CertificateWizard.tsx` — issuer picker fetch.
- `frontend/islands/IssuerWizard.tsx` — scope-variant pattern; type-conditional step body.
- `frontend/islands/ExternalSecretWizard.tsx` — store picker + data/dataFrom.
- `frontend/islands/SecretStoreWizard.tsx` — provider picker + per-provider auth schema.
- `frontend/islands/PolicyWizard.tsx` — engine auto-detect + template picker.
- `frontend/lib/policy-templates.ts` (or equivalent) — per-template param schemas.
- M3 PR-3a + PR-3c + PR-3d infrastructure.

**Test scenarios:**
- Happy path: Certificate `web-tls` in namespace `app`, dnsNames=`[app.example.com]`, issuerRef ClusterIssuer/letsencrypt-prod, RSA 2048. Apply succeeds; cert-manager begins issuance.
- Happy path: Issuer `selfsigned` (SelfSigned type) in namespace `app`. Apply succeeds; issuer ready immediately.
- Happy path: ClusterIssuer `letsencrypt-staging` (ACME, LetsEncrypt-Staging server, email, HTTP01 solver with ingressClass `nginx`). Apply succeeds.
- Happy path: ExternalSecret `db-creds` in namespace `app`, secretStoreRef ClusterSecretStore/vault-shared, refreshInterval `1h`, data: `[{secretKey: password, remoteRef: {key: kv/db, property: password}}]`. Apply succeeds.
- Happy path: SecretStore `vault-app` (Vault provider, server `https://vault.internal:8200`, path `kv/`, version `v2`, kubernetes auth, serviceAccountRef `default`). Apply succeeds.
- Happy path: ClusterSecretStore `aws-shared` (AWS provider, region `us-west-2`, service `SecretsManager`, IRSA auth via service account annotation). Apply succeeds.
- Happy path: Policy from Kyverno template `disallow-host-network`. Picker shows Kyverno templates; operator picks one; Configure step shows template description; Review shows ClusterPolicy YAML; apply succeeds.
- Edge case: Policy with neither engine installed. Engine auto-detect returns `{engines: []}`. Wizard renders EmptyState + Cancel; no apply path.
- Edge case: Policy with only Gatekeeper installed. Template picker shows only Gatekeeper templates; output is ConstraintTemplate.
- Edge case: SecretStore provider switch resets per-provider form. Operator fills Vault form, switches to AWS — Vault form state is discarded (per-provider FamilyNotifier). No stale Vault fields leak into AWS YAML.
- Edge case: Certificate's issuer picker on a namespace with no namespaced Issuers. Picker shows ClusterIssuers only. Apply succeeds for ClusterIssuer ref.
- Edge case: Issuer ACME with invalid email. Server returns 422 `field: spec.acme.email`. Routes back to Configure; inline message under email.
- Error path: ExternalSecret refresh interval `0s`. Server returns 422 (must be > 0). Routes back to Configure.
- Error path: SecretStore Vault auth via a serviceAccountRef that doesn't exist. Apply succeeds (server doesn't validate cross-references), but store status will eventually show `Failed`. Wizard's job ends at SSA; operator monitors the store's detail per M2.
- Integration: scope-variant Issuer apply via `/wizards/cluster-issuer/new` route. Apply hits `:type=cluster-issuer` preview endpoint; YAML shows `kind: ClusterIssuer`.
- Integration: ExternalSecret + Vault demonstrable per R8 PR-3e end (also Certificate + ClusterIssuer).
- Integration: full M3 demonstrable end-to-end. Operator opens drawer's Create submenu, sees all 28 wizards (RBAC-gated), and successfully creates one of each common kind without leaving the phone.

**Verification:**
- `cd mobile && flutter analyze` clean.
- `cd mobile && flutter test` clean.
- `make check-themes` passes.
- Smoke against homelab (assumes cert-manager + ESO + Kyverno installed): create a SelfSigned Issuer, then a Certificate signed by it. Verify cert-manager mints the secret. Create a SecretStore (Vault), then an ExternalSecret pulling a key. Verify the ExternalSecret syncs and creates the target Secret. Create a Kyverno policy from a template. Verify with `kubectl get clusterpolicy`.
- Smoke against homelab: full wizard count check. Open drawer → Create submenu shows entries for every kind the operator's RBAC permits.

---

## System-Wide Impact

- **Interaction graph:** New "Create" submenu in the drawer (RBAC-gated). Each wizard route group lives under `/clusters/:clusterId/wizards/:type/new`. No changes to PR-1c's cluster picker, PR-1d/1e's resource list, PR-1f's notification feed, PR-1g's CI pipeline, M2's action sheet or YAML editor. Wizards integrate with M2's `yaml_apply_controller` for apply, sharing the audit/RBAC/conflict UX.
- **Error propagation:** Field-level 422 responses route back to the offending step; preview/apply 5xx surfaces via `ErrorState` widget; cluster-mismatch surfaces via `ConfirmSheet`. The master plan's "fail loud" commitment carries through. No silent failures.
- **State lifecycle risks:** Wizard state is ephemeral (R6); no persistence across app restarts. Mid-wizard app crash discards the draft — acceptable given M1's no-offline-cache commitment. SSA conflicts on apply (concurrent web/CLI apply) reconcile per k8s; the loser sees a conflict error and can retry.
- **API surface parity:** Web frontend, CLI, mobile all hit the same `/v1/wizards/:type/preview` and `/v1/yaml/apply`. Audit log identifies the actor via impersonation. Adding mobile as a third caller doesn't introduce a new write path.
- **Integration coverage:** Tests cover request/response shapes for every wizard, RBAC nav-gating, field-error routing, cluster-mismatch, multi-resource apply (NamespaceLimits), and engine auto-detect (Policy). Cross-cluster behavior already works via PR-1c's `X-Cluster-ID` interceptor.
- **Unchanged invariants:** PR-1b's auth interceptor stack, PR-1c's cluster context + cluster-pinning discipline (M3 inherits and extends), PR-1d/1e's read-side resource fetching, PR-1f's WebSocket log tail, the theme generator pipeline, M2's action infrastructure and YAML apply controller. M3 adds creation; nothing alters existing reads or writes.
- **Web/Dart isomorphism extends.** M2 established the pattern with `action-handlers.ts` ↔ `resource_actions.dart`; M3 extends it to wizards via the implicit web wizard catalogue ↔ `wizard_registry.dart`. Future wizard additions update both sides in lockstep.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Wizard registry drifts between web (`frontend/islands/*Wizard.tsx`) and mobile (`wizard_registry.dart`) over time. Web adds a new wizard kind, mobile silently lags. | Document the registry as the canonical mobile catalogue; in PR descriptions for any future wizard add/remove (web or mobile) reference both files. Optional CI check that the registered types in `wizard_registry.dart` match the names of `*Wizard.tsx` components — deferred unless drift actually happens. |
| `code_text_field` fails to render very large preview YAML (Deployment with many env vars, large NamespaceLimits) cleanly. | Read-only mode is much less stressful than M2's edit mode; `code_text_field` on read-only with YAML grammar handles thousands of lines. If a real wizard produces too-large preview, fall back to plain `SelectableText` with monospace styling. State machine and apply pipeline don't depend on the editor widget. |
| Server-side validation for Policy templates differs from web frontend's expectations (template registry on server changed without mobile knowing). | Mobile fetches the engine + template list at wizard-open time via `/v1/policies/status` and the policy template registry endpoint. Mobile renders whatever the server returns. If a template's param schema changes, mobile renders the new schema generically; no hardcoded per-template UI assumptions. |
| SecretStore provider auth schemas drift over time (web adds a new provider or new auth method). | Per-provider forms are ports of `frontend/islands/SecretStoreWizard.tsx`'s subforms; PR-3e takes a snapshot. Drift is detected at smoke-test time when an operator tries a provider that mobile doesn't have or that mobile's schema is out of date. M5 polish revisits if drift is painful. The wizard is best-effort UX; backend validates and rejects bad input. |
| Issuer/ClusterIssuer scope variant introduces routing bugs (operator on `/wizards/issuer/new` accidentally creates a ClusterIssuer or vice versa). | Both routes share a component with an explicit `scope: WizardScope` prop. The component uses `scope` to: (1) hide/show the namespace field on Identity step, (2) compute the registered type for preview (`issuer` vs `cluster-issuer`), (3) compute the API path for the picker fetch. Tests cover both scope variants per PR-3e to lock the discrimination. |
| Operator on a flaky train connection times out mid-preview. | Preview timeout is the dio default (30s); on timeout, controller stays at `previewState: idle` and the operator can re-tap Next. No data loss — form state is preserved. Failed preview != failed apply; nothing has hit the cluster. |
| Policy engine auto-detect call (`GET /v1/policies/status`) fails or is slow on wizard open. | Wizard renders `LoadingState` for up to 5 seconds, then renders an EmptyState ("Policy engine status unavailable — open k8scenter on a desktop") with a Retry button. No fallback to a hard-coded engine list — the registry is server-driven. |
| `named_resource_picker` for HPA target / RoleBinding role / Certificate issuer fetches a list that's too long for a phone (e.g., 200 deployments). | Picker is a `ListView.builder` with a `SearchField` at the top. Filters client-side. If real-world lists exceed ~500 entries, M5 polish adds server-side filtering — meanwhile the picker is functional even on slow phones (lazy list rendering). |
| Field-error router is incomplete (a server validation error has no mapping to a step index → operator sees no inline message). | Per-wizard `errorRouter` is exhaustively tested via the per-wizard test suites (each test exercises at least one validation error case). Unmapped errors surface as a generic `ErrorState` banner on the current step with the raw message. Operator can still address the issue from the message text. |
| Sub-agent swarming on PR-3b / PR-3e produces diverging code styles between wizards (Riverpod patterns, file naming, error router shapes). | Each PR's first commit lands shared infrastructure that constrains the per-wizard pattern. Sub-agents follow PR-3a's Reference wizard (ConfigMap) as the structural template. Each agent's output is ≤2 wizards; reviewer reads each side-by-side and rejects drift in `/ce:review`. |
| RBAC summary in `AuthState` becomes stale (operator's permissions change mid-session); a wizard nav entry shows but apply gets 403. | Mirrors web behavior — client-side gating is best-effort; backend is final. Operator sees the 403 message verbatim in the apply result panel and re-authenticates if needed. M5 polish may add periodic `/v1/auth/me` refresh; M3 doesn't need it. |

---

## Documentation / Operational Notes

- `CLAUDE.md` "Build Progress" appended after each PR-3a / PR-3b / PR-3c / PR-3d / PR-3e merge.
- `mobile/README.md` gets a "Wizards" section explaining the drawer's Create submenu, the per-wizard step model, the cluster-pinning discipline carried over from M2, and the field-error inline-routing UX. Not a per-wizard manual — operators learn each wizard by using it (web parity).
- No backend operational changes. Backend wizard preview + YAML apply are stable; mobile writes show up identically to web writes (different `userAgent` header — operators can filter by it for mobile-originated changes).
- No Helm chart changes. Backend endpoints already exist; FCM/Universal-Link infrastructure from PR-1g is unchanged.
- `plans/mobile-app.md` "What lands in PR-1+" section gets a one-line append noting M3 complete after PR-3e merges.

---

## Sources & References

- **Origin document:** [plans/mobile-app.md](mobile-app.md) — master plan; M3 scope is the "M3 (5–7 wk)" line.
- **Sibling plans:**
  - [plans/mobile-app-m1-pr-sequence.md](mobile-app-m1-pr-sequence.md) — M1 PR sequence; foundation (auth, cluster context, resource list/detail).
  - [plans/mobile-app-m2-pr-sequence.md](mobile-app-m2-pr-sequence.md) — M2 PR sequence; write actions + YAML editor; cluster-pinning discipline; `yaml_apply_controller.dart`.
- Related code:
  - `frontend/components/wizard/WizardStepper.tsx` (port target for `WizardStepperMobile`)
  - `frontend/islands/*Wizard.tsx` (28 wizard components — one-to-one port targets per type)
  - `frontend/lib/policy-templates.ts` (per-template Policy param schemas — port for `mobile/lib/wizards/types/policy/policy_templates.dart`)
  - `backend/internal/wizard/handler.go:HandlePreview` (server contract)
  - `backend/internal/server/routes.go` (wizard route registration; all 28 types)
  - `mobile/lib/api/yaml_apply_controller.dart` (M2; consumed unchanged for apply)
  - `mobile/lib/api/resource_actions.dart` (M2; isomorphism precedent for `wizard_registry.dart`)
- Related PRs/issues:
  - M1 series (PR-1a … PR-1g) and M2 series (PR-2a, PR-2b) — provide the foundation M3 builds on.
- External docs:
  - Flutter `code_text_field` 0.7.x — https://pub.dev/packages/code_text_field (read-only YAML preview)
  - `flutter_riverpod` 2.x `AutoDisposeFamilyNotifier` — https://riverpod.dev/docs/providers/notifier_provider
  - `go_router_builder` typed routes — https://pub.dev/packages/go_router_builder
  - Kubernetes Server-Side Apply — https://kubernetes.io/docs/reference/using-api/server-side-apply/
  - cert-manager Issuer/Certificate spec — https://cert-manager.io/docs/configuration/
  - External Secrets Operator providers — https://external-secrets.io/main/provider/
  - Velero Backup/Restore/Schedule — https://velero.io/docs/main/api-types/
