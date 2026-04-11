---
status: pending
priority: p2
issue_id: "292"
tags: [code-review, scanning, ux, pr-167]
dependencies: []
---

# Reorder scanner-availability vs. RBAC check in HandleVulnerabilityDetail

## Problem Statement

In `HandleVulnerabilityDetail`, the RBAC check (`canAccessTrivy`) runs before the Trivy-availability check. A user without Trivy CRD RBAC in a cluster where Trivy isn't installed at all gets a 403 "access denied" message when the real problem is "scanner not installed."

**Why it matters:** Confusing error messages mislead users — they'll open a ticket about missing permissions when the real fix is installing Trivy Operator.

## Findings

### Pattern Recognition Reviewer

**File:** `backend/internal/scanning/handler.go:287-299`

```go
// RBAC: require Trivy access. Kubescape CVE-level detail is not supported.
if !h.canAccessTrivy(r.Context(), user, namespace) {
    httputil.WriteError(w, http.StatusForbidden,
        fmt.Sprintf("access denied to vulnerability reports in namespace %q", namespace), "")
    return
}

// Check scanner status. If only Kubescape is available, return a helpful message.
status := h.Discoverer.Status()
if status.Trivy == nil || !status.Trivy.Available {
    httputil.WriteError(w, http.StatusNotImplemented,
        "CVE-level detail requires Trivy Operator; Kubescape summaries only expose severity totals", "")
    return
}
```

Ordering matters: RBAC runs first and returns 403 for users who have no Trivy CRD RBAC (because Trivy isn't installed, so nobody has that RBAC). They see "access denied" instead of "scanner not installed."

## Proposed Solutions

### Option A: Swap the check order (Recommended)

```go
// Check scanner status first — if Trivy isn't installed, RBAC is irrelevant.
status := h.Discoverer.Status()
if status.Trivy == nil || !status.Trivy.Available {
    httputil.WriteError(w, http.StatusNotImplemented,
        "CVE-level detail requires Trivy Operator", "")
    return
}

// RBAC: require Trivy access.
if !h.canAccessTrivy(r.Context(), user, namespace) {
    httputil.WriteError(w, http.StatusForbidden, ..., "")
    return
}
```

**Pros:** Correct error for the common case; trivial change
**Cons:** None
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/handler.go:287-299`

## Acceptance Criteria

- [ ] Scanner-availability check runs before RBAC check
- [ ] Error message is accurate when Trivy is not installed
- [ ] Existing tests still pass

## Work Log

<!-- Dated record of actions taken -->

## Resources

- PR #167
- Plan: `plans/security-vulnerability-detail-view.md`
