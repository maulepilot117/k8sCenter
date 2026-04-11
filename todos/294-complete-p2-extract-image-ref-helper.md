---
status: pending
priority: p2
issue_id: "294"
tags: [code-review, scanning, dry, pr-167]
dependencies: []
---

# Extract shared image reference helper in trivy.go

## Problem Statement

`ListTrivyVulnSummaries` and `GetTrivyWorkloadVulnDetails` both rebuild the container image reference from `report.artifact.repository` + `report.artifact.tag`, with identical fallback logic to the container name. This is pure duplication that should be a shared helper.

**Why it matters:** DRY violation; future changes (e.g., adding digest support) require editing two places.

## Findings

### Code Simplicity + Pattern Recognition Reviewers

**File:** `backend/internal/scanning/trivy.go`

**Summary path (existing, lines 62-72):**
```go
repo, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "repository")
tag, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "tag")
image := repo
if tag != "" {
    image = repo + ":" + tag
}
if image == "" {
    image = container
}
```

**Detail path (new, lines 228-236):** Identical 10 lines.

## Proposed Solutions

### Option A: Extract helper (Recommended)

```go
// imageRefFromArtifact builds a container image reference from a Trivy report's
// artifact fields, falling back to the container name when repository is empty.
func imageRefFromArtifact(obj map[string]interface{}, container string) string {
    repo, _, _ := unstructured.NestedString(obj, "report", "artifact", "repository")
    tag, _, _ := unstructured.NestedString(obj, "report", "artifact", "tag")
    if repo == "" {
        return container
    }
    if tag == "" {
        return repo
    }
    return repo + ":" + tag
}
```

Use in both `ListTrivyVulnSummaries` and `GetTrivyWorkloadVulnDetails`.

**Pros:** Removes duplication; easier to extend; clearer intent
**Cons:** None
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy.go:62-72` (summary path)
- `backend/internal/scanning/trivy.go:228-236` (detail path)

## Acceptance Criteria

- [ ] Helper function extracted and used in both places
- [ ] Existing tests still pass
- [ ] No behavior change

## Work Log

<!-- Dated record -->

## Resources

- PR #167
