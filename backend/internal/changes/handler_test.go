package changes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	k8stesting "k8s.io/client-go/testing"

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
}

func newFakeReader() *fakeReader {
	return &fakeReader{fakeStore: newFakeStore(), grants: map[uuid.UUID][]string{}}
}

func (f *fakeReader) ListForOwner(_ context.Context, p store.ReceiptQueryParams) ([]store.ChangeReceipt, int, error) {
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
	dyn        dynamic.Interface
	mapper     meta.RESTMapper
	generation string
	err        error
}

func (f *fakeTargeter) TargetFor(_ context.Context, clusterID, _ string, _ []string) (*k8s.ClientPair, *k8s.TargetSchema, error) {
	f.mu.Lock()
	f.asked = append(f.asked, clusterID)
	f.mu.Unlock()
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
	calls []accessCall
}

func allowAll(string, string, string) bool { return true }

func (f *fakeAccess) CanAccessGroupResource(_ context.Context, clusterID, _ string, _ []string, verb, apiGroup, resource, namespace string) (bool, error) {
	if verb != "get" {
		panic("receipt redaction must check get, got " + verb)
	}
	f.calls = append(f.calls, accessCall{clusterID, apiGroup, resource, namespace})
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
	if got.Object.Name != "api" || got.Controller != gitops.OwnedByArgoCD || got.Confidence != gitops.ConfidenceConfirmed {
		t.Fatalf("verdict must survive: %+v", got)
	}
	if len(got.Apps) != 0 || got.RedactedApps != 1 {
		t.Fatalf("apps must be re-authorized: %+v", got)
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
// List
// ---------------------------------------------------------------------------

func TestHandleList_OwnerScopedAndPaginated(t *testing.T) {
	hs := newHarness(t)
	var mine []*store.ChangeReceipt
	for i := 0; i < 3; i++ {
		r := hs.seed(testUser, "local", recordedDeployment(0, fmt.Sprintf("web-%d", i), "uid"))
		created := fixedNow.Add(time.Duration(i) * time.Minute)
		r.CreatedAt = created
		hs.reader.put(*r)
		mine = append(mine, r)
	}
	hs.seed(otherUser, "local", recordedDeployment(0, "bob-web", "uid-bob"))

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=2&pageSize=2"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	e := decodeEnvelope(t, w)
	if e.Metadata == nil || e.Metadata.Total != 3 || e.Metadata.Page != 2 || e.Metadata.PageSize != 2 {
		t.Fatalf("metadata = %+v", e.Metadata)
	}
	var items []map[string]any
	if err := json.Unmarshal(e.Data, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0]["operationId"] != mine[0].ID.String() {
		t.Fatalf("page 2 = %+v, want the oldest receipt %s", items, mine[0].ID)
	}
	for _, k := range []string{"objects", "checks", "ownership", "summary"} {
		if _, ok := items[0][k]; ok {
			t.Fatalf("list items are envelope views only; found %q", k)
		}
	}
	if strings.Contains(w.Body.String(), "bob") {
		t.Fatal("another owner's row leaked into the list")
	}

	// Defaults and clamps come from the store's Normalize.
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=0&pageSize=99999"})
	e = decodeEnvelope(t, w)
	if e.Metadata.Page != 1 || e.Metadata.PageSize != store.ReceiptMaxPageSize {
		t.Fatalf("normalized metadata = %+v", e.Metadata)
	}
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?page=x"})
	expectError(t, w, http.StatusBadRequest, "invalid page")
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser, query: "?pageSize=1.5"})
	expectError(t, w, http.StatusBadRequest, "invalid pageSize")
}

func TestHandleList_NeverReturnsAnotherOwnersRow(t *testing.T) {
	hs := newHarness(t)
	shared := hs.seed(otherUser, "local", recordedDeployment(0, "bob-web", "uid-bob"))
	hs.reader.grants[shared.ID] = []string{testUser.ID}

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser})
	e := decodeEnvelope(t, w)
	if e.Metadata.Total != 0 || string(e.Data) != "[]" {
		t.Fatalf("a granted receipt is reachable by id only, not listed: %s", w.Body.String())
	}
	// And an admin lists only their own, too.
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: adminUser})
	if e := decodeEnvelope(t, w); e.Metadata.Total != 0 {
		t.Fatalf("admin list = %s", w.Body.String())
	}
}

func TestHandleList_EmptyIsAnArrayWithZeroTotal(t *testing.T) {
	hs := newHarness(t)
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: testUser})
	e := decodeEnvelope(t, w)
	if string(e.Data) != "[]" || e.Metadata == nil || e.Metadata.Total != 0 || e.Metadata.Page != 1 || e.Metadata.PageSize != store.ReceiptDefaultPageSize {
		t.Fatalf("empty list = %s", w.Body.String())
	}
}

func TestHandleList_RemoteGenerationResolvedOncePerCluster(t *testing.T) {
	hs := newHarness(t)
	hs.seed(adminUser, "remote-1", recordedDeployment(0, "a", "u"))
	hs.seed(adminUser, "remote-1", recordedDeployment(0, "b", "u"))
	hs.seed(adminUser, "local", recordedDeployment(0, "c", "u"))

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: adminUser})
	var items []ReceiptView
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &items); err != nil {
		t.Fatal(err)
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("generation lookups = %v, want one for remote-1", calls)
	}
	for _, it := range items {
		if (it.ClusterID == "remote-1") != it.TargetGenerationChanged {
			t.Fatalf("item %+v", it)
		}
	}
}

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

func TestHandleVerification_PersistsAndFreezesAfterWindow(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, verificationWindow+time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, testUser, rec.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Reason != ReasonWindowExpired || v.RetryAfterSeconds != 0 {
		t.Fatalf("view = %+v", v)
	}
	if w.Header().Get("Retry-After") != "" {
		t.Fatal("a frozen verdict carries no Retry-After")
	}
	state, checks := persistedState(t, hs.reader.fakeStore, rec.ID)
	if state != store.VerifyInconclusive || len(checks) != 1 || checks[0].Reason != ReasonWindowExpired {
		t.Fatalf("persisted = %s %+v", state, checks)
	}
	if hs.reader.row(t, rec.ID).VerifiedAt == nil {
		t.Fatal("a final verdict stamps verifiedAt")
	}

	// A second poll returns the frozen verdict without touching the cluster.
	before := len(hs.targeter.calls())
	w = hs.verify(t, testUser, rec.ID)
	if w.Code != http.StatusOK || len(hs.targeter.calls()) != before {
		t.Fatalf("frozen verdict re-read the cluster: %d %v", w.Code, hs.targeter.calls())
	}
}

func TestHandleVerification_VerifyingCarriesRetryAfter(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyVerifying || v.RetryAfterSeconds != verifyRetryAfterSeconds || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("view = %+v, Retry-After = %q", v, w.Header().Get("Retry-After"))
	}
	if v.Checks[0].Reason != ReasonRolloutInProgress || v.Checks[0].Source == nil || v.Checks[0].Source.Name != "web" {
		t.Fatalf("check = %+v", v.Checks[0])
	}
}

func TestHandleVerification_ForbiddenObjectIsInconclusive(t *testing.T) {
	hs := newHarness(t)
	dyn, _ := fakeDyn()
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac"))
	})
	hs.targeter.dyn = dyn
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	// Live read forbidden, SAR still allowed: the check is visible and says so.
	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Redacted ||
		v.Checks[0].Status != CheckInconclusive || v.Checks[0].Reason != ReasonReadForbidden {
		t.Fatalf("view = %+v", v)
	}

	// SAR denied as well: the same check is reduced to the stub.
	hs.access.allow = func(string, string, string) bool { return false }
	rec2 := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	w = hs.verify(t, testUser, rec2.ID)
	v = decodeView(t, w)
	if v.RedactedChecks != 1 || !v.Checks[0].Redacted || v.Checks[0].RedactionReason != RedactionForbidden ||
		v.Checks[0].Source != nil || v.Checks[0].Message != "" {
		t.Fatalf("redacted check = %+v", v.Checks[0])
	}
	if strings.Contains(w.Body.String(), "web") {
		t.Fatalf("redacted check leaked the object: %s", w.Body.String())
	}
}

func TestHandleVerification_RequestCancelled(t *testing.T) {
	hs := newHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	// The client hangs up while the live read is in flight.
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		cancel()
		return false, nil, nil
	})
	hs.targeter.dyn = dyn
	hs.reader.honorCtx = true
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: testUser, id: rec.ID.String(), ctx: ctx})
	expectError(t, w, http.StatusServiceUnavailable, "change receipt store unavailable")
	if decodeEnvelope(t, w).Error.Reason != ReasonReceiptStoreUnavailable {
		t.Fatalf("reason = %s", w.Body.String())
	}
	if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
		t.Fatal("a cancelled verification must persist nothing")
	}
}

func TestHandleVerification_GranteeEvaluatesLiveButNeverPersists(t *testing.T) {
	hs := newHarness(t)
	// The grantee's identity is forbidden on the live read.
	dyn, _ := fakeDyn()
	reads := 0
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		reads++
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac"))
	})
	hs.targeter.dyn = dyn
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	w := hs.verify(t, otherUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Reason != ReasonReadForbidden || reads != 1 {
		t.Fatalf("grantee must get a live evaluation: %+v (reads %d)", v, reads)
	}
	row := hs.reader.row(t, rec.ID)
	if row.VerificationState != store.VerifyPending || row.Verification != nil || row.VerifiedAt != nil {
		t.Fatalf("a grantee's verdict must never be stored: %s %s", row.VerificationState, row.Verification)
	}
	for _, c := range hs.reader.calls {
		if c == "setVerification" {
			t.Fatal("grantee verification wrote to the store")
		}
	}

	// The owner, who can read the object, then records the real verdict.
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	w = hs.verify(t, testUser, rec.ID)
	v = decodeView(t, w)
	if v.State != store.VerifyVerified || hs.reader.row(t, rec.ID).VerificationState != store.VerifyVerified {
		t.Fatalf("owner verdict = %+v, stored %s", v, hs.reader.row(t, rec.ID).VerificationState)
	}

	// Once final, the grantee reads the stored verdict, no cluster call.
	before := len(hs.targeter.calls())
	w = hs.verify(t, otherUser, rec.ID)
	v = decodeView(t, w)
	if v.State != store.VerifyVerified || len(hs.targeter.calls()) != before {
		t.Fatalf("frozen verdict for grantee = %+v, target calls %v", v, hs.targeter.calls())
	}
}

func TestHandleVerification_GranteeWindowExpiryIsNotFrozenForOwner(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, verificationWindow+time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	w := hs.verify(t, otherUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || v.Checks[0].Reason != ReasonWindowExpired {
		t.Fatalf("grantee sees the same rules: %+v", v)
	}
	if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
		t.Fatal("grantee evaluation froze the owner's receipt")
	}
}

func TestHandleVerification_PendingReceiptDoesNotRead(t *testing.T) {
	hs := newHarness(t)
	dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	hs.targeter.dyn = dyn
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	rec.State, rec.CompletedAt = store.ReceiptApplying, nil
	hs.reader.put(*rec)

	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyPending || len(v.Checks) != 0 || gets.Load() != 0 {
		t.Fatalf("pending view = %+v, gets = %d", v, gets.Load())
	}
}

func TestHandleVerification_UsesReceiptClusterNotHeader(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	rec.ClusterID, rec.OwnerID = "remote-1", adminUser.ID
	hs.reader.put(*rec)

	// Header: local. Receipt: remote-1. Admin owner.
	w := hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: adminUser, cluster: "local", id: rec.ID.String()})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("verification client built for %v, want the receipt's remote-1", calls)
	}
	if len(hs.access.calls) != 1 || hs.access.calls[0].cluster != "remote-1" {
		t.Fatalf("check redaction SAR went to %v", hs.access.calls)
	}
	v := decodeView(t, w)
	if v.State != store.VerifyVerified || v.Checks[0].Source == nil || v.Checks[0].Source.ClusterID != "remote-1" {
		t.Fatalf("view = %+v", v)
	}

	// The same receipt, owned by a non-admin: no remote cluster access.
	rec.OwnerID = testUser.ID
	rec.VerificationState, rec.Verification, rec.VerifiedAt = store.VerifyPending, nil, nil
	hs.reader.put(*rec)
	before := len(hs.targeter.calls())
	w = hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: testUser, cluster: "remote-1", id: rec.ID.String()})
	expectError(t, w, http.StatusForbidden, "admin role required for remote cluster access")
	if len(hs.targeter.calls()) != before {
		t.Fatal("a non-admin must not reach a remote cluster through a receipt")
	}
}

func TestHandleVerification_TargetFailure(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.err = errors.New("cannot build client")
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	w := hs.verify(t, testUser, rec.ID)
	expectError(t, w, http.StatusInternalServerError, "failed to create kubernetes client")
	if strings.Contains(w.Body.String(), "cannot build client") {
		t.Fatal("5xx detail leaked")
	}

	hs.h.clusters = nil
	w = hs.verify(t, testUser, rec.ID)
	expectError(t, w, http.StatusServiceUnavailable, "cluster routing is not configured")
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

func TestHandleResolveOwnership_CapsRefCount(t *testing.T) {
	hs := newHarness(t)
	w := hs.ownership(t, testUser, "local", ownershipBody(maxOwnershipRefs+1))
	expectError(t, w, http.StatusBadRequest, "at most 50 objects")
	w = hs.ownership(t, testUser, "local", `{"objects":[]}`)
	expectError(t, w, http.StatusBadRequest, "objects is required")
	w = hs.ownership(t, testUser, "local", `{"objects":[{"kind":"Deployment"}]}`)
	expectError(t, w, http.StatusBadRequest, "objects[0]: kind and name are required")
	w = hs.ownership(t, testUser, "local", `{"objects":`)
	expectError(t, w, http.StatusBadRequest, "invalid JSON body")
	if hs.resolver.calls != 0 || len(hs.targeter.calls()) != 0 {
		t.Fatal("rejected input must not reach the cluster or the resolver")
	}

	w = hs.ownership(t, testUser, "local", ownershipBody(maxOwnershipRefs))
	if w.Code != http.StatusOK || hs.resolver.calls != 1 || len(hs.resolver.refs) != maxOwnershipRefs {
		t.Fatalf("exactly the cap must pass: %d %s (resolver calls %d)", w.Code, w.Body.String(), hs.resolver.calls)
	}
}

func TestHandleResolveOwnership_BodyTooLarge(t *testing.T) {
	hs := newHarness(t)
	padding := strings.Repeat("x", maxOwnershipBodyBytes)
	w := hs.ownership(t, testUser, "local", `{"objects":[{"kind":"Deployment","name":"`+padding+`"}]}`)
	expectError(t, w, http.StatusRequestEntityTooLarge, "request body too large")
}

func TestHandleResolveOwnership_BodyClusterMustMatchHeader(t *testing.T) {
	hs := newHarness(t)
	w := hs.ownership(t, adminUser, "remote-1", `{"clusterId":"remote-2","objects":[{"kind":"Deployment","name":"web"}]}`)
	expectError(t, w, http.StatusBadRequest, "clusterId does not match X-Cluster-ID")
	if hs.resolver.calls != 0 {
		t.Fatal("a mismatched body cluster must not resolve anything")
	}
	// Matching, or omitted, is fine; "" and "local" are the same cluster.
	for _, body := range []string{
		`{"clusterId":"remote-1","objects":[{"kind":"Deployment","name":"web"}]}`,
		`{"objects":[{"kind":"Deployment","name":"web"}]}`,
	} {
		if w := hs.ownership(t, adminUser, "remote-1", body); w.Code != http.StatusOK {
			t.Fatalf("body %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := hs.ownership(t, testUser, "local", `{"clusterId":"","objects":[{"kind":"Deployment","name":"web"}]}`); w.Code != http.StatusOK {
		t.Fatalf("empty clusterId on local = %d", w.Code)
	}
	if hs.resolver.cluster != "local" {
		t.Fatalf("resolver cluster = %q", hs.resolver.cluster)
	}
}

func TestHandleResolveOwnership_ResolvesRefsThroughTargetMapper(t *testing.T) {
	hs := newHarness(t)
	hs.resolver.results = []gitops.OwnershipResult{
		{Object: gitops.ObjectRef{Kind: "Deployment", Name: "web"}, Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceUnknown, Reason: "no-evidence"},
		{Object: gitops.ObjectRef{Kind: "Widget", Name: "w"}, Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceUnknown, Reason: "no-evidence"},
	}
	body := `{"objects":[
		{"group":"apps","kind":"Deployment","namespace":"prod","name":"web"},
		{"group":"example.io","version":"v1","kind":"Widget","namespace":"prod","name":"w"}]}`

	w := hs.ownership(t, adminUser, "remote-1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("target = %v", calls)
	}
	if hs.resolver.user != adminUser || hs.resolver.cluster != "remote-1" || hs.resolver.dyn != hs.targeter.dyn {
		t.Fatalf("resolver got user=%v cluster=%q dyn-matches=%v", hs.resolver.user, hs.resolver.cluster, hs.resolver.dyn == hs.targeter.dyn)
	}
	refs := hs.resolver.refs
	if len(refs) != 2 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Resource != "deployments" || refs[0].Version != "v1" || refs[0].ClusterID != "remote-1" || refs[0].Namespace != "prod" {
		t.Fatalf("mapped ref = %+v", refs[0])
	}
	if refs[1].Resource != "" || refs[1].Version != "v1" || refs[1].Kind != "Widget" {
		t.Fatalf("unmappable ref must pass through: %+v", refs[1])
	}
	var resp OwnershipResponse
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ClusterID != "remote-1" || len(resp.Results) != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHandleResolveOwnership_ForbiddenAppLeaksNoRepoURL(t *testing.T) {
	hs := newHarness(t)
	// What gitops answers when the caller may not list Applications: a
	// forbidden verdict with no apps. The handler must pass it through as-is
	// and add nothing.
	hs.resolver.results = []gitops.OwnershipResult{{
		Object:     gitops.ObjectRef{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "web"},
		Controller: gitops.OwnedByNone, Confidence: gitops.ConfidenceForbidden, Reason: "argo-list-forbidden",
		IdentityBasis: "group-kind-namespace-name",
	}}
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if hs.resolver.user != testUser {
		t.Fatal("ownership must be resolved under the caller's own identity")
	}
	body := w.Body.String()
	for _, leak := range []string{"repoURL", "apps\":", "git.example"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response carries %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, `"confidence":"forbidden"`) {
		t.Fatalf("verdict missing: %s", body)
	}
}

func TestHandleResolveOwnership_Timeout(t *testing.T) {
	hs := newHarness(t)
	hs.h.ownershipTimeout = 20 * time.Millisecond
	hs.resolver.block = true
	start := time.Now()
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusGatewayTimeout, "ownership resolution timed out")
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout did not bound the call")
	}
}

func TestHandleResolveOwnership_ResolverFailure(t *testing.T) {
	hs := newHarness(t)
	hs.resolver.err = errors.New("gitops: boom")
	w := hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusInternalServerError, "ownership resolution failed")
	if strings.Contains(w.Body.String(), "boom") {
		t.Fatal("resolver error text leaked")
	}

	hs.targeter.err = errors.New("no client")
	w = hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusInternalServerError, "failed to create kubernetes client")

	hs.h.gitops = nil
	w = hs.ownership(t, testUser, "local", ownershipBody(1))
	expectError(t, w, http.StatusServiceUnavailable, "ownership resolution is not configured")
}

// ---------------------------------------------------------------------------
// Cross-cutting
// ---------------------------------------------------------------------------

func TestHandlers_RequireAuth(t *testing.T) {
	hs := newHarness(t)
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	for name, fn := range map[string]http.HandlerFunc{
		"list": hs.h.HandleList, "get": hs.h.HandleGet, "verification": hs.h.HandleVerification, "ownership": hs.h.HandleResolveOwnership,
	} {
		w := hs.do(t, fn, http.MethodGet, request{id: rec.ID.String(), body: ownershipBody(1)})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a user = %d %s", name, w.Code, w.Body.String())
		}
		expectError(t, w, http.StatusUnauthorized, "authentication required")
	}
	if len(hs.reader.calls) != 0 || hs.resolver.calls != 0 {
		t.Fatal("unauthenticated requests must not reach the store or the resolver")
	}
}

// TestHandlers_LegacyApplyEnvelopeUnaffected: this package serves nothing
// under /yaml and declares no type that could shadow the legacy apply
// envelope; and handler.go reaches the cluster only through the targeter.
func TestHandlers_LegacyApplyEnvelopeUnaffected(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			name := spec.(*ast.TypeSpec).Name.Name
			switch name {
			case "ApplyResult", "ApplyResponse", "ApplySummary", "ApplyRequest":
				t.Fatalf("handler.go declares %s, which shadows the legacy apply envelope", name)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING && strings.Contains(x.Value, "/yaml") {
				t.Fatalf("handler.go references a /yaml route: %s", x.Value)
			}
		case *ast.SelectorExpr:
			switch x.Sel.Name {
			case "ClientForUser", "DynamicClientForUser", "LocalFactory", "RESTMapper", "DiscoveryClient", "BaseDynamicClient":
				t.Fatalf("handler.go calls .%s at %s; cluster access must go through ClusterTargeter.TargetFor", x.Sel.Name, fset.Position(x.Pos()))
			}
		}
		return true
	})
}
