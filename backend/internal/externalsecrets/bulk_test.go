package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/store"
)

// --- in-memory fakes -------------------------------------------------------

type fakeBulkJobStore struct {
	mu   sync.Mutex
	jobs map[uuid.UUID]*store.ESOBulkRefreshJob
}

func newFakeBulkJobStore() *fakeBulkJobStore {
	return &fakeBulkJobStore{jobs: map[uuid.UUID]*store.ESOBulkRefreshJob{}}
}

func (f *fakeBulkJobStore) Insert(_ context.Context, j store.ESOBulkRefreshJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	j.CreatedAt = now
	if j.Succeeded == nil {
		j.Succeeded = []string{}
	}
	if j.Failed == nil {
		j.Failed = []store.BulkRefreshOutcome{}
	}
	if j.Skipped == nil {
		j.Skipped = []store.BulkRefreshOutcome{}
	}
	cp := j
	f.jobs[j.ID] = &cp
	return nil
}

func (f *fakeBulkJobStore) Get(_ context.Context, id uuid.UUID) (*store.ESOBulkRefreshJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *j
	return &cp, nil
}

func (f *fakeBulkJobStore) FindActive(
	_ context.Context, clusterID string, action store.BulkRefreshAction, scopeTarget string,
) (*store.ESOBulkRefreshJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ClusterID == clusterID && j.Action == action && j.ScopeTarget == scopeTarget && j.CompletedAt == nil {
			cp := *j
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeBulkJobStore) AppendOutcome(
	_ context.Context, id uuid.UUID, succeededUID string,
	failed *store.BulkRefreshOutcome, skipped *store.BulkRefreshOutcome,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return errors.New("not found")
	}
	switch {
	case succeededUID != "":
		j.Succeeded = append(j.Succeeded, succeededUID)
	case failed != nil:
		j.Failed = append(j.Failed, *failed)
	case skipped != nil:
		j.Skipped = append(j.Skipped, *skipped)
	}
	return nil
}

func (f *fakeBulkJobStore) Complete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return errors.New("not found")
	}
	if j.CompletedAt == nil {
		now := time.Now().UTC()
		j.CompletedAt = &now
	}
	return nil
}

func (f *fakeBulkJobStore) CompleteOrphans(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	now := time.Now().UTC()
	for _, j := range f.jobs {
		if j.CompletedAt == nil {
			j.CompletedAt = &now
			n++
		}
	}
	return n, nil
}

// fakeWorker captures Enqueue calls so tests can drive processJob synchronously.
type fakeWorker struct {
	mu       sync.Mutex
	enqueued []BulkJobMessage
	full     bool
}

func (f *fakeWorker) Enqueue(msg BulkJobMessage) error {
	if f.full {
		return errors.New("queue full")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, msg)
	return nil
}

// --- scope matching --------------------------------------------------------

func TestMatchScope_Store(t *testing.T) {
	ess := []ExternalSecret{
		{Namespace: "apps", Name: "es1", StoreRef: StoreRef{Name: "vault", Kind: "SecretStore"}},
		{Namespace: "apps", Name: "es2", StoreRef: StoreRef{Name: "other", Kind: "SecretStore"}},
		{Namespace: "platform", Name: "es3", StoreRef: StoreRef{Name: "vault", Kind: "SecretStore"}},
		{Namespace: "apps", Name: "es4", StoreRef: StoreRef{Name: "vault", Kind: "ClusterSecretStore"}},
	}
	got := matchScope(ess, store.BulkRefreshActionStore, "apps/vault")
	if len(got) != 1 || got[0].Name != "es1" {
		t.Errorf("got %d match(es); want 1 (es1). matches=%+v", len(got), got)
	}
}

func TestMatchScope_ClusterStore(t *testing.T) {
	ess := []ExternalSecret{
		{Namespace: "apps", Name: "es1", StoreRef: StoreRef{Name: "global", Kind: "ClusterSecretStore"}},
		{Namespace: "platform", Name: "es2", StoreRef: StoreRef{Name: "global", Kind: "ClusterSecretStore"}},
		{Namespace: "apps", Name: "es3", StoreRef: StoreRef{Name: "vault", Kind: "SecretStore"}},
	}
	got := matchScope(ess, store.BulkRefreshActionClusterStore, "global")
	if len(got) != 2 {
		t.Errorf("got %d; want 2", len(got))
	}
}

func TestMatchScope_Namespace(t *testing.T) {
	ess := []ExternalSecret{
		{Namespace: "apps", Name: "a"},
		{Namespace: "apps", Name: "b"},
		{Namespace: "platform", Name: "c"},
	}
	got := matchScope(ess, store.BulkRefreshActionNamespace, "apps")
	if len(got) != 2 {
		t.Errorf("got %d; want 2", len(got))
	}
}

func TestCompareUIDs_NoChange(t *testing.T) {
	scope := []BulkScopeTarget{
		{UID: "a"}, {UID: "b"}, {UID: "c"},
	}
	added, removed := compareUIDs([]string{"a", "b", "c"}, scope)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("added=%v removed=%v; want both empty", added, removed)
	}
}

func TestCompareUIDs_Drift(t *testing.T) {
	scope := []BulkScopeTarget{
		{UID: "a"}, {UID: "b"}, {UID: "newer"},
	}
	added, removed := compareUIDs([]string{"a", "b", "stale"}, scope)
	if len(added) != 1 || added[0] != "newer" {
		t.Errorf("added=%v; want [newer]", added)
	}
	if len(removed) != 1 || removed[0] != "stale" {
		t.Errorf("removed=%v; want [stale]", removed)
	}
}

// --- handler tests ---------------------------------------------------------

func makeESForBulk(ns, name, uid string, storeRef StoreRef) *unstructured.Unstructured {
	es := makeES(ns, name, uid)
	spec, _ := es.Object["spec"].(map[string]any)
	spec["secretStoreRef"] = map[string]any{
		"name": storeRef.Name, "kind": storeRef.Kind,
	}
	return es
}

func newBulkHandler(esObjs []runtime.Object, accessChecker *resources.AccessChecker) (*Handler, *fakeBulkJobStore, *fakeWorker, *recordingAudit) {
	dynFake := newEsoFakeDynClient(esObjs...)
	jobStore := newFakeBulkJobStore()
	worker := &fakeWorker{}
	rec := &recordingAudit{}
	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: accessChecker,
		AuditLogger:   rec,
		Logger:        slog.Default(),
		BulkJobStore:  jobStore,
		BulkWorker:    worker,
		dynOverride:   dynFake,
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}
	return h, jobStore, worker, rec
}

// AE5 happy path — scope resolves, POST creates job, worker queue receives msg.
func TestBulkRefresh_StoreHappyPath(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeESForBulk("apps", "es2", "uid-2", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, worker, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	// 1. GET refresh-scope
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleResolveStoreScope(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("scope GET status = %d; body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data BulkScopeResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.VisibleCount != 2 || env.Data.TotalCount != 2 || env.Data.Restricted {
		t.Errorf("scope = %+v; want visible=2/total=2/!restricted", env.Data)
	}

	// 2. POST refresh-all
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d; body = %s", w.Code, w.Body.String())
	}

	// Worker channel got the message
	if len(worker.enqueued) != 1 {
		t.Fatalf("worker enqueued = %d; want 1", len(worker.enqueued))
	}
	if len(worker.enqueued[0].Targets) != 2 {
		t.Errorf("worker job targets = %d; want 2", len(worker.enqueued[0].Targets))
	}

	// Job row persisted
	if len(jobStore.jobs) != 1 {
		t.Errorf("jobStore size = %d", len(jobStore.jobs))
	}
}

// Empty scope — store has no dependents.
func TestBulkRefresh_EmptyScope(t *testing.T) {
	objs := []runtime.Object{
		makeStore("apps", "vault", "uid-store"),
	}
	h, _, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422 (empty scope)", w.Code)
	}
}

// Scope-changed at execution — client sends UIDs that no longer match.
func TestBulkRefresh_ScopeChanged(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-current", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, _, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	body := strings.NewReader(`{"targetUIDs":["uid-stale"]}`)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", body), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(`{"targetUIDs":["uid-stale"]}`))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", w.Code)
	}
	var body2 struct {
		Error struct {
			Reason  string   `json:"reason"`
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body2)
	if body2.Error.Reason != "scope_changed" {
		t.Errorf("reason = %q; want scope_changed", body2.Error.Reason)
	}
	foundAdded := false
	for _, u := range body2.Error.Added {
		if u == "uid-current" {
			foundAdded = true
		}
	}
	if !foundAdded {
		t.Errorf("added = %v; want uid-current present", body2.Error.Added)
	}
}

// Concurrent same scope — second request gets the first job's id back.
func TestBulkRefresh_ConcurrentSameScope(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	// First POST creates the job.
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first POST status = %d", w.Code)
	}

	// Second POST against same scope — job is still in-flight (CompletedAt nil).
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("second POST status = %d; want 409", w.Code)
	}
	var body struct {
		Error struct {
			Reason string `json:"reason"`
			JobID  string `json:"jobId"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Reason != "active_job_exists" || body.Error.JobID == "" {
		t.Errorf("error = %+v", body.Error)
	}

	// And the existing job's id round-trips.
	if _, err := uuid.Parse(body.Error.JobID); err != nil {
		t.Errorf("jobId = %q; not a uuid", body.Error.JobID)
	}
	if len(jobStore.jobs) != 1 {
		t.Errorf("expected 1 job; got %d", len(jobStore.jobs))
	}
}

// GET bulk-refresh-jobs/{jobId} — visible to requester, forbidden to others.
func TestBulkRefresh_GetJob_Visibility(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, _, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d", w.Code)
	}
	var jobBody struct {
		Data struct {
			JobID string `json:"jobId"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &jobBody)
	jobID := jobBody.Data.JobID
	if jobID == "" {
		t.Fatalf("no jobId returned: %s", w.Body.String())
	}

	// Requester gets the job.
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{Username: "alice"})
	r = urlWithChiParams(r, map[string]string{"jobId": jobID})
	h.HandleGetBulkRefreshJob(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("requester GET = %d; want 200; body = %s", w.Code, w.Body.String())
	}

	// Different non-admin user — forbidden.
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodGet, "/", nil), &auth.User{Username: "bob"})
	r = urlWithChiParams(r, map[string]string{"jobId": jobID})
	h.HandleGetBulkRefreshJob(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("bob GET = %d; want 403", w.Code)
	}
}

// --- worker tests ----------------------------------------------------------

// Mixed outcomes: 3 succeed, 1 already-refreshing (skipped), 1 RBAC (failed).
func TestBulkWorker_MixedOutcomes(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-5 * time.Second).Format(time.RFC3339)

	es1 := makeES("apps", "ok1", "uid-ok1")
	es2 := makeES("apps", "ok2", "uid-ok2")
	// in-flight: refreshTime within 30s window
	es3 := makeES("apps", "in-flight", "uid-flight")
	st, _ := es3.Object["status"].(map[string]any)
	st["refreshTime"] = recent
	// denied: GET succeeds, PATCH returns 403 via the reactor below
	es4 := makeES("apps", "denied", "uid-denied")

	dynFake := newEsoFakeDynClient(es1, es2, es3, es4)
	dynFake.PrependReactor("patch", "externalsecrets", func(a clienttesting.Action) (bool, runtime.Object, error) {
		patchAction := a.(clienttesting.PatchAction)
		if patchAction.GetName() == "denied" {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Group: GroupName, Resource: "externalsecrets"},
				"denied",
				errors.New("rbac"),
			)
		}
		return false, nil, nil
	})

	jobStore := newFakeBulkJobStore()
	jobID := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{
		ID:          jobID,
		ClusterID:   "local",
		RequestedBy: "alice",
		Action:      store.BulkRefreshActionStore,
		ScopeTarget: "apps/vault",
		TargetUIDs:  []string{"uid-ok1", "uid-ok2", "uid-flight", "uid-denied"},
	})

	rec := &recordingAudit{}
	h := &Handler{
		Logger:      slog.Default(),
		AuditLogger: rec,
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}

	// processJob calls w.k8s.DynamicClientForUser, which is nil in this
	// test wiring. Drive the per-target loop directly via patchForceSync
	// to exercise the same outcome-classification logic without a real
	// ClientFactory.

	msg := BulkJobMessage{
		JobID:     jobID,
		ClusterID: "local",
		Action:    store.BulkRefreshActionStore,
		ScopeTgt:  "apps/vault",
		Targets: []BulkScopeTarget{
			{Namespace: "apps", Name: "ok1", UID: "uid-ok1"},
			{Namespace: "apps", Name: "ok2", UID: "uid-ok2"},
			{Namespace: "apps", Name: "in-flight", UID: "uid-flight"},
			{Namespace: "apps", Name: "denied", UID: "uid-denied"},
		},
		Username:  "u",
		ActorName: "alice",
	}
	// Drive the per-target loop directly to avoid w.k8s dependency.
	dynClient, _ := h.dynForUser(msg.Username, msg.Groups)
	for _, target := range msg.Targets {
		_, err := h.patchForceSync(context.Background(), dynClient, target.Namespace, target.Name)
		switch {
		case errors.Is(err, errAlreadyRefreshing):
			_ = jobStore.AppendOutcome(context.Background(), msg.JobID, "", nil, &store.BulkRefreshOutcome{UID: target.UID, Reason: "already_refreshing"})
		case apierrors.IsForbidden(err):
			_ = jobStore.AppendOutcome(context.Background(), msg.JobID, "", &store.BulkRefreshOutcome{UID: target.UID, Reason: "rbac_denied"}, nil)
		case apierrors.IsNotFound(err):
			_ = jobStore.AppendOutcome(context.Background(), msg.JobID, "", &store.BulkRefreshOutcome{UID: target.UID, Reason: "not_found"}, nil)
		case err != nil:
			_ = jobStore.AppendOutcome(context.Background(), msg.JobID, "", &store.BulkRefreshOutcome{UID: target.UID, Reason: "patch_error"}, nil)
		default:
			_ = jobStore.AppendOutcome(context.Background(), msg.JobID, target.UID, nil, nil)
		}
	}
	_ = jobStore.Complete(context.Background(), msg.JobID)

	final, _ := jobStore.Get(context.Background(), msg.JobID)
	if len(final.Succeeded) != 2 {
		t.Errorf("succeeded = %v; want 2", final.Succeeded)
	}
	if len(final.Skipped) != 1 || final.Skipped[0].Reason != "already_refreshing" {
		t.Errorf("skipped = %+v; want 1 already_refreshing", final.Skipped)
	}
	if len(final.Failed) != 1 || final.Failed[0].Reason != "rbac_denied" {
		t.Errorf("failed = %+v; want 1 rbac_denied", final.Failed)
	}

	// Audit row from the worker (passing the refreshed final job).
	h.auditBulkJob(context.Background(), msg, final)
	last := rec.last()
	if last.Action != "eso_bulk_refresh" {
		t.Errorf("audit action = %q", last.Action)
	}
	if last.Result != "failure" {
		t.Errorf("audit result = %q; want failure (any failed[] entry triggers failure)", last.Result)
	}
}

// CompleteOrphans — startup recovery.
func TestBulkRefresh_CompleteOrphans(t *testing.T) {
	jobStore := newFakeBulkJobStore()
	id1 := uuid.New()
	id2 := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{ID: id1, ClusterID: "local", Action: store.BulkRefreshActionStore})
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{ID: id2, ClusterID: "local", Action: store.BulkRefreshActionStore})
	_ = jobStore.Complete(context.Background(), id1)

	n, err := jobStore.CompleteOrphans(context.Background())
	if err != nil {
		t.Fatalf("CompleteOrphans: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped = %d; want 1 (id2)", n)
	}
	final, _ := jobStore.Get(context.Background(), id2)
	if final.CompletedAt == nil {
		t.Errorf("id2 should have completed_at set")
	}
}

// Audit detail JSON renders the full outcome shape for the audit-log viewer.
func TestBulkRefresh_AuditDetailShape(t *testing.T) {
	rec := &recordingAudit{}
	h := &Handler{AuditLogger: rec, Logger: slog.Default()}
	id := uuid.New()
	now := time.Now().UTC()
	final := &store.ESOBulkRefreshJob{
		ID:          id,
		ClusterID:   "local",
		RequestedBy: "alice",
		Action:      store.BulkRefreshActionStore,
		ScopeTarget: "apps/vault",
		TargetUIDs:  []string{"a", "b", "c"},
		CompletedAt: &now,
		Succeeded:   []string{"a"},
		Failed:      []store.BulkRefreshOutcome{{UID: "b", Reason: "rbac_denied"}},
		Skipped:     []store.BulkRefreshOutcome{{UID: "c", Reason: "already_refreshing"}},
	}
	msg := BulkJobMessage{
		Action: store.BulkRefreshActionStore, ScopeTgt: "apps/vault", ActorName: "alice",
	}
	h.auditBulkJob(context.Background(), msg, final)
	last := rec.last()
	var detail struct {
		JobID          string `json:"jobId"`
		SucceededCount int    `json:"succeeded_count"`
		Failed         []store.BulkRefreshOutcome `json:"failed"`
		Skipped        []store.BulkRefreshOutcome `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(last.Detail), &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	_ = id
	if detail.JobID != id.String() {
		t.Errorf("detail jobId = %q; want %q", detail.JobID, id.String())
	}
	if detail.SucceededCount != 1 || len(detail.Failed) != 1 || len(detail.Skipped) != 1 {
		t.Errorf("counts = %d/%d/%d", detail.SucceededCount, len(detail.Failed), len(detail.Skipped))
	}
}
