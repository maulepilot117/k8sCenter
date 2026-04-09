# Step 33: Diff View — Compare GitOps Revisions

## Overview

Add compare links to the GitOps revision history table. Each row links to GitHub's compare view showing what changed since the previous deployment. Frontend-only — zero backend changes.

## Problem Statement

Users see revision SHAs but have no quick way to see what changed between deployments. They must manually navigate to GitHub and construct compare URLs. This feature automates that with a single link per history row.

## Scope

**In scope:**
- Compare link per history row opening `{repoURL}/compare/{prevSha}...{thisSha}` in new tab
- Argo CD applications with HTTP(S) repo URLs only
- Full SHAs in URL (no truncation)

**Out of scope:**
- In-app diff viewer or change summary panel (GitHub's compare UI is better than anything we'd build)
- Rendered manifest diff (would require Argo CD gRPC API, which k8sCenter doesn't use)
- GitLab/Bitbucket compare URLs (future, trivial to add)
- Flux Kustomization comparison (no history)

## Implementation

**Files to modify (1):**
- `frontend/islands/GitOpsAppDetail.tsx`

**Changes (~15 LOC):**

1. Compute `canCompare` once from `app.source.repoURL`:
```tsx
const canCompare = repoURL && /^https?:\/\//i.test(repoURL);
const compareBase = canCompare ? repoURL!.replace(/\.git$/, "") : null;
```

2. In the history table row, add a compare link after the SHA:
```tsx
const prevRevision = i < history.length - 1 ? history[i + 1].revision : null;
// ...
{canCompare && prevRevision && (
  <a href={`${compareBase}/compare/${prevRevision}...${h.revision}`}
     target="_blank" rel="noopener noreferrer"
     class="text-xs text-brand hover:underline ml-2">
    compare
  </a>
)}
```

**Assumption:** History is ordered most-recent-first, so `history[i+1]` is the previous deployment. Verify in `extractArgoHistory`.

## Success Criteria

- [ ] Each history row (except oldest) shows a "compare" link
- [ ] Clicking opens GitHub's compare view in a new tab
- [ ] Link absent for non-HTTP repo URLs and oldest entry
- [ ] `deno lint` and `deno fmt --check` pass

## References

- `frontend/islands/GitOpsAppDetail.tsx:462-579` — revision history table
- `backend/internal/gitops/argocd.go:197-220` — `extractArgoHistory` (verify sort order)
- [GitHub Compare URL format](https://docs.github.com/en/pull-requests/committing-changes-to-your-project/viewing-and-comparing-commits/comparing-commits)
