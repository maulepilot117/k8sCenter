# Phase 4: Features & Wizards — Implementation Plan

## Overview

Phase 4 adds pod log streaming, pod exec terminal, persistent settings UI, user/RBAC management, storage snapshot lifecycle, and 10 resource creation wizards. Work is decomposed into four independent phases (4A–4D), each on its own branch.

**Spec:** `docs/superpowers/specs/2026-03-22-phase4-features-design.md`

---

## Phase 4A: Core Infrastructure

**Branch:** `feat/phase4a-core-infrastructure`

### Step 4A.1 — Pod Log Streaming (WebSocket)

**Backend: `backend/internal/server/handle_ws_logs.go`**

New WebSocket endpoint following Pattern B (direct per-connection, same as `handle_ws_flows.go`):

- Route: `WS /api/v1/ws/logs/{namespace}/{pod}/{container}`
- Auth: in-band JWT (first message), same pattern as flows
- RBAC: `get` on `pods/log` subresource via impersonated client
- Stream: `cs.CoreV1().Pods(ns).GetLogs(name, opts).Stream(ctx)` with `Follow: true`
- Query params from initial auth message: `tailLines` (default 500), `previous`, `timestamps`
- Backpressure: bounded channel (256) between log reader goroutine and WS writer, drop-with-counter when full
- Line scanning: `bufio.NewScanner` with 256KB max line buffer
- Ping: 30s heartbeat, 60s pong wait
- Connection limit: `atomic.Int64` counter, max 100 concurrent log streams
- Audit: log stream initiation logged as `ActionReadLogs`
- Message format: `{"type":"log","data":"line text","timestamp":"RFC3339"}` or `{"type":"error","message":"..."}` or `{"type":"dropped","count":N}` (drop notifications batched every 5s, not per-drop)
- Lines exceeding 256KB scanner buffer are silently truncated — document this behavior

Register in `routes.go` alongside existing WS routes (around line 22).

**Frontend: Replace `frontend/islands/LogViewer.tsx`**

Full rewrite of the existing HTTP-polling LogViewer:

- **Mode toggle**: "Follow" (WS streaming, default) and "Snapshot" (one-shot HTTP fetch, existing behavior)
- **WebSocket client**: connect to `/ws/v1/ws/logs/{ns}/{pod}/{container}`, auth via first message (reuse pattern from `lib/ws.ts`), handle `log`, `error`, `dropped` message types
- **ANSI rendering**: use `npm:ansi_up` (zero-dep, 7KB, streaming-aware). Set `use_classes = true` for CSS theming. Import in `deno.json`: `"ansi_up": "npm:ansi_up@^6"`
- **Multi-container tabs**: tab bar for all containers (init containers labeled with "(init)" suffix). Each tab gets its own WS connection. State per tab: independent log buffer, scroll position, search
- **Side-by-side split**: button to split view for 2-container pods (CSS grid 50/50)
- **Search**: client-side text search with `<input>` above log area. Match highlighting (wrap matches in `<mark>`), prev/next navigation with match count display
- **Auto-scroll**: auto-scroll to bottom when in follow mode. Pause when user scrolls up (detect via `scrollTop + clientHeight < scrollHeight`). "Resume" FAB button to snap back
- **Buffer**: keep last 10,000 lines in JS array. Drop oldest when exceeded. Display "(N lines dropped)" indicator
- **Download**: "Download" button serializes current buffer to a `.log` file via `Blob` + `URL.createObjectURL`
- **DOM performance**: batch updates via `requestAnimationFrame`. Accumulate lines for one frame, then flush to DOM as a single innerHTML append on a `<pre>` element

**Files to create/modify:**
- `backend/internal/server/handle_ws_logs.go` (new)
- `backend/internal/server/routes.go` (add WS route)
- `frontend/islands/LogViewer.tsx` (rewrite)
- `frontend/deno.json` (add `ansi_up` import)
- `frontend/routes/ws/[...path].ts` (add `/ws/logs/` to WS proxy allowlist)

### Step 4A.2 — Pod Exec Terminal (xterm.js)

**Backend: Enhance `backend/internal/k8s/resources/pods.go`**

Modify existing `HandlePodExec` (lines 165-284):

- **Connection limit**: add `atomic.Int64` counter, max 50 concurrent exec sessions (same pattern as `handle_ws_flows.go` `maxFlowConnections`). Return 503 when exceeded.
- **Auth model**: keep existing middleware-based auth (Auth + CSRF middleware on the exec route group). Do NOT move to in-band auth — middleware auth is already working and more secure (auth happens before WS upgrade).
- **Message framing protocol**: client sends JSON for control messages, raw bytes for stdin:
  - `{"type":"input","data":"base64-encoded-bytes"}` — stdin data
  - `{"type":"resize","cols":N,"rows":N}` — terminal resize
  - Server sends raw bytes (stdout/stderr) as binary WS messages
  - Server sends `{"type":"shell","name":"/bin/bash"}` as first message after exec starts
- **TerminalSizeQueue**: new `termSizeQueue` struct implementing `remotecommand.TerminalSizeQueue`:
  - `ch chan remotecommand.TerminalSize` (buffered 1)
  - `done chan struct{}` for stop signal
  - `Next()` blocks on channel, returns nil on done
  - `Send(width, height)` non-blocking send (drop if pending)
- **Shell detection**: try shells in order: `/bin/bash`, `/bin/sh`, `/bin/ash` (max 3 attempts). Use `exec.StreamWithContext` with `Command: []string{shell}`. If first attempt fails, retry with next shell. If all fail, send `{"type":"error","message":"no shell found"}` and close the connection — do not hang.
- **Read pump refactor**: separate goroutine reads WS messages, dispatches `input` to `io.PipeWriter` (stdin), dispatches `resize` to `termSizeQueue.Send()`
- **StreamOptions**: add `TerminalSizeQueue: sizeQueue` to the existing `StreamOptions`

**Frontend: `frontend/islands/PodTerminal.tsx` (new)**

- **xterm.js packages** in `deno.json`:
  ```
  "@xterm/xterm": "npm:@xterm/xterm@^5.5.0",
  "@xterm/addon-fit": "npm:@xterm/addon-fit@^0.10.0",
  "@xterm/addon-web-links": "npm:@xterm/addon-web-links@^0.11.0"
  ```
- **CSS**: add `@import "npm:@xterm/xterm/css/xterm.css";` to `frontend/assets/styles.css`
- **Dynamic import**: all xterm.js imports inside `useEffect` to avoid SSR failures (xterm accesses DOM at import time). This matches the CodeMirror precedent in the codebase.
- **Terminal init**: `new Terminal({ cursorBlink: true, fontSize: 13, fontFamily: "monospace" })`, load FitAddon + WebLinksAddon
- **Resize**: `ResizeObserver` on container div, throttled `fitAddon.fit()` (100ms). `terminal.onResize` sends `{"type":"resize","cols","rows"}` via WS
- **WebSocket**: connect to `/ws/v1/ws/exec/{ns}/{pod}/{container}`, send auth JWT as first message. On auth success, attach terminal I/O
- **Tab bar**: signal `sessions: Signal<{id, container, shell, ws, terminal}[]>`. "Open Terminal" button adds new session. Close button per tab closes WS + disposes terminal
- **Container picker**: dropdown populated from pod spec containers (exclude init by default, toggle to include)
- **Reconnect**: on WS close, show semi-transparent overlay with "Session ended. Reconnect?" button. Don't auto-reconnect.
- **Fullscreen**: toggle button sets container to `position: fixed; inset: 0; z-index: 50`. Esc key or button exits. Refit terminal on toggle.
- **Placement**: add "Terminal" tab to `ResourceDetail.tsx` for pods (alongside existing Logs tab)

**Files to create/modify:**
- `backend/internal/k8s/resources/pods.go` (modify exec handler)
- `frontend/islands/PodTerminal.tsx` (new)
- `frontend/islands/ResourceDetail.tsx` (add Terminal tab for pods)
- `frontend/deno.json` (add xterm packages)
- `frontend/assets/styles.css` (add xterm CSS import)

### Step 4A.3 — Persistent Settings UI

**Backend additions:**

- `GET /api/v1/setup/status` — public endpoint, returns `{"data":{"needsSetup": bool}}` based on `userStore.Count() == 0`. Add to routes.go in the public (no-auth) group alongside `/setup/init`.
- `GET /api/v1/settings` — **admin-only**, returns masked settings via `settingsService.Get()` + `MaskedSettings()`. Must be admin-only because even masked settings reveal SMTP hosts, Grafana URLs, etc.
- `PUT /api/v1/settings` — admin-only, accepts partial update JSON, calls `settingsService.Update()`. Route does not yet exist — must be explicitly added to routes.go.
- Add `ClusterDisplayName` and `DefaultNamespace` fields to settings table if not present (migration `000005_add_general_settings.up.sql`)

**Frontend: `frontend/islands/SettingsPage.tsx` (new)**

- Accordion sections (General, Monitoring, Alerting, Auth) using `<details>` + `<summary>` HTML elements (no JS needed for expand/collapse)
- Each section: form fields populated from `GET /api/v1/settings`, section-local dirty tracking, "Save" button that PUTs only that section's fields
- Test Connection buttons: `POST /api/v1/settings/auth/test-oidc` and `test-ldap` (existing endpoints)
- Send Test Email: `POST /api/v1/alerts/test` (existing endpoint)
- Toast on save success/error
- `beforeunload` guard when any section is dirty

**Frontend: `frontend/islands/SetupWizard.tsx` (new)**

- Route: `frontend/routes/setup.tsx`
- BFF middleware: check `GET /api/v1/setup/status` — if `needsSetup: false`, redirect to `/`
- Steps using WizardStepper (existing component):
  1. Welcome — static branding, "Get Started" button
  2. Admin Account — username + password + confirm (POST `/api/v1/setup/init` with setup token). **On success, auto-login** by immediately calling `POST /api/v1/auth/login` with the just-created credentials and storing the access token. This is required because steps 3-4 call authenticated endpoints.
  3. Monitoring — optional Prometheus/Grafana URLs (PUT `/api/v1/settings`, now authenticated). Skip button.
  4. Alerting — optional SMTP config (PUT `/api/v1/settings`). Skip button.
  5. Review — summary of what was configured
  6. Done — redirect to dashboard (already authenticated)
- **Partial completion**: if user completes step 2 but closes browser before step 5, `needsSetup` will be `false` (user exists). Steps 3-4 are non-critical — the system functions without them. User can configure monitoring/alerting later from the Settings page.
- Setup token: read from env var, passed as hidden field or prompt if not configured
- `GET /api/v1/setup/status` must only return `{needsSetup: bool}` — never leak user count, settings state, or other information

**Files to create/modify:**
- `backend/internal/server/handle_setup.go` (add `handleSetupStatus`)
- `backend/internal/server/routes.go` (add settings + setup/status routes)
- `backend/internal/store/migrations/000005_add_general_settings.up.sql` (new)
- `backend/internal/store/migrations/000005_add_general_settings.down.sql` (new)
- `backend/internal/store/settings.go` (add new fields)
- `frontend/islands/SettingsPage.tsx` (new)
- `frontend/islands/SetupWizard.tsx` (new)
- `frontend/routes/setup.tsx` (new)
- `frontend/routes/settings/general.tsx` (new or modify existing)

---

## Phase 4B: User & RBAC Management

**Branch:** `feat/phase4b-user-rbac-management`

### Step 4B.1 — User Creation Endpoint

**Backend: `backend/internal/server/handle_users.go`**

Add `handleCreateUser`:
- Route: `POST /api/v1/users` (admin-only, inside existing users group)
- Body: `{"username","password","k8sUsername","k8sGroups":[],"roles":[]}`
- Validation: username regex (DNS label), password 8-128 chars, k8sUsername defaults to username if empty
- Calls `userStore.Create(ctx, user)` (existing method)
- Audit log: `ActionCreate`, resource type "User"
- Returns created user (without password)

### Step 4B.2 — User Creation + Role Assignment Wizard

**Frontend: `frontend/islands/UserWizard.tsx` (new)**

- Route: `frontend/routes/settings/users/new.tsx`
- Linked from UserManager.tsx "Create User" button
- Steps:
  1. **Account**: username, password, confirm password. Debounced uniqueness check via `GET /api/v1/users` (check client-side if username exists in list). Validation: 8-128 chars, match confirmation.
  2. **Kubernetes Identity**: k8s username (text input, defaults to local username), k8s groups (tag-style input using KeyValueListEditor pattern, default `["system:authenticated"]`). Info tooltip explaining impersonation.
  3. **Role Assignment** (optional, skippable):
     - Toggle: "Quick assign" (ClusterRoleBinding) vs "Namespace-scoped" (RoleBinding)
     - Quick: dropdown of existing ClusterRoles fetched from `GET /api/v1/resources/clusterroles`
     - Namespace-scoped: namespace picker + Role/ClusterRole picker per row. Add multiple rows.
     - Pending assignments table with remove button per row
  4. **Review**: summary + YAML preview of RoleBinding/ClusterRoleBinding resources
- Apply flow: POST create user → POST create each binding via `/api/v1/resources/rolebindings/{ns}` or `/api/v1/resources/clusterrolebindings` (once RBAC CRUD is implemented in step 4B.3). Show per-resource success/failure.

### Step 4B.3 — Full RBAC CRUD

**Backend: Rename `rbac_viewer.go` → `rbac.go`, add CRUD**

Add create/update/delete handlers for all four types, following existing patterns from `deployments.go`:

```
HandleCreateRole(w, r)           — POST /resources/roles/{namespace}
HandleUpdateRole(w, r)           — PUT /resources/roles/{namespace}/{name}
HandleDeleteRole(w, r)           — DELETE /resources/roles/{namespace}/{name}
HandleCreateClusterRole(w, r)    — POST /resources/clusterroles
HandleUpdateClusterRole(w, r)    — PUT /resources/clusterroles/{name}
HandleDeleteClusterRole(w, r)    — DELETE /resources/clusterroles/{name}
HandleCreateRoleBinding(w, r)    — POST /resources/rolebindings/{namespace}
HandleUpdateRoleBinding(w, r)    — PUT /resources/rolebindings/{namespace}/{name}
HandleDeleteRoleBinding(w, r)    — DELETE /resources/rolebindings/{namespace}/{name}
HandleCreateClusterRoleBinding(w, r) — POST /resources/clusterrolebindings
HandleUpdateClusterRoleBinding(w, r) — PUT /resources/clusterrolebindings/{name}
HandleDeleteClusterRoleBinding(w, r) — DELETE /resources/clusterrolebindings/{name}
```

Each handler: decode JSON body → impersonated client → `cs.RbacV1().Xxx().Create/Update/Delete()` → audit log → respond.

Register in `routes.go` alongside existing RBAC GET routes.

### Step 4B.4 — RBAC Wizards

**Shared components:**

- `frontend/components/wizard/RuleBuilder.tsx` (new): RBAC rule editor
  - Each rule row: API Group multi-select, Resources multi-select, Verbs checkbox group
  - API groups and resources populated from `GET /api/v1/cluster/api-resources` (existing endpoint)
  - Verb presets: "Read Only" (get, list, watch), "Read/Write" (get, list, watch, create, update, patch, delete), "Full" (all + deletecollection)
  - Add/remove rule rows
- `frontend/components/wizard/SubjectPicker.tsx` (new): RBAC subject selector
  - Each row: Kind dropdown (User/Group/ServiceAccount), Name text input, Namespace (ServiceAccount only)
  - "Add from local users" button fetches from `GET /api/v1/users` and populates

**Frontend: `frontend/islands/RoleWizard.tsx` (new)**

- Used for both Role and ClusterRole (prop: `clusterScoped: boolean`)
- Steps: Basics (name, namespace if Role, labels) → Rules (RuleBuilder) → Review (YAML preview)
- Routes: `frontend/routes/resources/roles/new.tsx`, `frontend/routes/resources/clusterroles/new.tsx`

**Frontend: `frontend/islands/RoleBindingWizard.tsx` (new)**

- Used for both RoleBinding and ClusterRoleBinding (prop: `clusterScoped: boolean`)
- Steps: Basics (name, namespace if RoleBinding) → Role Reference (searchable dropdown, grouped by Role/ClusterRole) → Subjects (SubjectPicker) → Review
- Routes: `frontend/routes/resources/rolebindings/new.tsx`, `frontend/routes/resources/clusterrolebindings/new.tsx`

**Sidebar update:** Add "Access Control" section to `frontend/lib/constants.ts` NAV_SECTIONS with Roles, ClusterRoles, RoleBindings, ClusterRoleBindings entries. Add "Create" button links to resource browser pages.

**Files to create/modify:**
- `backend/internal/server/handle_users.go` (add create handler)
- `backend/internal/k8s/resources/rbac.go` (rename + add CRUD)
- `backend/internal/server/routes.go` (add RBAC CRUD + user create routes)
- `frontend/islands/UserWizard.tsx` (new)
- `frontend/islands/RoleWizard.tsx` (new)
- `frontend/islands/RoleBindingWizard.tsx` (new)
- `frontend/components/wizard/RuleBuilder.tsx` (new)
- `frontend/components/wizard/SubjectPicker.tsx` (new)
- `frontend/routes/settings/users/new.tsx` (new)
- `frontend/routes/resources/roles/new.tsx` (new)
- `frontend/routes/resources/clusterroles/new.tsx` (new)
- `frontend/routes/resources/rolebindings/new.tsx` (new)
- `frontend/routes/resources/clusterrolebindings/new.tsx` (new)
- `frontend/lib/constants.ts` (update NAV_SECTIONS)

---

## Phase 4C: Storage

**Branch:** `feat/phase4c-storage`

### Step 4C.1 — Snapshot CRUD Backend

**Backend: `backend/internal/storage/handler.go`**

Add to existing Handler:

- `HandleCreateSnapshot`: POST `/api/v1/storage/snapshots/{namespace}` — builds `unstructured.Unstructured` with VolumeSnapshot spec (source PVC, snapshot class name), creates via `dynClient.Resource(volumeSnapshotGVR).Namespace(ns).Create()`. Audit log.
- `HandleDeleteSnapshot`: DELETE `/api/v1/storage/snapshots/{namespace}/{name}` — delete via dynamic client. Audit log.
- `HandleListSnapshotClasses`: GET `/api/v1/storage/snapshot-classes` — list VolumeSnapshotClasses via dynamic client (for wizard dropdown)
- `HandleGetSnapshot`: GET `/api/v1/storage/snapshots/{namespace}/{name}` — get single snapshot for detail page

All handlers check CRD availability first (`checkSnapshotCRDs()`), return 404 with clear message if not installed.

Register routes in `registerStorageRoutes()`.

### Step 4C.2 — Snapshot Browser + Detail

- Add VolumeSnapshots to resource browser: new route `frontend/routes/resources/volumesnapshots.tsx`
- Add column definitions to `frontend/lib/resource-columns.ts`: name, namespace, source PVC, snapshot class, status (readyToUse), restore size, age
- Add VolumeSnapshot to `RESOURCE_DETAIL_PATHS` and `NAV_SECTIONS` in constants.ts (under Storage section)
- Add snapshot-specific action buttons: "Restore" (opens restore wizard), "Delete"

### Step 4C.3 — Snapshot Wizard (Create)

**Backend: `backend/internal/wizard/snapshot.go` (new)**

- `SnapshotInput`: name, namespace, pvcName, snapshotClassName, labels
- `Validate()`: DNS label name, PVC name required, snapshot class required
- `ToVolumeSnapshot()`: returns `unstructured.Unstructured` (not typed — CRD)
- Preview endpoint: `POST /api/v1/wizards/snapshot/preview`

**Frontend: `frontend/islands/SnapshotWizard.tsx` (new)**

- Route: `frontend/routes/resources/volumesnapshots/new.tsx`
- Steps:
  1. Source — namespace selector + PVC dropdown (fetched from `/api/v1/resources/pvcs/{ns}`), shows PVC size and storage class
  2. Snapshot Class — dropdown of VolumeSnapshotClasses (fetched from `/api/v1/storage/snapshot-classes`), filtered to classes matching PVC's CSI driver
  3. Options — name (auto-generated: `{pvc}-snap-{YYYYMMDD-HHmmss}`), labels
  4. Review — YAML preview via preview endpoint

### Step 4C.4 — Restore Wizard

**Frontend: `frontend/islands/RestoreSnapshotWizard.tsx` (new)**

- Launched from snapshot detail page "Restore" action
- Route: `frontend/routes/resources/volumesnapshots/restore.tsx?ns={ns}&name={name}`
- Steps:
  1. Target — new PVC name, namespace (defaults to snapshot's), size (defaults to `status.restoreSize`, editable up, validated >= restoreSize)
  2. Storage Class — auto-selected from snapshot's driver, editable dropdown
  3. Access Mode — radio: ReadWriteOnce / ReadWriteMany / ReadOnlyMany
  4. Review — YAML preview of PVC with `dataSource: {name, kind: VolumeSnapshot, apiGroup: snapshot.storage.k8s.io}`
- Uses existing PVC create endpoint: `POST /api/v1/resources/pvcs/{ns}`

### Step 4C.5 — Scheduled Snapshots Wizard

**Frontend: `frontend/islands/ScheduledSnapshotWizard.tsx` (new)**

- Route: `frontend/routes/resources/volumesnapshots/schedule.tsx`
- Steps:
  1. Source — PVC picker (same as create wizard)
  2. Snapshot Class — same as create wizard
  3. Schedule — CronScheduleInput component (see shared components below), timezone info
  4. Retention — number input: keep last N snapshots (default 5)
  5. Review — YAML preview of CronJob
- Generates a CronJob spec:
  - Container: `bitnami/kubectl:1.31` (pinned version, NOT `latest` — mutable tags are a production risk)
  - Command: `kubectl create -f /tmp/snapshot.yaml` (snapshot YAML mounted via ConfigMap)
  - Labels: `k8scenter.io/scheduled-snapshot: "true"`, `k8scenter.io/source-pvc: "{name}"`
- Generates a retention CronJob (mandatory, not optional — without retention, snapshots accumulate forever):
  - Lists snapshots by label, deletes oldest beyond N
  - Consider combining create + cleanup into a single CronJob with a shell script for simplicity
- **RBAC output**: the wizard must also generate a ServiceAccount + Role + RoleBinding granting the CronJob permission to create/list/delete VolumeSnapshots in the target namespace. These resources are included in the YAML preview and applied alongside the CronJob.
- Uses existing CronJob create: `POST /api/v1/resources/cronjobs/{ns}` (multi-doc YAML for CronJob + ServiceAccount + Role + RoleBinding)

### Step 4C.6 — PVC Wizard

**Backend: `backend/internal/wizard/pvc.go` (new)**

- `PVCInput`: name, namespace, storageClassName, size, sizeUnit (Mi/Gi/Ti), accessMode, selectorLabels
- `Validate()`: DNS label name, size > 0, valid access mode
- `ToPVC()`: returns `corev1.PersistentVolumeClaim`
- Preview endpoint: `POST /api/v1/wizards/pvc/preview`

**Frontend: `frontend/islands/PVCWizard.tsx` (new)**

- Route: `frontend/routes/resources/pvcs/new.tsx`
- Steps:
  1. Basics — name, namespace
  2. Storage — storage class dropdown (from informer), size input with unit selector (Mi/Gi/Ti), access mode radio
  3. Selector — optional label selector (KeyValueListEditor for match labels)
  4. Review

**Files to create/modify:**
- `backend/internal/storage/handler.go` (add snapshot CRUD)
- `backend/internal/wizard/snapshot.go` (new)
- `backend/internal/wizard/pvc.go` (new)
- `backend/internal/wizard/handler.go` (add preview routes)
- `backend/internal/server/routes.go` (add storage + wizard routes)
- `frontend/islands/SnapshotWizard.tsx` (new)
- `frontend/islands/RestoreSnapshotWizard.tsx` (new)
- `frontend/islands/ScheduledSnapshotWizard.tsx` (new)
- `frontend/islands/PVCWizard.tsx` (new)
- `frontend/components/wizard/CronScheduleInput.tsx` (new — shared, created here in Phase 4C, reused by Phase 4D CronJob wizard)
- `frontend/lib/resource-columns.ts` (add VolumeSnapshot columns)
- `frontend/lib/constants.ts` (add snapshot routes + nav)
- `frontend/routes/resources/volumesnapshots/*.tsx` (new routes)
- `frontend/routes/resources/pvcs/new.tsx` (new)

---

## Phase 4D: Resource Wizards

**Branch:** `feat/phase4d-resource-wizards`

### Step 4D.0 — Extract Shared ContainerForm

Before building wizards, extract the reusable container config from `DeploymentBasicsStep.tsx` + `DeploymentResourcesStep.tsx` + `DeploymentNetworkStep.tsx`:

**`frontend/components/wizard/ContainerForm.tsx` (new)**

Consolidates: image, command, args, env vars, ports, resource limits (CPU/memory requests + limits), liveness/readiness/startup probes. **Does NOT include volume mounts** — those do not exist in the current DeploymentWizard and should be added as a separate enhancement later, not smuggled in via extraction.

Props: `container: ContainerState`, `onChange: (field, value) => void`, `errors: Record<string,string>`

Refactor `DeploymentWizard.tsx` to use `ContainerForm` instead of the separate step components.

**`frontend/components/wizard/SelectorBuilder.tsx` (new)**

Label selector builder: rows of `{key, operator, values[]}` for matchExpressions, plus simple `{key: value}` rows for matchLabels. Used by NetworkPolicy, PodSelector, NodeSelector wizards.

### Step 4D.1 — ConfigMap Wizard

**Backend: `backend/internal/wizard/configmap.go`**
- `ConfigMapInput`: name, namespace, labels, data (map[string]string), binaryData (map[string]string, base64)
- Validation: DNS label name, total data size < 1MB (k8s limit), key names valid
- Preview endpoint: `POST /api/v1/wizards/configmap/preview`

**Frontend: `frontend/islands/ConfigMapWizard.tsx`**
- Steps: Basics (name, namespace, labels) → Data (KeyValueListEditor for string data, file upload button for binary data with base64 encoding) → Review
- Route: `frontend/routes/resources/configmaps/new.tsx`

### Step 4D.2 — Secret Wizard

**Backend: `backend/internal/wizard/secret.go`**
- `SecretInput`: name, namespace, type (Opaque/kubernetes.io/tls/kubernetes.io/dockerconfigjson/kubernetes.io/basic-auth), labels, data (map[string]string)
- Type-specific validation: TLS requires `tls.crt` + `tls.key`, DockerConfigJSON requires `.dockerconfigjson`, BasicAuth requires `username`
- Preview endpoint: `POST /api/v1/wizards/secret/preview` (values masked in preview YAML)

**Frontend: `frontend/islands/SecretWizard.tsx`**
- Steps: Basics (name, namespace, type dropdown) → Data (dynamic form based on type: Opaque = key-value with masked inputs + file upload; TLS = cert + key file uploads; DockerConfigJSON = registry/username/password/email fields; BasicAuth = username + password) → Review
- Route: `frontend/routes/resources/secrets/new.tsx`

### Step 4D.3 — Namespace Wizard

**Backend: `backend/internal/wizard/namespace.go`**
- `NamespaceInput`: name, labels, resourceQuota (optional: cpu, memory, pods limits), limitRange (optional: defaultCPU, defaultMemory)
- Returns array of resources: Namespace + optional ResourceQuota + optional LimitRange
- Preview endpoint: `POST /api/v1/wizards/namespace/preview` (returns multi-doc YAML)

**Frontend: `frontend/islands/NamespaceWizard.tsx`**
- Steps: Basics (name, labels) → Resource Quotas (toggle enable, CPU/memory/pod count inputs) → Limit Range (toggle enable, default container CPU/memory) → Review (multi-doc YAML)
- Route: `frontend/routes/resources/namespaces/new.tsx`

### Step 4D.4 — NetworkPolicy Wizard

**Backend: `backend/internal/wizard/networkpolicy.go`**
- `NetworkPolicyInput`: name, namespace, podSelector, ingressRules[], egressRules[]
- Each rule: from/to selectors (podSelector, namespaceSelector, ipBlock), ports[]
- Preview endpoint: `POST /api/v1/wizards/networkpolicy/preview`

**Frontend: `frontend/islands/NetworkPolicyWizard.tsx`**
- Steps: Basics (name, namespace) → Pod Selector (SelectorBuilder for target pods) → Ingress (rule builder: add rules, each with from-selectors + ports) → Egress (same pattern) → Review
- Route: `frontend/routes/resources/networkpolicies/new.tsx`

### Step 4D.5 — CiliumNetworkPolicy Wizard

**Backend: `backend/internal/wizard/ciliumnetworkpolicy.go`**
- Uses `unstructured.Unstructured` (CRD, not typed)
- `CiliumNetworkPolicyInput`: name, namespace, endpointSelector, ingressRules[], egressRules[]
- Ingress/egress support L3 (fromEndpoints, fromCIDR), L4 (ports + protocol), L7 (HTTP method/path rules, DNS names)
- Preview endpoint: `POST /api/v1/wizards/ciliumnetworkpolicy/preview`

**Frontend: `frontend/islands/CiliumNetworkPolicyWizard.tsx`**
- Steps: Basics (name, namespace) → Endpoint Selector (SelectorBuilder) → Ingress (L3/L4/L7 sections per rule) → Egress (same + FQDN/DNS rules) → Review
- CRD availability check: if `ciliumnetworkpolicies` CRD not detected, show info message and hide wizard
- Route: `frontend/routes/resources/ciliumnetworkpolicies/new.tsx`

### Step 4D.6 — Ingress Wizard

**Backend: `backend/internal/wizard/ingress.go`**
- `IngressInput`: name, namespace, ingressClassName, rules[] (host, paths[]{path, pathType, serviceName, servicePort}), tls[] (hosts[], secretName)
- Validation: valid hostnames, path starts with `/`, service exists (optional check), port valid
- Preview endpoint: `POST /api/v1/wizards/ingress/preview`

**Frontend: `frontend/islands/IngressWizard.tsx`**
- Steps: Basics (name, namespace, ingress class dropdown from cluster) → Rules (host + path table: add host, add paths per host, service/port pickers from informer) → TLS (toggle per host, secret selector dropdown or auto cert-manager annotation checkbox) → Review
- Route: `frontend/routes/resources/ingresses/new.tsx`

### Step 4D.7 — Job Wizard

**Backend: `backend/internal/wizard/job.go`**
- `JobInput`: name, namespace, labels, container (ContainerInput — reuse from deployment.go), completions, parallelism, backoffLimit, activeDeadlineSeconds
- Preview endpoint: `POST /api/v1/wizards/job/preview`

**Frontend: `frontend/islands/JobWizard.tsx`**
- Steps: Basics (name, namespace) → Container (ContainerForm) → Job Config (completions, parallelism, backoff limit, active deadline seconds) → Review
- Route: `frontend/routes/resources/jobs/new.tsx`

### Step 4D.8 — CronJob Wizard

**Backend: `backend/internal/wizard/cronjob.go`**
- `CronJobInput`: name, namespace, schedule, timezone, container (ContainerInput), concurrencyPolicy (Allow/Forbid/Replace), successfulJobsHistoryLimit, failedJobsHistoryLimit, suspend
- Preview endpoint: `POST /api/v1/wizards/cronjob/preview`

**Frontend: `frontend/islands/CronJobWizard.tsx`**
- Steps: Basics (name, namespace) → Schedule (CronScheduleInput) → Container (ContainerForm) → Job Config (concurrency policy dropdown, history limits, suspend toggle) → Review
- Route: `frontend/routes/resources/cronjobs/new.tsx`

### Step 4D.9 — DaemonSet Wizard

**Backend: `backend/internal/wizard/daemonset.go`**
- `DaemonSetInput`: name, namespace, labels, container (ContainerInput), nodeSelector (map), maxUnavailable
- Preview endpoint: `POST /api/v1/wizards/daemonset/preview`

**Frontend: `frontend/islands/DaemonSetWizard.tsx`**
- Steps: Basics (name, namespace, labels) → Container (ContainerForm) → Node Selector (SelectorBuilder for targeting nodes) → Update Strategy (RollingUpdate maxUnavailable input) → Review
- Route: `frontend/routes/resources/daemonsets/new.tsx`

### Step 4D.10 — StatefulSet Wizard

**Backend: `backend/internal/wizard/statefulset.go`**
- `StatefulSetInput`: name, namespace, serviceName, labels, container (ContainerInput), volumeClaimTemplates[] (name, storageClassName, size, accessMode), podManagementPolicy (OrderedReady/Parallel), partition
- Preview endpoint: `POST /api/v1/wizards/statefulset/preview`

**Frontend: `frontend/islands/StatefulSetWizard.tsx`**
- Steps: Basics (name, namespace, headless service name dropdown) → Container (ContainerForm) → Volume Claim Templates (add multiple: name, storage class, size, access mode per template) → Update Strategy (podManagementPolicy radio, partition number for RollingUpdate) → Review
- Route: `frontend/routes/resources/statefulsets/new.tsx`

**Shared component — CronScheduleInput:**

`frontend/components/wizard/CronScheduleInput.tsx` (new):
- Raw cron expression input field (5-field, standard k8s format)
- Preset buttons: Every minute, Every 5 min, Hourly, Daily midnight, Weekly Monday 9AM, Monthly 1st
- Human-readable preview using `npm:cronstrue` (add to `deno.json`: `"cronstrue": "npm:cronstrue@^2"`)
- Validation: parse cron expression, reject invalid syntax
- Optional timezone selector (for k8s 1.27+ `.spec.timeZone`)

**Files to create/modify (Phase 4D total):**
- `frontend/components/wizard/ContainerForm.tsx` (new — extracted from Deployment steps)
- `frontend/components/wizard/SelectorBuilder.tsx` (new)
- `frontend/components/wizard/CronScheduleInput.tsx` (new)
- `frontend/islands/DeploymentWizard.tsx` (refactor to use ContainerForm)
- `backend/internal/wizard/configmap.go` (new)
- `backend/internal/wizard/secret.go` (new)
- `backend/internal/wizard/namespace.go` (new)
- `backend/internal/wizard/networkpolicy.go` (new)
- `backend/internal/wizard/ciliumnetworkpolicy.go` (new)
- `backend/internal/wizard/ingress.go` (new)
- `backend/internal/wizard/job.go` (new)
- `backend/internal/wizard/cronjob.go` (new)
- `backend/internal/wizard/daemonset.go` (new)
- `backend/internal/wizard/statefulset.go` (new)
- `backend/internal/wizard/handler.go` (add 10 preview handlers)
- `backend/internal/server/routes.go` (add 10 wizard preview routes)
- `frontend/islands/{ConfigMap,Secret,Namespace,NetworkPolicy,CiliumNetworkPolicy,Ingress,Job,CronJob,DaemonSet,StatefulSet}Wizard.tsx` (10 new)
- `frontend/routes/resources/*/new.tsx` (10 new route pages)
- `frontend/deno.json` (add `cronstrue` import)
- `frontend/lib/constants.ts` (add "Create" links for all resource types)

---

## Shared Validation & Generic Handler Extraction

Before Phase 4D, extract shared infrastructure from the wizard package:

**`backend/internal/wizard/validation.go` (new):**
- `dnsLabelRegex`, `envVarNameRegex`
- `validateQuantity(field, value string) *FieldError`
- `validateProbe(prefix string, probe *ProbeInput) []FieldError`
- `ContainerInput` struct + `ValidateContainer()` method (shared across Deployment, Job, CronJob, DaemonSet, StatefulSet)

**`backend/internal/wizard/handler.go` — define `WizardInput` interface + generic preview handler:**

```go
// WizardInput is the contract all wizard input types must implement.
type WizardInput interface {
    Validate() []FieldError
    ToYAML() ([]byte, error)
}

// handlePreview is the single generic preview handler that replaces 13 copy-pasted methods.
func (h *Handler) handlePreview(input WizardInput) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if _, ok := httputil.RequireUser(w, r); !ok { return }
        if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(input); err != nil { ... }
        if errs := input.Validate(); len(errs) > 0 { writeValidationErrors(w, errs); return }
        yamlBytes, err := input.ToYAML()
        if err != nil { ... }
        httputil.WriteData(w, map[string]string{"yaml": string(yamlBytes)})
    }
}
```

Each wizard input type (DeploymentInput, ServiceInput, etc.) implements `WizardInput`. Route registration becomes:
```go
wr.Post("/deployment/preview", h.handlePreview(&DeploymentInput{}))
wr.Post("/configmap/preview", h.handlePreview(&ConfigMapInput{}))
// ... one line per wizard, zero boilerplate
```

Refactor existing 3 wizard handlers (deployment, service, storage) to use this interface first, verify tests pass, then add new wizards using it.

---

## Testing Strategy

Each phase includes:
- **Backend unit tests**: new handlers tested with httptest + fake clientset (follow existing `resources_test.go` pattern)
- **Frontend lint + type check**: `deno lint`, `deno fmt --check`, `deno check`
- **Homelab smoke test**: mandatory before merge per CLAUDE.md pre-merge requirements
  - Phase 4A: verify log streaming works with real pods, exec terminal connects, settings page saves
  - Phase 4B: create user via wizard, verify k8s impersonation works, create Role/RoleBinding
  - Phase 4C: create snapshot (requires CSI driver with snapshot support — may need to test with Longhorn or mock)
  - Phase 4D: create one resource of each type via wizard, verify YAML preview accuracy

**Phase-specific test requirements (from plan review):**

- **WebSocket handlers (4A)**: test that connects, authenticates, sends messages, and verifies response format. Test backpressure (slow consumer), connection limit enforcement, and context cancellation cleanup. Test exec shell detection with container that has no shell (should return error, not hang).
- **Wizard validation (4D)**: table-driven tests for each wizard input's `Validate()` method covering edge cases (empty required fields, invalid DNS labels, out-of-range ports, duplicate entries). One test file per wizard: `wizard/{type}_test.go`.
- **Shared components**: the `WizardInput` interface contract should have a test that verifies all implementations produce valid YAML (parse the output with `sigs.k8s.io/yaml`).

---

## Dependencies & Risk

| Risk | Mitigation |
|------|-----------|
| xterm.js SSR failure | Dynamic import() inside useEffect — CodeMirror precedent proves pattern works |
| VolumeSnapshot CRDs not installed on homelab | Graceful degradation — detect CRDs, hide UI if absent. Test with mock or install snapshot controller |
| CiliumNetworkPolicy CRD not on all clusters | Already handled — dynamic informer pattern from PR #49 |
| Large number of new files (40+ frontend, 15+ backend) | Independent phases reduce blast radius. Each wizard follows identical pattern via `WizardInput` interface. |
| Phase dependency: CronScheduleInput needed in 4C but shared with 4D | Create the component in Phase 4C (scheduled snapshots), reuse in 4D (CronJob wizard) |
| Scheduled snapshot CronJob needs RBAC | Wizard generates ServiceAccount + Role + RoleBinding alongside CronJob in multi-doc YAML |
| Shared ContainerForm extraction breaking DeploymentWizard | Extract first in Phase 4D step 0, verify deployment wizard still works before proceeding |
| gorilla/websocket archived | Stable, widely used, already throughout codebase. No action needed. |

---

## Acceptance Criteria

### Phase 4A
- [ ] Pod log streaming works via WebSocket with follow mode, ANSI colors, search, download, multi-container tabs
- [ ] Pod exec terminal renders via xterm.js with resize, shell detection, multiple sessions, fullscreen
- [ ] Settings page saves all sections to PostgreSQL, Test Connection buttons work
- [ ] First-run setup wizard creates admin and configures monitoring/alerting
- [ ] Backend tests pass, frontend lint/type check pass, homelab smoke test pass

### Phase 4B
- [ ] New user wizard creates user + assigns k8s role in one flow
- [ ] RBAC CRUD (create/update/delete) works for all four types (Role, ClusterRole, RoleBinding, ClusterRoleBinding)
- [ ] Role wizard builds valid rules with API group/resource/verb selection
- [ ] RoleBinding wizard assigns subjects to roles with local user integration
- [ ] All RBAC operations are audit logged

### Phase 4C
- [ ] VolumeSnapshot browser page lists snapshots with status
- [ ] Create snapshot wizard picks PVC + class, generates valid VolumeSnapshot YAML
- [ ] Restore wizard creates PVC from snapshot with correct dataSource
- [ ] Scheduled snapshot wizard generates CronJob with retention policy
- [ ] PVC wizard creates PVC with storage class, size, access mode
- [ ] Graceful degradation when snapshot CRDs not installed

### Phase 4D
- [ ] All 10 resource wizards produce valid YAML that applies successfully
- [ ] ContainerForm extracted and shared across Deployment, Job, CronJob, DaemonSet, StatefulSet
- [ ] SelectorBuilder shared across NetworkPolicy, CiliumNetworkPolicy, DaemonSet
- [ ] CronScheduleInput shared across CronJob and Scheduled Snapshot wizards
- [ ] All wizard "Create" buttons visible in resource browser pages
- [ ] Existing DeploymentWizard still works after ContainerForm extraction
