package store

// backup_assurance_delivery_test.go — coverage for the composite open/resolve
// transitions, the delivery intents and the collector lease (Release F,
// U32b). Rows are isolated by a unique cluster id from testOwnerID(t) exactly
// as backup_assurance_test.go does; the lease table's primary key IS the
// cluster id, so the same key isolates it.
//
// The concurrency tests are the point of this unit: they prove, against a
// real PostgreSQL, that competing openers, resolvers, claimers and lease
// takers cannot duplicate an exception, a delivery or an epoch.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// scheduleException is an opener input for a schedule subject. openedAt is
// truncated to microseconds so round-tripped timestamps compare equal.
func scheduleException(cluster string, policyID uuid.UUID, ns, name, uid string, cond AssuranceCondition) BackupAssuranceException {
	return BackupAssuranceException{
		ID:               uuid.New(),
		ClusterID:        cluster,
		PolicyID:         policyID,
		SubjectKind:      ScopeSchedule,
		SubjectNamespace: ns,
		SubjectName:      name,
		SubjectUID:       uid,
		Condition:        cond,
		Severity:         AssuranceSeverityWarning,
		OpenedAt:         time.Now().Truncate(time.Microsecond),
	}
}

// openFixture inserts a policy and returns a ready opener input against it.
func openFixture(t *testing.T, s *BackupAssuranceStore) (string, BackupAssuranceException) {
	t.Helper()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	return cluster, scheduleException(cluster, p.ID, "velero", "daily", "uid-a", ConditionOverdue)
}

func mustOpen(t *testing.T, s *BackupAssuranceStore, e BackupAssuranceException) BackupAssuranceException {
	t.Helper()
	got, opened, err := s.OpenExceptionAndEnqueue(t.Context(), e)
	if err != nil {
		t.Fatalf("OpenExceptionAndEnqueue: %v", err)
	}
	if !opened {
		t.Fatalf("OpenExceptionAndEnqueue opened = false for a fresh identity; want true")
	}
	return got
}

func mustResolve(t *testing.T, s *BackupAssuranceStore, id uuid.UUID) {
	t.Helper()
	resolved, err := s.ResolveExceptionAndEnqueue(t.Context(), id, time.Now(), AssuranceResolutionConditionCleared)
	if err != nil {
		t.Fatalf("ResolveExceptionAndEnqueue: %v", err)
	}
	if !resolved {
		t.Fatalf("ResolveExceptionAndEnqueue resolved = false for an open row; want true")
	}
}

type deliveryRow struct {
	exceptionID uuid.UUID
	transition  string
	state       string
	attempts    int
	lastError   string
	deliveredAt *time.Time
}

// deliveriesFor reads every intent of one exception, oldest first.
func deliveriesFor(t *testing.T, pool *pgxpool.Pool, exceptionID uuid.UUID) []deliveryRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT exception_id, transition, state, attempts, last_error, delivered_at
		  FROM backup_assurance_deliveries WHERE exception_id = $1 ORDER BY created_at, id`, exceptionID)
	if err != nil {
		t.Fatalf("listing deliveries: %v", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (deliveryRow, error) {
		var d deliveryRow
		err := r.Scan(&d.exceptionID, &d.transition, &d.state, &d.attempts, &d.lastError, &d.deliveredAt)
		return d, err
	})
	if err != nil {
		t.Fatalf("scanning deliveries: %v", err)
	}
	return out
}

// deliveryByID reads one intent by its primary key.
func deliveryByID(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) deliveryRow {
	t.Helper()
	var d deliveryRow
	if err := pool.QueryRow(t.Context(), `
		SELECT exception_id, transition, state, attempts, last_error, delivered_at
		  FROM backup_assurance_deliveries WHERE id = $1`, id).Scan(
		&d.exceptionID, &d.transition, &d.state, &d.attempts, &d.lastError, &d.deliveredAt); err != nil {
		t.Fatalf("reading delivery %s: %v", id, err)
	}
	return d
}

// exceptionsForIdentity counts rows (any state) sharing e's condition identity.
func exceptionsForIdentity(t *testing.T, pool *pgxpool.Pool, e BackupAssuranceException) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM backup_assurance_exceptions
		 WHERE cluster_id = $1 AND subject_kind = $2 AND subject_namespace = $3
		   AND subject_name = $4 AND subject_uid = $5 AND condition = $6`,
		e.ClusterID, string(e.SubjectKind), e.SubjectNamespace, e.SubjectName, e.SubjectUID, string(e.Condition)).Scan(&n); err != nil {
		t.Fatalf("counting exceptions: %v", err)
	}
	return n
}

// wideTestPool opens a pool with maxConns connections against the harness
// database, for tests whose whole point is that many transactions are in
// flight at once. testDB's 4-connection pool would serialize most of them.
func wideTestPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	testDB(t) // gate + migrate
	config, err := pgxpool.ParseConfig(testDatabaseURL(os.LookupEnv))
	if err != nil {
		t.Fatalf("parsing %s: %v", testDatabaseURLEnv, err)
	}
	config.MaxConns = maxConns
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("creating wide pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// backdateLease moves the lease's expiry into the past on the database
// clock, standing in for an incumbent that stopped renewing.
func backdateLease(t *testing.T, pool *pgxpool.Pool, cluster string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`UPDATE backup_assurance_collector_lease SET expires_at = NOW() - interval '1 second' WHERE cluster_id = $1`,
		cluster); err != nil {
		t.Fatalf("backdating lease: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Hermetic tests — no database required.
// ---------------------------------------------------------------------------

// TestBackupAssuranceDelivery_NoProcessClockOrBackgroundContext pins two
// exit criteria of the unit by reading the source: lease expiry is judged
// on the database clock only (so no time.Now anywhere in the file), and no
// store method detaches from the caller's context.
func TestBackupAssuranceDelivery_NoProcessClockOrBackgroundContext(t *testing.T) {
	src, err := os.ReadFile("backup_assurance_delivery.go")
	if err != nil {
		t.Fatalf("reading source: %v", err)
	}
	// Comments may name the rule; code may not break it.
	var code strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}
	for _, forbidden := range []string{"time.Now(", "context.Background(", "context.TODO("} {
		if strings.Contains(code.String(), forbidden) {
			t.Errorf("backup_assurance_delivery.go contains %q; the store must use the database clock and the caller's context", forbidden)
		}
	}
}

func TestValidateOpenException_RejectsInconsistentShapes(t *testing.T) {
	base := scheduleException("local", uuid.New(), "velero", "daily", "uid-a", ConditionOverdue)
	cases := []struct {
		name   string
		mutate func(*BackupAssuranceException)
	}{
		{"nil id", func(e *BackupAssuranceException) { e.ID = uuid.Nil }},
		{"empty cluster", func(e *BackupAssuranceException) { e.ClusterID = "" }},
		{"nil policy", func(e *BackupAssuranceException) { e.PolicyID = uuid.Nil }},
		{"unknown kind", func(e *BackupAssuranceException) { e.SubjectKind = "backup" }},
		{"schedule without uid", func(e *BackupAssuranceException) { e.SubjectUID = "" }},
		{"schedule without name", func(e *BackupAssuranceException) { e.SubjectName = "" }},
		{"namespace with uid", func(e *BackupAssuranceException) {
			e.SubjectKind, e.SubjectName = ScopeNamespace, ""
		}},
		{"cluster with namespace", func(e *BackupAssuranceException) {
			e.SubjectKind, e.SubjectName, e.SubjectUID = ScopeCluster, "", ""
		}},
		{"unknown condition", func(e *BackupAssuranceException) { e.Condition = "stale" }},
		{"unknown severity", func(e *BackupAssuranceException) { e.Severity = "severe" }},
		{"zero openedAt", func(e *BackupAssuranceException) { e.OpenedAt = time.Time{} }},
		{"malformed detail", func(e *BackupAssuranceException) { e.Detail = []byte(`{not json`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.mutate(&e)
			if err := validateOpenException(e); !errors.Is(err, ErrAssuranceExceptionInvalid) {
				t.Fatalf("validateOpenException = %v; want ErrAssuranceExceptionInvalid", err)
			}
		})
	}
	ns := base
	ns.SubjectKind, ns.SubjectName, ns.SubjectUID = ScopeNamespace, "", ""
	cl := base
	cl.SubjectKind, cl.SubjectNamespace, cl.SubjectName, cl.SubjectUID = ScopeCluster, "", "", ""
	for _, e := range []BackupAssuranceException{base, ns, cl} {
		if err := validateOpenException(e); err != nil {
			t.Errorf("validateOpenException(%s) = %v; want nil", e.SubjectKind, err)
		}
	}
}

func TestDeliveryIDFor_IsDerivedFromIdentity(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if deliveryIDFor(a, AssuranceTransitionOpened) != deliveryIDFor(a, AssuranceTransitionOpened) {
		t.Error("deliveryIDFor is not deterministic")
	}
	if deliveryIDFor(a, AssuranceTransitionOpened) == deliveryIDFor(a, AssuranceTransitionResolved) {
		t.Error("deliveryIDFor collides across transitions of one exception")
	}
	if deliveryIDFor(a, AssuranceTransitionOpened) == deliveryIDFor(b, AssuranceTransitionOpened) {
		t.Error("deliveryIDFor collides across exceptions")
	}
}

func TestQualifyColumns(t *testing.T) {
	got := qualifyColumns("e", "\n\tid, cluster_id,\n\tdetail")
	if got != "e.id, e.cluster_id, e.detail" {
		t.Fatalf("qualifyColumns = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Open
// ---------------------------------------------------------------------------

func TestOpenExceptionAndEnqueue_CreatesExactlyOneDelivery(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	lastSuccess := in.OpenedAt.Add(-30 * time.Hour)
	in.LastSuccessAt = &lastSuccess
	in.Detail = []byte(`{"ageSeconds":108000}`)

	got := mustOpen(t, s, in)
	if got.ID != in.ID || got.State != AssuranceStateOpen || got.ObservationCount != 1 ||
		!got.OpenedAt.Equal(in.OpenedAt) || !got.LastObservedAt.Equal(in.OpenedAt) ||
		got.ResolvedAt != nil || got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(lastSuccess) ||
		got.PolicyID != in.PolicyID || got.ClusterID != cluster {
		t.Errorf("opened row = %+v; want the input stored as an open row with observation_count 1", got)
	}
	var detail map[string]any
	if err := json.Unmarshal(got.Detail, &detail); err != nil || detail["ageSeconds"] != float64(108000) {
		t.Errorf("detail = %s (%v); want the input detail", got.Detail, err)
	}

	ds := deliveriesFor(t, pool, got.ID)
	if len(ds) != 1 || ds[0].transition != AssuranceTransitionOpened || ds[0].state != AssuranceDeliveryPending || ds[0].attempts != 0 {
		t.Fatalf("deliveries after open = %+v; want exactly one pending 'opened' intent with 0 attempts", ds)
	}
	open, err := s.ListOpenExceptions(ctx, cluster)
	if err != nil || len(open) != 1 || open[0].ID != got.ID {
		t.Errorf("ListOpenExceptions = (%v, %v); want just the opened row", open, err)
	}

	// An empty detail is stored as '{}' so jsonb_set on resolution has an object to merge into.
	bare := scheduleException(cluster, in.PolicyID, "velero", "daily", "uid-a", ConditionPaused)
	if got := mustOpen(t, s, bare); string(got.Detail) != "{}" {
		t.Errorf("empty detail stored as %s; want {}", got.Detail)
	}
}

func TestOpenExceptionAndEnqueue_SecondCallerGetsOpenedFalseAndNoDelivery(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	first := mustOpen(t, s, in)

	// The second replica evaluates the same condition a moment later, with
	// its own id and a different (edited) policy for the same scope.
	second := in
	second.ID = uuid.New()
	second.OpenedAt = in.OpenedAt.Add(time.Minute)
	second.Severity = AssuranceSeverityCritical
	got, opened, err := s.OpenExceptionAndEnqueue(ctx, second)
	if err != nil {
		t.Fatalf("second OpenExceptionAndEnqueue: %v", err)
	}
	if opened {
		t.Fatal("second opener reported opened = true; want false (the identity was already open)")
	}
	if got.ID != first.ID || got.Severity != first.Severity || got.ObservationCount != 1 || !got.OpenedAt.Equal(first.OpenedAt) {
		t.Errorf("second opener got %+v; want the first row unchanged (%s)", got, first.ID)
	}
	if n := exceptionsForIdentity(t, pool, in); n != 1 {
		t.Errorf("exceptions for identity = %d; want 1", n)
	}
	if ds := deliveriesFor(t, pool, first.ID); len(ds) != 1 {
		t.Errorf("deliveries after a lost race = %d; want still exactly 1", len(ds))
	}
	if exceptionExists(t, pool, second.ID) {
		t.Error("the loser's id was persisted; want nothing written")
	}
	if ds := deliveriesFor(t, pool, second.ID); len(ds) != 0 {
		t.Errorf("the loser enqueued %d deliveries; want 0", len(ds))
	}

	// The loser follows up with an observation, which the first row absorbs.
	if err := s.ObserveException(ctx, got.ID, second.OpenedAt, second.Severity, nil, nil); err != nil {
		t.Fatalf("ObserveException after losing the open race: %v", err)
	}
	open, _ := s.ListOpenExceptions(ctx, cluster)
	if len(open) != 1 || open[0].ObservationCount != 2 || open[0].Severity != AssuranceSeverityCritical {
		t.Errorf("after observe: %+v; want one row with count 2 and severity critical", open)
	}
}

// TestOpenExceptionAndEnqueue_ConcurrentGoroutinesProduceOneRow is the
// competing-replicas proof for AE8 steps 3/5: sixteen openers released at
// once against one identity, over a pool wide enough that they genuinely
// overlap, yield exactly one exception row, exactly one opened = true, and
// exactly one delivery intent.
func TestOpenExceptionAndEnqueue_ConcurrentGoroutinesProduceOneRow(t *testing.T) {
	const openers = 16
	pool := wideTestPool(t, openers)
	s := NewBackupAssuranceStore(pool)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))

	type outcome struct {
		got    BackupAssuranceException
		opened bool
		err    error
	}
	results := make([]outcome, openers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range openers {
		in := scheduleException(cluster, p.ID, "velero", "daily", "uid-a", ConditionOverdue)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, opened, err := s.OpenExceptionAndEnqueue(ctx, in)
			results[i] = outcome{got, opened, err}
		}()
	}
	close(start)
	wg.Wait()

	var winners int
	var winnerID uuid.UUID
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("opener %d: %v", i, r.err)
		}
		if r.opened {
			winners++
			winnerID = r.got.ID
		}
	}
	if winners != 1 {
		t.Fatalf("opened = true reported by %d of %d concurrent openers; want exactly 1", winners, openers)
	}
	for i, r := range results {
		if r.got.ID != winnerID {
			t.Errorf("opener %d was handed row %s; want every opener to converge on the winner %s", i, r.got.ID, winnerID)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM backup_assurance_exceptions WHERE cluster_id = $1`, cluster).Scan(&rows); err != nil {
		t.Fatalf("counting exceptions: %v", err)
	}
	if rows != 1 {
		t.Errorf("exception rows for the cluster = %d; want exactly 1", rows)
	}
	var deliveries int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM backup_assurance_deliveries d
		  JOIN backup_assurance_exceptions e ON e.id = d.exception_id
		 WHERE e.cluster_id = $1`, cluster).Scan(&deliveries); err != nil {
		t.Fatalf("counting deliveries: %v", err)
	}
	if deliveries != 1 {
		t.Errorf("delivery intents for the cluster = %d; want exactly 1", deliveries)
	}
}

func TestOpenExceptionAndEnqueue_RecreatedSubjectUIDOpensNewException(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	old := mustOpen(t, s, in)

	// The schedule is deleted and recreated under the same name: new UID.
	recreated := scheduleException(cluster, in.PolicyID, "velero", "daily", "uid-b", ConditionOverdue)
	fresh := mustOpen(t, s, recreated)
	if fresh.ID == old.ID {
		t.Fatal("recreated subject reused the old exception row")
	}
	open, err := s.ListOpenExceptions(ctx, cluster)
	if err != nil || len(open) != 2 {
		t.Fatalf("ListOpenExceptions = (%d rows, %v); want both UIDs open side by side", len(open), err)
	}
	if ds := deliveriesFor(t, pool, fresh.ID); len(ds) != 1 || ds[0].transition != AssuranceTransitionOpened {
		t.Errorf("deliveries for the recreated subject = %+v; want one opened intent", ds)
	}

	// The reconciler then resolves the stale UID as absent; the new one is untouched.
	resolved, err := s.ResolveExceptionAndEnqueue(ctx, old.ID, time.Now(), AssuranceResolutionSubjectAbsent)
	if err != nil || !resolved {
		t.Fatalf("resolving the old UID = (%v, %v); want (true, nil)", resolved, err)
	}
	open, _ = s.ListOpenExceptions(ctx, cluster)
	if len(open) != 1 || open[0].ID != fresh.ID {
		t.Errorf("after resolving the old UID, open = %v; want only %s", open, fresh.ID)
	}
}

// TestOpenExceptionAndEnqueue_RollsBackDeliveryWhenExceptionInsertFails
// proves the two inserts share one transaction from both sides: when the
// exception cannot be inserted no intent appears, and when the intent cannot
// be inserted the exception does not survive either.
func TestOpenExceptionAndEnqueue_RollsBackDeliveryWhenExceptionInsertFails(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)

	// Exception side: the policy is gone (deleted between ListPolicies and
	// this call). Nothing is written and the caller learns why.
	orphan := in
	orphan.PolicyID = uuid.New()
	if _, _, err := s.OpenExceptionAndEnqueue(ctx, orphan); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Fatalf("open against a missing policy = %v; want ErrAssurancePolicyNotFound", err)
	}
	if exceptionExists(t, pool, orphan.ID) || len(deliveriesFor(t, pool, orphan.ID)) != 0 {
		t.Error("a refused open left an exception or delivery behind")
	}

	// Delivery side: occupy the primary key the opened intent will derive,
	// attached to an unrelated exception, so the intent insert fails after
	// the exception insert succeeded.
	decoy := mustOpen(t, s, scheduleException(cluster, in.PolicyID, "velero", "daily", "uid-decoy", ConditionPaused))
	if _, err := pool.Exec(ctx,
		`INSERT INTO backup_assurance_deliveries (id, exception_id, transition, state) VALUES ($1, $2, 'resolved', 'pending')`,
		deliveryIDFor(in.ID, AssuranceTransitionOpened), decoy.ID); err != nil {
		t.Fatalf("seeding the colliding delivery: %v", err)
	}
	if _, _, err := s.OpenExceptionAndEnqueue(ctx, in); err == nil {
		t.Fatal("open succeeded although its delivery intent could not be inserted")
	}
	if exceptionExists(t, pool, in.ID) {
		t.Error("exception row survived a failed delivery insert; want the whole transaction rolled back")
	}
	if n := exceptionsForIdentity(t, pool, in); n != 0 {
		t.Errorf("exceptions for identity = %d after rollback; want 0", n)
	}
}

// TestOpenExceptionAndEnqueue_RejectsPolicyFromAnotherCluster covers the
// invariant U32 left out of the DDL: an exception's cluster must be its
// policy's cluster. The store refuses the mismatch with nothing written.
func TestOpenExceptionAndEnqueue_RejectsPolicyFromAnotherCluster(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	clusterA, clusterB := testOwnerID(t), testOwnerID(t)
	policyB := mustInsertPolicy(t, s, schedulePolicy(clusterB, "velero", "daily"))

	in := scheduleException(clusterA, policyB.ID, "velero", "daily", "uid-a", ConditionOverdue)
	if _, _, err := s.OpenExceptionAndEnqueue(ctx, in); !errors.Is(err, ErrAssurancePolicyNotFound) {
		t.Fatalf("open in cluster A citing cluster B's policy = %v; want ErrAssurancePolicyNotFound", err)
	}
	if exceptionExists(t, pool, in.ID) {
		t.Error("cross-cluster exception was persisted")
	}
	for _, c := range []string{clusterA, clusterB} {
		if open, err := s.ListOpenExceptions(ctx, c); err != nil || len(open) != 0 {
			t.Errorf("ListOpenExceptions(%s) = (%v, %v); want nothing", c, open, err)
		}
	}
	// Every stored exception agrees with its policy on cluster_id.
	var mismatched int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM backup_assurance_exceptions e
		  JOIN backup_assurance_policies p ON p.id = e.policy_id
		 WHERE e.cluster_id <> p.cluster_id AND p.cluster_id IN ($1, $2)`, clusterA, clusterB).Scan(&mismatched); err != nil {
		t.Fatalf("checking invariant: %v", err)
	}
	if mismatched != 0 {
		t.Errorf("%d exceptions disagree with their policy's cluster", mismatched)
	}
}

// ---------------------------------------------------------------------------
// Resolve
// ---------------------------------------------------------------------------

func TestResolveExceptionAndEnqueue_OnlyFirstCallerEnqueues(t *testing.T) {
	const resolvers = 8
	pool := wideTestPool(t, resolvers)
	s := NewBackupAssuranceStore(pool)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	in := scheduleException(cluster, p.ID, "velero", "daily", "uid-a", ConditionOverdue)
	in.Detail = []byte(`{"ageSeconds":1}`)
	opened := mustOpen(t, s, in)
	at := time.Now().Truncate(time.Microsecond)

	results := make([]bool, resolvers)
	errs := make([]error, resolvers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range resolvers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = s.ResolveExceptionAndEnqueue(ctx, opened.ID, at, AssuranceResolutionConditionCleared)
		}()
	}
	close(start)
	wg.Wait()

	var winners int
	for i := range resolvers {
		if errs[i] != nil {
			t.Fatalf("resolver %d: %v", i, errs[i])
		}
		if results[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("resolved = true reported by %d of %d concurrent resolvers; want exactly 1", winners, resolvers)
	}
	ds := deliveriesFor(t, pool, opened.ID)
	if len(ds) != 2 || ds[0].transition != AssuranceTransitionOpened || ds[1].transition != AssuranceTransitionResolved ||
		ds[1].state != AssuranceDeliveryPending {
		t.Fatalf("deliveries after resolve = %+v; want [opened, resolved(pending)]", ds)
	}

	// A late resolver after the fact gets false and adds nothing.
	again, err := s.ResolveExceptionAndEnqueue(ctx, opened.ID, time.Now(), AssuranceResolutionSubjectAbsent)
	if err != nil || again {
		t.Errorf("resolving a resolved row = (%v, %v); want (false, nil)", again, err)
	}
	if len(deliveriesFor(t, pool, opened.ID)) != 2 {
		t.Error("a late resolve added a delivery")
	}
	// Resolving a row that never existed (cascaded away) is also false, nil.
	if gone, err := s.ResolveExceptionAndEnqueue(ctx, uuid.New(), time.Now(), AssuranceResolutionSubjectAbsent); err != nil || gone {
		t.Errorf("resolving a missing row = (%v, %v); want (false, nil)", gone, err)
	}

	// The row carries the resolution, and the rest of detail survived the merge.
	all, _, err := s.ListExceptions(ctx, cluster, AssuranceExceptionQuery{State: AssuranceStateResolved})
	if err != nil || len(all) != 1 {
		t.Fatalf("ListExceptions(resolved) = (%v, %v)", all, err)
	}
	e := all[0]
	if e.ResolvedAt == nil || !e.ResolvedAt.Equal(at) {
		t.Errorf("resolved_at = %v; want %v", e.ResolvedAt, at)
	}
	var detail map[string]any
	if err := json.Unmarshal(e.Detail, &detail); err != nil ||
		detail["resolutionReason"] != AssuranceResolutionConditionCleared || detail["ageSeconds"] != float64(1) {
		t.Errorf("detail after resolve = %s; want resolutionReason merged into the existing detail", e.Detail)
	}
}

// TestResolveExceptionAndEnqueue_AlwaysStampsResolvedAt covers the other
// invariant U32 left to the store: a resolved row always has resolved_at.
// The schema admits state = 'resolved' with resolved_at NULL; the method
// refuses the inputs that would get there.
func TestResolveExceptionAndEnqueue_AlwaysStampsResolvedAt(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	opened := mustOpen(t, s, in)

	if _, err := s.ResolveExceptionAndEnqueue(ctx, opened.ID, time.Time{}, AssuranceResolutionConditionCleared); !errors.Is(err, ErrAssuranceResolutionInvalid) {
		t.Errorf("resolve with a zero time = %v; want ErrAssuranceResolutionInvalid", err)
	}
	if _, err := s.ResolveExceptionAndEnqueue(ctx, opened.ID, time.Now(), ""); !errors.Is(err, ErrAssuranceResolutionInvalid) {
		t.Errorf("resolve without a reason = %v; want ErrAssuranceResolutionInvalid", err)
	}
	open, _ := s.ListOpenExceptions(ctx, cluster)
	if len(open) != 1 || len(deliveriesFor(t, pool, opened.ID)) != 1 {
		t.Fatal("a refused resolution changed the row or enqueued an intent")
	}

	at := time.Now().Truncate(time.Microsecond)
	if resolved, err := s.ResolveExceptionAndEnqueue(ctx, opened.ID, at, AssuranceResolutionConditionCleared); err != nil || !resolved {
		t.Fatalf("resolve = (%v, %v)", resolved, err)
	}
	var state string
	var resolvedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT state, resolved_at FROM backup_assurance_exceptions WHERE id = $1`, opened.ID).Scan(&state, &resolvedAt); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if state != AssuranceStateResolved || resolvedAt == nil || !resolvedAt.Equal(at) {
		t.Errorf("row = state %s resolved_at %v; want resolved at %v", state, resolvedAt, at)
	}
	var violations int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM backup_assurance_exceptions WHERE cluster_id = $1 AND state = 'resolved' AND resolved_at IS NULL`,
		cluster).Scan(&violations); err != nil {
		t.Fatalf("checking invariant: %v", err)
	}
	if violations != 0 {
		t.Errorf("%d resolved rows without resolved_at", violations)
	}
}

func TestResolveExceptionAndEnqueue_ReopenAfterResolveCreatesNewRow(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	first := mustOpen(t, s, in)
	mustResolve(t, s, first.ID)

	// The same condition recurs: the partial index does not cover the
	// resolved row, so a brand-new exception opens and notifies once more.
	again := in
	again.ID = uuid.New()
	again.OpenedAt = in.OpenedAt.Add(2 * time.Hour)
	second := mustOpen(t, s, again)
	if second.ID == first.ID || second.ObservationCount != 1 {
		t.Fatalf("re-occurrence returned %+v; want a new row with observation_count 1", second)
	}
	if n := exceptionsForIdentity(t, pool, in); n != 2 {
		t.Errorf("exceptions for identity = %d; want 2 (one resolved, one open)", n)
	}
	open, _ := s.ListOpenExceptions(ctx, cluster)
	if len(open) != 1 || open[0].ID != second.ID {
		t.Errorf("open = %v; want only the new row", open)
	}
	if ds := deliveriesFor(t, pool, first.ID); len(ds) != 2 {
		t.Errorf("first row's deliveries = %+v; want [opened, resolved]", ds)
	}
	if ds := deliveriesFor(t, pool, second.ID); len(ds) != 1 || ds[0].transition != AssuranceTransitionOpened {
		t.Errorf("second row's deliveries = %+v; want one opened intent", ds)
	}
	pending, failed, err := s.CountPendingDeliveries(ctx, cluster)
	if err != nil || pending != 3 || failed != 0 {
		t.Errorf("CountPendingDeliveries = (%d, %d, %v); want (3, 0, nil)", pending, failed, err)
	}
}

// ---------------------------------------------------------------------------
// Claim / mark
// ---------------------------------------------------------------------------

func TestClaimPendingDeliveries_SkipsLockedAndRespectsLimit(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)
	p := mustInsertPolicy(t, s, schedulePolicy(cluster, "velero", "daily"))
	var ids []uuid.UUID
	for _, uid := range []string{"uid-1", "uid-2", "uid-3"} {
		ids = append(ids, mustOpen(t, s, scheduleException(cluster, p.ID, "velero", "daily", uid, ConditionOverdue)).ID)
	}
	// Another cluster's intent must never be claimed, nor have its attempts spent.
	otherCluster := testOwnerID(t)
	otherPolicy := mustInsertPolicy(t, s, schedulePolicy(otherCluster, "velero", "daily"))
	other := mustOpen(t, s, scheduleException(otherCluster, otherPolicy.ID, "velero", "daily", "uid-x", ConditionOverdue))

	// A competing drainer holds the second intent's row lock.
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = locker.Rollback(context.WithoutCancel(ctx)) })
	lockedID := deliveryIDFor(ids[1], AssuranceTransitionOpened)
	if _, err := locker.Exec(ctx, `SELECT id FROM backup_assurance_deliveries WHERE id = $1 FOR UPDATE`, lockedID); err != nil {
		t.Fatalf("locking: %v", err)
	}

	jobs, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 10)
	if err != nil {
		t.Fatalf("ClaimPendingDeliveries: %v", err)
	}
	gotExc := map[uuid.UUID]AssuranceDeliveryJob{}
	for _, j := range jobs {
		gotExc[j.Exception.ID] = j
	}
	if len(jobs) != 2 || gotExc[ids[1]].Delivery.ID != uuid.Nil || gotExc[ids[0]].Delivery.ID == uuid.Nil || gotExc[ids[2]].Delivery.ID == uuid.Nil {
		t.Fatalf("claimed %d jobs for exceptions %v; want the two unlocked intents (not %s)", len(jobs), gotExc, ids[1])
	}
	for _, j := range jobs {
		if j.Delivery.Attempts != 1 || j.Delivery.State != AssuranceDeliveryPending || j.Delivery.Transition != AssuranceTransitionOpened {
			t.Errorf("claimed delivery = %+v; want pending, attempts 1, opened", j.Delivery)
		}
		if j.Exception.ClusterID != cluster || j.Exception.SubjectKind != ScopeSchedule || j.Exception.State != AssuranceStateOpen {
			t.Errorf("claimed exception = %+v; want the joined open row of this cluster", j.Exception)
		}
	}
	if d := deliveryByID(t, pool, lockedID); d.attempts != 0 {
		t.Errorf("locked intent's attempts = %d; want 0 (skipped, not waited on)", d.attempts)
	}
	if d := deliveryByID(t, pool, deliveryIDFor(other.ID, AssuranceTransitionOpened)); d.attempts != 0 {
		t.Errorf("another cluster's intent had attempts spent: %d", d.attempts)
	}

	// Once the lock is gone, the skipped row is claimable. Nothing was
	// marked, so all three are pending; the limit bounds the batch and the
	// batch is oldest-first.
	if err := locker.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	jobs, err = s.ClaimPendingDeliveries(ctx, cluster, 5, 1)
	if err != nil || len(jobs) != 1 || jobs[0].Exception.ID != ids[0] {
		t.Fatalf("ClaimPendingDeliveries(limit 1) = (%v, %v); want exactly the oldest intent (%s)", jobs, err, ids[0])
	}
	jobs, err = s.ClaimPendingDeliveries(ctx, cluster, 5, 10)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("ClaimPendingDeliveries after unlock = (%d jobs, %v); want all 3 including the once-locked one", len(jobs), err)
	}
	if d := deliveryByID(t, pool, lockedID); d.attempts != 1 {
		t.Errorf("once-locked intent's attempts = %d after unlock and claim; want 1", d.attempts)
	}
	if _, err := s.ClaimPendingDeliveries(ctx, cluster, 0, 1); err == nil {
		t.Error("ClaimPendingDeliveries with maxAttempts 0 succeeded; want an error")
	}
	if _, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 0); err == nil {
		t.Error("ClaimPendingDeliveries with limit 0 succeeded; want an error")
	}
	empty, err := s.ClaimPendingDeliveries(ctx, testOwnerID(t), 5, 10)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("claim for a cluster with nothing pending = (%v, %v); want an empty non-nil slice", empty, err)
	}
}

func TestClaimPendingDeliveries_StopsAtMaxAttempts(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	opened := mustOpen(t, s, in)
	const maxAttempts = 3

	// Each tick claims, fails to send, and records the failure.
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		jobs, err := s.ClaimPendingDeliveries(ctx, cluster, maxAttempts, 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("attempt %d: claim = (%d jobs, %v); want 1", attempt, len(jobs), err)
		}
		if jobs[0].Delivery.Attempts != attempt {
			t.Errorf("attempt %d: claimed attempts = %d", attempt, jobs[0].Delivery.Attempts)
		}
		if err := s.MarkDeliveryFailed(ctx, jobs[0].Delivery.ID, "smtp: connection refused", maxAttempts); err != nil {
			t.Fatalf("attempt %d: MarkDeliveryFailed: %v", attempt, err)
		}
	}
	jobs, err := s.ClaimPendingDeliveries(ctx, cluster, maxAttempts, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("claim after %d failures = (%d jobs, %v); want nothing claimable", maxAttempts, len(jobs), err)
	}
	d := deliveryByID(t, pool, deliveryIDFor(opened.ID, AssuranceTransitionOpened))
	if d.state != AssuranceDeliveryFailed || d.attempts != maxAttempts || d.lastError != "smtp: connection refused" {
		t.Errorf("poison intent = %+v; want failed after %d attempts with the last error kept", d, maxAttempts)
	}
	pending, failed, err := s.CountPendingDeliveries(ctx, cluster)
	if err != nil || pending != 0 || failed != 1 {
		t.Errorf("CountPendingDeliveries = (%d, %d, %v); want (0, 1, nil)", pending, failed, err)
	}
	// The condition itself is untouched: still open, no second intent.
	open, _ := s.ListOpenExceptions(ctx, cluster)
	if len(open) != 1 || len(deliveriesFor(t, pool, opened.ID)) != 1 {
		t.Error("delivery failure regenerated the condition or a second intent")
	}

	// An abandoned final attempt (claimed, then the process died) is a
	// pending row with its attempts spent. The next claim fails it instead of
	// leaving an unclaimable pending row in the backlog forever.
	abandoned := mustOpen(t, s, scheduleException(cluster, in.PolicyID, "velero", "daily", "uid-a", ConditionPaused))
	abandonedID := deliveryIDFor(abandoned.ID, AssuranceTransitionOpened)
	if _, err := pool.Exec(ctx, `UPDATE backup_assurance_deliveries SET attempts = $2 WHERE id = $1`, abandonedID, maxAttempts); err != nil {
		t.Fatalf("simulating an abandoned claim: %v", err)
	}
	if jobs, err := s.ClaimPendingDeliveries(ctx, cluster, maxAttempts, 10); err != nil || len(jobs) != 0 {
		t.Fatalf("claim = (%d jobs, %v); want the exhausted row not claimed", len(jobs), err)
	}
	if d := deliveryByID(t, pool, abandonedID); d.state != AssuranceDeliveryFailed || d.lastError != assuranceDeliveryExhaustedError {
		t.Errorf("abandoned intent = %+v; want failed with the exhausted marker", d)
	}
}

func TestMarkDeliveryFailed_TransitionsToFailedAtCap(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	opened := mustOpen(t, s, in)
	id := deliveryIDFor(opened.ID, AssuranceTransitionOpened)

	// Below the cap: stays pending, error recorded, retryable.
	if _, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 1); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.MarkDeliveryFailed(ctx, id, "first failure", 5); err != nil {
		t.Fatalf("MarkDeliveryFailed below cap: %v", err)
	}
	if d := deliveryByID(t, pool, id); d.state != AssuranceDeliveryPending || d.attempts != 1 || d.lastError != "first failure" {
		t.Errorf("below cap = %+v; want pending, 1 attempt, error kept", d)
	}

	// At the cap (attempts >= maxAttempts): terminal.
	if _, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 1); err != nil {
		t.Fatalf("claim: %v", err)
	}
	long := strings.Repeat("x", assuranceDeliveryErrorMaxLen+100)
	if err := s.MarkDeliveryFailed(ctx, id, long, 2); err != nil {
		t.Fatalf("MarkDeliveryFailed at cap: %v", err)
	}
	d := deliveryByID(t, pool, id)
	if d.state != AssuranceDeliveryFailed || d.attempts != 2 || len(d.lastError) != assuranceDeliveryErrorMaxLen {
		t.Errorf("at cap = state %s attempts %d errlen %d; want failed, 2, %d", d.state, d.attempts, len(d.lastError), assuranceDeliveryErrorMaxLen)
	}

	// failed is terminal for both markers; so is delivered.
	if err := s.MarkDeliveryFailed(ctx, id, "again", 5); !errors.Is(err, ErrAssuranceDeliveryNotPending) {
		t.Errorf("MarkDeliveryFailed on a failed row = %v; want ErrAssuranceDeliveryNotPending", err)
	}
	if err := s.MarkDelivered(ctx, id); !errors.Is(err, ErrAssuranceDeliveryNotPending) {
		t.Errorf("MarkDelivered on a failed row = %v; want ErrAssuranceDeliveryNotPending", err)
	}
	if err := s.MarkDeliveryFailed(ctx, id, "x", 0); err == nil {
		t.Error("MarkDeliveryFailed with maxAttempts 0 succeeded; want an error")
	}

	// A successful send: delivered, stamped, error cleared, and terminal.
	mustResolve(t, s, opened.ID)
	resolvedID := deliveryIDFor(opened.ID, AssuranceTransitionResolved)
	if _, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 1); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.MarkDeliveryFailed(ctx, resolvedID, "transient", 5); err != nil {
		t.Fatalf("MarkDeliveryFailed: %v", err)
	}
	if err := s.MarkDelivered(ctx, resolvedID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if d := deliveryByID(t, pool, resolvedID); d.state != AssuranceDeliveryDelivered || d.deliveredAt == nil || d.lastError != "" {
		t.Errorf("delivered row = %+v; want delivered with delivered_at set and last_error cleared", d)
	}
	if err := s.MarkDelivered(ctx, resolvedID); !errors.Is(err, ErrAssuranceDeliveryNotPending) {
		t.Errorf("second MarkDelivered = %v; want ErrAssuranceDeliveryNotPending", err)
	}
	if err := s.MarkDelivered(ctx, uuid.New()); !errors.Is(err, ErrAssuranceDeliveryNotPending) {
		t.Errorf("MarkDelivered on a missing row = %v; want ErrAssuranceDeliveryNotPending", err)
	}
	if jobs, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 10); err != nil || len(jobs) != 0 {
		t.Errorf("claim with everything terminal = (%d jobs, %v); want none", len(jobs), err)
	}
}

// TestPendingDeliverySurvivesSimulatedRestart: the transition commits, the
// process dies before the send (pool closed), a new process connects and
// finds exactly one pending intent to drain. No seeding, no title decoding.
func TestPendingDeliverySurvivesSimulatedRestart(t *testing.T) {
	ctx := t.Context()
	firstPool := testDB(t)
	before := NewBackupAssuranceStore(firstPool)
	cluster, in := openFixture(t, before)
	opened := mustOpen(t, before, in)
	firstPool.Close() // the process is gone; Close is idempotent for t.Cleanup

	secondPool := testDB(t)
	after := NewBackupAssuranceStore(secondPool)
	pending, failed, err := after.CountPendingDeliveries(ctx, cluster)
	if err != nil || pending != 1 || failed != 0 {
		t.Fatalf("after restart CountPendingDeliveries = (%d, %d, %v); want (1, 0, nil)", pending, failed, err)
	}
	jobs, err := after.ClaimPendingDeliveries(ctx, cluster, 5, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("after restart claim = (%d jobs, %v); want exactly the one intent", len(jobs), err)
	}
	if jobs[0].Exception.ID != opened.ID || jobs[0].Delivery.Transition != AssuranceTransitionOpened {
		t.Errorf("after restart job = %+v; want the opened intent of %s", jobs[0], opened.ID)
	}
	if err := after.MarkDelivered(ctx, jobs[0].Delivery.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	// The new process's first tick re-evaluates the same condition: still
	// open, so it lands on an observation, not a second intent.
	again := in
	again.ID = uuid.New()
	again.OpenedAt = in.OpenedAt.Add(time.Minute)
	got, wasOpened, err := after.OpenExceptionAndEnqueue(ctx, again)
	if err != nil || wasOpened || got.ID != opened.ID {
		t.Fatalf("re-evaluation after restart = (%s, %v, %v); want the existing row, opened = false", got.ID, wasOpened, err)
	}
	if pending, failed, _ := after.CountPendingDeliveries(ctx, cluster); pending != 0 || failed != 0 {
		t.Errorf("backlog after restart drain = (%d, %d); want (0, 0)", pending, failed)
	}
}

// TestDeliveryContextCancellationLeavesRowClaimable: a cancelled claim
// changes nothing, and a claim whose caller stops before marking leaves the
// row pending (one attempt spent) for the next tick.
func TestDeliveryContextCancellationLeavesRowClaimable(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster, in := openFixture(t, s)
	opened := mustOpen(t, s, in)
	id := deliveryIDFor(opened.ID, AssuranceTransitionOpened)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ClaimPendingDeliveries(cancelled, cluster, 5, 10); err == nil {
		t.Fatal("claim on a cancelled context succeeded; want an error")
	}
	if d := deliveryByID(t, pool, id); d.state != AssuranceDeliveryPending || d.attempts != 0 {
		t.Fatalf("after a cancelled claim = %+v; want untouched pending with 0 attempts", d)
	}
	if err := s.MarkDelivered(cancelled, id); err == nil {
		t.Error("MarkDelivered on a cancelled context succeeded; want an error")
	}
	if d := deliveryByID(t, pool, id); d.state != AssuranceDeliveryPending {
		t.Fatalf("cancelled MarkDelivered changed the row: %+v", d)
	}

	// Claimed, then the loop observed ctx.Err() and returned without marking.
	jobs, err := s.ClaimPendingDeliveries(ctx, cluster, 5, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim = (%d jobs, %v)", len(jobs), err)
	}
	// Next tick: the row is claimable again with the attempt recorded.
	jobs, err = s.ClaimPendingDeliveries(ctx, cluster, 5, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Delivery.ID != id || jobs[0].Delivery.Attempts != 2 {
		t.Fatalf("re-claim = (%+v, %v); want the same intent with attempts 2", jobs, err)
	}
}

// ---------------------------------------------------------------------------
// Lease
// ---------------------------------------------------------------------------

func TestAcquireOrRenewLease_SecondHolderRejectedWhileLive(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)

	a, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if a.ClusterID != cluster || a.Holder != "replica-a" || a.Fence != 1 || a.Expired ||
		!a.RenewedAt.Equal(a.AcquiredAt) || a.ExpiresAt.Sub(a.AcquiredAt) != time.Hour {
		t.Errorf("fresh lease = %+v; want holder replica-a, fence 1, live, expiring ttl after acquisition", a)
	}
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", time.Hour); !errors.Is(err, ErrLeaseHeldByOther) {
		t.Fatalf("second holder while live = %v; want ErrLeaseHeldByOther", err)
	}
	// Asking for a longer ttl does not help the challenger.
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", 24*time.Hour); !errors.Is(err, ErrLeaseHeldByOther) {
		t.Fatalf("second holder with a longer ttl = %v; want ErrLeaseHeldByOther", err)
	}
	got, err := s.GetLease(ctx, cluster)
	if err != nil || got.Holder != "replica-a" || got.Fence != 1 || got.Expired {
		t.Errorf("GetLease after rejected challenger = (%+v, %v); want replica-a, fence 1, live", got, err)
	}
	// Another cluster's lease is independent.
	if _, err := s.AcquireOrRenewLease(ctx, testOwnerID(t), "replica-b", time.Hour); err != nil {
		t.Errorf("acquire for another cluster: %v", err)
	}
	if _, err := s.GetLease(ctx, testOwnerID(t)); !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("GetLease for a never-leased cluster = %v; want ErrLeaseNotFound", err)
	}
	for _, bad := range []struct {
		cluster, holder string
		ttl             time.Duration
	}{{cluster, "", time.Hour}, {"", "x", time.Hour}, {cluster, "x", 0}, {cluster, "x", -time.Second}} {
		if _, err := s.AcquireOrRenewLease(ctx, bad.cluster, bad.holder, bad.ttl); err == nil || errors.Is(err, ErrLeaseHeldByOther) {
			t.Errorf("AcquireOrRenewLease(%q, %q, %s) = %v; want an input error", bad.cluster, bad.holder, bad.ttl, err)
		}
	}
}

// TestAcquireOrRenewLease_TakeoverAfterExpiryIncrementsFence also races the
// takeover: eight challengers against one expired lease yield exactly one
// new holder and a fence that moved by exactly one.
func TestAcquireOrRenewLease_TakeoverAfterExpiryIncrementsFence(t *testing.T) {
	const challengers = 8
	pool := wideTestPool(t, challengers)
	s := NewBackupAssuranceStore(pool)
	ctx := t.Context()
	cluster := testOwnerID(t)

	a, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	backdateLease(t, pool, cluster)
	if got, _ := s.GetLease(ctx, cluster); !got.Expired {
		t.Fatal("backdated lease still reads as live")
	}

	leases := make([]AssuranceLease, challengers)
	errs := make([]error, challengers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range challengers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			leases[i], errs[i] = s.AcquireOrRenewLease(ctx, cluster, "replica-"+string(rune('b'+i)), time.Hour)
		}()
	}
	close(start)
	wg.Wait()

	var winner AssuranceLease
	var wins int
	for i := range challengers {
		switch {
		case errs[i] == nil:
			wins++
			winner = leases[i]
		case !errors.Is(errs[i], ErrLeaseHeldByOther):
			t.Fatalf("challenger %d: %v", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("%d of %d challengers took over an expired lease; want exactly 1", wins, challengers)
	}
	if winner.Fence != a.Fence+1 || winner.Holder == "replica-a" || !winner.AcquiredAt.After(a.AcquiredAt) || winner.Expired {
		t.Errorf("takeover = %+v; want fence %d, a new holder, a fresh acquired_at, live", winner, a.Fence+1)
	}
	// The former incumbent is now the one rejected.
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour); !errors.Is(err, ErrLeaseHeldByOther) {
		t.Errorf("former holder after takeover = %v; want ErrLeaseHeldByOther", err)
	}
	got, err := s.GetLease(ctx, cluster)
	if err != nil || got.Holder != winner.Holder || got.Fence != winner.Fence {
		t.Errorf("GetLease = (%+v, %v); want the winner", got, err)
	}

	// The former holder returning after its own lease expired starts a new
	// epoch too: tenure was not continuous, so the fence must say so.
	backdateLease(t, pool, cluster)
	back, err := s.AcquireOrRenewLease(ctx, cluster, winner.Holder, time.Hour)
	if err != nil || back.Fence != winner.Fence+1 || !back.AcquiredAt.After(winner.AcquiredAt) {
		t.Errorf("same holder re-acquiring an expired lease = (%+v, %v); want fence %d and a new acquired_at", back, err, winner.Fence+1)
	}
}

func TestAcquireOrRenewLease_RenewByIncumbentPreservesAcquiredAtAndFence(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)

	first, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Age the row so the renewal's NOW() is visibly later than acquisition.
	if _, err := pool.Exec(ctx, `
		UPDATE backup_assurance_collector_lease
		   SET acquired_at = acquired_at - interval '10 minutes',
		       renewed_at  = renewed_at  - interval '10 minutes',
		       expires_at  = expires_at  - interval '10 minutes'
		 WHERE cluster_id = $1`, cluster); err != nil {
		t.Fatalf("aging lease: %v", err)
	}
	aged, _ := s.GetLease(ctx, cluster)

	renewed, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.Fence != first.Fence || !renewed.AcquiredAt.Equal(aged.AcquiredAt) {
		t.Errorf("renewal changed fence/acquired_at: %+v vs %+v", renewed, aged)
	}
	if !renewed.RenewedAt.After(aged.RenewedAt) || !renewed.ExpiresAt.After(aged.ExpiresAt) ||
		renewed.ExpiresAt.Sub(renewed.RenewedAt) != time.Hour {
		t.Errorf("renewal did not extend: renewed_at %v→%v expires_at %v→%v",
			aged.RenewedAt, renewed.RenewedAt, aged.ExpiresAt, renewed.ExpiresAt)
	}
	// A shorter ttl on renewal shortens the lease: the database stamps NOW()+ttl, nothing else.
	short, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Minute)
	if err != nil || short.ExpiresAt.Sub(short.RenewedAt) != time.Minute || short.Fence != first.Fence {
		t.Errorf("renew with a shorter ttl = (%+v, %v); want expiry one minute after renewal, same fence", short, err)
	}
}

func TestReleaseLease_OnlyByHolder(t *testing.T) {
	s, _ := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)

	a, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", time.Hour)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// A non-holder cannot steal by releasing: no-op, nil error, lease intact.
	if err := s.ReleaseLease(ctx, cluster, "replica-b"); err != nil {
		t.Fatalf("ReleaseLease by a non-holder = %v; want nil (no-op)", err)
	}
	if got, _ := s.GetLease(ctx, cluster); got.Holder != "replica-a" || got.Expired {
		t.Fatalf("non-holder release changed the lease: %+v", got)
	}
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", time.Hour); !errors.Is(err, ErrLeaseHeldByOther) {
		t.Fatalf("challenger after a bogus release = %v; want ErrLeaseHeldByOther", err)
	}

	// The holder releases: the lease is expired on the database clock, the
	// row and its fence history remain, and the next taker starts a new epoch.
	if err := s.ReleaseLease(ctx, cluster, "replica-a"); err != nil {
		t.Fatalf("ReleaseLease by the holder: %v", err)
	}
	got, err := s.GetLease(ctx, cluster)
	if err != nil || !got.Expired || got.Holder != "replica-a" || got.Fence != a.Fence {
		t.Errorf("after release GetLease = (%+v, %v); want the same row, expired", got, err)
	}
	b, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", time.Hour)
	if err != nil || b.Fence != a.Fence+1 || b.Expired {
		t.Errorf("acquire after release = (%+v, %v); want fence %d, live", b, err, a.Fence+1)
	}
	// Releasing twice, or releasing a cluster that has no lease, is harmless.
	if err := s.ReleaseLease(ctx, cluster, "replica-a"); err != nil {
		t.Errorf("stale second release = %v; want nil", err)
	}
	if err := s.ReleaseLease(ctx, testOwnerID(t), "replica-a"); err != nil {
		t.Errorf("release of a never-leased cluster = %v; want nil", err)
	}
	if got, _ := s.GetLease(ctx, cluster); got.Holder != "replica-b" || got.Expired {
		t.Errorf("stale release disturbed the new holder: %+v", got)
	}
}

// TestLeaseUsesDatabaseClockNotProcessClock: the store never consults the
// process clock (pinned hermetically above), so the only clock that can
// expire a lease is PostgreSQL's. Here the process clock is made to disagree
// wildly with the database — the test "advances" it by treating a Go time far
// in the future as now — and nothing about the lease changes: expiry is
// relative to the database's NOW(), and the challenger is still rejected.
func TestLeaseUsesDatabaseClockNotProcessClock(t *testing.T) {
	s, pool := newAssuranceStore(t)
	ctx := t.Context()
	cluster := testOwnerID(t)

	a, err := s.AcquireOrRenewLease(ctx, cluster, "replica-a", 2*time.Hour)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		t.Fatalf("SELECT NOW(): %v", err)
	}
	if remaining := a.ExpiresAt.Sub(dbNow); remaining < 2*time.Hour-time.Minute || remaining > 2*time.Hour {
		t.Errorf("expiry is %s from the database clock; want ~2h (ttl is applied to NOW(), not to a process timestamp)", remaining)
	}

	// The process believes hours have passed. The database does not care.
	skewedProcessNow := time.Now().Add(48 * time.Hour)
	if skewedProcessNow.Before(a.ExpiresAt) {
		t.Fatal("test setup: the skewed process clock should be past the lease expiry")
	}
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", time.Hour); !errors.Is(err, ErrLeaseHeldByOther) {
		t.Errorf("challenger with a skewed process clock = %v; want ErrLeaseHeldByOther (expiry is judged by the database)", err)
	}
	got, _ := s.GetLease(ctx, cluster)
	if got.Expired || got.Holder != "replica-a" {
		t.Errorf("GetLease = %+v; want live and held by replica-a", got)
	}

	// Conversely, moving the database's view of expiry is what enables a
	// takeover — no process time is involved.
	backdateLease(t, pool, cluster)
	if _, err := s.AcquireOrRenewLease(ctx, cluster, "replica-b", time.Hour); err != nil {
		t.Errorf("takeover after database-side expiry = %v; want success", err)
	}
}
