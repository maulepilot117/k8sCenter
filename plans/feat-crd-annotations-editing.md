# feat: CRD Annotation Display, Editing & Creation Support

## Overview

Add full annotation support to the CRD Extensions pages. Currently, `SchemaForm` handles `metadata.name`, `metadata.namespace`, `metadata.labels`, and `spec` — but **completely omits `metadata.annotations`**. This means:

1. Annotations are invisible in the CRD form (create and edit)
2. Annotations are missing from the YAML preview
3. **Editing any CRD instance silently strips all existing annotations** (including system-managed ones like `kubectl.kubernetes.io/last-applied-configuration`) because `buildBody()` omits them from the PUT payload

This is a data integrity bug for edit mode and a missing feature for create mode.

## Problem Statement

Users managing CRDs through k8sCenter (e.g., Cilium BGP advertisements, LoadBalancer IP Pools) need to see and edit annotation data. Cilium and other operators use annotations on standard Kubernetes resources for cross-resource linking (e.g., `lbipam.cilium.io/ips` on Services). The generic SchemaForm must support annotations to be a complete CRD management tool.

**Critical bug:** Opening a CRD instance in edit mode and clicking Update (without changing anything) silently deletes all annotations because `buildBody()` doesn't include them.

## Acceptance Criteria

- [ ] Annotations section visible in SchemaForm between Labels and Spec sections
- [ ] Edit mode loads existing annotations from the fetched instance
- [ ] Annotations included in `buildBody()` PUT payload (preserving existing annotations)
- [ ] Annotations included in `formStateToYaml()` YAML preview output
- [ ] `yamlPreview` computed updated to pass annotations to `formStateToYaml()`
- [ ] Annotations included in validation dry-run payload
- [ ] `resourceVersion` stored from edit-mode fetch and included in PUT body (fixes optimistic concurrency)
- [ ] `resourceVersion` updated from PUT response after successful save
- [ ] Annotation values use textarea (not single-line input) since values can be large/multiline
- [ ] All annotations fully editable (no system/user distinction — matches how labels work)
- [ ] Duplicate annotation keys prevented (warn on blur or dedup visually)
- [ ] New CRD instances can be created with annotations via the form

## Technical Approach

### Step 1: Add `formAnnotations` signal + `resourceVersion` to SchemaForm

**File:** `frontend/islands/SchemaForm.tsx`

Add new signals alongside existing form state (line ~97):

```typescript
const formAnnotations = useSignal<Array<{ key: string; value: string }>>([]);
const resourceVersion = useSignal<string>("");
```

Type is `Array<{ key: string; value: string }>` — identical to `formLabels`. No special categories.

In the edit-mode fetch block (line ~157), load annotations and resourceVersion:

```typescript
if (meta?.annotations && typeof meta.annotations === "object") {
  formAnnotations.value = Object.entries(
    meta.annotations as Record<string, string>
  ).map(([k, v]) => ({ key: k, value: v }));
}
if (meta?.resourceVersion) {
  resourceVersion.value = meta.resourceVersion as string;
}
```

### Step 2: Include annotations + resourceVersion in `buildBody()`

**File:** `frontend/islands/SchemaForm.tsx` (line ~215)

```typescript
const annotations: Record<string, string> = {};
for (const { key, value } of formAnnotations.value) {
  if (key) annotations[key] = value;
}

const body: Record<string, unknown> = {
  apiVersion: `${group}/${storageVersion.value}`,
  kind: kind.value,
  metadata: {
    name: formName.value,
    ...(scope.value === "Namespaced" ? { namespace: formNamespace.value } : {}),
    ...(Object.keys(labels).length > 0 ? { labels } : {}),
    ...(Object.keys(annotations).length > 0 ? { annotations } : {}),
    ...(mode === "edit" && resourceVersion.value
      ? { resourceVersion: resourceVersion.value }
      : {}),
  },
};
```

**After successful PUT/POST**, update `resourceVersion` from the response so subsequent saves don't 409:

```typescript
if (res.data?.metadata?.resourceVersion) {
  resourceVersion.value = res.data.metadata.resourceVersion;
}
```

### Step 3: Include annotations in YAML preview

**File:** `frontend/lib/schema-to-yaml.ts`

Update `formStateToYaml()` signature to accept annotations:

```typescript
export function formStateToYaml(
  apiVersion: string,
  kind: string,
  metadata: {
    name: string;
    namespace?: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;  // NEW
  },
  spec: Record<string, unknown>,
): string
```

Add annotations rendering after labels in the YAML output.

**File:** `frontend/islands/SchemaForm.tsx` — update `yamlPreview` computed (line ~193)

Build the annotations record and pass it to `formStateToYaml()`:

```typescript
const yamlPreview = useComputed(() => {
  const labels: Record<string, string> = {};
  for (const { key, value } of formLabels.value) {
    if (key) labels[key] = value;
  }
  const annotations: Record<string, string> = {};
  for (const { key, value } of formAnnotations.value) {
    if (key) annotations[key] = value;
  }
  const apiVersion = `${group}/${storageVersion.value}`;
  return formStateToYaml(
    apiVersion,
    kind.value,
    {
      name: formName.value,
      namespace: scope.value === "Namespaced" ? formNamespace.value : undefined,
      labels: Object.keys(labels).length > 0 ? labels : undefined,
      annotations: Object.keys(annotations).length > 0 ? annotations : undefined,
    },
    formSpec.value,
  );
});
```

### Step 4: Render Annotations section in the form UI

**File:** `frontend/islands/SchemaForm.tsx`

Add between the Labels section (line ~643) and Spec section (line ~645). Copy the labels pattern with two differences:

1. **Textarea for values** instead of single-line input (annotation values can be multi-line JSON, URLs, etc.)
2. **Duplicate key warning** — highlight key input in red if another entry has the same key

```
┌─────────────────────────────────────────────┐
│ Annotations ─────────────────────────────── │
│                                             │
│ [kubectl.kubernetes.io/last-applied...]     │
│ [{"apiVersion":"cilium.io/v2...        ]    │
│                                             │
│ [lbipam.cilium.io/ips              ]        │
│ [10.0.0.1,10.0.0.2                ]        │
│                                             │
│ [+ Add Annotation                      ]    │
└─────────────────────────────────────────────┘
```

All annotations are fully editable — no system/user distinction. This matches how labels work in the same form.

## Files to Modify

| File | Change |
|---|---|
| `frontend/islands/SchemaForm.tsx` | Add `formAnnotations` + `resourceVersion` signals, load from instance, render annotations section, include in `buildBody()` + `yamlPreview`, update resourceVersion after save |
| `frontend/lib/schema-to-yaml.ts` | Add `annotations` parameter to `formStateToYaml()`, render in YAML output |

## Files NOT Modified (Confirmed)

| File | Why |
|---|---|
| `backend/internal/k8s/resources/crd_handler.go` | Already returns full unstructured objects including annotations. PUT handler already accepts full object replacement. No backend changes needed. |
| `frontend/lib/crd-types.ts` | Annotations are `Record<string, string>`, no new types needed |
| `frontend/routes/extensions/**` | Routes are thin wrappers, no changes needed |
| `frontend/islands/SchemaFormField.tsx` | Annotations editor is in SchemaForm itself (like labels), not a schema field |

## Design Decisions

1. **All annotations fully editable** — no system/user distinction. Matches how labels work. Avoids maintaining an incomplete prefix deny-list. Users who edit system annotations accept the consequences (same as `kubectl edit`).
2. **All annotations loaded into form state on edit** — safest approach, prevents silent data loss without backend changes.
3. **Textarea for values, not input** — annotation values can be multi-line, JSON blobs, or URLs. Labels use single-line inputs because label values are always short.
4. **resourceVersion included in edit PUT** — fixes a pre-existing optimistic concurrency gap. Updated from response after successful save to prevent stale 409s.
5. **Duplicate key prevention** — visual warning on duplicate keys. `buildBody()` loop is last-write-wins, so duplicates silently lose data without a warning.
6. **No dedicated Cilium wizard** — the generic SchemaForm with annotations support covers CiliumLoadBalancerIPPool and other Cilium CRDs. Dedicated wizards are deferred.
7. **No backend changes** — all gaps are frontend-only.

## Known Limitations

- YAML preview is read-only for all fields (not just annotations) — this is the existing behavior
- Validate endpoint uses dry-run Create even in edit mode (pre-existing issue, out of scope)
- No client-side annotation key format validation — server validates on submit/validate

## Deferred (Not in This PR)

- Collapsible large annotation values (wait for user feedback)
- Client-side annotation key regex validation (server already validates)
- 409 Conflict UX with reload button (existing error toast is sufficient for now)
- System annotation read-only mode (re-evaluate if users report accidental corruption)
- CRD-specific wizards (e.g., dedicated CiliumLoadBalancerIPPool wizard)

## References

- `frontend/islands/SchemaForm.tsx` — main form island, lines 80-240 for state/body/yaml
- `frontend/lib/schema-to-yaml.ts` — YAML serializer, `formStateToYaml()` function
- `frontend/components/k8s/detail/MetadataSection.tsx:98-99` — read-only annotation display (built-in resources)
- `backend/internal/k8s/resources/crd_handler.go:230` — `HandleUpdateCRDInstance` (PUT handler)
- Kubernetes annotation format: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
- Cilium LB IPAM annotations: https://docs.cilium.io/en/stable/network/lb-ipam/
