package incidents

// Handler tests (Release D, U23a). Every test names the Q1 policy clause it
// proves. The stores are in-memory fakes behind the handler's narrow
// interfaces, mirroring the real store's error contract (incidents.go,
// incident_grants.go, incident_evidence.go), so the tests stay hermetic; the
// access checker is a recording fake so a test can see which CLUSTER a check
// was sent to, which resources.NewPredicateAccessChecker cannot expose.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

var fixedNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// fakeStore implements incidentStore, evidenceStore and grantStore in memory
// with the real stores' error contract.
type fakeStore struct {
	incidents map[uuid.UUID]store.IncidentRow
	grants    map[uuid.UUID]map[string]store.IncidentGrantRow
	evidence  map[uuid.UUID][]store.IncidentEvidenceRow
	notes     map[uuid.UUID][]store.IncidentNoteRow
	failWith  error            // when set, every call fails with it
	failOp    map[string]error // per-operation failure, by method name
	busy      bool             // note writes return store.ErrIncidentBusy
	// failGetAfterWrite makes Get fail once after the next Create/Update
	// commits, to exercise the read-back path.
	failGetAfterWrite bool
	getFails          int
	// afterListVisible runs between ListVisible and the role lookups.
	afterListVisible func()
	// beforeUpdate runs once inside the next Update, before its write.
	beforeUpdate func()
	// blockAccessUntilDone makes the recording access checker block until
	// its ctx is done (deterministic budget expiry, no wall clock).
	blockAccessUntilDone bool
	// deadlines records whether each store call's context carried a deadline.
	deadlines []bool
	clock     time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		incidents: map[uuid.UUID]store.IncidentRow{},
		grants:    map[uuid.UUID]map[string]store.IncidentGrantRow{},
		evidence:  map[uuid.UUID][]store.IncidentEvidenceRow{},
		notes:     map[uuid.UUID][]store.IncidentNoteRow{},
		failOp:    map[string]error{},
		clock:     fixedNow,
	}
}

func (f *fakeStore) tick() time.Time {
	f.clock = f.clock.Add(time.Second)
	return f.clock
}

// enter records the call's context and returns the forced failure, if any.
func (f *fakeStore) enter(ctx context.Context, op string) error {
	_, has := ctx.Deadline()
	f.deadlines = append(f.deadlines, has)
	if f.failWith != nil {
		return f.failWith
	}
	return f.failOp[op]
}

func (f *fakeStore) Create(ctx context.Context, r store.IncidentRow) (uuid.UUID, error) {
	if err := f.enter(ctx, "Create"); err != nil {
		return uuid.Nil, err
	}
	if r.OwnerID == "" {
		return uuid.Nil, fmt.Errorf("%w: owner id is required", store.ErrIncidentInvalid)
	}
	for _, v := range []error{
		store.ValidateIncidentTitle(r.Title), store.ValidateIncidentSummary(r.Summary),
		store.ValidateIncidentWindow(r.WindowStart, r.WindowEnd), store.ValidateIncidentRetentionDays(r.RetentionDaysAtCapture),
	} {
		if v != nil {
			return uuid.Nil, v
		}
	}
	if r.ClusterID == "" {
		r.ClusterID = "local"
	}
	r.ID = uuid.New()
	r.Status = store.IncidentStatusOpen
	r.CreatedAt = f.tick()
	r.UpdatedAt = r.CreatedAt
	f.incidents[r.ID] = r
	if f.failGetAfterWrite {
		f.getFails = 1
	}
	return r.ID, nil
}

func (f *fakeStore) Get(ctx context.Context, id uuid.UUID) (*store.IncidentRow, error) {
	if err := f.enter(ctx, "Get"); err != nil {
		return nil, err
	}
	if f.getFails > 0 {
		f.getFails--
		return nil, errors.New("pg: read-back failed")
	}
	r, ok := f.incidents[id]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (f *fakeStore) ListVisible(ctx context.Context, userID string, limit int, cursor string) ([]store.IncidentRow, string, error) {
	if err := f.enter(ctx, "ListVisible"); err != nil {
		return nil, "", err
	}
	if limit < 1 {
		limit = store.IncidentDefaultPageSize
	}
	var after *store.IncidentCursor
	if cursor != "" {
		c, err := store.DecodeIncidentCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		after = &c
	}
	rows := make([]store.IncidentRow, 0)
	for _, r := range f.incidents {
		_, granted := f.grants[r.ID][userID]
		if r.OwnerID != userID && !granted {
			continue
		}
		if after != nil && !(r.CreatedAt.Before(after.CreatedAt) || (r.CreatedAt.Equal(after.CreatedAt) && r.ID.String() < after.ID.String())) {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID.String() > rows[j].ID.String()
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	next := ""
	if len(rows) == limit {
		last := rows[len(rows)-1]
		next = store.EncodeIncidentCursor(store.IncidentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	if f.afterListVisible != nil {
		f.afterListVisible()
	}
	return rows, next, nil
}

func (f *fakeStore) ownershipMiss(id uuid.UUID) error {
	if _, ok := f.incidents[id]; !ok {
		return store.ErrIncidentNotFound
	}
	return store.ErrNotOwner
}

// Update mirrors the store's COALESCE semantics: a nil field keeps the
// column; closed_at follows the resulting status. beforeUpdate runs after
// validation and before the write, so a test can interleave another write.
func (f *fakeStore) Update(ctx context.Context, id uuid.UUID, ownerID string, title, summary, status *string) error {
	if err := f.enter(ctx, "Update"); err != nil {
		return err
	}
	if title != nil {
		if err := store.ValidateIncidentTitle(*title); err != nil {
			return err
		}
	}
	if summary != nil {
		if err := store.ValidateIncidentSummary(*summary); err != nil {
			return err
		}
	}
	if status != nil {
		if err := store.ValidateIncidentStatus(*status); err != nil {
			return err
		}
	}
	if f.beforeUpdate != nil {
		hook := f.beforeUpdate
		f.beforeUpdate = nil
		hook()
	}
	r, ok := f.incidents[id]
	if !ok || r.OwnerID != ownerID {
		return f.ownershipMiss(id)
	}
	if title != nil {
		r.Title = *title
	}
	if summary != nil {
		r.Summary = *summary
	}
	if status != nil {
		r.Status = *status
	}
	r.UpdatedAt = f.tick()
	if r.Status == store.IncidentStatusClosed {
		if r.ClosedAt == nil {
			t := r.UpdatedAt
			r.ClosedAt = &t
		}
	} else {
		r.ClosedAt = nil
	}
	f.incidents[id] = r
	if f.failGetAfterWrite {
		f.getFails = 1
	}
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, id uuid.UUID, ownerID string) error {
	if err := f.enter(ctx, "Delete"); err != nil {
		return err
	}
	r, ok := f.incidents[id]
	if !ok || r.OwnerID != ownerID {
		return f.ownershipMiss(id)
	}
	delete(f.incidents, id)
	delete(f.grants, id)
	delete(f.evidence, id)
	delete(f.notes, id)
	return nil
}

func (f *fakeStore) CreateNote(ctx context.Context, incidentID uuid.UUID, authorID, body string) (store.IncidentNoteRow, error) {
	if err := f.enter(ctx, "CreateNote"); err != nil {
		return store.IncidentNoteRow{}, err
	}
	if f.busy {
		return store.IncidentNoteRow{}, store.ErrIncidentBusy
	}
	if err := store.ValidateIncidentNoteBody(body); err != nil {
		return store.IncidentNoteRow{}, err
	}
	if _, ok := f.incidents[incidentID]; !ok {
		return store.IncidentNoteRow{}, store.ErrIncidentNotFound
	}
	n := store.IncidentNoteRow{ID: uuid.New(), IncidentID: incidentID, AuthorID: authorID, Body: body, Revision: 1, CreatedAt: f.tick()}
	n.UpdatedAt = n.CreatedAt
	f.notes[incidentID] = append(f.notes[incidentID], n)
	return n, nil
}

func (f *fakeStore) ListNotes(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]store.IncidentNoteRow, string, error) {
	if err := f.enter(ctx, "ListNotes"); err != nil {
		return nil, "", err
	}
	switch {
	case limit < 1:
		limit = store.IncidentDefaultPageSize
	case limit > store.IncidentMaxPageSize:
		limit = store.IncidentMaxPageSize
	}
	var after *store.IncidentNoteCursor
	if cursor != "" {
		c, err := store.DecodeIncidentNoteCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		after = &c
	}
	out := make([]store.IncidentNoteRow, 0)
	for _, n := range f.notes[incidentID] { // stored oldest first
		if after != nil && !(n.CreatedAt.After(after.CreatedAt) || (n.CreatedAt.Equal(after.CreatedAt) && n.ID.String() > after.ID.String())) {
			continue
		}
		out = append(out, n)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = store.EncodeIncidentNoteCursor(store.IncidentNoteCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return out, next, nil
}

func (f *fakeStore) findNote(incidentID, noteID uuid.UUID, authorID string) (int, error) {
	for i, n := range f.notes[incidentID] {
		if n.ID != noteID {
			continue
		}
		if n.AuthorID != authorID {
			return -1, store.ErrNotNoteAuthor
		}
		return i, nil
	}
	return -1, store.ErrNoteNotFound
}

func (f *fakeStore) UpdateNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID, body string, expectedRevision int) (store.IncidentNoteRow, error) {
	if err := f.enter(ctx, "UpdateNote"); err != nil {
		return store.IncidentNoteRow{}, err
	}
	if f.busy {
		return store.IncidentNoteRow{}, store.ErrIncidentBusy
	}
	if err := store.ValidateIncidentNoteBody(body); err != nil {
		return store.IncidentNoteRow{}, err
	}
	if expectedRevision < 1 {
		return store.IncidentNoteRow{}, fmt.Errorf("%w: expected revision must be at least 1", store.ErrIncidentInvalid)
	}
	i, err := f.findNote(incidentID, noteID, authorID)
	if err != nil {
		return store.IncidentNoteRow{}, err
	}
	n := f.notes[incidentID][i]
	if n.Revision != expectedRevision {
		return store.IncidentNoteRow{}, &store.NoteRevisionConflictError{Current: n.Revision}
	}
	n.Body = body
	n.Revision++
	n.UpdatedAt = f.tick()
	f.notes[incidentID][i] = n
	return n, nil
}

func (f *fakeStore) DeleteNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID string) error {
	if err := f.enter(ctx, "DeleteNote"); err != nil {
		return err
	}
	if f.busy {
		return store.ErrIncidentBusy
	}
	i, err := f.findNote(incidentID, noteID, authorID)
	if err != nil {
		return err
	}
	f.notes[incidentID] = append(f.notes[incidentID][:i], f.notes[incidentID][i+1:]...)
	return nil
}

func (f *fakeStore) sortedEvidence(incidentID uuid.UUID) []store.IncidentEvidenceRow {
	rows := append([]store.IncidentEvidenceRow(nil), f.evidence[incidentID]...)
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CollectedAt.Equal(rows[j].CollectedAt) {
			return rows[i].CollectedAt.After(rows[j].CollectedAt)
		}
		return rows[i].ID.String() > rows[j].ID.String()
	})
	return rows
}

func (f *fakeStore) ListByIncident(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]store.IncidentEvidenceRow, string, error) {
	if err := f.enter(ctx, "ListByIncident"); err != nil {
		return nil, "", err
	}
	switch {
	case limit < 1:
		limit = store.IncidentDefaultPageSize
	case limit > store.IncidentMaxPageSize:
		limit = store.IncidentMaxPageSize
	}
	var after *store.EvidenceCursor
	if cursor != "" {
		c, err := store.DecodeEvidenceCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		after = &c
	}
	rows := make([]store.IncidentEvidenceRow, 0)
	for _, r := range f.sortedEvidence(incidentID) {
		if after != nil && !(r.CollectedAt.Before(after.CollectedAt) || (r.CollectedAt.Equal(after.CollectedAt) && r.ID.String() < after.ID.String())) {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	next := ""
	if len(rows) == limit {
		last := rows[len(rows)-1]
		next = store.EncodeEvidenceCursor(store.EvidenceCursor{CollectedAt: last.CollectedAt, ID: last.ID})
	}
	return rows, next, nil
}

// ListScopeRowsByIncident mirrors the store: only the authorization columns.
func (f *fakeStore) ListScopeRowsByIncident(ctx context.Context, incidentID uuid.UUID) ([]store.IncidentEvidenceRow, error) {
	if err := f.enter(ctx, "ListScopeRowsByIncident"); err != nil {
		return nil, err
	}
	out := make([]store.IncidentEvidenceRow, 0)
	for _, r := range f.sortedEvidence(incidentID) {
		out = append(out, store.IncidentEvidenceRow{
			ID: r.ID, IncidentID: r.IncidentID, EvidenceKind: r.EvidenceKind, Mode: r.Mode, ClusterID: r.ClusterID,
			APIGroup: r.APIGroup, Resource: r.Resource, SourceKind: r.SourceKind, Namespace: r.Namespace,
			SecretDerived: r.SecretDerived, CollectedAt: r.CollectedAt,
		})
	}
	return out, nil
}

func (f *fakeStore) GetGrant(ctx context.Context, incidentID uuid.UUID, userID string) (*store.IncidentGrantRow, error) {
	if err := f.enter(ctx, "GetGrant"); err != nil {
		return nil, err
	}
	g, ok := f.grants[incidentID][userID]
	if !ok {
		return nil, nil
	}
	return &g, nil
}

func (f *fakeStore) GrantsFor(ctx context.Context, userID string, ids []uuid.UUID) (map[uuid.UUID]store.IncidentGrantRow, error) {
	if err := f.enter(ctx, "GrantsFor"); err != nil {
		return nil, err
	}
	out := map[uuid.UUID]store.IncidentGrantRow{}
	for _, id := range ids {
		if g, ok := f.grants[id][userID]; ok {
			out[id] = g
		}
	}
	return out, nil
}

// accessCall is one recorded CanAccessGroupResource call.
type accessCall struct{ clusterID, group, resource, namespace string }

// fakeAccess answers `get` checks from a predicate that also sees the cluster
// id, and records every call. errFor fails individual scopes; delay makes a
// check stall while honouring the context like the real checker.
type fakeAccess struct {
	allow  func(clusterID, group, resource, namespace string) bool
	err    error
	errFor func(accessCall) error
	// blockUntilDone makes every check wait for ctx to end and return its
	// error: a stalled cluster, without a wall-clock delay.
	blockUntilDone bool
	// onFirstCall runs once, inside the first check, before any wait: a
	// test uses it to end the request so the budget is spent at a point it
	// controls rather than on a timer.
	onFirstCall func()
	calls       []accessCall
}

func allowAll(string, string, string, string) bool { return true }

func (f *fakeAccess) CanAccessGroupResource(ctx context.Context, clusterID, _ string, _ []string, verb, apiGroup, resource, namespace string) (bool, error) {
	if verb != "get" {
		panic("incident evidence filtering must check get, got " + verb)
	}
	call := accessCall{clusterID, apiGroup, resource, namespace}
	f.calls = append(f.calls, call)
	if f.onFirstCall != nil {
		hook := f.onFirstCall
		f.onFirstCall = nil
		hook()
	}
	if f.blockUntilDone {
		<-ctx.Done()
		return false, fmt.Errorf("SelfSubjectAccessReview: %w", ctx.Err())
	}
	if f.err != nil {
		return false, f.err
	}
	if f.errFor != nil {
		if err := f.errFor(call); err != nil {
			return false, err
		}
	}
	return f.allow(clusterID, apiGroup, resource, namespace), nil
}

type fakeAudit struct{ entries []audit.Entry }

func (f *fakeAudit) Log(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAudit) actions() []string {
	out := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, string(e.Action)+":"+string(e.Result))
	}
	return out
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

var (
	alice = &auth.User{ID: "local:alice", Username: "alice", Provider: "local", KubernetesUsername: "alice"}
	bob   = &auth.User{ID: "local:bob", Username: "bob", Provider: "local", KubernetesUsername: "bob"}
	carol = &auth.User{ID: "local:carol", Username: "carol", Provider: "local", KubernetesUsername: "carol"}
	root  = &auth.User{ID: "oidc:root", Username: "root", Provider: "oidc", KubernetesUsername: "root", Roles: []string{"admin"}}
)

type harness struct {
	st     *fakeStore
	access *fakeAccess
	audit  *fakeAudit
	h      *Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hs := &harness{st: newFakeStore(), access: &fakeAccess{allow: allowAll}, audit: &fakeAudit{}}
	hs.h = newHandlerWith(hs.st, hs.st, hs.st, hs.access, hs.audit, nil)
	return hs
}

// seed stores an open incident owned by owner and returns its id.
func (hs *harness) seed(t *testing.T, owner *auth.User) uuid.UUID {
	t.Helper()
	id, err := hs.st.Create(context.Background(), store.IncidentRow{
		OwnerID: owner.ID, Title: "checkout latency", WindowStart: fixedNow.Add(-time.Hour), RetentionDaysAtCapture: 30,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func (hs *harness) grant(id uuid.UUID, grantee *auth.User, canAnnotate bool) {
	if hs.st.grants[id] == nil {
		hs.st.grants[id] = map[string]store.IncidentGrantRow{}
	}
	hs.st.grants[id][grantee.ID] = store.IncidentGrantRow{IncidentID: id, GranteeID: grantee.ID, GrantedBy: "local:alice", CanAnnotate: canAnnotate, CreatedAt: hs.st.tick()}
}

func (hs *harness) revoke(id uuid.UUID, grantee *auth.User) {
	delete(hs.st.grants[id], grantee.ID)
}

// row builds one stored evidence row. Rows are collected in call order so
// the newest-first listing is deterministic.
func (hs *harness) row(id uuid.UUID, kind, group, resource, sourceKind, namespace, name string) store.IncidentEvidenceRow {
	r := store.IncidentEvidenceRow{
		ID: uuid.New(), IncidentID: id, EvidenceKind: kind, Mode: store.EvidenceModeSnapshot,
		ClusterID: "local", APIGroup: group, Resource: resource, SourceKind: sourceKind,
		Namespace: namespace, Name: name, SourceUID: "uid-" + name, CollectedAt: hs.st.tick(),
		Completeness: store.EvidenceCompletenessComplete, Redaction: json.RawMessage(`{}`),
		Payload: json.RawMessage(`{"kind":"` + sourceKind + `","metadata":{"name":"` + name + `"}}`), CaptureKey: "k-" + name,
	}
	r.PayloadBytes = len(r.Payload)
	return r
}

func (hs *harness) addRows(rows ...store.IncidentEvidenceRow) {
	for _, r := range rows {
		hs.st.evidence[r.IncidentID] = append(hs.st.evidence[r.IncidentID], r)
	}
}

type request struct {
	user       *auth.User
	cluster    string
	incidentID string
	noteID     string
	query      string
	body       string
	ctx        context.Context
}

func (hs *harness) do(t *testing.T, handler http.HandlerFunc, method string, req request) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if req.body != "" {
		body = strings.NewReader(req.body)
	}
	ctx := req.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	r := httptest.NewRequestWithContext(ctx, method, "/incidents"+req.query, body)
	rctx := chi.NewRouteContext()
	if req.incidentID != "" {
		rctx.URLParams.Add("incidentID", req.incidentID)
	}
	if req.noteID != "" {
		rctx.URLParams.Add("noteID", req.noteID)
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
	return hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: user, incidentID: id.String()})
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, w.Body.String())
	}
	return out
}

func data(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	d, _ := decode(t, w)["data"].(map[string]any)
	if d == nil {
		t.Fatalf("no data object in response: %s", w.Body.String())
	}
	return d
}

func errorOf(t *testing.T, w *httptest.ResponseRecorder) (reason string, extra map[string]any) {
	t.Helper()
	e, _ := decode(t, w)["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error object in response: %s", w.Body.String())
	}
	reason, _ = e["reason"].(string)
	extra, _ = e["extra"].(map[string]any)
	return reason, extra
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d\n%s", w.Code, want, w.Body.String())
	}
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// withheldOf returns the withheld placeholders of a detail response and
// fails the test if any carries a field beyond the five the policy allows.
func withheldOf(t *testing.T, d map[string]any) []map[string]any {
	t.Helper()
	allowed := map[string]bool{"id": true, "evidenceKind": true, "collectedAt": true, "withheld": true, "withheldReason": true}
	out := make([]map[string]any, 0)
	for _, item := range list(d["withheld"]) {
		m := item.(map[string]any)
		for k := range m {
			if !allowed[k] {
				t.Fatalf("withheld placeholder leaks field %q: %v", k, m)
			}
		}
		if m["withheld"] != true {
			t.Fatalf("withheld placeholder must say withheld: %v", m)
		}
		out = append(out, m)
	}
	return out
}

func counts(t *testing.T, d map[string]any) (visible, withheld int) {
	t.Helper()
	c, _ := d["counts"].(map[string]any)
	if c == nil {
		t.Fatalf("no counts in response: %v", d)
	}
	return int(c["visible"].(float64)), int(c["withheld"].(float64))
}

// noRawTotals fails when any object in the response carries a raw store
// total (U23a review P1: evidence_bytes, evidence_count and scope_count
// reveal what a caller cannot see).
func noRawTotals(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, key := range []string{`"evidenceBytes"`, `"evidenceCount"`, `"scopeCount"`} {
		if strings.Contains(w.Body.String(), key) {
			t.Fatalf("response carries raw store total %s: %s", key, w.Body.String())
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Visibility (P1)
// ---------------------------------------------------------------------------

func TestGuessedIncidentIDReturns404NotForbidden(t *testing.T) {
	hs := newHarness(t)
	w := hs.get(t, bob, uuid.New())
	wantStatus(t, w, http.StatusNotFound)
}

func TestNonOwnerNonCollaboratorSeesNothing(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindObjectSummary, "apps", "deployments", "Deployment", "payments", "api"))

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"get": func() *httptest.ResponseRecorder { return hs.get(t, bob, id) },
		"update": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: bob, incidentID: id.String(), body: `{"title":"x"}`})
		},
		"delete": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: bob, incidentID: id.String()})
		},
		"list notes": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: bob, incidentID: id.String()})
		},
		"create note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: bob, incidentID: id.String(), body: `{"body":"hi"}`})
		},
		"update note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: bob, incidentID: id.String(), noteID: uuid.NewString(), body: `{"body":"hi","revision":1}`})
		},
		"delete note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: bob, incidentID: id.String(), noteID: uuid.NewString()})
		},
	} {
		w := call()
		if w.Code != http.StatusNotFound {
			t.Errorf("%s by a stranger: status = %d, want 404 (never 403)\n%s", name, w.Code, w.Body.String())
		}
	}
	// The 404 body is the same one a missing id produces: nothing to tell apart.
	missing := hs.get(t, bob, uuid.New()).Body.String()
	if got := hs.get(t, bob, id).Body.String(); got != missing {
		t.Fatalf("non-visible incident body differs from a missing one:\n%s\n%s", got, missing)
	}
	// The list does not include it either.
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: bob})
	wantStatus(t, w, http.StatusOK)
	if n := len(list(decode(t, w)["data"])); n != 0 {
		t.Fatalf("stranger lists %d incidents, want 0", n)
	}
}

func TestAdminWithoutGrantGets404(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	wantStatus(t, hs.get(t, root, id), http.StatusNotFound)
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: root})
	wantStatus(t, w, http.StatusOK)
	if n := len(list(decode(t, w)["data"])); n != 0 {
		t.Fatalf("admin lists %d incidents without a grant, want 0 (P1: no admin override)", n)
	}
}

func TestOwnerSeesIncidentWithRole(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	w := hs.get(t, alice, id)
	wantStatus(t, w, http.StatusOK)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["role"] != "owner" || inc["canAnnotate"] != true || inc["ownerId"] != alice.ID {
		t.Fatalf("owner view = %v", inc)
	}
}

func TestCollaboratorSeesIncidentButOnlyAuthorizedEvidence(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	visible := hs.row(id, EvidenceKindObjectSummary, "apps", "deployments", "Deployment", "payments", "api")
	hidden := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "billing", "worker-1")
	hs.addRows(visible, hidden)
	hs.access.allow = func(_, _, _, ns string) bool { return ns == "payments" }

	w := hs.get(t, bob, id)
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	inc := d["incident"].(map[string]any)
	if inc["role"] != "collaborator" || inc["canAnnotate"] != true {
		t.Fatalf("collaborator view = %v", inc)
	}
	ev := list(d["evidence"])
	if len(ev) != 1 || ev[0].(map[string]any)["id"] != visible.ID.String() {
		t.Fatalf("evidence = %v, want only %s", ev, visible.ID)
	}
	wh := withheldOf(t, d)
	if len(wh) != 1 || wh[0]["id"] != hidden.ID.String() || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v", wh)
	}
	if v, h := counts(t, d); v != 1 || h != 1 {
		t.Fatalf("counts = (%d, %d), want (1, 1)", v, h)
	}
}

func TestRevokedCollaboratorLosesAccessImmediately(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	wantStatus(t, hs.get(t, bob, id), http.StatusOK)
	hs.revoke(id, bob)
	wantStatus(t, hs.get(t, bob, id), http.StatusNotFound)
}

func TestOwnerOnlyActionsByCollaboratorAre403(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	w := hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: bob, incidentID: id.String(), body: `{"title":"renamed","summary":"","status":"closed"}`})
	wantStatus(t, w, http.StatusForbidden)
	w = hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: bob, incidentID: id.String()})
	wantStatus(t, w, http.StatusForbidden)
	if _, ok := hs.st.incidents[id]; !ok {
		t.Fatal("collaborator deleted the incident")
	}
	if hs.st.incidents[id].Title != "checkout latency" {
		t.Fatal("collaborator renamed the incident")
	}
}

// ---------------------------------------------------------------------------
// Evidence filtering (P4-P8, P10, P11, R-l)
// ---------------------------------------------------------------------------

func TestSourcePermissionChangeWithholdsItemWithoutMetadataLeak(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api-7f9")
	hs.addRows(r)

	before := data(t, hs.get(t, alice, id))
	if n := len(list(before["evidence"])); n != 1 {
		t.Fatalf("before revocation: %d visible, want 1", n)
	}
	// Access revoked after capture (P7): the next read withholds it.
	hs.access.allow = func(string, string, string, string) bool { return false }
	after := data(t, hs.get(t, alice, id))
	if n := len(list(after["evidence"])); n != 0 {
		t.Fatalf("after revocation: %d visible, want 0", n)
	}
	wh := withheldOf(t, after)
	if len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden || wh[0]["evidenceKind"] != EvidenceKindObjectSummary {
		t.Fatalf("withheld = %v", wh)
	}
	for _, leak := range []string{"payments", "api-7f9", "Pod", "pods", "uid-api-7f9"} {
		if strings.Contains(string(mustJSON(t, after)), leak) {
			t.Fatalf("withheld response leaks %q: %v", leak, after)
		}
	}
	// Withholding is not deletion: a later re-grant restores it.
	hs.access.allow = allowAll
	if n := len(list(data(t, hs.get(t, alice, id))["evidence"])); n != 1 {
		t.Fatalf("after re-grant: %d visible, want 1", n)
	}
}

func TestSARErrorWithholdsAsCheckUnavailableNotForbidden(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api"))
	hs.access.err = errors.New("dial tcp: connection refused")

	d := data(t, hs.get(t, alice, id))
	wh := withheldOf(t, d)
	if len(wh) != 1 || wh[0]["withheldReason"] != WithheldAuthorizationCheckUnavailable {
		t.Fatalf("withheld = %v, want reason %q (P4: unavailable is not forbidden)", wh, WithheldAuthorizationCheckUnavailable)
	}
	if n := len(list(d["evidence"])); n != 0 {
		t.Fatalf("%d items visible on a failed check, want 0 (fail closed)", n)
	}
}

func TestDefiniteDenialWinsOverCheckErrorForTheSameRow(t *testing.T) {
	// A secret-derived row needs two scopes. The pods check errors, the
	// secrets check definitely denies: the row is forbidden, not
	// "unavailable", because no retry could ever make it readable.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api")
	r.SecretDerived = true
	hs.addRows(r)
	hs.access.errFor = func(c accessCall) error {
		if c.resource == "pods" {
			return errors.New("webhook down")
		}
		return nil
	}
	hs.access.allow = func(_, _, resource, _ string) bool { return resource != "secrets" }

	d := data(t, hs.get(t, alice, id))
	wh := withheldOf(t, d)
	if len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v, want forbidden (a denial outranks an error)", wh)
	}
	if len(hs.access.calls) != 2 {
		t.Fatalf("calls = %v, want both scopes checked", hs.access.calls)
	}
}

func TestCheckErrorIsIsolatedToItsScope(t *testing.T) {
	// One scope's check failing does not withhold rows on other scopes, on
	// the same cluster or another: every scope gets its own attempt.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	broken := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api")
	fine := hs.row(id, EvidenceKindObjectSummary, "apps", "deployments", "Deployment", "payments", "api")
	remote := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api-remote")
	remote.ClusterID = "remote-1"
	hs.addRows(broken, fine, remote)
	hs.access.errFor = func(c accessCall) error {
		if c.clusterID == "local" && c.resource == "pods" {
			return errors.New("webhook down")
		}
		return nil
	}

	d := data(t, hs.get(t, alice, id))
	ev := list(d["evidence"])
	if len(ev) != 2 {
		t.Fatalf("visible = %v, want the deployments row and the remote row", ev)
	}
	wh := withheldOf(t, d)
	if len(wh) != 1 || wh[0]["id"] != broken.ID.String() || wh[0]["withheldReason"] != WithheldAuthorizationCheckUnavailable {
		t.Fatalf("withheld = %v", wh)
	}
	if len(hs.access.calls) != 3 {
		t.Fatalf("calls = %v, want one per scope", hs.access.calls)
	}
}

func TestSlowAccessCheckIsBoundedPerRequest(t *testing.T) {
	// A stalled cluster must yield per-row authorization_check_unavailable
	// within the handler's access budget, not a whole-request timeout.
	// Deterministic: the budget is derived from the request context, and
	// the fake checker ends that context inside the FIRST check (then
	// blocks until it is done, as a stalled cluster would). No timer is
	// involved, so the first check is always issued and every later scope
	// is always refused without a call, whatever the runner's load.
	hs := newHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hs.access.onFirstCall = cancel
	hs.access.blockUntilDone = true
	id := hs.seed(t, alice)
	hs.addRows(
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "a"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "billing", "b"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "search", "c"),
	)
	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, incidentID: id.String(), ctx: ctx})
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	wh := withheldOf(t, d)
	if len(wh) != 3 {
		t.Fatalf("withheld = %v, want every row", wh)
	}
	for _, m := range wh {
		if m["withheldReason"] != WithheldAuthorizationCheckUnavailable {
			t.Fatalf("withheld = %v", wh)
		}
	}
	if v, h := counts(t, d); v != 0 || h != 3 {
		t.Fatalf("counts = (%d, %d)", v, h)
	}
	// Once the budget is spent no further check is even attempted.
	if len(hs.access.calls) != 1 {
		t.Fatalf("%d checks issued after the budget expired, want 1", len(hs.access.calls))
	}
}

func TestDeletedSourceObjectStillGovernedByScopeAuthorization(t *testing.T) {
	// P8: the filter never consults the live object. A row whose source is
	// gone is withheld or shown purely on the stored scope.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	gone := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "deleted-pod")
	hs.addRows(gone)
	hs.access.allow = func(_, _, resource, ns string) bool { return !(resource == "pods" && ns == "payments") }
	d := data(t, hs.get(t, alice, id))
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v (the absence of the object never grants access)", wh)
	}
	hs.access.allow = allowAll
	d = data(t, hs.get(t, alice, id))
	ev := list(d["evidence"])
	if len(ev) != 1 || ev[0].(map[string]any)["source"].(map[string]any)["uid"] != "uid-deleted-pod" {
		t.Fatalf("evidence = %v, want the stored row keyed by its original UID", ev)
	}
}

func TestSecretDerivedEvidenceRequiresSecretsGet(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api")
	r.SecretDerived = true
	hs.addRows(r)
	// bob may read pods but not secrets in payments.
	hs.access.allow = func(_, _, resource, _ string) bool { return resource != "secrets" }

	d := data(t, hs.get(t, bob, id))
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v, want forbidden without get secrets (P11.2)", wh)
	}
	var sawSecrets bool
	for _, c := range hs.access.calls {
		if c.group == "" && c.resource == "secrets" && c.namespace == "payments" && c.clusterID == "local" {
			sawSecrets = true
		}
	}
	if !sawSecrets {
		t.Fatalf("no get secrets check in %v", hs.access.calls)
	}
	// With get secrets the item is visible.
	hs.access.allow = allowAll
	if n := len(list(data(t, hs.get(t, bob, id))["evidence"])); n != 1 {
		t.Fatalf("with secrets get: %d visible, want 1", n)
	}
}

func TestOwnerWithoutSecretsGetStillWithheld(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api")
	r.SecretDerived = true
	hs.addRows(r)
	hs.access.allow = func(_, _, resource, _ string) bool { return resource != "secrets" }
	d := data(t, hs.get(t, alice, id))
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("owner withheld = %v, want forbidden (P11.2: ownership is not a bypass)", wh)
	}
}

func TestNonSecretRowNeedsNoSecretsCheck(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api"))
	hs.access.allow = func(_, _, resource, _ string) bool { return resource != "secrets" }
	if n := len(list(data(t, hs.get(t, alice, id))["evidence"])); n != 1 {
		t.Fatalf("plain row: %d visible, want 1", n)
	}
	for _, c := range hs.access.calls {
		if c.resource == "secrets" {
			t.Fatalf("secrets check issued for a non-secret row: %v", hs.access.calls)
		}
	}
}

func TestStoredClusterNotRequestHeaderDrivesSAR(t *testing.T) {
	// P6: the caller sends X-Cluster-ID for a cluster where they are allowed
	// everything; the row was captured on another cluster where they are not.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api")
	r.ClusterID = "remote-1"
	hs.addRows(r)
	hs.access.allow = func(clusterID, _, _, _ string) bool { return clusterID == "local" }

	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, cluster: "local", incidentID: id.String()})
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v, want forbidden on the STORED cluster", wh)
	}
	if len(hs.access.calls) == 0 {
		t.Fatal("no access check issued")
	}
	for _, c := range hs.access.calls {
		if c.clusterID != "remote-1" {
			t.Fatalf("check sent to cluster %q, want the stored cluster remote-1 (calls %v)", c.clusterID, hs.access.calls)
		}
	}
}

func TestDualScopeRowsRequireBothScopes(t *testing.T) {
	// R-l: content-scoped rows carry the target's kind; the reader must hold
	// get on the stored scope AND the target's resource.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	podCheck := hs.row(id, EvidenceKindDiagnosticCheck, "", "pods", "Deployment", "payments", "api")
	events := hs.row(id, EvidenceKindEventList, "", "events", "Deployment", "payments", "api")
	hs.addRows(podCheck, events)

	t.Run("pods-only viewer cannot read a pod-scoped check about a Deployment", func(t *testing.T) {
		hs.access.calls = nil
		hs.access.allow = func(_, _, resource, _ string) bool { return resource == "pods" }
		d := data(t, hs.get(t, alice, id))
		wh := withheldOf(t, d)
		ids := map[string]string{}
		for _, m := range wh {
			ids[m["id"].(string)] = m["withheldReason"].(string)
		}
		if ids[podCheck.ID.String()] != WithheldForbidden {
			t.Fatalf("pod-scoped check: withheld = %v, want forbidden", wh)
		}
		var sawTarget bool
		for _, c := range hs.access.calls {
			if c.group == "apps" && c.resource == "deployments" && c.namespace == "payments" {
				sawTarget = true
			}
		}
		if !sawTarget {
			t.Fatalf("no apps/deployments check in %v", hs.access.calls)
		}
	})
	t.Run("deployments-only viewer cannot read the event_list row", func(t *testing.T) {
		hs.access.allow = func(_, group, resource, _ string) bool { return group == "apps" && resource == "deployments" }
		d := data(t, hs.get(t, alice, id))
		for _, m := range withheldOf(t, d) {
			if m["id"] == events.ID.String() && m["withheldReason"] == WithheldForbidden {
				return
			}
		}
		t.Fatalf("event_list row not withheld: %v", d["withheld"])
	})
	t.Run("both scopes held shows both rows", func(t *testing.T) {
		hs.access.allow = func(_, _, resource, _ string) bool { return resource == "pods" || resource == "deployments" }
		d := data(t, hs.get(t, alice, id))
		if n := len(list(d["evidence"])); n != 1 {
			t.Fatalf("pods+deployments viewer sees %d rows, want 1 (events still withheld)", n)
		}
		hs.access.allow = allowAll
		if n := len(list(data(t, hs.get(t, alice, id))["evidence"])); n != 2 {
			t.Fatalf("full viewer sees %d rows, want 2", n)
		}
	})
}

func TestUnmappableDiagnosticKindIsWithheldAsUnavailable(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindDiagnosticCheck, "", "pods", "Widget", "payments", "w"))
	d := data(t, hs.get(t, alice, id))
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldAuthorizationCheckUnavailable {
		t.Fatalf("withheld = %v, want %s", wh, WithheldAuthorizationCheckUnavailable)
	}
}

// ---------------------------------------------------------------------------
// Counts (P10)
// ---------------------------------------------------------------------------

func TestCountsAreWholeIncidentAndExcludeWithheld(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, false)
	hs.addRows(
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "a"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "b"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "billing", "c"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "billing", "d"),
	)
	// Raw store totals exist and must never surface.
	inc := hs.st.incidents[id]
	inc.EvidenceBytes, inc.EvidenceCount, inc.ScopeCount = 123456, 4, 2
	hs.st.incidents[id] = inc

	// The collaborator may read payments only; the owner may read billing
	// only (ownership is not a bypass). Counts cover the whole incident even
	// when the page is smaller.
	for _, tc := range []struct {
		user *auth.User
		ns   string
	}{{bob, "payments"}, {alice, "billing"}} {
		hs.access.allow = func(_, _, _, ns string) bool { return ns == tc.ns }
		w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: tc.user, incidentID: id.String(), query: "?limit=1"})
		wantStatus(t, w, http.StatusOK)
		noRawTotals(t, w)
		d := data(t, w)
		v, h := counts(t, d)
		if v != 2 || h != 2 {
			t.Fatalf("%s: counts = (%d, %d), want whole-incident (2, 2)", tc.user.Username, v, h)
		}
		if n := len(list(d["evidence"])) + len(list(d["withheld"])); n != 1 {
			t.Fatalf("%s: page has %d items, want 1 (limit)", tc.user.Username, n)
		}
		meta := decode(t, w)["metadata"].(map[string]any)
		if int(meta["total"].(float64)) != 2 {
			t.Fatalf("%s: metadata.total = %v, want the whole-incident visible count 2", tc.user.Username, meta["total"])
		}
	}
	// No raw totals on the list, create or update echo either.
	hs.access.allow = allowAll
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: alice})
	wantStatus(t, w, http.StatusOK)
	noRawTotals(t, w)
	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"renamed"}`})
	wantStatus(t, w, http.StatusOK)
	noRawTotals(t, w)
	if v, h := counts(t, data(t, w)); v != 4 || h != 0 {
		t.Fatalf("update echo counts = (%d, %d), want (4, 0)", v, h)
	}
	w = hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{"title":"t","windowStart":"2026-10-05T11:00:00Z"}`})
	wantStatus(t, w, http.StatusCreated)
	noRawTotals(t, w)
	if v, h := counts(t, data(t, w)); v != 0 || h != 0 {
		t.Fatalf("create echo counts = (%d, %d), want (0, 0)", v, h)
	}
}

func TestEvidencePaginationAppliesTheSameFilter(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for i := 0; i < 4; i++ {
		ns := "payments"
		if i%2 == 1 {
			ns = "billing"
		}
		hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", ns, fmt.Sprintf("p%d", i)))
	}
	hs.access.allow = func(_, _, _, ns string) bool { return ns == "payments" }
	seenVisible, seenWithheld := 0, 0
	cursor := ""
	for page := 0; page < 5; page++ {
		w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, incidentID: id.String(), query: "?limit=2&continue=" + cursor})
		wantStatus(t, w, http.StatusOK)
		d := data(t, w)
		for _, item := range list(d["evidence"]) {
			if item.(map[string]any)["source"].(map[string]any)["namespace"] != "payments" {
				t.Fatalf("page %d leaks a billing row: %v", page, item)
			}
			seenVisible++
		}
		seenWithheld += len(withheldOf(t, d))
		if v, h := counts(t, d); v != 2 || h != 2 {
			t.Fatalf("page %d counts = (%d, %d), want whole-incident (2, 2) on every page", page, v, h)
		}
		meta := decode(t, w)["metadata"].(map[string]any)
		next, _ := meta["continue"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if seenVisible != 2 || seenWithheld != 2 {
		t.Fatalf("paged totals = (%d visible, %d withheld), want (2, 2)", seenVisible, seenWithheld)
	}
}

func TestScopeDeduplicationIssuesOneSARPerScope(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for i := 0; i < 5; i++ {
		hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", fmt.Sprintf("p%d", i)))
	}
	for i := 0; i < 2; i++ {
		r := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", fmt.Sprintf("s%d", i))
		r.SecretDerived = true
		hs.addRows(r)
	}
	// Two diagnostic rows sharing stored and target scopes with the above.
	hs.addRows(
		hs.row(id, EvidenceKindDiagnosticCheck, "", "pods", "Pod", "payments", "d0"),
		hs.row(id, EvidenceKindDiagnosticCheck, "", "pods", "Deployment", "payments", "d1"),
	)
	// The detail read counts the whole incident AND filters the page with
	// one memo: still one check per scope.
	wantStatus(t, hs.get(t, alice, id), http.StatusOK)
	want := map[accessCall]bool{
		{"local", "", "pods", "payments"}:            true,
		{"local", "", "secrets", "payments"}:         true,
		{"local", "apps", "deployments", "payments"}: true,
	}
	if len(hs.access.calls) != len(want) {
		t.Fatalf("%d access checks for 9 rows over 3 scopes: %v", len(hs.access.calls), hs.access.calls)
	}
	for _, c := range hs.access.calls {
		if !want[c] {
			t.Fatalf("unexpected check %v", c)
		}
	}
}

func TestNoAccessCheckerWithholdsEverything(t *testing.T) {
	hs := newHarness(t)
	hs.h = newHandlerWith(hs.st, hs.st, hs.st, nil, hs.audit, nil)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "api"))
	d := data(t, hs.get(t, alice, id))
	if wh := withheldOf(t, d); len(wh) != 1 || wh[0]["withheldReason"] != WithheldAuthorizationCheckUnavailable {
		t.Fatalf("withheld = %v", wh)
	}
}

// ---------------------------------------------------------------------------
// List (P1, P10)
// ---------------------------------------------------------------------------

func TestListDoesNotLeakWithheldIncidentsViaPagination(t *testing.T) {
	hs := newHarness(t)
	var mine []uuid.UUID
	for i := 0; i < 3; i++ {
		mine = append(mine, hs.seed(t, alice))
	}
	shared := hs.seed(t, alice)
	hs.grant(shared, bob, false)
	hs.seed(t, carol)

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: bob, query: "?limit=1&continue=" + cursor})
		wantStatus(t, w, http.StatusOK)
		body := decode(t, w)
		items := list(body["data"])
		meta := body["metadata"].(map[string]any)
		if int(meta["total"].(float64)) != len(items) {
			t.Fatalf("metadata.total = %v for %d items", meta["total"], len(items))
		}
		for _, it := range items {
			m := it.(map[string]any)
			seen[m["id"].(string)] = true
			if m["role"] != "collaborator" || m["canAnnotate"] != false {
				t.Fatalf("list item = %v, want collaborator without annotate", m)
			}
		}
		next, _ := meta["continue"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 1 || !seen[shared.String()] {
		t.Fatalf("bob saw %v, want only %s", seen, shared)
	}
	// A forged cursor positioned among alice's own incidents still yields
	// only what bob may see.
	forged := store.EncodeIncidentCursor(store.IncidentCursor{CreatedAt: fixedNow.Add(time.Hour), ID: mine[0]})
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: bob, query: "?continue=" + forged})
	wantStatus(t, w, http.StatusOK)
	for _, it := range list(decode(t, w)["data"]) {
		if it.(map[string]any)["id"] != shared.String() {
			t.Fatalf("forged cursor exposed %v", it)
		}
	}
	w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: bob, query: "?continue=garbage"})
	wantStatus(t, w, http.StatusBadRequest)
}

func TestListOwnerRowsCarryOwnerRole(t *testing.T) {
	hs := newHarness(t)
	hs.seed(t, alice)
	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: alice})
	wantStatus(t, w, http.StatusOK)
	items := list(decode(t, w)["data"])
	if len(items) != 1 || items[0].(map[string]any)["role"] != "owner" || items[0].(map[string]any)["canAnnotate"] != true {
		t.Fatalf("items = %v", items)
	}
}

func TestListResolvesCollaboratorRolesInOneQueryAndDropsRevokedRows(t *testing.T) {
	hs := newHarness(t)
	a := hs.seed(t, alice)
	b := hs.seed(t, alice)
	hs.grant(a, bob, true)
	hs.grant(b, bob, false)
	// b's grant is revoked between the list read and the role lookup.
	hs.st.afterListVisible = func() { hs.revoke(b, bob) }
	hs.st.deadlines = nil

	w := hs.do(t, hs.h.HandleList, http.MethodGet, request{user: bob})
	wantStatus(t, w, http.StatusOK)
	items := list(decode(t, w)["data"])
	if len(items) != 1 || items[0].(map[string]any)["id"] != a.String() || items[0].(map[string]any)["canAnnotate"] != true {
		t.Fatalf("items = %v, want only a (annotate)", items)
	}
	if total := decode(t, w)["metadata"].(map[string]any)["total"]; total != float64(1) {
		t.Fatalf("metadata.total = %v, want 1", total)
	}
	// One grant lookup for the page, not one per row: ListVisible + GrantsFor
	// are the only store calls.
	if n := len(hs.st.deadlines); n != 2 {
		t.Fatalf("%d store calls for a two-row page, want 2 (ListVisible + GrantsFor)", n)
	}
}

// ---------------------------------------------------------------------------
// Notes (P3)
// ---------------------------------------------------------------------------

func TestCollaboratorWithoutAnnotateCanReadButNotWriteNotes(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, false)
	note, _ := hs.st.CreateNote(context.Background(), id, alice.ID, "owner's note")

	w := hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: bob, incidentID: id.String()})
	wantStatus(t, w, http.StatusOK)
	if n := len(list(decode(t, w)["data"])); n != 1 {
		t.Fatalf("read-only collaborator lists %d notes, want 1", n)
	}
	w = hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: bob, incidentID: id.String(), body: `{"body":"mine"}`})
	wantStatus(t, w, http.StatusForbidden)
	w = hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: bob, incidentID: id.String(), noteID: note.ID.String(), body: `{"body":"edit","revision":1}`})
	wantStatus(t, w, http.StatusForbidden)
	w = hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: bob, incidentID: id.String(), noteID: note.ID.String()})
	wantStatus(t, w, http.StatusForbidden)
	if len(hs.st.notes[id]) != 1 || hs.st.notes[id][0].Body != "owner's note" {
		t.Fatalf("notes changed: %v", hs.st.notes[id])
	}
}

func TestAnnotatingCollaboratorCanWriteOwnNotesOnly(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	alices, _ := hs.st.CreateNote(context.Background(), id, alice.ID, "owner's note")

	w := hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: bob, incidentID: id.String(), body: `{"body":"bob's note"}`})
	wantStatus(t, w, http.StatusCreated)
	created := data(t, w)
	if created["authorId"] != bob.ID || created["revision"] != float64(1) {
		t.Fatalf("created note = %v", created)
	}
	noteID := created["id"].(string)
	w = hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: bob, incidentID: id.String(), noteID: noteID, body: `{"body":"bob's edit","revision":1}`})
	wantStatus(t, w, http.StatusOK)
	if data(t, w)["revision"] != float64(2) {
		t.Fatalf("updated note = %v", data(t, w))
	}
	// Someone else's note: author-only, 403.
	w = hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: bob, incidentID: id.String(), noteID: alices.ID.String(), body: `{"body":"x","revision":1}`})
	wantStatus(t, w, http.StatusForbidden)
	w = hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: bob, incidentID: id.String(), noteID: alices.ID.String()})
	wantStatus(t, w, http.StatusForbidden)
	w = hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: bob, incidentID: id.String(), noteID: noteID})
	wantStatus(t, w, http.StatusNoContent)
}

func TestRevokedGranteeCannotEditOwnEarlierNote(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	note, _ := hs.st.CreateNote(context.Background(), id, bob.ID, "bob's note")
	hs.revoke(id, bob)
	w := hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: bob, incidentID: id.String(), noteID: note.ID.String(), body: `{"body":"edit","revision":1}`})
	wantStatus(t, w, http.StatusNotFound)
	w = hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: bob, incidentID: id.String(), noteID: note.ID.String()})
	wantStatus(t, w, http.StatusNotFound)
	w = hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: bob, incidentID: id.String()})
	wantStatus(t, w, http.StatusNotFound)
}

func TestNoteFromAnotherIncidentIs404(t *testing.T) {
	hs := newHarness(t)
	a := hs.seed(t, alice)
	b := hs.seed(t, alice)
	note, _ := hs.st.CreateNote(context.Background(), b, alice.ID, "on b")
	w := hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: a.String(), noteID: note.ID.String(), body: `{"body":"x","revision":1}`})
	wantStatus(t, w, http.StatusNotFound)
	w = hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: alice, incidentID: a.String(), noteID: note.ID.String()})
	wantStatus(t, w, http.StatusNotFound)
	if len(hs.st.notes[b]) != 1 {
		t.Fatal("note on b was touched through a")
	}
}

func TestNoteRevisionConflictReturns409WithReason(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	note, _ := hs.st.CreateNote(context.Background(), id, alice.ID, "v1")
	if _, err := hs.st.UpdateNote(context.Background(), id, note.ID, alice.ID, "v2", 1); err != nil {
		t.Fatal(err)
	}
	w := hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: id.String(), noteID: note.ID.String(), body: `{"body":"stale","revision":1}`})
	wantStatus(t, w, http.StatusConflict)
	reason, extra := errorOf(t, w)
	if reason != ReasonNoteRevisionConflict || extra["currentRevision"] != float64(2) {
		t.Fatalf("reason = %q, extra = %v", reason, extra)
	}
}

func TestListNotesPagesSoEveryNoteIsReachable(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	const n = 501
	for i := 0; i < n; i++ {
		if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, fmt.Sprintf("note %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	cursor := ""
	pages := 0
	for {
		w := hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: alice, incidentID: id.String(), query: "?limit=200&continue=" + cursor})
		wantStatus(t, w, http.StatusOK)
		body := decode(t, w)
		items := list(body["data"])
		meta := body["metadata"].(map[string]any)
		if int(meta["total"].(float64)) != len(items) {
			t.Fatalf("metadata.total = %v for %d notes on the page", meta["total"], len(items))
		}
		for _, it := range items {
			if want := fmt.Sprintf("note %d", seen); it.(map[string]any)["body"] != want {
				t.Fatalf("note at position %d = %v, want %q (oldest first, none skipped)", seen, it, want)
			}
			seen++
		}
		pages++
		next, _ := meta["continue"].(string)
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("cursor never ended")
		}
	}
	if seen != n || pages != 3 {
		t.Fatalf("reached %d notes over %d pages, want %d over 3", seen, pages, n)
	}
	// Exactly a page's worth yields no cursor.
	hs.st.notes[id] = hs.st.notes[id][:200]
	w := hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: alice, incidentID: id.String(), query: "?limit=200"})
	wantStatus(t, w, http.StatusOK)
	meta := decode(t, w)["metadata"].(map[string]any)
	if next, _ := meta["continue"].(string); next != "" {
		t.Fatalf("a page ending on the last note carried a cursor %q", next)
	}
	w = hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: alice, incidentID: id.String(), query: "?continue=garbage"})
	wantStatus(t, w, http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// Writes (R-f), audit (P16), busy (R-g)
// ---------------------------------------------------------------------------

func TestCreateTakesOwnerFromSessionAndPinsLocalCluster(t *testing.T) {
	hs := newHarness(t)
	w := hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, cluster: "remote-1",
		body: `{"ownerId":"local:mallory","clusterId":"remote-1","title":"api errors","summary":"5xx spike","windowStart":"2026-10-05T11:00:00Z"}`})
	wantStatus(t, w, http.StatusCreated)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["ownerId"] != alice.ID || inc["clusterId"] != "local" || inc["role"] != "owner" || inc["status"] != "open" {
		t.Fatalf("created = %v", inc)
	}
	if inc["retentionDays"] != float64(DefaultRetentionDays) {
		t.Fatalf("retentionDays = %v, want %d", inc["retentionDays"], DefaultRetentionDays)
	}
	id := uuid.MustParse(inc["id"].(string))
	if row := hs.st.incidents[id]; row.OwnerID != alice.ID || row.ClusterID != "local" {
		t.Fatalf("stored row = %+v", row)
	}
}

func TestCreateValidationErrorsAre400WithStoreMessage(t *testing.T) {
	hs := newHarness(t)
	for name, body := range map[string]string{
		"empty title":  `{"title":"","windowStart":"2026-10-05T11:00:00Z"}`,
		"no window":    `{"title":"x"}`,
		"end < start":  `{"title":"x","windowStart":"2026-10-05T11:00:00Z","windowEnd":"2026-10-05T10:00:00Z"}`,
		"invalid json": `{"title":`,
	} {
		w := hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: body})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400\n%s", name, w.Code, w.Body.String())
		}
	}
	if len(hs.st.incidents) != 0 {
		t.Fatal("an invalid create was stored")
	}
}

func TestOversizedBodyIs413AndBadLimitIs400(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	huge := `{"title":"t","summary":"` + strings.Repeat("x", maxBodyBytes) + `","windowStart":"2026-10-05T11:00:00Z"}`
	w := hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: huge})
	wantStatus(t, w, http.StatusRequestEntityTooLarge)
	if len(hs.st.incidents) != 1 {
		t.Fatal("an oversized create was stored")
	}
	for _, q := range []string{"?limit=abc", "?limit=1.5", "?limit="} {
		w = hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, incidentID: id.String(), query: q})
		if q == "?limit=" {
			wantStatus(t, w, http.StatusOK) // absent is the default
			continue
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, w.Code)
		}
		w = hs.do(t, hs.h.HandleList, http.MethodGet, request{user: alice, query: q})
		if w.Code != http.StatusBadRequest {
			t.Errorf("list %s: status = %d, want 400", q, w.Code)
		}
	}
}

func TestUpdateAndDeleteByOwner(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	w := hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"renamed","summary":"done","status":"closed"}`})
	wantStatus(t, w, http.StatusOK)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["title"] != "renamed" || inc["status"] != "closed" || inc["closedAt"] == nil {
		t.Fatalf("updated = %v", inc)
	}
	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"","status":"closed"}`})
	wantStatus(t, w, http.StatusBadRequest)
	w = hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: alice, incidentID: id.String()})
	wantStatus(t, w, http.StatusNoContent)
	wantStatus(t, hs.get(t, alice, id), http.StatusNotFound)
}

func TestPartialUpdateKeepsOmittedFields(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	w := hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"t","summary":"the summary","status":"closed"}`})
	wantStatus(t, w, http.StatusOK)

	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"renamed"}`})
	wantStatus(t, w, http.StatusOK)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["title"] != "renamed" || inc["summary"] != "the summary" || inc["status"] != "closed" {
		t.Fatalf("after title-only PUT: %v (summary and status must be kept)", inc)
	}
	row := hs.st.incidents[id]
	if row.Summary != "the summary" || row.Status != "closed" {
		t.Fatalf("stored row = %+v", row)
	}
	// An explicit empty summary does clear it.
	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"summary":""}`})
	wantStatus(t, w, http.StatusOK)
	if hs.st.incidents[id].Summary != "" || hs.st.incidents[id].Title != "renamed" {
		t.Fatalf("stored row = %+v", hs.st.incidents[id])
	}
}

func TestInterleavedPartialUpdatesKeepBothFields(t *testing.T) {
	// Two owners' sessions read the same row; the title-only PUT's write is
	// interleaved with a summary-only write. The merge is the store's
	// (COALESCE), not the handler's stale read, so both survive.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	wantStatus(t, hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"summary":"s0","status":"closed"}`}), http.StatusOK)
	hs.st.beforeUpdate = func() {
		if err := hs.st.Update(context.Background(), id, alice.ID, nil, ptrTo("summary from the other session"), nil); err != nil {
			t.Fatal(err)
		}
	}
	w := hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"title from this session"}`})
	wantStatus(t, w, http.StatusOK)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["title"] != "title from this session" || inc["summary"] != "summary from the other session" || inc["status"] != "closed" {
		t.Fatalf("after interleaved partial PUTs: %v", inc)
	}
	row := hs.st.incidents[id]
	if row.Title != "title from this session" || row.Summary != "summary from the other session" || row.Status != "closed" || row.ClosedAt == nil {
		t.Fatalf("stored row = %+v", row)
	}
}

func ptrTo(s string) *string { return &s }

func TestCountsReadFailureIs503OnDetailButNotOnWriteEcho(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.failOp["ListScopeRowsByIncident"] = errors.New("pg: scope read failed")

	w := hs.get(t, alice, id)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if strings.Contains(w.Body.String(), `"counts"`) {
		t.Fatalf("detail answered with counts on a failed count read: %s", w.Body.String())
	}

	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"renamed"}`})
	wantStatus(t, w, http.StatusOK)
	if _, has := data(t, w)["counts"]; has {
		t.Fatalf("update echo carries counts after a failed count read: %s", w.Body.String())
	}
	if data(t, w)["incident"].(map[string]any)["title"] != "renamed" {
		t.Fatalf("update echo = %s", w.Body.String())
	}
	w = hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{"title":"t","windowStart":"2026-10-05T11:00:00Z"}`})
	wantStatus(t, w, http.StatusCreated)
	if _, has := data(t, w)["counts"]; has {
		t.Fatalf("create echo carries counts after a failed count read: %s", w.Body.String())
	}
	want := []string{string(ActionIncidentUpdate) + ":success", string(ActionIncidentCreate) + ":success"}
	if got := hs.audit.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit = %v, want %v", got, want)
	}
}

func TestCommittedWriteIsSuccessAndAuditedWhenReadBackFails(t *testing.T) {
	hs := newHarness(t)
	hs.st.failGetAfterWrite = true

	w := hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{"title":"t","summary":"s","windowStart":"2026-10-05T11:00:00Z"}`})
	wantStatus(t, w, http.StatusCreated)
	inc := data(t, w)["incident"].(map[string]any)
	if inc["id"] == "" || inc["title"] != "t" || inc["summary"] != "s" || inc["ownerId"] != alice.ID || inc["status"] != "open" {
		t.Fatalf("created (read-back failed) = %v", inc)
	}
	id := uuid.MustParse(inc["id"].(string))
	if _, ok := hs.st.incidents[id]; !ok {
		t.Fatal("the committed incident is missing")
	}

	w = hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"renamed","status":"closed"}`})
	wantStatus(t, w, http.StatusOK)
	inc = data(t, w)["incident"].(map[string]any)
	if inc["title"] != "renamed" || inc["status"] != "closed" || inc["summary"] != "s" || inc["closedAt"] == nil {
		t.Fatalf("updated (read-back failed) = %v", inc)
	}
	want := []string{string(ActionIncidentCreate) + ":success", string(ActionIncidentUpdate) + ":success"}
	if got := hs.audit.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit = %v, want %v (a committed write is audited whatever the read-back did)", got, want)
	}
}

func TestWriteOperationsAreAudited(t *testing.T) {
	hs := newHarness(t)
	w := hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{"title":"t","windowStart":"2026-10-05T11:00:00Z"}`})
	wantStatus(t, w, http.StatusCreated)
	id := data(t, w)["incident"].(map[string]any)["id"].(string)
	wantStatus(t, hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id, body: `{"title":"t2","status":"open"}`}), http.StatusOK)
	w = hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: alice, incidentID: id, body: `{"body":"secret-ish note body"}`})
	wantStatus(t, w, http.StatusCreated)
	noteID := data(t, w)["id"].(string)
	wantStatus(t, hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: id, noteID: noteID, body: `{"body":"edited","revision":1}`}), http.StatusOK)
	wantStatus(t, hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: alice, incidentID: id, noteID: noteID}), http.StatusNoContent)
	wantStatus(t, hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: alice, incidentID: id}), http.StatusNoContent)
	// A failed write is audited as a failure too.
	wantStatus(t, hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{"title":"","windowStart":"2026-10-05T11:00:00Z"}`}), http.StatusBadRequest)

	want := []string{
		string(ActionIncidentCreate) + ":success", string(ActionIncidentUpdate) + ":success",
		string(ActionIncidentNoteCreate) + ":success", string(ActionIncidentNoteUpdate) + ":success",
		string(ActionIncidentNoteDelete) + ":success", string(ActionIncidentDelete) + ":success",
		string(ActionIncidentCreate) + ":failure",
	}
	if got := hs.audit.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions = %v, want %v", got, want)
	}
	for _, e := range hs.audit.entries {
		if e.User != alice.Username || e.ClusterID != "local" {
			t.Fatalf("audit entry = %+v", e)
		}
		if strings.Contains(e.Detail, "secret-ish") || strings.Contains(e.Detail, "edited") {
			t.Fatalf("audit detail carries a note body: %q", e.Detail)
		}
		if len(e.Detail) > 200 {
			t.Fatalf("audit detail unbounded: %q", e.Detail)
		}
	}
}

func TestFailedWritesAreAuditedAsFailures(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	note, _ := hs.st.CreateNote(context.Background(), id, alice.ID, "n")
	hs.st.failOp["Update"] = errors.New("pg: write failed")
	hs.st.failOp["Delete"] = errors.New("pg: write failed")
	hs.st.failOp["CreateNote"] = errors.New("pg: write failed")
	hs.st.failOp["UpdateNote"] = errors.New("pg: write failed")
	hs.st.failOp["DeleteNote"] = errors.New("pg: write failed")

	wantStatus(t, hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{"title":"x"}`}), http.StatusServiceUnavailable)
	wantStatus(t, hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: alice, incidentID: id.String()}), http.StatusServiceUnavailable)
	wantStatus(t, hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: alice, incidentID: id.String(), body: `{"body":"x"}`}), http.StatusServiceUnavailable)
	wantStatus(t, hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: id.String(), noteID: note.ID.String(), body: `{"body":"x","revision":1}`}), http.StatusServiceUnavailable)
	wantStatus(t, hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: alice, incidentID: id.String(), noteID: note.ID.String()}), http.StatusServiceUnavailable)

	want := []string{
		string(ActionIncidentUpdate) + ":failure", string(ActionIncidentDelete) + ":failure",
		string(ActionIncidentNoteCreate) + ":failure", string(ActionIncidentNoteUpdate) + ":failure",
		string(ActionIncidentNoteDelete) + ":failure",
	}
	if got := hs.audit.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions = %v, want %v", got, want)
	}
}

func TestIncidentBusyIs503WithRetryAfter(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.busy = true
	w := hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: alice, incidentID: id.String(), body: `{"body":"x"}`})
	wantStatus(t, w, http.StatusServiceUnavailable)
	reason, _ := errorOf(t, w)
	if reason != ReasonIncidentBusy || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("reason = %q, Retry-After = %q", reason, w.Header().Get("Retry-After"))
	}
}

func TestStoreFaultIs503NotNotFound(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.failWith = errors.New("pg: connection reset")
	w := hs.get(t, alice, id)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if strings.Contains(w.Body.String(), "connection reset") {
		t.Fatalf("internal error leaked: %s", w.Body.String())
	}
}

func TestRequestContextReachesTheStore(t *testing.T) {
	// The request context carries the server's deadline; every store call
	// must receive it (never context.Background()).
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.deadlines = nil
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	wantStatus(t, hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, incidentID: id.String(), ctx: ctx}), http.StatusOK)
	wantStatus(t, hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: alice, incidentID: id.String(), body: `{"body":"x"}`, ctx: ctx}), http.StatusCreated)
	if len(hs.st.deadlines) < 4 {
		t.Fatalf("only %d store calls recorded", len(hs.st.deadlines))
	}
	for i, has := range hs.st.deadlines {
		if !has {
			t.Fatalf("store call %d received a context without the request deadline", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Gates (C4, C6, baseline)
// ---------------------------------------------------------------------------

func TestNoDatabaseReturns503WithReason(t *testing.T) {
	hs := newHarness(t)
	hs.h = NewHandler(nil, nil, nil, nil, hs.audit, nil)
	id := uuid.New()
	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"list": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleList, http.MethodGet, request{user: alice})
		},
		"create": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleCreate, http.MethodPost, request{user: alice, body: `{}`})
		},
		"get": func() *httptest.ResponseRecorder { return hs.get(t, alice, id) },
		"update": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleUpdate, http.MethodPut, request{user: alice, incidentID: id.String(), body: `{}`})
		},
		"delete": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleDelete, http.MethodDelete, request{user: alice, incidentID: id.String()})
		},
		"list notes": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{user: alice, incidentID: id.String()})
		},
		"create note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleCreateNote, http.MethodPost, request{user: alice, incidentID: id.String(), body: `{}`})
		},
		"update note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: id.String(), noteID: id.String(), body: `{}`})
		},
		"delete note": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleDeleteNote, http.MethodDelete, request{user: alice, incidentID: id.String(), noteID: id.String()})
		},
	} {
		w := call()
		if w.Code == http.StatusNotFound {
			t.Fatalf("%s without a database answered 404; must be 503", name)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without a database: status = %d, want 503\n%s", name, w.Code, w.Body.String())
		}
		reason, extra := errorOf(t, w)
		if reason != ReasonPersistenceUnavailable || extra["requires"] != "postgresql" {
			t.Fatalf("%s: reason = %q extra = %v", name, reason, extra)
		}
	}
}

func TestInvalidUUIDReturns400(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for _, bad := range []string{"not-a-uuid", "", "00000000-0000-0000-0000-00000000000g", "../etc"} {
		w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: alice, incidentID: bad})
		if w.Code != http.StatusBadRequest {
			t.Errorf("incident id %q: status = %d, want 400", bad, w.Code)
		}
		w = hs.do(t, hs.h.HandleUpdateNote, http.MethodPut, request{user: alice, incidentID: id.String(), noteID: bad, body: `{"body":"x","revision":1}`})
		if w.Code != http.StatusBadRequest {
			t.Errorf("note id %q: status = %d, want 400", bad, w.Code)
		}
	}
}

func TestUnauthenticatedRequestReturns401(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"list": func() *httptest.ResponseRecorder { return hs.do(t, hs.h.HandleList, http.MethodGet, request{}) },
		"get": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleGet, http.MethodGet, request{incidentID: id.String()})
		},
		"create": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleCreate, http.MethodPost, request{body: `{}`})
		},
		"notes": func() *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleListNotes, http.MethodGet, request{incidentID: id.String()})
		},
	} {
		if w := call(); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, w.Code)
		}
	}
}

func TestNewHandlerKeepsNilStoresUntyped(t *testing.T) {
	// A nil *store.IncidentStore must not become a non-nil interface that
	// panics on first use instead of answering 503.
	h := NewHandler(nil, nil, nil, nil, nil, nil)
	if h.incidents != nil || h.evidence != nil || h.grants != nil || h.access != nil {
		t.Fatal("typed nil leaked into an interface field")
	}
	if h.retentionDays != DefaultRetentionDays || h.accessTimeout != accessCheckTimeout {
		t.Fatalf("defaults = (%d, %s)", h.retentionDays, h.accessTimeout)
	}
}
