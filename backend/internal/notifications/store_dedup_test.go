package notifications

// store_dedup_test.go — dedup identity for the notification feed (defect #2).
//
// The dedup key used to be (source, kind, namespace, name, title). It left out
// the cluster and the object UID, so a same-named resource failing on a second
// cluster, or a resource deleted and recreated under a new UID, was silently
// suppressed as a duplicate of the first.
//
// The hermetic tests always run. The PostgreSQL-backed tests come through the
// same KUBECENTER_TEST_DATABASE_URL gate as backend/internal/store's harness
// (docs/solutions/postgres-test-harness-conventions.md): unset skips, and
// KUBECENTER_TEST_REQUIRE_DATABASE turns that skip into a failure in CI.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	testDatabaseURLEnv     = "KUBECENTER_TEST_DATABASE_URL"
	testDatabaseRequireEnv = "KUBECENTER_TEST_REQUIRE_DATABASE"
)

// testDatabaseRequired mirrors the canonical predicate in
// backend/internal/store/testdb_test.go exactly. Two gates that claim the same
// semantics must not disagree, or one package skips where the other fails.
func testDatabaseRequired(lookup func(string) (string, bool)) bool {
	v, ok := lookup(testDatabaseRequireEnv)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

var (
	// migrateOnce runs the migration pass at most once per test binary, as
	// backend/internal/store's harness does; each test then gets its own
	// small pool so a poisoned connection cannot affect a sibling.
	migrateOnce sync.Once
	migrateErr  error
)

// testNotifStore returns a Store over the migrated test database, or skips
// the calling test when none is configured. Migrations run through
// store.New, the production entry point, once per process.
func testNotifStore(t *testing.T) *Store {
	t.Helper()

	connString := strings.TrimSpace(os.Getenv(testDatabaseURLEnv))
	if connString == "" {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("%s is set but %s is empty; a database was required", testDatabaseRequireEnv, testDatabaseURLEnv)
		}
		t.Skipf("%s is not set; skipping PostgreSQL-backed notification test", testDatabaseURLEnv)
	}

	migrateOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := store.New(ctx, connString, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			migrateErr = fmt.Errorf("connecting to %s and applying migrations: %w "+
				"(if the test database was migrated by a branch with later migrations, drop and recreate it)",
				testDatabaseURLEnv, err)
			return
		}
		db.Close()
	})
	if migrateErr != nil {
		t.Fatalf("test database unavailable: %v", migrateErr)
	}

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parsing %s: %v", testDatabaseURLEnv, err)
	}
	config.MaxConns = 4
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool, "")
}

// uniqueName returns a resource name no other test (or rerun) writes, so the
// dedup lookups only ever see rows this test inserted. nc_notifications has
// no owner column; the resource name is the isolation key.
func uniqueName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generating name suffix: %v", err)
	}
	return "test-dedup-" + hex.EncodeToString(suffix[:])
}

// baseNotification is one diagnostic finding against a uniquely named pod.
func baseNotification(t *testing.T) Notification {
	t.Helper()
	return Notification{
		Source:       SourceDiagnostic,
		Severity:     SeverityWarning,
		Title:        "CrashLoopBackOff: web",
		Message:      "container restarting",
		ResourceKind: "Pod",
		ResourceNS:   "team-a",
		ResourceName: uniqueName(t),
	}
}

func mustInsert(t *testing.T, s *Store, n Notification) string {
	t.Helper()
	id, err := s.InsertNotification(t.Context(), n)
	if err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}
	return id
}

func mustDedup(t *testing.T, s *Store, n Notification) bool {
	t.Helper()
	exists, err := dedupExists(t.Context(), s.pool, n, dedupWindow)
	if err != nil {
		t.Fatalf("dedupExists: %v", err)
	}
	return exists
}

// ---------------------------------------------------------------------------
// Hermetic tests — always on.
// ---------------------------------------------------------------------------

func TestDedupClusterID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", k8s.LocalClusterID},
		{"local", k8s.LocalClusterID},
		{"prod-east", "prod-east"},
		// Not trimmed or case-folded: cluster ids are opaque, and folding
		// would merge two registrations the operator kept distinct.
		{"Local", "Local"},
		{" local", " local"},
	}
	for _, tc := range tests {
		if got := dedupClusterID(tc.in); got != tc.want {
			t.Errorf("dedupClusterID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDedupLockKey_MatchesTheDedupIdentity pins InsertDeduped's lock to the
// lookup's identity. Two emits the lookup would fold together must contend
// for one lock, or they race past each other again (#590); fields outside
// the identity must not split the lock either.
func TestDedupLockKey_MatchesTheDedupIdentity(t *testing.T) {
	base := Notification{
		Source: SourceVelero, Severity: SeverityWarning, Title: "t", Message: "m",
		ResourceKind: "Pod", ResourceNS: "ns", ResourceName: "web", ResourceUID: "uid", ClusterID: "",
	}
	key := dedupLockKey(base)

	same := base
	same.ClusterID = k8s.LocalClusterID
	same.Severity, same.Message = SeverityCritical, "other message"
	if got := dedupLockKey(same); got != key {
		t.Errorf("empty and local cluster, or a differing severity/message, split the lock:\n%q\n%q", key, got)
	}

	for name, mutate := range map[string]func(*Notification){
		"source":  func(n *Notification) { n.Source = SourceDiagnostic },
		"kind":    func(n *Notification) { n.ResourceKind = "Deployment" },
		"ns":      func(n *Notification) { n.ResourceNS = "other" },
		"name":    func(n *Notification) { n.ResourceName = "api" },
		"title":   func(n *Notification) { n.Title = "other" },
		"uid":     func(n *Notification) { n.ResourceUID = "uid-2" },
		"cluster": func(n *Notification) { n.ClusterID = "prod-east" },
	} {
		n := base
		mutate(&n)
		if dedupLockKey(n) == key {
			t.Errorf("a different %s shares the lock key %q", name, key)
		}
	}
}

// TestDedupQuery_KeysOnClusterAndUID pins the dedup statement's shape so a
// later edit cannot quietly drop either new key column. The PostgreSQL tests
// below prove the behaviour; this one keeps the guarantee visible when no
// database is available.
func TestDedupQuery_KeysOnClusterAndUID(t *testing.T) {
	// Whitespace-normalised so a reflow of the statement does not fail the
	// test, while dropping or loosening either predicate still does.
	query := strings.Join(strings.Fields(dedupExistsQuery), " ")
	_, where, ok := strings.Cut(query, " WHERE ")
	if !ok {
		t.Fatalf("dedupExistsQuery has no WHERE clause:\n%s", dedupExistsQuery)
	}
	for _, want := range []string{
		// UID compared exactly against the caller's UID ($6).
		"AND resource_uid = $6 AND",
		// Cluster compared with the empty id folded onto the local id ($8)
		// against the caller's already-folded id ($7).
		"AND (CASE WHEN cluster_id = '' THEN $8::text ELSE cluster_id END) = $7::text AND",
	} {
		if !strings.Contains(where, want) {
			t.Errorf("dedupExistsQuery WHERE clause does not contain %q:\n%s", want, dedupExistsQuery)
		}
	}
}

func TestClusterStatusNotification(t *testing.T) {
	down := ClusterStatusNotification("prod-east", "connected", "unreachable")
	if down.Severity != SeverityCritical {
		t.Errorf("severity = %q, want critical", down.Severity)
	}
	if down.Title != "Cluster prod-east is unreachable" {
		t.Errorf("title = %q", down.Title)
	}
	if down.ClusterID != "prod-east" {
		t.Errorf("ClusterID = %q, want prod-east: the change belongs to the probed cluster", down.ClusterID)
	}
	if down.Source != SourceCluster || down.Message != "Status changed from connected to unreachable" {
		t.Errorf("unexpected notification: %+v", down)
	}

	up := ClusterStatusNotification("prod-east", "unreachable", "connected")
	if up.Severity != SeverityInfo || up.Title != "Cluster prod-east is now connected" || up.ClusterID != "prod-east" {
		t.Errorf("unexpected recovery notification: %+v", up)
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL-backed tests.
// ---------------------------------------------------------------------------

func TestDedupExists_SameNameOnTwoClustersIsNotADuplicate(t *testing.T) {
	s := testNotifStore(t)

	first := baseNotification(t)
	first.ClusterID = "cluster-a"
	first.ResourceUID = "uid-1"
	mustInsert(t, s, first)

	second := first
	second.ClusterID = "cluster-b"
	if mustDedup(t, s, second) {
		t.Fatal("a same-named resource on a second cluster was suppressed as a duplicate of the first")
	}
}

func TestDedupExists_RecreatedResourceWithNewUIDIsNotADuplicate(t *testing.T) {
	s := testNotifStore(t)

	first := baseNotification(t)
	first.ClusterID = "cluster-a"
	first.ResourceUID = "uid-old"
	mustInsert(t, s, first)

	recreated := first
	recreated.ResourceUID = "uid-new"
	if mustDedup(t, s, recreated) {
		t.Fatal("a resource recreated under a new UID was suppressed as a duplicate of its predecessor")
	}
}

func TestDedupExists_TrueDuplicateIsStillSuppressed(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ClusterID = "cluster-a"
	n.ResourceUID = "uid-1"
	mustInsert(t, s, n)

	if !mustDedup(t, s, n) {
		t.Fatal("an identical notification inside the window was not deduplicated")
	}

	// A notification without a resource (cluster, policy and scan sources)
	// keeps deduplicating on the remaining key.
	bare := Notification{Source: SourceScan, Severity: SeverityInfo, Title: "scan " + uniqueName(t)}
	mustInsert(t, s, bare)
	if !mustDedup(t, s, bare) {
		t.Fatal("a resource-less notification was not deduplicated")
	}
}

func TestDedupExists_DifferentTitleOrSourceIsNotADuplicate(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ResourceUID = "uid-1"
	mustInsert(t, s, n)

	otherTitle := n
	otherTitle.Title = "ImagePullBackOff: web"
	if mustDedup(t, s, otherTitle) {
		t.Error("a different title was treated as a duplicate")
	}
	otherSource := n
	otherSource.Source = SourceAlert
	if mustDedup(t, s, otherSource) {
		t.Error("a different source was treated as a duplicate")
	}
}

// TestDedupExists_LegacyRowsAreLocal covers rows written before the fix:
// cluster_id and resource_uid are both empty (their column defaults, the
// latter since 000021). An empty cluster id has always meant the local cluster, so it
// must deduplicate against "local" and against "", and never against a
// remote cluster.
func TestDedupExists_LegacyRowsAreLocal(t *testing.T) {
	s := testNotifStore(t)

	legacy := baseNotification(t)
	legacy.ResourceUID = ""
	legacy.ClusterID = ""
	mustInsert(t, s, legacy)

	asLocal := legacy
	asLocal.ClusterID = "local"
	if !mustDedup(t, s, asLocal) {
		t.Error("a legacy row with no cluster id did not deduplicate against the local cluster")
	}
	if !mustDedup(t, s, legacy) {
		t.Error("a legacy row with no cluster id did not deduplicate against an empty cluster id")
	}
	remote := legacy
	remote.ClusterID = "cluster-b"
	if mustDedup(t, s, remote) {
		t.Error("a legacy (local) row suppressed a notification from a remote cluster")
	}
}

// TestDedupExists_ExplicitLocalMatchesEmpty is the reverse direction: a row
// written with the explicit "local" id suppresses an emitter that leaves the
// cluster id empty, so local-only sources need not agree on a spelling.
func TestDedupExists_ExplicitLocalMatchesEmpty(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ClusterID = "local"
	n.ResourceUID = "uid-1"
	mustInsert(t, s, n)

	implicit := n
	implicit.ClusterID = ""
	if !mustDedup(t, s, implicit) {
		t.Error(`a row stored with cluster id "local" did not deduplicate against an empty cluster id`)
	}
}

func TestDedupExists_OutsideWindowIsNotADuplicate(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ResourceUID = "uid-1"
	id := mustInsert(t, s, n)

	if _, err := s.pool.Exec(t.Context(),
		`UPDATE nc_notifications SET created_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatalf("aging row: %v", err)
	}
	if mustDedup(t, s, n) {
		t.Fatal("a row older than the dedup window still suppressed a new notification")
	}
}

// TestDedupExists_WindowIsMeasuredWhenTheLookupRuns covers the lookup inside
// InsertDeduped's transaction, which runs only after the advisory lock is
// granted. now() is the transaction start, so a cutoff built on it would
// stretch the window by the lock wait and report a row that has already aged
// out as a duplicate, swallowing the new notification. The sleep stands in
// for the lock wait, and a one-second window keeps the test short.
func TestDedupExists_WindowIsMeasuredWhenTheLookupRuns(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	mustInsert(t, s, n)

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT pg_sleep(1.5)`); err != nil {
		t.Fatalf("sleeping inside the transaction: %v", err)
	}
	exists, err := dedupExists(t.Context(), tx, n, time.Second)
	if err != nil {
		t.Fatalf("dedupExists: %v", err)
	}
	if exists {
		t.Fatal("a row 1.5s old matched a 1s window because the cutoff used the transaction start")
	}
}

func TestInsertAndList_RoundTripResourceUID(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ClusterID = "cluster-a"
	n.ResourceUID = "uid-roundtrip"
	since := time.Now().Add(-time.Minute)
	id := mustInsert(t, s, n)

	var stored string
	if err := s.pool.QueryRow(t.Context(),
		`SELECT resource_uid FROM nc_notifications WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("reading resource_uid: %v", err)
	}
	if stored != "uid-roundtrip" {
		t.Fatalf("stored resource_uid = %q, want uid-roundtrip", stored)
	}

	list, _, err := s.ListNotifications(t.Context(), ListOpts{
		UserID:     uniqueName(t),
		Namespaces: []string{"team-a"},
		Source:     SourceDiagnostic,
		Since:      since,
		Limit:      200,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	for _, got := range list {
		if got.ID != id {
			continue
		}
		if got.ResourceUID != "uid-roundtrip" || got.ClusterID != "cluster-a" {
			t.Fatalf("listed notification lost its identity: uid=%q cluster=%q", got.ResourceUID, got.ClusterID)
		}
		return
	}
	t.Fatalf("inserted notification %s not returned by ListNotifications", id)
}

// The two other readers of resource_uid select it next to cluster_id. Both
// columns are text, so a SELECT/Scan misalignment would swap the values
// rather than fail; the fixtures use distinct, recognisable values for each
// field so a swap is caught.

func TestRecentBySource_RoundTripResourceUIDAndCluster(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	n.ClusterID = "cluster-rt-" + uniqueName(t)
	n.ResourceUID = "uid-recent"
	since := time.Now().Add(-time.Minute)
	id := mustInsert(t, s, n)

	got, err := s.RecentBySource(t.Context(), SourceDiagnostic, n.ClusterID, since)
	if err != nil {
		t.Fatalf("RecentBySource: %v", err)
	}
	for _, r := range got {
		if r.ID != id {
			continue
		}
		if r.ResourceUID != "uid-recent" || r.ClusterID != n.ClusterID {
			t.Fatalf("RecentBySource lost the identity: uid=%q cluster=%q", r.ResourceUID, r.ClusterID)
		}
		if r.ResourceName != n.ResourceName || r.ResourceNS != "team-a" || r.ResourceKind != "Pod" || r.Title != n.Title {
			t.Fatalf("RecentBySource scanned columns out of order: %+v", r)
		}
		return
	}
	t.Fatalf("inserted notification %s not returned by RecentBySource", id)
}

func TestNotificationsSince_RoundTripResourceUIDAndCluster(t *testing.T) {
	s := testNotifStore(t)

	n := baseNotification(t)
	// A namespace no other test writes keeps the LIMIT 500 window to this
	// test's rows plus resource-less rows of the filtered source.
	n.ResourceNS = uniqueName(t)
	n.ClusterID = "cluster-since"
	n.ResourceUID = "uid-since"
	since := time.Now().Add(-time.Minute)
	id := mustInsert(t, s, n)

	got, err := s.NotificationsSince(t.Context(), since, []string{n.ResourceNS},
		[]string{string(SourceDiagnostic)}, []string{string(SeverityWarning)})
	if err != nil {
		t.Fatalf("NotificationsSince: %v", err)
	}
	for _, r := range got {
		if r.ID != id {
			continue
		}
		if r.ResourceUID != "uid-since" || r.ClusterID != "cluster-since" {
			t.Fatalf("NotificationsSince lost the identity: uid=%q cluster=%q", r.ResourceUID, r.ClusterID)
		}
		if r.ResourceName != n.ResourceName || r.ResourceNS != n.ResourceNS || r.ResourceKind != "Pod" || r.Title != n.Title {
			t.Fatalf("NotificationsSince scanned columns out of order: %+v", r)
		}
		return
	}
	t.Fatalf("inserted notification %s not returned by NotificationsSince", id)
}
