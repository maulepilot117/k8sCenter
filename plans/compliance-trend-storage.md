# feat: Compliance Trend Storage

## Overview

Store daily compliance score snapshots in PostgreSQL and display historical trends on the compliance dashboard. One row per cluster per day with an overall score and a minimal JSONB payload (`pass`, `fail`, `total`).

## Problem Statement

The compliance dashboard shows only the current point-in-time score. Users cannot answer "is our compliance improving or degrading?" without manual tracking. Historical trend data enables compliance auditing, regression detection, and progress reporting.

## Architecture

### Design Decisions

| Decision | Choice | Why |
|---|---|---|
| Table design | Flat table, no partitioning | <365 rows/year/cluster — partitioning is overkill |
| Granularity | One row per cluster per day | Overall score as column, counts in JSONB |
| Aggregation tables | None (v1) | YAGNI — 90 days of daily data is <100 rows per cluster |
| Retention | 90-day hard delete | Simple `DELETE WHERE snapshot_date < now() - 90 days` |
| Snapshot source | Raw unfiltered scores via service account | Background job has no user context |
| RBAC on history endpoint | RBAC-filtered (same as existing compliance endpoint) | Matches existing pattern; not admin-only |
| Multi-cluster | Local cluster only (v1) | Remote cluster snapshot requires new credential-access pattern; defer |
| Scheduler | time.NewTimer computing next midnight UTC | No drift, idempotent via ON CONFLICT, explicit timer.Stop() |
| Chart | Custom SVG inline in island | Zero dependencies; matches LogVolumeHistogram pattern |
| Summary stats | Frontend-computed from points array | No HistorySummary struct; 5 lines of TS |
| JSONB payload | Minimal: `{pass, fail, total}` | No namespace iteration; add byNamespace later via migration |
| Gap filling | Frontend handles sparse data | No generate_series; simpler SQL, matches LogVolumeHistogram |
| Recorder dependency | PolicyFetcher interface, not *Handler | Avoids cache entanglement; testable with mock |

### Schema

```sql
CREATE TABLE IF NOT EXISTS compliance_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    snapshot_date   DATE NOT NULL,
    cluster_id      TEXT NOT NULL DEFAULT 'local',
    overall_score   DOUBLE PRECISION NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_compliance_snapshot_unique
    ON compliance_snapshots (cluster_id, snapshot_date);
```

One index only — the unique index covers QueryHistory ordering.

**JSONB payload structure (v1 — minimal):**
```json
{"pass": 35, "fail": 5, "total": 42}
```

### API

```
GET /api/v1/policy/compliance/history?days=30

Response:
{
  "data": [
    {"date": "2026-04-01", "score": 82.4, "pass": 30, "fail": 7, "total": 37},
    {"date": "2026-04-02", "score": 84.1, "pass": 31, "fail": 6, "total": 37},
    ...
  ]
}
```

- `days` parameter: any integer in [1, 90], default 30
- Returns sparse data (only days with snapshots); frontend fills gaps
- RBAC-filtered same as existing compliance endpoint
- Respects `X-Cluster-ID` header (local only for v1)
- Summary stats (current, start, delta, best, worst) computed by frontend

### Background Job Flow

```
Server starts
  → ComplianceRecorder.Run(ctx) goroutine launched
    → Take immediate snapshot (idempotent via ON CONFLICT DO NOTHING)
    → Compute duration until next midnight UTC
    → Loop:
        ← time.NewTimer(durationToMidnight) fires
        → Fetch policies + violations via PolicyFetcher interface (service account, unfiltered)
        → computeCompliance() for cluster-wide score
        → INSERT INTO compliance_snapshots ... ON CONFLICT DO NOTHING
        → DELETE FROM compliance_snapshots WHERE snapshot_date < now() - 90 days
        → timer.Stop(), recompute duration to next midnight
```

### Edge Cases

| Scenario | Behavior |
|---|---|
| First deploy (no data) | Chart shows "Compliance trend data will appear after the first snapshot." |
| Few data points | Chart renders available points; delta shows difference or "—" if <2 points |
| Missing day (gap) | Frontend detects non-consecutive dates, breaks chart line |
| Server restart at 23:59 UTC | Startup snapshot fires, midnight fires 1 min later — ON CONFLICT deduplicates |
| Policy engine removed | Score changes; recorded as-is |
| PostgreSQL down during snapshot | Error logged, snapshot missed, gap in chart |
| days param invalid | Clamp to [1, 90], default 30 |
| No policy engine installed | Score is 0; chart still records the data point |

---

## Implementation Phases

### Phase 1: Database Migration + ComplianceStore

**Files to create:**

| File | Purpose |
|---|---|
| `backend/internal/store/migrations/000005_create_compliance_snapshots.up.sql` | Create table + index |
| `backend/internal/store/migrations/000005_create_compliance_snapshots.down.sql` | Drop table |
| `backend/internal/store/compliance.go` | ComplianceStore: Insert, QueryHistory, Cleanup |

**ComplianceStore:**
```go
type ComplianceStore struct {
    pool *pgxpool.Pool
}

type ComplianceSnapshot struct {
    Date         time.Time       `json:"date"`
    ClusterID    string          `json:"clusterId"`
    OverallScore float64         `json:"score"`
    Pass         int             `json:"pass"`
    Fail         int             `json:"fail"`
    Total        int             `json:"total"`
}

// Insert stores a snapshot, ignoring duplicates for the same cluster+date.
func (s *ComplianceStore) Insert(ctx context.Context, snap *ComplianceSnapshot) error

// QueryHistory returns snapshots for a cluster within the last N days (sparse, no gap-fill).
func (s *ComplianceStore) QueryHistory(ctx context.Context, clusterID string, days int) ([]ComplianceSnapshot, error)

// Cleanup deletes snapshots older than retentionDays, returns rows deleted.
func (s *ComplianceStore) Cleanup(ctx context.Context, retentionDays int) (int64, error)
```

**QueryHistory SQL** (simple, no generate_series):
```sql
SELECT snapshot_date, overall_score,
       (payload->>'pass')::int, (payload->>'fail')::int, (payload->>'total')::int
FROM compliance_snapshots
WHERE cluster_id = $1 AND snapshot_date >= CURRENT_DATE - $2
ORDER BY snapshot_date
```

**Acceptance criteria:**
- [ ] Migration creates table with unique constraint on (cluster_id, snapshot_date)
- [ ] Insert is idempotent (ON CONFLICT DO NOTHING)
- [ ] QueryHistory returns sparse data ordered by date
- [ ] Cleanup deletes rows older than retention period, returns count
- [ ] `go vet` passes

---

### Phase 2: ComplianceRecorder Background Job

**Files to create:**

| File | Purpose |
|---|---|
| `backend/internal/policy/recorder.go` | ComplianceRecorder with Run() goroutine + PolicyFetcher interface |

**Files to modify:**

| File | Change |
|---|---|
| `backend/cmd/kubecenter/main.go` | Construct ComplianceStore, launch `go recorder.Run(ctx)` |
| `backend/internal/policy/handler.go` | Add FetchUnfiltered method implementing PolicyFetcher |

**ComplianceRecorder:**
```go
// PolicyFetcher fetches raw policy/violation data for snapshots.
type PolicyFetcher interface {
    FetchUnfiltered(ctx context.Context) ([]NormalizedPolicy, []NormalizedViolation, error)
}

type ComplianceRecorder struct {
    store     *store.ComplianceStore
    fetcher   PolicyFetcher
    clusterID string
    logger    *slog.Logger
}

func (r *ComplianceRecorder) Run(ctx context.Context) {
    r.takeSnapshot(ctx)  // immediate on startup
    for {
        timer := time.NewTimer(durationUntilMidnightUTC())
        select {
        case <-ctx.Done():
            timer.Stop()
            return
        case <-timer.C:
            r.takeSnapshot(ctx)
        }
    }
}
```

The recorder calls `FetchUnfiltered` (service account, no RBAC filter) then `computeCompliance` (package-level function) for the cluster-wide score. Builds a minimal `{pass, fail, total}` payload and inserts.

**Acceptance criteria:**
- [ ] Snapshot taken immediately on startup
- [ ] Daily snapshots at midnight UTC (no drift)
- [ ] Idempotent: double-fire produces one row
- [ ] Uses time.NewTimer with explicit Stop
- [ ] Context cancellation stops the goroutine
- [ ] Cleanup runs after each snapshot (90-day retention)
- [ ] Errors logged but don't crash the process

---

### Phase 3: History API Endpoint

**Files to modify:**

| File | Change |
|---|---|
| `backend/internal/policy/handler.go` | Add HandleComplianceHistory method, wire ComplianceStore |
| `backend/internal/server/routes.go` | Add `GET /policy/compliance/history` route |

**Handler:**
```go
func (h *Handler) HandleComplianceHistory(w http.ResponseWriter, r *http.Request) {
    if _, ok := httputil.RequireUser(w, r); !ok { return }
    
    days := clampInt(r.URL.Query().Get("days"), 1, 90, 30)
    clusterID := middleware.ClusterIDFromContext(r.Context())
    
    points, err := h.complianceStore.QueryHistory(r.Context(), clusterID, days)
    // ... write response as {data: [...]}
}
```

**Acceptance criteria:**
- [ ] Requires authentication (RequireUser)
- [ ] `days` param clamped to [1, 90], default 30
- [ ] Returns sparse points array (frontend fills gaps)
- [ ] Respects X-Cluster-ID header

---

### Phase 4: Frontend — ComplianceTrendChart Island

**Files to create:**

| File | Purpose |
|---|---|
| `frontend/islands/ComplianceTrendChart.tsx` | Island: fetch history, render SVG area chart, time range selector |

**Files to modify:**

| File | Change |
|---|---|
| `frontend/islands/ComplianceDashboard.tsx` | Import and render ComplianceTrendChart below existing gauge |

**ComplianceTrendChart island:**
- Fetches `GET /v1/policy/compliance/history?days=N`
- Time range selector: 7d / 30d / 90d buttons
- SVG area chart with gradient fill (inline, not extracted)
- Summary stats computed from points: current score, delta, best/worst
- Frontend fills date gaps for chart rendering (detects non-consecutive dates)
- Empty state when no data
- Loading/error states matching existing island patterns

**Acceptance criteria:**
- [ ] Chart renders correctly with varying day ranges
- [ ] Missing days show as gaps (no fake interpolation)
- [ ] Score delta badge: green for positive, red for negative
- [ ] Empty state when no data exists
- [ ] Theme-compliant: CSS custom properties for all colors
- [ ] `deno lint` and `deno fmt --check` pass

---

## File Summary

### New Files (4)

| File | Phase |
|---|---|
| `backend/internal/store/migrations/000005_create_compliance_snapshots.up.sql` | 1 |
| `backend/internal/store/migrations/000005_create_compliance_snapshots.down.sql` | 1 |
| `backend/internal/store/compliance.go` | 1 |
| `backend/internal/policy/recorder.go` | 2 |
| `frontend/islands/ComplianceTrendChart.tsx` | 4 |

### Modified Files (4)

| File | Phase | Change |
|---|---|---|
| `backend/cmd/kubecenter/main.go` | 2 | Construct store, launch recorder |
| `backend/internal/policy/handler.go` | 2-3 | FetchUnfiltered + HandleComplianceHistory |
| `backend/internal/server/routes.go` | 3 | Add history route |
| `frontend/islands/ComplianceDashboard.tsx` | 4 | Embed ComplianceTrendChart |

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Missed snapshot (DB down) | Gap in chart | Logged, startup snapshot backfills current day |
| Policy engine removed | Score jumps | Acceptable; add engine metadata to payload later |
| Clock skew | Duplicate/missed snapshots | DATE type + ON CONFLICT + UTC throughout |

## Future Enhancements

- `byNamespace` and `bySeverity` in JSONB payload (add fields, no schema change)
- Per-namespace trend drill-down (click namespace row → chart)
- Weekly/monthly aggregation tables for >90 day trends
- Remote cluster snapshots (requires background credential access pattern)
- Configurable retention via koanf (`KUBECENTER_COMPLIANCE_RETENTION_DAYS`)
- Score change notifications (alert when score drops >5 points)

## References

### Internal
- Compliance scoring: `backend/internal/policy/handler.go:347-408` (computeCompliance)
- ComplianceScore type: `backend/internal/policy/types.go:73-89`
- Store patterns: `backend/internal/store/clusters.go` (ClusterStore exemplar)
- Audit store (time-series precedent): `backend/internal/audit/store.go`
- Background job pattern: `backend/internal/k8s/cluster_prober.go:36-48`
- Migration convention: `backend/internal/store/migrations/000004_*`
- SVG chart patterns: `frontend/islands/LogVolumeHistogram.tsx`
- Frontend types: `frontend/lib/policy-types.ts:55-63`
