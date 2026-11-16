package store

// rubrik_operations_test.go - durable Rubrik write records (KTD7).
//
// The validation tests run without a database: the store refuses that input
// before any SQL. The rest are env-gated (testDB, skipped without
// KUBECENTER_TEST_DATABASE_URL) and isolate by a cluster id from
// testOwnerID(t), so the in-flight index never sees another test's rows.

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// newRubrikOp returns a valid operation on object in cluster.
func newRubrikOp(cluster, object, kind string) RubrikOperation {
	return RubrikOperation{
		ID:          uuid.NewString(),
		Owner:       "user-1",
		ClusterID:   cluster,
		Kind:        kind,
		Namespace:   "team-a",
		RSCObjectID: object,
		RequestHash: "hash-1",
		Request:     json.RawMessage(`{"slaId":"gold"}`),
	}
}

func TestRubrikOperationFinal(t *testing.T) {
	for state, want := range map[string]bool{
		RubrikOpApplying: false, RubrikOpRunning: false, RubrikOpUnknown: false,
		RubrikOpSucceeded: true, RubrikOpFailed: true, RubrikOpPartial: true, "": false,
	} {
		if got := RubrikOperationFinal(state); got != want {
			t.Errorf("RubrikOperationFinal(%q) = %v, want %v", state, got, want)
		}
	}
}

// TestRubrikOperationValidation needs no database: every case is refused
// before the pool is touched, which the nil pool would otherwise panic on.
func TestRubrikOperationValidation(t *testing.T) {
	s := &RubrikStore{}
	ctx := t.Context()
	valid := newRubrikOp("cluster-1", uuid.NewString(), RubrikOpSnapshot)

	for name, mutate := range map[string]func(*RubrikOperation){
		"id not a uuid":       func(o *RubrikOperation) { o.ID = "op-1" },
		"nil uuid":            func(o *RubrikOperation) { o.ID = uuid.Nil.String() },
		"unknown kind":        func(o *RubrikOperation) { o.Kind = "delete" },
		"no owner":            func(o *RubrikOperation) { o.Owner = "" },
		"no cluster":          func(o *RubrikOperation) { o.ClusterID = "" },
		"no namespace":        func(o *RubrikOperation) { o.Namespace = "" },
		"no object":           func(o *RubrikOperation) { o.RSCObjectID = "" },
		"no request hash":     func(o *RubrikOperation) { o.RequestHash = "" },
		"oversized namespace": func(o *RubrikOperation) { o.Namespace = strings.Repeat("n", rubrikOpMaxText+1) },
		"request not json":    func(o *RubrikOperation) { o.Request = json.RawMessage(`{"a":`) },
		"source not json":     func(o *RubrikOperation) { o.Source = json.RawMessage(`nope`) },
		"oversized request": func(o *RubrikOperation) {
			o.Request = json.RawMessage(`"` + strings.Repeat("x", rubrikOpMaxJSONSize) + `"`)
		},
		"oversized source": func(o *RubrikOperation) {
			o.Source = json.RawMessage(`"` + strings.Repeat("x", rubrikOpMaxJSONSize) + `"`)
		},
	} {
		op := valid
		mutate(&op)
		if _, err := s.InsertOperation(ctx, op); !errors.Is(err, ErrRubrikOperationInvalid) {
			t.Errorf("insert with %s = %v, want ErrRubrikOperationInvalid", name, err)
		}
	}

	id := uuid.NewString()
	for name, c := range map[string]struct {
		id   string
		from []string
		u    RubrikOperationUpdate
	}{
		"id not a uuid":      {"op-1", []string{RubrikOpApplying}, RubrikOperationUpdate{State: RubrikOpRunning}},
		"no from state":      {id, nil, RubrikOperationUpdate{State: RubrikOpRunning}},
		"unknown from state": {id, []string{"done"}, RubrikOperationUpdate{State: RubrikOpRunning}},
		"back to applying":   {id, []string{RubrikOpUnknown}, RubrikOperationUpdate{State: RubrikOpApplying}},
		"unknown state":      {id, []string{RubrikOpApplying}, RubrikOperationUpdate{State: "done"}},
		"unknown job family": {id, []string{RubrikOpApplying}, RubrikOperationUpdate{State: RubrikOpRunning, JobFamily: "cron"}},
		"oversized handle": {id, []string{RubrikOpApplying}, RubrikOperationUpdate{
			State: RubrikOpRunning, JobFamily: RubrikJobTaskchain, JobHandle: strings.Repeat("h", rubrikOpMaxText+1)}},
		"oversized failure": {id, []string{RubrikOpApplying}, RubrikOperationUpdate{
			State: RubrikOpFailed, FailureMessage: strings.Repeat("f", rubrikOpMaxFailure+1)}},
	} {
		if _, err := s.UpdateOperation(ctx, c.id, c.from, c.u); !errors.Is(err, ErrRubrikOperationInvalid) {
			t.Errorf("update with %s = %v, want ErrRubrikOperationInvalid", name, err)
		}
	}
}

func TestRubrikOperationInsertAndGet(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)

	op := newRubrikOp(cluster, uuid.NewString(), RubrikOpSnapshot)
	op.State = RubrikOpSucceeded // ignored: a new row is always applying
	op.JobHandle = "ignored"
	got, err := s.InsertOperation(ctx, op)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got.ID != op.ID || got.Owner != op.Owner || got.ClusterID != cluster || got.Kind != RubrikOpSnapshot ||
		got.Namespace != "team-a" || got.RSCObjectID != op.RSCObjectID || got.RequestHash != "hash-1" {
		t.Errorf("inserted row = %+v; want the fields of %+v", got, op)
	}
	if got.State != RubrikOpApplying || got.JobFamily != "" || got.JobHandle != "" || got.FinishedAt != nil {
		t.Errorf("new row state = %q family %q handle %q finished %v; want applying with no job",
			got.State, got.JobFamily, got.JobHandle, got.FinishedAt)
	}
	var req map[string]string
	if err := json.Unmarshal(got.Request, &req); err != nil || req["slaId"] != "gold" {
		t.Errorf("request = %s (%v); want the stored document", got.Request, err)
	}
	if string(got.Source) != "{}" {
		t.Errorf("source = %s; want {} for an omitted source", got.Source)
	}

	read, err := s.GetOperation(ctx, strings.ToUpper(op.ID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.ID != op.ID || read.State != RubrikOpApplying {
		t.Errorf("get = %+v; want the inserted row", read)
	}
	if _, err := s.GetOperation(ctx, uuid.NewString()); !errors.Is(err, ErrRubrikOperationNotFound) {
		t.Errorf("get of an unknown id = %v, want ErrRubrikOperationNotFound", err)
	}
	if _, err := s.GetOperation(ctx, "not-a-uuid"); !errors.Is(err, ErrRubrikOperationNotFound) {
		t.Errorf("get of a malformed id = %v, want ErrRubrikOperationNotFound", err)
	}
}

func TestRubrikOperationIDReuse(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)

	first := newRubrikOp(cluster, uuid.NewString(), RubrikOpSnapshot)
	if _, err := s.InsertOperation(ctx, first); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// The same id is refused whatever the payload: the caller reads the
	// stored row to tell a replay from reuse.
	again := first
	again.Namespace = "team-b"
	again.RequestHash = "hash-2"
	if _, err := s.InsertOperation(ctx, again); !errors.Is(err, ErrRubrikOperationExists) {
		t.Errorf("insert of a used id = %v, want ErrRubrikOperationExists", err)
	}

	// A retry of an SLA change that is still open sees "exists", not "in
	// flight", although its own row is what blocks the object.
	sla := newRubrikOp(cluster, uuid.NewString(), RubrikOpSLAAssign)
	if _, err := s.InsertOperation(ctx, sla); err != nil {
		t.Fatalf("insert sla: %v", err)
	}
	if _, err := s.InsertOperation(ctx, sla); !errors.Is(err, ErrRubrikOperationExists) {
		t.Errorf("retry of an open sla change = %v, want ErrRubrikOperationExists", err)
	}

	stored, err := s.GetOperation(ctx, first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Namespace != "team-a" || stored.RequestHash != "hash-1" {
		t.Errorf("a refused insert changed the row: %+v", stored)
	}
}

func TestRubrikOperationInFlightRules(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()

	type step struct {
		kind    string
		blocked bool
	}
	for _, c := range []struct {
		name  string
		open  string
		state string // the open operation's state after insert
		next  []step
	}{
		{"open sla change", RubrikOpSLAAssign, RubrikOpApplying, []step{
			{RubrikOpSLAAssign, true}, {RubrikOpRestore, true}, {RubrikOpSnapshot, false}}},
		{"applying restore", RubrikOpRestore, RubrikOpApplying, []step{
			{RubrikOpSnapshot, true}, {RubrikOpSLAAssign, true}, {RubrikOpRestore, true}}},
		{"running restore", RubrikOpRestore, RubrikOpRunning, []step{
			{RubrikOpSLAAssign, true}, {RubrikOpRestore, true}, {RubrikOpSnapshot, true}}},
		{"unknown sla change", RubrikOpSLAAssign, RubrikOpUnknown, []step{
			{RubrikOpSLAAssign, true}, {RubrikOpRestore, true}}},
		{"unknown restore", RubrikOpRestore, RubrikOpUnknown, []step{{RubrikOpSnapshot, true}}},
		{"running snapshot", RubrikOpSnapshot, RubrikOpRunning, []step{
			{RubrikOpSnapshot, false}, {RubrikOpSLAAssign, false}, {RubrikOpRestore, false}}},
		{"finished restore", RubrikOpRestore, RubrikOpFailed, []step{
			{RubrikOpSnapshot, false}, {RubrikOpRestore, false}}},
		{"partial sla change", RubrikOpSLAAssign, RubrikOpPartial, []step{{RubrikOpSLAAssign, false}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cluster := testOwnerID(t)
			object := uuid.NewString()
			open, err := s.InsertOperation(ctx, newRubrikOp(cluster, object, c.open))
			if err != nil {
				t.Fatalf("insert open %s: %v", c.open, err)
			}
			if c.state != RubrikOpApplying {
				if _, err := s.UpdateOperation(ctx, open.ID, []string{RubrikOpApplying},
					RubrikOperationUpdate{State: c.state}); err != nil {
					t.Fatalf("moving the open operation to %s: %v", c.state, err)
				}
			}
			for _, n := range c.next {
				added, err := s.InsertOperation(ctx, newRubrikOp(cluster, object, n.kind))
				switch {
				case n.blocked && !errors.Is(err, ErrRubrikOperationInFlight):
					t.Errorf("%s after a %s %s = %v, want ErrRubrikOperationInFlight", n.kind, c.state, c.open, err)
				case !n.blocked && err != nil:
					t.Errorf("%s after a %s %s = %v, want it allowed", n.kind, c.state, c.open, err)
				}
				// Settle an allowed operation, so the next step is judged
				// against the open operation alone.
				if err == nil {
					if _, err := s.UpdateOperation(ctx, added.ID, []string{RubrikOpApplying},
						RubrikOperationUpdate{State: RubrikOpFailed}); err != nil {
						t.Fatalf("settling the allowed %s: %v", n.kind, err)
					}
				}
			}

			// The same object on another cluster, and another object on
			// this cluster, are never blocked.
			if _, err := s.InsertOperation(ctx, newRubrikOp(testOwnerID(t), object, c.open)); err != nil {
				t.Errorf("same object, other cluster = %v, want it allowed", err)
			}
			if _, err := s.InsertOperation(ctx, newRubrikOp(cluster, uuid.NewString(), c.open)); err != nil {
				t.Errorf("other object, same cluster = %v, want it allowed", err)
			}
		})
	}
}

// TestRubrikOperationInFlightRace: concurrent SLA changes on one object
// leave exactly one row, and every other request is told "in flight", never
// a store error. The partial unique index is what holds this across
// replicas; the NOT EXISTS alone could not.
func TestRubrikOperationInFlightRace(t *testing.T) {
	s := NewRubrikStore(testDBWithMaxConns(t, 16), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)
	object := uuid.NewString()

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = s.InsertOperation(ctx, newRubrikOp(cluster, object, RubrikOpSLAAssign))
		})
	}
	close(start)
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrRubrikOperationInFlight):
			t.Errorf("request %d = %v, want success or ErrRubrikOperationInFlight", i, err)
		}
	}
	if won != 1 {
		t.Errorf("%d concurrent sla changes were recorded, want exactly 1", won)
	}
}

// TestRubrikOperationSameIDRace: concurrent sends of one SLA change (a
// client retrying while its first request is still open) leave one row,
// and every other send is told the id exists, never that it is in flight,
// so the handler answers it from the row. Each loser collides on both the
// primary key and the in-flight index, whichever PostgreSQL reports.
func TestRubrikOperationSameIDRace(t *testing.T) {
	s := NewRubrikStore(testDBWithMaxConns(t, 16), rubrikTestKey)
	ctx := t.Context()
	op := newRubrikOp(testOwnerID(t), uuid.NewString(), RubrikOpSLAAssign)

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = s.InsertOperation(ctx, op)
		})
	}
	close(start)
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrRubrikOperationExists):
			t.Errorf("send %d = %v, want success or ErrRubrikOperationExists", i, err)
		}
	}
	if won != 1 {
		t.Errorf("%d concurrent sends of one id were recorded, want exactly 1", won)
	}
}

func TestRubrikOperationUpdate(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)

	op, err := s.InsertOperation(ctx, newRubrikOp(cluster, uuid.NewString(), RubrikOpSnapshot))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	running, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpApplying}, RubrikOperationUpdate{
		State: RubrikOpRunning, JobFamily: RubrikJobTaskchain, JobHandle: "tc-1",
	})
	if err != nil {
		t.Fatalf("applying -> running: %v", err)
	}
	if running.State != RubrikOpRunning || running.JobFamily != RubrikJobTaskchain || running.JobHandle != "tc-1" ||
		running.FinishedAt != nil {
		t.Errorf("running row = %+v; want running on taskchain tc-1, not finished", running)
	}
	if !running.UpdatedAt.After(op.UpdatedAt) {
		t.Errorf("updated_at did not advance: %v -> %v", op.UpdatedAt, running.UpdatedAt)
	}

	// A second writer that still expects applying loses and is shown the
	// row as it is.
	current, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpApplying}, RubrikOperationUpdate{State: RubrikOpUnknown})
	if !errors.Is(err, ErrRubrikOperationStateChanged) {
		t.Fatalf("stale update = %v, want ErrRubrikOperationStateChanged", err)
	}
	if current.State != RubrikOpRunning || current.JobHandle != "tc-1" {
		t.Errorf("stale update returned %+v; want the running row", current)
	}

	// A refresh that learns nothing about the job keeps its handle.
	partial, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpRunning, RubrikOpUnknown}, RubrikOperationUpdate{
		State: RubrikOpPartial, FailureMessage: "2 of 3 resources",
	})
	if err != nil {
		t.Fatalf("running -> partial: %v", err)
	}
	if partial.JobFamily != RubrikJobTaskchain || partial.JobHandle != "tc-1" {
		t.Errorf("partial row lost its job: family %q handle %q", partial.JobFamily, partial.JobHandle)
	}
	if partial.FinishedAt == nil || partial.FailureMessage != "2 of 3 resources" {
		t.Errorf("partial row = %+v; want finished with the failure message", partial)
	}

	// Leaving a final state clears the finish time.
	reopened, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpPartial}, RubrikOperationUpdate{State: RubrikOpUnknown})
	if err != nil {
		t.Fatalf("partial -> unknown: %v", err)
	}
	if reopened.FinishedAt != nil {
		t.Errorf("a non-final row kept finished_at %v", reopened.FinishedAt)
	}

	if _, err := s.UpdateOperation(ctx, uuid.NewString(), []string{RubrikOpApplying},
		RubrikOperationUpdate{State: RubrikOpFailed}); !errors.Is(err, ErrRubrikOperationNotFound) {
		t.Errorf("update of an unknown id = %v, want ErrRubrikOperationNotFound", err)
	}
}

// TestRubrikOperationUnknownSettles: an unknown outcome blocks its object,
// and a refresh that settles it, from unknown, frees the object and stamps
// the finish once.
func TestRubrikOperationUnknownSettles(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)
	object := uuid.NewString()

	op, err := s.InsertOperation(ctx, newRubrikOp(cluster, object, RubrikOpSLAAssign))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	unknown, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpApplying}, RubrikOperationUpdate{
		State: RubrikOpUnknown, FailureMessage: "unreachable",
	})
	if err != nil {
		t.Fatalf("applying -> unknown: %v", err)
	}
	if unknown.FinishedAt != nil {
		t.Errorf("unknown is not final, but finished_at = %v", unknown.FinishedAt)
	}
	if _, err := s.InsertOperation(ctx, newRubrikOp(cluster, object, RubrikOpSLAAssign)); !errors.Is(err, ErrRubrikOperationInFlight) {
		t.Fatalf("sla change beside an unknown one = %v, want ErrRubrikOperationInFlight", err)
	}

	done, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpUnknown}, RubrikOperationUpdate{State: RubrikOpSucceeded})
	if err != nil {
		t.Fatalf("unknown -> succeeded: %v", err)
	}
	if done.FinishedAt == nil || done.FailureMessage != "" {
		t.Errorf("settled row = %+v; want finished with the message cleared", done)
	}
	// A repeated final write keeps the first finish time.
	again, err := s.UpdateOperation(ctx, op.ID, []string{RubrikOpSucceeded}, RubrikOperationUpdate{State: RubrikOpSucceeded})
	if err != nil {
		t.Fatalf("succeeded -> succeeded: %v", err)
	}
	if again.FinishedAt == nil || !again.FinishedAt.Equal(*done.FinishedAt) {
		t.Errorf("finished_at moved from %v to %v", done.FinishedAt, again.FinishedAt)
	}
	if _, err := s.InsertOperation(ctx, newRubrikOp(cluster, object, RubrikOpSLAAssign)); err != nil {
		t.Errorf("sla change after the unknown one settled = %v, want it allowed", err)
	}
}
