package externalsecrets

import (
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

func TestAttemptTimeFor_PrefersLastSyncTime(t *testing.T) {
	t1 := time.Date(2026, 4, 30, 12, 34, 56, 789_000_000, time.UTC)
	es := pollerES("u1", StatusSynced)
	es.LastSyncTime = &t1

	got := attemptTimeFor(es)
	want := time.Date(2026, 4, 30, 12, 34, 56, 0, time.UTC) // truncated to second
	if !got.Equal(want) {
		t.Errorf("attemptTimeFor LastSyncTime: got %v; want %v", got, want)
	}
}

func TestAttemptTimeFor_FallsBackToNow(t *testing.T) {
	es := pollerES("u1", StatusSynced) // LastSyncTime nil

	before := time.Now().UTC().Truncate(time.Second)
	got := attemptTimeFor(es)
	after := time.Now().UTC().Truncate(time.Second).Add(time.Second)

	if got.Before(before) || got.After(after) {
		t.Errorf("attemptTimeFor fallback: got %v; want between %v and %v", got, before, after)
	}
	if got.Nanosecond() != 0 {
		t.Errorf("attemptTimeFor must be second-truncated; got nanos=%d", got.Nanosecond())
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
	// titles in future versions.
	cases := []struct {
		sev  notifications.Severity
		want statusBucket
	}{
		{notifications.SeverityCritical, bucketFailed},
		{notifications.SeverityWarning, bucketStale},
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
	p.seedNSName = map[string]statusBucket{
		"apps/es-u1": bucketFailed,
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
	p.seedNSName = map[string]statusBucket{
		"apps/es-deleted": bucketFailed,
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
	p.seedNSName = map[string]statusBucket{"apps/es-u1": bucketFailed}
	p.mu.Unlock()

	// Healthy observation — seed should NOT fire a recovery, because the
	// poller already has prevBucket[u1] = healthy.
	if got := p.check(healthy); len(got) != 0 {
		t.Errorf("seed for already-tracked UID: got %v; want 0", kindsOf(got))
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
