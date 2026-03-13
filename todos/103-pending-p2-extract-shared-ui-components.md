---
status: pending
priority: p2
issue_id: "103"
tags: [code-review, quality, step-6]
dependencies: []
---

# Extract Shared UI Components (Field, ErrorBanner, LoadingSpinner, SectionHeader)

## Problem Statement
Several UI patterns are repeated extensively:
1. **Field label/value pattern** — 8-line div pair appears 40+ times across 14 overview files. `MetadataSection.tsx` already has a private `Field` component doing exactly this.
2. **ErrorBanner** — defined privately in `ResourceDetail.tsx`, duplicated inline in `ResourceTable.tsx`
3. **LoadingSpinner** — defined privately in `ResourceDetail.tsx`, `ResourceTable.tsx` uses text instead
4. **SectionHeader** — `<h4 class="text-xs font-medium uppercase ...">` pattern appears 50+ times

## Findings
- **Agent**: pattern-recognition-specialist, code-simplicity-reviewer (PR #6 review)
- **Location**: All `*Overview.tsx` files, `ResourceDetail.tsx:391-423`, `ResourceTable.tsx:292-295`

## Proposed Solutions

### Option A: Promote to Shared Components (Recommended)
Move `Field`, `ErrorBanner`, `LoadingSpinner`, and `SectionHeader` to `components/ui/`. Update all consumers.
- **Pros**: ~280 LOC saved from Field extraction alone, consistent styling
- **Cons**: Many file touches
- **Effort**: Medium
- **Risk**: Low

## Acceptance Criteria
- [ ] `Field`, `ErrorBanner`, `LoadingSpinner` are shared components in `components/ui/`
- [ ] All overview components use shared `Field` instead of inline divs
- [ ] `ResourceDetail` and `ResourceTable` share `ErrorBanner` and `LoadingSpinner`
