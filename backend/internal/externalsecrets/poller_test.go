package externalsecrets

import (
	"testing"
)

// pollerES builds a minimal ExternalSecret with the given UID + Status.
// Defaults give the resolver enough to compute a stale-default. Override
// AlertOnRecovery / AlertOnLifecycle directly when a test needs them.
func pollerES(uid string, status Status) ExternalSecret {
	return ExternalSecret{
		UID:             uid,
		Namespace:       "apps",
		Name:            "es-" + uid,
		Status:          status,
		StoreRef:        StoreRef{Name: "vault", Kind: "SecretStore"},
		RefreshInterval: "1h",
	}
}

// observe seeds a poller's prevBucket with the given (UID, Status). Caller
// is asserting "this ES was at status X on the previous tick" without
// going through the full check() flow.
func observe(p *Poller, es ExternalSecret) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prevBucket[es.UID] = bucketFor(es.Status)
	p.seen[es.UID] = true
	p.initialized = true
}

// AE1 from plan §Unit 13 test scenarios: ES Synced -> SyncFailed emits
// sync_failed once. Subsequent ticks with same Ready=False -> no re-fire.
func TestPoller_AE1_FailureFiresOnce(t *testing.T) {
	p := newPollerForTest()
	es := pollerES("u1", StatusSynced)
	observe(p, es)

	failing := pollerES("u1", StatusSyncFailed)

	// First failure: emit.
	recs1 := p.check(failing)
	if len(recs1) != 1 || recs1[0].Kind != EventSyncFailed {
		t.Fatalf("first SyncFailed: got %v records (kinds %v); want 1 sync_failed", len(recs1), kindsOf(recs1))
	}

	// Second tick still failing: no re-fire.
	recs2 := p.check(failing)
	if len(recs2) != 0 {
		t.Fatalf("second SyncFailed (still failing): got %v records (kinds %v); want 0", len(recs2), kindsOf(recs2))
	}
}

// AE2: ES SyncFailed -> Ready=True emits recovered once. The sync_failed
// dedupe entry does NOT suppress recovery (separate dedupe slot — but our
// implementation uses the bucket transition as the gate, so this is
// implicit: prev=Failed, current=Healthy, AlertOnRecovery=true -> emit).
func TestPoller_AE2_RecoveryFiresOnceWhenAlertOnRecovery(t *testing.T) {
	p := newPollerForTest()

	failing := pollerES("u1", StatusSyncFailed)
	failing.AlertOnRecovery = boolPtr(true)
	observe(p, failing)

	recovered := pollerES("u1", StatusSynced)
	recovered.AlertOnRecovery = boolPtr(true)

	recs := p.check(recovered)
	if len(recs) != 1 || recs[0].Kind != EventRecovered {
		t.Fatalf("recovery: got %v records (kinds %v); want 1 recovered", len(recs), kindsOf(recs))
	}

	// Stay healthy on next tick: no re-fire.
	recs2 := p.check(recovered)
	if len(recs2) != 0 {
		t.Fatalf("recovery still healthy: got %v records (kinds %v); want 0", len(recs2), kindsOf(recs2))
	}
}

// Reason change while still failing -> no re-fire. The dedupe key is
// (uid, kind), NOT (uid, reason). Prevents reason-flap noise (plan AE1
// footnote).
func TestPoller_ReasonChangeNoRefire(t *testing.T) {
	p := newPollerForTest()

	healthy := pollerES("u1", StatusSynced)
	observe(p, healthy)

	// Tick 1: enters SyncFailed with reason A — fires once.
	es := pollerES("u1", StatusSyncFailed)
	es.ReadyReason = "AuthFailed"
	if got := p.check(es); len(got) != 1 {
		t.Fatalf("initial failure: got %d records; want 1", len(got))
	}

	// Tick 2: still SyncFailed but reason changes — no re-fire.
	es.ReadyReason = "PathNotFound"
	if got := p.check(es); len(got) != 0 {
		t.Fatalf("reason flap: got %d records; want 0", len(got))
	}
}

// alert-on-recovery=false suppresses the recovery event.
func TestPoller_AlertOnRecoveryFalse(t *testing.T) {
	p := newPollerForTest()

	failing := pollerES("u1", StatusSyncFailed)
	failing.AlertOnRecovery = boolPtr(false)
	observe(p, failing)

	recovered := pollerES("u1", StatusSynced)
	recovered.AlertOnRecovery = boolPtr(false)

	recs := p.check(recovered)
	if len(recs) != 0 {
		t.Fatalf("alert-on-recovery=false: got %v records (kinds %v); want 0", len(recs), kindsOf(recs))
	}
}

// Lifecycle off by default — a created ES does NOT fire created/first_synced
// unless AlertOnLifecycle is true at some level of the chain.
func TestPoller_LifecycleOffByDefault(t *testing.T) {
	p := newPollerForTest()
	p.mu.Lock()
	p.initialized = true // simulate not-the-first-tick
	p.mu.Unlock()

	// AlertOnLifecycle nil (default): no emit.
	es := pollerES("u-new", StatusSynced)
	if got := p.check(es); len(got) != 0 {
		t.Fatalf("lifecycle off (default): got %d records; want 0", len(got))
	}

	// New ES with AlertOnLifecycle=true: fires first_synced (Synced state).
	p2 := newPollerForTest()
	p2.mu.Lock()
	p2.initialized = true
	p2.mu.Unlock()
	es2 := pollerES("u-new2", StatusSynced)
	es2.AlertOnLifecycle = boolPtr(true)
	got := p2.check(es2)
	if len(got) != 1 || got[0].Kind != EventFirstSynced {
		t.Fatalf("lifecycle on, Synced: got %v; want 1 first_synced", kindsOf(got))
	}
}

// Bucket dedupe correctness: Synced -> Stale -> Synced emits one stale
// and one recovered, and the dedupe slot is properly cleared.
func TestPoller_BucketCleanRoundtrip(t *testing.T) {
	p := newPollerForTest()

	healthy := pollerES("u1", StatusSynced)
	healthy.AlertOnRecovery = boolPtr(true)
	observe(p, healthy)

	// -> Stale: fires stale.
	stale := pollerES("u1", StatusStale)
	stale.AlertOnRecovery = boolPtr(true)
	if got := p.check(stale); len(got) != 1 || got[0].Kind != EventStale {
		t.Fatalf("healthy -> stale: got %v; want 1 stale", kindsOf(got))
	}

	// Stays stale: no re-fire.
	if got := p.check(stale); len(got) != 0 {
		t.Fatalf("stale -> stale: got %v; want 0", kindsOf(got))
	}

	// -> Synced: fires recovered.
	recovered := pollerES("u1", StatusSynced)
	recovered.AlertOnRecovery = boolPtr(true)
	if got := p.check(recovered); len(got) != 1 || got[0].Kind != EventRecovered {
		t.Fatalf("stale -> healthy: got %v; want 1 recovered", kindsOf(got))
	}

	// Goes stale again: fires stale fresh (dedupe was cleared on transition).
	stale2 := pollerES("u1", StatusStale)
	stale2.AlertOnRecovery = boolPtr(true)
	if got := p.check(stale2); len(got) != 1 || got[0].Kind != EventStale {
		t.Fatalf("re-entry to stale: got %v; want 1 stale", kindsOf(got))
	}
}

// First-tick suppression: a brand-new poller's first observation of an ES
// records the bucket but does not emit. AE1 only fires on a subsequent
// tick where the bucket has actually transitioned.
func TestPoller_FirstTickSuppressesEmit(t *testing.T) {
	p := newPollerForTest()
	es := pollerES("u1", StatusSyncFailed)

	// First check: poller has no prior bucket — record only, no emit.
	if got := p.check(es); len(got) != 0 {
		t.Fatalf("first observation: got %v; want 0 (no prior state)", kindsOf(got))
	}

	// Second check: still failing, same bucket — no emit.
	if got := p.check(es); len(got) != 0 {
		t.Fatalf("second observation (same bucket): got %v; want 0", kindsOf(got))
	}
}

// Lateral failure transition: SyncFailed -> Stale on the same UID emits
// the new failure-class event because the dedupe slot is per-kind.
func TestPoller_LateralFailureTransition(t *testing.T) {
	p := newPollerForTest()

	failing := pollerES("u1", StatusSyncFailed)
	observe(p, failing)

	stale := pollerES("u1", StatusStale)
	if got := p.check(stale); len(got) != 1 || got[0].Kind != EventStale {
		t.Fatalf("failed -> stale: got %v; want 1 stale", kindsOf(got))
	}
}

func kindsOf(recs []emitRecord) []EventKind {
	out := make([]EventKind, len(recs))
	for i, r := range recs {
		out[i] = r.Kind
	}
	return out
}
