# Phase 4: Features & Wizards Design Spec

## Overview

Phase 4 adds core infrastructure features (pod log streaming, pod exec terminal, persistent settings), user/RBAC management wizards, storage snapshot lifecycle, and 10 resource creation wizards. Work is decomposed into four independent phases (4A-4D), each producing a working increment with its own branch.

---

## Phase 4A: Core Infrastructure

### Pod Log Streaming

**Backend:** New WebSocket endpoint `WS /api/v1/ws/logs/{namespace}/{pod}/{container}`.

- Authenticates via first WS message (same pattern as resource WS hub)
- Wraps k8s `pods/log` API with `Follow: true` and `TailLines` for initial backfill
- Streams log lines as JSON: `{type: "log", data: "line text", timestamp: "RFC3339"}`
- Query params: `tailLines` (initial backfill count), `previous` (previous container), `timestamps` (include timestamps)
- Heartbeat ping every 30s to detect dead connections
- RBAC: checks `get` on `pods/log` subresource via impersonated client
- Audit logging for log stream initiation

**Frontend:** Replace current `LogViewer.tsx` HTTP polling island with full-featured version.

- **WebSocket follow mode** — toggle between "tail" (live streaming) and "snapshot" (one-shot HTTP fetch). Default to tail.
- **ANSI color rendering** — lightweight ANSI-to-HTML parser for colored output in `<pre>` block.
- **Search** — client-side text search with match highlighting and prev/next navigation.
- **Download** — download current buffer as `.log` file.
- **Multi-container** — tab bar showing all containers in the pod (init containers labeled). Each tab has its own independent log stream. Side-by-side split view option for 2-container pods.
- **Auto-scroll** — auto-scroll to bottom in follow mode, pause when user scrolls up, resume button to snap back.
- **Buffer limit** — keep last 10,000 lines in memory, drop oldest when exceeded.

### Pod Exec Terminal

**Backend enhancements to existing SPDY bridge in `pods.go`:**

- **Resize support** — handle `{type: "resize", cols: N, rows: N}` messages from the client to send SIGWINCH via the SPDY resize stream.
- **Shell detection** — try `/bin/bash` first, fall back to `/bin/sh`, then `/bin/ash`. Send detected shell in initial response message.
- **Multiple shells** — each "Open Terminal" click creates a new WS connection (separate exec session). No server-side multiplexing — client manages tabs.

**Frontend:** New `PodTerminal.tsx` island using xterm.js (via npm specifier).

- **xterm.js + fit addon** — terminal emulator with VT100 rendering, resize tracking via `ResizeObserver` on the container.
- **Tab bar** — multiple terminal sessions per pod, each with its own WS connection. Close tab = close WS. Tab label shows container name + shell.
- **Container picker** — dropdown to select which container to exec into (default: first non-init container).
- **Copy/paste** — xterm.js handles Ctrl+Shift+C/V natively. Right-click context menu for copy/paste.
- **Reconnect** — if WS drops, show overlay with "Reconnect" button (don't auto-reconnect — user may have typed sensitive commands).
- **Placement** — new tab in pod detail page ("Terminal") alongside Overview, YAML, Events, Logs, Metrics.
- **Fullscreen toggle** — expand terminal to fill viewport, Esc to exit.

### Persistent Settings UI + First-Run Wizard

**Settings Page** (`/settings/general`):

Single page with accordion sections, each independently collapsible:

- **General** — cluster display name, default namespace
- **Monitoring** — Prometheus URL, Grafana URL, Grafana token, monitoring namespace. "Test Connection" button for each.
- **Alerting** — enable/disable toggle, SMTP host/port/username/password/from, rate limit, recipients list. "Send Test Email" button.
- **Auth Providers** — existing OIDC/LDAP config UI integrated here.

Each section has a "Save" button that PUTs only that section's fields. Toast on success/error. Unsaved changes warning on navigation away.

**First-Run Setup Wizard** (`/setup`):

Triggered when backend detects zero users AND no settings configured. BFF checks `GET /api/v1/setup/status` on app load — if `needsSetup: true`, redirect to `/setup`.

Steps:
1. **Welcome** — branding, brief description of k8sCenter
2. **Admin Account** — username + password (reuses existing `POST /setup/init`)
3. **Monitoring** — optional Prometheus/Grafana URLs, skip button
4. **Alerting** — optional SMTP config, skip button
5. **Review** — summary of what was configured
6. **Done** — redirect to login page

Backend addition: `GET /api/v1/setup/status` — returns `{needsSetup: bool}` based on user count = 0. Public endpoint (no auth).

---

## Phase 4B: User & RBAC Management

### User Creation + Role Assignment Wizard

Accessible from UserManager page via "Create User" button.

Steps:
1. **Account** — username, password, confirm password. Validation: 8-128 chars, username uniqueness check (debounced API call).
2. **Kubernetes Identity** — k8s username (defaults to local username), k8s groups (tag-style input, default: `["system:authenticated"]`). Help tooltip explaining impersonation.
3. **Role Assignment** — optional, skippable. Dual-mode picker:
   - **Quick assign** — dropdown of existing ClusterRoles (cluster-admin, admin, edit, view, custom) → creates ClusterRoleBinding
   - **Namespace-scoped** — pick namespace(s) + Role/ClusterRole → creates RoleBinding(s) per namespace
   - Can add multiple bindings (table showing pending assignments, remove button per row)
4. **Review** — summary of account details + YAML preview of all resources (user in local store + RoleBinding/ClusterRoleBinding k8s resources)

**Backend additions:**
- `POST /api/v1/users` — create local user (admin-only, audit logged). Returns created user.
- RoleBinding/ClusterRoleBinding creation uses existing generic resource CRUD endpoints. The wizard frontend orchestrates: create user → create binding(s) in sequence.

### Full RBAC Management

**New sidebar section** ("Access Control") with pages for Roles, ClusterRoles, RoleBindings, ClusterRoleBindings.

**Role / ClusterRole Wizard:**
1. **Basics** — name, namespace (Role only), labels
2. **Rules** — rule builder UI. Each rule row: API groups (multi-select), resources (multi-select with common presets), verbs (checkboxes: get, list, watch, create, update, patch, delete). Add/remove rule rows.
3. **Review** — YAML preview

**RoleBinding / ClusterRoleBinding Wizard:**
1. **Basics** — name, namespace (RoleBinding only)
2. **Role Reference** — pick Role or ClusterRole from searchable dropdown (grouped by type)
3. **Subjects** — add subjects table. Each row: kind (User/Group/ServiceAccount), name, namespace (ServiceAccount only). Common presets for known local users.
4. **Review** — YAML preview

**Backend additions:**
- Add create/update/delete handlers to `rbac_viewer.go` (rename to `rbac.go`) for all four RBAC resource types
- Follow existing CRUD pattern: impersonated client, audit logging, input validation

**Shared components:**
- `RuleBuilder.tsx` — reusable rule editor (API groups + resources + verbs)
- `SubjectPicker.tsx` — reusable subject selector with local user integration

---

## Phase 4C: Storage

### Storage Snapshot Wizard

**Snapshot Browser:** New resource browser page at `/resources/volumesnapshots`. Columns: name, namespace, source PVC, snapshot class, status, size, age.

**Create Snapshot Wizard:**
1. **Source** — pick source PVC from dropdown (filtered by namespace), shows PVC size and storage class
2. **Snapshot Class** — pick VolumeSnapshotClass (filtered to classes compatible with PVC's CSI driver). Auto-select if only one.
3. **Options** — name (auto-generated default: `{pvc-name}-snap-{timestamp}`), labels
4. **Review** — YAML preview

**Restore Snapshot Wizard** (action button on snapshot detail page):
1. **Target** — new PVC name, namespace (defaults to snapshot's namespace), size (defaults to restore size, editable up)
2. **Storage Class** — auto-selected from snapshot's driver, editable
3. **Access Mode** — ReadWriteOnce / ReadWriteMany / ReadOnlyMany
4. **Review** — YAML preview of PVC with `dataSource` pointing to the snapshot

**Scheduled Snapshots Wizard:**
- Implemented as CronJob that creates VolumeSnapshot resources on a schedule
- Steps: pick source PVC, snapshot class, schedule (cron expression with presets: hourly, daily, weekly), retention count
- CronJob runs `bitnami/kubectl` to create VolumeSnapshot
- Retention: second CronJob that lists snapshots by label and deletes old ones
- Uses existing CronJob CRUD — wizard builds the right CronJob spec

### PersistentVolumeClaim Wizard

1. **Basics** — name, namespace
2. **Storage** — storage class (dropdown), size (with unit selector: Mi/Gi/Ti), access mode
3. **Selector** — optional label selector for PV binding
4. **Review** — YAML preview

---

## Phase 4D: Resource Wizards

All follow established pattern: WizardStepper shell → form steps → YAML preview → apply.

### ConfigMap Wizard
- Steps: Basics (name, namespace, labels) → Data (key-value editor, file upload for binary data) → Review

### Secret Wizard
- Steps: Basics (name, namespace, type: Opaque/TLS/DockerConfigJSON/BasicAuth) → Data (key-value editor with masked input, file upload for certs) → Review
- Type selection changes the data form (TLS: cert+key fields, DockerConfigJSON: registry/username/password)

### Namespace Wizard
- Steps: Basics (name, labels) → Resource Quotas (optional: CPU/memory limits, pod count) → LimitRange (optional: default container limits) → Review
- Creates Namespace + optional ResourceQuota + optional LimitRange in one flow

### NetworkPolicy Wizard
- Steps: Basics (name, namespace) → Pod Selector (label selector builder) → Ingress Rules (from: pod/namespace/CIDR selectors, ports) → Egress Rules (same pattern) → Review
- Reusable `SelectorBuilder.tsx` component for label match expressions

### CiliumNetworkPolicy Wizard
- Steps: Basics (name, namespace) → Endpoint Selector → Ingress (L3/L4/L7 rules with HTTP path/method awareness) → Egress → Review
- Leverages existing Cilium integration. More expressive than native NetworkPolicy (L7 rules, DNS-based egress, FQDN selectors)

### Ingress Wizard
- Steps: Basics (name, namespace, ingress class) → Rules (host + path routing table, backend service/port picker) → TLS (enable/disable, secret selector or auto cert-manager annotation) → Review

### Job Wizard
- Steps: Basics (name, namespace) → Container (reuse ContainerForm component: image, command, env, resources) → Job Config (completions, parallelism, backoff limit, active deadline) → Review

### CronJob Wizard
- Steps: Basics (name, namespace) → Schedule (cron expression input with presets + human-readable preview) → Container (reuse ContainerForm) → Job Config (concurrency policy, history limits, suspend) → Review

### DaemonSet Wizard
- Steps: Basics (name, namespace, labels) → Container (reuse ContainerForm) → Node Selector (label selector for targeting specific nodes) → Update Strategy (RollingUpdate max unavailable) → Review

### StatefulSet Wizard
- Steps: Basics (name, namespace, service name) → Container (reuse ContainerForm) → Volume Claim Templates (name, storage class, size, access mode — add multiple) → Update Strategy (RollingUpdate partition, OrderedReady/Parallel) → Review

---

## Shared Components (extracted across all wizards)

- `ContainerForm.tsx` — image, command, args, env vars, resource limits, probes (refactored from DeploymentBasicsStep)
- `SelectorBuilder.tsx` — label key-value match expression builder
- `CronScheduleInput.tsx` — cron expression with presets and human-readable preview
- `RuleBuilder.tsx` — RBAC rule editor (API groups + resources + verbs)
- `SubjectPicker.tsx` — RBAC subject selector with local user integration
- `KeyValueEditor.tsx` — already exists, reuse for ConfigMap/Secret/labels

---

## Phasing & Branch Strategy

| Phase | Branch | Features |
|-------|--------|----------|
| 4A | `feat/phase4a-core-infrastructure` | Pod log streaming, pod exec terminal, settings UI + first-run wizard |
| 4B | `feat/phase4b-user-rbac-management` | User creation wizard, full RBAC management (4 wizards) |
| 4C | `feat/phase4c-storage` | Snapshot create/restore/schedule wizards, PVC wizard |
| 4D | `feat/phase4d-resource-wizards` | ConfigMap, Secret, Namespace, NetworkPolicy, CiliumNetworkPolicy, Ingress, Job, CronJob, DaemonSet, StatefulSet wizards |

Each phase is an independent branch merged to main after smoke testing. Phases can be worked in order (4A→4B→4C→4D) but are designed to be independent — no cross-phase dependencies except shared components extracted in whichever phase needs them first.

---

## Non-Goals (explicitly out of scope)

- Multi-cluster wizard support (Phase 2 infrastructure exists but wizard UI is single-cluster)
- Bidirectional YAML↔form sync (form-to-YAML only, consistent with existing wizards)
- Custom resource definition (CRD) wizards — only built-in k8s resources
- Helm chart wizard — out of scope for Phase 4
- E2E test automation for wizards — manual smoke testing per existing process
