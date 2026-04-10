---
status: pending
priority: p1
issue_id: "282"
tags: [code-review, limits, wizard, panic, stability]
dependencies: []
---

# Wizard MustParse Can Panic on Invalid Input

## Problem Statement

The `ToYAML()` method uses `resource.MustParse()` which panics if parsing fails. Although `Validate()` checks these values first, the two methods can be called independently. If `ToYAML()` is called without prior validation (or with a modified input after validation), the server will crash.

## Findings

**Source:** Security Sentinel + Data Integrity Guardian agents

**Location:** `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/wizard/namespace_limits.go` (lines 183-260)

**Evidence:**
```go
func (n *NamespaceLimitsInput) buildResourceQuotaYAML() (string, error) {
    hard := corev1.ResourceList{
        corev1.ResourceCPU:    resource.MustParse(n.Quota.CPUHard), // PANIC if invalid
        corev1.ResourceMemory: resource.MustParse(n.Quota.MemoryHard),
        corev1.ResourcePods:   resource.MustParse(strconv.Itoa(n.Quota.PodsHard)),
    }
    // ... more MustParse calls for optional fields
}
```

**Impact:**
- A crafted request bypassing validation could cause a panic and service disruption (DoS)
- Server crash affects all users

## Proposed Solutions

### Solution A: Replace MustParse with ParseQuantity + Error Handling

```go
func (n *NamespaceLimitsInput) buildResourceQuotaYAML() (string, error) {
    cpuQty, err := resource.ParseQuantity(n.Quota.CPUHard)
    if err != nil {
        return "", fmt.Errorf("invalid CPU quantity: %w", err)
    }
    memQty, err := resource.ParseQuantity(n.Quota.MemoryHard)
    if err != nil {
        return "", fmt.Errorf("invalid memory quantity: %w", err)
    }
    // ...
}
```

**Pros:** Defensive, no panic possible, clear error messages  
**Cons:** More verbose, but safer  
**Effort:** Medium (many MustParse calls to change)  
**Risk:** Low

### Solution B: Validate Then Trust Pattern

Ensure `ToYAML()` is ONLY called after `Validate()` succeeds by making it internal to a combined method.

```go
func (n *NamespaceLimitsInput) ValidateAndGenerateYAML() (string, []FieldError, error) {
    errs := n.Validate()
    if len(errs) > 0 {
        return "", errs, nil
    }
    yaml, err := n.toYAMLInternal() // unexported, MustParse is safe
    return yaml, nil, err
}
```

**Pros:** Single entry point enforces contract  
**Cons:** Changes API contract, existing tests may need updates  
**Effort:** Medium  
**Risk:** Medium

### Solution C: Add recover() wrapper (Not Recommended)

Wrap MustParse in recover() to convert panic to error.

**Pros:** Minimal code changes  
**Cons:** Panics are not the right control flow for errors  
**Effort:** Small  
**Risk:** Medium - masks underlying issue

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/wizard/namespace_limits.go`

**MustParse Call Sites:**
- Line 184: `n.Quota.CPUHard`
- Line 185: `n.Quota.MemoryHard`
- Line 186: `n.Quota.PodsHard`
- Line 191-204: Optional quota fields
- Lines 246-261: LimitRange default/min/max values
- Lines 271-277: Optional Pod limits
- Lines 286-293: Optional PVC limits

## Acceptance Criteria

- [ ] No MustParse calls that can receive user input
- [ ] All parsing errors return proper error responses
- [ ] Server does not crash on malformed input
- [ ] Test verifies error handling for invalid quantities

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Security and data integrity both flagged |

## Resources

- PR #164 code review
