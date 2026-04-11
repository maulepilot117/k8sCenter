---
status: pending
priority: p3
issue_id: "305"
tags: [code-review, validation, dry, architecture, pre-existing]
dependencies: []
---

# Consolidate duplicate Kubernetes name/namespace regexes across packages

## Problem Statement

At least 6 packages define semantically identical regexes for DNS-1123 labels (namespaces) and DNS-1123 subdomains (resource names). PR #167 adds the 3rd copy of each.

**Why it matters:** Inconsistency risk — one package tightens its regex, another doesn't, and behavior diverges. Also a maintenance burden.

**Scope:** Pre-existing. PR #167 extends rather than causes.

## Findings

### Pattern Recognition Reviewer

**Duplicated DNS-1123 label regex** (namespaces, service names):
- `backend/internal/scanning/handler.go:46` — `validNamespace`
- `backend/internal/server/response.go:15` — `dnsLabelRegex`
- `backend/internal/velero/handler.go:33` — `dnsLabelRegex`
- `backend/internal/wizard/container.go:13` — `dnsLabelRegex`
- `backend/internal/notification/types.go:33` — `k8sNameRegex`
- `backend/internal/alerting/rules.go:29` — variant

**Duplicated DNS-1123 subdomain regex** (resource names up to 253 chars):
- `backend/internal/k8s/resources/handler.go:233` — `k8sNameRegexp`
- `backend/internal/storage/handler.go:29` — `k8sNameRegexp`
- `backend/internal/scanning/handler.go:259` — `validWorkloadName` (NEW in PR #167)

## Proposed Solutions

### Option A: Create `internal/validate` package (Recommended)

```go
// internal/validate/k8s.go
package validate

import "regexp"

var (
    dnsLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)
    dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)
    kindName     = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{1,62}$`)
)

func IsNamespace(s string) bool    { return dnsLabel.MatchString(s) }
func IsResourceName(s string) bool { return dnsSubdomain.MatchString(s) }
func IsKind(s string) bool         { return kindName.MatchString(s) }
```

Migrate all 9 callsites.

**Pros:** Single source of truth; easier to keep in sync with Kubernetes upstream changes
**Cons:** Cross-cutting refactor
**Effort:** Medium
**Risk:** Low (regex semantics preserved)

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:** All listed above + new `internal/validate` package.

## Acceptance Criteria

- [ ] Single `internal/validate` package owns the regexes
- [ ] All 9 callsites migrated
- [ ] All tests still pass

## Work Log

### 2026-04-11 — PR #167 partial mitigation

PR #167 originally added two new copies of DNS/subdomain regexes (`validNamespace`, `validWorkloadName`). After applying the fix for todo #293 (use shared `resources.ValidateURLParams` middleware), `validWorkloadName` was removed entirely. The only new regex remaining is `validWorkloadKind` (Kubernetes kind name — PascalCase identifier), which is genuinely unique to the scanning package and not a duplicate of any existing regex.

**Net effect of PR #167:** zero new duplicate regexes. The pre-existing duplication across 7 packages remains for a future `internal/validate` package refactor.

## Resources

- PR #167 (adds 2 new copies)
- [Kubernetes names and IDs](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)
