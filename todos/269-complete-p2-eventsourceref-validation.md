---
status: pending
priority: p2
issue_id: "269"
tags: [code-review, security, backend, validation]
dependencies: []
---

# EventSourceRef Fields and Arrays Lack Validation Bounds

## Problem Statement

The `EventSources` (Alert) and `Resources` (Receiver) arrays have a minimum (>=1) but no maximum bound. The `EventSourceRef` fields (Kind, Name, MatchLabels) are passed through to the CRD spec without validation. While the 8KB MaxBytesReader provides a coarse limit, application-level validation is missing.

## Findings

**Identified by:** Security Sentinel

**Evidence:**
- `flux_notifications.go:360-413` (ValidateAlertInput, ValidateReceiverInput)
- `flux_notifications.go:147-168` (buildEventSourcesSpec)
- Kind field: no validation against known Flux kinds
- Name field: accepts any string (no k8s name regex, though `*` wildcard is valid)
- MatchLabels: unbounded map size
- EventSources/Resources arrays: no max count
- InclusionList/ExclusionList: no count limit

## Proposed Solutions

### Option A: Add validation bounds (Recommended)
- Max 50 entries for EventSources/Resources arrays
- Validate Kind against known Flux kinds (Kustomization, HelmRelease, GitRepository, etc.)
- Validate Name matches k8s name regex OR is exactly `*`
- Max 20 MatchLabels entries
- Max 50 entries for InclusionList/ExclusionList
- **Effort:** Small
- **Risk:** Low (could reject valid edge cases, but limits are generous)

## Acceptance Criteria

- [ ] EventSources/Resources arrays capped at 50 entries
- [ ] EventSourceRef.Kind validated against known Flux kinds
- [ ] EventSourceRef.Name validated (k8s name regex or `*`)
- [ ] MatchLabels capped at 20 entries
- [ ] Tests updated

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Defense-in-depth, K8s API would reject truly malformed specs |

## Resources

- PR: #153
- File: backend/internal/notification/flux_notifications.go
