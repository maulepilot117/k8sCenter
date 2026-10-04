package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Fakes. The receipt store fake from service_test.go is reused for Get; the
// reader adds the two read-only methods the handler needs.
// ---------------------------------------------------------------------------

type fakeReader struct {
	*fakeStore
	grants     map[uuid.UUID][]string
	failList   error
	failGrants error
	grantCalls int
	listCalls  int
}

func newFakeReader() *fakeReader {
	return &fakeReader{fakeStore: newFakeStore(), grants: map[uuid.UUID][]string{}}
}

func (f *fakeReader) ListForOwner(_ context.Context, p store.ReceiptQueryParams) ([]store.ChangeReceipt, int, error) {
	f.listCalls++
	if f.failList != nil {
		return nil, 0, f.failList
	}
	if p.OwnerID == "" {
		return nil, 0, store.ErrReceiptInvalid
	}
	p.Normalize()
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []store.ChangeReceipt
	for _, r := range f.rows {
		if r.OwnerID == p.OwnerID && (p.ClusterID == "" || r.ClusterID == p.ClusterID) {
			all = append(all, *r)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	total := len(all)
	start := p.Offset()
	if start > total {
		start = total
	}
	end := start + p.PageSize
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeReader) GrantsFor(_ context.Context, id uuid.UUID) ([]string, error) {
	f.grantCalls++
	if f.failGrants != nil {
		return nil, f.failGrants
	}
	return append([]string{}, f.grants[id]...), nil
}

// fakeTargeter records which cluster each TargetFor asked for and hands back
// a scripted pair/schema.
type fakeTargeter struct {
	mu         sync.Mutex
	asked      []string
	delay      time.Duration // a stalled cluster: honours ctx like the real router
	dyn        dynamic.Interface
	mapper     meta.RESTMapper
	generation string
	err        error
}

func (f *fakeTargeter) TargetFor(ctx context.Context, clusterID, _ string, _ []string) (*k8s.ClientPair, *k8s.TargetSchema, error) {
	f.mu.Lock()
	f.asked = append(f.asked, clusterID)
	delay := f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("remote cluster: %w", ctx.Err())
		}
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	id := k8s.NormalizedClusterID(clusterID)
	gen := f.generation
	if k8s.IsLocalClusterID(clusterID) {
		gen = "local"
	}
	return &k8s.ClientPair{ClusterID: id, IsLocal: k8s.IsLocalClusterID(clusterID), Dynamic: f.dyn},
		&k8s.TargetSchema{ClusterID: id, Generation: gen, IsLocal: k8s.IsLocalClusterID(clusterID), Mapper: f.mapper}, nil
}

func (f *fakeTargeter) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.asked...)
}

// fakeResolver records what ResolveOwnership was handed and returns a script,
// or blocks until the context ends when block is set.
type fakeResolver struct {
	calls   int
	user    *auth.User
	cluster string
	dyn     dynamic.Interface
	refs    []gitops.ObjectRef
	results []gitops.OwnershipResult
	err     error
	block   bool
}

func (f *fakeResolver) ResolveOwnership(ctx context.Context, user *auth.User, clusterID string, dyn dynamic.Interface, refs []gitops.ObjectRef) ([]gitops.OwnershipResult, error) {
	f.calls++
	f.user, f.cluster, f.dyn, f.refs = user, clusterID, dyn, refs
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

// accessCall is one SAR the handler asked for.
type accessCall struct{ cluster, group, resource, namespace string }

// fakeAccess answers `get` checks from a predicate and records every call,
// including which cluster it was sent to (the resources test constructors
// cannot expose that).
type fakeAccess struct {
	allow func(group, resource, namespace string) bool
	err   error
	delay time.Duration // a stalled SAR: honours ctx like the real checker
	calls []accessCall
}

func allowAll(string, string, string) bool { return true }

func (f *fakeAccess) CanAccessGroupResource(ctx context.Context, clusterID, _ string, _ []string, verb, apiGroup, resource, namespace string) (bool, error) {
	if verb != "get" {
		panic("receipt redaction must check get, got " + verb)
	}
	f.calls = append(f.calls, accessCall{clusterID, apiGroup, resource, namespace})
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return false, fmt.Errorf("SelfSubjectAccessReview: %w", ctx.Err())
		}
	}
	if f.err != nil {
		return false, f.err
	}
	return f.allow(apiGroup, resource, namespace), nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

var adminUser = &auth.User{ID: "oidc:root", Username: "root", Roles: []string{"admin"}}

type harness struct {
	reader   *fakeReader
	svc      *Service
	targeter *fakeTargeter
	resolver *fakeResolver
	access   *fakeAccess
	h        *Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "apps", Version: "v1"}, {Version: "v1"}})
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}, meta.RESTScopeNamespace)
	dyn, _ := fakeDyn()
	hs := &harness{
		reader:   newFakeReader(),
		targeter: &fakeTargeter{dyn: dyn, mapper: mapper, generation: "gen-now"},
		resolver: &fakeResolver{},
		access:   &fakeAccess{allow: allowAll},
	}
	hs.reader.now = func() time.Time { return fixedNow }
	hs.svc = newServiceWith(hs.reader.fakeStore, nil, func() time.Time { return fixedNow })
	hs.h = newHandlerWith(hs.svc, hs.reader, hs.resolver, hs.targeter, hs.access, nil)
	return hs
}

// seed stores a terminal receipt owned by owner and returns it.
func (hs *harness) seed(owner *auth.User, clusterID string, objs ...store.ReceiptObject) *store.ChangeReceipt {
	done := fixedNow.Add(-10 * time.Second)
	started := done.Add(-time.Second)
	r := store.ChangeReceipt{
		ID: uuid.New(), OwnerID: owner.ID, OwnerUsername: owner.Username, ClusterID: clusterID,
		ClusterGeneration: "gen-then", ContentDigest: computeDigest([]byte("raw")), DocumentCount: len(objs),
		State: stateForObjects(objs), Objects: objs, VerificationState: store.VerifyPending,
		CreatedAt: started, MutationStartedAt: &started, CompletedAt: &done,
	}
	if k8s.IsLocalClusterID(clusterID) {
		r.ClusterGeneration = "local"
	}
	hs.reader.put(r)
	return &r
}

func stateForObjects(objs []store.ReceiptObject) store.ReceiptState {
	obs := make([]ApplyObservation, 0, len(objs))
	for _, o := range objs {
		obs = append(obs, ApplyObservation{Action: o.Action})
	}
	if len(obs) == 0 {
		return store.ReceiptApplied
	}
	return stateFor(obs)
}

type request struct {
	user    *auth.User
	cluster string
	id      string
	query   string
	body    string
	ctx     context.Context
}

func (hs *harness) do(t *testing.T, handler http.HandlerFunc, method string, req request) *httptest.ResponseRecorder {
	t.Helper()
	ctx := req.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	var body io.Reader
	if req.body != "" {
		body = strings.NewReader(req.body)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/changes"+req.query, body)
	rctx := chi.NewRouteContext()
	if req.id != "" {
		rctx.URLParams.Add("id", req.id)
	}
	ctx = context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	if req.user != nil {
		ctx = auth.ContextWithUser(ctx, req.user)
	}
	cluster := req.cluster
	if cluster == "" {
		cluster = "local"
	}
	ctx = middleware.WithClusterID(ctx, cluster)
	w := httptest.NewRecorder()
	handler(w, r.WithContext(ctx))
	return w
}

func (hs *harness) get(t *testing.T, user *auth.User, id uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	return hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: user, id: id.String()})
}

func (hs *harness) verify(t *testing.T, user *auth.User, id uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	return hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: user, id: id.String()})
}

// envelope is the api.Response as a test sees it.
type envelope struct {
	Data     json.RawMessage `json:"data"`
	Metadata *struct {
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	} `json:"metadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not an api.Response: %v\n%s", err, w.Body.String())
	}
	return e
}

func decodeDetail(t *testing.T, w *httptest.ResponseRecorder) ReceiptDetail {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var d ReceiptDetail
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &d); err != nil {
		t.Fatalf("data is not a ReceiptDetail: %v", err)
	}
	return d
}

// rawDetail returns the data object's arrays as raw JSON, byte-for-byte as
// the handler wrote them (a re-marshal would reorder keys).
func rawDetail(t *testing.T, w *httptest.ResponseRecorder) (objects, checks []json.RawMessage) {
	t.Helper()
	var m struct {
		Objects []json.RawMessage `json:"objects"`
		Checks  []json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &m); err != nil {
		t.Fatalf("data is not an object: %v", err)
	}
	return m.Objects, m.Checks
}

// decodeView decodes a fresh VerificationView (never into a reused value:
// json.Unmarshal keeps stale fields of reused slice elements).
func decodeView(t *testing.T, w *httptest.ResponseRecorder) VerificationView {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var v VerificationView
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &v); err != nil {
		t.Fatalf("data is not a VerificationView: %v", err)
	}
	return v
}

func expectError(t *testing.T, w *httptest.ResponseRecorder, status int, messageSubstr string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d: %s", w.Code, status, w.Body.String())
	}
	e := decodeEnvelope(t, w)
	if e.Error == nil || e.Error.Code != status || !strings.Contains(e.Error.Message, messageSubstr) {
		t.Fatalf("error envelope = %s, want code %d containing %q", w.Body.String(), status, messageSubstr)
	}
	if e.Data != nil && string(e.Data) != "null" {
		t.Fatalf("error response carries data: %s", e.Data)
	}
}

func secretObj(index int, ns, name string) store.ReceiptObject {
	return recordedObj(index, "", "v1", "secrets", "Secret", ns, name, "uid-secret-"+name)
}

func failedObj(index int, ns, name, errText string) store.ReceiptObject {
	o := recordedDeployment(index, name, "")
	o.Namespace = ns
	o.Action = ActionFailed
	o.Error = errText
	return o
}

// ---------------------------------------------------------------------------
// Envelope gate
// ---------------------------------------------------------------------------

func TestAuthorizeReceiptRead(t *testing.T) {
	rec := &store.ChangeReceipt{OwnerID: "local:alice"}
	cases := []struct {
		name   string
		user   *auth.User
		grants []string
		want   ReadDecision
	}{
		{"nil user", nil, []string{"local:alice"}, ReadDenied},
		{"user without id never matches, even an empty grant", &auth.User{ID: ""}, []string{""}, ReadDenied},
		{"owner", &auth.User{ID: "local:alice"}, nil, ReadAsOwner},
		{"owner outranks admin", &auth.User{ID: "local:alice", Roles: []string{"admin"}}, nil, ReadAsOwner},
		{"admin outranks grantee", &auth.User{ID: "oidc:root", Roles: []string{"admin"}}, []string{"oidc:root"}, ReadAsAdmin},
		{"grantee", &auth.User{ID: "local:bob"}, []string{"local:carol", "local:bob"}, ReadAsGrantee},
		{"stranger", &auth.User{ID: "local:bob"}, []string{"local:carol"}, ReadDenied},
		{"stranger with no grants", &auth.User{ID: "local:bob"}, nil, ReadDenied},
		{"viewer role is not admin", &auth.User{ID: "local:bob", Roles: []string{"viewer", "operator"}}, nil, ReadDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authorizeReceiptRead(tc.user, rec, tc.grants); got != tc.want {
				t.Fatalf("decision = %v, want %v", got, tc.want)
			}
		})
	}
	if authorizeReceiptRead(&auth.User{ID: "local:alice"}, nil, nil) != ReadDenied {
		t.Fatal("nil receipt must be denied")
	}
}

func TestHandleGet_OwnerSeesOwnReceipt(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local",
		recordedDeployment(0, "web", "uid-web"),
		recordedObj(1, "", "v1", "configmaps", "ConfigMap", "prod", "cfg", "uid-cfg"))

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)

	if d.Access != "owner" || d.OperationID != rec.ID.String() || d.ReceiptURL != "/v1/changes/"+rec.ID.String() {
		t.Fatalf("envelope = %+v", d.ReceiptView)
	}
	if d.TargetGenerationChanged {
		t.Fatal("local receipt must not report a generation change")
	}
	if len(d.Objects) != 2 || d.RedactedObjects != 0 {
		t.Fatalf("objects = %+v (redacted %d)", d.Objects, d.RedactedObjects)
	}
	web := d.Objects[0]
	if web.Redacted || web.Kind != "Deployment" || web.Name != "web" || web.Namespace != "prod" ||
		web.UID != "uid-web" || web.Resource != "deployments" || web.Group != "apps" || web.Action != ActionConfigured {
		t.Fatalf("object 0 = %+v", web)
	}
	if d.Summary != (ReceiptSummary{Total: 2, Configured: 2}) {
		t.Fatalf("summary = %+v", d.Summary)
	}
	if d.Checks == nil || d.Ownership == nil {
		t.Fatalf("checks/ownership must be arrays, not null: %s", w.Body.String())
	}
	if hs.reader.grantCalls != 0 {
		t.Fatal("owner read must not consult grants")
	}
	// Re-authorization went to the receipt's cluster for both tuples.
	want := []accessCall{{"local", "apps", "deployments", "prod"}, {"local", "", "configmaps", "prod"}}
	if fmt.Sprint(hs.access.calls) != fmt.Sprint(want) {
		t.Fatalf("access calls = %v, want %v", hs.access.calls, want)
	}
}

func TestHandleGet_OtherUserGets404(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))

	w := hs.get(t, otherUser, rec.ID)
	expectError(t, w, http.StatusNotFound, "change receipt not found")
	if strings.Contains(w.Body.String(), "web") || strings.Contains(w.Body.String(), "alice") {
		t.Fatalf("404 body discloses receipt content: %s", w.Body.String())
	}
	if hs.reader.grantCalls != 1 {
		t.Fatalf("grants consulted %d times, want 1", hs.reader.grantCalls)
	}
	// A missing receipt is indistinguishable from a forbidden one.
	missing := hs.get(t, otherUser, uuid.New())
	if missing.Code != http.StatusNotFound || missing.Body.String() != w.Body.String() {
		t.Fatalf("missing receipt = %d %s; forbidden receipt = %s", missing.Code, missing.Body.String(), w.Body.String())
	}
	if len(hs.access.calls) != 0 {
		t.Fatal("a denied envelope must not trigger per-object access checks")
	}
}

func TestHandleGet_GranteeCanRead(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{"local:carol", otherUser.ID}

	d := decodeDetail(t, hs.get(t, otherUser, rec.ID))
	if d.Access != "grantee" || len(d.Objects) != 1 || d.Objects[0].Redacted {
		t.Fatalf("grantee view = %+v", d)
	}
	if d.OwnerUsername != "alice" {
		t.Fatalf("ownerUsername = %q", d.OwnerUsername)
	}
}

func TestHandleGet_AdminCanRead(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))

	d := decodeDetail(t, hs.get(t, adminUser, rec.ID))
	if d.Access != "admin" || len(d.Objects) != 1 || d.Objects[0].Redacted {
		t.Fatalf("admin view = %+v", d)
	}
	if hs.reader.grantCalls != 0 {
		t.Fatal("admin read must not consult grants")
	}
}

func TestHandleGet_GrantLookupFailureIsNot404(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	hs.reader.failGrants = errors.New("pg down")

	w := hs.get(t, otherUser, rec.ID)
	expectError(t, w, http.StatusServiceUnavailable, "change receipt store unavailable")
	if decodeEnvelope(t, w).Error.Reason != ReasonReceiptStoreUnavailable {
		t.Fatalf("reason = %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Per-object redaction
// ---------------------------------------------------------------------------

func TestHandleGet_RevokedNamespaceAccessRedactsObject(t *testing.T) {
	hs := newHarness(t)
	hs.access.allow = func(_, _, ns string) bool { return ns != "prod" }
	web := recordedDeployment(0, "web", "uid-web")
	other := recordedDeployment(1, "api", "uid-api")
	other.Namespace = "staging"
	rec := hs.seed(testUser, "local", web, other)

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.Access != "owner" {
		t.Fatalf("the envelope stays visible to the owner: %+v", d.ReceiptView)
	}
	if d.RedactedObjects != 1 || len(d.Objects) != 2 {
		t.Fatalf("redactedObjects = %d, objects = %+v", d.RedactedObjects, d.Objects)
	}
	// The hidden object collapses to exactly {index, redacted, reason}.
	objects, _ := rawDetail(t, w)
	hidden := string(objects[0])
	if hidden != `{"index":0,"redacted":true,"reason":"forbidden"}` {
		t.Fatalf("redacted object = %s", hidden)
	}
	if d.Objects[1].Redacted || d.Objects[1].Name != "api" {
		t.Fatalf("visible object = %+v", d.Objects[1])
	}
	for _, leak := range []string{"web", "uid-web", `"prod"`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("response leaks %q: %s", leak, w.Body.String())
		}
	}
	if d.Summary != (ReceiptSummary{Total: 2, Configured: 2}) {
		t.Fatalf("summary must stay the historical record: %+v", d.Summary)
	}
}

func TestHandleGet_SecretBearingReceiptFiltersErrorText(t *testing.T) {
	hs := newHarness(t)
	sanitized := failedObj(0, "prod", "web", sanitizedError(ErrorClassInvalid))
	// A row the store did NOT sanitize (it should never exist); redaction
	// must not depend on it having been.
	unsanitized := failedObj(1, "prod", "api", `admission webhook denied: data.password "hunter2" is too short`)
	ok := recordedDeployment(2, "worker", "uid-worker")
	rec := hs.seed(testUser, "local", sanitized, unsanitized, ok)
	rec.ContainsSecret = true
	hs.reader.put(*rec)

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if !d.ContainsSecret {
		t.Fatal("containsSecret must be reported")
	}
	if d.Objects[0].ErrorClass != ErrorClassInvalid || d.Objects[0].Error != "" {
		t.Fatalf("sanitized object = %+v", d.Objects[0])
	}
	if d.Objects[1].ErrorClass != ErrorClassOther || d.Objects[1].Error != "" {
		t.Fatalf("unsanitized object = %+v", d.Objects[1])
	}
	if strings.Contains(w.Body.String(), "hunter2") || strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("Secret-bearing receipt emitted error text: %s", w.Body.String())
	}
	if d.Summary != (ReceiptSummary{Total: 3, Configured: 1, Failed: 2}) {
		t.Fatalf("summary = %+v", d.Summary)
	}
}

func TestErrorClassOf_RoundTripsEveryClass(t *testing.T) {
	// Every class the service can record, restated by value so a constant
	// added to types.go without a matching allowlist entry fails here.
	classes := []string{"conflict", "forbidden", "invalid", "not_found", "indeterminate", "other"}
	for _, c := range classes {
		if got := errorClassOf(c, ""); got != c {
			t.Fatalf("stored class %q -> %q", c, got)
		}
		if got := errorClassOf("", sanitizedError(c)); got != c {
			t.Fatalf("sanitized text for %q -> %q", c, got)
		}
		if got := errorClassOf(c, "raw admission text"); got != c {
			t.Fatalf("stored class must win over text: %q -> %q", c, got)
		}
	}
	for _, want := range []string{ErrorClassConflict, ErrorClassForbidden, ErrorClassInvalid, ErrorClassNotFound, ErrorClassIndeterminate, ErrorClassOther} {
		if !isErrorClass(want) {
			t.Fatalf("constant %q is not in the allowlist", want)
		}
	}
	for _, bad := range []struct{ stored, text string }{
		{"", ""}, {"", "no class here"}, {"bogus", ""}, {"bogus", "conflict: x"}, {"", "Conflict: capitalised"}, {"", "conflict"},
	} {
		if bad.stored == "bogus" && bad.text == "conflict: x" {
			if got := errorClassOf(bad.stored, bad.text); got != "conflict" {
				t.Fatalf("unknown stored class must fall back to the text: %q", got)
			}
			continue
		}
		if got := errorClassOf(bad.stored, bad.text); got != "" {
			t.Fatalf("errorClassOf(%q, %q) = %q, want unclassified", bad.stored, bad.text, got)
		}
	}
}

func TestHandleGet_StoredErrorClassPreferredOverText(t *testing.T) {
	hs := newHarness(t)
	o := failedObj(0, "prod", "web", `admission webhook denied: data.password "hunter2"`)
	o.ErrorClass = ErrorClassConflict
	rec := hs.seed(testUser, "local", o)
	rec.ContainsSecret = true
	hs.reader.put(*rec)

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.Objects[0].ErrorClass != ErrorClassConflict || d.Objects[0].Error != "" || strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("object = %+v\n%s", d.Objects[0], w.Body.String())
	}
}

func TestHandleGet_NonSecretReceiptKeepsErrorText(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", failedObj(0, "prod", "web", "Deployment.apps \"web\" is invalid: spec.replicas: must be >= 0"))

	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if d.Objects[0].Error == "" || d.Objects[0].ErrorClass != "" {
		t.Fatalf("non-secret failed object = %+v", d.Objects[0])
	}
}

func TestHandleGet_SecretObjectRedactedWithoutSecretGet(t *testing.T) {
	hs := newHarness(t)
	// The caller may get everything except secrets in prod.
	hs.access.allow = func(_, resource, ns string) bool { return !(resource == "secrets" && ns == "prod") }
	rec := hs.seed(testUser, "local",
		secretObj(0, "prod", "db-creds"),
		secretObj(1, "staging", "db-creds"),
		recordedDeployment(2, "web", "uid-web"))
	rec.ContainsSecret = true
	hs.reader.put(*rec)

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.RedactedObjects != 1 {
		t.Fatalf("redactedObjects = %d: %+v", d.RedactedObjects, d.Objects)
	}
	if !d.Objects[0].Redacted || d.Objects[0].Reason != RedactionSecretFiltered || d.Objects[0].Name != "" {
		t.Fatalf("prod Secret = %+v", d.Objects[0])
	}
	if d.Objects[1].Redacted || d.Objects[1].Name != "db-creds" {
		t.Fatalf("staging Secret = %+v", d.Objects[1])
	}
	if d.Objects[2].Redacted {
		t.Fatalf("Deployment = %+v", d.Objects[2])
	}
	if strings.Contains(w.Body.String(), "uid-secret-db-creds") && strings.Count(w.Body.String(), "uid-secret-db-creds") != 1 {
		t.Fatalf("hidden Secret uid leaked: %s", w.Body.String())
	}
}

func TestHandleGet_ObjectWithoutRecordedResourceIsHidden(t *testing.T) {
	hs := newHarness(t)
	unmapped := failedObj(0, "prod", "thing", "no matches for kind Widget")
	unmapped.Kind, unmapped.Group, unmapped.Version, unmapped.Resource = "Widget", "example.io", "", ""
	rec := hs.seed(testUser, "local", unmapped)

	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if !d.Objects[0].Redacted || d.Objects[0].Reason != RedactionForbidden {
		t.Fatalf("object without a resource must fail closed: %+v", d.Objects[0])
	}
	if len(hs.access.calls) != 0 {
		t.Fatalf("no SAR can be issued for an empty resource: %v", hs.access.calls)
	}
}

func TestHandleGet_RedactedCountReportedAndSummaryPreserved(t *testing.T) {
	hs := newHarness(t)
	hs.access.allow = func(string, string, string) bool { return false }
	objs := []store.ReceiptObject{
		recordedDeployment(0, "web", "uid-web"),
		failedObj(1, "prod", "api", "boom"),
		recordedDeployment(2, "worker", "uid-worker"),
	}
	objs[2].Action = ActionCreated
	rec := hs.seed(testUser, "local", objs...)
	rec.DocumentCount = 4 // one document was never recorded
	hs.reader.put(*rec)

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.RedactedObjects != 3 || len(d.Objects) != 3 {
		t.Fatalf("redactedObjects = %d, objects = %+v", d.RedactedObjects, d.Objects)
	}
	for i, o := range d.Objects {
		if !o.Redacted || o.Index != i {
			t.Fatalf("object %d = %+v", i, o)
		}
	}
	if d.Summary != (ReceiptSummary{Total: 4, Created: 1, Configured: 1, Failed: 1, NotRecorded: 1}) {
		t.Fatalf("summary = %+v", d.Summary)
	}
	if d.RecordedThrough != 3 || d.DocumentCount != 4 {
		t.Fatalf("envelope counts = %d/%d", d.RecordedThrough, d.DocumentCount)
	}
	if strings.Contains(w.Body.String(), "boom") {
		t.Fatal("redacted object leaked its error")
	}
}

func TestHandleGet_AccessCheckErrorFailsClosed(t *testing.T) {
	hs := newHarness(t)
	hs.access.err = errors.New("SAR endpoint unreachable")
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"), recordedDeployment(1, "api", "uid-api"))

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.RedactedObjects != 2 {
		t.Fatalf("an access-check error must redact, not allow: %+v", d.Objects)
	}
	if strings.Contains(w.Body.String(), "uid-web") {
		t.Fatal("object leaked past an erroring access check")
	}
	// The same failure through the real AccessChecker type the production
	// constructor accepts.
	h := NewHandler(hs.svc, nil, nil, nil, resources.NewErroringAccessChecker(errors.New("down")), nil)
	h.receipts = hs.reader
	w = hs.do(t, h.HandleGet, http.MethodGet, request{user: testUser, id: rec.ID.String()})
	if d := decodeDetail(t, w); d.RedactedObjects != 2 {
		t.Fatalf("erroring AccessChecker must redact: %+v", d.Objects)
	}
	// Memoized: both objects share (apps, deployments, prod), so the erroring
	// check ran once for the whole request.
	if len(hs.access.calls) != 1 {
		t.Fatalf("access calls = %v, want one for the shared tuple", hs.access.calls)
	}
}

func TestHandleGet_AccessCheckErrorShortCircuitsAndLogsOnce(t *testing.T) {
	hs := newHarness(t)
	var logs bytes.Buffer
	hs.h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	hs.access.err = errors.New("remote SAR endpoint unreachable")
	// Three distinct tuples: deployments/prod, configmaps/prod, secrets/staging.
	rec := hs.seed(testUser, "local",
		recordedDeployment(0, "web", "uid-web"),
		recordedObj(1, "", "v1", "configmaps", "ConfigMap", "prod", "cfg", "uid-cfg"),
		secretObj(2, "staging", "s"))

	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if d.RedactedObjects != 3 {
		t.Fatalf("every object must be hidden: %+v", d.Objects)
	}
	if len(hs.access.calls) != 1 {
		t.Fatalf("after one failed check no further tuple may dial the checker: %v", hs.access.calls)
	}
	if n := strings.Count(logs.String(), "access check failed"); n != 1 {
		t.Fatalf("access-check failure logged %d times, want once:\n%s", n, logs.String())
	}
}

func TestHandleGet_NoAccessCheckerRedactsEverything(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	h := NewHandler(hs.svc, nil, nil, nil, nil, nil)
	h.receipts = hs.reader

	d := decodeDetail(t, hs.do(t, h.HandleGet, http.MethodGet, request{user: testUser, id: rec.ID.String()}))
	if d.RedactedObjects != 1 {
		t.Fatalf("no authorization source must mean no disclosure: %+v", d.Objects)
	}
}

func TestHandleGet_AccessChecksMemoizedPerTuple(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local",
		recordedDeployment(0, "web", "uid-web"),
		recordedDeployment(1, "api", "uid-api"),
		secretObj(2, "prod", "a"),
		secretObj(3, "prod", "b"))

	decodeDetail(t, hs.get(t, testUser, rec.ID))
	want := []accessCall{{"local", "apps", "deployments", "prod"}, {"local", "", "secrets", "prod"}}
	if fmt.Sprint(hs.access.calls) != fmt.Sprint(want) {
		t.Fatalf("access calls = %v, want %v", hs.access.calls, want)
	}
}

// ---------------------------------------------------------------------------
// Stored verification and ownership on GET
// ---------------------------------------------------------------------------

func storedCheck(rec *store.ChangeReceipt, status CheckStatus, reason string, o store.ReceiptObject, evidence map[string]string) CheckResult {
	src := SourceRef{ClusterID: rec.ClusterID, Group: o.Group, Version: o.Version, Resource: o.Resource,
		Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID}
	c := newCheck(src, fixedNow, status, reason, "warning", "message about "+o.Name, "detail "+o.Namespace)
	c.Evidence = evidence
	return c
}

func withVerification(hs *harness, rec *store.ChangeReceipt, state store.VerificationState, checks ...CheckResult) {
	payload, _ := json.Marshal(checks)
	rec.VerificationState = state
	rec.Verification = payload
	if state.IsFinal() {
		rec.VerifiedAt = &fixedNow
	}
	hs.reader.put(*rec)
}

func TestHandleGet_DeletedRecreatedTargetStillShowsStoredUID(t *testing.T) {
	hs := newHarness(t)
	web := recordedDeployment(0, "web", "uid-old")
	rec := hs.seed(testUser, "local", web)
	withVerification(hs, rec, store.VerifyInconclusive,
		storedCheck(rec, CheckInconclusive, ReasonTargetRecreated, web, map[string]string{"Deployment/web": "uid-new"}))

	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if d.Objects[0].UID != "uid-old" {
		t.Fatalf("the receipt must show the UID it acted on, got %q", d.Objects[0].UID)
	}
	if len(d.Checks) != 1 || d.Checks[0].Redacted || d.Checks[0].Reason != ReasonTargetRecreated {
		t.Fatalf("checks = %+v", d.Checks)
	}
	if d.Checks[0].Evidence["Deployment/web"] != "uid-new" || d.Checks[0].Source == nil || d.Checks[0].Source.UID != "uid-old" {
		t.Fatalf("verification, not the receipt, reports the mismatch: %+v", d.Checks[0])
	}
	if d.Verification.State != store.VerifyInconclusive || d.Verification.URL != "/v1/changes/"+rec.ID.String()+"/verification" {
		t.Fatalf("verification link = %+v", d.Verification)
	}
}

func TestHandleGet_SecretSourceRefInChecksIsFiltered(t *testing.T) {
	hs := newHarness(t)
	hs.access.allow = func(_, resource, _ string) bool { return resource != "secrets" }
	sec := secretObj(0, "prod", "db-creds")
	web := recordedDeployment(1, "web", "uid-web")
	rec := hs.seed(testUser, "local", sec, web)
	rec.ContainsSecret = true
	withVerification(hs, rec, store.VerifyInconclusive,
		storedCheck(rec, CheckInconclusive, ReasonKindNotSupported, sec, nil),
		storedCheck(rec, CheckPass, ReasonRolloutComplete, web, map[string]string{"Deployment/web": "uid-web"}))

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.RedactedChecks != 1 || !d.Checks[0].Redacted || d.Checks[0].RedactionReason != RedactionSecretFiltered || d.Checks[0].Source != nil {
		t.Fatalf("Secret check = %+v", d.Checks[0])
	}
	if d.Checks[1].Redacted {
		t.Fatalf("Deployment check = %+v", d.Checks[1])
	}
	if strings.Contains(w.Body.String(), "db-creds") {
		t.Fatalf("Secret name leaked through a check: %s", w.Body.String())
	}
}

func TestHandleGet_ChecksRedactedWithObject(t *testing.T) {
	hs := newHarness(t)
	hs.access.allow = func(_, _, ns string) bool { return ns != "prod" }
	web := recordedDeployment(0, "web", "uid-web")
	api := recordedDeployment(1, "api", "uid-api")
	api.Namespace = "staging"
	rec := hs.seed(testUser, "local", web, api)
	withVerification(hs, rec, store.VerifyVerified,
		storedCheck(rec, CheckPass, ReasonRolloutComplete, web, map[string]string{"Deployment/web": "uid-web"}),
		storedCheck(rec, CheckPass, ReasonRolloutComplete, api, map[string]string{"Deployment/api": "uid-api"}))

	w := hs.get(t, testUser, rec.ID)
	d := decodeDetail(t, w)
	if d.RedactedChecks != 1 || len(d.Checks) != 2 {
		t.Fatalf("redactedChecks = %d, checks = %+v", d.RedactedChecks, d.Checks)
	}
	_, checks := rawDetail(t, w)
	hidden := string(checks[0])
	want := fmt.Sprintf(`{"checkId":%q,"status":"pass","severity":"warning","reason":"ok","redacted":true,"redactionReason":"forbidden","observedAt":%q}`,
		CheckIDRolloutComplete, fixedNow.Format(time.RFC3339Nano))
	if hidden != want {
		t.Fatalf("redacted check = %s\nwant %s", hidden, want)
	}
	if d.Checks[1].Redacted || d.Checks[1].Source == nil || d.Checks[1].Source.Name != "api" {
		t.Fatalf("visible check = %+v", d.Checks[1])
	}
	for _, leak := range []string{"web", "uid-web", "message about web", "detail prod", `"prod"`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("redacted check leaked %q: %s", leak, w.Body.String())
		}
	}
}

func storedOwnership(t *testing.T, rec *store.ChangeReceipt, results ...gitops.OwnershipResult) {
	t.Helper()
	payload, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	rec.Ownership = payload
}

func argoOwned(o store.ReceiptObject, repo string) gitops.OwnershipResult {
	return gitops.OwnershipResult{
		Object:     gitops.ObjectRef{Group: o.Group, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name},
		Controller: gitops.OwnedByArgoCD, Confidence: gitops.ConfidenceConfirmed, Reason: "confirmed-argo-status",
		Apps: []gitops.OwnedByApp{{AppID: "argo:argocd:" + o.Name, Tool: gitops.ToolArgoCD, Kind: "Application",
			Namespace: "argocd", Name: o.Name, Source: gitops.AppSource{RepoURL: repo}}},
		Evidence: []gitops.OwnershipEvidence{
			{Kind: gitops.EvidenceArgoStatusResource, Tool: gitops.ToolArgoCD, AppID: "argo:argocd:" + o.Name},
			{Kind: gitops.EvidenceInstanceLabel, Tool: gitops.ToolArgoCD, RawValue: o.Name},
		},
		IdentityBasis: "group-kind-namespace-name",
	}
}

func TestHandleGet_OwnershipFollowsObjectAndAppAccess(t *testing.T) {
	hs := newHarness(t)
	web := recordedDeployment(0, "web", "uid-web")
	api := recordedDeployment(1, "api", "uid-api")
	api.Namespace = "staging"
	rec := hs.seed(testUser, "local", web, api)
	storedOwnership(t, rec, argoOwned(web, "https://git.example/web"), argoOwned(api, "https://git.example/api"))
	hs.reader.put(*rec)

	// Owner with full access: everything is returned.
	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if len(d.Ownership) != 2 || d.RedactedOwnership != 0 || len(d.Ownership[0].Apps) != 1 || len(d.Ownership[0].Evidence) != 2 {
		t.Fatalf("full ownership = %+v", d.Ownership)
	}

	// Grantee who lost prod and may not get Argo Applications: the prod
	// entry is gone with its object; the staging entry keeps the verdict but
	// loses the application (and its repo URL) and the evidence naming it.
	hs.reader.grants[rec.ID] = []string{otherUser.ID}
	hs.access.allow = func(group, resource, ns string) bool {
		return ns != "prod" && resource != gitops.ArgoApplicationGVR.Resource
	}
	w := hs.get(t, otherUser, rec.ID)
	d = decodeDetail(t, w)
	if d.RedactedOwnership != 1 || len(d.Ownership) != 1 {
		t.Fatalf("ownership = %+v (redacted %d)", d.Ownership, d.RedactedOwnership)
	}
	got := d.Ownership[0]
	if got.Object.Name != "api" || got.Controller != gitops.OwnedByNone || got.Confidence != gitops.ConfidenceForbidden || got.Reason != OwnershipReasonAppsRedacted {
		t.Fatalf("an entry with every app hidden must not say which controller claims it: %+v", got)
	}
	if len(got.Apps) != 0 || got.RedactedApps != 1 {
		t.Fatalf("apps must be re-authorized: %+v", got)
	}
	if strings.Contains(w.Body.String(), `"controller":"argocd"`) || strings.Contains(w.Body.String(), "confirmed") {
		t.Fatalf("controller claim leaked: %s", w.Body.String())
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Kind != gitops.EvidenceInstanceLabel {
		t.Fatalf("evidence naming a hidden app must go: %+v", got.Evidence)
	}
	if strings.Contains(w.Body.String(), "git.example") || strings.Contains(w.Body.String(), "argo:argocd") {
		t.Fatalf("repo coordinates leaked: %s", w.Body.String())
	}
	if fmt.Sprint(hs.access.calls[len(hs.access.calls)-1]) != fmt.Sprint(accessCall{"local", "argoproj.io", "applications", "argocd"}) {
		t.Fatalf("application check = %v", hs.access.calls)
	}
}

func TestRedactOwnership_ReauthorizesEveryAppKind(t *testing.T) {
	web := recordedDeployment(0, "web", "uid-web")
	objs := []store.ReceiptObject{web}
	visible, _ := redactObjects(objs, false, allowAll)
	app := func(kind, ns, repo string) gitops.OwnedByApp {
		tool := gitops.ToolFluxCD
		if kind == "Application" {
			tool = gitops.ToolArgoCD
		}
		return gitops.OwnedByApp{AppID: "x:" + ns + ":" + kind, Tool: tool, Kind: kind, Namespace: ns, Name: "n",
			Source: gitops.AppSource{RepoURL: repo}}
	}
	stored := []gitops.OwnershipResult{{
		Object:     gitops.ObjectRef{Group: web.Group, Kind: web.Kind, Namespace: web.Namespace, Name: web.Name},
		Controller: gitops.OwnedByBoth, Confidence: gitops.ConfidenceConflicting, Reason: "both-claim",
		Apps: []gitops.OwnedByApp{
			app("Application", "argocd", "repo://argo"),
			app("Kustomization", "flux-system", "repo://ks"),
			app("HelmRelease", "flux-system", "repo://hr"),
			app("Widget", "flux-system", "repo://unknown-kind"),
		},
		Evidence: []gitops.OwnershipEvidence{
			{Kind: gitops.EvidenceArgoStatusResource, AppID: "x:argocd:Application"},
			{Kind: gitops.EvidenceFluxInventoryEntry, AppID: "x:flux-system:Kustomization"},
			{Kind: gitops.EvidenceFluxOwnerLabel, AppID: "x:flux-system:HelmRelease"},
			{Kind: gitops.EvidenceFluxOwnerLabel, AppID: "x:flux-system:Widget"},
			{Kind: gitops.EvidenceInstanceLabel, RawValue: "web"},
		},
	}}
	const (
		argoApps = "argoproj.io/applications"
		fluxKS   = "kustomize.toolkit.fluxcd.io/kustomizations"
		fluxHR   = "helm.toolkit.fluxcd.io/helmreleases"
	)
	type verdict struct {
		ctl    gitops.OwnershipController
		conf   gitops.OwnershipConfidence
		reason string
	}
	both := verdict{gitops.OwnedByBoth, gitops.ConfidenceConflicting, "both-claim"}
	cases := []struct {
		name     string
		deny     map[string]bool // "group/resource" tuples the caller may not get
		wantApps []string        // repo URLs that survive
		wantEv   int
		want     verdict
	}{
		{"all allowed", nil, []string{"repo://argo", "repo://ks", "repo://hr"}, 4, both},
		{"no kustomizations", map[string]bool{fluxKS: true}, []string{"repo://argo", "repo://hr"}, 3, both},
		{"no helmreleases", map[string]bool{fluxHR: true}, []string{"repo://argo", "repo://ks"}, 3, both},
		// Hiding the only Argo app must not leave "both": the caller may not
		// learn that Argo claims the object.
		{"no applications", map[string]bool{argoApps: true}, []string{"repo://ks", "repo://hr"}, 3,
			verdict{gitops.OwnedByFluxCD, gitops.ConfidenceConfirmed, "confirmed-flux-inventory"}},
		{"only argo", map[string]bool{fluxKS: true, fluxHR: true}, []string{"repo://argo"}, 2,
			verdict{gitops.OwnedByArgoCD, gitops.ConfidenceConfirmed, "confirmed-argo-status"}},
		{"nothing", map[string]bool{argoApps: true, fluxKS: true, fluxHR: true}, nil, 1,
			verdict{gitops.OwnedByNone, gitops.ConfidenceForbidden, OwnershipReasonAppsRedacted}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var asked []accessCall
			allowed := func(group, resource, ns string) bool {
				asked = append(asked, accessCall{"", group, resource, ns})
				return !tc.deny[group+"/"+resource]
			}
			views, redacted := redactOwnership(stored, objs, visible, allowed)
			if redacted != 0 || len(views) != 1 {
				t.Fatalf("views = %+v (redacted %d)", views, redacted)
			}
			v := views[0]
			var repos []string
			for _, a := range v.Apps {
				repos = append(repos, a.Source.RepoURL)
			}
			if fmt.Sprint(repos) != fmt.Sprint(tc.wantApps) {
				t.Fatalf("apps = %v, want %v", repos, tc.wantApps)
			}
			// The unknown kind is always removed and never asked about.
			if v.RedactedApps != 4-len(tc.wantApps) {
				t.Fatalf("redactedApps = %d", v.RedactedApps)
			}
			for _, a := range asked {
				if a.resource == "" || a.namespace == "" {
					t.Fatalf("an unknown app kind must fail closed without a SAR: %+v", a)
				}
			}
			if len(v.Evidence) != tc.wantEv {
				t.Fatalf("evidence = %+v, want %d entries", v.Evidence, tc.wantEv)
			}
			raw, _ := json.Marshal(v)
			if strings.Contains(string(raw), "unknown-kind") || strings.Contains(string(raw), "Widget") {
				t.Fatalf("unknown app kind leaked: %s", raw)
			}
			if got := (verdict{v.Controller, v.Confidence, v.Reason}); got != tc.want {
				t.Fatalf("verdict = %+v, want %+v", got, tc.want)
			}
			if tc.want.ctl != gitops.OwnedByBoth && strings.Contains(string(raw), "both") {
				t.Fatalf("hidden tool leaked through the verdict: %s", raw)
			}
		})
	}
	// An entry that never had a confirming app (hints only) is not collapsed.
	hintsOnly := []gitops.OwnershipResult{{
		Object:     gitops.ObjectRef{Group: web.Group, Kind: web.Kind, Namespace: web.Namespace, Name: web.Name},
		Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceUnknown, Reason: "hints-only",
		Evidence: []gitops.OwnershipEvidence{{Kind: gitops.EvidenceInstanceLabel, RawValue: "web"}},
	}}
	views, _ := redactOwnership(hintsOnly, objs, visible, func(string, string, string) bool { return false })
	if len(views) != 1 || views[0].Reason != "hints-only" || views[0].RedactedApps != 0 || len(views[0].Evidence) != 1 {
		t.Fatalf("hints-only entry = %+v", views)
	}
}

func TestHandleGet_SecretReceiptDigestOnlyForOwner(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", secretObj(0, "prod", "db-creds"))
	rec.ContainsSecret = true
	hs.reader.put(*rec)
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	if d := decodeDetail(t, hs.get(t, testUser, rec.ID)); d.ContentDigest != rec.ContentDigest {
		t.Fatalf("owner digest = %q", d.ContentDigest)
	}
	for _, u := range []*auth.User{otherUser, adminUser} {
		w := hs.get(t, u, rec.ID)
		if d := decodeDetail(t, w); d.ContentDigest != "" {
			t.Fatalf("%s sees the digest of a Secret-bearing bundle: %q", u.Username, d.ContentDigest)
		}
		if strings.Contains(w.Body.String(), "sha256:") {
			t.Fatalf("digest leaked: %s", w.Body.String())
		}
	}
	// A non-secret receipt keeps its digest for every reader.
	plain := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	if d := decodeDetail(t, hs.get(t, adminUser, plain.ID)); d.ContentDigest != plain.ContentDigest {
		t.Fatalf("non-secret digest = %q", d.ContentDigest)
	}
}

func TestHandleGet_UndecodableStoredOwnershipIsEmpty(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	rec.Ownership = json.RawMessage(`{"not":"an array"}`)
	rec.Verification = json.RawMessage(`"garbage"`)
	hs.reader.put(*rec)

	d := decodeDetail(t, hs.get(t, testUser, rec.ID))
	if len(d.Ownership) != 0 || len(d.Checks) != 0 || d.RedactedOwnership != 0 {
		t.Fatalf("undecodable JSON must read as empty: %+v", d)
	}
}

// ---------------------------------------------------------------------------
// Remote receipts: the receipt's cluster, never the header's
// ---------------------------------------------------------------------------

func TestHandleGet_RemoteReceiptReauthorizedOnItsOwnCluster(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(adminUser, "remote-1", recordedDeployment(0, "web", "uid-web"))

	// Header says local; the receipt says remote-1. Admin owner.
	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: adminUser, cluster: "local", id: rec.ID.String()})
	d := decodeDetail(t, w)
	if len(hs.access.calls) != 1 || hs.access.calls[0].cluster != "remote-1" {
		t.Fatalf("access checks must target the receipt's cluster: %v", hs.access.calls)
	}
	if d.Objects[0].Redacted {
		t.Fatalf("admin owner sees the object: %+v", d.Objects[0])
	}
	if !d.TargetGenerationChanged || d.ClusterGeneration != "gen-then" {
		t.Fatalf("generation gen-then vs gen-now must flag a change: %+v", d.ReceiptView)
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("generation lookup went to %v", calls)
	}
}

func TestHandleGet_StalledRemoteIsBoundedAndFailsClosed(t *testing.T) {
	hs := newHarness(t)
	hs.h.clusterTimeout = 20 * time.Millisecond
	hs.targeter.delay = 5 * time.Second // generation lookup stalls
	hs.access.delay = 5 * time.Second   // and so do the SARs
	rec := hs.seed(adminUser, "remote-1",
		recordedDeployment(0, "web", "uid-web"), recordedObj(1, "", "v1", "configmaps", "ConfigMap", "prod", "cfg", "uid-cfg"))

	start := time.Now()
	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: adminUser, cluster: "remote-1", id: rec.ID.String()})
	if time.Since(start) > 2*time.Second {
		t.Fatal("a stalled remote cluster was not bounded by the budget")
	}
	d := decodeDetail(t, w)
	if d.RedactedObjects != 2 || d.TargetGenerationChanged {
		t.Fatalf("stalled SARs must redact and a stalled generation lookup must not flag: %+v", d)
	}
	if len(hs.access.calls) != 1 {
		t.Fatalf("after the deadline the memo must stop dialing: %v", hs.access.calls)
	}
}

func TestHandleGet_NonAdminOwnerOfRemoteReceiptSeesEnvelopeOnly(t *testing.T) {
	hs := newHarness(t)
	// alice was admin when she applied; she is not any more.
	rec := hs.seed(testUser, "remote-1", recordedDeployment(0, "web", "uid-web"))

	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: testUser, cluster: "remote-1", id: rec.ID.String()})
	d := decodeDetail(t, w)
	if d.Access != "owner" || d.RedactedObjects != 1 {
		t.Fatalf("non-admin remote read = %+v", d)
	}
	if len(hs.access.calls) != 0 || len(hs.targeter.calls()) != 0 {
		t.Fatalf("no remote cluster call may be made for a non-admin: access=%v target=%v", hs.access.calls, hs.targeter.calls())
	}
	if d.TargetGenerationChanged {
		t.Fatal("unknown current generation must not read as changed")
	}
}

// ---------------------------------------------------------------------------
// Input validation and availability
// ---------------------------------------------------------------------------

func TestHandleGet_NoDatabaseReturns503(t *testing.T) {
	hs := newHarness(t)
	id := uuid.New().String()
	for _, h := range []*Handler{
		NewHandler(NewService(nil, nil), nil, nil, nil, resources.NewAlwaysAllowAccessChecker(), nil),
		NewHandler(nil, nil, nil, nil, nil, nil),
		newHandlerWith(hs.svc, nil, hs.resolver, hs.targeter, hs.access, nil),
	} {
		for name, fn := range map[string]http.HandlerFunc{
			"list": h.HandleList, "get": h.HandleGet, "verification": h.HandleVerification, "ownership": h.HandleResolveOwnership,
		} {
			w := hs.do(t, fn, http.MethodGet, request{user: testUser, id: id, body: `{"objects":[{"kind":"Deployment","name":"web"}]}`})
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "change receipts require a database") {
				t.Fatalf("%s without a database = %d %s", name, w.Code, w.Body.String())
			}
		}
	}
}

func TestHandleGet_InvalidUUIDReturns400(t *testing.T) {
	hs := newHarness(t)
	hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	v1, err := uuid.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not-a-uuid", v1.String(), uuid.Nil.String(), "00000000-0000-7000-8000-000000000000"} {
		for _, fn := range []http.HandlerFunc{hs.h.HandleGet, hs.h.HandleVerification} {
			w := hs.do(t, fn, http.MethodGet, request{user: testUser, id: bad})
			expectError(t, w, http.StatusBadRequest, "invalid receipt id")
		}
	}
	for _, c := range hs.reader.calls {
		if c == "get" {
			t.Fatal("an invalid id must be rejected before any store call")
		}
	}
}

func TestHandleGet_StoreReadFailureIs503(t *testing.T) {
	hs := newHarness(t)
	hs.reader.failGet = errors.New("pg down")
	w := hs.get(t, testUser, uuid.New())
	expectError(t, w, http.StatusServiceUnavailable, "change receipt store unavailable")
	if strings.Contains(w.Body.String(), "pg down") {
		t.Fatal("internal error text leaked")
	}
}

// ---------------------------------------------------------------------------
// Ownership resolution
// ---------------------------------------------------------------------------

func ownershipBody(n int) string {
	refs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		refs = append(refs, fmt.Sprintf(`{"group":"apps","version":"v1","kind":"Deployment","namespace":"prod","name":"web-%d"}`, i))
	}
	return `{"objects":[` + strings.Join(refs, ",") + `]}`
}

func (hs *harness) ownership(t *testing.T, user *auth.User, cluster, body string) *httptest.ResponseRecorder {
	t.Helper()
	return hs.do(t, hs.h.HandleResolveOwnership, http.MethodPost, request{user: user, cluster: cluster, body: body})
}
