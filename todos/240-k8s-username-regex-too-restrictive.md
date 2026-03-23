---
status: complete
priority: p2
issue_id: 240
tags: [validation, code-review, phase4b]
---

# validUsername regex too restrictive for k8sUsername

## Problem Statement

The `k8sUsername` field is validated with the same `validUsername` regex (`^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$`) used for the local login username. This regex rejects colons, which are valid and common in Kubernetes usernames (e.g., OIDC subjects like `oidc:jane.doe`). The `system:` prefix is already explicitly guarded separately.

## Findings

Kubernetes usernames can contain colons, slashes, and plus signs. OIDC providers commonly produce subject identifiers with colons (e.g., `oidc:jane.doe`, `https://accounts.google.com/12345`). The current regex prevents administrators from mapping local users to these valid k8s identities.

## Technical Details

- **File:** `backend/internal/server/handle_users.go`, line 67
- `validUsername` regex: `^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$`
- This regex is applied to both the local `username` and `k8sUsername` fields
- The `system:` prefix guard is a separate check and does not address the character restriction

## Acceptance Criteria

- [ ] A separate, more permissive regex is used for `k8sUsername` validation (allowing colons, slashes, `+`)
- [ ] The `system:` prefix guard remains in place
- [ ] The local `username` regex remains unchanged (it controls login identifiers)
- [ ] Unit tests verify that k8s usernames with colons (e.g., `oidc:jane.doe`) and slashes are accepted
- [ ] Unit tests verify that `system:` prefixed k8s usernames are still rejected
