---
status: pending
priority: p2
issue_id: "157"
tags: [code-review, security, audit, step-10]
dependencies: ["149"]
---

# Audit Log for CNI Config: Missing Changed Keys and Hardcoded Namespace

## Problem Statement
The audit log for CNI config updates (1) does not record which keys were changed or their previous values, and (2) hardcodes `ResourceNamespace: "kube-system"` even though the ConfigMap may be in the `cilium` namespace. This hampers forensic investigation of CNI misconfigurations.

## Findings
- **Agents**: security-sentinel (MEDIUM-02), data-integrity-guardian (P2-FINDING-9, P2-FINDING-4)
- **Location**: `backend/internal/networking/handler.go:133-158`

## Proposed Solutions
- Include changed key names in the audit entry's `Detail` field
- Have `UpdateCiliumConfig` return the actual namespace used
- Record previous values of changed keys for rollback capability

- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Audit entry includes changed key names
- [ ] Audit entry uses actual namespace (not hardcoded)
- [ ] Previous values recorded for potential rollback

## Work Log
- 2026-03-14: Identified by 2 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
