---
name: Fix ClusterIssuer success-link pointing at Issuers list
status: complete
priority: p1
issue_id: 322
tags: [code-review, frontend, bug, cert-manager, pr-180]
dependencies: []
---

## Problem Statement

After successfully creating a ClusterIssuer via the wizard, the "View Resource" link in the review step sends the user to `/security/certificates/issuers` — the **namespaced** Issuers list. The just-created resource won't appear there because it's cluster-scoped.

`frontend/islands/IssuerWizard.tsx:328` hardcodes:
```tsx
detailBasePath="/security/certificates/issuers"
```
regardless of the `scope` prop.

## Findings

- Reviewer: kieran-typescript-reviewer (P2, bumped to P1 — user-visible bug on the happy path).
- Current routes: `/security/certificates/issuers` (namespaced list), `/security/certificates/cluster-issuers/new` (ClusterIssuer wizard). No ClusterIssuer list route exists yet — the IssuersList shows both. Base path should still differ so link styling is consistent.

## Proposed Solutions

### Option A — branch on scope (recommended)
```tsx
detailBasePath={scope === "cluster"
  ? "/security/certificates/cluster-issuers"
  : "/security/certificates/issuers"}
```
Then either (a) create a `/security/certificates/cluster-issuers/index.tsx` that renders the same `IssuersList` island filtered to cluster scope, or (b) redirect `/security/certificates/cluster-issuers` to `/security/certificates/issuers` for now.

**Pros:** obvious fix. **Cons:** needs matching route. **Effort:** Small.

### Option B — point both scopes at `/security/certificates/issuers`
Accept that cluster issuers also live on the combined list. Update copy on the success screen to say "View Issuers".
**Pros:** zero route work. **Cons:** loses the semantic link to the specific resource. **Effort:** Trivial.

## Recommended Action
<!-- filled at triage -->

## Technical Details
- `frontend/islands/IssuerWizard.tsx:328`
- Possibly new route `frontend/routes/security/certificates/cluster-issuers/index.tsx`

## Acceptance Criteria
- [ ] After creating a ClusterIssuer, "View Resource" link navigates to a page where the new ClusterIssuer is visible.
- [ ] After creating a namespaced Issuer, same is true.
- [ ] E2E or manual test confirming both.

## Work Log
- 2026-04-14: Filed from PR #180 review.

## Resources
- PR #180
- Reviewer: kieran-typescript-reviewer
