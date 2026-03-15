---
status: pending
priority: p2
issue_id: "176"
tags: [code-review, reliability, step-11]
dependencies: []
---

# sync.Once CRD Check Has No Retry — Permanently False If CRD Installed Later

## Problem Statement
`Available()` uses `sync.Once` to check if PrometheusRule CRD exists. If the CRD is not installed at startup (e.g., Prometheus Operator installed after KubeCenter), `Available()` permanently returns `false` with no way to recover.

## Findings
- **Source**: Performance review (Finding 9)
- **Location**: `backend/internal/alerting/rules.go:53-69`

## Proposed Solutions
### Option A: Re-check periodically until available
Replace `sync.Once` with an `atomic.Bool` + periodic re-check (e.g., every 5 minutes until available, then stop).
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
