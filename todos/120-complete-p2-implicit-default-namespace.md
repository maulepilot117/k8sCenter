---
status: pending
priority: p2
issue_id: "120"
tags: [code-review, data-integrity, step-7]
dependencies: []
---

# Implicit Default Namespace When Metadata Omits Namespace

## Problem Statement

When namespaced resources omit `metadata.namespace`, the applier and differ silently default to `"default"` namespace. A user may have "production" selected in the namespace selector but resources land in "default" with no warning, creating a data integrity risk.

## Findings

- File: `backend/internal/yaml/applier.go` lines 96-98
- File: `backend/internal/yaml/differ.go` lines 87-89
- Both silently substitute `"default"` when namespace is empty

## Recommendation

Either reject resources that omit `metadata.namespace` with a clear validation error, or accept an optional namespace parameter from the frontend (the selected namespace) and use it as the fallback instead of hardcoding `"default"`.
