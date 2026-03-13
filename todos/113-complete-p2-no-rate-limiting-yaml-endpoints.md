---
status: pending
priority: p2
issue_id: "113"
tags: [code-review, security, step-7]
dependencies: []
---

# No Rate Limiting on YAML Endpoints

## Problem Statement

YAML endpoints (validate/apply/diff) accept 2MB bodies with up to 100 documents each, generating many k8s API calls per request. No rate limiting is applied to these endpoints, allowing a single client to overwhelm the API server.

## Findings

- File: `backend/internal/server/routes.go` lines 70-76
- The YAML route group does not include rate limiting middleware
- A single apply request with 100 documents generates 100+ API server calls (discovery + apply each)

## Recommendation

Add rate limiting middleware to the YAML route group in `registerYAMLRoutes`. Consider a lower limit than auth endpoints (e.g., 10 req/min) given the amplification factor per request.
