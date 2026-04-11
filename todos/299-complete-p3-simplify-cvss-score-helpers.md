---
status: pending
priority: p3
issue_id: "299"
tags: [code-review, scanning, simplicity, pr-167]
dependencies: []
---

# Simplify selectCVSSScore + remove dead int branches

## Problem Statement

`selectCVSSScore` and `extractV3Score` in `trivy.go` are over-factored (two helpers where one suffices) and contain dead code. JSON unmarshalled through `unstructured` always produces `float64` for numeric fields, but `extractV3Score` has int64/int branches that are never reached.

**Why it matters:** Less code to maintain and reason about.

## Findings

### Code Simplicity Reviewer

**File:** `backend/internal/scanning/trivy.go:148-188`

```go
func selectCVSSScore(cvss map[string]interface{}) *float64 {
    // ... loops over preferred vendors ...
}

func extractV3Score(cvss map[string]interface{}, vendor string) *float64 {
    vendorMap, ok := cvss[vendor].(map[string]interface{})
    if !ok { return nil }
    raw, ok := vendorMap["V3Score"]
    if !ok { return nil }
    switch v := raw.(type) {
    case float64:
        return &v
    case int64:       // DEAD - unstructured produces float64
        f := float64(v)
        return &f
    case int:         // DEAD
        f := float64(v)
        return &f
    }
    return nil
}
```

`extractV3Score` is called only from `selectCVSSScore` (single caller).

## Proposed Solutions

### Option A: Collapse into one function, drop dead branches (Recommended)

```go
func selectCVSSScore(cvss map[string]interface{}) *float64 {
    get := func(vendor string) *float64 {
        m, _ := cvss[vendor].(map[string]interface{})
        if f, ok := m["V3Score"].(float64); ok {
            return &f
        }
        return nil
    }
    for _, v := range []string{"nvd", "redhat", "ubuntu", "debian"} {
        if s := get(v); s != nil {
            return s
        }
    }
    for v := range cvss {
        if s := get(v); s != nil {
            return s
        }
    }
    return nil
}
```

**Pros:** ~25 LOC removed; reads more directly; drops untested dead code
**Cons:** Slight risk if some Trivy version stores int types (can be verified against actual CRD schema)
**Effort:** Trivial
**Risk:** Low

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy.go:148-188`

## Acceptance Criteria

- [ ] Single `selectCVSSScore` function
- [ ] Dead int/int64 branches removed
- [ ] Existing tests still pass
- [ ] Consider: sort unknown-vendor iteration for determinism

## Work Log

<!-- Dated record -->

## Resources

- PR #167
