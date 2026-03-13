---
status: pending
priority: p2
issue_id: "115"
tags: [code-review, security, step-7]
dependencies: []
---

# Export Route Missing URL Parameter Validation

## Problem Statement

Resource routes use `ValidateURLParams` middleware for RFC 1123 validation on kind, namespace, and name parameters. The YAML export route `GET /yaml/export/{kind}/{namespace}/{name}` accepts the same URL params but is registered without this validation middleware.

## Findings

- File: `backend/internal/server/routes.go` lines 70-76 (YAML route group registration)
- File: `backend/internal/yaml/handler.go` line 198 (export handler)
- Resource routes apply `ValidateURLParams` but the export route in the YAML group does not

## Recommendation

Add `ValidateURLParams` middleware to the export route, or apply it to the entire YAML route group if all routes benefit from it.
