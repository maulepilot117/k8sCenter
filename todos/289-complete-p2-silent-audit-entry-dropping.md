---
status: pending
priority: p2
issue_id: "289"
tags: [code-review, observability, compliance, pr-166]
dependencies: []
---

# Silent Audit Entry Dropping Without Metrics

## Problem Statement

When the audit buffer fills (1000 entries), new entries are silently dropped with only a log warning. The calling code receives `nil` error and has no indication the audit failed. There are no metrics to alert on this condition.

**Why it matters:** Audit logs are critical for security compliance (SOC2, PCI-DSS, HIPAA). Silent data loss could mask attacker activity and violate audit completeness requirements.

## Findings

### Security Sentinel Analysis

**File:** `backend/internal/audit/sqlite_logger.go`
**Lines:** 88-94

```go
select {
case l.entryCh <- e:
    // Queued successfully
default:
    // Buffer full — log warning but don't block the caller
    l.logger.Warn("audit buffer full, entry dropped", "action", e.Action, "user", e.User)
}
return nil  // Caller has no idea the audit failed!
```

**Issues:**
1. No error returned to caller
2. No Prometheus metric for monitoring
3. No notification center event
4. An attacker could intentionally overflow the buffer to hide malicious actions

### Data Integrity Guardian Analysis

Confirmed same finding. Under sustained load (1000+ operations in flight), entries are dropped. Security team investigating a breach would have an incomplete audit trail.

## Proposed Solutions

### Option A: Add Prometheus Counter (Recommended)

Add a metric that can be alerted on:

```go
var auditEntriesDropped = prometheus.NewCounter(prometheus.CounterOpts{
    Name: "kubecenter_audit_entries_dropped_total",
    Help: "Number of audit entries dropped due to buffer full",
})

func init() {
    prometheus.MustRegister(auditEntriesDropped)
}

// In Log():
default:
    auditEntriesDropped.Inc()
    l.logger.Warn("audit buffer full, entry dropped", ...)
```

**Pros:** Enables alerting, standard pattern, low overhead
**Cons:** Need to import Prometheus client
**Effort:** Small
**Risk:** Low

### Option B: Return Error to Caller

Let the caller decide what to do:

```go
func (l *PostgresLogger) Log(ctx context.Context, e Entry) error {
    l.slog.Log(ctx, e)
    select {
    case l.entryCh <- e:
        return nil
    default:
        return fmt.Errorf("audit buffer full, entry dropped")
    }
}
```

**Pros:** Explicit, caller can handle (e.g., retry, block)
**Cons:** Breaks fire-and-forget semantics, all callers must handle error
**Effort:** Medium (touch all call sites)
**Risk:** Medium

### Option C: Blocking Mode for Critical Actions

For security-critical actions, use synchronous writes:

```go
func (l *PostgresLogger) LogBlocking(ctx context.Context, e Entry) error {
    l.slog.Log(ctx, e)
    return l.store.Insert(ctx, e)  // Block on database
}
```

Then use `LogBlocking` for: `ActionReveal`, `ActionLogin`, `ActionDelete`, `ActionAgentExec`

**Pros:** Guarantees critical entries, maintains async for non-critical
**Cons:** Two code paths, complexity
**Effort:** Medium
**Risk:** Low

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/audit/sqlite_logger.go`
- (Option A) `backend/internal/server/metrics.go` or similar

**Components:** Audit logging, observability

## Acceptance Criteria

- [ ] Dropped audit entries are visible in monitoring
- [ ] Alert can be configured on sustained drops
- [ ] Consider: Error returned to caller OR blocking mode for critical actions

## Work Log

| Date | Author | Action | Notes |
|------|--------|--------|-------|
| 2026-04-11 | Code Review | Created | Found by security-sentinel, data-integrity-guardian agents |

## Resources

- PR: https://github.com/maulepilot117/k8sCenter/pull/166
- Compliance frameworks: SOC2 CC7.2, PCI-DSS 10.2, HIPAA 164.312(b)
