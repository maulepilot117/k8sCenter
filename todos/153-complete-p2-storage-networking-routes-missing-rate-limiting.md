---
status: pending
priority: p2
issue_id: "153"
tags: [code-review, security, step-10]
dependencies: []
---

# Storage and Networking Routes Missing Rate Limiting

## Problem Statement
`registerStorageRoutes` and `registerNetworkingRoutes` do not apply any rate limiter middleware, unlike YAML, wizard, and monitoring routes. The `PUT /cni/config` endpoint and `GET /cni?refresh=true` are particularly concerning — they trigger expensive Kubernetes API operations without throttling.

## Findings
- **Agents**: security-sentinel (HIGH-02), architecture-strategist (P2), pattern-recognition-specialist (P2)
- **Location**: `backend/internal/server/routes.go:147-165`

## Proposed Solutions
Add `middleware.RateLimit(yamlRL)` to both route groups, matching the pattern used by monitoring/YAML/wizard routes. Consider a stricter limit for the `PUT /cni/config` endpoint.

- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Storage routes have rate limiting middleware
- [ ] Networking routes have rate limiting middleware

## Work Log
- 2026-03-14: Identified by 3 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
