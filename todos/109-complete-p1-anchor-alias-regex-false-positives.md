---
status: pending
priority: p1
issue_id: "109"
tags: [code-review, security, step-7]
dependencies: []
---

# Anchor/Alias Regex Produces False Positives on Valid YAML

## Problem Statement

The anchor/alias regex `(?m)^\s*[^#]*[&*][a-zA-Z_][a-zA-Z0-9_]*` matches `&` or `*` in any non-comment position, causing false positives on legitimate YAML values such as `url: "https://example.com?foo&bar"`, `command: "echo *glob"`, and `description: "R&D_team"`. This rejects valid Kubernetes manifests.

## Findings

- **File**: `backend/internal/yaml/security.go` line 37 — overly broad regex pattern for anchor/alias detection.

## Recommendation

Either use a more precise regex that accounts for YAML quoting context and anchor/alias syntax (anchors use `&name` at definition sites, aliases use `*name` as standalone values), or remove the regex check and rely solely on the post-parse expansion ratio check to detect YAML bomb attacks.
