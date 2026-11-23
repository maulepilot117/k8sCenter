package store

// rubrik_poller_test.go — the Rubrik poller's persistence (U10, KTD10).
//
// The validation tests run without a database. The rest are env-gated
// (testDB) and isolate by a cluster id from testOwnerID(t).

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pollerFixture is a mapped cluster, its store and a live lease on it.
type pollerFixture struct {
	s         *RubrikStore
	pool      *pgxpool.Pool
	cluster   string
	rscID     string
	mappingAt time.Time
	lease     RubrikPollerLease
}

func newPollerFixture(t *testing.T) pollerFixture {
	t.Helper()
	return newPollerFixtureOn(t, testDB(t))
}

func newPollerFixtureOn(t *testing.T, pool *pgxpool.Pool) pollerFixture {
	t.Helper()
	s := NewRubrikStore(pool, rubrikTestKey)
	connID, _ := newRubrikTestConnection(t, s, "https://tenant.my.rubrik.com")
	rscID := "rsc-" + testOwnerID(t)
	cluster := mapRubrikTestCluster(t, s, pool, connID, rscID)
	m, err := s.GetMapping(t.Context(), cluster)
	if err != nil {
		t.Fatalf("reading the mapping: %v", err)
	}
	lease, err := s.AcquireOrRenewPollerLease(t.Context(), cluster, "replica-a", time.Minute)
	if err != nil {
		t.Fatalf("acquiring the lease: %v", err)
	}
	return pollerFixture{s: s, pool: pool, cluster: cluster, rscID: rscID, mappingAt: m.UpdatedAt, lease: lease}
}

// event returns a valid event of kind with dedup key key.
func pollerEvent(kind, key string) RubrikPollerEvent {
	return RubrikPollerEvent{
		Kind: kind, RSCObjectID: "obj-1", Namespace: "team-a", DedupKey: key,
		OccurredAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), Detail: json.RawMessage(`{"jobId":"series-1"}`),
	}
}

func (f pollerFixture) tick(events ...RubrikPollerEvent) RubrikPollerTick {
	return RubrikPollerTick{RSCClusterID: f.rscID, MappingUpdatedAt: f.mappingAt, Events: events}
}

func (f pollerFixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), query, f.cluster).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	return n
}

func (f pollerFixture) deliveries(t *testing.T) int {
	return f.count(t, `SELECT count(*) FROM rubrik_poller_deliveries d
		JOIN rubrik_poller_events e ON e.id = d.event_id WHERE e.cluster_id = $1`)
}

func TestValidatePollerTick(t *testing.T) {
	good := RubrikPollerTick{
		RSCClusterID:     "rsc-1",
		MappingUpdatedAt: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC),
		Compliance:       []RubrikComplianceState{{RSCObjectID: "obj-1", Namespace: "team-a", Status: RubrikComplianceLate, LateStreak: 1}},
		Events:           []RubrikPollerEvent{pollerEvent(RubrikEventSnapshotFailed, "series-1")},
	}
	if err := validatePollerTick("cluster-1", good); err != nil {
		t.Fatalf("a valid tick was refused: %v", err)
	}
	for name, mutate := range map[string]func(*RubrikPollerTick){
		"no rsc cluster":       func(k *RubrikPollerTick) { k.RSCClusterID = "" },
		"no mapping time":      func(k *RubrikPollerTick) { k.MappingUpdatedAt = time.Time{} },
		"state namespace long": func(k *RubrikPollerTick) { k.Compliance[0].Namespace = strings.Repeat("n", rubrikOpMaxText+1) },
		"event object long":    func(k *RubrikPollerTick) { k.Events[0].RSCObjectID = strings.Repeat("o", rubrikOpMaxText+1) },
		"detail too large": func(k *RubrikPollerTick) {
			k.Events[0].Detail = json.RawMessage(`"` + strings.Repeat("d", rubrikOpMaxJSONSize) + `"`)
		},
		"negative streak":      func(k *RubrikPollerTick) { k.State.ConnectionFailureStreak = -1 },
		"empty object id":      func(k *RubrikPollerTick) { k.Compliance[0].RSCObjectID = "" },
		"unknown status":       func(k *RubrikPollerTick) { k.Compliance[0].Status = "red" },
		"negative late streak": func(k *RubrikPollerTick) { k.Compliance[0].LateStreak = -1 },
		"object judged twice":  func(k *RubrikPollerTick) { k.Compliance = append(k.Compliance, k.Compliance[0]) },
		"unknown event kind":   func(k *RubrikPollerTick) { k.Events[0].Kind = "exploded" },
		"empty dedup key":      func(k *RubrikPollerTick) { k.Events[0].DedupKey = "" },
		"no occurred-at":       func(k *RubrikPollerTick) { k.Events[0].OccurredAt = time.Time{} },
		"detail not JSON":      func(k *RubrikPollerTick) { k.Events[0].Detail = json.RawMessage(`{`) },
		"namespace too long":   func(k *RubrikPollerTick) { k.Events[0].Namespace = strings.Repeat("n", rubrikOpMaxText+1) },
		"NUL in dedup key":     func(k *RubrikPollerTick) { k.Events[0].DedupKey = "a\x00b" },
	} {
		t.Run(name, func(t *testing.T) {
			k := good
			k.Compliance = append([]RubrikComplianceState(nil), good.Compliance...)
			k.Events = append([]RubrikPollerEvent(nil), good.Events...)
			mutate(&k)
			if err := validatePollerTick("cluster-1", k); !errors.Is(err, ErrRubrikPollerInvalid) {
				t.Errorf("err = %v, want ErrRubrikPollerInvalid", err)
			}
		})
	}
	if err := validatePollerTick("", good); !errors.Is(err, ErrRubrikPollerInvalid) {
		t.Errorf("no cluster id: err = %v, want ErrRubrikPollerInvalid", err)
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL-backed.
// ---------------------------------------------------------------------------

func TestRubrikPollerLease(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()

	renewed, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-a", time.Minute)
	if err != nil || renewed.Fence != f.lease.Fence || !renewed.AcquiredAt.Equal(f.lease.AcquiredAt) {
		t.Fatalf("renewal = (%+v, %v); want the same fence and tenure", renewed, err)
	}
	if _, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-b", time.Minute); !errors.Is(err, ErrRubrikPollerLeaseHeld) {
		t.Fatalf("a second replica took a live lease: err = %v", err)
	}
	// Releasing a lease one does not hold changes nothing.
	if err := f.s.ReleasePollerLease(ctx, f.cluster, "replica-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-b", time.Minute); !errors.Is(err, ErrRubrikPollerLeaseHeld) {
		t.Fatalf("a foreign release freed the lease: err = %v", err)
	}
	if err := f.s.ReleasePollerLease(ctx, f.cluster, "replica-a"); err != nil {
		t.Fatal(err)
	}
	taken, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-b", time.Minute)
	if err != nil || taken.Holder != "replica-b" || taken.Fence != f.lease.Fence+1 {
		t.Fatalf("takeover = (%+v, %v); want replica-b at fence %d", taken, err, f.lease.Fence+1)
	}
}

// A replica back from a pause longer than the ttl finds its lease expired.
// Its old lease value commits nothing; renewing it as the same holder is a
// new tenure with the next fence, and the old value stays refused.
func TestCommitPollerTick_ExpiredLeaseCommitsNothing(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE rubrik_poller_lease SET expires_at = NOW() - interval '1 second'
		WHERE cluster_id = $1`, f.cluster); err != nil {
		t.Fatal(err)
	}
	stale := f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-1"))
	stale.Compliance = []RubrikComplianceState{{RSCObjectID: "obj-1", Status: RubrikComplianceLate, LateStreak: 1}}
	if _, err := f.s.CommitPollerTick(ctx, f.lease, stale); !errors.Is(err, ErrRubrikPollerLeaseLost) {
		t.Fatalf("expired lease: err = %v, want ErrRubrikPollerLeaseLost", err)
	}
	if n := f.count(t, `SELECT (SELECT count(*) FROM rubrik_poller_events WHERE cluster_id = $1)
		+ (SELECT count(*) FROM rubrik_compliance_states WHERE cluster_id = $1)
		+ (SELECT count(*) FROM rubrik_poller_state WHERE cluster_id = $1)`); n != 0 {
		t.Fatalf("the expired lease wrote %d rows, want 0", n)
	}

	renewed, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-a", time.Minute)
	if err != nil || renewed.Fence != f.lease.Fence+1 || !renewed.AcquiredAt.After(f.lease.AcquiredAt) {
		t.Fatalf("same holder after expiry = (%+v, %v); want fence %d and a new tenure", renewed, err, f.lease.Fence+1)
	}
	if _, err := f.s.CommitPollerTick(ctx, f.lease, stale); !errors.Is(err, ErrRubrikPollerLeaseLost) {
		t.Fatalf("pre-expiry lease after renewal: err = %v, want ErrRubrikPollerLeaseLost", err)
	}
	if rec, err := f.s.CommitPollerTick(ctx, renewed, stale); err != nil || len(rec) != 1 {
		t.Fatalf("renewed lease = (%d recorded, %v); want 1", len(rec), err)
	}
}

func TestCommitPollerTick_RecordsTheTick(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()

	if _, found, err := f.s.GetPollerState(ctx, f.cluster); err != nil || found {
		t.Fatalf("before the first tick: found=%v err=%v, want no state", found, err)
	}
	cursor := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	tick := f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-1"))
	tick.State = RubrikPollerState{ActivityCursor: &cursor, ConnectionFailureStreak: 2, ConnectionFailing: true}
	tick.Compliance = []RubrikComplianceState{{RSCObjectID: "obj-1", Namespace: "team-a", Status: RubrikComplianceLate, LateStreak: 1}}

	recorded, err := f.s.CommitPollerTick(ctx, f.lease, tick)
	if err != nil {
		t.Fatalf("CommitPollerTick: %v", err)
	}
	if len(recorded) != 1 || recorded[0].ID == "" || recorded[0].ClusterID != f.cluster || recorded[0].CreatedAt.IsZero() {
		t.Fatalf("recorded = %+v; want the one event with its id", recorded)
	}
	st, found, err := f.s.GetPollerState(ctx, f.cluster)
	if err != nil || !found || st.ActivityCursor == nil || !st.ActivityCursor.Equal(cursor) ||
		st.ConnectionFailureStreak != 2 || !st.ConnectionFailing || st.LastTickAt == nil {
		t.Errorf("state = (%+v, found=%v, %v); want the tick's bookkeeping", st, found, err)
	}
	states, err := f.s.ListComplianceStates(ctx, f.cluster)
	if err != nil || len(states) != 1 || states[0].Status != RubrikComplianceLate || states[0].LateStreak != 1 {
		t.Errorf("compliance = (%+v, %v); want obj-1 late once", states, err)
	}
	if n := f.deliveries(t); n != 1 {
		t.Errorf("deliveries = %d, want 1", n)
	}
}

// R27, AE9: a transition recorded twice, by the same holder or by a later
// one, is one event with one delivery. A holder whose lease was taken over
// writes nothing at all.
func TestCommitPollerTick_OneDeliveryPerTransitionAcrossReplicas(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	lost := pollerEvent(RubrikEventComplianceLost, "obj-1@2026-11-22T10:00:00Z")

	if rec, err := f.s.CommitPollerTick(ctx, f.lease, f.tick(lost)); err != nil || len(rec) != 1 {
		t.Fatalf("first commit = (%d recorded, %v); want 1", len(rec), err)
	}
	if rec, err := f.s.CommitPollerTick(ctx, f.lease, f.tick(lost)); err != nil || len(rec) != 0 {
		t.Fatalf("same holder again = (%d recorded, %v); want 0", len(rec), err)
	}

	if err := f.s.ReleasePollerLease(ctx, f.cluster, "replica-a"); err != nil {
		t.Fatal(err)
	}
	b, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, "replica-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := f.s.CommitPollerTick(ctx, b, f.tick(lost)); err != nil || len(rec) != 0 {
		t.Fatalf("next holder = (%d recorded, %v); want 0", len(rec), err)
	}

	// The former holder's lease is stale: its whole tick is refused.
	stale := f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-9"))
	stale.Compliance = []RubrikComplianceState{{RSCObjectID: "obj-9", Status: RubrikComplianceOut, LateStreak: 2}}
	if _, err := f.s.CommitPollerTick(ctx, f.lease, stale); !errors.Is(err, ErrRubrikPollerLeaseLost) {
		t.Fatalf("stale holder: err = %v, want ErrRubrikPollerLeaseLost", err)
	}
	if n := f.deliveries(t); n != 1 {
		t.Errorf("deliveries = %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM rubrik_compliance_states WHERE cluster_id = $1`); n != 0 {
		t.Errorf("the stale tick wrote %d compliance states, want 0", n)
	}
}

// Three replicas racing for the lease and committing the same gap: one
// event and one delivery (R24, AE9).
func TestCommitPollerTick_ConcurrentReplicasRecordOneGap(t *testing.T) {
	f := newPollerFixtureOn(t, testDBWithMaxConns(t, 8))
	ctx := t.Context()
	if err := f.s.ReleasePollerLease(ctx, f.cluster, "replica-a"); err != nil {
		t.Fatal(err)
	}
	gap := pollerEvent(RubrikEventMonitoringGap, "gap@2026-11-20T00:00:00Z")
	gap.RSCObjectID, gap.Namespace = "", ""

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		committed int
	)
	for _, holder := range []string{"replica-1", "replica-2", "replica-3"} {
		wg.Go(func() {
			lease, err := f.s.AcquireOrRenewPollerLease(ctx, f.cluster, holder, time.Minute)
			if err != nil {
				return // another replica holds it
			}
			if _, err := f.s.CommitPollerTick(ctx, lease, f.tick(gap)); err != nil {
				t.Errorf("%s: CommitPollerTick: %v", holder, err)
			} else {
				mu.Lock()
				committed++
				mu.Unlock()
			}
			if err := f.s.ReleasePollerLease(ctx, f.cluster, holder); err != nil {
				t.Errorf("%s: release: %v", holder, err)
			}
		})
	}
	wg.Wait()
	if committed == 0 {
		t.Fatal("no replica committed the gap")
	}
	if n := f.count(t, `SELECT count(*) FROM rubrik_poller_events WHERE cluster_id = $1 AND kind = 'monitoring_gap'`); n != 1 {
		t.Errorf("gap events = %d, want 1", n)
	}
	if n := f.deliveries(t); n != 1 {
		t.Errorf("deliveries = %d, want 1", n)
	}
}

// A tick read against one RSC cluster is not written once the mapping
// names another, or is gone.
func TestCommitPollerTick_RefusesAfterTheMappingChanged(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	tick := f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-1"))
	tick.RSCClusterID = "rsc-other"
	if _, err := f.s.CommitPollerTick(ctx, f.lease, tick); !errors.Is(err, ErrRubrikPollerMappingChanged) {
		t.Fatalf("other rsc cluster: err = %v, want ErrRubrikPollerMappingChanged", err)
	}
	// The mapping was written after the tick read it (a delete and re-map
	// of the same target, or any edit).
	rewritten := f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-1"))
	rewritten.MappingUpdatedAt = f.mappingAt.Add(-time.Second)
	if _, err := f.s.CommitPollerTick(ctx, f.lease, rewritten); !errors.Is(err, ErrRubrikPollerMappingChanged) {
		t.Fatalf("mapping written since the read: err = %v, want ErrRubrikPollerMappingChanged", err)
	}
	if err := f.s.DeleteMapping(ctx, f.cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CommitPollerTick(ctx, f.lease, f.tick(pollerEvent(RubrikEventSnapshotFailed, "series-2"))); !errors.Is(err, ErrRubrikPollerMappingChanged) {
		t.Fatalf("mapping deleted: err = %v, want ErrRubrikPollerMappingChanged", err)
	}
	if n := f.count(t, `SELECT count(*) FROM rubrik_poller_events WHERE cluster_id = $1`); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
}

// changed_at moves only when an object's status changes; a tick that saw
// every object prunes the rest, and one that did not keeps them.
func TestCommitPollerTick_ComplianceStates(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	state := func(id, status string, streak int) RubrikComplianceState {
		return RubrikComplianceState{RSCObjectID: id, Namespace: "team-a", Status: status, LateStreak: streak}
	}
	read := func() map[string]RubrikComplianceState {
		t.Helper()
		rows, err := f.s.ListComplianceStates(ctx, f.cluster)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]RubrikComplianceState{}
		for _, r := range rows {
			out[r.RSCObjectID] = r
		}
		return out
	}
	commit := func(prune bool, states ...RubrikComplianceState) {
		t.Helper()
		tick := f.tick()
		tick.Compliance, tick.PruneCompliance = states, prune
		if _, err := f.s.CommitPollerTick(ctx, f.lease, tick); err != nil {
			t.Fatalf("CommitPollerTick: %v", err)
		}
	}

	commit(true, state("obj-a", RubrikComplianceIn, 0), state("obj-b", RubrikComplianceIn, 0))
	first := read()
	commit(false, state("obj-a", RubrikComplianceIn, 0), state("obj-b", RubrikComplianceLate, 1))
	second := read()
	if !second["obj-a"].ChangedAt.Equal(first["obj-a"].ChangedAt) {
		t.Errorf("obj-a kept its status but changed_at moved")
	}
	if !second["obj-b"].ChangedAt.After(first["obj-b"].ChangedAt) || second["obj-b"].LateStreak != 1 {
		t.Errorf("obj-b = %+v; want a new changed_at and late streak 1", second["obj-b"])
	}
	if !second["obj-a"].EvaluatedAt.After(first["obj-a"].EvaluatedAt) {
		t.Errorf("obj-a was re-evaluated but evaluated_at did not move")
	}

	commit(false, state("obj-a", RubrikComplianceIn, 0))
	if _, ok := read()["obj-b"]; !ok {
		t.Error("a tick without the full object list pruned obj-b")
	}
	commit(true, state("obj-a", RubrikComplianceIn, 0))
	if got := read(); len(got) != 1 || got["obj-b"].RSCObjectID != "" {
		t.Errorf("states = %+v; want obj-a only after a full tick", got)
	}
	other := newPollerFixtureOn(t, f.pool)
	otherTick := other.tick()
	otherTick.Compliance = []RubrikComplianceState{state("obj-b", RubrikComplianceIn, 0)}
	if _, err := other.s.CommitPollerTick(ctx, other.lease, otherTick); err != nil {
		t.Fatal(err)
	}
	commit(true)
	if got := read(); len(got) != 0 {
		t.Errorf("a full tick that saw no object kept %+v", got)
	}
	if got, err := other.s.ListComplianceStates(ctx, other.cluster); err != nil || len(got) != 1 {
		t.Errorf("another cluster's states after the prune = (%+v, %v); want its one row", got, err)
	}
}

func TestPollerDeliveries(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	other := newPollerFixture(t)
	if _, err := other.s.CommitPollerTick(ctx, other.lease, other.tick(pollerEvent(RubrikEventSnapshotFailed, "series-x"))); err != nil {
		t.Fatal(err)
	}
	recorded, err := f.s.CommitPollerTick(ctx, f.lease, f.tick(
		pollerEvent(RubrikEventSnapshotFailed, "series-1"), pollerEvent(RubrikEventSnapshotFailed, "series-2")))
	if err != nil || len(recorded) != 2 {
		t.Fatalf("CommitPollerTick = (%d, %v); want 2 events", len(recorded), err)
	}

	jobs, err := f.s.ClaimPollerDeliveries(ctx, f.cluster, 3, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim = (%d jobs, %v); want this cluster's 2", len(jobs), err)
	}
	first := jobs[0]
	want := pollerEvent(RubrikEventSnapshotFailed, first.Event.DedupKey)
	var detail map[string]string
	if err := json.Unmarshal(first.Event.Detail, &detail); err != nil || detail["jobId"] != "series-1" {
		t.Errorf("claimed detail = %s (%v); want the recorded detail", first.Event.Detail, err)
	}
	if first.Delivery.Attempts != 1 || first.Delivery.State != RubrikDeliveryPending || first.Event.ID != first.Delivery.EventID ||
		first.Event.ClusterID != f.cluster || first.Event.Kind != want.Kind || first.Event.Namespace != want.Namespace ||
		first.Event.RSCObjectID != want.RSCObjectID || !first.Event.OccurredAt.Equal(want.OccurredAt) ||
		(first.Event.DedupKey != "series-1" && first.Event.DedupKey != "series-2") {
		t.Errorf("claimed job = %+v; want attempt 1 with its event", first)
	}

	if err := f.s.MarkPollerDelivered(ctx, first.Delivery.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.MarkPollerDelivered(ctx, first.Delivery.ID); !errors.Is(err, ErrRubrikDeliveryNotPending) {
		t.Errorf("marking twice: err = %v, want ErrRubrikDeliveryNotPending", err)
	}

	second := jobs[1].Delivery.ID
	if err := f.s.MarkPollerDeliveryFailed(ctx, second, "emit_failed", 2); err != nil {
		t.Fatal(err)
	}
	again, err := f.s.ClaimPollerDeliveries(ctx, f.cluster, 2, 10)
	if err != nil || len(again) != 1 || again[0].Delivery.ID != second || again[0].Delivery.Attempts != 2 ||
		again[0].Delivery.LastError != "emit_failed" {
		t.Fatalf("reclaim = (%+v, %v); want the failed delivery at attempt 2", again, err)
	}
	if err := f.s.MarkPollerDeliveryFailed(ctx, second, "emit_failed", 2); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM rubrik_poller_deliveries d JOIN rubrik_poller_events e ON e.id = d.event_id
		WHERE e.cluster_id = $1 AND d.state = 'failed'`); n != 1 {
		t.Errorf("failed deliveries = %d, want 1 once the attempts are spent", n)
	}
	if left, err := f.s.ClaimPollerDeliveries(ctx, f.cluster, 2, 10); err != nil || len(left) != 0 {
		t.Errorf("claim after both settled = (%d, %v); want none", len(left), err)
	}
}

func TestExpireApplyingOperations(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	s := NewRubrikStore(pool, rubrikTestKey)
	cluster, otherCluster := testOwnerID(t), testOwnerID(t)

	insert := func(clusterID string, age time.Duration, state string) string {
		t.Helper()
		op, err := s.InsertOperation(ctx, newRubrikOp(clusterID, testOwnerID(t), RubrikOpSnapshot))
		if err != nil {
			t.Fatalf("InsertOperation: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE rubrik_operations SET state = $2, updated_at = NOW() - make_interval(secs => $3::double precision)
			WHERE id = $1`, op.ID, state, age.Seconds()); err != nil {
			t.Fatal(err)
		}
		return op.ID
	}
	stuck := insert(cluster, time.Hour, RubrikOpApplying)
	fresh := insert(cluster, time.Second, RubrikOpApplying)
	running := insert(cluster, time.Hour, RubrikOpRunning)
	elsewhere := insert(otherCluster, time.Hour, RubrikOpApplying)

	n, err := s.ExpireApplyingOperations(ctx, cluster, 10*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("ExpireApplyingOperations = (%d, %v); want 1", n, err)
	}
	for id, want := range map[string]string{stuck: RubrikOpUnknown, fresh: RubrikOpApplying, running: RubrikOpRunning, elsewhere: RubrikOpApplying} {
		op, err := s.GetOperation(ctx, id)
		if err != nil || op.State != want {
			t.Errorf("operation %s = (%s, %v); want %s", id, op.State, err, want)
		}
	}
	if _, err := s.ExpireApplyingOperations(ctx, cluster, time.Minute); err == nil {
		t.Errorf("a grace below %s was accepted; it would expire a live send", RubrikApplyingMinGrace)
	}
}

func TestPruneRubrikHistory(t *testing.T) {
	f := newPollerFixture(t)
	ctx := t.Context()
	const maxAttempts = 3
	retention := RubrikRetention{Events: RubrikEventRetention, Operations: RubrikOperationRetention, MaxDeliveryAttempts: maxAttempts}

	recorded, err := f.s.CommitPollerTick(ctx, f.lease, f.tick(
		pollerEvent(RubrikEventSnapshotFailed, "settled"), pollerEvent(RubrikEventSnapshotFailed, "claimable"),
		pollerEvent(RubrikEventSnapshotFailed, "spent"), pollerEvent(RubrikEventSnapshotFailed, "young")))
	if err != nil || len(recorded) != 4 {
		t.Fatalf("CommitPollerTick = (%d, %v); want 4 events", len(recorded), err)
	}
	event := map[string]string{}
	for _, e := range recorded {
		event[e.DedupKey] = e.ID
	}
	setDelivery := func(key, state string, attempts int) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, `UPDATE rubrik_poller_deliveries SET state = $2, attempts = $3 WHERE event_id = $1::uuid`,
			event[key], state, attempts); err != nil {
			t.Fatal(err)
		}
	}
	setDelivery("settled", RubrikDeliveryDelivered, 1)
	setDelivery("claimable", RubrikDeliveryPending, maxAttempts-1)
	setDelivery("spent", RubrikDeliveryPending, maxAttempts)
	setDelivery("young", RubrikDeliveryDelivered, 1)
	old := retention.Events + time.Hour
	for _, key := range []string{"settled", "claimable", "spent"} {
		if _, err := f.pool.Exec(ctx, `UPDATE rubrik_poller_events SET created_at = NOW() - make_interval(secs => $2::double precision)
			WHERE id = $1::uuid`, event[key], old.Seconds()); err != nil {
			t.Fatal(err)
		}
	}

	op := func(state string, age time.Duration, finished bool) string {
		t.Helper()
		o, err := f.s.InsertOperation(ctx, newRubrikOp(f.cluster, testOwnerID(t), RubrikOpSnapshot))
		if err != nil {
			t.Fatalf("InsertOperation: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE rubrik_operations
			SET state = $2, updated_at = NOW() - make_interval(secs => $3::double precision),
			    finished_at = CASE WHEN $4::boolean THEN NOW() - make_interval(secs => $3::double precision) ELSE NULL END
			WHERE id = $1::uuid`, o.ID, state, age.Seconds(), finished); err != nil {
			t.Fatal(err)
		}
		return o.ID
	}
	oldOp := retention.Operations + time.Hour
	succeeded := op(RubrikOpSucceeded, oldOp, true)
	failedNoStamp := op(RubrikOpFailed, oldOp, false)
	unknown := op(RubrikOpUnknown, oldOp, false)
	running := op(RubrikOpRunning, oldOp, false)
	recent := op(RubrikOpPartial, retention.Operations-time.Hour, true)

	pruned, err := f.s.PruneRubrikHistory(ctx, retention)
	if err != nil {
		t.Fatalf("PruneRubrikHistory: %v", err)
	}
	// Other tests share the database, so the counts are lower bounds.
	if pruned.Events < 2 || pruned.Operations < 2 {
		t.Errorf("pruned %+v; want at least 2 events and 2 operations", pruned)
	}
	for key, kept := range map[string]bool{"settled": false, "spent": false, "claimable": true, "young": true} {
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM rubrik_poller_events WHERE id = $1::uuid`, event[key]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		var d int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM rubrik_poller_deliveries WHERE event_id = $1::uuid`, event[key]).Scan(&d); err != nil {
			t.Fatal(err)
		}
		if (n == 1) != kept || (d == 1) != kept {
			t.Errorf("event %q: %d event rows, %d delivery rows; kept = %v", key, n, d, kept)
		}
	}
	for id, kept := range map[string]bool{succeeded: false, failedNoStamp: false, unknown: true, running: true, recent: true} {
		_, err := f.s.GetOperation(ctx, id)
		if gone := errors.Is(err, ErrRubrikOperationNotFound); gone == kept || (err != nil && !gone) {
			t.Errorf("operation %s: err %v; kept = %v", id, err, kept)
		}
	}

	for _, bad := range []RubrikRetention{
		{Events: RubrikMinRetention - time.Minute, Operations: RubrikOperationRetention, MaxDeliveryAttempts: maxAttempts},
		{Events: RubrikEventRetention, Operations: RubrikMinRetention - time.Minute, MaxDeliveryAttempts: maxAttempts},
		{Events: RubrikEventRetention, Operations: RubrikOperationRetention},
	} {
		if _, err := f.s.PruneRubrikHistory(ctx, bad); err == nil {
			t.Errorf("PruneRubrikHistory(%+v) was accepted", bad)
		}
	}
}
