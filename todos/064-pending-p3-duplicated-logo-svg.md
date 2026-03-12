---
status: pending
priority: p3
issue_id: "064"
tags: [code-review, frontend, duplication]
dependencies: []
---

# Duplicated Logo SVG Between Sidebar and Login Page

## Problem Statement
The KubeCenter logo SVG is copy-pasted verbatim in Sidebar.tsx (lines 23-37) and login.tsx (lines 10-23), differing only in size (24 vs 48).

## Findings
- `frontend/islands/Sidebar.tsx:23-37` — Logo SVG with size 24
- `frontend/routes/login.tsx:10-23` — Same SVG with size 48

Flagged by: Pattern Recognition Specialist

## Proposed Solutions

### Option A: Extract to `components/ui/Logo.tsx` with size prop
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Single Logo component with configurable size
- [ ] Both Sidebar and login page use the shared component
