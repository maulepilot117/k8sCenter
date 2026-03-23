---
status: complete
priority: p2
issue_id: 238
tags: [security, audit, code-review, phase4b]
---

# Audit log for user creation missing k8sUsername and k8sGroups

## Problem Statement

The audit entry for user creation only logs ResourceKind and ResourceName. It does not capture k8sUsername or k8sGroups, which are the security-critical fields determining Kubernetes impersonation identity. This makes it impossible to trace what impersonation identity was granted to a user from the audit log alone.

## Findings

The audit log entry created after a new user is added records the resource kind ("user") and resource name (the username), but omits the k8sUsername and k8sGroups fields. These fields control which Kubernetes identity the user will impersonate, making them essential for security auditing.

## Technical Details

- **File:** `backend/internal/server/handle_users.go`, lines 114-117
- The `newAuditEntry` call sets `ResourceKind` and `ResourceName` but does not populate `entry.Detail` with the k8s identity fields.

## Acceptance Criteria

- [ ] Audit entry for user creation includes `entry.Detail` with JSON containing `k8sUsername` and `k8sGroups`
- [ ] Existing audit log consumers (log output, future persistence) receive the additional detail without breaking changes
- [ ] Unit test verifies the audit entry detail contains the k8s identity fields
