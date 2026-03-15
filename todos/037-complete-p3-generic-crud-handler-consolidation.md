---
status: complete
resolution: "Deferred — large refactor, tracked for a future PR. Current handler duplication is acceptable for MVP."
priority: p3
issue_id: "037"
tags: [code-review, architecture, duplication]
dependencies: []
---

# Generic CRUD Handler Consolidation

## Problem Statement

~1000 lines of nearly identical CRUD boilerplate across 8 handler files (configmaps, services, ingresses, daemonsets, networkpolicies, pvcs, jobs, cronjobs). Each file follows the same List/Get/Create/Update/Delete pattern, differing only in type names and API accessors. Shotgun surgery risk — cross-cutting changes require modifying 8+ files.

## Findings

- 8 handler files contain structurally identical CRUD logic
- Only the type names, API group accessors, and GVR differ between files
- Any cross-cutting concern (error handling, logging, pagination) must be updated in all 8 files
- Deployments (scale/rollback/restart), nodes (cordon/drain), secrets (masking/reveal), and namespaces have unique logic and should remain standalone

## Proposed Solutions

Create a generic ResourceDef[T] table and 5 generic handler functions (List, Get, Create, Update, Delete). Standard CRUD resources are defined as table entries with their GVR, lister, and API accessor. Keep specialized handlers (deployments, nodes, secrets, namespaces) as standalone files. Estimated ~860 LOC reduction.

## Recommended Action


## Technical Details

- Affected files: 8 handler files (configmaps.go, services.go, ingresses.go, daemonsets.go, networkpolicies.go, pvcs.go, jobs.go, cronjobs.go) consolidated into 1 generic.go + resource table
- Effort: Large (follow-up PR)

## Acceptance Criteria

- Standard CRUD resources defined as table entries
- Adding a new resource type requires <15 lines of code
- Specialized handlers (deployments, nodes, secrets, namespaces) remain standalone
- All existing tests pass without modification

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-12 | Created | From PR #3 code review |

## Resources

- PR: #3
