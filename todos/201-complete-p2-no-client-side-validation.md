---
status: pending
priority: p2
issue_id: "201"
tags: [code-review, frontend, ux]
dependencies: []
---

# No Client-Side Validation Before Submit

## Problem Statement

`CiliumPolicyEditor` has no client-side validation. The submit button is only disabled when `name` is empty. Invalid ports (non-numeric), empty entity selections, and malformed CIDRs are all submitted to the backend, returning unhelpful server error messages.

## Findings

- **CiliumPolicyEditor.tsx:656-665**: `parseInt("http", 10)` returns `NaN`, serialized as `null`
- No validation for empty entity selection when peerType is "entities"
- No CIDR format check on client
- ServiceWizard.tsx has `validateStep()` — this editor has none
- Also missing `beforeunload` guard for unsaved changes
- Found by: Pattern Recognition, Security Sentinel

## Proposed Solutions

### Option A: Add inline validation before submit
Validate name format, port numbers, non-empty entities, CIDR format. Show inline errors per field. Add beforeunload guard.
- Effort: Medium
- Risk: Low

## Acceptance Criteria
- [ ] Invalid port numbers show inline error
- [ ] Empty entities selection flagged when peerType is "entities"
- [ ] CIDR format validated client-side
- [ ] beforeunload warns on unsaved changes

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `frontend/islands/CiliumPolicyEditor.tsx`
- Pattern: `frontend/islands/ServiceWizard.tsx` (validateStep)
