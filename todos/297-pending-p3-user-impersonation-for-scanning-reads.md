---
status: pending
priority: p3
issue_id: "297"
tags: [code-review, scanning, security, architecture, impersonation, pre-existing]
dependencies: []
---

# Switch scanning package to user impersonation for reads

## Problem Statement

CLAUDE.md states: *"All k8s API calls use user impersonation. Never use the service account's own permissions."* The scanning package (both pre-existing `HandleVulnerabilities` and the new `HandleVulnerabilityDetail`) uses `h.K8sClient.BaseDynamicClient()` — the service account — and relies on a SelfSubjectAccessReview precheck via `canAccessTrivy` for authorization.

The RBAC precheck can return stale results for up to 5 minutes (AccessChecker cache TTL at `backend/internal/k8s/resources/access.go:158`), meaning a user whose RBAC was revoked can continue reading vulnerability data for up to 5 minutes.

**Why it matters:** Violates documented core architecture principle. Weakens audit trails (all scanning reads show as service account, not end user). Creates stale-authorization window.

**Scope:** This is a pre-existing gap across scanning/policy/gitops/velero packages. PR #167 inherits but doesn't cause it. Captured here for follow-up; **not a PR #167 blocker**.

## Findings

### Architecture Strategist + Security Sentinel

**File:** `backend/internal/scanning/handler.go:305` (new code)
```go
dynClient := h.K8sClient.BaseDynamicClient()
detail, err := GetTrivyWorkloadVulnDetails(ctx, dynClient, namespace, kind, name)
```

**Compare:** `backend/internal/gitops/handler.go:310` (`HandleGetApplication`) builds an impersonating dynamic client via `h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)` and calls the API as the user.

**Pre-existing in scanning:** `HandleVulnerabilities` at `handler.go:92` has the same pattern.

## Proposed Solutions

### Option A: Switch scanning reads to DynamicClientForUser

```go
dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
if err != nil {
    httputil.WriteError(w, http.StatusInternalServerError, "failed to build impersonating client", "")
    return
}
detail, err := GetTrivyWorkloadVulnDetails(ctx, dynClient, namespace, kind, name)
```

**Pros:** Matches gitops pattern; defense-in-depth; proper audit attribution
**Cons:** Every scanning request now does two SSAR-style calls (precheck + impersonated read); caching model needs review (cache keys must include user identity or be per-user)
**Effort:** Medium (also requires rework of existing namespace cache to be per-user, or removal of cache)
**Risk:** Medium — caching model change affects `fetchVulns` path

### Option B: Document as known limitation, track for wider refactor

Add a comment in the code and a section in CLAUDE.md or an architecture doc explaining that scanning/policy/gitops use "SA + RBAC precheck" as an intentional caching optimization, with a note to revisit.

**Pros:** No code change, transparent about trade-off
**Cons:** Violates documented principle
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage. Probably Option B for now, Option A in a dedicated refactor PR. -->

## Technical Details

**Affected files (cross-package):**
- `backend/internal/scanning/handler.go` (both list and detail handlers)
- `backend/internal/policy/handler.go`
- `backend/internal/gitops/handler.go` (partially compliant already)
- `backend/internal/velero/handler.go`
- `backend/internal/k8s/resources/access.go` (AccessChecker cache)

## Acceptance Criteria

- [ ] Decision made: Option A or Option B
- [ ] If Option A: all scanning reads use impersonation; caching reworked
- [ ] If Option B: comment added to handler.go explaining the trade-off

## Work Log

### 2026-04-11 — PR #167 partial mitigation

Option B applied: added a doc comment to `scanning.Handler` type in `backend/internal/scanning/handler.go` acknowledging the gap as a known limitation:

```go
// Known limitations carried from existing scanning endpoints (tracked for
// follow-up; see todos/297 and todos/304):
//   - Reads use the service account (BaseDynamicClient) plus an SSAR precheck
//     rather than user impersonation. Stale-authorization window up to the
//     AccessChecker cache TTL (~5 min).
```

Full fix (Option A — switching scanning/policy/gitops/velero to `DynamicClientForUser`) deferred to a dedicated refactor because it requires rework of the per-namespace/per-workload cache keying and is cross-package.

## Resources

- PR #167
- CLAUDE.md — "Architecture Principles > Backend"
- Reference pattern: `backend/internal/gitops/handler.go:310`
