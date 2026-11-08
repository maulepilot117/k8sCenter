# UI walkthrough findings and fix plan (2026-11-05)

Handoff document for a fresh session. Everything below was observed on the deployed
homelab build and root-caused against the source at `main` = `a0e925c4`.
Nothing here has been fixed or committed.

## How the test was run

- Target: `http://k8scenter.stwlfrm.monster:8000`, images `sha-a0e925c` (frontend + backend), local cluster `stwlfrm-k3s` (3× arm64 RPi).
- Browser: Claude in Chrome, signed in as `admin`.
- Coverage: every top-level route under `frontend/src/pages` (~190), one detail page of every core kind, the top-bar dropdowns, and write paths.
- Write tests used only a throwaway namespace `k8sc-uitest` (deployment, configmap, secret with a dummy value, service). It has been deleted. No pre-existing resource was modified.
- Not clicked on purpose (would write to live objects): certificate Renew/Re-issue, Argo Sync/Suspend/Refresh, alerting/settings saves, user/cluster management.
- Static link check: a script matched every literal internal `href`/`to`/`path` in `src`, `components` and `lib` against the route table. Its hits are in section C.

### Verification commands (CLAUDE.md rule 4, run repo-wide before any push)

```bash
cd frontend && bun run check && bun test && bun run build
cd backend && go vet ./... && go test ./...
```

Per CLAUDE.md: phase the work (≤5 files per phase), run GitNexus `impact` before editing each symbol, `detect_changes` before each commit, feature branches only.

---

## A. Feature-breaking bugs (fix first)

### A1. Cluster-scoped detail pages never load (Node, Namespace, PV, StorageClass, ClusterRole, …)
- **Symptom:** the page shows only the title and tab strip with an endless spinner. Console: `TypeError: Cannot convert undefined or null to object` in `ResourceDetail`.
- **Root cause:** the frontend fetches `GET /v1/resources/{kind}/{name}` for cluster-scoped kinds (`frontend/src/islands/ResourceDetail.tsx:387-388`). In `backend/internal/server/routes.go:996`, `/resources/{kind}/{namespace}` is bound to `HandleListResource`, which zeroes `ns` for cluster-scoped adapters and returns the **whole list**. Verified live: `GET /api/v1/resources/namespaces/adguard` → 200, `data` is an array of 24. `extractNsName` (`backend/internal/k8s/resources/crud.go:414`) shows the 2-segment GET was meant to reach `HandleGetResource`. Mobile builds the same URL (`mobile/lib/api/resource_actions.dart` `_resourceBase`), so mobile cluster-scoped detail is likely broken too.
- **Fix:** in `HandleListResource`, when `adapter.ClusterScoped()` and the `{namespace}` URL param is non-empty, delegate to `HandleGetResource`. Alternatively add a dispatcher handler on line 996. Add a backend test: a cluster-scoped kind with a name segment returns a single object.
- **GitNexus:** `HandleGetResource` was rated CRITICAL last session (283 dependents). Run `impact` on `HandleListResource` too.
- **Verify:** `/cluster/nodes/stwlfrm-rpi-01`, `/cluster/namespaces/adguard`, `/cluster/pvs/<pv>`, `/cluster/storageclasses/local-path`, `/rbac/clusterroles/admin`.

### A2. Resource-detail YAML editor hangs the page on keystrokes
- **Symptom:** YAML tab → **Edit YAML** → typing hangs the renderer completely, and the tab never recovers. Reproduced 4 times: twice on Enter, twice on a normal character. Once, a single `x` and a Backspace went through before the next key hung. A `localStorage` heartbeat probe written every 100 ms stopped at the first keystroke, before the document changed, so this is a **synchronous** loop.
- **Caveat:** reproduced with automated (CDP) key events. xterm in the Terminal tab took the same input without trouble, so the app is the likely cause, but **confirm with a physical keyboard first**.
- **Where to look:** `frontend/src/components/ui/CodeMirrorEditor.tsx`. Its `updateListener` → `onChange` sets `yamlEditContent` in `ResourceDetail.tsx` (~line 860), and the `[value]` sync effect (lines 94-103) dispatches a full-doc replace when `value !== doc`. Check:
  1. whether `@codemirror/*` / `@lezer/*` resolve to two copies (CLAUDE.md requires them in `vite.resolve.dedupe`; a duplicate breaks `Facet` identity),
  2. whether the WebSocket resource updates keep re-rendering `ResourceDetail` while editing,
  3. `@codemirror/lang-yaml` indentation (Enter → `insertNewlineAndIndent`).
  `node_modules` was not installed in the checkout used for the audit.
- **Repro harness idea:** a happy-dom island test that mounts `ResourceDetail` in edit mode and dispatches several transactions, with a time budget assertion.

### A3. Every wizard keeps "unsaved changes" after a successful apply
- **Symptom:** after "Applied successfully", clicking **View Resource** (or any navigation) triggers the `beforeunload` "Leave site?" prompt. Under automation the extension swallows the prompt and the page just doesn't navigate.
- **Root cause:** `frontend/lib/hooks/use-dirty-guard.ts` blocks unload while `dirty.value` is true. Each wizard sets `dirty.value = true` on edit (for example `src/islands/DeploymentWizard.tsx:80-91`) and **none of the 29 wizards resets it** after apply.
- **Fix:** have the shared apply path clear dirty on success (e.g. `WizardReviewStep` calls an `onApplied` callback, or the guard takes an `applied` signal). Do it once in the shared shell rather than in 29 files if possible. Users of the hook: CiliumPolicyEditor, ConfigMap, CronJob, DaemonSet, Deployment, ExternalSecret, HPA, Ingress, Job, NamespaceLimits, NetworkPolicy, PDB, Policy, RoleBinding, SecretStore, Secret, Service, StatefulSet wizards, plus SettingsPage and ResourceDetail.

### A4. Wizard review step: Apply hidden, footer button says "Close"
- **Symptom (Deployment, ConfigMap, Secret, Service):** on the Review step the real **Apply** button sits below a fixed 400px Monaco editor inside the scrolling pane, partly hidden behind the footer. On the Service wizard it was missing from the accessibility tree entirely. The footer's prominent primary button reads **"Close"** and discards the wizard. The review editor's last lines (ports, resources) were also clipped, although the preview API returned them.
- **Code:** `src/components/wizard/WizardReviewStep.tsx:212-234` (editor `height="400px"`, Apply in the body), `src/islands/WizardShell.tsx:176-185` (scroll pane) and `:286-306` (footer button renders `nextLabel`), `src/islands/DeploymentWizard.tsx:269` (`nextLabel={currentStep.value < 3 ? "Continue" : "Close"}`, same pattern in the other wizards).
- **Fix:** on the last step the footer primary action should be **Apply** (lift `handleApply` into the shell, or let the step register a primary action). After a successful apply it can become "Done / View resource". Size the editor to the available height (flex) instead of 400px.

### A5. Top-bar dropdowns are clipped; the cluster switcher hides the whole bar
- **Symptom:** opening the cluster switcher shifts every top-bar control up by about 33px (cluster, namespace, search and the right-side theme/bell/avatar buttons are cut off), and the list is clipped to the bar's height. The notification panel shows 9px of 155px, and the avatar menu is clipped the same way.
- **Root cause:** `src/islands/TopBarV2.tsx:68` sets `overflow: "hidden"` on the `<header class="glass-bar">` (clientHeight 55, scrollHeight 88). `src/islands/ClusterSwitcher.tsx:205` runs `listRef.current?.focus()` when the list opens, so the browser scrolls the clipped header to reveal the focused `<ul>` (header `scrollTop` = 33).
- **Fix:** remove `overflow: hidden` from the header (keep `minWidth: 0`; clip only the inner left group if truncation is needed) and use `focus({ preventScroll: true })`. Check all three dropdowns afterwards.

### A6. ExternalSecrets always report "Drifted"
- **Root cause:** `backend/internal/externalsecrets/handler.go:905` `computeDriftStatus(es.SyncedResourceVersion, secret.ResourceVersion)`. ESO's `status.syncedResourceVersion` is `<generation>-<hash of the ES>` (live value `1-84972dd6…`), not the Secret's `metadata.resourceVersion` (`35498635`), so they never match.
- **Fix:** compare something ESO actually maintains. Option one: the Secret annotation `reconcile.external-secrets.io/data-hash` against a hash of the live Secret data, using the same algorithm ESO uses (check the ESO version in the cluster). Option two: record the Secret `resourceVersion` observed right after each successful sync and compare against that. Update the unit tests and the `RecordDrift` and alerting consumers. The drift UI is in `frontend/components/eso/ESODriftIndicator.tsx`.

### A7. Log Explorer finds nothing with "All Namespaces"
- **Root cause:** `src/islands/LogFilterBar.tsx:35-40` copies `selectedNamespace.value` into the local namespace signal. The global sentinel for "all" is the string `"all"` (`src/lib/namespace.ts:17`), so the query becomes `{namespace="all"}` (seen in network requests), and the select renders blank because no option has value `"all"`.
- **Fix:** map `"all"` to `""` when syncing. Audit other islands that copy `selectedNamespace` directly.

### A8. Notification Channels page crashes; Rules says "Failed to load rules"
- **Root cause:** `src/islands/NotificationChannels.tsx:69` and `src/islands/NotificationRules.tsx:45,55` read `res.data.data`. `notifApi.listChannels/listRules` (`frontend/lib/api.ts:438-451`) wrap `apiGet`, which already returns the `{data}` envelope, so `res.data` is the array (or `null` when empty). With `null` it throws; with real rows it shows nothing. The generic types `apiGet<{ data: NotifChannel[] }>` are also wrong.
- **Fix:** use `res.data ?? []` and type the calls as `apiGet<NotifChannel[]>`. Check the create/test response handling in the same file.

### A9. `/scaling` and `/scaling/pdbs`: "API error 404: unknown resource kind"
- **Root cause:** the table kind must be the adapter `Kind()` (`hpas`, `pdbs`), while count keys are API resource names (`horizontalpodautoscalers`, `poddisruptionbudgets`).
  - `src/pages/scaling/index.astro` uses `kind="horizontalpodautoscalers"`, which should be `hpas` (or the page should render `HPAsDashboard`).
  - `src/islands/PDBsDashboard.tsx:107` uses `kind="poddisruptionbudgets"`, which should be `pdbs`. Line 28's count key is correct.
  - `src/islands/HPAsDashboard.tsx:28` uses count key `"hpas"`, which should be `"horizontalpodautoscalers"`.
- **Verify:** the cluster has 1 PDB (`k8scenter/k8scenter-postgresql`), and the list currently shows 0.

### A10. Topology: "Fit to view" does nothing; active overlay toggle invisible
- `src/islands/NamespaceTopology.tsx:456-460` `fitToView` only resets zoom and pan to 0, but the rendered graph group sits at `translate(40, -1048)`, about 1000px above the viewport. Fit should compute the node bounding box and set pan and zoom from it (and the initial view should do the same).
- The active overlay radio uses `bg-accent-primary` (line 608; also line 526), which doesn't exist (see D1), so the selected "Base" button is transparent with near-black text.

### A11. Violations page applies a hidden global namespace filter
- With the global namespace set to `k8scenter`, `/security/violations` shows "0 of 53 violations" while its own namespace select reads "All Namespaces". With the global set to all, it shows 53 of 53. Either sync the local select with the global value, or don't apply the global filter.

### A12. Gateway API HTTPRoute parent status and link
- `backend/internal/gateway/types.go:117` `ParentRef.Status` is never populated: `normalize.go:67-95` `extractParentRefs` doesn't read `status.parents`, and `resolveRelationships` (`handler.go:912`) only fills `GatewayConditions`. So the Status column always shows "-".
- `frontend/components/gateway/ParentGatewaysTable.tsx` builds `/networking/gateway-api/gateways/${ref.namespace}/${ref.name}`. When `parentRef.namespace` is omitted (it defaults to the route's namespace), that becomes `//argocd`, and the Namespace column shows "-".
- **Fix:** default the namespace to the route's namespace in the backend, and derive `status` from the matching `status.parents[].conditions` (Accepted).

### A13. Missing web actions
- **No Secret Reveal in the web UI.** The backend has `GET /resources/secrets/{ns}/{name}/reveal/{key}` (`routes.go:1023`) and mobile has a Reveal UI (`mobile/lib/features/resources/secret_screens.dart`). The web Secret detail shows `****` with no control. Add a per-key reveal (audited server-side already).
- **No Delete** for ConfigMaps, Secrets, Services, ReplicaSets, Endpoints, NetworkPolicies, Namespaces (detail pages have no Delete; ConfigMap/Namespace list rows have no ⋮ menu). Pods and Deployments have both. Find where the per-kind action list is defined (`frontend/lib/action-handlers.ts` is the web side of the mobile action map; keep the two isomorphic per CLAUDE.md).

---

## B. Wrong data

| # | Symptom | Root cause / location | Fix |
|---|---|---|---|
| B1 | PVCs and PVs list headers say "0 …" (actual 16 / 18) | `src/islands/StorageDashboard.tsx:43,53` `resolveTab` passes `kind: "pvcs"` / `"pvs"` to `formatCount`; counts keys are `persistentvolumeclaims` / `persistentvolumes` | Use the full names (the nav in `lib/constants.ts` already does) |
| B2 | Secrets header says "0 secrets" | The counts API returns no `secrets` key; `getCount` (`src/lib/resource-counts.ts:48-52`) returns `?? 0` for missing keys | Return `null` for a missing key and render the table count, or count secrets server-side if RBAC allows |
| B3 | Settings → Clusters: Local Cluster shows 0 nodes, version "—" | `backend/internal/k8s/cluster_prober.go:131` skips `IsLocal` clusters, so `UpdateStatus` never runs for local | Probe local too, or fill it from the informer and discovery at read time |
| B4 | ClusterRoles subtitle "100 shown (100 of 142 loaded)" | wording: only 100 are loaded | "100 of 142 shown" |
| B5 | Pod `#logs` / `#terminal` deep link opens Overview (`#yaml` works) | Pod's Logs/Terminal tabs are added only when `IS_BROWSER`, after the hash is resolved (known open item from the #688 session) | Always declare the tabs and resolve the hash after hydration |
| B6 | Logs "Previous" on a container that never restarted shows "API error 400: kubernetes API error" above the current logs | raw API error passed through | Disable Previous when `restartCount == 0`, or map to "No previous container instance" |
| B7 | Investigate doesn't prefill the namespace from the global selection; blast radius lists scaled-to-zero old ReplicaSets as "directly affected" | `ObservabilityInvestigate` island | Prefill when global ≠ all; skip `replicas == 0` RS |

## C. Broken links (all 404)

| Link | Source | Correct target |
|---|---|---|
| Certificate detail → Secret | `src/islands/CertificateDetail.tsx:223` `/workloads/secrets/${ns}/${name}` | `/config/secrets/${ns}/${name}` |
| Velero backup rows | `components/velero/BackupsResourceTable.tsx:49`, `src/islands/VeleroDashboard.tsx:737` `/backup/backups/${ns}/${name}` | No detail route exists (`src/pages/backup/backups/` has only `new.astro`): add detail pages or drop the links |
| Velero restore rows | `components/velero/RestoresResourceTable.tsx:39` | same |
| Velero schedule rows | `components/velero/SchedulesResourceTable.tsx:58` | same |
| PromQL admin gate button | `src/islands/PromQLAdminGate.tsx:68` `/cluster/dashboard` | `/` (Overview), or remove |

Consider turning the link check into a `bun run check` guard. The scratch script matched every literal internal href against `src/pages` route patterns; it can live next to the other `server/check-*.ts` guards.

## D. Rendering and theme

### D1. About 50 components use colour utilities whose theme tokens don't exist
Probed the built CSS on the live page: these resolve to nothing (transparent background or default text colour). File counts are for non-test `.tsx`:

| Utility | Files |
|---|---|
| `bg-bg-elevated` | 21 |
| `border-border-emphasis` | 9 |
| `bg-bg-surface` | 7 |
| `text-on-accent` | 4 |
| `border-border` (bare) | 4 |
| `bg-status-error` (incl. `/5`) | 3 |
| `bg-accent-primary`, `text-accent-primary`, `border-accent-primary`, `bg-accent-primary/50` | 5 |
| `text-status-error`, `text-status-info`, `text-status-warning` | 5 |
| `bg-bg-base`, `text-bg-base` | 3 |
| `bg-surface-hover`, `bg-status-warning`, `bg-status-success`, `bg-danger` | 1 each |

Defined tokens live in `frontend/assets/styles.css` (`--color-*`, e.g. `--color-surface`, `--color-accent`, `--color-danger`). Most likely leftovers from the Liquid Glass token rename (`plans/liquid-glass-redesign.md`). Fix: map each to an existing token, then add a guard that fails the build when a `bg-|text-|border-` colour utility has no matching `--color-*` token. The heaviest users are the Log* islands, NamespaceTopology, PromQLAdminGate and the incident/limits islands; list them with:

```bash
cd frontend && grep -rlE "bg-bg-elevated|bg-bg-surface|border-border-emphasis|accent-primary|status-(error|info|warning|success)|text-on-accent|text-bg-base|bg-surface-hover" src components lib --include=*.tsx | grep -v _test
```

### D2. Smaller rendering issues
- Log rows have no gap between the timestamp, level and pod columns (`src/islands/LogResults.tsx`, row `flex` without `gap`).
- The floating "+" quick-action button overlaps the log viewer's "Resume auto-scroll" pill (pod Logs tab).
- Service wizard protocol select is too narrow and shows "TC" (`components/wizard/ServicePortsStep.tsx`).
- Gateway API dashboard has no page heading, and the gRPC card is misaligned because the "healthy" line is omitted when 0 (`src/islands/GatewayAPIDashboard.tsx:448`). Gateway detail "Age" is a raw ISO timestamp.
- The wizard dialog changes height between steps, so the footer jumps.
- Prometheus range queries show only "201 samples" per series, with no chart.

## E. Copy and UX

- Developer text shown to users: `src/islands/ESODashboard.tsx:424` "Phase D resolves stale thresholds — count is 0 until that ships." and `components/eso/ESODriftIndicator.tsx:57,62` "Force-sync ships in Phase E" / "Force-sync action available in Phase E." (the Force sync button already works).
- `src/islands/PodTerminal.tsx:445`: `click"Open Terminal"` is missing a space.
- Issuers page says "Issuers will appear here once cert-manager is installed" while cert-manager is installed (the cluster uses an external `StepClusterIssuer`).
- Notification source filter shows raw enum names ("External_secrets", "Certmanager", "Gitops").
- Creating a namespace redirects to the Overview dashboard instead of the namespace list or detail.
- CRD instance page (`/extensions/<group>/<res>/<ns>/<name>`) opens straight into an edit form with editable Name and Namespace. Changing them would create a copy, not rename. Make those fields read-only for an existing object, or add a read view.
- Secret wizard review step shows the value in plaintext as `stringData` (masked while typing). Decide whether that's intended.
- A "Metrics" tab appears on kinds with no metrics (NetworkPolicy, Endpoints, EndpointSlice, ConfigMap, Secret).
- Argo app Resources tab: the Health column is "-" for every row; PVC and ExternalSecret rows aren't linked while Services are.

## F. Console, network and backend hygiene

- `src/lib/ws.ts:90-98` `onopen` dereferences module-level `ws!`, which `onclose`/disconnect can null (or replace) before `open` fires. Seen as `TypeError: Cannot read properties of null (reading 'send')`. Fix: capture the socket in a local `const sock = new WebSocket(...)` and use `sock` in its handlers; ignore events from a superseded socket. Backend logs show many `websocket auth read failed: close 1000`, consistent with this.
- Every page load fires about 17 API calls that 401 before the token refresh, then retries. Consider gating the first fetches on the refresh promise.
- The audit log is flooded with `refresh` entries (one or more per page navigation). Consider not auditing successful refreshes, or collapsing them.
- Backend log lines seen during the run, worth a look:
  - `failed to get cluster-scoped rules: SelfSubjectRulesReview for namespace "": no namespace on request`, a SSRR sent with an empty namespace.
  - `list pushsecrets failed: the server could not find the requested resource`, a poller listing a CRD that isn't served; should be gated on discovery.
  - `grafana provisioning token not set — dashboard provisioning disabled` (config, informational).
- Accessibility: secondary-nav links and several icon buttons have no accessible name in the a11y tree (`SecondaryNav`, top-bar icon buttons, wizard step-rail buttons).

## G. Verified working (no action needed)

- **Read-only pages:** every top-level route rendered without a non-401 HTTP error except those listed above.
- **List tables:** search, sorting, "Load more" pagination (100 per page).
- **Queries and logs:** PromQL instant and range, Loki search with a real namespace, Live Tail start and stop.
- **Navigation and detail views:** Investigate diagnostics, Gateway list → detail → HTTPRoute drill-in, GitOps app detail (Overview/Resources/History), certificate detail, ESO list/detail, Extensions CRD list.
- **Create and modify:** the namespace, Deployment, ConfigMap, Secret and Service wizards produce the right objects (checked with kubectl); Scale and Restart (with live header update).
- **Pod tabs:** Logs (follow, snapshot, search) and Terminal (exec, input, close).
- **Delete:** type-to-confirm delete for Pod and Deployment, with cascade and the "was deleted" banner.
- **Secret masking:** values show as `****` on Secret detail.

## Suggested phase order (≤5 files each, verify between phases)

1. **A1** backend cluster-scoped GET (+ test). Unblocks all cluster-scoped detail pages.
2. **A5** top bar (TopBarV2, ClusterSwitcher) and **F** ws.ts onopen.
3. **A3 + A4** wizard shell: Apply in the footer, flexible editor height, clear dirty on success. Shared files: WizardShell, WizardReviewStep, use-dirty-guard, plus the `nextLabel` pattern in the wizards (spread the per-wizard edits over sub-agents per CLAUDE.md rule 5).
4. **A2** YAML editor hang. Confirm with a real keyboard first, then diagnose (dedupe / lang-yaml / re-render loop).
5. **A7, A8, A9, B1, B2**: small frontend data fixes.
6. **A6** ESO drift (backend + tests).
7. **A10, A11, A12, B3**: topology fit, violations filter, gateway parent status, local cluster prober.
8. **C**: broken links (+ link-check guard).
9. **D1**: theme-token sweep (+ guard), then D2/E copy and polish.
10. **A13**: Secret reveal and the missing Delete actions (feature work; consider `/ce:brainstorm` for the action-parity scope).

Session log with the same findings: `F:\Obsidian\secondBrain\Sessions\2026-11-05-k8sCenter-ui-walkthrough-bug-audit.md`.
