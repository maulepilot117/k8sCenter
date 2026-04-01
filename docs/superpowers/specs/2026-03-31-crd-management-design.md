# CRD Management — Extensions Hub Design

## Overview

Add a dedicated "Extensions" section to k8sCenter for discovering, browsing, and managing Custom Resource Definitions (CRDs) and their instances. The design uses a **Hybrid Hub** approach: a central Extensions page auto-discovers all CRDs in the cluster, groups them by API group, and provides full CRUD for any custom resource via a schema-driven form.

Editing uses a **progressive schema-driven form** auto-generated from each CRD's OpenAPI validation schema — no code editor required. Deep schema branches degrade to inline YAML editing. A read-only YAML preview tab shows the generated output.

## Problem Statement

k8sCenter manages 30+ built-in Kubernetes resource types but has no generic support for Custom Resource Definitions. CRDs are critical API extensions for installed operators (Cilium, cert-manager, Prometheus, etc.). Currently, Cilium policies are the only CRD with dedicated handler code — every other CRD is invisible in the UI.

Most k8s management tools handle CRDs poorly: a flat dropdown list of every API extension, expanding to raw YAML editing. This approach fails on three axes:

1. **Discoverability** — hard to find what you're looking for among dozens of CRDs
2. **Context** — CRDs shown in isolation, disconnected from the domain they belong to
3. **Usability** — editing is raw YAML with no structure or validation

## Design

### Extensions Hub (9th Icon Rail Section)

A new "Extensions" entry in the icon rail (puzzle piece icon) opens the hub page.

**Page header** — "Extensions" title with subtitle showing CRD count and API group count (e.g., "37 CRDs across 8 API groups"). Search/filter bar for filtering CRD cards.

**API group clustering** — CRDs grouped by their API group (e.g., `cilium.io`, `cert-manager.io`, `snapshot.storage.k8s.io`). Each group shows a header with type count and instance count, followed by a responsive card grid.

**CRD cards** — each card shows:
- Kind name (e.g., "Certificate")
- Full resource name (e.g., `certificates.cert-manager.io`)
- Instance count badge (lazy-loaded, cached server-side)
- Scope indicator (Namespaced / Cluster)

Clicking a card navigates to the resource list view.

### Resource List View

Navigating to a specific CRD shows:
- **Breadcrumbs**: Extensions → cert-manager.io → Certificates
- **CRD metadata bar**: group, version, kind, scope (namespaced/cluster), served, storage version
- **Search + status filter tabs** (All, Ready, Not Ready — derived from `.status.conditions`)
- **Resource table**: columns are determined by priority:
  1. Always: Name, Namespace (if namespaced), Age
  2. Status: derived from `.status.conditions[?type==Ready].status` if present
  3. `additionalPrinterColumns`: if the CRD defines them (68% of real-world CRDs do), use those — they represent the CRD author's explicit intent for what to display
  4. Fallback: no additional columns beyond Name/Namespace/Age/Status

### Schema-Driven Form (Create/Edit)

When creating or editing a CR instance, the form is auto-generated from the CRD's `spec.versions[storageVersion].schema.openAPIV3Schema` using **progressive rendering**.

#### Progressive Rendering Strategy

Based on real-world CRD analysis (37 CRDs in a production homelab: max 1,963 fields, max depth 18, schemas up to 475KB), hard caps would break legitimate CRDs. Instead, the form uses progressive disclosure:

1. **Depth 0-4: full form fields** — covers the fields users edit 90% of the time
2. **Depth 5+: collapsible YAML textarea** — renders that subtree as editable YAML, not as individual form fields. The user sees form fields for common configuration and an inline YAML block for deeply-nested structures (e.g., pod template specs)
3. **Lazy mounting** — collapsed fieldsets do not render children until expanded. A 1,963-field Prometheus CRD only renders ~30 top-level fields initially
4. **`anyOf`/`oneOf`: type selector dropdown** — "Select variant: A | B | C" — then renders the selected variant's fields as form fields (32% of CRDs use anyOf)
5. **`x-kubernetes-preserve-unknown-fields`: key-value editor** — renders like labels/annotations (11% of CRDs use this)
6. **`x-kubernetes-int-or-string`: text input** with validation accepting either type
7. **No hard field count cap** — lazy mounting makes this unnecessary

#### Field Type Mapping

- `type: string` → text input
- `type: string` with `enum` → select dropdown (if >100 values, use searchable text input with validation)
- `type: string` with `format: date-time` → text input with format hint
- `type: integer` / `type: number` → number input
- `type: boolean` → toggle switch
- `type: array` of primitives → repeatable input rows with + Add / × Remove
- `type: array` of objects → repeatable fieldsets
- `type: object` → collapsible fieldset section
- `additionalProperties: true` → key-value editor (like labels)

#### Form Structure

- **Metadata section**: name (text input, validated: `[a-z0-9][a-z0-9\-\.]*`, max 253 chars) + namespace (dropdown, populated from cluster). Not shown for cluster-scoped CRDs.
- **Spec section**: fields generated from schema using progressive rendering
- **Labels/Annotations**: key-value editor (existing pattern from wizards)

#### Field Features

- Required fields marked with red asterisk
- Field descriptions from schema shown as inline helper text (rendered as **escaped plain text**, never innerHTML — prevents XSS from malicious schema descriptions)
- Validation on blur (type checking, required, enum from schema). Pattern validation deferred to server-side dry-run (avoids ReDoS from malicious regex patterns in schemas)
- Required nested objects start expanded, optional ones start collapsed
- Default values from schema pre-populate fields

#### Action Bar

- **Create/Update** — server-side apply
- **Validate** — dry-run against the API server
- **View YAML** — read-only syntax-highlighted YAML preview (CSS-only, `<pre>` with color classes)
- **Cancel** — return to resource list

#### Graceful Degradation Tiers

1. **Full OpenAPI schema** → progressive schema form (most CRDs)
2. **Schema with `x-kubernetes-preserve-unknown-fields` at root** → YAML textarea with dry-run validation
3. **No schema defined** → YAML textarea with template from `additionalPrinterColumns` hints
4. **Schema fetch failure** → error state with retry button

#### Post-Action Navigation

- **After create**: redirect to resource list with success toast
- **After update**: stay on detail page with success toast
- **After delete**: redirect to resource list (row removed)

### Delete Confirmation

Delete actions show the existing `ConfirmDialog` component with the resource name. If the resource has finalizers, the dialog warns that deletion may be blocked or delayed.

### Multi-Cluster Scoping

**MVP: Extensions is local-cluster-only.** CRD discovery runs against the local cluster where k8sCenter is deployed. Remote clusters accessed via multi-cluster support do not have CRD discovery or extensions browsing. This is consistent with how informers and WebSocket events are local-only. Multi-cluster CRD support is future work.

## Backend Architecture

### Generic CRD Handler

A single `GenericCRDHandler` in `backend/internal/k8s/resources/` handles all CRD operations using the dynamic client. No per-CRD handler methods needed.

**API endpoints:**
```
GET    /api/v1/extensions/crds                                    → list all CRDs (lightweight metadata, no schemas)
GET    /api/v1/extensions/crds/:group/:resource                   → CRD metadata + OpenAPI schema
GET    /api/v1/extensions/crds/counts                             → batch instance counts (cached, 30s TTL)
POST   /api/v1/extensions/crds/rediscover                         → force CRD cache refresh (admin-only)
GET    /api/v1/extensions/resources/:group/:resource               → list instances (cluster-scoped)
GET    /api/v1/extensions/resources/:group/:resource/:ns           → list instances (namespaced)
GET    /api/v1/extensions/resources/:group/:resource/:ns/:name     → get single instance
POST   /api/v1/extensions/resources/:group/:resource/:ns           → create instance
PUT    /api/v1/extensions/resources/:group/:resource/:ns/:name     → update instance
DELETE /api/v1/extensions/resources/:group/:resource/:ns/:name     → delete instance
POST   /api/v1/extensions/resources/:group/:resource/-/validate    → dry-run validate (note: /-/ sentinel avoids namespace ambiguity)
```

For cluster-scoped CRDs, omit the `:ns` segment.

### Security: GVR Allowlist

The generic handler MUST NOT serve core Kubernetes resources. Before resolving `{group}/{resource}` to a GVR:

1. **Verify the GVR corresponds to an actual CRD** — check against the cached CRD discovery list
2. **Denylist core API groups** — reject requests for `""` (core), `apps`, `batch`, `networking.k8s.io`, `rbac.authorization.k8s.io`, `storage.k8s.io`, `apiextensions.k8s.io`, `policy`, `autoscaling`, `coordination.k8s.io`
3. **Validate path params** — `{group}` and `{resource}` must match RFC 1123 DNS subdomain format
4. **Return 404** for unknown group/resource combinations

This prevents the generic handler from bypassing dedicated handlers that have secret masking, danger detection, or other safety logic.

### Schema Security

- All schema-derived text (descriptions, titles, defaults, enum labels) is **sanitized server-side** — strip HTML tags before returning in the API response
- Schema `pattern` values are NOT sent to the frontend for client-side validation (avoids ReDoS). Pattern validation happens only via server-side dry-run
- Max schema response size: 1MB (reject CRDs with larger schemas, fall back to YAML textarea)

### Implementation Details

- Path params `{group}` and `{resource}` resolve to a GVR using the **in-memory CRD cache** (not a live discovery call per request)
- All CRUD uses `DynamicClientForUser()` (already exists in `client.go`) for user-impersonated access
- CRD discovery uses a **CRD informer** (`apiextensionsv1.CustomResourceDefinition`) for efficient delta updates — not polling. Instant detection of new/removed CRDs, no 60-second staleness window
- Schema fetched on-demand when user navigates to a specific CRD's form (not eagerly cached for all CRDs)
- In-memory GVR lookup map (`map[groupResource]schema.GroupVersionResource`) built from the CRD informer cache, updated on add/update/delete events
- Instance counts cached server-side with 30s TTL, fetched with bounded concurrency (semaphore of 5) to avoid API server flooding
- Version strategy: use the CRD version marked `storage: true`
- Stale cache handling: if dynamic client returns 404 for a previously-discovered CRD, evict from cache and return user-friendly "CRD may have been removed" message

**Rate limiting:** Apply the YAML rate limiter (30 req/min) to all write endpoints and the validate endpoint.

**Audit logging:** All CRD create/update/delete operations log to the audit table using the existing `audit.Logger` interface with `action=create|update|delete`, `resourceKind=<CRD kind>`, `resourceName=<instance name>`.

**What stays unchanged:**
- Cilium's dedicated handler (has custom validation, danger detection)
- VolumeSnapshot handlers (have wizard integration)
- All existing resource handlers

## Frontend Architecture

### New Routes (file-system routing)

- `routes/extensions/index.tsx` — hub page
- `routes/extensions/[group]/[resource]/index.tsx` — resource list
- `routes/extensions/[group]/[resource]/new.tsx` — create form
- `routes/extensions/[group]/[resource]/[namespace]/[name].tsx` — detail + edit (namespaced)
- `routes/extensions/[group]/[resource]/_/[name].tsx` — detail + edit (cluster-scoped, `_` as namespace placeholder)

### New Islands

- `ExtensionsHub.tsx` — hub page: fetches CRD list, renders API group sections with search/filter
- `CRDResourceTable.tsx` — thin wrapper around ResourceTable for `/extensions/resources/...` endpoints. Supports `limit`/`continue` pagination params via dynamic client
- `SchemaForm.tsx` — progressive form generator from OpenAPI schema (depth 0-4 as fields, 5+ as YAML textarea)
- `SchemaFormField.tsx` — single field renderer (dispatches by type: string, number, boolean, array, object, anyOf, preserveUnknown)
- `YamlPreview.tsx` — read-only syntax-highlighted YAML (`<pre>` with CSS color classes)

### Accessibility

All dynamically-generated form fields must have:
- Stable `id` attributes (derived from schema path, e.g., `field-spec-issuerRef-name`)
- `<label htmlFor>` associations
- `aria-required` on required fields
- `aria-expanded` on collapsible fieldsets
- Keyboard navigation for expand/collapse (Enter/Space)

### Modified Files

- `lib/constants.ts` — add Extensions to `DOMAIN_SECTIONS`
- `islands/IconRail.tsx` — add 9th entry (puzzle piece icon, `/extensions` href)

### Client-Side Schema Caching

Cache the processed schema in `sessionStorage` keyed by `group/resource/resourceVersion`. On subsequent visits, skip the network request if the cached version matches. Avoids re-fetching on back-navigation.

## Scope Boundaries

**In scope (MVP):**
- CRD discovery and listing (local cluster only)
- Generic CRUD for all CRD instances via dynamic client
- Progressive schema-driven form generation
- Read-only YAML preview
- `additionalPrinterColumns` for resource table columns
- Server-side instance count caching
- GVR allowlist security
- Audit logging for CRD operations

**Deferred to v2:**
- Domain assignment (assign CRDs to existing navigation sections like Networking, Security)
- Command palette (Cmd+K) integration for CRD search
- WebSocket live updates for CRD instances
- Multi-cluster CRD discovery
- CRD-specific wizards (e.g., cert-manager-aware Certificate wizard)
- CRD relationship mapping (e.g., Certificates → Issuers)
- Custom column configuration per CRD type
