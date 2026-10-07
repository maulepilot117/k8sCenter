package notifications

// service_emit_test.go — the EmitSync result seam (Release F U34a).
//
// EmitSync is Emit with a reported outcome, added so a caller holding a
// durable delivery intent (the backup-assurance drain, U34b) can decide
// whether to mark the intent delivered or retry. Emit is now a thin wrapper
// over EmitSync, so these tests double as the regression suite for every
// existing emit site: the golden table in
// TestEmit_DelegatesToEmitSyncWithIdenticalObservableBehaviour runs every
// Source through Emit and pins the persisted row, the WebSocket payload
// bytes, the queued dispatch item and the dedup-on-repeat behaviour.
//
// PostgreSQL-backed tests share testNotifStore's gate (store_dedup_test.go):
// KUBECENTER_TEST_DATABASE_URL unset skips, KUBECENTER_TEST_REQUIRE_DATABASE
// turns the skip into a failure in CI. The cancellation and store-unavailable
// tests are hermetic: pgxpool connects lazily, so a pool pointed at a closed
// port never needs a server.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubecenter/kubecenter/internal/websocket"
)

// allSources is every Source constant declared in types.go. The golden
// table runs each through Emit; TestAllSources_MatchesTypesGo fails when a
// Source is added to types.go without being added here, so the regression
// table cannot silently lose coverage.
var allSources = []Source{
	SourceAlert,
	SourcePolicy,
	SourceGitOps,
	SourceDiagnostic,
	SourceScan,
	SourceCluster,
	SourceAudit,
	SourceLimits,
	SourceVelero,
	SourceCertManager,
	SourceExternalSecrets,
}

func TestAllSources_MatchesTypesGo(t *testing.T) {
	src, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatalf("read types.go: %v", err)
	}
	declared := regexp.MustCompile(`(?m)^\s*Source\w+\s+Source\s*=\s*"`).FindAll(src, -1)
	if len(declared) != len(allSources) {
		t.Fatalf("types.go declares %d Source constants, allSources lists %d; update allSources so the Emit golden table covers every source", len(declared), len(allSources))
	}
	seen := map[Source]bool{}
	for _, s := range allSources {
		if !s.Valid() {
			t.Errorf("allSources entry %q is not Valid()", s)
		}
		if seen[s] {
			t.Errorf("allSources lists %q twice", s)
		}
		seen[s] = true
	}
}

// --- Hermetic ---------------------------------------------------------------

func TestEmitResult_DeliveredAndString(t *testing.T) {
	cases := []struct {
		res       EmitResult
		delivered bool
		str       string
	}{
		{EmitFailed, false, "failed"},
		{EmitPersisted, true, "persisted"},
		{EmitDeduped, true, "deduped"},
		{EmitSkipped, true, "skipped"},
		{EmitResult(99), false, "EmitResult(99)"},
	}
	for _, tc := range cases {
		if got := tc.res.Delivered(); got != tc.delivered {
			t.Errorf("%v.Delivered() = %v, want %v", tc.res, got, tc.delivered)
		}
		if got := tc.res.String(); got != tc.str {
			t.Errorf("EmitResult(%d).String() = %q, want %q", int(tc.res), got, tc.str)
		}
	}
	// The zero value must never read as delivered: a drain loop that ignores
	// the error return still cannot mark a failed emit as delivered.
	var zero EmitResult
	if zero != EmitFailed || zero.Delivered() {
		t.Fatalf("zero EmitResult must be EmitFailed and not Delivered, got %v", zero)
	}
}

func TestBroadcastPayload_StrippedShapeForEverySource(t *testing.T) {
	for _, src := range allSources {
		n := Notification{
			ID:           "11111111-2222-3333-4444-555555555555",
			Source:       src,
			Severity:     SeverityCritical,
			Title:        "title for " + string(src),
			Message:      "must not appear",
			ResourceKind: "Pod",
			ResourceNS:   "tenant-a",
			ResourceName: "secret-name",
			ResourceUID:  "uid-1",
			ClusterID:    "prod-east",
		}
		want := fmt.Sprintf(`{"id":%q,"severity":"critical","source":%q,"title":%q}`, n.ID, src, n.Title)
		if got := string(broadcastPayload(n)); got != want {
			t.Errorf("broadcastPayload(%s):\n got %s\nwant %s", src, got, want)
		}
	}
}

// unreachableStore returns a Store over a pool that connects lazily to a
// closed loopback port. No server is involved; the first query fails.
func unreachableStore(t *testing.T) *Store {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MinConns = 0
	cfg.MaxConns = 1
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool, "")
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// broadcastCall is one hub.HandleEvent invocation as the service made it.
type broadcastCall struct {
	eventType, kind, namespace, name string
	payload                          string
}

// recordingBroadcaster stands in for *websocket.Hub so a test can assert the
// broadcast a notification actually produced, not a recomputation of it.
type recordingBroadcaster struct {
	mu    sync.Mutex
	calls []broadcastCall
}

func (r *recordingBroadcaster) HandleEvent(eventType, kind, namespace, name string, obj any) {
	raw, ok := obj.(json.RawMessage)
	if !ok {
		panic(fmt.Sprintf("broadcast payload is %T, want json.RawMessage", obj))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, broadcastCall{eventType: eventType, kind: kind, namespace: namespace, name: name, payload: string(raw)})
}

// take returns the recorded calls and clears them.
func (r *recordingBroadcaster) take() []broadcastCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

// newTestService builds a service over st whose hub is a recorder. A real
// *websocket.Hub satisfies the same seam in production (NewService).
func newTestService(st *Store) (*NotificationService, *recordingBroadcaster) {
	logger := testLogger()
	svc := NewService(st, websocket.NewHub(logger, nil), nil, nil, logger)
	rec := &recordingBroadcaster{}
	svc.hub = rec
	return svc, rec
}

// assertBroadcast checks that exactly one feed event was broadcast for the
// persisted row id, with the stripped {id, severity, source, title} payload.
func assertBroadcast(t *testing.T, rec *recordingBroadcaster, id string, n Notification) {
	t.Helper()
	calls := rec.take()
	if len(calls) != 1 {
		t.Fatalf("broadcast calls = %d, want 1: %+v", len(calls), calls)
	}
	want := broadcastCall{
		eventType: "ADDED",
		kind:      "notifications",
		namespace: "",
		name:      id,
		payload:   fmt.Sprintf(`{"id":%q,"severity":%q,"source":%q,"title":%q}`, id, n.Severity, n.Source, n.Title),
	}
	if calls[0] != want {
		t.Errorf("broadcast call differs:\n got %+v\nwant %+v", calls[0], want)
	}
}

// assertNoBroadcast checks that nothing reached the hub.
func assertNoBroadcast(t *testing.T, rec *recordingBroadcaster) {
	t.Helper()
	if calls := rec.take(); len(calls) != 0 {
		t.Errorf("expected no broadcast, got %+v", calls)
	}
}

// unseenRow returns the one row whose ID is not in seen and marks it seen.
// Two inserts in quick succession can share created_at, so "newest first"
// is not a safe way to find the row a given call produced.
func unseenRow(t *testing.T, rows []Notification, seen map[string]bool) Notification {
	t.Helper()
	var out []Notification
	for _, r := range rows {
		if !seen[r.ID] {
			out = append(out, r)
		}
	}
	if len(out) != 1 {
		t.Fatalf("unseen rows = %d, want 1: %+v", len(out), out)
	}
	seen[out[0].ID] = true
	return out[0]
}

func TestEmitSync_ContextCancellationSurfacesError(t *testing.T) {
	svc, rec := newTestService(unreachableStore(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := svc.EmitSync(ctx, Notification{Source: SourceVelero, Severity: SeverityWarning, Title: "cancelled"})
	if err == nil {
		t.Fatal("EmitSync with a cancelled context must return an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled, got %v", err)
	}
	if res != EmitFailed || res.Delivered() {
		t.Errorf("result = %v (Delivered=%v), want EmitFailed", res, res.Delivered())
	}
	if len(svc.queue) != 0 {
		t.Errorf("nothing may be enqueued for dispatch on failure, queue len = %d", len(svc.queue))
	}
	assertNoBroadcast(t, rec)
}

func TestEmitSync_StoreUnavailableReturnsError(t *testing.T) {
	svc, rec := newTestService(unreachableStore(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, src := range []Source{SourceVelero, SourceAudit} {
		res, err := svc.EmitSync(ctx, Notification{Source: src, Severity: SeverityWarning, Title: "db down"})
		if err == nil {
			t.Fatalf("%s: EmitSync against an unreachable store must return an error", src)
		}
		if res != EmitFailed || res.Delivered() {
			t.Errorf("%s: result = %v (Delivered=%v), want EmitFailed", src, res, res.Delivered())
		}
	}
	if len(svc.queue) != 0 {
		t.Errorf("nothing may be enqueued for dispatch on failure, queue len = %d", len(svc.queue))
	}
	assertNoBroadcast(t, rec)
}

// --- PostgreSQL-backed ------------------------------------------------------

// emitNotification is one uniquely named notification for src with every
// field populated, so the golden table can prove each one round-trips.
func emitNotification(t *testing.T, src Source) Notification {
	t.Helper()
	return Notification{
		Source:                 src,
		Severity:               SeverityWarning,
		Title:                  "U34a golden " + string(src),
		Message:                "message for " + string(src),
		ResourceKind:           "Pod",
		ResourceNS:             "team-a",
		ResourceName:           uniqueName(t),
		ResourceUID:            "uid-" + string(src),
		ClusterID:              "",
		SuppressResourceFields: true,
	}
}

// rowsFor returns the persisted rows for n's unique resource name.
func rowsFor(t *testing.T, st *Store, n Notification) []Notification {
	t.Helper()
	all, err := st.NotificationsSince(t.Context(), time.Now().Add(-time.Hour), nil, []string{string(n.Source)}, nil)
	if err != nil {
		t.Fatalf("NotificationsSince: %v", err)
	}
	var out []Notification
	for _, row := range all {
		if row.ResourceName == n.ResourceName {
			out = append(out, row)
		}
	}
	return out
}

// assertRowMatches checks the persisted row carries exactly the input's
// persisted fields. ID and CreatedAt are database-generated;
// SuppressResourceFields is json:"-" and never stored.
func assertRowMatches(t *testing.T, row, in Notification) {
	t.Helper()
	got := Notification{
		Source: row.Source, Severity: row.Severity, Title: row.Title, Message: row.Message,
		ResourceKind: row.ResourceKind, ResourceNS: row.ResourceNS, ResourceName: row.ResourceName,
		ResourceUID: row.ResourceUID, ClusterID: row.ClusterID,
	}
	want := Notification{
		Source: in.Source, Severity: in.Severity, Title: in.Title, Message: in.Message,
		ResourceKind: in.ResourceKind, ResourceNS: in.ResourceNS, ResourceName: in.ResourceName,
		ResourceUID: in.ResourceUID, ClusterID: in.ClusterID,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("persisted row differs from input:\n got %+v\nwant %+v", got, want)
	}
	if row.ID == "" || row.CreatedAt.IsZero() {
		t.Errorf("persisted row must carry a database id and created_at, got id=%q created_at=%v", row.ID, row.CreatedAt)
	}
}

// takeQueued returns the one notification enqueued for external dispatch,
// failing when the queue holds anything but exactly one item.
func takeQueued(t *testing.T, svc *NotificationService) Notification {
	t.Helper()
	if got := len(svc.queue); got != 1 {
		t.Fatalf("dispatch queue len = %d, want 1", got)
	}
	return <-svc.queue
}

func TestEmitSync_ReturnsEmitPersistedOnFirstEmit(t *testing.T) {
	st := testNotifStore(t)
	svc, rec := newTestService(st)
	n := emitNotification(t, SourceVelero)

	res, err := svc.EmitSync(t.Context(), n)
	if err != nil {
		t.Fatalf("EmitSync: %v", err)
	}
	if res != EmitPersisted || !res.Delivered() {
		t.Fatalf("result = %v (Delivered=%v), want EmitPersisted", res, res.Delivered())
	}
	rows := rowsFor(t, st, n)
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	assertRowMatches(t, rows[0], n)
	assertBroadcast(t, rec, rows[0].ID, n)
	if q := takeQueued(t, svc); !reflect.DeepEqual(q, n) {
		t.Errorf("queued item differs from input:\n got %+v\nwant %+v", q, n)
	}
}

func TestEmitSync_ReturnsEmitDedupedInsideWindow(t *testing.T) {
	st := testNotifStore(t)
	svc, rec := newTestService(st)
	n := emitNotification(t, SourceVelero)

	if res, err := svc.EmitSync(t.Context(), n); err != nil || res != EmitPersisted {
		t.Fatalf("first EmitSync = (%v, %v), want (EmitPersisted, nil)", res, err)
	}
	takeQueued(t, svc)
	rec.take()

	res, err := svc.EmitSync(t.Context(), n)
	if err != nil {
		t.Fatalf("second EmitSync: %v", err)
	}
	if res != EmitDeduped {
		t.Fatalf("second EmitSync result = %v, want EmitDeduped", res)
	}
	// Deduped counts as delivered: the feed already carries the entry.
	if !res.Delivered() {
		t.Error("EmitDeduped must report Delivered()")
	}
	if rows := rowsFor(t, st, n); len(rows) != 1 {
		t.Errorf("persisted rows after dedup = %d, want 1", len(rows))
	}
	if len(svc.queue) != 0 {
		t.Errorf("a deduped emit must not enqueue dispatch, queue len = %d", len(svc.queue))
	}
	assertNoBroadcast(t, rec)

	// A different UID is a different resource (migration 000021): not a dup.
	recreated := n
	recreated.ResourceUID = "uid-recreated"
	res, err = svc.EmitSync(t.Context(), recreated)
	if err != nil || res != EmitPersisted {
		t.Fatalf("EmitSync for recreated UID = (%v, %v), want (EmitPersisted, nil)", res, err)
	}
	takeQueued(t, svc)
	if calls := rec.take(); len(calls) != 1 {
		t.Errorf("recreated UID must broadcast once, got %+v", calls)
	}
}

// TestEmitSync_ConcurrentEmitsOfOneIdentityPersistOnce is the regression for
// #590. Two backup-assurance replicas can claim the same pending delivery
// intent and send it at the same moment; the dedup window is what is meant
// to absorb that re-send. A check-then-insert dedup lets every concurrent
// caller see "no row yet" and persist its own copy, so each round here races
// several emits of one identity and requires exactly one to persist.
func TestEmitSync_ConcurrentEmitsOfOneIdentityPersistOnce(t *testing.T) {
	st := testNotifStore(t)
	svc, rec := newTestService(st)
	const rounds, emitters = 20, 4 // emitters matches testNotifStore's MaxConns

	for round := range rounds {
		n := emitNotification(t, SourceVelero)
		results := make([]EmitResult, emitters)
		errs := make([]error, emitters)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range emitters {
			wg.Go(func() {
				<-start
				results[i], errs[i] = svc.EmitSync(t.Context(), n)
			})
		}
		close(start)
		wg.Wait()

		persisted := 0
		for i := range emitters {
			if errs[i] != nil {
				t.Fatalf("round %d emitter %d: %v", round, i, errs[i])
			}
			switch results[i] {
			case EmitPersisted:
				persisted++
			case EmitDeduped:
			default:
				t.Fatalf("round %d emitter %d result = %v, want EmitPersisted or EmitDeduped", round, i, results[i])
			}
		}
		if rows := rowsFor(t, st, n); len(rows) != 1 || persisted != 1 {
			t.Fatalf("round %d: persisted rows = %d, EmitPersisted results = %d; want exactly one of each", round, len(rows), persisted)
		}
		takeQueued(t, svc)
		if calls := rec.take(); len(calls) != 1 {
			t.Fatalf("round %d: broadcast calls = %d, want 1", round, len(calls))
		}
	}
}

func TestEmitSync_ReturnsEmitSkippedForAuditSource(t *testing.T) {
	st := testNotifStore(t)
	svc, rec := newTestService(st)
	n := emitNotification(t, SourceAudit)
	seen := map[string]bool{}

	for i := 1; i <= 2; i++ {
		res, err := svc.EmitSync(t.Context(), n)
		if err != nil {
			t.Fatalf("EmitSync #%d: %v", i, err)
		}
		if res != EmitSkipped || !res.Delivered() {
			t.Fatalf("EmitSync #%d result = %v (Delivered=%v), want EmitSkipped", i, res, res.Delivered())
		}
		// Audit is persisted and broadcast but never deduped and never
		// externally dispatched — the pre-existing short circuit.
		rows := rowsFor(t, st, n)
		if len(rows) != i {
			t.Fatalf("after EmitSync #%d persisted rows = %d, want %d (audit is never deduped)", i, len(rows), i)
		}
		assertBroadcast(t, rec, unseenRow(t, rows, seen).ID, n)
		if len(svc.queue) != 0 {
			t.Errorf("audit must never be enqueued for external dispatch, queue len = %d", len(svc.queue))
		}
	}
}

func TestEmitSync_QueueFullStillReportsPersisted(t *testing.T) {
	st := testNotifStore(t)
	svc, rec := newTestService(st)
	// A one-slot queue already holding an item: the external leg is dropped.
	svc.queue = make(chan Notification, 1)
	occupant := Notification{Source: SourceAlert, Title: "occupant"}
	svc.queue <- occupant

	n := emitNotification(t, SourceVelero)
	res, err := svc.EmitSync(t.Context(), n)
	if err != nil {
		t.Fatalf("EmitSync: %v", err)
	}
	if res != EmitPersisted {
		t.Fatalf("result = %v, want EmitPersisted (persisted is not the same as dispatched)", res)
	}
	rows := rowsFor(t, st, n)
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	// The feed and the WebSocket leg still happen; only the external leg drops.
	assertBroadcast(t, rec, rows[0].ID, n)
	if got := takeQueued(t, svc); !reflect.DeepEqual(got, occupant) {
		t.Errorf("queue occupant was displaced: got %+v", got)
	}
}

// TestEmit_DelegatesToEmitSyncWithIdenticalObservableBehaviour is the
// regression table for the Emit → EmitSync refactor. For every Source it
// pins, as golden values, the pre-change behaviour of Emit:
//
//   - the persisted feed row is exactly the input (ID/CreatedAt generated);
//   - the hub receives exactly one ("ADDED", "notifications", "", <row id>)
//     event whose payload is the stripped {id, severity, source, title}
//     object, with keys in json.Marshal's sorted order — observed through
//     the broadcaster seam, not recomputed;
//   - the item enqueued for channel dispatch is the pre-persist copy of the
//     input (no ID, SuppressResourceFields retained) — except audit, which
//     is never enqueued;
//   - a repeat inside the dedup window persists nothing, broadcasts nothing
//     and enqueues nothing — except audit, which is persisted and broadcast
//     again every time.
func TestEmit_DelegatesToEmitSyncWithIdenticalObservableBehaviour(t *testing.T) {
	st := testNotifStore(t)
	for _, src := range allSources {
		t.Run(string(src), func(t *testing.T) {
			svc, rec := newTestService(st)
			n := emitNotification(t, src)
			isAudit := src == SourceAudit

			svc.Emit(t.Context(), n)

			rows := rowsFor(t, st, n)
			if len(rows) != 1 {
				t.Fatalf("after first Emit persisted rows = %d, want 1", len(rows))
			}
			assertRowMatches(t, rows[0], n)
			seen := map[string]bool{rows[0].ID: true}
			assertBroadcast(t, rec, rows[0].ID, n)

			if isAudit {
				if len(svc.queue) != 0 {
					t.Errorf("audit must not be enqueued, queue len = %d", len(svc.queue))
				}
			} else if q := takeQueued(t, svc); !reflect.DeepEqual(q, n) {
				t.Errorf("queued item differs from input:\n got %+v\nwant %+v", q, n)
			}

			svc.Emit(t.Context(), n)

			rows = rowsFor(t, st, n)
			if isAudit {
				if len(rows) != 2 {
					t.Fatalf("after repeat Emit persisted rows = %d, want 2 (audit is never deduped)", len(rows))
				}
				assertBroadcast(t, rec, unseenRow(t, rows, seen).ID, n)
			} else {
				if len(rows) != 1 {
					t.Errorf("after repeat Emit persisted rows = %d, want 1", len(rows))
				}
				assertNoBroadcast(t, rec)
			}
			if len(svc.queue) != 0 {
				t.Errorf("repeat Emit must not enqueue, queue len = %d", len(svc.queue))
			}
		})
	}
}
