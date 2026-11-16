package rubrik

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	testOpID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherOp  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	idSLA2   = "99999999-9999-4999-8999-999999999999"
)

// Writers carry a user id: operations are owned by it.
var (
	alice = &auth.User{ID: "u-alice", Username: "alice", KubernetesUsername: "alice", Roles: []string{"viewer"}}
	bob   = &auth.User{ID: "u-bob", Username: "bob", KubernetesUsername: "bob", Roles: []string{"viewer"}}
	root  = &auth.User{ID: "u-root", Username: "root", KubernetesUsername: "root", Roles: []string{"admin"}}
)

// fakeOps is an in-memory OperationStore with the store's rules: an id is
// recorded once, and the in-flight matrix of store.InsertOperation. Its
// hooks inject store failures and races, and it records whether the outcome
// write ran on a cancelled context.
type fakeOps struct {
	mu  sync.Mutex
	ops map[string]store.RubrikOperation

	insertErr, getErr, updateErr error
	// raceRow is stored just before an insert, as a concurrent request
	// with the same id would.
	raceRow *store.RubrikOperation
	// afterInsert runs once a row was inserted, before the mutation.
	afterInsert  func()
	updateCtxErr error
}

func newFakeOps() *fakeOps { return &fakeOps{ops: map[string]store.RubrikOperation{}} }

func openState(state string) bool {
	return state == store.RubrikOpApplying || state == store.RubrikOpRunning || state == store.RubrikOpUnknown
}

func (f *fakeOps) InsertOperation(_ context.Context, op store.RubrikOperation) (store.RubrikOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return store.RubrikOperation{}, f.insertErr
	}
	if f.raceRow != nil {
		f.ops[f.raceRow.ID], f.raceRow = *f.raceRow, nil
	}
	id := strings.ToLower(op.ID)
	if _, ok := f.ops[id]; ok {
		return store.RubrikOperation{}, store.ErrRubrikOperationExists
	}
	for _, x := range f.ops {
		if x.ClusterID != op.ClusterID || x.RSCObjectID != op.RSCObjectID || !openState(x.State) {
			continue
		}
		if x.Kind == store.RubrikOpRestore || (op.Kind != store.RubrikOpSnapshot && x.Kind == store.RubrikOpSLAAssign) {
			return store.RubrikOperation{}, store.ErrRubrikOperationInFlight
		}
	}
	now := time.Now()
	op.ID, op.State, op.CreatedAt, op.UpdatedAt = id, store.RubrikOpApplying, now, now
	f.ops[id] = op
	if f.afterInsert != nil {
		f.afterInsert()
	}
	return op, nil
}

func (f *fakeOps) GetOperation(_ context.Context, id string) (store.RubrikOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.RubrikOperation{}, f.getErr
	}
	op, ok := f.ops[strings.ToLower(id)]
	if !ok {
		return store.RubrikOperation{}, store.ErrRubrikOperationNotFound
	}
	return op, nil
}

func (f *fakeOps) UpdateOperation(ctx context.Context, id string, from []string, u store.RubrikOperationUpdate) (store.RubrikOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCtxErr = ctx.Err()
	if f.updateErr != nil {
		return store.RubrikOperation{}, f.updateErr
	}
	op, ok := f.ops[strings.ToLower(id)]
	if !ok {
		return store.RubrikOperation{}, store.ErrRubrikOperationNotFound
	}
	if !slices.Contains(from, op.State) {
		return op, store.ErrRubrikOperationStateChanged
	}
	op.State = u.State
	if u.JobFamily != "" {
		op.JobFamily = u.JobFamily
	}
	if u.JobHandle != "" {
		op.JobHandle = u.JobHandle
	}
	op.FailureMessage = u.FailureMessage
	op.UpdatedAt = time.Now()
	if store.RubrikOperationFinal(u.State) {
		now := time.Now()
		op.FinishedAt = &now
	} else {
		op.FinishedAt = nil
	}
	f.ops[op.ID] = op
	return op, nil
}

func (f *fakeOps) seed(op store.RubrikOperation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops[op.ID] = op
}

func (f *fakeOps) get(t *testing.T, id string) store.RubrikOperation {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[strings.ToLower(id)]
	if !ok {
		t.Fatalf("operation %s not recorded", id)
	}
	return op
}

func (f *fakeOps) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ops)
}

// fakeWriter is an RSCWriter that records the mutations it receives.
type fakeWriter struct {
	mu          sync.Mutex
	snapshots   []string // "family object sla"
	assigns     []string // "objects sla"
	handle      JobHandle
	snapshotErr error
	assignErr   error
	taskchains  map[string]TaskchainState
	jobs        []Job
	jobsErr     error
	// ctxErr is the mutation context's error when the mutation ran.
	ctxErr error
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{handle: JobHandle{Family: FamilyNamespace, TaskchainID: "tc-1", JobID: 7}, taskchains: map[string]TaskchainState{}}
}

func (f *fakeWriter) CreateSnapshot(ctx context.Context, family Family, objectID, slaID string) (JobHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctxErr = ctx.Err()
	f.snapshots = append(f.snapshots, string(family)+" "+objectID+" "+slaID)
	return f.handle, f.snapshotErr
}

func (f *fakeWriter) AssignSLA(ctx context.Context, objectIDs []string, slaID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctxErr = ctx.Err()
	f.assigns = append(f.assigns, strings.Join(objectIDs, ",")+" "+slaID)
	return f.assignErr
}

func (f *fakeWriter) Taskchain(_ context.Context, id string) (TaskchainState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.taskchains[id]
	if !ok {
		return TaskchainState{Status: JobRunning}, nil
	}
	return st, nil
}

func (f *fakeWriter) Jobs(context.Context, ActivityFilter) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.jobs), f.jobsErr
}

func (f *fakeWriter) sent() (snapshots, assigns []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.snapshots), slices.Clone(f.assigns)
}

func (f *fakeWriter) mutations() int {
	s, a := f.sent()
	return len(s) + len(a)
}

// writeFixture is the read fixture with writes wired to in-memory fakes and
// two SLA domains the service account can see.
func writeFixture(t *testing.T, namespaces map[string]time.Time, writes bool, grants ...string) (*handlerFixture, *fakeOps, *fakeWriter, *fakeChecker) {
	t.Helper()
	fx, checker := readFixture(t, namespaces, writes, grants...)
	ops, w := newFakeOps(), newFakeWriter()
	fx.h.ops, fx.h.writer = ops, w
	fx.rsc.set(func(f *fakeReader) {
		f.slas = []SLADomain{{ID: idSLA, Name: "gold"}, {ID: idSLA2, Name: "silver"}}
	})
	return fx, ops, w, checker
}

func teamA() map[string]time.Time { return map[string]time.Time{"team-a": created} }

func snapshotBody(op string) map[string]any { return map[string]any{"operationId": op} }

func slaBody(op, objectID, sla string) map[string]any {
	b := map[string]any{"operationId": op, "slaId": sla}
	if objectID != "" {
		b["objectId"] = objectID
	}
	return b
}

func useObjects(fx *handlerFixture, objects ...Object) {
	fx.rsc.set(func(f *fakeReader) { f.objects[idCluster] = objects })
}

func entriesFor(fx *handlerFixture, action audit.Action) []audit.Entry {
	var out []audit.Entry
	for _, e := range fx.audit.all() {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------

// Covers AE8: with the writes setting off, both writes refuse with a reason,
// for admins too, and RSC receives nothing at all.
func TestWritesOffRefuseBeforeAnything(t *testing.T) {
	fx, ops, w, _ := writeFixture(t, teamA(), false,
		"get protections team-a", "create snapshots team-a", "update slaassignments team-a")
	for _, u := range []*auth.User{alice, root} {
		rec := fx.do(t, u, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID))
		wantStatus(t, rec, http.StatusForbidden)
		if got := errorReason(t, rec); got != "writes_disabled" {
			t.Errorf("snapshot reason = %q, want writes_disabled", got)
		}
		rec = fx.do(t, u, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(otherOp, "", idSLA2))
		wantStatus(t, rec, http.StatusForbidden)
		if got := errorReason(t, rec); got != "writes_disabled" {
			t.Errorf("sla reason = %q, want writes_disabled", got)
		}
	}
	if n := rscCalls(fx); n != 0 || w.mutations() != 0 || ops.count() != 0 {
		t.Errorf("a refused write reached RSC (%d reads, %d mutations) or the store (%d rows)", n, w.mutations(), ops.count())
	}
	for _, e := range fx.audit.all() {
		if e.Result != audit.ResultDenied || !strings.Contains(e.Detail, "writes_disabled") {
			t.Errorf("audit entry %+v; want a denial naming writes_disabled", e)
		}
	}
	if n := len(fx.audit.all()); n != 4 {
		t.Errorf("%d audit entries, want one per refused request (4)", n)
	}
}

// A user without the snapshot grant is refused with 403, the denial is
// audited, and no operation is recorded.
func TestSnapshotWithoutGrantIsDeniedAndAudited(t *testing.T) {
	fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID))
	wantStatus(t, rec, http.StatusForbidden)
	if got := errorReason(t, rec); got != string(RefusalNoGrant) {
		t.Errorf("reason = %q, want no_grant", got)
	}
	if ops.count() != 0 || w.mutations() != 0 {
		t.Error("a refused snapshot was recorded or sent")
	}
	entries := entriesFor(fx, audit.ActionRubrikSnapshot)
	if len(entries) != 1 || entries[0].Result != audit.ResultDenied || entries[0].ResourceNamespace != "team-a" ||
		entries[0].User != "alice" || entries[0].ResourceName != idNS {
		t.Errorf("audit = %+v; want one denial for alice on team-a naming the object", entries)
	}
}

// A snapshot is recorded, sent under the object's own SLA domain (KTD8),
// reported as running with its job, and audited exactly once with the job
// id. Repeating the request replays the record without a second mutation.
func TestSnapshotIsRecordedSentAndReplayed(t *testing.T) {
	fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "create snapshots team-a")
	rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID))
	wantStatus(t, rec, http.StatusCreated)
	got := responseData[OperationView](t, rec)
	if got.ID != testOpID || got.Kind != store.RubrikOpSnapshot || got.State != store.RubrikOpRunning ||
		got.JobFamily != store.RubrikJobTaskchain || got.JobID != "tc-1" || got.ObjectID != idNS || got.Replayed {
		t.Errorf("response = %+v; want a running snapshot of idNS on taskchain tc-1", got)
	}
	snaps, _ := w.sent()
	if want := []string{string(FamilyNamespace) + " " + idNS + " " + idSLA}; !slices.Equal(snaps, want) {
		t.Errorf("snapshots sent = %v, want %v", snaps, want)
	}
	if op := ops.get(t, testOpID); op.Owner != "u-alice" || op.State != store.RubrikOpRunning || op.JobHandle != "tc-1" {
		t.Errorf("stored operation = %+v; want alice's running snapshot with its handle", op)
	}
	entries := entriesFor(fx, audit.ActionRubrikSnapshot)
	if len(entries) != 1 || entries[0].Result != audit.ResultSuccess || !strings.Contains(entries[0].Detail, "job tc-1") ||
		!strings.Contains(entries[0].Detail, testOpID) {
		t.Fatalf("audit = %+v; want one success carrying the operation and job id", entries)
	}

	again := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(strings.ToUpper(testOpID)))
	wantStatus(t, again, http.StatusOK)
	if r := responseData[OperationView](t, again); !r.Replayed || r.ID != testOpID || r.JobID != "tc-1" {
		t.Errorf("replay = %+v; want the recorded operation, marked replayed", r)
	}
	if w.mutations() != 1 || len(entriesFor(fx, audit.ActionRubrikSnapshot)) != 1 {
		t.Error("a replay sent the mutation again or was audited as a new write")
	}
}

// The same operation id with a different request is operation_id_reused,
// and another user's id is a conflict that discloses nothing.
func TestOperationIDReuseIsRefused(t *testing.T) {
	fx, _, w, _ := writeFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true,
		"get protections team-a", "create snapshots team-a", "get protections team-b", "create snapshots team-b",
		"update slaassignments team-a")
	useObjects(fx, objNS(idNS, "team-a", idSLA, nil), objNS(idB, "team-b", idSLA, nil))
	wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusCreated)

	for name, c := range map[string]struct {
		user         *auth.User
		method, path string
		body         any
		reason       string
	}{
		"other namespace": {alice, http.MethodPost, "/rubrik/namespaces/team-b/snapshots", snapshotBody(testOpID), "operation_id_reused"},
		"other kind":      {alice, http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", idSLA2), "operation_id_reused"},
		"an admin":        {root, http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID), "operation_id_conflict"},
		"other user":      {bob, http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID), "operation_id_conflict"},
	} {
		rec := fx.do(t, c.user, "local", c.method, c.path, c.body)
		wantStatus(t, rec, http.StatusConflict)
		if got := errorReason(t, rec); got != c.reason {
			t.Errorf("%s: reason = %q, want %s", name, got, c.reason)
		}
		if strings.Contains(rec.Body.String(), "tc-1") {
			t.Errorf("%s: the refusal disclosed the earlier operation: %s", name, rec.Body.String())
		}
	}
	if w.mutations() != 1 {
		t.Errorf("%d mutations sent, want only the first", w.mutations())
	}
}

// An SLA change while another SLA change on the same object is still open
// is operation_in_flight, and nothing is sent.
func TestSLAChangeWhileAnotherIsInFlight(t *testing.T) {
	for _, state := range []string{store.RubrikOpApplying, store.RubrikOpRunning, store.RubrikOpUnknown} {
		t.Run(state, func(t *testing.T) {
			fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "update slaassignments team-a")
			ops.seed(store.RubrikOperation{ID: otherOp, Owner: "u-bob", ClusterID: "local", Kind: store.RubrikOpSLAAssign,
				Namespace: "team-a", RSCObjectID: idNS, State: state, CreatedAt: time.Now(), UpdatedAt: time.Now()})
			rec := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", idSLA2))
			wantStatus(t, rec, http.StatusConflict)
			if got := errorReason(t, rec); got != "operation_in_flight" {
				t.Errorf("reason = %q, want operation_in_flight", got)
			}
			if strings.Contains(rec.Body.String(), otherOp) {
				t.Error("the refusal disclosed the other user's operation id")
			}
			if w.mutations() != 0 {
				t.Error("the blocked change was sent")
			}
			if e := entriesFor(fx, audit.ActionRubrikSLAAssign); len(e) != 1 || e[0].Result != audit.ResultFailure {
				t.Errorf("audit = %+v; want one failure", e)
			}
		})
	}
}

// An indeterminate RSC answer (a timeout, a 5xx, a GraphQL error) leaves
// the operation unknown with a fixed message, never RSC's text, and a
// definite refusal is failed, for both writes. Repeating the request
// replays the record and never resends the mutation.
func TestMutationOutcomes(t *testing.T) {
	outcomes := map[string]struct {
		err   error
		state string
	}{
		"timeout":                {fmt.Errorf("%w: context deadline exceeded at 10.0.0.7", ErrUnreachable), store.RubrikOpUnknown},
		"5xx":                    {fmt.Errorf("%w: status 502 from 10.0.0.7", ErrUnreachable), store.RubrikOpUnknown},
		"graphql":                {&GraphQLError{Messages: []string{"internal error at tenant"}}, store.RubrikOpUnknown},
		"rejected":               {fmt.Errorf("%w: sla assignment not applied", ErrRejected), store.RubrikOpFailed},
		"auth":                   {fmt.Errorf("%w: status 401 at 10.0.0.7", ErrAuthFailed), store.RubrikOpFailed},
		"invalid request":        {fmt.Errorf("%w: bad input at 10.0.0.7", ErrInvalidRequest), store.RubrikOpFailed},
		"not configured":         {ErrNotConfigured, store.RubrikOpFailed},
		"connection unavailable": {fmt.Errorf("%w: secret at 10.0.0.7", ErrConnectionUnavailable), store.RubrikOpFailed},
		"invalid tenant url":     {fmt.Errorf("%w: 10.0.0.7", ErrInvalidTenantURL), store.RubrikOpFailed},
		"rate limited":           {fmt.Errorf("%w: 10.0.0.7", ErrRateLimited), store.RubrikOpFailed},
	}
	kinds := map[string]struct {
		grant, method, path string
		body                any
		action              audit.Action
		fail                func(*fakeWriter, error)
	}{
		"sla": {"update slaassignments team-a", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", idSLA2),
			audit.ActionRubrikSLAAssign, func(w *fakeWriter, err error) { w.assignErr = err }},
		"snapshot": {"create snapshots team-a", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID),
			audit.ActionRubrikSnapshot, func(w *fakeWriter, err error) { w.snapshotErr = err }},
	}
	for kindName, k := range kinds {
		for name, c := range outcomes {
			t.Run(kindName+"/"+name, func(t *testing.T) {
				fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", k.grant)
				k.fail(w, c.err)
				rec := fx.do(t, alice, "local", k.method, k.path, k.body)
				wantStatus(t, rec, http.StatusCreated)
				got := responseData[OperationView](t, rec)
				if got.State != c.state || got.Message == "" || got.JobID != "" || got.JobFamily != "" {
					t.Errorf("response = %+v; want state %s with a message and no job", got, c.state)
				}
				for _, leak := range []string{"10.0.0.7", "internal error at tenant", "status", "sla assignment", "bad input"} {
					if strings.Contains(got.Message, leak) {
						t.Errorf("the message carries RSC error text %q: %q", leak, got.Message)
					}
				}
				if op := ops.get(t, testOpID); op.State != c.state || strings.Contains(op.FailureMessage, "10.0.0.7") {
					t.Errorf("stored operation = %+v; want state %s with a fixed message", op, c.state)
				}
				e := entriesFor(fx, k.action)
				if len(e) != 1 || e[0].Result != audit.ResultFailure || strings.Contains(e[0].Detail, "10.0.0.7") {
					t.Errorf("audit = %+v; want one failure carrying no RSC text", e)
				}

				again := fx.do(t, alice, "local", k.method, k.path, k.body)
				wantStatus(t, again, http.StatusOK)
				if r := responseData[OperationView](t, again); !r.Replayed || r.State != c.state {
					t.Errorf("retry = %+v; want a replay of the %s operation", r, c.state)
				}
				if w.mutations() != 1 || len(entriesFor(fx, k.action)) != 1 {
					t.Errorf("%d mutations and %d audit entries after a retry; want one of each", w.mutations(), len(entriesFor(fx, k.action)))
				}
			})
		}
	}
}

// Covers AE5 at the handler: a removal sends the removal for the chosen
// object only (the RETAIN_SNAPSHOTS argument is pinned against the fake
// tenant in operations_test.go), and says whether another object still
// protects the namespace.
func TestSLARemoval(t *testing.T) {
	nsSet := func(id, sla string) Object {
		return objSet(id, "namespace", Coverage{Namespaces: []string{"team-a"}}, sla, nil)
	}
	for name, c := range map[string]struct {
		objects   []Object
		sets      bool
		objectID  string
		protected bool
	}{
		"only object":           {[]Object{objNS(idNS, "team-a", idSLA, nil)}, false, "", false},
		"other set protects":    {[]Object{nsSet(idSet, idSLA), nsSet(idB, idSLA)}, true, idSet, true},
		"other set unprotected": {[]Object{nsSet(idSet, ""), nsSet(idB, idSLA)}, true, idB, false},
	} {
		t.Run(name, func(t *testing.T) {
			fx, _, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "update slaassignments team-a")
			if c.sets {
				useSets(fx, c.objects...)
			} else {
				useObjects(fx, c.objects...)
			}
			rec := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, c.objectID, ""))
			wantStatus(t, rec, http.StatusCreated)
			got := responseData[OperationView](t, rec)
			if got.State != store.RubrikOpSucceeded || got.StillProtected == nil || *got.StillProtected != c.protected {
				t.Errorf("response = %+v; want succeeded with stillProtected %v", got, c.protected)
			}
			want := c.objectID
			if want == "" {
				want = idNS
			}
			if _, assigns := w.sent(); !slices.Equal(assigns, []string{want + " "}) {
				t.Errorf("assignments sent = %q; want a removal of %s only", assigns, want)
			}
			if e := entriesFor(fx, audit.ActionRubrikSLAAssign); len(e) != 1 || !strings.Contains(e[0].Detail, "sla removed") {
				t.Errorf("audit = %+v; want one entry saying the SLA was removed", e)
			}

			// A retry and a later read answer it too.
			again := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, c.objectID, ""))
			wantStatus(t, again, http.StatusOK)
			read := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
			wantStatus(t, read, http.StatusOK)
			for name, rec := range map[string]*httptest.ResponseRecorder{"replay": again, "read": read} {
				if got := responseData[OperationView](t, rec); got.StillProtected == nil || *got.StillProtected != c.protected {
					t.Errorf("%s = %+v; want stillProtected %v", name, got, c.protected)
				}
			}
		})
	}
}

// With two objects protecting a namespace an SLA change must name one
// (R15); naming one changes that object only.
func TestSLAChangeNeedsObjectWhenSeveralProtect(t *testing.T) {
	fx, _, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "update slaassignments team-a")
	useSets(fx, objSet(idB, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil),
		objSet(idSet, "namespace", Coverage{Namespaces: []string{"team-a"}}, idSLA, nil))

	rec := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", idSLA2))
	wantStatus(t, rec, http.StatusBadRequest)
	if got := errorReason(t, rec); got != "object_required" {
		t.Errorf("reason = %q, want object_required", got)
	}
	if !strings.Contains(rec.Body.String(), idB) || !strings.Contains(rec.Body.String(), idSet) {
		t.Errorf("the refusal does not list the objects to choose from: %s", rec.Body.String())
	}

	rec = fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(otherOp, strings.ToUpper(idSet), idSLA2))
	wantStatus(t, rec, http.StatusCreated)
	if _, assigns := w.sent(); !slices.Equal(assigns, []string{idSet + " " + idSLA2}) {
		t.Errorf("assignments sent = %q; want only %s", assigns, idSet)
	}
}

// An objectId that does not protect the namespace is 404 and nothing is
// sent: an object of another namespace, an unknown object, and a relic.
func TestObjectMustProtectTheNamespace(t *testing.T) {
	relic := objNS(idSet, "team-a", idSLA, nil)
	relic.Relic = true
	fx, ops, w, _ := writeFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true,
		"get protections team-a", "update slaassignments team-a", "get protections team-b")
	useObjects(fx, objNS(idNS, "team-a", idSLA, nil), objNS(idB, "team-b", idSLA, nil), relic)
	for i, id := range []string{idB, idWide, idSet} {
		op := fmt.Sprintf("cccccccc-cccc-4ccc-8ccc-%012d", i)
		rec := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(op, id, idSLA2))
		wantStatus(t, rec, http.StatusNotFound)
		if got := errorReason(t, rec); got != "object_not_found" {
			t.Errorf("object %s: reason = %q, want object_not_found", id, got)
		}
	}
	if w.mutations() != 0 || ops.count() != 0 {
		t.Error("a write naming an object outside the namespace was recorded or sent")
	}
}

// A set covering several namespaces is admin-only to act on (R7), even for
// a user holding every grant on every namespace it covers.
func TestMultiNamespaceSetIsAdminOnlyToChange(t *testing.T) {
	wide := objSet(idWide, "cluster", Coverage{Namespaces: []string{"team-a", "team-b"}}, idSLA, nil)
	fx, _, w, _ := writeFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true,
		"get protections team-a", "update slaassignments team-a", "get protections team-b", "update slaassignments team-b")
	useSets(fx, wide)

	rec := fx.do(t, alice, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, idWide, idSLA2))
	wantStatus(t, rec, http.StatusForbidden)
	if got := errorReason(t, rec); got != string(RefusalAdminOnly) {
		t.Errorf("reason = %q, want admin_only", got)
	}
	if w.mutations() != 0 {
		t.Error("a non-admin change of a multi-namespace set was sent")
	}
	wantStatus(t, fx.do(t, root, "local", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(otherOp, idWide, idSLA2)), http.StatusCreated)
}

// The snapshot's SLA domain (KTD8): the object's own, a different pick is
// refused, an object without one needs a known pick.
func TestSnapshotSLAChoice(t *testing.T) {
	grants := []string{"get protections team-a", "create snapshots team-a"}
	t.Run("pick differs from the object's own", func(t *testing.T) {
		fx, _, w, _ := writeFixture(t, teamA(), true, grants...)
		rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots",
			map[string]any{"operationId": testOpID, "slaId": idSLA2})
		wantStatus(t, rec, http.StatusBadRequest)
		if got := errorReason(t, rec); got != "sla_not_allowed" || w.mutations() != 0 {
			t.Errorf("reason = %q, mutations %d; want sla_not_allowed and nothing sent", got, w.mutations())
		}
	})
	for name, c := range map[string]struct {
		pick   any
		status int
		reason string
	}{
		"no pick":      {nil, http.StatusBadRequest, "sla_required"},
		"unknown pick": {idWide, http.StatusBadRequest, "unknown_sla"},
		"known pick":   {idSLA2, http.StatusCreated, ""},
	} {
		t.Run("unprotected object, "+name, func(t *testing.T) {
			fx, _, w, _ := writeFixture(t, teamA(), true, grants...)
			useObjects(fx, objNS(idNS, "team-a", "", nil))
			body := map[string]any{"operationId": testOpID}
			if c.pick != nil {
				body["slaId"] = c.pick
			}
			rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", body)
			wantStatus(t, rec, c.status)
			if c.reason != "" {
				if got := errorReason(t, rec); got != c.reason {
					t.Errorf("reason = %q, want %s", got, c.reason)
				}
				return
			}
			if snaps, _ := w.sent(); !slices.Equal(snaps, []string{string(FamilyNamespace) + " " + idNS + " " + idSLA2}) {
				t.Errorf("snapshots sent = %v; want one under the picked domain", snaps)
			}
		})
	}
}

// Bad bodies are 400 and nothing is recorded or audited: a malformed
// operation id, a missing slaId on an SLA change (so a forgotten field can
// never remove protection), malformed object and SLA ids.
func TestWriteRequestValidation(t *testing.T) {
	fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "update slaassignments team-a", "create snapshots team-a")
	for name, c := range map[string]struct {
		method, path string
		body         any
	}{
		"braced id":    {http.MethodPost, "/rubrik/namespaces/team-a/snapshots", map[string]any{"operationId": "{" + testOpID + "}"}},
		"nil id":       {http.MethodPost, "/rubrik/namespaces/team-a/snapshots", map[string]any{"operationId": "00000000-0000-0000-0000-000000000000"}},
		"no slaId":     {http.MethodPut, "/rubrik/namespaces/team-a/sla", map[string]any{"operationId": testOpID}},
		"bad objectId": {http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "ns-1", idSLA2)},
		"bad slaId":    {http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", "gold")},
		"not json":     {http.MethodPut, "/rubrik/namespaces/team-a/sla", "{"},
	} {
		rec := fx.do(t, alice, "local", c.method, c.path, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if rec := fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/Team_A/snapshots", snapshotBody(testOpID)); rec.Code != http.StatusNotFound {
		t.Errorf("invalid namespace: status %d, want 404", rec.Code)
	}
	if ops.count() != 0 || w.mutations() != 0 || len(fx.audit.all()) != 0 {
		t.Error("an invalid request was recorded, sent or audited")
	}
}

// A non-admin cannot write on a remote cluster (R10), and a non-admin
// without the read grant learns nothing about the namespace.
func TestWriteRefusalsBeforeTheMapping(t *testing.T) {
	fx, _, w, _ := writeFixture(t, map[string]time.Time{"team-a": created, "team-b": created}, true,
		"get protections team-a", "create snapshots team-a", "create snapshots team-b")
	rec := fx.do(t, alice, "remote-1", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID))
	wantStatus(t, rec, http.StatusForbidden)
	if got := errorReason(t, rec); got != StateAdminOnly {
		t.Errorf("remote reason = %q, want admin_only", got)
	}
	rec = fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-b/snapshots", snapshotBody(otherOp))
	wantStatus(t, rec, http.StatusNotFound)
	if w.mutations() != 0 || rscCalls(fx) != 0 {
		t.Error("a refused write reached RSC")
	}
}

// Without an operation store or writer the write endpoints answer 503
// rather than failing open.
func TestWritesUnavailableWithoutStore(t *testing.T) {
	fx := newHandlerFixture(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/rubrik/namespaces/team-a/snapshots"},
		{http.MethodPut, "/rubrik/namespaces/team-a/sla"},
		{http.MethodGet, "/rubrik/operations/" + testOpID},
	} {
		rec := fx.do(t, root, "local", c.method, c.path, slaBody(testOpID, "", idSLA))
		wantStatus(t, rec, http.StatusServiceUnavailable)
		if got := errorReason(t, rec); got != "writes_unavailable" {
			t.Errorf("%s %s reason = %q, want writes_unavailable", c.method, c.path, got)
		}
	}
}

// GET on a running operation follows its job: a taskchain that succeeded
// while the job reports PARTIAL_SUCCESS is partial, never success; an
// async request follows the activity series.
func TestOperationReadRefreshesTheJob(t *testing.T) {
	t.Run("taskchain partial", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "create snapshots team-a")
		wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusCreated)

		get := func() OperationView {
			rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
			wantStatus(t, rec, http.StatusOK)
			return responseData[OperationView](t, rec)
		}
		if got := get(); got.State != store.RubrikOpRunning {
			t.Errorf("while the taskchain runs, state = %s, want running", got.State)
		}
		started := time.Now()
		w.taskchains["tc-1"] = TaskchainState{Status: JobSucceeded}
		w.jobs = []Job{
			{ID: "old", ObjectID: idNS, Status: JobSucceeded, StartTime: ptr(started.Add(-time.Hour))},
			{ID: "j1", ObjectID: idNS, Status: JobPartial, StartTime: &started, FailureReason: "2 of 3 resources"},
		}
		got := get()
		if got.State != store.RubrikOpPartial || got.Message != "2 of 3 resources" || got.FinishedAt == nil {
			t.Errorf("state = %+v; want partial with RSC's reason, finished", got)
		}
		if op := ops.get(t, testOpID); op.State != store.RubrikOpPartial {
			t.Errorf("stored state = %s, want partial recorded", op.State)
		}
	})
	t.Run("async request", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
		now := time.Now()
		ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
			Namespace: "team-a", RSCObjectID: idSet, State: store.RubrikOpRunning, JobFamily: store.RubrikJobAsyncRequest,
			JobHandle: "req-1", CreatedAt: now, UpdatedAt: now})
		w.jobs = []Job{{ID: "j1", ObjectID: idSet, Status: JobFailed, StartTime: &now, FailureReason: "quota exceeded"}}
		rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
		wantStatus(t, rec, http.StatusOK)
		if got := responseData[OperationView](t, rec); got.State != store.RubrikOpFailed || got.Message != "quota exceeded" {
			t.Errorf("state = %+v; want failed with RSC's reason", got)
		}
	})
	// The activity series is filtered to the operation's own object and to
	// jobs that started around it; with several candidates nothing is
	// guessed.
	t.Run("job attribution", func(t *testing.T) {
		now := time.Now()
		own := Job{ID: "own", ObjectID: idSet, Status: JobFailed, StartTime: ptr(now.Add(time.Second)), FailureReason: "quota exceeded"}
		for name, c := range map[string]struct {
			jobs  []Job
			state string
			msg   string
		}{
			"foreign object ignored": {[]Job{{ID: "x", ObjectID: idB, Status: JobSucceeded, StartTime: &now, Message: "other tenant"}, own},
				store.RubrikOpFailed, "quota exceeded"},
			"later job ignored": {[]Job{own, {ID: "late", ObjectID: idSet, Status: JobSucceeded, StartTime: ptr(now.Add(noJobGrace + time.Minute))}},
				store.RubrikOpFailed, "quota exceeded"},
			"two candidates stay running": {[]Job{own, {ID: "sched", ObjectID: idSet, Status: JobSucceeded, StartTime: ptr(now.Add(time.Minute))}},
				store.RubrikOpRunning, ""},
			"only a foreign job": {[]Job{{ID: "x", ObjectID: idB, Status: JobSucceeded, StartTime: &now}},
				store.RubrikOpRunning, ""},
		} {
			t.Run(name, func(t *testing.T) {
				fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
				ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
					Namespace: "team-a", RSCObjectID: idSet, State: store.RubrikOpRunning, JobFamily: store.RubrikJobAsyncRequest,
					JobHandle: "req-1", CreatedAt: now, UpdatedAt: now})
				w.jobs = c.jobs
				rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
				wantStatus(t, rec, http.StatusOK)
				if got := responseData[OperationView](t, rec); got.State != c.state || got.Message != c.msg {
					t.Errorf("state = %+v; want %s with message %q", got, c.state, c.msg)
				}
			})
		}
	})
	// A succeeded taskchain is partial only when its one job says so; a
	// partial job of another snapshot running alongside does not count.
	t.Run("taskchain with a concurrent job", func(t *testing.T) {
		fx, _, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "create snapshots team-a")
		wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusCreated)
		now := time.Now()
		w.taskchains["tc-1"] = TaskchainState{Status: JobSucceeded}
		w.jobs = []Job{
			{ID: "j1", ObjectID: idNS, Status: JobSucceeded, StartTime: &now},
			{ID: "j2", ObjectID: idNS, Status: JobPartial, StartTime: ptr(now.Add(time.Second)), FailureReason: "another snapshot's"},
		}
		rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
		if got := responseData[OperationView](t, rec); got.State != store.RubrikOpSucceeded {
			t.Errorf("state = %+v; want the taskchain's succeeded", got)
		}
	})
	t.Run("taskchain failed and canceled", func(t *testing.T) {
		for st, msg := range map[TaskchainState]string{
			{Status: JobFailed, Error: "disk full"}: "disk full",
			{Status: JobCanceled}:                   msgCanceled,
		} {
			fx, _, w, _ := writeFixture(t, teamA(), true, "get protections team-a", "create snapshots team-a")
			wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusCreated)
			w.taskchains["tc-1"] = st
			rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
			if got := responseData[OperationView](t, rec); got.State != store.RubrikOpFailed || got.Message != msg {
				t.Errorf("taskchain %+v: state = %+v; want failed with %q", st, got, msg)
			}
		}
	})
	// An unknown snapshot is running once its job shows up, and failed when
	// RSC shows no job for it after the grace.
	t.Run("unknown snapshot", func(t *testing.T) {
		now := time.Now()
		for name, c := range map[string]struct {
			updated time.Time
			jobs    []Job
			state   string
		}{
			"job running":        {now, []Job{{ID: "j1", ObjectID: idSet, Status: JobRunning, StartTime: &now}}, store.RubrikOpRunning},
			"no job yet":         {now, nil, store.RubrikOpUnknown},
			"no job after grace": {now.Add(-noJobGrace - time.Minute), nil, store.RubrikOpFailed},
		} {
			t.Run(name, func(t *testing.T) {
				fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
				ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
					Namespace: "team-a", RSCObjectID: idSet, State: store.RubrikOpUnknown, CreatedAt: c.updated, UpdatedAt: c.updated})
				w.jobs = c.jobs
				rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
				if got := responseData[OperationView](t, rec); got.State != c.state {
					t.Errorf("state = %s, want %s", got.State, c.state)
				}
			})
		}
	})
	t.Run("refresh failure keeps the state", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a")
		now := time.Now()
		ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
			Namespace: "team-a", RSCObjectID: idSet, State: store.RubrikOpRunning, JobFamily: store.RubrikJobAsyncRequest,
			CreatedAt: now, UpdatedAt: now})
		w.jobsErr = fmt.Errorf("%w: dial 10.0.0.7", ErrUnreachable)
		rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
		wantStatus(t, rec, http.StatusOK)
		got := responseData[OperationView](t, rec)
		if got.State != store.RubrikOpRunning || got.RefreshError != HealthUnreachable || strings.Contains(rec.Body.String(), "10.0.0.7") {
			t.Errorf("response = %+v; want running with the unreachable class only", got)
		}
	})
}

// An SLA change of unknown outcome settles from RSC's object list: a list
// read after the attempt that shows the requested SLA proves it applied; a
// list showing another SLA proves it did not only once the change had
// noJobGrace to land, since a 5xx may be applied late and a list stored
// just after the attempt may have been read before it. A list read before
// the attempt settles nothing.
func TestUnknownSLAChangeSettles(t *testing.T) {
	now := time.Now()
	for name, c := range map[string]struct {
		configured string
		updated    time.Time
		state      string
	}{
		"applied":                 {idSLA2, now.Add(-time.Minute), store.RubrikOpSucceeded},
		"not applied yet":         {idSLA, now.Add(-time.Minute), store.RubrikOpUnknown},
		"not applied after grace": {idSLA, now.Add(-noJobGrace - time.Minute), store.RubrikOpFailed},
		"list older than attempt": {idSLA2, now.Add(time.Hour), store.RubrikOpUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, _, _ := writeFixture(t, teamA(), true, "get protections team-a")
			o := objNS(idNS, "team-a", idSLA2, nil)
			o.ConfiguredSLA = SLARef{ID: c.configured}
			useObjects(fx, o)
			ops.seed(store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSLAAssign,
				Namespace: "team-a", RSCObjectID: idNS, State: store.RubrikOpUnknown,
				Request: json.RawMessage(`{"family":"namespace","slaId":"` + idSLA2 + `"}`), CreatedAt: c.updated, UpdatedAt: c.updated})
			rec := fx.do(t, alice, "local", http.MethodGet, "/rubrik/operations/"+testOpID, nil)
			wantStatus(t, rec, http.StatusOK)
			if got := responseData[OperationView](t, rec); got.State != c.state {
				t.Errorf("state = %s, want %s", got.State, c.state)
			}
		})
	}
}

// settleSLA's failed verdict relies on any fetch finishing noJobGrace after
// the attempt having started after it, with room for clock skew.
func TestSLASettleGraceOutlastsAFetch(t *testing.T) {
	if fetchTimeout+jobStartSkew >= noJobGrace {
		t.Fatalf("fetchTimeout %v + skew %v must stay under noJobGrace %v", fetchTimeout, jobStartSkew, noJobGrace)
	}
}

// Reading an operation re-checks access every time (KTD7): after the
// owner's read grant is revoked the owner gets 404 and an admin still sees
// it; another user never sees it.
func TestOperationReadRechecksTheGrant(t *testing.T) {
	fx, _, _, _ := writeFixture(t, teamA(), true, "get protections team-a", "create snapshots team-a")
	wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusCreated)
	path := "/rubrik/operations/" + testOpID

	wantStatus(t, fx.do(t, alice, "local", http.MethodGet, path, nil), http.StatusOK)
	wantStatus(t, fx.do(t, bob, "local", http.MethodGet, path, nil), http.StatusNotFound)

	fx.h.access = NewAccess(newFakeChecker(), lister(t, teamA()), true)
	wantStatus(t, fx.do(t, alice, "local", http.MethodGet, path, nil), http.StatusNotFound)
	wantStatus(t, fx.do(t, alice, "local", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)), http.StatusNotFound)
	wantStatus(t, fx.do(t, root, "local", http.MethodGet, path, nil), http.StatusOK)

	// A namespace recreated after the operation is an earlier namespace's.
	fx.h.access = NewAccess(newFakeChecker("get protections team-a"), lister(t, map[string]time.Time{"team-a": time.Now().Add(time.Hour)}), true)
	wantStatus(t, fx.do(t, alice, "local", http.MethodGet, path, nil), http.StatusNotFound)

	wantStatus(t, fx.do(t, root, "local", http.MethodGet, "/rubrik/operations/not-a-uuid", nil), http.StatusNotFound)
}

// A store that cannot answer stops a write before RSC: 503, the request
// may be retried with the same id, and nothing is sent. A concurrent
// request that won the insert with the same id is answered as a replay.
func TestWriteStoreFailures(t *testing.T) {
	grants := []string{"get protections team-a", "create snapshots team-a"}
	path := "/rubrik/namespaces/team-a/snapshots"
	for name, inject := range map[string]func(*fakeOps){
		"reading the earlier row": func(o *fakeOps) { o.getErr = fmt.Errorf("pg down at 10.0.0.7") },
		"inserting the row":       func(o *fakeOps) { o.insertErr = fmt.Errorf("pg down at 10.0.0.7") },
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, w, _ := writeFixture(t, teamA(), true, grants...)
			inject(ops)
			rec := fx.do(t, alice, "local", http.MethodPost, path, snapshotBody(testOpID))
			wantStatus(t, rec, http.StatusServiceUnavailable)
			if got := errorReason(t, rec); got != "store_unavailable" ||
				!strings.Contains(rec.Body.String(), `"retrySameOperationId":true`) ||
				!strings.Contains(rec.Body.String(), `"applied":false`) || strings.Contains(rec.Body.String(), "10.0.0.7") {
				t.Errorf("response = %s; want store_unavailable, applied false, retry the same id", rec.Body.String())
			}
			if w.mutations() != 0 {
				t.Error("the mutation was sent although the operation was not recorded")
			}
			if e := entriesFor(fx, audit.ActionRubrikSnapshot); len(e) != 1 || e[0].Result != audit.ResultFailure {
				t.Errorf("audit = %+v; want one failure", e)
			}
		})
	}
	t.Run("lost insert race", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, grants...)
		now := time.Now()
		ops.raceRow = &store.RubrikOperation{ID: testOpID, Owner: "u-alice", ClusterID: "local", Kind: store.RubrikOpSnapshot,
			Namespace: "team-a", RSCObjectID: idNS, State: store.RubrikOpRunning, JobFamily: store.RubrikJobTaskchain, JobHandle: "tc-9",
			RequestHash: requestHash(kindSnapshot, "local", "team-a", writeRequest{OperationID: testOpID}), CreatedAt: now, UpdatedAt: now}
		rec := fx.do(t, alice, "local", http.MethodPost, path, snapshotBody(testOpID))
		wantStatus(t, rec, http.StatusOK)
		if got := responseData[OperationView](t, rec); !got.Replayed || got.JobID != "tc-9" {
			t.Errorf("response = %+v; want the winner's operation, replayed", got)
		}
		if w.mutations() != 0 {
			t.Error("the losing request sent the mutation")
		}
	})
	// The mutation reached RSC but its outcome could not be recorded: the
	// response reports what is known, the write is audited once, and a
	// retry finds the row still applying and sends nothing.
	t.Run("recording the outcome fails", func(t *testing.T) {
		fx, ops, w, _ := writeFixture(t, teamA(), true, grants...)
		ops.updateErr = fmt.Errorf("pg down")
		rec := fx.do(t, alice, "local", http.MethodPost, path, snapshotBody(testOpID))
		wantStatus(t, rec, http.StatusCreated)
		if got := responseData[OperationView](t, rec); got.State != store.RubrikOpRunning || got.JobID != "tc-1" {
			t.Errorf("response = %+v; want running on tc-1", got)
		}
		if e := entriesFor(fx, audit.ActionRubrikSnapshot); len(e) != 1 || e[0].Result != audit.ResultSuccess {
			t.Errorf("audit = %+v; want one success", e)
		}
		ops.updateErr = nil
		again := fx.do(t, alice, "local", http.MethodPost, path, snapshotBody(testOpID))
		wantStatus(t, again, http.StatusConflict)
		if got := errorReason(t, again); got != "operation_in_flight" {
			t.Errorf("retry reason = %q, want operation_in_flight", got)
		}
		if w.mutations() != 1 {
			t.Errorf("%d mutations sent, want 1", w.mutations())
		}
	})
}

// A client that hangs up once the operation is recorded cuts neither the
// mutation nor the record of its outcome short (KTD7).
func TestMutationSurvivesClientHangUp(t *testing.T) {
	for name, c := range map[string]struct {
		grant, method, path string
		body                any
	}{
		"snapshot": {"create snapshots team-a", http.MethodPost, "/rubrik/namespaces/team-a/snapshots", snapshotBody(testOpID)},
		"sla":      {"update slaassignments team-a", http.MethodPut, "/rubrik/namespaces/team-a/sla", slaBody(testOpID, "", idSLA2)},
	} {
		t.Run(name, func(t *testing.T) {
			fx, ops, w, _ := writeFixture(t, teamA(), true, "get protections team-a", c.grant)
			var hangUp context.CancelFunc
			cancelling := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					ctx, cancel := context.WithCancel(r.Context())
					defer cancel()
					hangUp = cancel
					next.ServeHTTP(rw, r.WithContext(ctx))
				})
			}
			ops.afterInsert = func() { hangUp() }
			serveWith(t, fx.h, cancelling, alice, "local", c.method, c.path, c.body)
			if w.mutations() != 1 || w.ctxErr != nil {
				t.Errorf("mutations %d, mutation context error %v; want one sent on a live context", w.mutations(), w.ctxErr)
			}
			if ops.updateCtxErr != nil {
				t.Errorf("the outcome was recorded on a cancelled context: %v", ops.updateCtxErr)
			}
			if op := ops.get(t, testOpID); op.State == store.RubrikOpApplying {
				t.Error("the outcome was not recorded after the client hung up")
			}
		})
	}
}
