package velero

// assurance_service_db_test.go — PostgreSQL-backed coverage for the Release
// F U34b collector: acceptance example AE8 end to end over the real store
// and the real Notification Center service, the competing-replica proof
// (R-3) and the lease hand-over.
//
// Gated exactly as the store, notifications and preferences harnesses are:
// KUBECENTER_TEST_DATABASE_URL unset skips, KUBECENTER_TEST_REQUIRE_DATABASE
// turns the skip into a failure in CI. Migrations run once per process
// through store.New, the production entry point. Every row a test writes
// carries a cluster id unique to that test, so suites can share a database
// without truncation.

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/internal/websocket"
)

const (
	testDatabaseURLEnv     = "KUBECENTER_TEST_DATABASE_URL"
	testDatabaseRequireEnv = "KUBECENTER_TEST_REQUIRE_DATABASE"
)

// testDatabaseRequired mirrors the canonical predicate in
// internal/store/testdb_test.go: "", "0", "false" and "no" mean not required.
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
	assuranceMigrateOnce sync.Once
	assuranceMigrateErr  error
)

// assuranceTestPool returns a pool over the migrated test database or skips.
func assuranceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connString := strings.TrimSpace(os.Getenv(testDatabaseURLEnv))
	if connString == "" {
		if testDatabaseRequired(os.LookupEnv) {
			t.Fatalf("%s is set but %s is empty; a database was required", testDatabaseRequireEnv, testDatabaseURLEnv)
		}
		t.Skipf("%s is not set; skipping PostgreSQL-backed assurance collector test", testDatabaseURLEnv)
	}
	assuranceMigrateOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db, err := store.New(ctx, connString, 0, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			assuranceMigrateErr = fmt.Errorf("connecting to %s and applying migrations: %w", testDatabaseURLEnv, err)
			return
		}
		db.Close()
	})
	if assuranceMigrateErr != nil {
		t.Fatalf("test database unavailable: %v", assuranceMigrateErr)
	}
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parsing %s: %v", testDatabaseURLEnv, err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// dbHarness is a collector over the real store and the real notification
// service, reading the local fake cluster of an embedded handler harness.
type dbHarness struct {
	*harness
	pool      *pgxpool.Pool
	st        *store.BackupAssuranceStore
	notif     *notifications.NotificationService
	clusterID string
	logger    *slog.Logger
	svc       *AssuranceService
}

func newAssuranceDBHarness(t *testing.T, objs ...*unstructured.Unstructured) *dbHarness {
	t.Helper()
	pool := assuranceTestPool(t)
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &dbHarness{
		harness:   newLocalHarness(t, objs...),
		pool:      pool,
		st:        store.NewBackupAssuranceStore(pool),
		notif:     notifications.NewService(notifications.NewStore(pool, ""), websocket.NewHub(logger, nil), nil, nil, logger),
		clusterID: "test-u34b-" + hex.EncodeToString(suffix[:]),
		logger:    logger,
	}
	// The public constructor: the same wiring U34c performs in main.go.
	d.svc = NewAssuranceService(d.h, d.h.Discoverer, d.st, d.notif, d.clusterID, "holder-a", logger)
	if !d.svc.Snapshot().Enabled {
		t.Fatal("service built with a store and a notification service is disabled")
	}
	return d
}

func (d *dbHarness) addPolicy(t *testing.T, name string) store.BackupAssurancePolicy {
	t.Helper()
	p := testSchedulePolicy(d.clusterID, name)
	if err := d.st.InsertPolicy(t.Context(), p); err != nil {
		t.Fatalf("InsertPolicy: %v", err)
	}
	return p
}

func (d *dbHarness) exceptions(t *testing.T) []store.BackupAssuranceException {
	t.Helper()
	out, _, err := d.st.ListExceptions(t.Context(), d.clusterID, store.AssuranceExceptionQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ListExceptions: %v", err)
	}
	return out
}

type deliveryRow struct {
	transition, state string
	attempts          int
}

func (d *dbHarness) deliveries(t *testing.T) []deliveryRow {
	t.Helper()
	rows, err := d.pool.Query(t.Context(), `
		SELECT dl.transition, dl.state, dl.attempts
		  FROM backup_assurance_deliveries dl
		  JOIN backup_assurance_exceptions e ON e.id = dl.exception_id
		 WHERE e.cluster_id = $1
		 ORDER BY dl.created_at, dl.id`, d.clusterID)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var r deliveryRow
		if err := rows.Scan(&r.transition, &r.state, &r.attempts); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// notificationTitles is every Velero notification persisted for this
// test's cluster, oldest first: the browserless evidence AE8 asks for.
func (d *dbHarness) notificationTitles(t *testing.T) []string {
	t.Helper()
	rows, err := d.pool.Query(t.Context(),
		`SELECT title FROM nc_notifications WHERE cluster_id = $1 AND source = $2 ORDER BY created_at, id`,
		d.clusterID, string(notifications.SourceVelero))
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		out = append(out, title)
	}
	return out
}

func (d *dbHarness) tick(t *testing.T, svc *AssuranceService) {
	t.Helper()
	svc.tick(t.Context())
	if s := svc.Snapshot(); s.LastError != "" {
		t.Fatalf("tick failed: %s (see logs)", s.LastError)
	}
}

// assertAE8Opened checks the state AE8 step 2 leaves behind: one open
// overdue exception, one delivered "opened" intent, one feed entry.
func (d *dbHarness) assertAE8Opened(t *testing.T, observations int64) {
	t.Helper()
	es := d.exceptions(t)
	if len(es) != 1 || es[0].State != store.AssuranceStateOpen || es[0].Condition != store.ConditionOverdue || es[0].SubjectUID != "uid-daily" {
		t.Fatalf("exceptions = %+v; want one open overdue for uid-daily", es)
	}
	if es[0].ObservationCount != observations {
		t.Errorf("observation_count = %d, want %d", es[0].ObservationCount, observations)
	}
	if ds := d.deliveries(t); len(ds) != 1 || ds[0] != (deliveryRow{store.AssuranceTransitionOpened, store.AssuranceDeliveryDelivered, 1}) {
		t.Fatalf("deliveries = %+v; want one delivered 'opened' with 1 attempt", ds)
	}
	if titles := d.notificationTitles(t); len(titles) != 1 || titles[0] != TitleBackupOverdue {
		t.Fatalf("persisted notifications = %v; want exactly [%q]", titles, TitleBackupOverdue)
	}
}

// ---------------------------------------------------------------------------
// AE8
// ---------------------------------------------------------------------------

func TestAssurance_AE8_TransitionEmitsExactlyOnceWithoutBrowser(t *testing.T) {
	d := newAssuranceDBHarness(t, overdueCluster(time.Now())...)
	d.addPolicy(t, "daily")
	d.tick(t, d.svc)
	d.assertAE8Opened(t, 1)
	if s := d.svc.Snapshot(); !s.LeaseHeld || s.LeaseHolder != "holder-a" || s.LastCollection != CollectionOK || s.FindingCount != 1 {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestAssurance_AE8_RepeatedTicksDoNotReEmit(t *testing.T) {
	d := newAssuranceDBHarness(t, overdueCluster(time.Now())...)
	d.addPolicy(t, "daily")
	for range 4 {
		d.tick(t, d.svc)
	}
	d.assertAE8Opened(t, 4)
}

func TestAssurance_AE8_SimulatedRestartDoesNotDuplicate(t *testing.T) {
	d := newAssuranceDBHarness(t, overdueCluster(time.Now())...)
	d.addPolicy(t, "daily")
	d.tick(t, d.svc)
	d.svc.releaseLeaseOnShutdown() // graceful stop of the first process

	// A new process: new holder, new in-memory state, same store. Nothing
	// is seeded; the open row is the memory.
	restarted := NewAssuranceService(d.h, d.h.Discoverer, d.st, d.notif, d.clusterID, "holder-restarted", d.logger)
	d.tick(t, restarted)
	d.assertAE8Opened(t, 2)
	if s := restarted.Snapshot(); !s.LeaseHeld || s.LeaseHolder != "holder-restarted" {
		t.Errorf("restarted replica did not take the released lease: %+v", s)
	}
}

func TestAssurance_AE8_FreshSuccessfulBackupResolvesAndEmitsResolution(t *testing.T) {
	now := time.Now()
	d := newAssuranceDBHarness(t, overdueCluster(now)...)
	d.addPolicy(t, "daily")
	d.tick(t, d.svc)
	d.assertAE8Opened(t, 1)

	if err := d.localDyn().Tracker().Create(BackupGVR, asrBackup("daily-fresh", "daily", "Completed", now.Add(-10*time.Minute)), veleroNamespace); err != nil {
		t.Fatal(err)
	}
	resetLocalCache(d.h)
	d.tick(t, d.svc)

	es := d.exceptions(t)
	if len(es) != 1 || es[0].State != store.AssuranceStateResolved || es[0].ResolvedAt == nil {
		t.Fatalf("exceptions = %+v; want the one row resolved", es)
	}
	if reason := resolutionReasonOf(es[0].Detail); reason != store.AssuranceResolutionConditionCleared {
		t.Errorf("detail = %s; want resolutionReason %q", es[0].Detail, store.AssuranceResolutionConditionCleared)
	}
	ds := d.deliveries(t)
	if len(ds) != 2 || ds[1] != (deliveryRow{store.AssuranceTransitionResolved, store.AssuranceDeliveryDelivered, 1}) {
		t.Fatalf("deliveries = %+v; want opened + resolved, both delivered", ds)
	}
	if titles := d.notificationTitles(t); len(titles) != 2 || titles[1] != TitleBackupResolvedPrefix+TitleBackupOverdue {
		t.Fatalf("persisted notifications = %v; want [%q %q]", titles, TitleBackupOverdue, TitleBackupResolvedPrefix+TitleBackupOverdue)
	}

	// The condition recurring later is a NEW exception (the resolved row is
	// terminal) with its own "opened" intent. Its notification, inside the
	// Notification Center's 15 min dedup window of the first one, is
	// deduped: the intent is still marked delivered (the feed carries the
	// earlier entry) and no third row is written. That is the second layer
	// doing its job, not a lost notification.
	if err := d.localDyn().Tracker().Delete(BackupGVR, veleroNamespace, "daily-fresh"); err != nil {
		t.Fatal(err)
	}
	resetLocalCache(d.h)
	d.tick(t, d.svc)
	es = d.exceptions(t)
	if len(es) != 2 {
		t.Fatalf("exceptions after recurrence = %d, want 2", len(es))
	}
	ds = d.deliveries(t)
	if len(ds) != 3 || ds[2] != (deliveryRow{store.AssuranceTransitionOpened, store.AssuranceDeliveryDelivered, 1}) {
		t.Fatalf("deliveries after recurrence = %+v; want a third, delivered 'opened'", ds)
	}
	if titles := d.notificationTitles(t); len(titles) != 2 {
		t.Errorf("persisted notifications after recurrence = %v, want 2 (the re-open is deduped inside the window)", titles)
	}
}

// ---------------------------------------------------------------------------
// Replicas and the lease
// ---------------------------------------------------------------------------

func TestAssurance_TwoServicesOneStoreProduceOneExceptionAndOneDelivery(t *testing.T) {
	now := time.Now()
	d := newAssuranceDBHarness(t, overdueCluster(now)...)
	d.addPolicy(t, "daily")
	// Two replicas that BOTH believe they hold the lease (same holder id
	// stands in for an expired-lease overlap): correctness must come from
	// the store's unique indexes, not from the lease.
	other := newLocalHarness(t, overdueCluster(now)...)
	svcB := NewAssuranceService(other.h, other.h.Discoverer, d.st, d.notif, d.clusterID, "holder-a", d.logger)

	var wg sync.WaitGroup
	for _, svc := range []*AssuranceService{d.svc, svcB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.tick(t.Context())
		}()
	}
	wg.Wait()
	for i, svc := range []*AssuranceService{d.svc, svcB} {
		if s := svc.Snapshot(); s.LastError != "" {
			t.Errorf("replica %d tick failed: %s", i, s.LastError)
		}
	}

	es := d.exceptions(t)
	if len(es) != 1 || es[0].State != store.AssuranceStateOpen {
		t.Fatalf("exceptions = %+v; want exactly one open", es)
	}
	if es[0].ObservationCount < 1 || es[0].ObservationCount > 2 {
		t.Errorf("observation_count = %d; want 1 (loser observed stale) or 2", es[0].ObservationCount)
	}
	if ds := d.deliveries(t); len(ds) != 1 || ds[0].state != store.AssuranceDeliveryDelivered {
		t.Fatalf("deliveries = %+v; want exactly one, delivered", ds)
	}
	if titles := d.notificationTitles(t); len(titles) != 1 {
		t.Fatalf("persisted notifications = %v; want exactly one", titles)
	}
}

func TestAssurance_LeaseHeldByOtherReplicaSkipsWorkWithoutError(t *testing.T) {
	d := newAssuranceDBHarness(t, overdueCluster(time.Now())...)
	d.addPolicy(t, "daily")
	d.tick(t, d.svc)

	other := newLocalHarness(t, overdueCluster(time.Now())...)
	svcB := NewAssuranceService(other.h, other.h.Discoverer, d.st, d.notif, d.clusterID, "holder-b", d.logger)
	svcB.tick(t.Context())
	s := svcB.Snapshot()
	if s.LeaseHeld || s.LastError != "" || !s.LastRunAt.IsZero() || s.LastTickAt.IsZero() {
		t.Errorf("replica B snapshot = %+v; want no lease, no error, no run", s)
	}
	if n := len(other.localDyn().Actions()); n != 0 {
		t.Errorf("replica B read Kubernetes %d times without the lease", n)
	}
	lease, err := d.st.GetLease(t.Context(), d.clusterID)
	if err != nil || lease.Holder != "holder-a" || lease.Expired {
		t.Errorf("lease = %+v (%v); want live, held by holder-a", lease, err)
	}
	if es := d.exceptions(t); len(es) != 1 || es[0].ObservationCount != 1 {
		t.Errorf("exceptions = %+v; replica B must not have observed", es)
	}
}

func TestAssurance_LeaseTakeoverAfterIncumbentStops(t *testing.T) {
	d := newAssuranceDBHarness(t, overdueCluster(time.Now())...)
	d.addPolicy(t, "daily")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.svc.Start(ctx) // first tick is immediate; the interval never fires
	}()
	// Wait for a COMPLETED run, not just the lease: LeaseHeld flips mid-tick
	// and a cancellation there abandons the tick (correctly, at debug level).
	waitFor(t, "incumbent to complete a run", func() bool { return !d.svc.Snapshot().LastRunAt.IsZero() })
	fenceA := d.svc.Snapshot().LeaseFence
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return")
	}
	lease, err := d.st.GetLease(t.Context(), d.clusterID)
	if err != nil || !lease.Expired {
		t.Fatalf("lease after shutdown = %+v (%v); want released (expired)", lease, err)
	}

	other := newLocalHarness(t, overdueCluster(time.Now())...)
	svcB := NewAssuranceService(other.h, other.h.Discoverer, d.st, d.notif, d.clusterID, "holder-b", d.logger)
	d.tick(t, svcB)
	s := svcB.Snapshot()
	if !s.LeaseHeld || s.LeaseHolder != "holder-b" || s.LeaseFence != fenceA+1 {
		t.Errorf("replica B snapshot = %+v; want the lease with fence %d", s, fenceA+1)
	}
	if es := d.exceptions(t); len(es) != 1 || es[0].ObservationCount != 2 {
		t.Errorf("exceptions = %+v; want the one row observed by the successor", es)
	}
	if titles := d.notificationTitles(t); len(titles) != 1 {
		t.Errorf("persisted notifications = %v; the hand-over must not re-notify", titles)
	}
}
