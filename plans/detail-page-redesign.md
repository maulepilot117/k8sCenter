# Resource Detail Page Redesign

## Overview

Restyle the deployment detail page to match `mockups/03-split-pane-detail.html`. Two phases: first restyle the existing single-column layout (low risk, pure visual), then add the split pane with related pods.

## Phase A: Restyle Left Pane (Single PR)

Restyle 3 small files to match the mockup. No behavior changes, no new data fetching.

### 1. ResourceDetail.tsx — Header Only

Replace the current header (breadcrumb → name + age) with the mockup layout:

```
[44x44 icon] [resource name (18px mono)]          [Scale] [Restart] [YAML] [Delete]
             [Workloads / Deployments / name]
```

- Icon: rounded div with accent bg/border matching kind
- Name: 18px, font-mono, font-weight 600, letter-spacing -0.02em
- Breadcrumb: 12px, text-muted, with clickable links
- Action buttons: small bordered buttons — Scale, Restart, YAML (ghost), Delete (danger border)
- Keep all existing action handling logic — just restyle the button rendering

### 2. DeploymentOverview.tsx — Full Restyle (97 lines)

**MetadataSection handling:** For workload kinds, the new info grid subsumes MetadataSection's role (it includes Namespace and Created). Conditionally skip MetadataSection rendering in ResourceDetail.tsx when the kind has a redesigned overview (start with deployments only). Other kinds keep MetadataSection unchanged.

**New shared component:** Create `frontend/components/k8s/detail/InfoGrid.tsx` — a reusable 2-column info grid component that can be adopted by other overview components later.

**New component:** Create `frontend/components/k8s/detail/ConditionsGrid.tsx` — styled conditions display matching the mockup. Do NOT modify the existing `ConditionsTable.tsx` — it's shared by Node, Pod, and Job overviews. DeploymentOverview uses the new ConditionsGrid; other overviews keep using ConditionsTable until they're individually redesigned.

Replace current sections with mockup's info grid + conditions + container card:

**Info Grid** (2-column, 4 rows):
- Status (green dot + "Available") | Replicas ("4/4 ready")
- Namespace | Strategy ("RollingUpdate")
- Created (timestamp + age) | Revision ("#7")
- Selector ("app=api-gateway") | Image (mono, truncated)

CSS: `grid-template-columns: repeat(2, 1fr)`, `gap: 1px`, `background: var(--border-subtle)` for grid lines. Each cell: `bg-surface`, `padding: 12px 14px`. Labels: 10px uppercase muted. Values: 13px mono.

**Conditions Section:**
- Section title: 11px uppercase muted with horizontal line (`::after`)
- Rows: Type (500 weight) | Status (green True / red False, mono) | Message | Age (mono muted)

**Containers Section:**
- Card per container: bg-surface, border, rounded, padding 14px
- Header: container name (mono accent) + status badge (RUNNING green)
- 2x2 grid: Image, Ports, CPU Request/Limit, Memory Request/Limit (text only, no usage bars)

### 3. ConditionsTable.tsx — Restyle (76 lines)

Update row layout to match mockup: `grid-template-columns: 140px 60px 1fr 80px`, hover bg-elevated, styled True/False status text.

### Files touched:
- `frontend/islands/ResourceDetail.tsx` (header restyle + conditional MetadataSection skip, ~60 lines changed)
- `frontend/components/k8s/detail/DeploymentOverview.tsx` (full rewrite, 97 lines)
- `frontend/components/k8s/detail/InfoGrid.tsx` (NEW — reusable 2-column grid)
- `frontend/components/k8s/detail/ConditionsGrid.tsx` (NEW — styled conditions, separate from ConditionsTable)

**NOT modified:** `ConditionsTable.tsx` (shared by Node/Pod/Job — leave unchanged)

### Acceptance Criteria:
- [ ] Header shows icon + name + breadcrumb + styled action buttons
- [ ] Info grid shows 8 fields in 2-column layout matching mockup
- [ ] Conditions show colored True/False with section title
- [ ] Container cards show Image, Ports, CPU/Mem request/limit as text
- [ ] All existing actions (Scale, Restart, Delete, YAML editing) still work
- [ ] `deno fmt && deno lint && deno task build` pass
- [ ] E2E tests pass

---

## Phase B: Split Pane + Related Pods (Follow-up PR)

### 1. ResourceDetail.tsx — SplitPane Wrapper

For workload kinds (deployments, statefulsets, daemonsets, jobs), wrap the Overview tab content in `SplitPane`:
- Left pane: existing overview content
- Right pane: `RelatedPods` component

For non-workload kinds, keep single-column layout.

**SplitPane height issue:** The current SplitPane uses `height: 100%` + `overflow: hidden`. This will conflict with CodeMirror (YAML tab) and tall tab content. Options:
- Make the detail page viewport-filling (`h-[calc(100vh-var(--topbar-height))]`) with header above SplitPane
- Or modify SplitPane to support auto-height mode
This needs prototyping before committing.

**Pods tab removal:** The current "Pods" tab becomes the always-visible right pane. This changes data-fetching from lazy (on tab click) to eager (on page load). Consider lazy-loading RelatedPods with IntersectionObserver.

**E2E impact:** The `resource-detail.spec.ts` test checks for the "Pods" tab. Update E2E tests in the same PR.

### 2. RelatedPods.tsx — Restyle Pod Cards

Each pod card:
- bg-surface, border, rounded, padding 14px, hover effect
- Header: pod name (mono accent) + Running/Pending/Failed badge
- Meta row: Node, IP, Age, Restarts in 11px mono
- CPU/Memory: show request/limit text from pod spec (no Prometheus bars)

### Acceptance Criteria:
- [ ] Deployment detail shows split pane with Related Pods on right
- [ ] Pod cards show name, status, meta, resource requests/limits
- [ ] Clicking a pod navigates to pod detail page
- [ ] Non-workload resources keep single-column layout
- [ ] Tab switching (Overview/YAML/Events/Metrics) works correctly in split pane

---

## References

- Mockup: `mockups/03-split-pane-detail.html`
- ResourceDetail.tsx: 871 lines — header at ~line 454
- DeploymentOverview.tsx: 97 lines
- ConditionsTable.tsx: 76 lines
- RelatedPods.tsx: ~150 lines
- SplitPane.tsx: exists, ready to use
