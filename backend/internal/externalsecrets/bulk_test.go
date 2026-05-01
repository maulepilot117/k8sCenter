package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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
	// Mirror the production UNIQUE partial index — reject if an active row
	// already exists for the same scope. Without this, fakes would silently
	// accept duplicates and bypass the race-loser code path.
	for _, existing := range f.jobs {
		if existing.CompletedAt == nil &&
			existing.ClusterID == j.ClusterID &&
			existing.Action == j.Action &&
			existing.ScopeTarget == j.ScopeTarget {
			return store.ErrBulkJobActiveExists
		}
	}
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

func (f *fakeBulkJobStore) AppendOutcomes(
	_ context.Context, id uuid.UUID, batch store.BulkOutcomeBatch,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return errors.New("not found")
	}
	j.Succeeded = append(j.Succeeded, batch.Succeeded...)
	j.Failed = append(j.Failed, batch.Failed...)
	j.Skipped = append(j.Skipped, batch.Skipped...)
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return errors.New("queue full")
	}
	f.enqueued = append(f.enqueued, msg)
	return nil
}

func (f *fakeWorker) setFull(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.full = v
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

	// 2. POST refresh-all with the pin from step 1
	pinBody := `{"targetUIDs":["uid-1","uid-2"]}`
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(pinBody))
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
			Reason string `json:"reason"`
			Extra  struct {
				Added   []string `json:"added"`
				Removed []string `json:"removed"`
			} `json:"extra"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body2)
	if body2.Error.Reason != "scope_changed" {
		t.Errorf("reason = %q; want scope_changed", body2.Error.Reason)
	}
	foundAdded := false
	for _, u := range body2.Error.Extra.Added {
		if u == "uid-current" {
			foundAdded = true
		}
	}
	if !foundAdded {
		t.Errorf("added = %v; want uid-current present", body2.Error.Extra.Added)
	}
}

// Concurrent same scope — second request gets the first job's id back.
func TestBulkRefresh_ConcurrentSameScope(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	pinBody := `{"targetUIDs":["uid-1"]}`
	makePostReq := func() *http.Request {
		req := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(pinBody))
		return urlWithChiParams(req, map[string]string{"namespace": "apps", "name": "vault"})
	}

	// First POST creates the job.
	w := httptest.NewRecorder()
	h.HandleBulkRefreshStore(w, makePostReq())
	if w.Code != http.StatusAccepted {
		t.Fatalf("first POST status = %d; body = %s", w.Code, w.Body.String())
	}

	// Second POST against same scope — job is still in-flight (CompletedAt nil).
	w = httptest.NewRecorder()
	h.HandleBulkRefreshStore(w, makePostReq())
	if w.Code != http.StatusConflict {
		t.Fatalf("second POST status = %d; want 409", w.Code)
	}
	var body struct {
		Error struct {
			Reason string `json:"reason"`
			Extra  struct {
				JobID string `json:"jobId"`
			} `json:"extra"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Reason != "active_job_exists" || body.Error.Extra.JobID == "" {
		t.Errorf("error = %+v", body.Error)
	}

	// And the existing job's id round-trips.
	if _, err := uuid.Parse(body.Error.Extra.JobID); err != nil {
		t.Errorf("jobId = %q; not a uuid", body.Error.Extra.JobID)
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

	pinBody := `{"targetUIDs":["uid-1"]}`
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(pinBody))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d; body = %s", w.Code, w.Body.String())
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
		ScopeTarget:  "apps/vault",
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
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{ID: id1, ClusterID: "local", Action: store.BulkRefreshActionStore, ScopeTarget: "apps/store-a"})
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{ID: id2, ClusterID: "local", Action: store.BulkRefreshActionStore, ScopeTarget: "apps/store-b"})
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
		Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault", ActorName: "alice",
	}
	h.auditBulkJob(context.Background(), msg, final)
	last := rec.last()
	var detail struct {
		JobID          string `json:"jobId"`
		SucceededCount int    `json:"succeededCount"`
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

// Race-loser: a parallel POST inserts the active row first; the slower POST
// receives store.ErrBulkJobActiveExists from Insert and must surface the
// existing job's id with the same 409 active_job_exists shape FindActive
// produces. See todo #347.
func TestBulkRefresh_RaceLoserSurfacesExistingJob(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	// Pre-seed an active job for the same scope.
	preID := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{
		ID: preID, ClusterID: "local", RequestedBy: "carol",
		Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault",
	})

	pinBody := `{"targetUIDs":["uid-1"]}`
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(pinBody))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409; body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Reason string `json:"reason"`
			Extra  struct {
				JobID string `json:"jobId"`
			} `json:"extra"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Reason != "active_job_exists" || body.Error.Extra.JobID != preID.String() {
		t.Errorf("error = %+v; want active_job_exists / %s", body.Error, preID)
	}
}

// Empty body → 400. Phase E requires the operator to pin scope by passing
// the targetUIDs from the prior GET refresh-scope. Without that pin, a
// freshly-resolved scope could include resources the operator never saw or
// confirmed (race against ES create/delete). See todo #340.
func TestBulkRefresh_EmptyBodyRejected(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, worker, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid body") {
		t.Errorf("error message = %s", w.Body.String())
	}
	if len(jobStore.jobs) != 0 || len(worker.enqueued) != 0 {
		t.Errorf("guard fired late: jobs=%d enqueued=%d", len(jobStore.jobs), len(worker.enqueued))
	}
}

// Empty targetUIDs array → 400. Same rationale as #340: no implicit pin.
func TestBulkRefresh_EmptyTargetUIDsRejected(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, _, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	body := `{"targetUIDs":[]}`
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(body))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "targetUIDs required") {
		t.Errorf("expected targetUIDs-required message; got %s", w.Body.String())
	}
	if len(jobStore.jobs) != 0 {
		t.Errorf("job persisted on rejected pin: %d", len(jobStore.jobs))
	}
}

// Bulk-refresh against a non-local X-Cluster-ID returns 501 before scope
// resolution, job persistence, or worker enqueue. Prevents audit/DB rows
// from claiming the wrong cluster while the patch runs locally. See #339.
func TestBulkRefresh_RejectsNonLocalCluster(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, worker, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", nil), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r = r.WithContext(middleware.WithClusterID(r.Context(), "prod-cluster"))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d; want 501; body = %s", w.Code, w.Body.String())
	}
	if len(jobStore.jobs) != 0 {
		t.Errorf("job rows = %d; want 0 (guard fires before persistence)", len(jobStore.jobs))
	}
	if len(worker.enqueued) != 0 {
		t.Errorf("worker enqueued = %d; want 0", len(worker.enqueued))
	}
}

// Worker-side defense in depth: even if a job row was inserted with a
// non-local cluster_id (e.g. via direct DB write or a pre-guard race),
// processJob refuses to patch and reaps the row. See #339.
func TestBulkWorker_RefusesNonLocalClusterMessage(t *testing.T) {
	jobStore := newFakeBulkJobStore()
	id := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{
		ID: id, ClusterID: "prod-cluster", Action: store.BulkRefreshActionStore,
	})

	h := &Handler{Logger: slog.Default()}
	worker := &BulkWorker{store: jobStore, k8s: nil, handler: h, logger: slog.Default()}

	worker.processJob(context.Background(), BulkJobMessage{
		JobID:     id,
		ClusterID: "prod-cluster",
		Action:    store.BulkRefreshActionStore,
		ScopeTarget:  "apps/vault",
		Targets:   []BulkScopeTarget{{Namespace: "apps", Name: "es1", UID: "uid-1"}},
		Username:  "u",
	})

	final, err := jobStore.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if final.CompletedAt == nil {
		t.Errorf("job should be completed (reaped) on non-local guard")
	}
	if len(final.Succeeded) != 0 || len(final.Failed) != 0 {
		t.Errorf("no patches should have run; succeeded=%v failed=%v", final.Succeeded, final.Failed)
	}
}

// Cluster-scoped audit Detail must redact per-UID enumeration to avoid
// cross-tenant leakage. Aggregate counts + reason histograms remain.
// See todo #348.
func TestAuditBulkJob_ClusterStoreScope_RedactsUIDs(t *testing.T) {
	rec := &recordingAudit{}
	h := &Handler{AuditLogger: rec, Logger: slog.Default()}
	id := uuid.New()
	now := time.Now().UTC()
	final := &store.ESOBulkRefreshJob{
		ID: id, ClusterID: "local", RequestedBy: "alice",
		Action:      store.BulkRefreshActionClusterStore,
		ScopeTarget: "shared-vault",
		Succeeded:   []string{"uid-tenant-a", "uid-tenant-b"},
		Failed: []store.BulkRefreshOutcome{
			{UID: "uid-tenant-a-1", Reason: "rbac_denied"},
			{UID: "uid-tenant-c-1", Reason: "rbac_denied"},
			{UID: "uid-tenant-b-1", Reason: "not_found"},
		},
		CompletedAt: &now,
	}
	msg := BulkJobMessage{
		JobID: id, Action: store.BulkRefreshActionClusterStore,
		ScopeTarget: "shared-vault", ActorName: "alice",
	}
	h.auditBulkJob(context.Background(), msg, final)
	last := rec.last()

	var detail map[string]any
	if err := json.Unmarshal([]byte(last.Detail), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	failed, ok := detail["failed"].(map[string]any)
	if !ok {
		t.Fatalf("failed not redacted to map; got %T = %v", detail["failed"], detail["failed"])
	}
	if int(failed["count"].(float64)) != 3 {
		t.Errorf("failed count = %v; want 3", failed["count"])
	}
	reasons := failed["reasons"].(map[string]any)
	if int(reasons["rbac_denied"].(float64)) != 2 || int(reasons["not_found"].(float64)) != 1 {
		t.Errorf("reasons = %+v; want rbac_denied:2, not_found:1", reasons)
	}
	// UIDs must NOT appear anywhere in the redacted Detail.
	if strings.Contains(last.Detail, "uid-tenant") {
		t.Errorf("redacted detail leaks per-UID identity: %s", last.Detail)
	}
}

// Namespace and store-scoped audits retain full UID lists — the scope itself
// already establishes the permissioned context for the reader.
func TestAuditBulkJob_StoreScope_KeepsUIDs(t *testing.T) {
	rec := &recordingAudit{}
	h := &Handler{AuditLogger: rec, Logger: slog.Default()}
	id := uuid.New()
	now := time.Now().UTC()
	final := &store.ESOBulkRefreshJob{
		ID: id, ClusterID: "local", RequestedBy: "alice",
		Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault",
		Failed: []store.BulkRefreshOutcome{
			{UID: "uid-1", Reason: "rbac_denied"},
		},
		CompletedAt: &now,
	}
	h.auditBulkJob(context.Background(), BulkJobMessage{JobID: id, Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault"}, final)
	if !strings.Contains(rec.last().Detail, "uid-1") {
		t.Errorf("store-scope audit should retain UIDs: %s", rec.last().Detail)
	}
}

// Queue-full produces a 503 with synthetic Complete so the orphan row
// doesn't block the next same-scope POST. See todo #354.
func TestBulkRefresh_QueueFull(t *testing.T) {
	objs := []runtime.Object{
		makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"}),
		makeStore("apps", "vault", "uid-store"),
	}
	h, jobStore, worker, _ := newBulkHandler(objs, resources.NewAlwaysAllowAccessChecker())
	worker.setFull(true)

	pinBody := `{"targetUIDs":["uid-1"]}`
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(pinBody))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503; body = %s", w.Code, w.Body.String())
	}
	if len(jobStore.jobs) != 1 {
		t.Fatalf("expected 1 job row even on queue-full")
	}
	for _, j := range jobStore.jobs {
		if j.CompletedAt == nil {
			t.Errorf("queue-full row should be force-completed; got nil completed_at")
		}
	}

	// Subsequent same-scope POST is no longer blocked by FindActive.
	worker.setFull(false)
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(pinBody)), &auth.User{Username: "alice", KubernetesUsername: "u"})
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = int64(len(pinBody))
	r = urlWithChiParams(r, map[string]string{"namespace": "apps", "name": "vault"})
	h.HandleBulkRefreshStore(w, r)
	if w.Code != http.StatusAccepted {
		t.Errorf("retry status = %d; want 202 (queue-full row should not block); body = %s", w.Code, w.Body.String())
	}
}

// IsConflict from the apiserver becomes a `failed: optimistic_lock` outcome.
// Drives the real processJob loop via the dynForUser injection seam (#352).
func TestBulkWorker_OptimisticLockOutcome(t *testing.T) {
	es := makeESForBulk("apps", "es1", "uid-1", StoreRef{Name: "vault", Kind: "SecretStore"})
	dynFake := newEsoFakeDynClient(es)
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Resource: "externalsecrets"}, "es1", errors.New("rv conflict"),
		)
	})

	jobStore := newFakeBulkJobStore()
	id := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{
		ID: id, ClusterID: "local", Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault",
	})

	h := &Handler{Logger: slog.Default()}
	worker := &BulkWorker{
		store:      jobStore,
		handler:    h,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		dynForUser: func(_ string, _ []string) (dynamic.Interface, error) { return dynFake, nil },
	}

	worker.processJob(context.Background(), BulkJobMessage{
		JobID:     id,
		ClusterID: "local",
		Action:    store.BulkRefreshActionStore,
		ScopeTarget:  "apps/vault",
		Targets:   []BulkScopeTarget{{Namespace: "apps", Name: "es1", UID: "uid-1"}},
		Username:  "u",
	})

	final, _ := jobStore.Get(context.Background(), id)
	if final.CompletedAt == nil {
		t.Fatal("expected job completed")
	}
	if len(final.Failed) != 1 || final.Failed[0].Reason != "optimistic_lock" {
		t.Errorf("failed = %+v; want one optimistic_lock entry", final.Failed)
	}
}

// Worker panic must mark the job completed and append a synthetic
// `worker_panic` outcome so the dialog's polling loop terminates and
// FindActive does not block subsequent same-scope POSTs. See todo #344.
func TestBulkWorker_PanicCompletesJobWithMarker(t *testing.T) {
	jobStore := newFakeBulkJobStore()
	id := uuid.New()
	_ = jobStore.Insert(context.Background(), store.ESOBulkRefreshJob{
		ID: id, ClusterID: "local", Action: store.BulkRefreshActionStore, ScopeTarget: "apps/vault",
	})

	worker := &BulkWorker{
		jobs:    make(chan BulkJobMessage, 1),
		store:   jobStore,
		k8s:     nil,
		handler: nil, // forces a nil-deref panic inside processJob
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	worker.jobs <- BulkJobMessage{
		JobID:     id,
		ClusterID: "local",
		Action:    store.BulkRefreshActionStore,
		ScopeTarget:  "apps/vault",
		Targets:   []BulkScopeTarget{{Namespace: "apps", Name: "es1", UID: "uid-1"}},
		Username:  "u",
	}

	ctx, cancel := context.WithCancel(context.Background())
	go worker.run(ctx)

	// Wait briefly for the panic + recovery to complete the job.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if final, err := jobStore.Get(context.Background(), id); err == nil && final.CompletedAt != nil {
			cancel()
			if len(final.Failed) != 1 || final.Failed[0].Reason != "worker_panic" {
				t.Errorf("failed = %+v; want one entry with reason=worker_panic", final.Failed)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatal("panic recovery did not complete the job within 2s")
}

// classifyPatchError must produce a meaningful k8s-reason or transient_*
// prefix instead of the legacy "patch_error:unknown" stub. Operators rely on
// the reason to distinguish transient (retryable) from permanent failures.
func TestClassifyPatchError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"timeout", apierrors.NewTimeoutError("upstream timeout", 0), "transient_timeout"},
		{"server-timeout", apierrors.NewServerTimeout(schema.GroupResource{Resource: "externalsecrets"}, "patch", 1), "transient_timeout"},
		{"throttled", apierrors.NewTooManyRequests("APF dropped request", 1), "transient_throttled"},
		{"unavailable", apierrors.NewServiceUnavailable("apiserver rolling restart"), "transient_unavailable"},
		{"conflict", apierrors.NewConflict(schema.GroupResource{Resource: "externalsecrets"}, "n", errors.New("rv conflict")), "patch_error:conflict"},
		{"invalid", apierrors.NewBadRequest("bad request"), "patch_error:badrequest"},
		{"non-status", errors.New("plain old error"), "patch_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPatchError(tc.err)
			if got != tc.want {
				t.Errorf("classifyPatchError(%v) = %q; want %q", tc.err, got, tc.want)
			}
		})
	}
}
