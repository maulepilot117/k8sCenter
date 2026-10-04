package velero

// assurance_handler_test.go — Release F U35: the backup-assurance HTTP
// surface. Requests go through the production route registration
// (RegisterAssuranceRoutes) behind the real CSRF middleware.
//
// Tests that read or write policies and exceptions run against PostgreSQL
// through the same gate as assurance_service_db_test.go (skipped without
// KUBECENTER_TEST_DATABASE_URL, a failure when KUBECENTER_TEST_REQUIRE_DATABASE
// is set, as in CI). Tests whose answer is decided before the store is
// touched (auth, validation, routing, 503) use a store over an unreachable
// pool, so they run everywhere and would fail loudly if they reached it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

var (
	asrAdmin = &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}}
	asrAlice = &auth.User{Username: "alice", KubernetesUsername: "alice", KubernetesGroups: []string{"team-a"}, Roles: []string{"viewer"}}
)

// assuranceAPI is a Velero handler wired for the assurance endpoints, with
// an access checker whose "list schedules" grants the test controls.
type assuranceAPI struct {
	*harness
	st        *store.BackupAssuranceStore
	svc       *AssuranceService
	clusterID string

	mu      sync.Mutex
	allowed map[string]bool // namespaces where alice may list schedules
	checks  int
}

func (a *assuranceAPI) allow(namespaces ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowed = map[string]bool{}
	for _, ns := range namespaces {
		a.allowed[ns] = true
	}
}

// newAssuranceAPIWith builds the API over st (which may be nil).
func newAssuranceAPIWith(t *testing.T, st *store.BackupAssuranceStore, objs ...*unstructured.Unstructured) *assuranceAPI {
	t.Helper()
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	a := &assuranceAPI{harness: newLocalHarness(t, objs...), st: st, clusterID: "test-u35-" + hex.EncodeToString(suffix[:]), allowed: map[string]bool{}}
	a.h.AccessChecker = resources.NewPredicateAccessChecker(func(verb, group, resource, ns string) bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.checks++
		return verb == "list" && group == "velero.io" && resource == "schedules" && a.allowed[ns]
	})
	var as assuranceStore
	if st != nil {
		as = st
	}
	a.svc = newAssuranceServiceWith(a.h, a.h.Discoverer, as, &fakeEmitter{result: notifications.EmitPersisted}, a.clusterID, "holder-a",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.h.Assurance = a.svc
	a.h.AssuranceStore = st
	return a
}

// newAssuranceAPI is backed by the test database, or skips.
func newAssuranceAPI(t *testing.T, objs ...*unstructured.Unstructured) *assuranceAPI {
	t.Helper()
	return newAssuranceAPIWith(t, store.NewBackupAssuranceStore(assuranceTestPool(t)), objs...)
}

// newAssuranceAPINoDB is backed by a store over a pool that can never
// connect: every test using it must be answered before the store is used.
func newAssuranceAPINoDB(t *testing.T) *assuranceAPI {
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
	return newAssuranceAPIWith(t, store.NewBackupAssuranceStore(pool))
}

// request sends method path as user (nil = unauthenticated) to the
// production route registration behind CSRF. Writes carry the CSRF header
// unless noCSRF is set.
type asrReq struct {
	user    *auth.User
	method  string
	path    string
	body    string
	cluster string
	noCSRF  bool
	ctx     context.Context
}

func (a *assuranceAPI) serve(t *testing.T, rq asrReq) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if rq.user != nil {
				ctx = auth.ContextWithUser(ctx, rq.user)
			}
			cluster := rq.cluster
			if cluster == "" {
				cluster = localCluster
			}
			next.ServeHTTP(w, req.WithContext(middleware.WithClusterID(ctx, cluster)))
		})
	})
	r.Use(middleware.CSRF)
	r.Route("/velero/assurance", func(asr chi.Router) {
		a.h.RegisterAssuranceRoutes(asr, func(next http.Handler) http.Handler { return next })
	})

	ctx := rq.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	req := httptest.NewRequestWithContext(ctx, rq.method, rq.path, strings.NewReader(rq.body))
	if rq.method != http.MethodGet && !rq.noCSRF {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func (a *assuranceAPI) get(t *testing.T, user *auth.User, path string) *httptest.ResponseRecorder {
	t.Helper()
	return a.serve(t, asrReq{user: user, method: http.MethodGet, path: path})
}

// envelope is the response shape every endpoint shares.
type envelope[T any] struct {
	Data     T `json:"data"`
	Metadata *struct {
		Total int `json:"total"`
	} `json:"metadata"`
	Error *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Reason  string         `json:"reason"`
		Extra   map[string]any `json:"extra"`
	} `json:"error"`
}

func decodeEnv[T any](t *testing.T, rr *httptest.ResponseRecorder) envelope[T] {
	t.Helper()
	var e envelope[T]
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return e
}

func wantStatus(t *testing.T, rr *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rr.Code != code {
		t.Fatalf("status %d, want %d: %s", rr.Code, code, rr.Body.String())
	}
}

func wantReason(t *testing.T, rr *httptest.ResponseRecorder, code int, reason string) envelope[json.RawMessage] {
	t.Helper()
	wantStatus(t, rr, code)
	e := decodeEnv[json.RawMessage](t, rr)
	if e.Error == nil || e.Error.Reason != reason {
		t.Fatalf("error = %+v, want reason %q", e.Error, reason)
	}
	return e
}

// --- seeding -----------------------------------------------------------------

func (a *assuranceAPI) policy(t *testing.T, kind store.AssuranceScopeKind, ns, name string) store.BackupAssurancePolicy {
	t.Helper()
	p := store.BackupAssurancePolicy{
		ID: uuid.New(), ClusterID: a.clusterID, ScopeKind: kind, ScopeNamespace: ns, ScopeName: name,
		MaxAge: 24 * time.Hour, Grace: time.Hour, TreatPartialAs: store.AssuranceTreatPartialAsFailure,
		AlertOnPaused: true, Enabled: true, CreatedBy: "seed",
	}
	if err := a.st.InsertPolicy(t.Context(), p); err != nil {
		t.Fatalf("InsertPolicy: %v", err)
	}
	return p
}

// open seeds one open exception for p's subject. uid is used for schedule
// subjects only.
func (a *assuranceAPI) open(t *testing.T, p store.BackupAssurancePolicy, cond store.AssuranceCondition, uid string, detail map[string]any) store.BackupAssuranceException {
	t.Helper()
	e := store.BackupAssuranceException{
		ID: uuid.New(), ClusterID: a.clusterID, PolicyID: p.ID, SubjectKind: p.ScopeKind,
		SubjectNamespace: p.ScopeNamespace, SubjectName: p.ScopeName,
		Condition: cond, Severity: store.AssuranceSeverityWarning, OpenedAt: time.Now().UTC(),
	}
	if p.ScopeKind == store.ScopeSchedule {
		e.SubjectUID = uid
	}
	if detail != nil {
		raw, err := json.Marshal(detail)
		if err != nil {
			t.Fatal(err)
		}
		e.Detail = raw
	}
	got, opened, err := a.st.OpenExceptionAndEnqueue(t.Context(), e)
	if err != nil || !opened {
		t.Fatalf("OpenExceptionAndEnqueue: opened=%v err=%v", opened, err)
	}
	return got
}

func (a *assuranceAPI) exceptions(t *testing.T, user *auth.User, query string) envelope[[]AssuranceExceptionView] {
	t.Helper()
	rr := a.get(t, user, "/velero/assurance/exceptions"+query)
	wantStatus(t, rr, http.StatusOK)
	return decodeEnv[[]AssuranceExceptionView](t, rr)
}

func subjectNamespaces(vs []AssuranceExceptionView) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Subject.Namespace)
	}
	slices.Sort(out)
	return out
}

// ---------------------------------------------------------------------------
// Exceptions: visibility
// ---------------------------------------------------------------------------

func TestAssuranceExceptions_NonAdminSeesOnlyVisibleNamespaces(t *testing.T) {
	a := newAssuranceAPI(t)
	pa := a.policy(t, store.ScopeNamespace, "team-a", "")
	pb := a.policy(t, store.ScopeNamespace, "team-b", "")
	ps := a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")
	a.open(t, pa, store.ConditionOverdue, "", nil)
	a.open(t, pb, store.ConditionOverdue, "", nil)
	a.open(t, ps, store.ConditionFailed, "uid-daily", nil)
	a.allow("team-a")

	got := a.exceptions(t, asrAlice, "")
	if ns := subjectNamespaces(got.Data); !slices.Equal(ns, []string{"team-a"}) {
		t.Fatalf("alice sees namespaces %v, want [team-a]", ns)
	}
	if got.Metadata == nil || got.Metadata.Total != 1 {
		t.Fatalf("metadata = %+v, want total 1", got.Metadata)
	}

	all := a.exceptions(t, asrAdmin, "")
	if ns := subjectNamespaces(all.Data); !slices.Equal(ns, []string{"team-a", "team-b", veleroNamespace}) {
		t.Fatalf("admin sees namespaces %v, want all three", ns)
	}
}

func TestAssuranceExceptions_CountsAreComputedAfterFiltering(t *testing.T) {
	a := newAssuranceAPI(t)
	pa := a.policy(t, store.ScopeNamespace, "team-a", "")
	pb := a.policy(t, store.ScopeNamespace, "team-b", "")
	a.open(t, pa, store.ConditionOverdue, "", nil)
	for _, c := range []store.AssuranceCondition{store.ConditionOverdue, store.ConditionFailed, store.ConditionNeverRun} {
		a.open(t, pb, c, "", nil)
	}
	a.allow("team-a")

	page := a.exceptions(t, asrAlice, "?limit=1")
	if page.Metadata == nil || page.Metadata.Total != 1 {
		t.Fatalf("alice total = %+v, want 1 (a pre-filter 4 would reveal team-b's rows)", page.Metadata)
	}

	rr := a.get(t, asrAlice, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	st := decodeEnv[AssuranceStatusView](t, rr).Data
	if st.Open.Total != 1 || st.Open.ByCondition["overdue"] != 1 || st.Open.ByCondition["failed"] != 0 || st.Open.ByCondition["never_run"] != 0 {
		t.Fatalf("alice open counts = %+v, want only team-a's overdue", st.Open)
	}
	if len(st.Open.ByCondition) != len(assuranceConditions) {
		t.Errorf("byCondition has %d keys, want every condition (%d) present", len(st.Open.ByCondition), len(assuranceConditions))
	}

	if adm := a.exceptions(t, asrAdmin, "?limit=1"); adm.Metadata.Total != 4 || len(adm.Data) != 1 {
		t.Fatalf("admin page = %d rows, total %d; want 1 row of 4", len(adm.Data), adm.Metadata.Total)
	}
}

func TestAssuranceExceptions_ClusterScopedExceptionsAreAdminOnly(t *testing.T) {
	a := newAssuranceAPI(t)
	pc := a.policy(t, store.ScopeCluster, "", "")
	a.open(t, pc, store.ConditionCollectionUnknown, "", nil)
	// Alice may list schedules in every namespace a policy names, and in the
	// empty namespace too: cluster rows are still not hers to see.
	a.allow("", veleroNamespace)

	if got := a.exceptions(t, asrAlice, ""); len(got.Data) != 0 || got.Metadata.Total != 0 {
		t.Fatalf("alice sees %d cluster rows (total %d), want none", len(got.Data), got.Metadata.Total)
	}
	rr := a.get(t, asrAlice, "/velero/assurance/status")
	if st := decodeEnv[AssuranceStatusView](t, rr).Data; st.Open.Total != 0 || st.Open.ByCondition["collection_unknown"] != 0 {
		t.Fatalf("alice status counts a cluster row: %+v", st.Open)
	}
	if got := a.exceptions(t, asrAdmin, ""); len(got.Data) != 1 || got.Data[0].Subject.Kind != "cluster" {
		t.Fatalf("admin rows = %+v, want the cluster row", got.Data)
	}
}

func TestAssuranceExceptions_NonAdminNeverReceivesPrivilegedDetailKeys(t *testing.T) {
	a := newAssuranceAPI(t)
	p := a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")
	a.open(t, p, store.ConditionLocationUnavailable, "uid-daily", map[string]any{
		"lastOutcome":      "failure",
		"expectedRunKnown": false,
		"storageLocation":  "SECRET-LOCATION",
		"bslMessage":       "SECRET-BUCKET arn:aws:s3:::SECRET-ARN",
		"failureReason":    "SECRET-REASON",
		"bucket":           "UNLISTED-KEY-VALUE",
	})
	a.allow(veleroNamespace)

	rr := a.get(t, asrAlice, "/velero/assurance/exceptions")
	wantStatus(t, rr, http.StatusOK)
	body := rr.Body.String()
	for _, leak := range []string{"SECRET-", "storageLocation", "bslMessage", "failureReason", "UNLISTED-KEY-VALUE", `"bucket"`} {
		if strings.Contains(body, leak) {
			t.Errorf("non-admin body contains %q: %s", leak, body)
		}
	}
	views := decodeEnv[[]AssuranceExceptionView](t, rr).Data
	if len(views) != 1 || views[0].Detail.LastOutcome != "failure" || views[0].ExpectedRunNote != "" {
		t.Fatalf("non-admin view = %+v; want the allow-listed fields and no expected-run note on a location exception", views)
	}

	adm := a.get(t, asrAdmin, "/velero/assurance/exceptions")
	abody := adm.Body.String()
	for _, want := range []string{"SECRET-LOCATION", "SECRET-ARN", "SECRET-REASON"} {
		if !strings.Contains(abody, want) {
			t.Errorf("admin body lacks %q", want)
		}
	}
	if strings.Contains(abody, "UNLISTED-KEY-VALUE") {
		t.Errorf("admin body carries a key outside the allow-list: %s", abody)
	}
}

func TestAssuranceExceptions_UserWithNamespaceButNoVeleroAccessSeesNothing(t *testing.T) {
	a := newAssuranceAPI(t)
	pa := a.policy(t, store.ScopeNamespace, "team-a", "")
	a.open(t, pa, store.ConditionOverdue, "", nil)
	// Alice holds a role in team-a (the predicate would allow pods there),
	// but no velero.io grant: the SAR for list schedules is denied.
	a.allow()

	got := a.exceptions(t, asrAlice, "")
	if len(got.Data) != 0 || got.Metadata.Total != 0 {
		t.Fatalf("alice sees %d rows (total %d), want none", len(got.Data), got.Metadata.Total)
	}
	if a.checks == 0 {
		t.Fatal("no access check was made; the filter is not consulting the SAR")
	}
}

func TestAssuranceExceptions_RevokedPermissionBetweenRequestsChangesResult(t *testing.T) {
	a := newAssuranceAPI(t)
	pa := a.policy(t, store.ScopeNamespace, "team-a", "")
	a.open(t, pa, store.ConditionOverdue, "", nil)

	a.allow("team-a")
	if got := a.exceptions(t, asrAlice, ""); len(got.Data) != 1 {
		t.Fatalf("granted: %d rows, want 1", len(got.Data))
	}
	a.allow()
	if got := a.exceptions(t, asrAlice, ""); len(got.Data) != 0 || got.Metadata.Total != 0 {
		t.Fatalf("revoked: %d rows (total %d), want none", len(got.Data), got.Metadata.Total)
	}
}

func TestAssuranceExceptions_AccessCheckFailureFailsTheRequest(t *testing.T) {
	a := newAssuranceAPI(t)
	pa := a.policy(t, store.ScopeNamespace, "team-a", "")
	a.open(t, pa, store.ConditionOverdue, "", nil)
	a.h.AccessChecker = resources.NewErroringAccessChecker(fmt.Errorf("apiserver timeout"))

	rr := a.get(t, asrAlice, "/velero/assurance/exceptions")
	// A check that could not be made is not a denial: a 200 with no rows
	// would tell alice her namespaces are healthy.
	wantStatus(t, rr, http.StatusInternalServerError)
	if strings.Contains(rr.Body.String(), "apiserver timeout") {
		t.Errorf("internal error text leaked: %s", rr.Body.String())
	}
}

func TestAssuranceExceptions_NoUserReturns401(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	for _, path := range []string{"/velero/assurance/exceptions", "/velero/assurance/status"} {
		wantStatus(t, a.get(t, nil, path), http.StatusUnauthorized)
	}
}

func TestAssuranceExceptions_MalformedQueryRejected(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	for _, q := range []string{"?state=opne", "?limit=0", "?limit=501", "?limit=x", "?offset=-1"} {
		t.Run(q, func(t *testing.T) {
			wantReason(t, a.get(t, asrAdmin, "/velero/assurance/exceptions"+q), http.StatusBadRequest, "invalid_query")
		})
	}
}

func TestAssuranceExceptions_RequestCancellationReturnsWithoutPanic(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, user := range []*auth.User{asrAdmin, asrAlice} {
		for _, path := range []string{"/velero/assurance/exceptions", "/velero/assurance/status", "/velero/assurance/policies"} {
			rr := a.serve(t, asrReq{user: user, method: http.MethodGet, path: path, ctx: ctx})
			if rr.Code == http.StatusOK {
				t.Errorf("%s as %s: 200 from a cancelled request: %s", path, user.Username, rr.Body.String())
			}
		}
	}
}

func TestAssurance_RemoteClusterIsNotImplemented(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	// The collector's own configured cluster id is a remote selection too: the
	// router treats any id but "local" as remote, so answering it from the
	// local store would serve local data under a remote cluster's name.
	for _, cluster := range []string{remoteCluster, a.clusterID} {
		rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodGet, path: "/velero/assurance/exceptions", cluster: cluster})
		wantReason(t, rr, http.StatusNotImplemented, "remote_assurance_unsupported")
	}
	// A remote selection is refused before the database is consulted.
	b := newAssuranceAPIWith(t, nil)
	wantReason(t, b.serve(t, asrReq{user: asrAdmin, method: http.MethodGet, path: "/velero/assurance/status", cluster: remoteCluster}),
		http.StatusNotImplemented, "remote_assurance_unsupported")
}

// ---------------------------------------------------------------------------
// Exceptions and policies: subjects that are gone
// ---------------------------------------------------------------------------

func TestAssurancePolicies_DeletedScheduleUIDStillRendersException(t *testing.T) {
	now := time.Now().UTC()
	// The live "daily" schedule is a recreation: same name, new uid.
	a := newAssuranceAPI(t, asrSchedule("daily", "uid-new", "0 2 * * *", false, now.Add(-time.Hour)))
	p := a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")
	gone := a.policy(t, store.ScopeSchedule, veleroNamespace, "gone")
	a.open(t, p, store.ConditionOverdue, "uid-old", map[string]any{"lastOutcome": "success", "expectedRunKnown": true})

	got := a.exceptions(t, asrAdmin, "")
	if len(got.Data) != 1 {
		t.Fatalf("rows = %d, want the old-uid exception", len(got.Data))
	}
	v := got.Data[0]
	if v.Subject.UID != "uid-old" || v.SubjectStatus == nil || *v.SubjectStatus != "not_found" || v.SubjectNote != "schedule not found" {
		t.Fatalf("view = %+v; want uid-old rendered as schedule not found", v)
	}
	if v.ExpectedRunNote != "" {
		t.Errorf("expectedRunNote = %q with expectedRunKnown true; want empty", v.ExpectedRunNote)
	}

	rr := a.get(t, asrAdmin, "/velero/assurance/policies")
	wantStatus(t, rr, http.StatusOK)
	byID := map[string]AssurancePolicyView{}
	for _, pv := range decodeEnv[[]AssurancePolicyView](t, rr).Data {
		byID[pv.ID] = pv
	}
	if s := byID[p.ID.String()].ScheduleStatus; s == nil || *s != "found" {
		t.Errorf("policy for the live schedule: scheduleStatus %v, want found", s)
	}
	if pv := byID[gone.ID.String()]; pv.ScheduleStatus == nil || *pv.ScheduleStatus != "not_found" || pv.ScheduleNote != "schedule not found" {
		t.Errorf("policy for a missing schedule = %+v, want schedule not found", pv)
	}
}

func TestAssurancePolicies_ScheduleExistenceUnknownWhenVeleroNotObserved(t *testing.T) {
	a := newAssuranceAPI(t)
	a.h.Discoverer = &Discoverer{logger: a.h.Logger, status: VeleroStatus{Detected: false, LastChecked: time.Now().UTC()}}
	a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")

	rr := a.get(t, asrAdmin, "/velero/assurance/policies")
	wantStatus(t, rr, http.StatusOK)
	views := decodeEnv[[]AssurancePolicyView](t, rr).Data
	if len(views) != 1 || views[0].ScheduleStatus == nil || *views[0].ScheduleStatus != "unknown" || views[0].ScheduleNote != "" {
		t.Fatalf("views = %+v; an unobserved Velero must be unknown, never schedule not found", views)
	}
}

func TestAssurancePolicies_DisabledPolicyIsReturnedButProducesNoExceptions(t *testing.T) {
	a := newAssuranceAPI(t, overdueCluster(time.Now().UTC())...)
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies",
		body: fmt.Sprintf(`{"scopeKind":"schedule","scopeNamespace":%q,"scopeName":"daily","maxAgeSeconds":86400,"enabled":false}`, veleroNamespace)})
	wantStatus(t, rr, http.StatusCreated)

	a.svc.tick(t.Context())
	if s := a.svc.Snapshot(); s.LastError != "" {
		t.Fatalf("tick failed: %s", s.LastError)
	}

	list := a.get(t, asrAdmin, "/velero/assurance/policies")
	views := decodeEnv[[]AssurancePolicyView](t, list).Data
	if len(views) != 1 || views[0].Enabled {
		t.Fatalf("policies = %+v, want the one disabled policy", views)
	}
	if got := a.exceptions(t, asrAdmin, ""); len(got.Data) != 0 {
		t.Fatalf("a disabled policy opened %d exceptions over an overdue schedule", len(got.Data))
	}
}

// ---------------------------------------------------------------------------
// Policies: access and validation
// ---------------------------------------------------------------------------

func TestAssurancePolicies_NonAdminForbidden(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	id := uuid.NewString()
	for _, rq := range []asrReq{
		{method: http.MethodGet, path: "/velero/assurance/policies"},
		{method: http.MethodPost, path: "/velero/assurance/policies", body: `{}`},
		{method: http.MethodPut, path: "/velero/assurance/policies/" + id, body: `{}`},
		{method: http.MethodDelete, path: "/velero/assurance/policies/" + id + "?confirm=true"},
	} {
		rq.user = asrAlice
		t.Run(rq.method, func(t *testing.T) {
			wantStatus(t, a.serve(t, rq), http.StatusForbidden)
		})
	}
}

func fieldErrorFields(t *testing.T, e envelope[json.RawMessage]) []string {
	t.Helper()
	raw, _ := json.Marshal(e.Error.Extra["fieldErrors"])
	var fes []assuranceFieldError
	if err := json.Unmarshal(raw, &fes); err != nil || len(fes) == 0 {
		t.Fatalf("fieldErrors = %s (%v), want a non-empty list", raw, err)
	}
	out := make([]string, 0, len(fes))
	for _, fe := range fes {
		out = append(out, fe.Field)
	}
	return out
}

func TestAssurancePolicies_InvalidThresholdRejected(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	for name, tc := range map[string]struct {
		body  string
		field string
	}{
		"negative grace":       {`{"scopeKind":"cluster","maxAgeSeconds":3600,"graceSeconds":-1}`, "graceSeconds"},
		"unknown partial":      {`{"scopeKind":"cluster","maxAgeSeconds":3600,"treatPartialAs":"maybe"}`, "treatPartialAs"},
		"max age overflow":     {`{"scopeKind":"cluster","maxAgeSeconds":4294967296}`, "maxAgeSeconds"},
		"missing max age":      {`{"scopeKind":"cluster"}`, "maxAgeSeconds"},
		"unknown scope":        {`{"scopeKind":"galaxy","maxAgeSeconds":3600}`, "scopeKind"},
		"schedule w/o name":    {`{"scopeKind":"schedule","scopeNamespace":"velero","maxAgeSeconds":3600}`, "scopeName"},
		"bad namespace":        {`{"scopeKind":"namespace","scopeNamespace":"Not_A_NS","maxAgeSeconds":3600}`, "scopeNamespace"},
		"cluster w/ namespace": {`{"scopeKind":"cluster","scopeNamespace":"velero","maxAgeSeconds":3600}`, "scopeNamespace"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies", body: tc.body})
			e := wantReason(t, rr, http.StatusBadRequest, "invalid_policy")
			if fields := fieldErrorFields(t, e); !slices.Contains(fields, tc.field) {
				t.Fatalf("field errors %v do not name %s", fields, tc.field)
			}
		})
	}

	for name, body := range map[string]string{
		"unknown field": `{"scopeKind":"cluster","maxAgeSeconds":3600,"maxAge":"1d"}`,
		"trailing data": `{"scopeKind":"cluster","maxAgeSeconds":3600} {}`,
		"not json":      `max_age=1d`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies", body: body})
			wantReason(t, rr, http.StatusBadRequest, "invalid_body")
		})
	}
}

func TestAssurancePolicies_MaxAgeBelowFloorRejectedWithFieldError(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies",
		body: `{"scopeKind":"cluster","maxAgeSeconds":299}`})
	e := wantReason(t, rr, http.StatusBadRequest, "invalid_policy")
	if fields := fieldErrorFields(t, e); !slices.Equal(fields, []string{"maxAgeSeconds"}) {
		t.Fatalf("field errors %v, want exactly maxAgeSeconds", fields)
	}
	if !strings.Contains(e.Error.Message, "300") {
		t.Errorf("message %q does not state the floor", e.Error.Message)
	}
}

func TestAssurancePolicies_MalformedUUIDReturns400(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	id := uuid.New()
	for _, raw := range []string{"not-a-uuid", "{" + id.String() + "}", "urn:uuid:" + id.String(), strings.ReplaceAll(id.String(), "-", ""), uuid.Nil.String()} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+raw, func(t *testing.T) {
				rr := a.serve(t, asrReq{user: asrAdmin, method: method, path: "/velero/assurance/policies/" + raw + "?confirm=true", body: `{"revision":1}`})
				wantReason(t, rr, http.StatusBadRequest, "invalid_id")
			})
		}
	}
}

func TestAssurancePolicyWrite_RequiresCSRFHeader(t *testing.T) {
	a := newAssuranceAPINoDB(t)
	id := uuid.NewString()
	for _, rq := range []asrReq{
		{method: http.MethodPost, path: "/velero/assurance/policies", body: `{}`},
		{method: http.MethodPut, path: "/velero/assurance/policies/" + id, body: `{}`},
		{method: http.MethodDelete, path: "/velero/assurance/policies/" + id + "?confirm=true"},
	} {
		rq.user, rq.noCSRF = asrAdmin, true
		t.Run(rq.method, func(t *testing.T) {
			wantStatus(t, a.serve(t, rq), http.StatusForbidden)
		})
	}
}

// ---------------------------------------------------------------------------
// Policies: persistence
// ---------------------------------------------------------------------------

func (a *assuranceAPI) create(t *testing.T, body string) AssurancePolicyView {
	t.Helper()
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies", body: body})
	wantStatus(t, rr, http.StatusCreated)
	return decodeEnv[AssurancePolicyView](t, rr).Data
}

func TestAssurancePolicies_CreateAppliesDefaults(t *testing.T) {
	a := newAssuranceAPI(t)
	v := a.create(t, `{"scopeKind":"namespace","scopeNamespace":"team-a","maxAgeSeconds":86400}`)
	if v.GraceSeconds != 3600 || v.TreatPartialAs != "failure" || !v.AlertOnPaused || !v.Enabled || v.Revision != 1 || v.CreatedBy != "admin" {
		t.Fatalf("created = %+v; want defaults grace 3600, failure, alertOnPaused, enabled, revision 1, createdBy admin", v)
	}
	if v.ScheduleStatus != nil {
		t.Errorf("namespace-scope policy carries scheduleStatus %v", *v.ScheduleStatus)
	}
}

func TestAssurancePolicies_DuplicateScopeReturns409(t *testing.T) {
	a := newAssuranceAPI(t)
	body := `{"scopeKind":"namespace","scopeNamespace":"team-a","maxAgeSeconds":86400}`
	a.create(t, body)
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPost, path: "/velero/assurance/policies", body: body})
	wantReason(t, rr, http.StatusConflict, "policy_exists")
}

func TestAssurancePolicies_StaleRevisionReturns409(t *testing.T) {
	a := newAssuranceAPI(t)
	v := a.create(t, `{"scopeKind":"cluster","maxAgeSeconds":86400}`)
	path := "/velero/assurance/policies/" + v.ID

	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"maxAgeSeconds":7200,"enabled":false}`})
	wantStatus(t, rr, http.StatusOK)
	up := decodeEnv[AssurancePolicyView](t, rr).Data
	if up.Revision != 2 || up.MaxAgeSeconds != 7200 || up.Enabled || up.GraceSeconds != 3600 || !up.AlertOnPaused || up.UpdatedBy != "admin" {
		t.Fatalf("updated = %+v; want revision 2, new thresholds, untouched fields kept", up)
	}

	rr = a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"maxAgeSeconds":600}`})
	wantReason(t, rr, http.StatusConflict, "revision_conflict")

	rr = a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: "/velero/assurance/policies/" + uuid.NewString(), body: `{"revision":1}`})
	wantReason(t, rr, http.StatusNotFound, "policy_not_found")

	rr = a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"maxAgeSeconds":600}`})
	wantReason(t, rr, http.StatusBadRequest, "invalid_policy")
}

func TestAssurancePolicies_ScopeIsImmutable(t *testing.T) {
	a := newAssuranceAPI(t)
	v := a.create(t, `{"scopeKind":"namespace","scopeNamespace":"team-a","maxAgeSeconds":86400}`)
	path := "/velero/assurance/policies/" + v.ID

	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"scopeNamespace":"team-b"}`})
	wantReason(t, rr, http.StatusBadRequest, "scope_immutable")

	// Echoing the stored scope back is not a change.
	rr = a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path,
		body: `{"revision":1,"scopeKind":"namespace","scopeNamespace":"team-a","scopeName":"","maxAgeSeconds":7200}`})
	wantStatus(t, rr, http.StatusOK)
	if got := decodeEnv[AssurancePolicyView](t, rr).Data; got.ScopeNamespace != "team-a" || got.MaxAgeSeconds != 7200 {
		t.Fatalf("updated = %+v", got)
	}
}

func TestAssurancePolicyWrite_IsAudited(t *testing.T) {
	a := newAssuranceAPI(t)
	v := a.create(t, `{"scopeKind":"namespace","scopeNamespace":"team-a","maxAgeSeconds":86400}`)
	e := a.audit.last(t)
	if e.Action != audit.ActionCreate || e.ResourceKind != "BackupAssurancePolicy" || e.ResourceNamespace != "team-a" ||
		e.ResourceName != v.ID || e.Result != audit.ResultSuccess || e.User != "admin" || !strings.Contains(e.Detail, "scope=namespace team-a") {
		t.Fatalf("create audit = %+v", e)
	}

	path := "/velero/assurance/policies/" + v.ID
	wantStatus(t, a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"maxAgeSeconds":7200}`}), http.StatusOK)
	if e := a.audit.last(t); e.Action != audit.ActionUpdate || e.ResourceName != v.ID || e.Result != audit.ResultSuccess {
		t.Fatalf("update audit = %+v", e)
	}
	wantStatus(t, a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"maxAgeSeconds":7200}`}), http.StatusConflict)
	if e := a.audit.last(t); e.Action != audit.ActionUpdate || e.Result != audit.ResultFailure {
		t.Fatalf("refused update audit = %+v, want a failure entry", e)
	}

	pid, _ := uuid.Parse(v.ID)
	p, err := a.st.GetPolicy(t.Context(), a.clusterID, pid)
	if err != nil {
		t.Fatal(err)
	}
	a.open(t, *p, store.ConditionOverdue, "", nil)
	// Another policy's open exceptions are not this delete's to discard.
	other := a.policy(t, store.ScopeNamespace, "team-b", "")
	a.open(t, other, store.ConditionOverdue, "", nil)
	a.open(t, other, store.ConditionFailed, "", nil)

	// Without confirm=true nothing is deleted, nothing is audited, and the
	// refusal says how many open exceptions the delete would discard.
	entries := len(a.audit.entries)
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodDelete, path: path})
	ref := wantReason(t, rr, http.StatusBadRequest, "confirmation_required")
	if n, _ := ref.Error.Extra["openExceptions"].(float64); n != 1 {
		t.Fatalf("confirmation extra = %+v, want openExceptions 1", ref.Error.Extra)
	}
	if len(a.audit.entries) != entries {
		t.Fatalf("an unconfirmed delete was audited")
	}
	if _, err := a.st.GetPolicy(t.Context(), a.clusterID, pid); err != nil {
		t.Fatalf("unconfirmed delete removed the policy: %v", err)
	}

	rr = a.serve(t, asrReq{user: asrAdmin, method: http.MethodDelete, path: path + "?confirm=true"})
	wantStatus(t, rr, http.StatusOK)
	if e := a.audit.last(t); e.Action != audit.ActionDelete || e.ResourceName != v.ID || e.Result != audit.ResultSuccess ||
		!strings.Contains(e.Detail, "open at delete: 1") {
		t.Fatalf("delete audit = %+v", e)
	}
	if got := a.exceptions(t, asrAdmin, ""); len(got.Data) != 2 || subjectNamespaces(got.Data)[0] != "team-b" {
		t.Fatalf("after the delete, exceptions = %+v; want only the other policy's two", got.Data)
	}
	wantReason(t, a.serve(t, asrReq{user: asrAdmin, method: http.MethodDelete, path: path + "?confirm=true"}), http.StatusNotFound, "policy_not_found")
}

// ---------------------------------------------------------------------------
// Unavailable database
// ---------------------------------------------------------------------------

var assuranceRoutes = []asrReq{
	{method: http.MethodGet, path: "/velero/assurance/status"},
	{method: http.MethodGet, path: "/velero/assurance/exceptions"},
	{method: http.MethodGet, path: "/velero/assurance/policies"},
	{method: http.MethodPost, path: "/velero/assurance/policies", body: `{}`},
	{method: http.MethodPut, path: "/velero/assurance/policies/0f6a0000-0000-4000-8000-000000000001", body: `{}`},
	{method: http.MethodDelete, path: "/velero/assurance/policies/0f6a0000-0000-4000-8000-000000000001?confirm=true"},
}

func TestAssurance_NilAssuranceServiceReturns503WithReason(t *testing.T) {
	a := newAssuranceAPIWith(t, nil)
	for _, rq := range assuranceRoutes {
		rq.user = asrAdmin
		t.Run(rq.method+" "+rq.path, func(t *testing.T) {
			e := wantReason(t, a.serve(t, rq), http.StatusServiceUnavailable, "database_unavailable")
			if e.Error.Extra["capability"] != "backup-assurance" {
				t.Errorf("extra = %+v, want capability backup-assurance", e.Error.Extra)
			}
		})
	}
}

func TestAssurance_NilAssuranceServiceNeverReturns404(t *testing.T) {
	for name, wire := range map[string]func(*assuranceAPI){
		"no store":     func(a *assuranceAPI) { a.h.AssuranceStore = nil },
		"no collector": func(a *assuranceAPI) { a.h.Assurance = nil },
		"neither":      func(a *assuranceAPI) { a.h.Assurance, a.h.AssuranceStore = nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			a := newAssuranceAPINoDB(t)
			wire(a)
			for _, rq := range assuranceRoutes {
				rq.user = asrAdmin
				if rr := a.serve(t, rq); rr.Code != http.StatusServiceUnavailable {
					t.Errorf("%s %s: status %d, want 503 (a 404 reads as an unregistered route)", rq.method, rq.path, rr.Code)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func TestAssuranceStatus_ReportsCollectionFreshnessAndLeaseHolder(t *testing.T) {
	a := newAssuranceAPI(t, overdueCluster(time.Now().UTC())...)
	a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")
	a.svc.tick(t.Context())
	if s := a.svc.Snapshot(); s.LastError != "" {
		t.Fatalf("tick failed: %s", s.LastError)
	}

	rr := a.get(t, asrAdmin, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	st := decodeEnv[AssuranceStatusView](t, rr).Data
	if !st.Enabled || st.Collection != "ok" || st.CollectionSource != "this_replica" || st.PolicyCount != 1 {
		t.Fatalf("status = %+v; want enabled ok collection judged by this replica", st)
	}
	if st.Open.Total != 1 || st.Open.ByCondition["overdue"] != 1 {
		t.Fatalf("open = %+v, want the overdue exception", st.Open)
	}
	rt := st.Runtime
	if rt == nil || rt.Holder != "holder-a" || !rt.LeaseHeld || rt.Lease == nil || rt.Lease.Holder != "holder-a" || rt.Lease.Expired ||
		rt.LastCollection != "ok" || rt.LastRunAt == nil {
		t.Fatalf("runtime = %+v; want holder-a holding an unexpired lease after an ok run", rt)
	}
	if rt.DeliveryBacklog.Pending != 0 || rt.DeliveryBacklog.Failed != 0 {
		t.Errorf("backlog = %+v, want drained", rt.DeliveryBacklog)
	}

	a.allow(veleroNamespace)
	rr = a.get(t, asrAlice, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	body := rr.Body.String()
	for _, leak := range []string{"holder-a", `"runtime"`, "lastRunAt", "lastCollection"} {
		if strings.Contains(body, leak) {
			t.Fatalf("non-admin status carries runtime detail %q: %s", leak, body)
		}
	}
	if st := decodeEnv[AssuranceStatusView](t, rr).Data; st.Collection != "ok" || st.Open.Total != 1 || st.PolicyCount != 1 {
		t.Fatalf("non-admin status = %+v", st)
	}
}

func TestAssuranceStatus_UnknownWhenLastCollectionFailed(t *testing.T) {
	a := newAssuranceAPI(t)
	a.h.Discoverer = &Discoverer{logger: a.h.Logger, status: VeleroStatus{Detected: false, LastChecked: time.Now().UTC()}}
	a.svc.disc = a.h.Discoverer
	a.policy(t, store.ScopeSchedule, veleroNamespace, "daily")
	a.svc.tick(t.Context())

	rr := a.get(t, asrAdmin, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	st := decodeEnv[AssuranceStatusView](t, rr).Data
	if st.Collection != "unknown" || st.Runtime == nil || st.Runtime.LastCollection != "failed" {
		t.Fatalf("status = %+v; a failed collection must read unknown", st)
	}
	if st.Open.ByCondition["collection_unknown"] != 1 {
		t.Errorf("open = %+v, want the cluster collection_unknown exception for admin", st.Open)
	}
}

// TestAssuranceStatus_FollowerReplicaJudgesFromTheLeaseHolder: a replica that
// does not hold the lease has never collected, but another replica holds a
// live lease. Its status must follow the durable state that holder keeps,
// not report unknown because its own snapshot is empty.
func TestAssuranceStatus_FollowerReplicaJudgesFromTheLeaseHolder(t *testing.T) {
	a := newAssuranceAPI(t)
	pc := a.policy(t, store.ScopeCluster, "", "")
	if _, err := a.st.AcquireOrRenewLease(t.Context(), a.clusterID, "holder-b", assuranceLeaseTTL); err != nil {
		t.Fatalf("AcquireOrRenewLease: %v", err)
	}

	status := func() AssuranceStatusView {
		t.Helper()
		rr := a.get(t, asrAdmin, "/velero/assurance/status")
		wantStatus(t, rr, http.StatusOK)
		return decodeEnv[AssuranceStatusView](t, rr).Data
	}
	if st := status(); st.Collection != "ok" || st.CollectionSource != "lease_holder" || st.Runtime.LeaseHeld || st.Runtime.Lease.Holder != "holder-b" {
		t.Fatalf("follower status = %+v (runtime %+v); want ok judged from holder-b's lease", st, st.Runtime)
	}

	// The holder's collection fails: it leaves collection_unknown open.
	a.open(t, pc, store.ConditionCollectionUnknown, "", nil)
	if st := status(); st.Collection != "unknown" || st.CollectionSource != "lease_holder" {
		t.Fatalf("follower status = %+v; an open collection_unknown must read unknown", st)
	}
}

func TestAssuranceStatus_EmptyWithoutPolicies(t *testing.T) {
	a := newAssuranceAPI(t)
	rr := a.get(t, asrAdmin, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	st := decodeEnv[AssuranceStatusView](t, rr).Data
	if st.Collection != "empty" || st.CollectionSource != "none" || st.PolicyCount != 0 || st.Runtime == nil || st.Runtime.Lease != nil {
		t.Fatalf("status = %+v; want empty, no policies, null lease", st)
	}
}

// TestAssuranceStatus_NonAdminPolicyCountIsScopedToVisibleNamespaces: the
// policy count must not reveal policies in namespaces the caller cannot see,
// nor cluster-scope policies.
func TestAssuranceStatus_NonAdminPolicyCountIsScopedToVisibleNamespaces(t *testing.T) {
	a := newAssuranceAPI(t)
	a.policy(t, store.ScopeNamespace, "team-a", "")
	a.policy(t, store.ScopeNamespace, "team-b", "")
	a.policy(t, store.ScopeCluster, "", "")

	a.allow("team-a")
	rr := a.get(t, asrAlice, "/velero/assurance/status")
	wantStatus(t, rr, http.StatusOK)
	if st := decodeEnv[AssuranceStatusView](t, rr).Data; st.PolicyCount != 1 {
		t.Fatalf("alice policyCount = %d, want 1 (team-a only)", st.PolicyCount)
	}

	a.allow()
	rr = a.get(t, asrAlice, "/velero/assurance/status")
	if st := decodeEnv[AssuranceStatusView](t, rr).Data; st.PolicyCount != 0 || st.Collection != "empty" {
		t.Fatalf("alice with no visible policy = %+v; want 0 and empty", st)
	}

	rr = a.get(t, asrAdmin, "/velero/assurance/status")
	if st := decodeEnv[AssuranceStatusView](t, rr).Data; st.PolicyCount != 3 {
		t.Fatalf("admin policyCount = %d, want 3", st.PolicyCount)
	}
}

func TestAssuranceCollectionState_Vocabulary(t *testing.T) {
	now := time.Now()
	ok := AssuranceRuntimeStatus{Enabled: true, Holder: "a", LeaseHeld: true, LastCollection: CollectionOK, LastRunAt: now.Add(-time.Minute)}
	follower := AssuranceRuntimeStatus{Enabled: true, Holder: "b"}
	liveOther := &store.AssuranceLease{Holder: "a", ExpiresAt: now.Add(time.Minute)}
	expiredOther := &store.AssuranceLease{Holder: "a", Expired: true}
	for name, tc := range map[string]struct {
		snap        AssuranceRuntimeStatus
		lease       *store.AssuranceLease
		policies    int
		unknownOpen bool
		want, src   string
	}{
		"disabled":                     {AssuranceRuntimeStatus{}, nil, 3, false, "unavailable", "none"},
		"no policies":                  {ok, nil, 0, false, "empty", "none"},
		"never collected":              {AssuranceRuntimeStatus{Enabled: true}, nil, 1, false, "unknown", "none"},
		"failed collection":            {AssuranceRuntimeStatus{Enabled: true, LastCollection: CollectionFailed, LastRunAt: now}, nil, 1, false, "unknown", "this_replica"},
		"degraded":                     {AssuranceRuntimeStatus{Enabled: true, LastCollection: CollectionDegraded, LastRunAt: now}, nil, 1, false, "unknown", "this_replica"},
		"stale":                        {AssuranceRuntimeStatus{Enabled: true, LastCollection: CollectionOK, LastRunAt: now.Add(-4 * time.Minute)}, nil, 1, false, "stale", "this_replica"},
		"fresh":                        {ok, nil, 1, false, "ok", "this_replica"},
		"follower, holder healthy":     {follower, liveOther, 1, false, "ok", "lease_holder"},
		"follower, holder failing":     {follower, liveOther, 1, true, "unknown", "lease_holder"},
		"follower, lease expired":      {follower, expiredOther, 1, false, "unknown", "none"},
		"holder never trusts follower": {ok, liveOther, 1, true, "ok", "this_replica"},
	} {
		t.Run(name, func(t *testing.T) {
			got, src := assuranceCollectionState(tc.snap, tc.lease, tc.policies, tc.unknownOpen, now)
			if got != tc.want || src != tc.src {
				t.Fatalf("state = %q/%q, want %q/%q", got, src, tc.want, tc.src)
			}
		})
	}
}

func TestAssuranceProjectException_ExpectedRunNoteOnlyOnFreshnessConditions(t *testing.T) {
	for cond, want := range map[store.AssuranceCondition]string{
		store.ConditionOverdue:             "not computable",
		store.ConditionNeverRun:            "not computable",
		store.ConditionFailed:              "",
		store.ConditionPartiallyFailed:     "",
		store.ConditionPaused:              "",
		store.ConditionLocationUnavailable: "",
		store.ConditionCollectionUnknown:   "",
	} {
		e := store.BackupAssuranceException{SubjectKind: store.ScopeSchedule, Condition: cond, Detail: []byte(`{"expectedRunKnown":false}`)}
		if got := projectException(e, true).ExpectedRunNote; got != want {
			t.Errorf("%s: expectedRunNote = %q, want %q", cond, got, want)
		}
	}
	known := store.BackupAssuranceException{SubjectKind: store.ScopeSchedule, Condition: store.ConditionOverdue, Detail: []byte(`{"expectedRunKnown":true}`)}
	if got := projectException(known, true).ExpectedRunNote; got != "" {
		t.Errorf("known expected run: note = %q, want empty", got)
	}
}

func TestAssuranceProjectDetail_AllowListAndPrivilege(t *testing.T) {
	raw := []byte(`{"lastOutcome":"partial","expectedRunKnown":true,"suppressedBy":"paused","resolutionReason":"subject_absent",
		"storageLocation":"s","bslMessage":"m","failureReason":"f","bucket":"b","arn":"a"}`)
	user := projectDetail(raw, false)
	if user.StorageLocation != "" || user.BSLMessage != "" || user.FailureReason != "" {
		t.Fatalf("non-admin projection kept privileged fields: %+v", user)
	}
	if user.LastOutcome != "partial" || !user.ExpectedRunKnown || user.SuppressedBy != "paused" || user.ResolutionReason != "subject_absent" {
		t.Fatalf("non-admin projection dropped allow-listed fields: %+v", user)
	}
	if adm := projectDetail(raw, true); adm.StorageLocation != "s" || adm.BSLMessage != "m" || adm.FailureReason != "f" {
		t.Fatalf("admin projection = %+v", adm)
	}
	if bad := projectDetail([]byte(`not json`), true); bad != (AssuranceDetailView{}) {
		t.Fatalf("undecodable detail projected to %+v, want empty", bad)
	}
}

// ---------------------------------------------------------------------------
// Release boundary
// ---------------------------------------------------------------------------

// TestAssuranceEndpoints_NeverWriteToKubernetes drives every assurance
// endpoint, including all three policy writes, and asserts the cluster saw
// reads only: no create, update, patch or delete of any object, so no
// Restore and no DeleteBackupRequest. The route-table half of this boundary
// is TestAssuranceRoutes_ExposeNoMutatingClusterOperation in package server.
func TestAssuranceEndpoints_NeverWriteToKubernetes(t *testing.T) {
	a := newAssuranceAPI(t, overdueCluster(time.Now().UTC())...)
	v := a.create(t, fmt.Sprintf(`{"scopeKind":"schedule","scopeNamespace":%q,"scopeName":"daily","maxAgeSeconds":86400}`, veleroNamespace))
	path := "/velero/assurance/policies/" + v.ID
	a.svc.tick(t.Context())

	for _, rq := range []asrReq{
		{method: http.MethodGet, path: "/velero/assurance/status"},
		{method: http.MethodGet, path: "/velero/assurance/exceptions"},
		{method: http.MethodGet, path: "/velero/assurance/policies"},
		{method: http.MethodPut, path: path, body: `{"revision":1,"maxAgeSeconds":7200}`},
		{method: http.MethodDelete, path: path + "?confirm=true"},
	} {
		rq.user = asrAdmin
		if rr := a.serve(t, rq); rr.Code >= 300 {
			t.Fatalf("%s %s: status %d: %s", rq.method, rq.path, rr.Code, rr.Body.String())
		}
	}
	for _, act := range a.localDyn().Actions() {
		switch act.GetVerb() {
		case "list", "get", "watch":
		default:
			t.Errorf("assurance endpoints sent %s %s to the cluster", act.GetVerb(), act.GetResource().Resource)
		}
	}
}

func TestAssurancePolicies_UpdateValidatesMergedValues(t *testing.T) {
	a := newAssuranceAPI(t)
	v := a.create(t, `{"scopeKind":"cluster","maxAgeSeconds":86400}`)
	path := "/velero/assurance/policies/" + v.ID
	for name, tc := range map[string]struct{ body, field string }{
		"below floor":     {`{"revision":1,"maxAgeSeconds":60}`, "maxAgeSeconds"},
		"unknown partial": {`{"revision":1,"treatPartialAs":"sometimes"}`, "treatPartialAs"},
		"negative grace":  {`{"revision":1,"graceSeconds":-5}`, "graceSeconds"},
	} {
		t.Run(name, func(t *testing.T) {
			e := wantReason(t, a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: tc.body}), http.StatusBadRequest, "invalid_policy")
			if fields := fieldErrorFields(t, e); !slices.Contains(fields, tc.field) {
				t.Fatalf("field errors %v do not name %s", fields, tc.field)
			}
		})
	}
	rr := a.serve(t, asrReq{user: asrAdmin, method: http.MethodPut, path: path, body: `{"revision":1,"bogus":true}`})
	wantReason(t, rr, http.StatusBadRequest, "invalid_body")

	// Nothing above changed the policy.
	list := decodeEnv[[]AssurancePolicyView](t, a.get(t, asrAdmin, "/velero/assurance/policies")).Data
	if len(list) != 1 || list[0].Revision != 1 || list[0].MaxAgeSeconds != 86400 {
		t.Fatalf("policy after refused updates = %+v", list)
	}
}

func TestAssuranceExceptions_StateFilterAndPaging(t *testing.T) {
	a := newAssuranceAPI(t)
	p := a.policy(t, store.ScopeNamespace, "team-a", "")
	resolved := a.open(t, p, store.ConditionOverdue, "", nil)
	if ok, err := a.st.ResolveExceptionAndEnqueue(t.Context(), resolved.ID, time.Now().UTC(), store.AssuranceResolutionConditionCleared); err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	a.open(t, p, store.ConditionFailed, "", nil)
	a.open(t, p, store.ConditionNeverRun, "", nil)
	a.allow("team-a")

	for _, user := range []*auth.User{asrAdmin, asrAlice} {
		open := a.exceptions(t, user, "?state=open")
		if len(open.Data) != 2 || open.Metadata.Total != 2 {
			t.Fatalf("%s open = %d rows, total %d; want 2", user.Username, len(open.Data), open.Metadata.Total)
		}
		res := a.exceptions(t, user, "?state=resolved")
		if len(res.Data) != 1 || res.Data[0].State != "resolved" || res.Data[0].Detail.ResolutionReason != "condition_cleared" || res.Data[0].ResolvedAt == nil {
			t.Fatalf("%s resolved = %+v; want the one resolved row with its reason", user.Username, res.Data)
		}
		page2 := a.exceptions(t, user, "?limit=2&offset=2")
		if len(page2.Data) != 1 || page2.Metadata.Total != 3 {
			t.Fatalf("%s page 2 = %d rows, total %d; want 1 of 3", user.Username, len(page2.Data), page2.Metadata.Total)
		}
	}
}
