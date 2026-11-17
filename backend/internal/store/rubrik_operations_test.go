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
	"slices"
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

			// The same object on another cluster, and another object in
			// another namespace of this cluster, are never blocked.
			if _, err := s.InsertOperation(ctx, newRubrikOp(testOwnerID(t), object, c.open)); err != nil {
				t.Errorf("same object, other cluster = %v, want it allowed", err)
			}
			other := newRubrikOp(cluster, uuid.NewString(), c.open)
			other.Namespace = "team-z"
			if _, err := s.InsertOperation(ctx, other); err != nil {
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

// TestRubrikOpNamespaces needs no database: the namespaces an operation
// writes into are its own and, for a restore only, its source's "into".
func TestRubrikOpNamespaces(t *testing.T) {
	restore := newRubrikOp("c", "o", RubrikOpRestore)
	restore.Namespace = "team-b"
	restore.Source = json.RawMessage(`{"objectId":"o","into":["team-c","team-b","team-a"]}`)
	snapshot := restore
	snapshot.Kind = RubrikOpSnapshot
	unreadable := restore
	unreadable.Source = json.RawMessage(`{"into":"team-c"}`)
	setSnapshot := snapshot
	setSnapshot.AlsoWritesInto = []string{"team-d", "team-b"}
	for name, c := range map[string]struct {
		op   RubrikOperation
		want []string
	}{
		"restore":                   {restore, []string{"team-a", "team-b", "team-c"}},
		"snapshot ignores a source": {snapshot, []string{"team-b"}},
		"unreadable into":           {unreadable, []string{"team-b"}},
		"set snapshot":              {setSnapshot, []string{"team-b", "team-d"}},
	} {
		if got := rubrikOpNamespaces(c.op); !slices.Equal(got, c.want) {
			t.Errorf("%s: namespaces = %v, want %v", name, got, c.want)
		}
	}
}

// An open restore blocks every write into a namespace it restores into,
// whichever object that write acts on: its target namespace, and every
// namespace a protection-set restore writes into ("into"). A new restore is
// blocked when any of its own namespaces is one of them. A finished restore,
// another namespace, another cluster, and an open write of another kind
// block nothing across objects.
func TestRubrikOperationRestoreBlocksItsNamespaces(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)

	op := func(kind, namespace string, into ...string) RubrikOperation {
		o := newRubrikOp(cluster, uuid.NewString(), kind)
		o.Namespace = namespace
		if len(into) > 0 {
			raw, _ := json.Marshal(map[string]any{"into": into})
			o.Source = raw
		}
		return o
	}
	settle := func(t *testing.T, id string) {
		t.Helper()
		if _, err := s.UpdateOperation(ctx, id, []string{RubrikOpApplying, RubrikOpRunning},
			RubrikOperationUpdate{State: RubrikOpFailed}); err != nil {
			t.Fatalf("settling %s: %v", id, err)
		}
	}

	open, err := s.InsertOperation(ctx, op(RubrikOpRestore, "team-a", "team-a", "team-b"))
	if err != nil {
		t.Fatalf("insert the open restore: %v", err)
	}
	if _, err := s.UpdateOperation(ctx, open.ID, []string{RubrikOpApplying},
		RubrikOperationUpdate{State: RubrikOpRunning, JobFamily: RubrikJobAsyncRequest, JobHandle: "req-1"}); err != nil {
		t.Fatalf("moving the restore to running: %v", err)
	}

	for name, c := range map[string]struct {
		op      RubrikOperation
		blocked bool
	}{
		"snapshot of another object in the target":   {op(RubrikOpSnapshot, "team-a"), true},
		"sla change in a namespace the set restores": {op(RubrikOpSLAAssign, "team-b"), true},
		"restore into the target":                    {op(RubrikOpRestore, "team-a", "team-a"), true},
		"set restore overlapping one namespace":      {op(RubrikOpRestore, "team-c", "team-b", "team-c"), true},
		"snapshot in another namespace":              {op(RubrikOpSnapshot, "team-c"), false},
		"restore into other namespaces":              {op(RubrikOpRestore, "team-c", "team-c", "team-d"), false},
		"source naming a blocked namespace, no restore": {func() RubrikOperation {
			o := op(RubrikOpSnapshot, "team-c")
			o.Source = json.RawMessage(`{"into":["team-a"]}`)
			return o
		}(), false},
		"set snapshot also writing into the target": {func() RubrikOperation {
			o := op(RubrikOpSnapshot, "team-c")
			o.AlsoWritesInto = []string{"team-c", "team-b"}
			return o
		}(), true},
		"set change writing into every namespace": {func() RubrikOperation {
			o := op(RubrikOpSLAAssign, "team-z")
			o.WritesIntoAll = true
			return o
		}(), true},
	} {
		t.Run(name, func(t *testing.T) {
			// BlockingOperations names the open restore exactly when the
			// insert is refused, by the same predicate.
			blockers, err := s.BlockingOperations(ctx, c.op)
			if err != nil {
				t.Fatalf("blocking operations: %v", err)
			}
			if named := len(blockers) == 1 && blockers[0].ID == open.ID; named != c.blocked || len(blockers) > 1 {
				t.Errorf("blocking operations = %d rows, want the open restore only when blocked (%v)", len(blockers), c.blocked)
			}
			added, err := s.InsertOperation(ctx, c.op)
			switch {
			case c.blocked && !errors.Is(err, ErrRubrikOperationInFlight):
				t.Fatalf("insert = %v, want ErrRubrikOperationInFlight", err)
			case !c.blocked && err != nil:
				t.Fatalf("insert = %v, want it allowed", err)
			}
			if err == nil {
				settle(t, added.ID)
			}
		})
	}

	other := newRubrikOp(testOwnerID(t), uuid.NewString(), RubrikOpSnapshot)
	if _, err := s.InsertOperation(ctx, other); err != nil {
		t.Errorf("the same namespace on another cluster = %v, want it allowed", err)
	}

	// An open snapshot or SLA change blocks only its own object.
	snap, err := s.InsertOperation(ctx, op(RubrikOpSnapshot, "team-e"))
	if err != nil {
		t.Fatalf("insert an open snapshot: %v", err)
	}
	if _, err := s.InsertOperation(ctx, op(RubrikOpRestore, "team-e", "team-e")); err != nil {
		t.Errorf("restore beside an open snapshot of another object = %v, want it allowed", err)
	}
	settle(t, snap.ID)

	// Once the restore has finished, its namespaces are free again.
	settle(t, open.ID)
	if _, err := s.InsertOperation(ctx, op(RubrikOpSnapshot, "team-a")); err != nil {
		t.Errorf("snapshot after the restore finished = %v, want it allowed", err)
	}
}

// BlockingOperations lists every open blocker of a new operation, oldest
// first, and nothing final, of another cluster, or unrelated; the
// insert-only namespace fields are never stored.
func TestRubrikBlockingOperations(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)
	object := uuid.NewString()

	insert := func(o RubrikOperation) RubrikOperation {
		t.Helper()
		got, err := s.InsertOperation(ctx, o)
		if err != nil {
			t.Fatalf("insert %s: %v", o.Kind, err)
		}
		return got
	}
	move := func(id, state string) {
		t.Helper()
		if _, err := s.UpdateOperation(ctx, id, []string{RubrikOpApplying}, RubrikOperationUpdate{State: state}); err != nil {
			t.Fatalf("moving %s to %s: %v", id, state, err)
		}
	}

	// The blockers, oldest first: an SLA change of the object, then a
	// restore of another object into team-a. A finished restore into
	// team-a, an open snapshot of the object and an open restore into
	// team-q block nothing here.
	done := newRubrikOp(cluster, uuid.NewString(), RubrikOpRestore)
	move(insert(done).ID, RubrikOpSucceeded)
	sla := insert(newRubrikOp(cluster, object, RubrikOpSLAAssign))
	move(sla.ID, RubrikOpUnknown)
	into := newRubrikOp(cluster, uuid.NewString(), RubrikOpRestore)
	into.Source = json.RawMessage(`{"into":["team-a"]}`)
	restore := insert(into)
	move(restore.ID, RubrikOpRunning)
	snapshot := newRubrikOp(cluster, uuid.NewString(), RubrikOpSnapshot)
	snapshot.RSCObjectID = object
	snapshot.Namespace = "team-q"
	insert(snapshot)
	elsewhere := newRubrikOp(cluster, uuid.NewString(), RubrikOpRestore)
	elsewhere.Namespace = "team-q"
	insert(elsewhere)

	next := newRubrikOp(cluster, object, RubrikOpSLAAssign)
	next.AlsoWritesInto = []string{"team-c"}
	got, err := s.BlockingOperations(ctx, next)
	if err != nil {
		t.Fatalf("blocking operations: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, o := range got {
		ids = append(ids, o.ID)
		if o.AlsoWritesInto != nil || o.WritesIntoAll {
			t.Errorf("row %s carries insert-only fields", o.ID)
		}
	}
	if want := []string{sla.ID, restore.ID}; !slices.Equal(ids, want) {
		t.Errorf("blockers = %v, want %v (oldest first)", ids, want)
	}

	other := next
	other.ClusterID = testOwnerID(t)
	if got, err := s.BlockingOperations(ctx, other); err != nil || len(got) != 0 {
		t.Errorf("another cluster = %d rows, %v; want none", len(got), err)
	}
}

// TestRubrikOperationBlocks needs no database: the in-flight rule in Go, cell
// by cell. TestRubrikOperationBlocksMatchesTheStore holds it to the SQL.
func TestRubrikOperationBlocks(t *testing.T) {
	open := func(kind, state string) RubrikOperation {
		o := newRubrikOp("c", "obj", kind)
		o.State = state
		return o
	}
	next := func(kind, object, namespace string) RubrikOperation {
		o := newRubrikOp("c", object, kind)
		o.Namespace = namespace
		return o
	}
	restoreInto := open(RubrikOpRestore, RubrikOpRunning)
	restoreInto.Namespace = "team-a"
	restoreInto.Source = json.RawMessage(`{"into":["team-a","team-b"]}`)
	stringInto := restoreInto
	stringInto.Source = json.RawMessage(`{"into":"team-c"}`)
	wide := next(RubrikOpSnapshot, "other", "team-z")
	wide.AlsoWritesInto = []string{"team-z", "team-b"}
	all := next(RubrikOpSLAAssign, "other", "team-z")
	all.WritesIntoAll = true
	otherCluster := next(RubrikOpSLAAssign, "obj", "team-z")
	otherCluster.ClusterID = "c2"

	for name, c := range map[string]struct {
		open, next RubrikOperation
		want       bool
	}{
		"sla blocks sla":               {open(RubrikOpSLAAssign, RubrikOpApplying), next(RubrikOpSLAAssign, "obj", "team-z"), true},
		"sla blocks restore":           {open(RubrikOpSLAAssign, RubrikOpUnknown), next(RubrikOpRestore, "obj", "team-z"), true},
		"sla allows snapshot":          {open(RubrikOpSLAAssign, RubrikOpRunning), next(RubrikOpSnapshot, "obj", "team-z"), false},
		"restore blocks snapshot":      {open(RubrikOpRestore, RubrikOpUnknown), next(RubrikOpSnapshot, "obj", "team-z"), true},
		"snapshot blocks nothing":      {open(RubrikOpSnapshot, RubrikOpRunning), next(RubrikOpRestore, "obj", "team-a"), false},
		"finished blocks nothing":      {open(RubrikOpRestore, RubrikOpPartial), next(RubrikOpSLAAssign, "obj", "team-a"), false},
		"other cluster":                {open(RubrikOpRestore, RubrikOpRunning), otherCluster, false},
		"restore into the namespace":   {restoreInto, next(RubrikOpSnapshot, "other", "team-b"), true},
		"restore elsewhere":            {restoreInto, next(RubrikOpSnapshot, "other", "team-z"), false},
		"set write over the namespace": {restoreInto, wide, true},
		"write into every namespace":   {restoreInto, all, true},
		"into as a string":             {stringInto, next(RubrikOpSnapshot, "other", "team-c"), false},
	} {
		if got := RubrikOperationBlocks(c.open, c.next); got != c.want {
			t.Errorf("%s: blocks = %v, want %v", name, got, c.want)
		}
	}
}

// seedRubrikOp writes a row as given, past the in-flight rules, so a test
// can lay out any mix of open and finished operations.
func seedRubrikOp(t *testing.T, s *RubrikStore, cluster, kind, namespace, object, state, source string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := s.pool.Exec(t.Context(), `
		INSERT INTO rubrik_operations (id, owner, cluster_id, kind, namespace, rsc_object_id, request_hash, source, state)
		VALUES ($1, 'user-1', $2, $3, $4, $5, 'hash-1', $6::jsonb, $7)`,
		id, cluster, kind, namespace, object, source, state); err != nil {
		t.Fatalf("seeding a %s %s: %v", state, kind, err)
	}
	return id
}

// TestRubrikOperationBlocksMatchesTheStore: for every write in a grid of
// kinds, objects, namespaces and coverage, the rows BlockingOperations
// returns are exactly those RubrikOperationBlocks picks out of every row on
// the clusters.
func TestRubrikOperationBlocksMatchesTheStore(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster, elsewhere := testOwnerID(t), testOwnerID(t)
	objects := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	namespaces := []string{"team-a", "team-b", "team-c", "team-g", "team-z"}

	seedRubrikOp(t, s, cluster, RubrikOpRestore, "team-a", objects[0], RubrikOpRunning, `{"into":["team-a","team-b"]}`)
	seedRubrikOp(t, s, cluster, RubrikOpSLAAssign, "team-c", objects[1], RubrikOpUnknown, `{}`)
	seedRubrikOp(t, s, cluster, RubrikOpRestore, "team-d", objects[2], RubrikOpFailed, `{"into":["team-d"]}`)
	seedRubrikOp(t, s, cluster, RubrikOpSnapshot, "team-e", objects[0], RubrikOpApplying, `{}`)
	seedRubrikOp(t, s, cluster, RubrikOpRestore, "team-f", objects[3], RubrikOpApplying, `{"into":"team-g"}`)
	seedRubrikOp(t, s, elsewhere, RubrikOpRestore, "team-a", objects[4], RubrikOpRunning, `{"into":["team-a"]}`)

	rows, err := s.pool.Query(ctx, `SELECT `+rubrikOperationColumns+` FROM rubrik_operations WHERE cluster_id = ANY($1)`,
		[]string{cluster, elsewhere})
	if err != nil {
		t.Fatalf("reading the seeded rows: %v", err)
	}
	every, err := collectRubrikOperations(rows)
	if err != nil || len(every) != 6 {
		t.Fatalf("seeded rows = %d, %v; want 6", len(every), err)
	}

	variants := map[string]func(*RubrikOperation){
		"plain":         func(*RubrikOperation) {},
		"set coverage":  func(o *RubrikOperation) { o.AlsoWritesInto = []string{"team-b", "team-y"} },
		"all":           func(o *RubrikOperation) { o.WritesIntoAll = true },
		"restore into":  func(o *RubrikOperation) { o.Source = json.RawMessage(`{"into":["team-f","team-x"]}`) },
		"string source": func(o *RubrikOperation) { o.Source = json.RawMessage(`{"into":"team-a"}`) },
	}
	for _, kind := range rubrikOpKinds {
		for i, object := range objects {
			for _, ns := range namespaces {
				for vname, vary := range variants {
					next := newRubrikOp(cluster, object, kind)
					next.Namespace = ns
					vary(&next)
					var want []string
					for _, o := range every {
						if RubrikOperationBlocks(o, next) {
							want = append(want, o.ID)
						}
					}
					blockers, err := s.BlockingOperations(ctx, next)
					if err != nil {
						t.Fatalf("blocking operations: %v", err)
					}
					got := make([]string, 0, len(blockers))
					for _, o := range blockers {
						got = append(got, o.ID)
					}
					slices.Sort(want)
					slices.Sort(got)
					if !slices.Equal(got, want) {
						t.Errorf("%s of object %d in %s (%s): store blocks %v, Go rule %v", kind, i, ns, vname, got, want)
					}
				}
			}
		}
	}
}

// OpenOperations lists a cluster's open operations, oldest first, and says
// when there are more than it returns.
func TestRubrikOpenOperations(t *testing.T) {
	s := NewRubrikStore(testDB(t), rubrikTestKey)
	ctx := t.Context()
	cluster := testOwnerID(t)

	first := seedRubrikOp(t, s, cluster, RubrikOpRestore, "team-a", uuid.NewString(), RubrikOpUnknown, `{}`)
	seedRubrikOp(t, s, cluster, RubrikOpSnapshot, "team-a", uuid.NewString(), RubrikOpSucceeded, `{}`)
	second := seedRubrikOp(t, s, cluster, RubrikOpSnapshot, "team-b", uuid.NewString(), RubrikOpApplying, `{}`)
	third := seedRubrikOp(t, s, cluster, RubrikOpSLAAssign, "team-c", uuid.NewString(), RubrikOpRunning, `{}`)
	seedRubrikOp(t, s, testOwnerID(t), RubrikOpRestore, "team-a", uuid.NewString(), RubrikOpRunning, `{}`)

	got, err := s.OpenOperations(ctx, cluster)
	if err != nil {
		t.Fatalf("open operations: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, o := range got {
		ids = append(ids, o.ID)
	}
	if want := []string{first, second, third}; !slices.Equal(ids, want) {
		t.Errorf("open operations = %v, want %v (oldest first)", ids, want)
	}

	// Past the cap, the oldest rows come back with the truncation error.
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO rubrik_operations (id, owner, cluster_id, kind, namespace, rsc_object_id, request_hash, state)
		SELECT gen_random_uuid(), 'user-1', $1, 'snapshot', 'team-d', gen_random_uuid()::text, 'hash-1', 'running'
		FROM generate_series(1, $2::int)`, cluster, maxOpenOperations); err != nil {
		t.Fatalf("seeding past the cap: %v", err)
	}
	got, err = s.OpenOperations(ctx, cluster)
	if !errors.Is(err, ErrRubrikOperationsTruncated) || len(got) != maxOpenOperations || got[0].ID != first {
		t.Errorf("past the cap = %d rows, %v; want the oldest %d, first %s, with ErrRubrikOperationsTruncated",
			len(got), err, maxOpenOperations, first)
	}
}
