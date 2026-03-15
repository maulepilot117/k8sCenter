---
status: complete
priority: p3
issue_id: "182"
tags: [code-review, ux, frontend, step-11]
dependencies: []
---

# JSON/YAML Mismatch in Alert Rules Editor

## Problem Statement
When creating a new rule, the editor shows a YAML template (`DEFAULT_RULE_YAML`). When editing an existing rule, the content is shown as `JSON.stringify` output. This is inconsistent and confusing.

## Proposed Solutions
Use YAML consistently: serialize existing rules to YAML when loading for edit, or use the Monaco YAML editor from Step 7.
- **Effort**: Medium

## Resources
- PR: #17
- File: `frontend/islands/AlertRulesPage.tsx:72-73`
