package velero

// assurance_service_failure_test.go — the collector's failure paths: a
// Velero read that fails with Velero present, a shared read cancelled by
// another caller, a wedged store call against the tick deadline, every
// reconcile and drain sentinel, the real store's observation rules as the
// fake mirrors them, and the notification dedup identity. These are the
// branches a happy-path suite never reaches and a regression in any of them
// is silent in production.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Collection failures with Velero present
// ---------------------------------------------------------------------------

func TestAssurance_VeleroReadFailureLeavesOpenExceptionsOpen(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...) // detected, but the list fails
	ah.localDyn().PrependReactor("list", "schedules", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the server is currently unable to handle the request")
	})
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	id := ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-daily", store.ConditionOverdue)

	ah.tick(t)
	open := ah.st.open()
	if got := conditionsOpen(open); len(got) != 2 || got[0] != store.ConditionCollectionUnknown || got[1] != store.ConditionOverdue {
		t.Fatalf("open conditions = %v; want the seeded overdue kept open plus one collection_unknown", got)
	}
	for _, e := range open {
		if e.ID == id && e.State != store.AssuranceStateOpen {
			t.Errorf("seeded exception resolved on a failed read")
		}
	}
	s := ah.svc.Snapshot()
	if s.LastCollection != CollectionFailed || s.LastError != "" {
		t.Errorf("snapshot = %+v; want LastCollection=failed and no tick error (a failed read is a collection outcome, not a tick failure)", s)
	}
}

func TestAssurance_ForeignCancellationOfSharedReadIsRetriedNotRecorded(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	// The first list fails the way a singleflight waiter sees a sibling
	// request's abandoned context; the retry runs clean.
	var failed atomic.Bool
	ah.localDyn().PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failed.CompareAndSwap(false, true) {
			return true, nil, context.Canceled
		}
		return false, nil, nil
	})

	ah.tick(t)
	if got := conditionsOpen(ah.st.open()); len(got) != 1 || got[0] != store.ConditionOverdue {
		t.Fatalf("open conditions = %v; want [overdue] only, no collection_unknown from a cancellation that was not ours", got)
	}
	if s := ah.svc.Snapshot(); s.LastCollection != CollectionOK || s.LastError != "" {
		t.Errorf("snapshot = %+v; want an ok collection", s)
	}
	if !failed.Load() {
		t.Fatal("the injected cancellation never fired; the test proved nothing")
	}
}

func TestAssurance_OwnCancellationOfReadIsNotRetried(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	var lists atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	ah.localDyn().PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		cancel() // the tick's own context ends mid-read
		return true, nil, context.Canceled
	})
	ah.svc.tick(ctx)
	if n := lists.Load(); n != 1 {
		t.Errorf("backups listed %d times; want 1 (no retry when the cancellation is ours)", n)
	}
	if got := ah.st.all(); len(got) != 0 {
		t.Errorf("exceptions = %+v; a tick ended by shutdown writes nothing", got)
	}
}

// ---------------------------------------------------------------------------
// Tick deadline versus shutdown
// ---------------------------------------------------------------------------

func TestAssurance_WedgedStoreCallIsBoundedByTickDeadline(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.svc.tickTimeout = 20 * time.Millisecond
	ah.st.mu.Lock()
	ah.st.acquireHook = func(ctx context.Context) { <-ctx.Done() } // a hung pgx call
	ah.st.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ah.svc.runTickWithRecover(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tick did not return; the deadline did not bound the wedged call")
	}
	s := ah.svc.Snapshot()
	if s.LastError != "acquire lease" || s.LastErrorAt.IsZero() || s.LeaseHeld {
		t.Fatalf("snapshot after a wedged tick = %+v; want LastError=acquire lease and LeaseHeld=false", s)
	}

	// The next tick is a clean slate.
	ah.st.mu.Lock()
	ah.st.acquireHook = nil
	ah.st.mu.Unlock()
	ah.svc.runTickWithRecover(context.Background())
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Errorf("snapshot after recovery = %+v; want the error cleared and a completed run", s)
	}
}

func TestAssurance_ShutdownMidTickIsNotRecordedAsError(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.st.mu.Lock()
	ah.st.acquireHook = func(ctx context.Context) { <-ctx.Done() }
	ah.st.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ah.svc.runTickWithRecover(ctx)
	if s := ah.svc.Snapshot(); s.LastError != "" || !s.LastRunAt.IsZero() {
		t.Errorf("snapshot = %+v; a tick ended by shutdown is neither an error nor a run", s)
	}
}

// ---------------------------------------------------------------------------
// Reconcile sentinels and errors
// ---------------------------------------------------------------------------

func TestAssurance_OpenSentinelsAreSkippedNotFatal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		wantErr string
	}{
		{"contended", store.ErrAssuranceOpenContended, ""},
		{"invalid", store.ErrAssuranceExceptionInvalid, ""},
		{"policy gone", store.ErrAssurancePolicyNotFound, ""},
		{"outage", errors.New("pg: connection refused"), "reconcile exceptions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
			ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
			ah.st.mu.Lock()
			ah.st.openErr = tc.err
			ah.st.mu.Unlock()
			ah.tick(t)
			if got := ah.st.all(); len(got) != 0 {
				t.Errorf("exceptions = %+v; want none", got)
			}
			s := ah.svc.Snapshot()
			if s.LastError != tc.wantErr {
				t.Errorf("LastError = %q, want %q", s.LastError, tc.wantErr)
			}
			if (tc.wantErr == "") != !s.LastRunAt.IsZero() {
				t.Errorf("LastRunAt zero=%v for wantErr=%q", s.LastRunAt.IsZero(), tc.wantErr)
			}
		})
	}
}

func TestAssurance_LostOpenRaceFallsBackToObservation(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	// Another replica opens the identity between this tick's
	// ListOpenExceptions and its OpenExceptionAndEnqueue.
	// A CAS, not sync.Once: seedOpen re-enters OpenExceptionAndEnqueue and
	// so this hook, and a nested Once.Do would deadlock.
	var raced atomic.Bool
	ah.st.mu.Lock()
	ah.st.beforeOpen = func() {
		if raced.CompareAndSwap(false, true) {
			ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-daily", store.ConditionOverdue)
		}
	}
	ah.st.mu.Unlock()

	ah.tick(t)
	all := ah.st.all()
	if len(all) != 1 || all[0].ObservationCount != 2 || all[0].State != store.AssuranceStateOpen {
		t.Fatalf("exceptions = %+v; want the winner's one row, observed by the loser", all)
	}
	if n := len(ah.emit.notifications()); n != 0 {
		t.Errorf("notifications = %d; the loser must not notify (the seed's intent was already delivered)", n)
	}
	if s := ah.svc.Snapshot(); s.LastError != "" {
		t.Errorf("LastError = %q; a lost race is not a failure", s.LastError)
	}
}

func TestAssurance_ResolveOfAlreadyResolvedRowIsNotAnError(t *testing.T) {
	now := time.Now()
	ah := newAssuranceHarness(t, true, overdueCluster(now)...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)
	if err := ah.localDyn().Tracker().Delete(ScheduleGVR, veleroNamespace, "daily"); err != nil {
		t.Fatal(err)
	}
	ah.resetCache()
	// Another replica resolves the row first; this one's resolve finds it
	// already terminal.
	ah.st.mu.Lock()
	ah.st.beforeResolve = func(id uuid.UUID) {
		ah.st.mu.Lock()
		defer ah.st.mu.Unlock()
		ah.st.exceptions[id].State = store.AssuranceStateResolved
	}
	ah.st.mu.Unlock()
	ah.advance(time.Minute)
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Errorf("snapshot = %+v; (false, nil) from resolve is not a failure", s)
	}
	if ds := ah.st.deliveryList(); len(ds) != 1 {
		t.Errorf("deliveries = %+v; the loser must not enqueue a second resolution", ds)
	}
}

func TestAssurance_ResolveErrorAbortsTickButKeepsEarlierWork(t *testing.T) {
	now := time.Now()
	ah := newAssuranceHarness(t, true, overdueCluster(now)...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)
	if err := ah.localDyn().Tracker().Delete(ScheduleGVR, veleroNamespace, "daily"); err != nil {
		t.Fatal(err)
	}
	ah.resetCache()
	ah.st.mu.Lock()
	ah.st.resolveErr = errors.New("pg: deadlock detected")
	ah.st.mu.Unlock()
	ah.advance(time.Minute)
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "reconcile exceptions" {
		t.Errorf("LastError = %q, want reconcile exceptions", s.LastError)
	}
	if open := ah.st.open(); len(open) != 1 {
		t.Errorf("open = %+v; the row stays open for the next tick", open)
	}
}

func TestAssurance_ListOpenErrorAbortsTick(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.st.mu.Lock()
	ah.st.listOpenErr = errors.New("pg: down")
	ah.st.mu.Unlock()
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "list open exceptions" {
		t.Errorf("LastError = %q, want list open exceptions", s.LastError)
	}
	if got := ah.st.all(); len(got) != 0 {
		t.Errorf("exceptions = %+v; nothing may be opened without the open set", got)
	}
}

// ---------------------------------------------------------------------------
// Observation rules
// ---------------------------------------------------------------------------

func TestAssurance_StaleObservationIsDroppedWithoutError(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.tick(t)
	ah.advance(-time.Hour) // this replica's clock is behind the one that opened the row
	ah.tick(t)
	all := ah.st.all()
	if len(all) != 1 || all[0].ObservationCount != 1 {
		t.Fatalf("exceptions = %+v; a stale observation must be dropped, not applied", all)
	}
	if s := ah.svc.Snapshot(); s.LastError != "" {
		t.Errorf("LastError = %q; a dropped observation is not a failure", s.LastError)
	}
	ah.advance(2 * time.Hour)
	ah.tick(t)
	if all := ah.st.all(); all[0].ObservationCount != 2 {
		t.Errorf("ObservationCount = %d after a newer observation, want 2", all[0].ObservationCount)
	}
}

func TestAssurance_InvalidObservationIsLoggedAndSkipped(t *testing.T) {
	ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
	p := ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	id := ah.st.seedOpen(t, testAssuranceCluster, p.ID, veleroNamespace, "daily", "uid-daily", store.ConditionOverdue)
	// A pending intent that only the drain can finish: it proves the tick
	// got past the bad observation.
	ah.st.mu.Lock()
	ah.st.enqueueLocked(id, store.AssuranceTransitionResolved)
	ah.st.observeErr = store.ErrAssuranceObservationInvalid
	ah.st.mu.Unlock()

	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Fatalf("snapshot = %+v; an unstorable observation must not abort the tick", s)
	}
	for _, d := range ah.st.deliveryList() {
		if d.Transition == store.AssuranceTransitionResolved && d.State != store.AssuranceDeliveryDelivered {
			t.Errorf("delivery %+v; the drain must still run after a skipped observation", d)
		}
	}
}

// ---------------------------------------------------------------------------
// Drain errors
// ---------------------------------------------------------------------------

func TestAssurance_DrainMarkOutcomes(t *testing.T) {
	type tc struct {
		name        string
		setup       func(ah *assuranceHarness)
		wantErr     string
		wantState   string
		wantLastErr string
	}
	cases := []tc{
		{"not pending elsewhere", func(ah *assuranceHarness) { ah.st.markDeliveredErr = store.ErrAssuranceDeliveryNotPending }, "", store.AssuranceDeliveryPending, ""},
		{"mark outage", func(ah *assuranceHarness) { ah.st.markDeliveredErr = errors.New("pg: down") }, "deliver notifications", store.AssuranceDeliveryPending, ""},
		{"claim outage", func(ah *assuranceHarness) { ah.st.claimErr = errors.New("pg: down") }, "deliver notifications", store.AssuranceDeliveryPending, ""},
		{"emit failed with nil error", func(ah *assuranceHarness) { ah.emit.result = notifications.EmitFailed }, "", store.AssuranceDeliveryPending, "notification not delivered: failed"},
		{"emit timed out", func(ah *assuranceHarness) { ah.emit.err = context.DeadlineExceeded }, "", store.AssuranceDeliveryPending, "notification emit timed out"},
		{"emit cancelled", func(ah *assuranceHarness) { ah.emit.err = context.Canceled }, "", store.AssuranceDeliveryPending, "notification emit cancelled"},
		{"mark failed outage", func(ah *assuranceHarness) {
			ah.emit.err = errors.New("boom")
			ah.st.markFailedErr = errors.New("pg: down")
		}, "deliver notifications", store.AssuranceDeliveryPending, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ah := newAssuranceHarness(t, true, overdueCluster(time.Now())...)
			ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
			ah.st.mu.Lock()
			c.setup(ah)
			ah.st.mu.Unlock()
			ah.tick(t)
			if s := ah.svc.Snapshot(); s.LastError != c.wantErr {
				t.Errorf("LastError = %q, want %q", s.LastError, c.wantErr)
			}
			ds := ah.st.deliveryList()
			if len(ds) != 1 {
				t.Fatalf("deliveries = %+v, want 1", ds)
			}
			if ds[0].State != c.wantState || ds[0].LastError != c.wantLastErr {
				t.Errorf("delivery = %+v; want state %q last_error %q", ds[0], c.wantState, c.wantLastErr)
			}
			if all := ah.st.all(); len(all) != 1 || all[0].State != store.AssuranceStateOpen {
				t.Errorf("exceptions = %+v; delivery trouble never touches the condition", all)
			}
		})
	}
}

func TestAssurance_PruneErrorIsNotATickFailure(t *testing.T) {
	ah := newAssuranceHarness(t, true)
	ah.addPolicy(testSchedulePolicy(testAssuranceCluster, "daily"))
	ah.st.mu.Lock()
	ah.st.pruneErr = errors.New("pg: down")
	ah.st.mu.Unlock()
	ah.tick(t)
	if s := ah.svc.Snapshot(); s.LastError != "" || s.LastRunAt.IsZero() {
		t.Errorf("snapshot = %+v; prune is housekeeping and must not fail the tick", s)
	}
}

// ---------------------------------------------------------------------------
// Dedup identity
// ---------------------------------------------------------------------------

func TestAssurance_DedupIdentityIsTheExceptionNotTheSubject(t *testing.T) {
	svc := &AssuranceService{now: time.Now}
	subject := store.BackupAssuranceException{SubjectKind: store.ScopeSchedule, SubjectNamespace: "velero", SubjectName: "daily", SubjectUID: "uid-daily", Condition: store.ConditionOverdue, Severity: store.AssuranceSeverityWarning}
	first, second := subject, subject
	first.ID, second.ID = uuid.New(), uuid.New()
	job := func(e store.BackupAssuranceException) store.AssuranceDeliveryJob {
		return store.AssuranceDeliveryJob{Delivery: store.AssuranceDelivery{Transition: store.AssuranceTransitionOpened}, Exception: e}
	}
	a, b, again := svc.notificationFor(job(first)), svc.notificationFor(job(second)), svc.notificationFor(job(first))
	if a.ResourceUID == b.ResourceUID {
		t.Errorf("two exceptions for one subject share ResourceUID %q; a new episode would be deduped away", a.ResourceUID)
	}
	if a.ResourceUID != again.ResourceUID || a.Title != again.Title {
		t.Errorf("a re-send of the same intent changed identity: %q vs %q", a.ResourceUID, again.ResourceUID)
	}
	if a.ResourceUID != first.ID.String() {
		t.Errorf("ResourceUID = %q, want the exception id %s", a.ResourceUID, first.ID)
	}
	if a.ResourceName != "daily" || a.ResourceNS != "velero" {
		t.Errorf("resource fields = %+v; the subject still rides in ns/name for the RBAC-filtered feed", a)
	}
}
