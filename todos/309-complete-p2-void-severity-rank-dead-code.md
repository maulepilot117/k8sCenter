---
status: pending
priority: p2
issue_id: "309"
tags: [code-review, simplicity, dead-code, pr-167]
dependencies: []
---

# Remove `void severityRank;` dead-code suppression and unused export

## Problem Statement

`VulnerabilityDetail.tsx:343` has `void severityRank;` inside `CVERow`, with a comment claiming the function is "used as defense when rendering." Nothing calls `severityRank` — there is no `.sort()` in the component. The import exists only to suppress an unused-import lint error, and the `void` statement is lying to the type system.

Additionally, `severityRank` is exported from `scanning-types.ts:147` and has no other consumers in the codebase — the export is dead.

**Why it matters:** Misleading comments are worse than no comments. The Step 0 rule in CLAUDE.md explicitly flags dead code that accelerates context compaction.

## Findings

### Pattern Recognition + Code Simplicity Reviewers

**File:** `frontend/islands/VulnerabilityDetail.tsx:18, 343`
```ts
import {
  ...
  severityRank,
  type WorkloadVulnDetail,
} from "@/lib/scanning-types.ts";

function CVERow({ cve }: { cve: FlatCVE }) {
  // Sort order is guaranteed by the backend (severity asc → CVSS desc → ID asc),
  // so severityRank is used here only as defense when rendering.
  void severityRank;
```

**File:** `frontend/lib/scanning-types.ts:138-149` — `severityRank` + `SEVERITY_RANK` defined and exported but referenced only by the `void` suppressor.

## Proposed Solutions

### Option A: Remove both import and export (Recommended)

- Delete the import line in `VulnerabilityDetail.tsx:18`
- Delete the `void` line and misleading comment (`VulnerabilityDetail.tsx:341-343`)
- Delete `severityRank` and `SEVERITY_RANK` from `scanning-types.ts:138-149`
- If a future feature needs client-side CVE re-sorting, add it back then

**Pros:** Removes ~15 LOC of dead code; removes misleading comment
**Cons:** None
**Effort:** Trivial
**Risk:** None — function confirmed unused by grep

### Option B: Actually use severityRank defensively

Wire a real client-side sort in `allCVEs` using severityRank so the import earns its keep.

**Pros:** Honest — the comment becomes true
**Cons:** Duplicates backend work; more code to maintain
**Effort:** Small
**Risk:** Low

## Recommended Action

Option A — delete all dead references.

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx:18, 341-343`
- `frontend/lib/scanning-types.ts:138-149`

## Acceptance Criteria

- [ ] `severityRank` import removed from VulnerabilityDetail.tsx
- [ ] `void` statement and misleading comment removed
- [ ] `severityRank` and `SEVERITY_RANK` removed from scanning-types.ts
- [ ] `deno check` + `deno lint` clean

## Work Log

## Resources

- PR #167
- CLAUDE.md Step 0 rule
