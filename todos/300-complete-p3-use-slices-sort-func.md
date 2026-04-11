---
status: pending
priority: p3
issue_id: "300"
tags: [code-review, scanning, performance, pr-167]
dependencies: []
---

# Replace sort.SliceStable with slices.SortFunc in CVE sort

## Problem Statement

`GetTrivyWorkloadVulnDetails` uses `sort.SliceStable` which uses reflection-based dispatch. For 600 CVEs × 5 images, this adds measurable (though small) overhead. Generic `slices.SortFunc` is faster. Additionally, stability isn't needed because the comparator is a total order (falls through to CVE ID).

**Why it matters:** Small perf win, cleaner code.

## Findings

### Performance Oracle

**File:** `backend/internal/scanning/trivy.go:259-276`

```go
sort.SliceStable(cves, func(i, j int) bool {
    si, sj := severityRank(cves[i].Severity), severityRank(cves[j].Severity)
    // ...
})
```

Also, CVSSScore being `*float64` causes dereference-on-every-compare. Consider denormalizing to `float64` with 0 sentinel during extraction.

## Proposed Solutions

### Option A: slices.SortFunc + value comparator (Recommended)

```go
import "slices"

slices.SortFunc(cves, func(a, b CVEDetail) int {
    if n := cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)); n != 0 {
        return n
    }
    ai := float64(0)
    if a.CVSSScore != nil { ai = *a.CVSSScore }
    bi := float64(0)
    if b.CVSSScore != nil { bi = *b.CVSSScore }
    if n := cmp.Compare(bi, ai); n != 0 { // descending
        return n
    }
    return cmp.Compare(a.ID, b.ID)
})
```

**Pros:** No reflection; generic/typed; slightly faster
**Cons:** Requires Go 1.21+ (already satisfied per go.mod)
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy.go:259-276`

## Acceptance Criteria

- [ ] Sort uses `slices.SortFunc`
- [ ] Existing tests still pass

## Work Log

## Resources

- PR #167
