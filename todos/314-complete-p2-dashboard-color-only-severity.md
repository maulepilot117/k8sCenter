---
status: pending
priority: p2
issue_id: "314"
tags: [code-review, a11y, pr-167]
dependencies: []
---

# Dashboard severity cells use color alone — colorblind users can't distinguish

## Problem Statement

`SeverityCell` in `VulnerabilityDashboard` renders severity counts as plain digits where color is the only differentiator. Colorblind users (especially red/green deficient) see identical-looking numbers across all severity columns. The column headers help somewhat, but the cell content itself has no icon, weight, or shape difference.

The detail page does this better — it uses `CVESeverityBadge` with text labels. The dashboard should match.

**Why it matters:** WCAG 2.x requires color not to be the sole means of conveying information.

## Findings

### UX Reviewer — friction (a11y)

**File:** `frontend/islands/VulnerabilityDashboard.tsx:370-379`
```tsx
function SeverityCell({ count, severity }: { count: number; severity: string }) {
  const color = count > 0 ? SEVERITY_COLORS[severity] : "var(--text-muted)";
  return (
    <td class="px-3 py-2 text-center text-xs font-medium" style={{ color }}>
      {count}
    </td>
  );
}
```

## Proposed Solutions

### Option A: Add weight/opacity difference beyond color

When count > 0, also apply `font-bold`. When count === 0, reduce opacity. This makes "has issues" scannable without relying on color.

**Pros:** Minimal change; preserves compact layout
**Cons:** Still subtle
**Effort:** Trivial
**Risk:** None

### Option B: Colored backgrounds with sufficient contrast

Wrap the count in a colored pill with text-on-color contrast, similar to `SeverityCount` used in the summary area.

**Pros:** Matches detail page style
**Cons:** Takes more horizontal space; changes visual density
**Effort:** Small
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDashboard.tsx:370-379`

## Acceptance Criteria

- [ ] Severity cells distinguishable without color (weight, shape, or background)
- [ ] Visual density preserved or improved

## Work Log

## Resources

- PR #167
- [WCAG 2.1 SC 1.4.1 Use of Color](https://www.w3.org/WAI/WCAG21/Understanding/use-of-color)
