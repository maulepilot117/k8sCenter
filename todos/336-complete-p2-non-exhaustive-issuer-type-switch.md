---
name: Add exhaustiveness check to IssuerType switch in fetchPreview
status: complete
priority: p2
issue_id: 336
tags: [code-review, frontend, type-safety, pr-181]
dependencies: []
---

## Problem Statement

`frontend/islands/IssuerWizard.tsx:155-172` switches on `f.type` for selfSigned/acme. No `default` arm, no exhaustiveness guard. `f.type` is `IssuerType | ""`. The empty string is unreachable here by construction, but TypeScript can't prove it. When a third `IssuerType` is added (e.g., CA/Vault returning, or a new backend), the switch silently emits a payload missing the type-discriminant block, and the bug surfaces only at apply time.

## Findings

- `IssuerWizard.tsx:155-172`
- Reviewer: kieran-typescript-reviewer (P2)

## Proposed Solutions

### Option A — exhaustive default with `never` (recommended)
```ts
if (!f.type) return; // narrow out ""
switch (f.type) {
  case "selfSigned": ...; break;
  case "acme":       ...; break;
  default: {
    const _exhaustive: never = f.type;
    throw new Error(`unsupported issuer type: ${_exhaustive}`);
  }
}
```

### Option B — typed record map
Replace switch with a `Record<IssuerType, (form) => payload>` map. Compiler enforces full coverage by construction.

## Acceptance Criteria
- [ ] Adding a new `IssuerType` member without updating fetchPreview produces a TS compile error.
- [ ] `deno task check` still passes on this PR's files.

## Work Log
- 2026-04-15: Filed from PR #181 review.
