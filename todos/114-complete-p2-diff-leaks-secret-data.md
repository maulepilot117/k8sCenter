---
status: pending
priority: p2
issue_id: "114"
tags: [code-review, security, step-7]
dependencies: []
---

# Diff Endpoint Leaks Secret Data Unmasked

## Problem Statement

The diff endpoint returns full YAML strings for current and proposed states. When diffing a Secret, base64-encoded secret data is returned unmasked, bypassing the masking enforced by resource GET handlers.

## Findings

- File: `backend/internal/yaml/differ.go` lines 96-141
- The differ fetches the current resource via the API and returns it as-is in the diff response
- Secret `data` and `stringData` fields are not masked, unlike the resource CRUD handlers which replace values with `****`

## Recommendation

Block diff for Secret resources (as the export endpoint already does) or mask `data` and `stringData` fields in both the current and proposed YAML before returning the diff.
