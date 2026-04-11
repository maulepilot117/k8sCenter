---
status: pending
priority: p2
issue_id: "312"
tags: [code-review, error-handling, pattern, pr-167]
dependencies: []
---

# Use ApiError instead of ad-hoc type cast in VulnerabilityDetail

## Problem Statement

`VulnerabilityDetail.tsx:69` casts the caught error to `{ status?: number; message?: string }` instead of using the exported `ApiError` class from `lib/api.ts:46`. A thrown non-ApiError (e.g., a plain `TypeError` from `fetch` failing) would have `undefined` status and fall into the generic "Failed to load" branch — but if some other code throws an object that happens to have a `status` field, it would be silently misinterpreted as an HTTP response.

**Why it matters:** Type safety hole; masks the difference between HTTP errors and network errors.

## Findings

### Pattern Recognition Reviewer

**File:** `frontend/islands/VulnerabilityDetail.tsx:69-79`
```ts
} catch (e) {
  const err = e as { status?: number; message?: string };
  if (err.status === 501) { ... }
  else if (err.status === 403) { ... }
  else { error.value = err.message ?? "Failed to load vulnerability details"; }
}
```

**Project convention** — `frontend/lib/api.ts:46`:
```ts
export class ApiError extends Error {
  constructor(public status: number, public code: number | string, message: string) {
    super(message);
  }
}
```

## Proposed Solutions

### Option A: Import and instanceof-check ApiError (Recommended)

```ts
import { ApiError } from "@/lib/api.ts";

} catch (e) {
  if (e instanceof ApiError) {
    if (e.status === 501) {
      error.value = "CVE-level detail requires Trivy Operator...";
    } else if (e.status === 403) {
      error.value = "Access denied...";
    } else {
      error.value = e.message || "Failed to load vulnerability details";
    }
  } else {
    error.value = "Failed to load vulnerability details";
  }
}
```

**Pros:** Type-safe; distinguishes HTTP errors from network errors
**Cons:** None
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `frontend/islands/VulnerabilityDetail.tsx:69-79`

## Acceptance Criteria

- [ ] `ApiError` imported and used via `instanceof`
- [ ] Non-ApiError exceptions produce the generic message
- [ ] Existing 501 and 403 messages still displayed correctly

## Work Log

## Resources

- PR #167
- `frontend/lib/api.ts:46` (ApiError class)
