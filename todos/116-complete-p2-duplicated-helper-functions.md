---
status: pending
priority: p2
issue_id: "116"
tags: [code-review, architecture, step-7]
dependencies: []
---

# Duplicated Helper Functions Between YAML and Resources Packages

## Problem Statement

`requireUser`, `writeJSON`, `writeError`, and `writeData` are copy-pasted verbatim between `yaml/handler.go` and `resources/handler.go`, totaling approximately 31 lines of exact duplication.

## Findings

- File: `backend/internal/yaml/handler.go` lines 276-312
- File: `backend/internal/k8s/resources/handler.go` lines 54-153
- Functions duplicated: `requireUser`, `writeJSON`, `writeError`, `writeData`

## Recommendation

Extract shared HTTP helpers into a common package such as `internal/httputil` or reuse the existing helpers in `server/response.go`. Both packages should import from the shared location.
