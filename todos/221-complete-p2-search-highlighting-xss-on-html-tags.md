---
status: complete
priority: p2
issue_id: "221"
tags: [code-review, xss, security, frontend, logviewer]
dependencies: []
---

# Search Highlighting XSS on HTML Tags

## Problem Statement

LogViewer search highlighting applies regex replace on the HTML output of `ansi_to_html()`. Searching for "class" or "span" injects `<mark>` tags inside HTML attributes, producing broken/malformed HTML. This can lead to XSS or at minimum a broken UI when users search for terms that match HTML tag/attribute names.

**Location:** `frontend/islands/LogViewer.tsx` lines 278-289

## Proposed Solutions

### Option A: Apply search on raw text before ANSI conversion
- Apply the search highlight markers on the raw text (before ANSI-to-HTML conversion), then convert ANSI codes to HTML while preserving the markers.
- **Effort:** Medium — requires reworking the highlight pipeline order.
- **Risk:** Low — cleaner separation of concerns.

### Option B: Skip HTML tag segments during regex replace
- Use a regex that skips content inside `<...>` when applying `<mark>` wrapping. For example, split the string by HTML tags and only apply highlighting to text segments.
- **Effort:** Low-Medium — can be done with a split/rejoin approach.
- **Risk:** Low — straightforward string manipulation.

## Acceptance Criteria

- [ ] Searching for "class", "span", "div", or any HTML tag/attribute name does not produce malformed HTML
- [ ] Search highlighting still works correctly for normal log text
- [ ] ANSI-colored log lines are still rendered correctly with highlights
- [ ] No XSS possible via crafted search terms

## Work Log

- 2026-03-22: Created from Phase 4A code review.
