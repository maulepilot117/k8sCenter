---
status: pending
priority: p2
issue_id: "295"
tags: [code-review, scanning, security, pr-167]
dependencies: []
---

# Use labels.SelectorFromSet instead of fmt.Sprintf for label selector

## Problem Statement

`GetTrivyWorkloadVulnDetails` builds the label selector with `fmt.Sprintf`, which is safe today only because regex validators block selector-escape characters upstream. A future regression in the regexes would silently allow label-selector injection. The idiomatic client-go way is `labels.SelectorFromSet(labels.Set{...}).String()`, which cannot be mis-escaped.

**Why it matters:** Defense-in-depth. Current code is safe but relies on implicit contract between validator and consumer. Using the typed API documents the contract and hardens against regex regressions.

## Findings

### Security Sentinel + Architecture Strategist

**File:** `backend/internal/scanning/trivy.go:202-205`

```go
labelSelector := fmt.Sprintf(
    "trivy-operator.resource.namespace=%s,trivy-operator.resource.kind=%s,trivy-operator.resource.name=%s",
    namespace, kind, name,
)
```

Safe today because:
- `validNamespace`, `validWorkloadKind`, `validWorkloadName` all reject `,`, `=`, `!`, `(`, `)`, whitespace

But the safety is not visible at the call site.

## Proposed Solutions

### Option A: Use labels.SelectorFromSet (Recommended)

```go
import "k8s.io/apimachinery/pkg/labels"

selector := labels.SelectorFromSet(labels.Set{
    "trivy-operator.resource.namespace": namespace,
    "trivy-operator.resource.kind":      kind,
    "trivy-operator.resource.name":      name,
}).String()

list, err := dynClient.Resource(trivyVulnReportGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
    LabelSelector: selector,
})
```

**Pros:** Idiomatic; defense-in-depth; impossible to mis-escape
**Cons:** One extra import
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/trivy.go:202-209`

## Acceptance Criteria

- [ ] Label selector built via `labels.SelectorFromSet`
- [ ] Existing tests still pass
- [ ] No behavior change for valid inputs

## Work Log

<!-- Dated record -->

## Resources

- PR #167
- [k8s.io/apimachinery/pkg/labels](https://pkg.go.dev/k8s.io/apimachinery/pkg/labels)
