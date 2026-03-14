---
status: pending
priority: p2
issue_id: "158"
tags: [code-review, architecture, step-10]
dependencies: ["151"]
---

# CRD Existence Check Permanently Cached — No TTL

## Problem Statement
Both `storage/handler.go` (snapshot CRDs) and `networking/detect.go` (CNI detection) cache CRD existence permanently. If VolumeSnapshot CRDs or CNI CRDs are installed after KubeCenter starts, the features will not be detected until pod restart. The monitoring package handles this correctly with periodic re-checks every 5 minutes.

## Findings
- **Agents**: architecture-strategist (P2), data-integrity-guardian (P3-FINDING-11), security-sentinel (LOW-03), performance-oracle (Finding 4)
- **Location**: `backend/internal/storage/handler.go:162-184`, `backend/internal/networking/detect.go:96`

## Proposed Solutions
Replace permanent cache with time-bounded re-check (e.g., every 5 minutes), consistent with the monitoring discovery pattern.

- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Snapshot CRD check has TTL-based re-check
- [ ] CNI detection has periodic re-check or TTL

## Work Log
- 2026-03-14: Identified by 4 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
