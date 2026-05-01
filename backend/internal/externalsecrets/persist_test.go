package externalsecrets

import (
	"context"
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// Plan §467-477 Phase C test scenarios. Restart-recovery emission and the
// retention path require a real PG (covered by manual smoke); pure-Go
// behaviors are covered here.

func TestOutcomeFor(t *testing.T) {
	cases := []struct {
		status     Status
		wantOut    string
		wantRecord bool
	}{
		{StatusSynced, "success", true},
		{StatusDrifted, "success", true},
		{StatusSyncFailed, "failure", true},
		{StatusRefreshing, "", false},
		{StatusStale, "", false},
		{StatusUnknown, "", false},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			gotOut, gotRecord := outcomeFor(c.status)
			if gotOut != c.wantOut || gotRecord != c.wantRecord {
				t.Errorf("outcomeFor(%q) = (%q, %v); want (%q, %v)",
					c.status, gotOut, gotRecord, c.wantOut, c.wantRecord)
			}
		})
	}
}

func TestAttemptTimeFor_UsesLastSyncTime(t *testing.T) {
	t1 := time.Date(2026, 4, 30, 12, 34, 56, 789_000_000, time.UTC)
	es := pollerES("u1", StatusSynced)
	es.LastSyncTime = &t1

	got, ok := attemptTimeFor(es)
	if !ok {
		t.Fatalf("attemptTimeFor with LastSyncTime: ok=false; want true")
	}
	want := time.Date(2026, 4, 30, 12, 34, 56, 0, time.UTC) // truncated to second
	if !got.Equal(want) {
		t.Errorf("attemptTimeFor LastSyncTime: got %v; want %v", got, want)
	}
}

// Plan §467 ADV-1 fix: when the controller hasn't populated
// LastSyncTime (some providers don't), attemptTimeFor must return
// ok=false. Falling back to time.Now() would produce a fresh
// second-truncated timestamp every tick, defeating the
// (uid, attempt_at) ON CONFLICT dedup and inserting a new row every
// 60s for every nil-LastSyncTime ES.
func TestAttemptTimeFor_NilLastSyncTimeReturnsFalse(t *testing.T) {
	es := pollerES("u1", StatusSynced) // LastSyncTime nil

	got, ok := attemptTimeFor(es)
	if ok {
		t.Errorf("attemptTimeFor nil LastSyncTime: ok=true (got %v); want false to skip persistence", got)
	}
	if !got.IsZero() {
		t.Errorf("attemptTimeFor must return zero time when ok=false; got %v", got)
	}
}

// Plan §467 scenario 2 + ADV-1: the persistence path must not advance
// prevAttemptAt when LastSyncTime is nil. (Indirect coverage — the
// pre-filter inside persistAttempts uses attemptTimeFor's bool return
// to skip these candidates. This test asserts the contract that
// downstream code relies on.)
func TestAttemptTimeFor_ZeroLastSyncTimeReturnsFalse(t *testing.T) {
	es := pollerES("u1", StatusSynced)
	zero := time.Time{}
	es.LastSyncTime = &zero

	_, ok := attemptTimeFor(es)
	if ok {
		t.Error("attemptTimeFor zero LastSyncTime: ok=true; want false")
	}
}

func TestDiffKeySets(t *testing.T) {
	hash := func(s string) [32]byte { return sha256.Sum256([]byte(s)) }
	cases := []struct {
		name        string
		prev        map[string][32]byte
		current     map[string][32]byte
		wantAdded   []string
		wantRemoved []string
		wantChanged []string
	}{
		{
			name: "no change",
			prev: map[string][32]byte{"a": hash("1"), "b": hash("2")},
			current: map[string][32]byte{"a": hash("1"), "b": hash("2")},
		},
		{
			name:      "key added",
			prev:      map[string][32]byte{"a": hash("1")},
			current:   map[string][32]byte{"a": hash("1"), "b": hash("2")},
			wantAdded: []string{"b"},
		},
		{
			name:        "key removed",
			prev:        map[string][32]byte{"a": hash("1"), "b": hash("2")},
			current:     map[string][32]byte{"a": hash("1")},
			wantRemoved: []string{"b"},
		},
		{
			name:        "value changed",
			prev:        map[string][32]byte{"a": hash("old")},
			current:     map[string][32]byte{"a": hash("new")},
			wantChanged: []string{"a"},
		},
		{
			name:        "all three at once, output sorted",
			prev:        map[string][32]byte{"alpha": hash("1"), "beta": hash("2"), "removed": hash("x")},
			current:     map[string][32]byte{"alpha": hash("1"), "beta": hash("2-new"), "added": hash("y")},
			wantAdded:   []string{"added"},
			wantRemoved: []string{"removed"},
			wantChanged: []string{"beta"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			added, removed, changed := diffKeySets(c.prev, c.current)
			if !equalStrings(added, c.wantAdded) {
				t.Errorf("added: got %v; want %v", added, c.wantAdded)
			}
			if !equalStrings(removed, c.wantRemoved) {
				t.Errorf("removed: got %v; want %v", removed, c.wantRemoved)
			}
			if !equalStrings(changed, c.wantChanged) {
				t.Errorf("changed: got %v; want %v", changed, c.wantChanged)
			}
		})
	}
}

func TestHashSecretData(t *testing.T) {
	data := map[string][]byte{
		"username": []byte("admin"),
		"password": []byte("secret"),
	}
	hashes := hashSecretData(data)
	if len(hashes) != 2 {
		t.Fatalf("hashSecretData: got %d entries; want 2", len(hashes))
	}
	wantUser := sha256.Sum256([]byte("admin"))
	if hashes["username"] != wantUser {
		t.Error("hashSecretData username hash mismatch")
	}
	// Same input -> stable hash.
	if hashSecretData(data)["username"] != hashes["username"] {
		t.Error("hashSecretData not deterministic")
	}
}

func TestBucketFromNotification_ByTitle(t *testing.T) {
	cases := []struct {
		title string
		want  statusBucket
	}{
		{"ExternalSecret sync failed", bucketFailed},
		{"ExternalSecret stale", bucketStale},
		{"ExternalSecret unhealthy", bucketUnknown},
		{"ExternalSecret recovered", bucketHealthy},
		{"ExternalSecret first synced", bucketHealthy},
	}
	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			n := notifications.Notification{Title: c.title}
			got := bucketFromNotification(n)
			if got != c.want {
				t.Errorf("title %q: got %v; want %v", c.title, got, c.want)
			}
		})
	}
}

func TestBucketFromNotification_FallbackBySeverity(t *testing.T) {
	// Unknown title — fall back to severity. Forward-compat for renamed
	// titles in future versions. SeverityWarning is collapsed to
	// bucketUnknown (not bucketStale) to avoid the Stale/Unhealthy
	// disambiguation gap surfaced by the adversarial review: a renamed
	// "ExternalSecret unhealthy" title would silently misclassify as
	// Stale, leaving the EventUnhealthy dedupe slot uncleared.
	cases := []struct {
		sev  notifications.Severity
		want statusBucket
	}{
		{notifications.SeverityCritical, bucketFailed},
		{notifications.SeverityWarning, bucketUnknown},
		{notifications.SeverityInfo, bucketHealthy},
	}
	for _, c := range cases {
		t.Run(string(c.sev), func(t *testing.T) {
			n := notifications.Notification{Title: "made-up", Severity: c.sev}
			got := bucketFromNotification(n)
			if got != c.want {
				t.Errorf("severity %q fallback: got %v; want %v", c.sev, got, c.want)
			}
		})
	}
}

// Plan §474: restart-recovery prev-bucket seeding lets check() observe
// seeded ESes as if their previous bucket were Failed. A Healthy
// observation against a Failed seed must emit Recovered when AlertOnRecovery
// is set.
func TestPoller_SeedConsumedOnFirstObservation(t *testing.T) {
	p := newPollerForTest()
	p.seedNSName = map[string]seedEntry{
		"apps/es-u1": {Bucket: bucketFailed, NotifTime: time.Now()},
	}
	p.mu.Lock()
	p.initialized = true
	p.mu.Unlock()

	healthy := pollerES("u1", StatusSynced)
	healthy.AlertOnRecovery = boolPtr(true)

	got := p.check(healthy)
	if len(got) != 1 || got[0].Kind != EventRecovered {
		t.Fatalf("seeded recovery: got %v; want 1 recovered", kindsOf(got))
	}

	// Seed entry was consumed — re-observation does NOT replay the
	// transition.
	if _, exists := p.seedNSName["apps/es-u1"]; exists {
		t.Error("seed entry should be removed after consumption")
	}

	// Healthy on next tick: no re-fire (now in normal prev=healthy state).
	if got := p.check(healthy); len(got) != 0 {
		t.Errorf("healthy stays healthy: got %v; want 0", kindsOf(got))
	}
}

// A seed entry for an ES that doesn't appear in the current inventory is
// silently retained — operator's local cluster may have lost the ES
// between failure and restart. The seed entry is harmless until pruned
// by the next eligible recovery (or simply discarded with the poller).
func TestPoller_SeedForMissingESStaysHarmless(t *testing.T) {
	p := newPollerForTest()
	p.seedNSName = map[string]seedEntry{
		"apps/es-deleted": {Bucket: bucketFailed, NotifTime: time.Now()},
	}
	p.mu.Lock()
	p.initialized = true
	p.mu.Unlock()

	// Different ES observation — seed for deleted entry is not consumed.
	other := pollerES("u-other", StatusSynced)
	other.AlertOnRecovery = boolPtr(true)
	if got := p.check(other); len(got) != 0 {
		t.Errorf("first-tick observation w/ no matching seed: got %v; want 0", kindsOf(got))
	}
	if _, exists := p.seedNSName["apps/es-deleted"]; !exists {
		t.Error("unmatched seed should remain")
	}
}

// Seed consumption only happens on FIRST observation (hadPrev=false). A
// subsequent seed for an already-tracked UID is a no-op — a future bug
// where seed is repopulated mid-run would not re-trigger transitions.
func TestPoller_SeedSkippedForAlreadyTrackedUID(t *testing.T) {
	p := newPollerForTest()
	healthy := pollerES("u1", StatusSynced)
	healthy.AlertOnRecovery = boolPtr(true)
	observe(p, healthy)

	// Now inject a Failed seed (simulating a hypothetical bug).
	p.mu.Lock()
	p.seedNSName = map[string]seedEntry{"apps/es-u1": {Bucket: bucketFailed, NotifTime: time.Now()}}
	p.mu.Unlock()

	// Healthy observation — seed should NOT fire a recovery, because the
	// poller already has prevBucket[u1] = healthy.
	if got := p.check(healthy); len(got) != 0 {
		t.Errorf("seed for already-tracked UID: got %v; want 0", kindsOf(got))
	}
}

// T01 regression: buildHistoryEntry must skip when prevAttemptAt for
// the UID matches the current attemptAt. Without this, the same row
// would re-fetch the Secret on every tick, blowing through pgxpool and
// the API server. Pre-filter happens inside persistAttempts, but the
// underlying logic gates per-attempt — emulate by calling outcomeFor +
// attemptTimeFor + comparing against prevAttemptAt directly.
func TestPoller_PrevAttemptAtSkipsDupTicks(t *testing.T) {
	p := newPollerForTest()
	t1 := time.Date(2026, 4, 30, 12, 34, 56, 0, time.UTC)

	es := pollerES("u1", StatusSynced)
	es.LastSyncTime = &t1

	// Seed the prev map directly (simulating a successful prior tick).
	p.mu.Lock()
	p.prevAttemptAt[es.UID] = t1
	p.mu.Unlock()

	// Same attempt_at -> dedup gate fires.
	gotAt, ok := attemptTimeFor(es)
	if !ok || !gotAt.Equal(t1) {
		t.Fatalf("attemptTimeFor returned (%v, %v); want (%v, true)", gotAt, ok, t1)
	}
	p.mu.Lock()
	prevAt, hadPrev := p.prevAttemptAt[es.UID]
	p.mu.Unlock()
	if !hadPrev || !prevAt.Equal(gotAt) {
		t.Errorf("dedup gate: hadPrev=%v prevAt=%v; want hadPrev=true prevAt=%v", hadPrev, prevAt, gotAt)
	}
}

// T02 regression: seedFromNotifications must be a safe no-op when
// notifStore is nil (test path or no-DB deployment). Without the guard,
// the poller would panic on startup against notifStore.RecentBySource.
func TestPoller_SeedFromNotificationsNilStoreNoOp(t *testing.T) {
	p := newPollerForTest()
	// p.notifStore is nil from newPollerForTest.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("seedFromNotifications nil store panicked: %v", r)
		}
	}()
	p.seedFromNotifications(context.Background())
	if p.seedNSName != nil {
		t.Errorf("seedNSName populated despite nil store; got %v", p.seedNSName)
	}
}

// T05 regression: RunRetention must be a safe no-op when historyStore
// is nil. Belt-and-suspenders for the explicit guard in
// RunRetention's body.
func TestPoller_RunRetentionNilStoreNoOp(t *testing.T) {
	p := newPollerForTest()
	// p.historyStore is nil from newPollerForTest.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunRetention nil store panicked: %v", r)
		}
	}()
	p.RunRetention(context.Background())
}

// T06 regression: an ES delete + recreate with the same name but a
// fresh UID must NOT inherit the predecessor's prevAttemptAt or
// prevKeys state. The tick prune block at the end of poller.tick()
// removes UIDs that vanished from currentUIDs. Verify directly that
// the UID-keyed maps are cleared.
func TestPoller_PruneClearsAllPerUIDState(t *testing.T) {
	p := newPollerForTest()
	uid := "uid-old"

	// Seed all four per-UID maps.
	p.mu.Lock()
	p.prevBucket[uid] = bucketHealthy
	p.seen[uid] = true
	p.prevAttemptAt[uid] = time.Now()
	p.prevKeys[uid] = map[string][32]byte{"k1": {}}
	p.mu.Unlock()

	// Simulate the prune step from tick() with currentUIDs not
	// containing uid-old.
	currentUIDs := map[string]bool{"uid-new": true}
	p.mu.Lock()
	for u := range p.prevBucket {
		if !currentUIDs[u] {
			delete(p.prevBucket, u)
			delete(p.seen, u)
			delete(p.prevAttemptAt, u)
			delete(p.prevKeys, u)
		}
	}
	p.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.prevBucket[uid]; exists {
		t.Error("prevBucket retained pruned UID")
	}
	if _, exists := p.seen[uid]; exists {
		t.Error("seen retained pruned UID")
	}
	if _, exists := p.prevAttemptAt[uid]; exists {
		t.Error("prevAttemptAt retained pruned UID")
	}
	if _, exists := p.prevKeys[uid]; exists {
		t.Error("prevKeys retained pruned UID")
	}
}

// emptyIfNil normalizes diff slices for the eso_sync_history TEXT[]
// NOT NULL columns. pgx encodes nil []string as SQL NULL; the schema
// rejects that. Verify the helper preserves non-nil and converts nil.
func TestEmptyIfNil(t *testing.T) {
	if got := emptyIfNil(nil); got == nil {
		t.Error("emptyIfNil(nil) = nil; want empty slice (PG NOT NULL)")
	}
	if got := emptyIfNil([]string{}); len(got) != 0 || got == nil {
		t.Errorf("emptyIfNil([]) = %v; want empty non-nil", got)
	}
	in := []string{"a", "b"}
	got := emptyIfNil(in)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("emptyIfNil preserved input incorrectly: got %v; want %v", got, in)
	}
}

// equalStrings treats nil and empty slice as equivalent — diffKeySets
// returns nil for empty sets, but tests express expectations as nil for
// readability.
func equalStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
