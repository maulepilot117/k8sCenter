---
status: pending
priority: p3
issue_id: "126"
tags: [code-review, testing, step-7]
dependencies: []
---

# No httptest Integration Tests for YAML Handlers

## Problem Statement

`yaml_test.go` covers security, parser, and export logic (21 tests) but has zero httptest integration tests for `HandleValidate`, `HandleApply`, `HandleDiff`, and `HandleExport`. The resources package sets the precedent with 19 handler-level tests.

## Findings

- **File:** `backend/internal/yaml/yaml_test.go`
- Missing handler-level coverage for: body size limit enforcement, security rejection responses, auth requirement verification, and error response formatting.
- The resources package (`backend/internal/k8s/resources/resources_test.go`) demonstrates the expected httptest pattern with 19 tests.

## Recommendation

Add httptest integration tests covering body size limits, security rejection (e.g., privileged containers, host namespaces), authentication requirements, and proper error response codes/formats for each handler.
