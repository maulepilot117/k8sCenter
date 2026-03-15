---
status: complete
priority: p3
issue_id: "144"
tags: [code-review, frontend, duplication]
dependencies: []
---

# Duplicated Label/Selector Key-Value Editor Across Components

## Problem Statement
The key-value pair editing UI (add/remove/update rows + identical X-icon SVG) is copy-pasted across DeploymentBasicsStep, ServiceBasicsStep, and ServicePortsStep (~80 lines duplicated). Extract a shared `KeyValueListEditor` component.

## Work Log
- 2026-03-13: Created from PR #14 code review (code-simplicity-reviewer, pattern-recognition-specialist)
