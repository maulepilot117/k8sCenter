package yaml

// tracked_apply_test.go — Release E U30a: the opt-in tracked apply and the
// legacy wire format it must not disturb.
//
// Everything here runs without a database. The tracked paths that need a
// working receipt store (replay, reuse, partial, Secret redaction, audit with
// an operation id) live in tracked_apply_db_test.go behind the
// KUBECENTER_TEST_DATABASE_URL gate: changes.Service only accepts the
// concrete PostgreSQL store from outside its package, so a hand fake cannot
// stand in for it here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/changes"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// --- A fake API server that remembers what was applied ----------------------

// statefulCluster makes a fake dynamic client answer GET and server-side
// apply PATCH the way an API server does for the purposes of action
// detection: a PATCH of an object that does not exist creates it with a fresh
// UID; a PATCH with byte-identical content leaves the resourceVersion alone
// (unchanged); any other PATCH bumps it (configured). Without this the shared
// echo reactor reports every document as created, and the created /
// configured / unchanged distinction the legacy wire format carries would go
// untested.
//
// Reactors added later with PrependReactor (rejectPatch, failGet) run first,
// so a test can still make one named object fail.
type statefulCluster struct {
	mu      sync.Mutex
	objects map[string]*storedObject
	minted  int
}

type storedObject struct {
	obj   *unstructured.Unstructured
	patch []byte
	rv    int
}

func objectKey(gvr schema.GroupVersionResource, ns, name string) string {
	return gvr.Resource + "/" + ns + "/" + name
}

func makeStateful(dyn *dynfake.FakeDynamicClient) *statefulCluster {
	c := &statefulCluster{objects: map[string]*storedObject{}}
	dyn.PrependReactor("get", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		g := action.(clienttesting.GetAction)
		c.mu.Lock()
		defer c.mu.Unlock()
		if s, ok := c.objects[objectKey(g.GetResource(), g.GetNamespace(), g.GetName())]; ok {
			return true, s.obj.DeepCopy(), nil
		}
		return true, nil, apierrors.NewNotFound(g.GetResource().GroupResource(), g.GetName())
	})
	dyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		p, ok := action.(clienttesting.PatchAction)
		if !ok || p.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		applied := &unstructured.Unstructured{}
		if err := applied.UnmarshalJSON(p.GetPatch()); err != nil {
			return true, nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		key := objectKey(p.GetResource(), p.GetNamespace(), p.GetName())
		s, exists := c.objects[key]
		switch {
		case !exists:
			c.minted++
			s = &storedObject{rv: 1}
			applied.SetUID(types.UID(fmt.Sprintf("uid-%s-%d", p.GetName(), c.minted)))
			c.objects[key] = s
		case string(s.patch) == string(p.GetPatch()):
			return true, s.obj.DeepCopy(), nil
		default:
			s.rv++
			applied.SetUID(s.obj.GetUID())
		}
		applied.SetResourceVersion(fmt.Sprint(s.rv))
		s.obj, s.patch = applied, append([]byte(nil), p.GetPatch()...)
		return true, applied.DeepCopy(), nil
	})
	return c
}

// seed stores an object as if an earlier apply (by anyone) had created it.
func (c *statefulCluster) seed(gvr schema.GroupVersionResource, obj *unstructured.Unstructured, uid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := obj.DeepCopy()
	o.SetUID(types.UID(uid))
	o.SetResourceVersion("1")
	c.objects[objectKey(gvr, o.GetNamespace(), o.GetName())] = &storedObject{obj: o, rv: 1}
}

// failGet makes the pre-PATCH GET of one named object fail with err.
func failGet(dyn *dynfake.FakeDynamicClient, name string, err error) {
	dyn.PrependReactor("get", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.GetAction).GetName() != name {
			return false, nil, nil
		}
		return true, nil, err
	})
}

// patchCount counts every PATCH that reached the client, whatever the
// reactors answered: the number that matters for "applied twice".
func patchCount(dyn *dynfake.FakeDynamicClient) int {
	n := 0
	for _, a := range dyn.Actions() {
		if _, ok := a.(clienttesting.PatchAction); ok {
			n++
		}
	}
	return n
}

func dataKeys(t *testing.T, body []byte) []string {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	keys := make([]string, 0, len(env.Data))
	for k := range env.Data {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func widgetDocSized(name string, size int) string {
	return strings.Replace(widgetDoc(name), "size: 3", fmt.Sprintf("size: %d", size), 1)
}

func bundle(docs ...string) string { return strings.Join(docs, "---\n") }

// --- Legacy wire format ------------------------------------------------------

// The guard on Tracking being a pointer with omitempty: an untracked response
// must serialize to exactly the two keys every legacy client was written
// against.
func TestApplyDocuments_UntrackedResponseHasOnlyResultsAndSummary(t *testing.T) {
	raw, err := json.Marshal(&ApplyResponse{Results: []ApplyResult{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"results", "summary"}) {
		t.Fatalf("untracked ApplyResponse keys = %v; want exactly [results summary] (%s)", keys, raw)
	}

	// The same holds end to end through the handler.
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	w := fx.apply(remoteClusterID, nil, widgetYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if got := dataKeys(t, w.Body.Bytes()); !slices.Equal(got, []string{"results", "summary"}) {
		t.Fatalf("untracked response data keys = %v; want exactly [results summary]", got)
	}
}

// untrackedGolden is the byte-exact body an untracked apply of the bundle in
// TestApplyDocuments_UntrackedBehaviourByteIdentical produced BEFORE tracked
// apply existed. Captured from the pre-U30a handler; any change to it is a
// change to the legacy wire format that web, mobile and wizards parse.
const untrackedGolden = `{"data":{"results":[` +
	`{"index":0,"kind":"Widget","name":"fresh","namespace":"team-a","action":"created"},` +
	`{"index":1,"kind":"Widget","name":"same","namespace":"team-a","action":"unchanged"},` +
	`{"index":2,"kind":"Widget","name":"edited","namespace":"team-a","action":"configured"},` +
	`{"index":3,"kind":"Widget","name":"denied","namespace":"team-a","action":"failed","error":"permission denied: widgets.example.com \"denied\" is forbidden: user cannot patch"}` +
	`],"summary":{"total":4,"created":1,"configured":1,"unchanged":1,"failed":1}}}` + "\n"

func TestApplyDocuments_UntrackedBehaviourByteIdentical(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	rejectPatch(fx.remoteDyn, "denied", func(clienttesting.PatchAction) error {
		return apierrors.NewForbidden(widgetGVR.GroupResource(), "denied", errors.New("user cannot patch"))
	})

	if w := fx.apply(remoteClusterID, nil, bundle(widgetDoc("same"), widgetDoc("edited"))); w.Code != http.StatusOK {
		t.Fatalf("seeding apply status = %d; body=%s", w.Code, w.Body.String())
	}

	w := fx.apply(remoteClusterID, nil, bundle(widgetDoc("fresh"), widgetDoc("same"), widgetDocSized("edited", 5), widgetDoc("denied")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != untrackedGolden {
		t.Fatalf("untracked apply body changed:\n got: %s\nwant: %s", got, untrackedGolden)
	}
}

// --- The observer contract (ApplyDocumentsObserved) --------------------------

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// observeAll applies body to the remote fixture through the observed engine
// and returns the response plus every observation, in the order received.
// The observer fails on the document at index stopAt (-1: never).
func observeAll(t *testing.T, fx *fixture, body string, stopAt int) (*ApplyResponse, []ApplyObservation) {
	t.Helper()
	docs, err := ParseMultiDoc([]byte(body))
	if err != nil {
		t.Fatalf("ParseMultiDoc: %v", err)
	}
	var seen []ApplyObservation
	resp := ApplyDocumentsObserved(context.Background(), fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger,
		func(_ context.Context, obs ApplyObservation) error {
			seen = append(seen, obs)
			if obs.Result.Index == stopAt {
				return errors.New("recording failed")
			}
			return nil
		})
	return resp, seen
}

func TestApplyDocumentsObserved_ObserverSeesMappingAndAppliedObject(t *testing.T) {
	fx := newFixture(t, nil, nil)
	cluster := makeStateful(fx.remoteDyn)
	cluster.seed(widgetGVR, widget("existing", "team-a"), "uid-pre")

	resp, seen := observeAll(t, fx, bundle(widgetDoc("fresh"), widgetDocSized("existing", 5)), -1)

	if len(seen) != 2 {
		t.Fatalf("observer called %d times; want exactly once per document (2)", len(seen))
	}
	wantUID := []types.UID{"uid-fresh-1", "uid-pre"}
	for i, obs := range seen {
		if obs.Result != resp.Results[i] {
			t.Errorf("observation %d result = %+v; want the response's result %+v", i, obs.Result, resp.Results[i])
		}
		if obs.Mapping == nil || obs.Mapping.Resource != widgetGVR || obs.Mapping.Scope.Name() != meta.RESTScopeNameNamespace {
			t.Errorf("observation %d mapping = %+v; want the namespaced widgets mapping", i, obs.Mapping)
		}
		if obs.UID != wantUID[i] {
			t.Errorf("observation %d UID = %q; want the applied object's UID %q", i, obs.UID, wantUID[i])
		}
		if obs.ErrorClass != "" {
			t.Errorf("observation %d errorClass = %q; want none for a success", i, obs.ErrorClass)
		}
	}
	if seen[0].Result.Action != "created" || seen[1].Result.Action != "configured" {
		t.Errorf("actions = %q, %q; want created, configured", seen[0].Result.Action, seen[1].Result.Action)
	}
}

func TestApplyDocumentsObserved_NilObserverEqualsApplyDocuments(t *testing.T) {
	run := func(apply func(*fixture, []*unstructured.Unstructured) *ApplyResponse) string {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		rejectPatch(fx.remoteDyn, "denied", func(clienttesting.PatchAction) error {
			return apierrors.NewForbidden(widgetGVR.GroupResource(), "denied", errors.New("user cannot patch"))
		})
		docs, err := ParseMultiDoc([]byte(bundle(widgetDoc("a"), widgetDoc("denied"), widgetDoc("b"))))
		if err != nil {
			t.Fatalf("ParseMultiDoc: %v", err)
		}
		raw, err := json.Marshal(apply(fx, docs))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(raw)
	}
	legacy := run(func(fx *fixture, docs []*unstructured.Unstructured) *ApplyResponse {
		return ApplyDocuments(context.Background(), fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger)
	})
	nilObs := run(func(fx *fixture, docs []*unstructured.Unstructured) *ApplyResponse {
		return ApplyDocumentsObserved(context.Background(), fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger, nil)
	})
	passive := run(func(fx *fixture, docs []*unstructured.Unstructured) *ApplyResponse {
		return ApplyDocumentsObserved(context.Background(), fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger,
			func(context.Context, ApplyObservation) error { return nil })
	})
	if nilObs != legacy || passive != legacy {
		t.Fatalf("observed engine diverges from ApplyDocuments:\n legacy: %s\n nil:    %s\n passive:%s", legacy, nilObs, passive)
	}
}

// D5: an observer failure stops the engine, and every document after it is
// still reported, as failed, so summary.total == len(docs) and a legacy
// client computing success from summary.failed cannot read success.
func TestApplyDocumentsObserved_ObserverErrorStopsAndReportsRemainingFailed(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)

	resp, seen := observeAll(t, fx, bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c"), widgetDoc("d")), 1)

	if len(seen) != 2 || seen[0].Result.Index != 0 || seen[1].Result.Index != 1 {
		t.Fatalf("observations = %+v; want exactly indices 0 and 1, then the engine stops", seen)
	}
	if got := patchCount(fx.remoteDyn); got != 2 {
		t.Fatalf("patches = %d; want 2 — no document after the failed observation may be attempted", got)
	}
	if resp.Summary.Total != 4 || len(resp.Results) != 4 {
		t.Fatalf("summary = %+v with %d results; want total 4 == len(docs)", resp.Summary, len(resp.Results))
	}
	if resp.Summary.Created != 2 || resp.Summary.Failed != 2 {
		t.Errorf("summary = %+v; want 2 created (attempted outcomes stand) and 2 failed", resp.Summary)
	}
	for i, name := range map[int]string{2: "c", 3: "d"} {
		r := resp.Results[i]
		if r.Index != i || r.Action != "failed" || r.Error != changes.NotAppliedError || r.Name != name || r.Kind != "Widget" || r.Namespace != "team-a" {
			t.Errorf("results[%d] = %+v; want failed %q identifying %s", i, r, changes.NotAppliedError, name)
		}
	}
}

// Only the SSA PATCH's error is classified, and on a failed PATCH the
// observation carries the UID of the object that existed before it, so an
// indeterminate outcome can be verified against the right object.
func TestApplyDocumentsObserved_FailedPatchCarriesPriorUIDAndErrorClass(t *testing.T) {
	gr := widgetGVR.GroupResource()
	cases := []struct {
		name      string
		seeded    bool
		err       error
		wantClass string
		wantText  string
	}{
		{"conflict", true, apierrors.NewConflict(gr, "w", errors.New("conflict with kubectl")), changes.ErrorClassConflict, "Use force to override"},
		{"forbidden", true, apierrors.NewForbidden(gr, "w", errors.New("nope")), changes.ErrorClassForbidden, "permission denied"},
		{"internal error", true, apierrors.NewInternalError(errors.New("etcdserver: request timed out")), changes.ErrorClassIndeterminate, "etcdserver"},
		{"503 from a proxy", true, apierrors.NewServiceUnavailable("upstream connect error"), changes.ErrorClassIndeterminate, "upstream"},
		{"transport reset", true, errors.New("read: connection reset by peer"), changes.ErrorClassIndeterminate, "connection reset"},
		{"deadline", true, context.DeadlineExceeded, changes.ErrorClassIndeterminate, "deadline"},
		{"new object, cut off", false, errors.New("unexpected EOF"), changes.ErrorClassIndeterminate, "EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)
			cluster := makeStateful(fx.remoteDyn)
			wantUID := types.UID("")
			if tc.seeded {
				cluster.seed(widgetGVR, widget("w", "team-a"), "uid-pre")
				wantUID = "uid-pre"
			}
			rejectPatch(fx.remoteDyn, "w", func(clienttesting.PatchAction) error { return tc.err })

			_, seen := observeAll(t, fx, widgetDoc("w"), -1)
			if len(seen) != 1 {
				t.Fatalf("observations = %d; want 1", len(seen))
			}
			obs := seen[0]
			if obs.Result.Action != "failed" || !strings.Contains(obs.Result.Error, tc.wantText) {
				t.Errorf("result = %+v; want failed with today's error text containing %q", obs.Result, tc.wantText)
			}
			if obs.ErrorClass != tc.wantClass {
				t.Errorf("errorClass = %q; want %q", obs.ErrorClass, tc.wantClass)
			}
			if obs.UID != wantUID {
				t.Errorf("UID = %q; want the pre-existing object's UID %q", obs.UID, wantUID)
			}
			if obs.Mapping == nil || obs.Mapping.Resource != widgetGVR {
				t.Errorf("mapping = %+v; want widgets even though the PATCH failed", obs.Mapping)
			}
		})
	}
}

// A failed pre-PATCH GET mutated nothing. It must never make an outcome
// indeterminate: only the PATCH's own error is classified.
func TestApplyDocumentsObserved_PreflightGetErrorIsNotClassified(t *testing.T) {
	gr := widgetGVR.GroupResource()
	t.Run("GET cut off, PATCH succeeds", func(t *testing.T) {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		failGet(fx.remoteDyn, "w", errors.New("read: connection reset by peer"))
		_, seen := observeAll(t, fx, widgetDoc("w"), -1)
		if obs := seen[0]; obs.Result.Action == "failed" || obs.ErrorClass != "" || obs.UID == "" {
			t.Errorf("observation = %+v; want a success with the applied UID and no error class", obs)
		}
	})
	t.Run("GET cut off, PATCH rejected", func(t *testing.T) {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		failGet(fx.remoteDyn, "w", errors.New("read: connection reset by peer"))
		rejectPatch(fx.remoteDyn, "w", func(clienttesting.PatchAction) error {
			return apierrors.NewForbidden(gr, "w", errors.New("nope"))
		})
		_, seen := observeAll(t, fx, widgetDoc("w"), -1)
		if obs := seen[0]; obs.ErrorClass != changes.ErrorClassForbidden || obs.UID != "" {
			t.Errorf("observation = %+v; want class forbidden (the PATCH's), not indeterminate (the GET's), and no UID", obs)
		}
	})
}

// A document that never reached the API server's write path (unknown kind)
// is still observed, with no mapping and no class.
func TestApplyDocumentsObserved_MappingFailureIsObservedUnclassified(t *testing.T) {
	orig := restMappingRetryBackoff
	restMappingRetryBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { restMappingRetryBackoff = orig })

	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	gadget := "apiVersion: example.com/v1\nkind: Gadget\nmetadata:\n  name: g\n  namespace: team-a\n"
	resp, seen := observeAll(t, fx, bundle(gadget, widgetDoc("w")), -1)

	if len(seen) != 2 {
		t.Fatalf("observations = %d; want one per document (2)", len(seen))
	}
	if obs := seen[0]; obs.Result.Action != "failed" || obs.Mapping != nil || obs.ErrorClass != "" || obs.UID != "" {
		t.Errorf("unknown-kind observation = %+v; want failed with no mapping, class or UID", obs)
	}
	if resp.Summary.Total != 2 || resp.Summary.Created != 1 {
		t.Errorf("summary = %+v; want the second document still applied", resp.Summary)
	}
}

// The adapter handed to changes.Service: every attempted document is in
// Attempted (including the one whose recording failed: it WAS sent to the
// cluster), the engine stops on the recorder's error, and the observation
// carries the mapping's group/version/resource.
func TestTrackedApplyEngine_ReportsAttemptedAndStopsOnRecordFailure(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	docs, err := ParseMultiDoc([]byte(bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c"))))
	if err != nil {
		t.Fatalf("ParseMultiDoc: %v", err)
	}
	recordErr := errors.New("append failed")
	var recorded []changes.ApplyObservation
	engine := trackedApplyEngine(context.Background(), fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger)
	out := engine(func(obs changes.ApplyObservation) error {
		recorded = append(recorded, obs)
		if obs.Index == 1 {
			return recordErr
		}
		return nil
	})

	if !errors.Is(out.Stopped, recordErr) {
		t.Errorf("Stopped = %v; want the recorder's error", out.Stopped)
	}
	if len(out.Attempted) != 2 || out.Attempted[0].Index != 0 || out.Attempted[1].Index != 1 {
		t.Fatalf("Attempted = %+v; want indices 0 and 1", out.Attempted)
	}
	if got := patchCount(fx.remoteDyn); got != 2 {
		t.Errorf("patches = %d; want 2", got)
	}
	want := changes.ApplyObservation{
		Index: 0, Group: "example.com", Version: "v1", Resource: "widgets",
		Kind: "Widget", Namespace: "team-a", Name: "a", UID: "uid-a-1", Action: changes.ActionCreated,
	}
	if out.Attempted[0] != want || recorded[0] != want {
		t.Errorf("observation 0 = %+v (recorded %+v); want %+v", out.Attempted[0], recorded[0], want)
	}
}

// --- Handler: opt-in, validation and availability (no database) ---------------

func trackedQuery(opID string) map[string]string {
	return map[string]string{"trackedOperationId": opID}
}

// Without the parameter, the handler never consults the changes service: an
// unavailable service (which 503s any tracked request) changes nothing, and a
// stray repairOf on an untracked request is ignored as before.
func TestHandleApply_NoTrackedParam_DoesNotTouchChangesService(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes *changes.Service
		params  map[string]string
	}{
		{"no service", nil, nil},
		{"unavailable service", changes.NewService(nil, discardLogger), nil},
		{"repairOf alone", changes.NewService(nil, discardLogger), map[string]string{"repairOf": uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)
			makeStateful(fx.remoteDyn)
			fx.handler.Changes = tc.changes
			w := fx.apply(remoteClusterID, tc.params, widgetYAML)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want the untracked 200, body=%s", w.Code, w.Body.String())
			}
			want := `{"data":{"results":[{"index":0,"kind":"Widget","name":"gizmo","namespace":"team-a","action":"created"}],` +
				`"summary":{"total":1,"created":1,"configured":0,"unchanged":0,"failed":0}}}` + "\n"
			if got := w.Body.String(); got != want {
				t.Fatalf("body = %s; want the legacy body %s", got, want)
			}
		})
	}
}

func TestHandleApply_InvalidOperationID_Returns400(t *testing.T) {
	v1, err := uuid.NewUUID()
	if err != nil {
		t.Fatalf("uuid.NewUUID: %v", err)
	}
	v4 := uuid.NewString()
	const badOp, badRepair = "invalid trackedOperationId", "invalid repairOf"
	cases := []struct {
		name        string
		params      map[string]string
		wantMessage string
	}{
		{"not a uuid", trackedQuery("not-a-uuid"), badOp},
		{"empty", trackedQuery(""), badOp},
		{"version 1", trackedQuery(v1.String()), badOp},
		{"nil uuid", trackedQuery(uuid.Nil.String()), badOp},
		{"braced form", trackedQuery("{" + v4 + "}"), badOp},
		{"urn form", trackedQuery("urn:uuid:" + v4), badOp},
		{"malformed repairOf", map[string]string{"trackedOperationId": v4, "repairOf": "nope"}, badRepair},
		{"empty repairOf", map[string]string{"trackedOperationId": v4, "repairOf": ""}, badRepair},
		{"version 1 repairOf", map[string]string{"trackedOperationId": v4, "repairOf": v1.String()}, badRepair},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No service at all: input validation answers before availability
			// is even considered.
			fx := newFixture(t, nil, nil)
			w := fx.apply(remoteClusterID, tc.params, widgetYAML)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400, body=%s", w.Code, w.Body.String())
			}
			if e := decodeError(t, w); e.Error.Message != tc.wantMessage {
				t.Errorf("message = %q; want %q", e.Error.Message, tc.wantMessage)
			}
			assertNoActions(t, "remote", fx.remoteDyn)
			fx.local.assertUntouched(t)
		})
	}
}

// assertRecordingUnavailable checks the tracked 503 contract: reason
// receipt_store_unavailable, extra.applied false, and the step-dependent
// retrySameOperationId hint.
func assertRecordingUnavailable(t *testing.T, w *httptest.ResponseRecorder, retrySame bool) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503, body=%s", w.Code, w.Body.String())
	}
	e := decodeError(t, w).Error
	if e.Reason != changes.ReasonReceiptStoreUnavailable {
		t.Errorf("reason = %q; want %q", e.Reason, changes.ReasonReceiptStoreUnavailable)
	}
	if e.Extra["applied"] != false || e.Extra["retrySameOperationId"] != retrySame {
		t.Errorf("extra = %v; want applied=false, retrySameOperationId=%v", e.Extra, retrySame)
	}
	wantPhrase := "Start a new attempt"
	if retrySame {
		wantPhrase = "retry with the same operation id"
	}
	if !strings.Contains(e.Message, wantPhrase) {
		t.Errorf("message = %q; want it to say %q", e.Message, wantPhrase)
	}
}

// unreachableReceiptService is a changes.Service whose store reports
// available but whose every query fails to connect: Insert fails, so
// TrackedApply returns StoreUnavailableError{Step: "insert"}.
func unreachableReceiptService(t *testing.T) *changes.Service {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return changes.NewService(store.NewChangeReceiptStore(pool), discardLogger)
}

// D3 step 2's precondition and step 2 itself: with no receipt store, or one
// whose insert fails, a tracked request is refused with nothing applied, and
// the operation id is NOT to be reused (it may be spent).
func TestHandleApply_ChangesUnavailable_Returns503AndAppliesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  func(*testing.T) *changes.Service
	}{
		{"nil service", func(*testing.T) *changes.Service { return nil }},
		{"service without a store", func(*testing.T) *changes.Service { return changes.NewService(nil, discardLogger) }},
		{"insert fails", unreachableReceiptService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)
			makeStateful(fx.remoteDyn)
			fx.handler.Changes = tc.svc(t)
			auditLog := &recordingAuditLogger{}
			fx.handler.AuditLogger = auditLog

			// A user with an id, so the service gets as far as the store.
			r := httptest.NewRequest(http.MethodPost, applyURL(trackedQuery(uuid.NewString())), strings.NewReader(widgetYAML))
			user := &auth.User{ID: "u-503", Username: "alice", KubernetesUsername: "alice"}
			r = r.WithContext(middleware.WithClusterID(auth.ContextWithUser(r.Context(), user), remoteClusterID))
			w := serve(fx.handler.HandleApply, r)
			assertRecordingUnavailable(t, w, false)
			if got := patchCount(fx.remoteDyn); got != 0 {
				t.Errorf("patches = %d; want 0", got)
			}
			assertNoActions(t, "remote", fx.remoteDyn)
			fx.local.assertUntouched(t)
			if n := len(auditLog.snapshot()); n != 0 {
				t.Errorf("audit entries = %d; want 0 — nothing was attempted", n)
			}
		})
	}
}

// changes.ErrInvalidRequest reaches the wire as 400 with nothing applied. An
// authenticated user with no id cannot own a receipt; the service refuses
// before any store call (the store here has no pool at all).
func TestHandleApply_TrackedInvalidRequest_Returns400AndAppliesNothing(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	fx.handler.Changes = changes.NewService(store.NewChangeReceiptStore(nil), discardLogger)
	auditLog := &recordingAuditLogger{}
	fx.handler.AuditLogger = auditLog

	// newRequest's user has no ID.
	w := fx.apply(remoteClusterID, trackedQuery(uuid.NewString()), widgetYAML)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400, body=%s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w); e.Error.Message != "invalid tracked apply request" {
		t.Errorf("message = %q; want %q", e.Error.Message, "invalid tracked apply request")
	}
	assertNoActions(t, "remote", fx.remoteDyn)
	if n := len(auditLog.snapshot()); n != 0 {
		t.Errorf("audit entries = %d; want 0", n)
	}
}

// Every TrackedApply error class maps to its documented status, reason and
// extra, and only an idempotency refusal is audited. The read step (an id
// collision whose existing receipt could not be read) is the one 503 that
// tells the client to keep the id. The default branch is unreachable through
// the real service (every error it returns is typed), so the mapping is
// driven directly.
func TestWriteTrackedApplyError_MapsEveryClass(t *testing.T) {
	receipt := uuid.New()
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
		wantExtra  map[string]any
		wantAudit  string
	}{
		{"id of another owner", &changes.OperationConflictError{Reason: changes.ReasonOperationIDConflict, Message: "in use"},
			http.StatusConflict, changes.ReasonOperationIDConflict, nil, changes.ReasonOperationIDConflict + " op=OP"},
		{"in flight", &changes.OperationConflictError{Reason: changes.ReasonOperationInFlight, Message: "poll", ReceiptID: receipt},
			http.StatusConflict, changes.ReasonOperationInFlight, map[string]any{"receiptId": receipt.String()}, changes.ReasonOperationInFlight + " op=OP"},
		{"reused", &changes.OperationConflictError{Reason: changes.ReasonOperationIDReused, Message: "new id"},
			http.StatusConflict, changes.ReasonOperationIDReused, nil, changes.ReasonOperationIDReused + " op=OP"},
		{"store insert", &changes.StoreUnavailableError{Step: "insert", Err: errors.New("dial")},
			http.StatusServiceUnavailable, changes.ReasonReceiptStoreUnavailable, map[string]any{"applied": false, "retrySameOperationId": false}, ""},
		{"store mark", &changes.StoreUnavailableError{Step: "mark", Err: errors.New("dial")},
			http.StatusServiceUnavailable, changes.ReasonReceiptStoreUnavailable, map[string]any{"applied": false, "retrySameOperationId": false}, ""},
		{"store read", &changes.StoreUnavailableError{Step: "read", Err: errors.New("dial")},
			http.StatusServiceUnavailable, changes.ReasonReceiptStoreUnavailable, map[string]any{"applied": false, "retrySameOperationId": true}, ""},
		{"invalid request", fmt.Errorf("%w: no documents", changes.ErrInvalidRequest),
			http.StatusBadRequest, "", nil, ""},
		{"anything else", errors.New("boom"),
			http.StatusInternalServerError, "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auditLog := &recordingAuditLogger{}
			h := &Handler{AuditLogger: auditLog, Logger: discardLogger}
			r := newRequest(http.MethodPost, "/yaml/apply", remoteClusterID, "")
			w := httptest.NewRecorder()
			h.writeTrackedApplyError(w, r, &auth.User{Username: "alice"}, remoteClusterID, "OP", tc.err)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d; want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			e := decodeError(t, w).Error
			if e.Reason != tc.wantReason || !reflect.DeepEqual(e.Extra, tc.wantExtra) {
				t.Errorf("reason/extra = %q %v; want %q %v", e.Reason, e.Extra, tc.wantReason, tc.wantExtra)
			}
			if tc.wantStatus == http.StatusInternalServerError && strings.Contains(w.Body.String(), "boom") {
				t.Errorf("500 body leaks the internal error: %s", w.Body.String())
			}
			entries := auditLog.snapshot()
			switch {
			case tc.wantAudit == "" && len(entries) != 0:
				t.Errorf("audit entries = %+v; want none", entries)
			case tc.wantAudit != "" && (len(entries) != 1 || entries[0].Detail != tc.wantAudit || entries[0].Result != audit.ResultFailure):
				t.Errorf("audit entries = %+v; want one failure with Detail %q", entries, tc.wantAudit)
			}
		})
	}
}

// Untracked per-result audit entries keep every field they had before
// tracked apply existed: one entry per document, on the resolved target, as
// the caller, with the action as Detail and no operation id.
func TestHandleApply_UntrackedAuditEntryFields(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	rejectPatch(fx.remoteDyn, "denied", func(clienttesting.PatchAction) error {
		return apierrors.NewForbidden(widgetGVR.GroupResource(), "denied", errors.New("user cannot patch"))
	})
	auditLog := &recordingAuditLogger{}
	fx.handler.AuditLogger = auditLog

	r := newRequest(http.MethodPost, applyURL(nil), remoteClusterID, bundle(widgetDoc("ok"), widgetDoc("denied")))
	serve(fx.handler.HandleApply, r)

	entries := auditLog.snapshot()
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d; want one per document (2)", len(entries))
	}
	want := []audit.Entry{
		{ClusterID: remoteClusterID, User: "alice", SourceIP: r.RemoteAddr, Action: audit.ActionApply,
			ResourceKind: "Widget", ResourceNamespace: "team-a", ResourceName: "ok", Result: audit.ResultSuccess, Detail: "created"},
		{ClusterID: remoteClusterID, User: "alice", SourceIP: r.RemoteAddr, Action: audit.ActionApply,
			ResourceKind: "Widget", ResourceNamespace: "team-a", ResourceName: "denied", Result: audit.ResultFailure, Detail: "failed"},
	}
	for i, got := range entries {
		if got.Timestamp.IsZero() {
			t.Errorf("entry %d has no timestamp", i)
		}
		got.Timestamp = time.Time{}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("entry %d = %+v\nwant      %+v", i, got, want[i])
		}
	}
}

// --- Request context ending mid-apply (observed applies only) -----------------

// cancelOnPatch cancels ctx when the PATCH of name reaches the API server,
// then answers it with err (nil: let the stateful reactor apply it).
func cancelOnPatch(dyn *dynfake.FakeDynamicClient, name string, cancel context.CancelFunc, err error) {
	dyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.PatchAction).GetName() != name {
			return false, nil, nil
		}
		cancel()
		if err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
}

// A document is indeterminate only if its PATCH was actually issued. Every
// document the apply did not send after the request context ended is
// reported not attempted, with the same text the changes service uses for it
// (NotAttemptedError), and is never observed.
func TestApplyDocumentsObserved_ContextDoneNeverRecordsUnsentDocuments(t *testing.T) {
	t.Run("cancelled after a successful PATCH", func(t *testing.T) {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelOnPatch(fx.remoteDyn, "a", cancel, nil)

		resp, seen := observeCtx(t, ctx, fx, bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c")))
		assertStoppedAfter(t, fx, resp, seen, 1, 1)
		if seen[0].Result.Action != "created" || seen[0].ErrorClass != "" {
			t.Errorf("observation 0 = %+v; want the sent document's real outcome", seen[0])
		}
	})
	t.Run("PATCH issued, then cut off", func(t *testing.T) {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelOnPatch(fx.remoteDyn, "a", cancel, context.Canceled)

		resp, seen := observeCtx(t, ctx, fx, bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c")))
		assertStoppedAfter(t, fx, resp, seen, 1, 1)
		if seen[0].ErrorClass != changes.ErrorClassIndeterminate {
			t.Errorf("observation 0 class = %q; want indeterminate — this PATCH was sent", seen[0].ErrorClass)
		}
	})
	t.Run("cancelled between the GET and the PATCH", func(t *testing.T) {
		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx.remoteDyn.PrependReactor("get", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.(clienttesting.GetAction).GetName() == "b" {
				cancel()
			}
			return false, nil, nil
		})

		resp, seen := observeCtx(t, ctx, fx, bundle(widgetDoc("a"), widgetDoc("b"), widgetDoc("c")))
		assertStoppedAfter(t, fx, resp, seen, 1, 1)
	})
	t.Run("cancelled while waiting for CRD discovery", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		orig := restMappingRetryBackoff
		restMappingRetryBackoff = func(int) time.Duration { cancel(); return time.Hour }
		t.Cleanup(func() { restMappingRetryBackoff = orig })

		fx := newFixture(t, nil, nil)
		makeStateful(fx.remoteDyn)
		gadget := "apiVersion: example.com/v1\nkind: Gadget\nmetadata:\n  name: g\n  namespace: team-a\n"
		resp, seen := observeCtx(t, ctx, fx, bundle(gadget, widgetDoc("w")))
		assertStoppedAfter(t, fx, resp, seen, 0, 0)
	})
}

// The untracked path is unchanged: it never stops early on its own, and a
// cancelled CRD wait keeps its legacy error text.
func TestApplyDocuments_UntrackedDoesNotStopOnContext(t *testing.T) {
	fx := newFixture(t, nil, nil)
	makeStateful(fx.remoteDyn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnPatch(fx.remoteDyn, "a", cancel, nil)
	docs, err := ParseMultiDoc([]byte(bundle(widgetDoc("a"), widgetDoc("b"))))
	if err != nil {
		t.Fatal(err)
	}
	resp := ApplyDocuments(ctx, fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger)
	if got := patchCount(fx.remoteDyn); got != 2 {
		t.Errorf("patches = %d; want 2 — the legacy path issues every PATCH as before", got)
	}
	for _, r := range resp.Results {
		if r.Error == changes.NotAttemptedError {
			t.Errorf("result %+v; the legacy path never reports not-attempted", r)
		}
	}

	orig := restMappingRetryBackoff
	restMappingRetryBackoff = func(int) time.Duration { return time.Hour }
	t.Cleanup(func() { restMappingRetryBackoff = orig })
	done, stop := context.WithCancel(context.Background())
	stop()
	gadget, err := ParseMultiDoc([]byte("apiVersion: example.com/v1\nkind: Gadget\nmetadata:\n  name: g\n  namespace: team-a\n"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := ApplyDocuments(done, fx.remoteDyn, fx.targeter.schema.Mapper, gadget, false, discardLogger)
	if got := legacy.Results[0].Error; !strings.HasPrefix(got, "context cancelled waiting for CRD") {
		t.Errorf("untracked CRD-wait error = %q; want the legacy text", got)
	}
}

func observeCtx(t *testing.T, ctx context.Context, fx *fixture, body string) (*ApplyResponse, []ApplyObservation) {
	t.Helper()
	docs, err := ParseMultiDoc([]byte(body))
	if err != nil {
		t.Fatalf("ParseMultiDoc: %v", err)
	}
	var seen []ApplyObservation
	resp := ApplyDocumentsObserved(ctx, fx.remoteDyn, fx.targeter.schema.Mapper, docs, false, discardLogger,
		func(_ context.Context, obs ApplyObservation) error {
			seen = append(seen, obs)
			return nil
		})
	return resp, seen
}

// assertStoppedAfter checks an observed apply that stopped on its context
// with `observed` documents observed and `patches` PATCHes issued: every
// later document is failed with NotAttemptedError and was never observed,
// and summary.total still equals len(docs).
func assertStoppedAfter(t *testing.T, fx *fixture, resp *ApplyResponse, seen []ApplyObservation, observed, patches int) {
	t.Helper()
	if len(seen) != observed {
		t.Fatalf("observations = %+v; want exactly %d", seen, observed)
	}
	if got := patchCount(fx.remoteDyn); got != patches {
		t.Errorf("patches = %d; want %d — nothing may be sent after the context ended", got, patches)
	}
	if resp.Summary.Total != len(resp.Results) {
		t.Errorf("summary.total = %d; want len(results) %d", resp.Summary.Total, len(resp.Results))
	}
	for _, r := range resp.Results[observed:] {
		if r.Action != "failed" || r.Error != changes.NotAttemptedError {
			t.Errorf("unsent result = %+v; want failed %q", r, changes.NotAttemptedError)
		}
	}
	for _, o := range seen {
		if o.Result.Error == changes.NotAttemptedError {
			t.Errorf("an unsent document was observed: %+v", o)
		}
	}
}
