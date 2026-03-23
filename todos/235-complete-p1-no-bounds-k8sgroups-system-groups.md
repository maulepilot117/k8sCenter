---
status: pending
priority: p1
issue_id: 235
tags: [security, code-review, phase4b]
---

# No bounds on k8sGroups array + dangerous system: groups not blocked

## Problem Statement

The k8sGroups validation only blocks `system:masters`, but other `system:` prefixed groups (e.g. `system:nodes`, `system:kube-controller-manager`) could grant unintended access via impersonation. There is also no limit on the number of groups or individual group string length, allowing unbounded input.

## Findings

- Only `system:masters` is explicitly blocked; other dangerous `system:` prefixed groups pass validation.
- Groups like `system:nodes`, `system:kube-controller-manager`, and `system:kube-scheduler` could grant significant cluster access when used for impersonation.
- No maximum length enforced on individual group strings.
- No maximum count enforced on the groups array.

## Technical Details

- **Affected file:** `backend/internal/server/handle_users.go`, lines 74-86

## Acceptance Criteria

- [ ] Block all `system:` prefixed groups except `system:authenticated` (and potentially `system:unauthenticated` if there is a use case)
- [ ] Cap the k8sGroups array at 20 entries maximum
- [ ] Cap individual group string length at 253 characters (Kubernetes name limit)
- [ ] Return a 400 response with a clear error message identifying the rejected group
- [ ] Add unit tests covering blocked system groups, array length cap, and string length cap
