package changes

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// In-memory receipt store. It mirrors the guards of store.ChangeReceiptStore
// (ErrReceiptExists on a duplicate id, ErrReceiptAlreadyFinal on a terminal
// row, ErrReceiptNotFound on an unknown id) and lets a test inject a failure
// at any D3 step.
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*store.ChangeReceipt
	now  func() time.Time

	failInsert          error
	failMark            error
	failAppendAt        int // 0-based append call that fails; -1 never
	failAppendErr       error
	failFinalize        error
	failSetVerification error
	failGet             error
	honorCtx            bool // return ctx.Err() when the context is done

	appends   int
	calls     []string
	finalized []store.ReceiptState
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		rows:         map[uuid.UUID]*store.ChangeReceipt{},
		now:          time.Now,
		failAppendAt: -1,
	}
}

func (f *fakeStore) ctxErr(ctx context.Context) error {
	if f.honorCtx {
		return ctx.Err()
	}
	return nil
}

func (f *fakeStore) Insert(ctx context.Context, r store.ChangeReceipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "insert")
	if err := f.ctxErr(ctx); err != nil {
		return err
	}
	if f.failInsert != nil {
		return f.failInsert
	}
	if r.ID == uuid.Nil || r.OwnerID == "" || r.ContentDigest == "" {
		return store.ErrReceiptInvalid
	}
	if _, ok := f.rows[r.ID]; ok {
		return store.ErrReceiptExists
	}
	if r.State == "" {
		r.State = store.ReceiptApplying
	}
	if r.VerificationState == "" {
		r.VerificationState = store.VerifyPending
	}
	if r.ClusterID == "" {
		r.ClusterID = "local"
	}
	r.Objects = []store.ReceiptObject{}
	r.CreatedAt = f.now()
	f.rows[r.ID] = &r
	return nil
}

func (f *fakeStore) MarkMutationStarted(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "mark")
	if err := f.ctxErr(ctx); err != nil {
		return err
	}
	if f.failMark != nil {
		return f.failMark
	}
	r, ok := f.rows[id]
	if !ok {
		return store.ErrReceiptNotFound
	}
	if r.CompletedAt != nil {
		return store.ErrReceiptAlreadyFinal
	}
	if r.MutationStartedAt == nil {
		t := f.now()
		r.MutationStartedAt = &t
	}
	return nil
}

func (f *fakeStore) AppendObject(ctx context.Context, id uuid.UUID, o store.ReceiptObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "append")
	n := f.appends
	f.appends++
	if err := f.ctxErr(ctx); err != nil {
		return err
	}
	if f.failAppendAt >= 0 && n == f.failAppendAt {
		if f.failAppendErr != nil {
			return f.failAppendErr
		}
		return errors.New("injected append failure")
	}
	r, ok := f.rows[id]
	if !ok {
		return store.ErrReceiptNotFound
	}
	if r.CompletedAt != nil {
		return store.ErrReceiptAlreadyFinal
	}
	r.Objects = append(r.Objects, o)
	return nil
}

func (f *fakeStore) Finalize(ctx context.Context, id uuid.UUID, state store.ReceiptState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "finalize")
	if err := f.ctxErr(ctx); err != nil {
		return err
	}
	if f.failFinalize != nil {
		return f.failFinalize
	}
	if !state.IsTerminal() {
		return store.ErrReceiptInvalid
	}
	r, ok := f.rows[id]
	if !ok {
		return store.ErrReceiptNotFound
	}
	if r.CompletedAt != nil {
		return store.ErrReceiptAlreadyFinal
	}
	t := f.now()
	r.State = state
	r.CompletedAt = &t
	f.finalized = append(f.finalized, state)
	return nil
}

func (f *fakeStore) SetVerification(ctx context.Context, id uuid.UUID, state store.VerificationState, payload json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "setVerification")
	if err := f.ctxErr(ctx); err != nil {
		return err
	}
	if f.failSetVerification != nil {
		return f.failSetVerification
	}
	r, ok := f.rows[id]
	if !ok {
		return store.ErrReceiptNotFound
	}
	if r.VerificationState.IsFinal() && !state.IsFinal() {
		return store.ErrReceiptAlreadyFinal
	}
	r.VerificationState = state
	r.Verification = append(json.RawMessage(nil), payload...)
	if state.IsFinal() {
		t := f.now()
		r.VerifiedAt = &t
	}
	return nil
}

func (f *fakeStore) Get(ctx context.Context, id uuid.UUID) (*store.ChangeReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "get")
	if err := f.ctxErr(ctx); err != nil {
		return nil, err
	}
	if f.failGet != nil {
		return nil, f.failGet
	}
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	cp.Objects = append([]store.ReceiptObject(nil), r.Objects...)
	return &cp, nil
}

// row returns the stored row, failing the test when absent.
func (f *fakeStore) row(t *testing.T, id uuid.UUID) *store.ChangeReceipt {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		t.Fatalf("no receipt row for %s", id)
	}
	cp := *r
	return &cp
}

// put seeds a row directly (for the idempotency branches).
func (f *fakeStore) put(r store.ChangeReceipt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[r.ID] = &r
}

// ---------------------------------------------------------------------------
// Fake apply engine: replays scripted observations through the observer and
// stops when the observer refuses, exactly as the contract demands.
// ---------------------------------------------------------------------------

type fakeEngine struct {
	calls   int
	script  []ApplyObservation
	between func(index int) // runs before each document is "applied"
}

func (e *fakeEngine) apply(observe ApplyObserverFunc) TrackedApplyOutcome {
	e.calls++
	var out TrackedApplyOutcome
	for _, o := range e.script {
		if e.between != nil {
			e.between(o.Index)
		}
		out.Attempted = append(out.Attempted, o)
		if err := observe(o); err != nil {
			out.Stopped = err
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

var (
	testUser  = &auth.User{ID: "local:alice", Username: "alice"}
	otherUser = &auth.User{ID: "local:bob", Username: "bob"}
	fixedNow  = time.Date(2026, 9, 10, 13, 15, 0, 0, time.UTC)
)

func doc(kind, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}}
	if ns != "" {
		u.SetNamespace(ns)
	}
	return u
}

func threeDocs() []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		doc("Deployment", "prod", "web"),
		doc("Service", "prod", "web"),
		doc("ConfigMap", "prod", "web-config"),
	}
}

func okObs(i int, kind, ns, name, action string) ApplyObservation {
	return ApplyObservation{Index: i, Group: "apps", Version: "v1", Resource: strings.ToLower(kind) + "s",
		Kind: kind, Namespace: ns, Name: name, UID: "uid-" + name, Action: action}
}

func failObs(i int, kind, ns, name, errText, class string) ApplyObservation {
	return ApplyObservation{Index: i, Kind: kind, Namespace: ns, Name: name, Action: ActionFailed, Error: errText, ErrorClass: class}
}

func threeSuccess() []ApplyObservation {
	return []ApplyObservation{
		okObs(0, "Deployment", "prod", "web", ActionConfigured),
		okObs(1, "Service", "prod", "web", ActionUnchanged),
		okObs(2, "ConfigMap", "prod", "web-config", ActionCreated),
	}
}

func newRequest(id uuid.UUID, docs []*unstructured.Unstructured, raw string) TrackedApplyRequest {
	return TrackedApplyRequest{
		OperationID: id,
		User:        testUser,
		ClusterID:   "local",
		ClusterGen:  "local",
		RawBody:     []byte(raw),
		Docs:        docs,
	}
}

func newTestService(fs *fakeStore) *Service {
	fs.now = func() time.Time { return fixedNow }
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServiceWith(fs, quiet, func() time.Time { return fixedNow })
}

func mustConflict(t *testing.T, err error, reason string) *OperationConflictError {
	t.Helper()
	var ce *OperationConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *OperationConflictError, got %T: %v", err, err)
	}
	if ce.Reason != reason {
		t.Fatalf("reason = %q, want %q", ce.Reason, reason)
	}
	return ce
}

func mustUnavailable(t *testing.T, err error, step string) *StoreUnavailableError {
	t.Helper()
	var se *StoreUnavailableError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StoreUnavailableError, got %T: %v", err, err)
	}
	if se.Step != step {
		t.Fatalf("step = %q, want %q", se.Step, step)
	}
	return se
}

// ---------------------------------------------------------------------------
// D3: write ordering and abort behaviour
// ---------------------------------------------------------------------------

func TestTrackedApply_HappyPath_RecordsEveryObjectInOrder(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	eng := &fakeEngine{script: threeSuccess()}
	id := uuid.New()

	res, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), eng.apply)
	if err != nil {
		t.Fatalf("TrackedApply: %v", err)
	}
	if eng.calls != 1 {
		t.Fatalf("apply called %d times, want 1", eng.calls)
	}
	wantCalls := []string{"insert", "mark", "append", "append", "append", "finalize"}
	if strings.Join(fs.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("store call order = %v, want %v", fs.calls, wantCalls)
	}
	row := fs.row(t, id)
	if row.State != store.ReceiptApplied || row.CompletedAt == nil || row.MutationStartedAt == nil {
		t.Fatalf("row not finalized as applied: %+v", row)
	}
	if len(row.Objects) != 3 {
		t.Fatalf("recorded %d objects, want 3", len(row.Objects))
	}
	for i, o := range row.Objects {
		if o.Index != i {
			t.Fatalf("object %d has index %d", i, o.Index)
		}
	}
	if row.ContentDigest != computeDigest([]byte("raw")) {
		t.Fatalf("digest = %q", row.ContentDigest)
	}
	tr := res.Tracking
	if tr.State != store.ReceiptApplied || tr.RecordedThrough != 3 || tr.NotAttempted != 0 || tr.Replayed {
		t.Fatalf("tracking = %+v", tr)
	}
	if tr.ReceiptURL != "/v1/changes/"+id.String() || tr.Verification.URL != "/v1/changes/"+id.String()+"/verification" {
		t.Fatalf("urls = %q / %q", tr.ReceiptURL, tr.Verification.URL)
	}
	if tr.Verification.State != store.VerifyPending {
		t.Fatalf("verification state = %q, want pending", tr.Verification.State)
	}
	if len(tr.Warnings) != 0 || tr.Warnings == nil {
		t.Fatalf("warnings = %#v, want empty non-nil", tr.Warnings)
	}
	c := res.Counts()
	if c != (ApplyCounts{Total: 3, Created: 1, Configured: 1, Unchanged: 1}) {
		t.Fatalf("counts = %+v", c)
	}
}

func TestTrackedApply_InsertFailsBeforeMutation_NothingApplied(t *testing.T) {
	fs := newFakeStore()
	fs.failInsert = errors.New("connection refused")
	svc := newTestService(fs)
	eng := &fakeEngine{script: threeSuccess()}

	_, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
	mustUnavailable(t, err, "insert")
	if eng.calls != 0 {
		t.Fatalf("apply was called %d times; nothing may be applied when intent was not recorded", eng.calls)
	}
	if len(fs.rows) != 0 {
		t.Fatalf("a row was persisted despite the insert failure")
	}
}

func TestTrackedApply_MarkMutationStartedFails_NothingApplied(t *testing.T) {
	t.Run("store error", func(t *testing.T) {
		fs := newFakeStore()
		fs.failMark = errors.New("connection reset")
		svc := newTestService(fs)
		eng := &fakeEngine{script: threeSuccess()}
		id := uuid.New()

		_, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), eng.apply)
		mustUnavailable(t, err, "mark")
		if eng.calls != 0 {
			t.Fatalf("apply called %d times", eng.calls)
		}
		// The row exists with mutation_started_at NULL: reconciliation will
		// classify it as failed ("never started"), which is the truth.
		row := fs.row(t, id)
		if row.MutationStartedAt != nil || row.CompletedAt != nil || row.State != store.ReceiptApplying {
			t.Fatalf("row should be an unstamped in-flight intent: %+v", row)
		}
	})
	t.Run("already final (reconciled between insert and mark)", func(t *testing.T) {
		fs := newFakeStore()
		fs.failMark = store.ErrReceiptAlreadyFinal
		svc := newTestService(fs)
		eng := &fakeEngine{script: threeSuccess()}

		_, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
		mustConflict(t, err, ReasonOperationIDReused)
		if eng.calls != 0 {
			t.Fatalf("apply called %d times", eng.calls)
		}
	})
}

func TestTrackedApply_AppendFailsMidBundle_StopsAndReportsNotAttempted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"database error", errors.New("write timeout")},
		{"row reconciled under us", store.ErrReceiptAlreadyFinal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeStore()
			fs.failAppendAt, fs.failAppendErr = 1, tc.err
			svc := newTestService(fs)
			eng := &fakeEngine{script: threeSuccess()}
			id := uuid.New()

			res, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), eng.apply)
			if err != nil {
				t.Fatalf("a recording failure after mutation must be reported, not returned: %v", err)
			}
			if eng.calls != 1 {
				t.Fatalf("apply called %d times", eng.calls)
			}
			if len(res.Results) != 3 {
				t.Fatalf("results must cover every document: got %d", len(res.Results))
			}
			// Documents 0 and 1 were attempted (1's outcome could not be
			// recorded); document 2 was never attempted.
			if res.Results[0].Action != ActionConfigured || res.Results[1].Action != ActionUnchanged {
				t.Fatalf("attempted outcomes must be reported truthfully: %+v", res.Results[:2])
			}
			if res.Results[2].Action != ActionFailed || res.Results[2].Error != NotAppliedError {
				t.Fatalf("never-attempted doc = %+v", res.Results[2])
			}
			if res.Results[2].Kind != "ConfigMap" || res.Results[2].Name != "web-config" || res.Results[2].Namespace != "prod" {
				t.Fatalf("never-attempted doc must carry the document identity: %+v", res.Results[2])
			}
			tr := res.Tracking
			if tr.NotAttempted != 1 || tr.RecordedThrough != 1 {
				t.Fatalf("notAttempted=%d recordedThrough=%d, want 1/1", tr.NotAttempted, tr.RecordedThrough)
			}
			if tr.State != store.ReceiptUnknown {
				t.Fatalf("state = %q, want unknown (the cluster holds more than the receipt)", tr.State)
			}
			if len(tr.Warnings) == 0 {
				t.Fatalf("a recording failure must surface as a warning")
			}
			c := res.Counts()
			if c.Total != 3 || c.Failed != 1 {
				t.Fatalf("legacy summary invariants broken: %+v", c)
			}
			row := fs.row(t, id)
			if len(row.Objects) != 1 {
				t.Fatalf("receipt must hold the recorded prefix only: %d objects", len(row.Objects))
			}
		})
	}
}

func TestTrackedApply_FinalizeFails_ReportsUnknown(t *testing.T) {
	fs := newFakeStore()
	fs.failFinalize = errors.New("connection lost")
	svc := newTestService(fs)
	eng := &fakeEngine{script: threeSuccess()}
	id := uuid.New()

	res, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), eng.apply)
	if err != nil {
		t.Fatalf("finalize failure must not become an error: %v", err)
	}
	if res.Tracking.State != store.ReceiptUnknown {
		t.Fatalf("state = %q, want unknown", res.Tracking.State)
	}
	if !containsWarning(res.Tracking.Warnings, "finalization failed") {
		t.Fatalf("warnings = %v", res.Tracking.Warnings)
	}
	// Results are still the truth about what happened.
	if c := res.Counts(); c.Failed != 0 || c.Total != 3 {
		t.Fatalf("counts = %+v", c)
	}
	// The row stays recovery-visible.
	row := fs.row(t, id)
	if row.CompletedAt != nil || row.State != store.ReceiptApplying || len(row.Objects) != 3 {
		t.Fatalf("row must remain in flight with its full object list: %+v", row)
	}
}

func TestTrackedApply_FinalizeAlreadyFinal_ReportsReconciliationWarning(t *testing.T) {
	fs := newFakeStore()
	fs.failFinalize = store.ErrReceiptAlreadyFinal
	svc := newTestService(fs)
	eng := &fakeEngine{script: threeSuccess()}

	res, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tracking.State != store.ReceiptUnknown || !containsWarning(res.Tracking.Warnings, "reconciliation") {
		t.Fatalf("tracking = %+v", res.Tracking)
	}
}

func TestTrackedApply_NeverReplaysOnAnyPath(t *testing.T) {
	scenarios := map[string]func(fs *fakeStore){
		"insert fails":           func(fs *fakeStore) { fs.failInsert = errors.New("x") },
		"mark fails":             func(fs *fakeStore) { fs.failMark = errors.New("x") },
		"mark already final":     func(fs *fakeStore) { fs.failMark = store.ErrReceiptAlreadyFinal },
		"append 0 fails":         func(fs *fakeStore) { fs.failAppendAt = 0 },
		"append 1 fails":         func(fs *fakeStore) { fs.failAppendAt = 1 },
		"append 2 fails":         func(fs *fakeStore) { fs.failAppendAt = 2 },
		"finalize fails":         func(fs *fakeStore) { fs.failFinalize = errors.New("x") },
		"finalize already final": func(fs *fakeStore) { fs.failFinalize = store.ErrReceiptAlreadyFinal },
		"happy":                  func(*fakeStore) {},
	}
	for name, arrange := range scenarios {
		t.Run(name, func(t *testing.T) {
			fs := newFakeStore()
			arrange(fs)
			svc := newTestService(fs)
			eng := &fakeEngine{script: threeSuccess()}
			id := uuid.New()
			req := newRequest(id, threeDocs(), "raw")

			_, _ = svc.TrackedApply(context.Background(), req, eng.apply)
			// A client retry with the same id, whatever happened the first time.
			_, _ = svc.TrackedApply(context.Background(), req, eng.apply)
			if eng.calls > 1 {
				t.Fatalf("apply ran %d times across the first attempt and a retry; a replay is a duplicate mutation", eng.calls)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// D4: idempotency branches
// ---------------------------------------------------------------------------

// seedTerminal writes a finished receipt for the given request as if a prior
// attempt had run and its response had been dropped.
func seedTerminal(fs *fakeStore, req TrackedApplyRequest, state store.ReceiptState, objs []store.ReceiptObject) {
	done := fixedNow.Add(-time.Minute)
	fs.put(store.ChangeReceipt{
		ID: req.OperationID, OwnerID: req.User.ID, OwnerUsername: req.User.Username,
		ClusterID: req.ClusterID, ClusterGeneration: req.ClusterGen,
		ContentDigest: computeDigest(req.RawBody), DocumentCount: len(req.Docs),
		State: state, Objects: objs, VerificationState: store.VerifyPending,
		CreatedAt: done.Add(-time.Second), MutationStartedAt: &done, CompletedAt: &done,
	})
}

func TestTrackedApply_DuplicateOperationID_SameDigest_Replays(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	req := newRequest(uuid.New(), threeDocs(), "raw")
	recorded := []store.ReceiptObject{
		{Index: 0, Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "prod", Name: "web", UID: "u0", Action: ActionConfigured},
		{Index: 1, Resource: "services", Kind: "Service", Namespace: "prod", Name: "web", UID: "u1", Action: ActionUnchanged},
		{Index: 2, Kind: "ConfigMap", Namespace: "prod", Name: "web-config", Action: ActionFailed, Error: "admission denied"},
	}
	seedTerminal(fs, req, store.ReceiptPartial, recorded)
	eng := &fakeEngine{script: threeSuccess()}

	res, err := svc.TrackedApply(context.Background(), req, eng.apply)
	if err != nil {
		t.Fatalf("replay must succeed: %v", err)
	}
	if eng.calls != 0 {
		t.Fatalf("apply was called on a replay")
	}
	if !res.Tracking.Replayed {
		t.Fatalf("replayed flag not set")
	}
	if res.Tracking.State != store.ReceiptPartial {
		t.Fatalf("state = %q, want the stored partial", res.Tracking.State)
	}
	if len(res.Results) != 3 || res.Results[2].Action != ActionFailed || res.Results[2].Error != "admission denied" {
		t.Fatalf("results must be the stored outcome: %+v", res.Results)
	}
	if res.Results[0].UID != "u0" || res.Results[0].Resource != "deployments" {
		t.Fatalf("result 0 = %+v", res.Results[0])
	}
	if c := res.Counts(); c != (ApplyCounts{Total: 3, Configured: 1, Unchanged: 1, Failed: 1}) {
		t.Fatalf("counts = %+v", c)
	}
	if res.Tracking.RecordedThrough != 3 || res.Tracking.NotAttempted != 0 {
		t.Fatalf("tracking = %+v", res.Tracking)
	}
	// No store write happened: a replay is read-only.
	for _, c := range fs.calls {
		if c != "insert" && c != "get" {
			t.Fatalf("replay performed a store write: %v", fs.calls)
		}
	}
}

func TestTrackedApply_Replay_UnknownReceiptFillsMissingOutcomesAsFailed(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	req := newRequest(uuid.New(), threeDocs(), "raw")
	// The original crashed after recording one object.
	seedTerminal(fs, req, store.ReceiptUnknown, []store.ReceiptObject{
		{Index: 0, Kind: "Deployment", Namespace: "prod", Name: "web", Action: ActionConfigured},
	})
	eng := &fakeEngine{script: threeSuccess()}

	res, err := svc.TrackedApply(context.Background(), req, eng.apply)
	if err != nil {
		t.Fatal(err)
	}
	if eng.calls != 0 {
		t.Fatal("apply called on replay of an unknown receipt")
	}
	if res.Tracking.State != store.ReceiptUnknown || res.Tracking.NotAttempted != 2 || res.Tracking.RecordedThrough != 1 {
		t.Fatalf("tracking = %+v", res.Tracking)
	}
	for _, i := range []int{1, 2} {
		if res.Results[i].Action != ActionFailed || res.Results[i].Error != NotRecordedError {
			t.Fatalf("result %d = %+v", i, res.Results[i])
		}
	}
	if c := res.Counts(); c.Failed != 2 || c.Total != 3 {
		t.Fatalf("a legacy client must not read an interrupted apply as success: %+v", c)
	}
}

func TestTrackedApply_DuplicateOperationID_DifferentDigest_Conflicts(t *testing.T) {
	t.Run("different content", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		first := newRequest(uuid.New(), threeDocs(), "raw-v1")
		seedTerminal(fs, first, store.ReceiptApplied, nil)
		second := first
		second.RawBody = []byte("raw-v2")
		eng := &fakeEngine{script: threeSuccess()}

		_, err := svc.TrackedApply(context.Background(), second, eng.apply)
		ce := mustConflict(t, err, ReasonOperationIDReused)
		if ce.Extra() != nil {
			t.Fatalf("reused carries no extra: %v", ce.Extra())
		}
		if eng.calls != 0 {
			t.Fatal("apply called")
		}
	})
	t.Run("different cluster", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		first := newRequest(uuid.New(), threeDocs(), "raw")
		seedTerminal(fs, first, store.ReceiptApplied, nil)
		second := first
		second.ClusterID = "staging"
		eng := &fakeEngine{script: threeSuccess()}

		_, err := svc.TrackedApply(context.Background(), second, eng.apply)
		mustConflict(t, err, ReasonOperationIDReused)
		if eng.calls != 0 {
			t.Fatal("apply called")
		}
	})
}

func TestTrackedApply_DuplicateOperationID_DifferentOwner_Conflicts(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	bobs := newRequest(uuid.New(), threeDocs(), "raw")
	bobs.User = otherUser
	bobs.ClusterID = "bobs-secret-cluster"
	seedTerminal(fs, bobs, store.ReceiptApplied, []store.ReceiptObject{
		{Index: 0, Kind: "Secret", Namespace: "bob-ns", Name: "bob-credentials", Action: ActionCreated},
	})
	alices := newRequest(bobs.OperationID, threeDocs(), "raw") // same id, same digest even
	eng := &fakeEngine{script: threeSuccess()}

	_, err := svc.TrackedApply(context.Background(), alices, eng.apply)
	ce := mustConflict(t, err, ReasonOperationIDConflict)
	if eng.calls != 0 {
		t.Fatal("apply called")
	}
	if ce.Extra() != nil {
		t.Fatalf("cross-owner conflict must carry no extra: %v", ce.Extra())
	}
	for _, leak := range []string{"bob", "secret", "credentials", "staging", computeDigest([]byte("raw"))} {
		if strings.Contains(strings.ToLower(ce.Error()), strings.ToLower(leak)) {
			t.Fatalf("conflict error leaks %q: %s", leak, ce.Error())
		}
	}
}

func TestTrackedApply_DuplicateOperationID_InFlight_Conflicts(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	req := newRequest(uuid.New(), threeDocs(), "raw")
	started := fixedNow.Add(-5 * time.Second)
	fs.put(store.ChangeReceipt{
		ID: req.OperationID, OwnerID: req.User.ID, ClusterID: "local",
		ContentDigest: computeDigest(req.RawBody), DocumentCount: 3,
		State: store.ReceiptApplying, MutationStartedAt: &started, VerificationState: store.VerifyPending,
	})
	eng := &fakeEngine{script: threeSuccess()}

	_, err := svc.TrackedApply(context.Background(), req, eng.apply)
	ce := mustConflict(t, err, ReasonOperationInFlight)
	if ce.ReceiptID != req.OperationID || ce.Extra()["receiptId"] != req.OperationID.String() {
		t.Fatalf("in-flight conflict must point at the receipt: %+v / %v", ce, ce.Extra())
	}
	if eng.calls != 0 {
		t.Fatal("apply called")
	}
}

func TestTrackedApply_DuplicateOperationID_ReadFails_Unavailable(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	req := newRequest(uuid.New(), threeDocs(), "raw")
	seedTerminal(fs, req, store.ReceiptApplied, nil)
	fs.failGet = errors.New("read timeout")
	eng := &fakeEngine{script: threeSuccess()}

	_, err := svc.TrackedApply(context.Background(), req, eng.apply)
	mustUnavailable(t, err, "read")
	if eng.calls != 0 {
		t.Fatal("apply called")
	}
}

// ---------------------------------------------------------------------------
// AE7, cancellation, generation, digest, secrets, independence
// ---------------------------------------------------------------------------

func TestTrackedApply_PartialSummaryMatchesReceipt(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	eng := &fakeEngine{script: []ApplyObservation{
		okObs(0, "Deployment", "prod", "web", ActionConfigured),
		failObs(1, "Service", "prod", "web", "permission denied: services is forbidden", ErrorClassForbidden),
		okObs(2, "ConfigMap", "prod", "web-config", ActionCreated),
	}}
	id := uuid.New()

	res, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), eng.apply)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tracking.State != store.ReceiptPartial {
		t.Fatalf("state = %q, want partial (never applied)", res.Tracking.State)
	}
	row := fs.row(t, id)
	if row.State != store.ReceiptPartial || len(row.Objects) != 3 {
		t.Fatalf("row = %+v", row)
	}
	if row.Objects[1].Action != ActionFailed || row.Objects[1].Error != "permission denied: services is forbidden" {
		t.Fatalf("non-secret bundle keeps the error text: %+v", row.Objects[1])
	}
	if c := res.Counts(); c != (ApplyCounts{Total: 3, Created: 1, Configured: 1, Failed: 1}) {
		t.Fatalf("counts = %+v", c)
	}
	if fs.finalized[0] != store.ReceiptPartial {
		t.Fatalf("finalized with %q", fs.finalized[0])
	}
}

func TestTrackedApply_AllFailed_FinalizesFailed(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	eng := &fakeEngine{script: []ApplyObservation{
		failObs(0, "Deployment", "prod", "web", "invalid", ErrorClassInvalid),
		failObs(1, "Service", "prod", "web", "invalid", ErrorClassInvalid),
		failObs(2, "ConfigMap", "prod", "web-config", "invalid", ErrorClassInvalid),
	}}
	res, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tracking.State != store.ReceiptFailed {
		t.Fatalf("state = %q", res.Tracking.State)
	}
}

func TestTrackedApply_ContextCancelled_LeavesRecoverableIntent(t *testing.T) {
	t.Run("cancelled before intent is recorded: nothing applied, no row", func(t *testing.T) {
		fs := newFakeStore()
		fs.honorCtx = true
		svc := newTestService(fs)
		eng := &fakeEngine{script: threeSuccess()}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := svc.TrackedApply(ctx, newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
		mustUnavailable(t, err, "insert")
		if eng.calls != 0 || len(fs.rows) != 0 {
			t.Fatalf("calls=%d rows=%d", eng.calls, len(fs.rows))
		}
	})
	t.Run("client hangs up mid-apply: outcomes already attempted are still recorded and finalized", func(t *testing.T) {
		fs := newFakeStore()
		fs.honorCtx = true
		svc := newTestService(fs)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// The engine sees the cancellation after the first document and, like
		// the real applier, reports the rest as failed.
		eng := &fakeEngine{
			script: []ApplyObservation{
				okObs(0, "Deployment", "prod", "web", ActionConfigured),
				failObs(1, "Service", "prod", "web", "context canceled", ErrorClassOther),
				failObs(2, "ConfigMap", "prod", "web-config", "context canceled", ErrorClassOther),
			},
			between: func(index int) {
				if index == 1 {
					cancel()
				}
			},
		}
		id := uuid.New()

		res, err := svc.TrackedApply(ctx, newRequest(id, threeDocs(), "raw"), eng.apply)
		if err != nil {
			t.Fatalf("a cancelled request must still get its truthful result: %v", err)
		}
		row := fs.row(t, id)
		if len(row.Objects) != 3 {
			t.Fatalf("recording must not depend on the request context: %d objects recorded", len(row.Objects))
		}
		if row.State != store.ReceiptPartial || row.CompletedAt == nil {
			t.Fatalf("row must be finalized truthfully despite cancellation: %+v", row)
		}
		if res.Tracking.State != store.ReceiptPartial || res.Tracking.RecordedThrough != 3 {
			t.Fatalf("tracking = %+v", res.Tracking)
		}
	})
}

func TestTrackedApply_ClusterGenerationChanged_IsFlagged(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	req := newRequest(uuid.New(), threeDocs(), "raw")
	req.ClusterID, req.ClusterGen = "staging", "2026-08-02T09:11:04Z"
	seedTerminal(fs, req, store.ReceiptApplied, nil)

	// The cluster was deleted and re-registered under the same id; the retry
	// carries the new generation. Same owner, digest and cluster id: it is a
	// replay, but the client is told the registration changed.
	retry := req
	retry.ClusterGen = "2026-09-01T00:00:00Z"
	eng := &fakeEngine{script: threeSuccess()}
	res, err := svc.TrackedApply(context.Background(), retry, eng.apply)
	if err != nil {
		t.Fatal(err)
	}
	if eng.calls != 0 || !res.Tracking.Replayed {
		t.Fatalf("expected a replay: calls=%d tracking=%+v", eng.calls, res.Tracking)
	}
	if res.Tracking.ClusterGeneration != req.ClusterGen {
		t.Fatalf("tracking reports the recorded generation, got %q", res.Tracking.ClusterGeneration)
	}
	if !containsWarning(res.Tracking.Warnings, "registration changed") {
		t.Fatalf("warnings = %v", res.Tracking.Warnings)
	}

	row := fs.row(t, req.OperationID)
	if v := NewReceiptView(row, "2026-09-01T00:00:00Z"); !v.TargetGenerationChanged {
		t.Fatalf("view must flag the changed generation: %+v", v)
	}
	if v := NewReceiptView(row, req.ClusterGen); v.TargetGenerationChanged {
		t.Fatalf("same generation must not be flagged")
	}
	if v := NewReceiptView(row, ""); v.TargetGenerationChanged {
		t.Fatalf("an unresolved current generation must not be flagged as a change")
	}
}

func TestTrackedApply_DigestCoversRawBodyNotParsedDocs(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	docsA := threeDocs()
	docsB := threeDocs() // identical parse
	idA, idB := uuid.New(), uuid.New()

	// Same parsed docs, different bytes (whitespace / comments) => different
	// digests: the digest is over what the client sent, not what we parsed.
	if _, err := svc.TrackedApply(context.Background(), newRequest(idA, docsA, "kind: Deployment\n"), (&fakeEngine{script: threeSuccess()}).apply); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TrackedApply(context.Background(), newRequest(idB, docsB, "kind: Deployment # comment\n"), (&fakeEngine{script: threeSuccess()}).apply); err != nil {
		t.Fatal(err)
	}
	a, b := fs.row(t, idA).ContentDigest, fs.row(t, idB).ContentDigest
	if a == b {
		t.Fatalf("digest ignored the raw bytes: %s", a)
	}
	if !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("digest format = %q", a)
	}
	if computeDigest([]byte("kind: Deployment\n")) != a {
		t.Fatalf("digest is not a stable function of the bytes")
	}
	// The raw body is not retained anywhere on the row.
	row := fs.row(t, idA)
	buf, _ := json.Marshal(row)
	if strings.Contains(string(buf), "kind: Deployment") {
		t.Fatalf("raw body leaked into the receipt row: %s", buf)
	}
}

func TestTrackedApply_SecretErrorNeverReachesStore(t *testing.T) {
	const submittedValue = "s3cr3t-p4ssw0rd-value"
	rawErr := `Secret "db" is invalid: data.password: Invalid value: "` + submittedValue + `": must be base64`

	t.Run("Secret object in a bundle", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		docs := []*unstructured.Unstructured{doc("Secret", "prod", "db"), doc("Service", "prod", "web")}
		eng := &fakeEngine{script: []ApplyObservation{
			failObs(0, "Secret", "prod", "db", rawErr, ErrorClassInvalid),
			failObs(1, "Service", "prod", "web", "Service web is invalid: spec.ports: "+submittedValue, ErrorClassInvalid),
		}}
		id := uuid.New()
		res, err := svc.TrackedApply(context.Background(), newRequest(id, docs, "raw"), eng.apply)
		if err != nil {
			t.Fatal(err)
		}
		row := fs.row(t, id)
		if !row.ContainsSecret || !res.Tracking.ContainsSecret {
			t.Fatalf("containsSecret not set")
		}
		buf, _ := json.Marshal(row)
		if strings.Contains(string(buf), submittedValue) {
			t.Fatalf("submitted value reached the store: %s", buf)
		}
		// Every object of a Secret-bearing bundle is reduced to a class.
		if row.Objects[0].Error != "invalid: error detail withheld for a Secret-bearing change" {
			t.Fatalf("secret error = %q", row.Objects[0].Error)
		}
		if row.Objects[1].Error != "invalid: error detail withheld for a Secret-bearing change" {
			t.Fatalf("sibling error in a Secret-bearing bundle = %q", row.Objects[1].Error)
		}
		// The live response keeps the raw text; that is the HTTP layer's call.
		if res.Results[0].Error != rawErr {
			t.Fatalf("live result was sanitized: %q", res.Results[0].Error)
		}
	})
	t.Run("lower-case kind and missing class", func(t *testing.T) {
		o := receiptObjectFor(failObs(0, "secret", "prod", "db", rawErr, ""), false, fixedNow)
		if o.Error != "other: error detail withheld for a Secret-bearing change" {
			t.Fatalf("error = %q", o.Error)
		}
	})
	t.Run("non-secret bundle keeps text but bounded", func(t *testing.T) {
		long := strings.Repeat("x", maxStoredErrorLen+50)
		o := receiptObjectFor(failObs(0, "ConfigMap", "prod", "c", long, ErrorClassInvalid), false, fixedNow)
		if len(o.Error) != maxStoredErrorLen+len("...") {
			t.Fatalf("stored error length = %d", len(o.Error))
		}
		ok := receiptObjectFor(okObs(0, "ConfigMap", "prod", "c", ActionCreated), true, fixedNow)
		if ok.Error != "" {
			t.Fatalf("a success carries no error: %q", ok.Error)
		}
	})
}

func TestTrackedApply_StateAndVerificationAreIndependent(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	id := uuid.New()
	if _, err := svc.TrackedApply(context.Background(), newRequest(id, threeDocs(), "raw"), (&fakeEngine{script: threeSuccess()}).apply); err != nil {
		t.Fatal(err)
	}
	row := fs.row(t, id)
	if row.State != store.ReceiptApplied || row.VerificationState != store.VerifyPending {
		t.Fatalf("applied + pending must round-trip as two independent columns: %q / %q", row.State, row.VerificationState)
	}
	v := NewReceiptView(row, "local")
	if v.State != store.ReceiptApplied || v.Verification.State != store.VerifyPending {
		t.Fatalf("view merged the two states: %+v", v)
	}
}

func TestTrackedApply_InvalidRequests(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	eng := &fakeEngine{script: threeSuccess()}
	v1, _ := uuid.NewUUID() // version 1
	cases := map[string]TrackedApplyRequest{
		"nil id":  func() TrackedApplyRequest { r := newRequest(uuid.Nil, threeDocs(), "raw"); return r }(),
		"v1 id":   func() TrackedApplyRequest { r := newRequest(v1, threeDocs(), "raw"); return r }(),
		"no user": func() TrackedApplyRequest { r := newRequest(uuid.New(), threeDocs(), "raw"); r.User = nil; return r }(),
		"no docs": newRequest(uuid.New(), nil, "raw"),
		"nil doc": newRequest(uuid.New(), []*unstructured.Unstructured{nil}, "raw"),
		"no owner": func() TrackedApplyRequest {
			r := newRequest(uuid.New(), threeDocs(), "raw")
			r.User = &auth.User{}
			return r
		}(),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.TrackedApply(context.Background(), req, eng.apply)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
	t.Run("nil apply", func(t *testing.T) {
		_, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), nil)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v", err)
		}
	})
	if eng.calls != 0 || len(fs.rows) != 0 {
		t.Fatalf("invalid requests must touch nothing: calls=%d rows=%d", eng.calls, len(fs.rows))
	}
}

func TestService_UnavailableWithoutStore(t *testing.T) {
	var nilStore *store.ChangeReceiptStore
	svc := NewService(nilStore, nil)
	if svc.Available() {
		t.Fatal("a nil store must report unavailable")
	}
	eng := &fakeEngine{script: threeSuccess()}
	_, err := svc.TrackedApply(context.Background(), newRequest(uuid.New(), threeDocs(), "raw"), eng.apply)
	mustUnavailable(t, err, "insert")
	if eng.calls != 0 {
		t.Fatal("apply called without a store")
	}
	var s *Service
	if s.Available() {
		t.Fatal("a nil service must report unavailable")
	}
}

func TestApplyTracking_WireShape(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	repair := uuid.New()
	req := newRequest(uuid.New(), threeDocs(), "raw")
	req.RepairOf = &repair
	res, err := svc.TrackedApply(context.Background(), req, (&fakeEngine{script: threeSuccess()}).apply)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := json.Marshal(res.Tracking)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"operationId", "receiptUrl", "state", "clusterId", "clusterGeneration", "contentDigest",
		"recordedThrough", "notAttempted", "replayed", "containsSecret", "repairOf", "objects", "verification", "warnings"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("tracking JSON lacks %q: %s", k, buf)
		}
	}
	if strings.Contains(string(buf), "\"spec\"") || strings.Contains(string(buf), "\"data\"") {
		t.Fatalf("tracking carries content: %s", buf)
	}
	objs := m["objects"].([]any)
	if len(objs) != 3 {
		t.Fatalf("objects = %v", objs)
	}
	for _, k := range []string{"index", "group", "version", "resource", "uid"} {
		if _, ok := objs[0].(map[string]any)[k]; !ok {
			t.Fatalf("object ref lacks %q", k)
		}
	}
	if _, ok := objs[0].(map[string]any)["name"]; ok {
		t.Fatalf("tracking.objects is references only; name belongs to the receipt read path")
	}
}

// ---------------------------------------------------------------------------
// Package invariants, read off the source: no audit, no yaml, no goroutines.
// ---------------------------------------------------------------------------

func TestTrackedApply_WritesNoAuditEntries(t *testing.T) {
	imports := packageImports(t)
	if _, ok := imports["github.com/kubecenter/kubecenter/internal/audit"]; ok {
		t.Fatal("changes imports internal/audit; TrackedApply must write no audit entries (the yaml handler owns them)")
	}
}

func TestPackageInvariants_NoYAMLImportNoGoroutines(t *testing.T) {
	imports := packageImports(t)
	for _, forbidden := range []string{
		"github.com/kubecenter/kubecenter/internal/yaml",
		"github.com/kubecenter/kubecenter/internal/recoverutil",
	} {
		if _, ok := imports[forbidden]; ok {
			t.Fatalf("changes imports %s", forbidden)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.HasPrefix(trimmed, "go ") || strings.Contains(trimmed, " go func") || strings.HasPrefix(trimmed, "go func") {
				t.Fatalf("%s:%d starts a goroutine: %s", e.Name(), i+1, trimmed)
			}
		}
	}
}

// packageImports parses every non-test file of this package and returns the
// set of import paths.
func packageImports(t *testing.T) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]struct{}{}
	for _, p := range pkgs {
		for name, f := range p.Files {
			if filepath.Ext(name) != ".go" {
				continue
			}
			for _, imp := range f.Imports {
				out[strings.Trim(imp.Path.Value, `"`)] = struct{}{}
			}
		}
	}
	return out
}

func containsWarning(ws []string, substr string) bool {
	for _, w := range ws {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
