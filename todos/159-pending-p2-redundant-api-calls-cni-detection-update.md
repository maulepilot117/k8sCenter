---
status: pending
priority: p2
issue_id: "159"
tags: [code-review, performance, step-10]
dependencies: []
---

# Redundant API Calls in CNI Detection and Config Update

## Problem Statement
Two performance issues with CNI API call patterns:
1. `HandleUpdateCNIConfig` makes 5+ Kubernetes API calls (Get+Update ConfigMap, full re-detection, another ConfigMap read) — at least 3 ConfigMap reads for the same object.
2. `checkCRDGroup()` calls `disc.ServerGroups()` on each invocation (2-3 times per detection run) without caching the result.

## Findings
- **Agents**: performance-oracle (Findings 2, 3)
- **Location**: `backend/internal/networking/handler.go:132-170`, `backend/internal/networking/detect.go:192-207`

## Proposed Solutions
- After `UpdateCiliumConfig` succeeds, call `ReadCiliumConfig` once and run `Detect()` asynchronously
- Fetch `ServerGroups()` once at start of `detect()` and pass to CRD group checks
- Pass known namespace to `ReadCiliumConfig` instead of probing two namespaces

- **Effort**: Medium
- **Risk**: Low

## Acceptance Criteria
- [ ] Config update handler eliminates redundant ConfigMap reads
- [ ] CRD group checks reuse single ServerGroups() call

## Work Log
- 2026-03-14: Identified by performance-oracle

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
