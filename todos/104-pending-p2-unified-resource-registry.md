---
status: pending
priority: p2
issue_id: "104"
tags: [code-review, architecture, step-6]
dependencies: []
---

# Consolidate Parallel Resource Mappings into Unified Registry

## Problem Statement
There are 5 parallel string-keyed mappings that must stay in sync:
1. `RESOURCE_API_KINDS` (constants.ts) — kind → PascalCase API kind
2. `RESOURCE_DETAIL_PATHS` (constants.ts) — kind → URL path prefix
3. `CLUSTER_SCOPED_KINDS` (constants.ts) — Set of cluster-scoped kinds
4. `RESOURCE_COLUMNS` (resource-columns.ts) — kind → column definitions
5. `OVERVIEW_COMPONENTS` (detail/index.tsx) — kind → overview component

Adding a new resource type requires updating all 5 in sync with no compile-time check.

## Findings
- **Agent**: architecture-strategist (PR #6 review)
- **Location**: `frontend/lib/constants.ts`, `frontend/lib/resource-columns.ts`, `frontend/components/k8s/detail/index.tsx`
- Will become more painful in Step 10 (CSI/StorageClass) when new resource types are added

## Proposed Solutions

### Option A: Single RESOURCE_REGISTRY Object (Recommended)
Define a typed `RESOURCE_REGISTRY: Record<ResourceKind, ResourceConfig>` where each entry bundles `apiKind`, `detailPath`, `clusterScoped`, `columns`, and `overviewComponent`. Derive the individual maps from it.
- **Pros**: Single source of truth, type-safe, impossible to forget a field
- **Cons**: Larger refactor, changes import patterns
- **Effort**: Medium
- **Risk**: Low

## Acceptance Criteria
- [ ] All resource metadata in a single registry
- [ ] Adding a new resource type requires editing one location
- [ ] TypeScript catches missing fields at compile time
