package incidents

// Capture, grant and export handler tests (Release D, U23b). The fakes in
// handler_test.go are extended here with InsertBatch and the grant writes,
// mirroring the real stores' error contract and precedence; the capture
// path drives the REAL Collector (collector.go) with stub sources from
// collector_test.go so request validation, finalization and
// de-duplication are the production code.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Fake store: evidence writes and grant writes
// ---------------------------------------------------------------------------

// InsertBatch mirrors store.IncidentEvidenceStore.InsertBatch, including its
// documented error precedence: invalid, item_bytes, busy, not found, not
// owner, closed, items, incident_bytes, scopes.
func (f *fakeStore) InsertBatch(ctx context.Context, incidentID uuid.UUID, ownerID string, rows []store.IncidentEvidenceRow, limits store.EvidenceLimits) (int, error) {
	if err := f.enter(ctx, "InsertBatch"); err != nil {
		return 0, err
	}
	if ownerID == "" {
		return 0, fmt.Errorf("%w: owner id is required", store.ErrIncidentInvalid)
	}
	if err := limits.Validate(); err != nil {
		return 0, err
	}
	if len(rows) > store.EvidenceMaxItemsCeiling {
		return 0, fmt.Errorf("%w: a capture holds at most %d items", store.ErrIncidentInvalid, store.EvidenceMaxItemsCeiling)
	}
	for i, r := range rows {
		if err := store.ValidateEvidenceRow(r); err != nil {
			return 0, fmt.Errorf("evidence item %d: %w", i, err)
		}
	}
	for _, r := range rows {
		if len(r.Payload) > limits.MaxItemBytes {
			return 0, &store.EvidenceLimitError{Limit: store.EvidenceLimitItemBytes, Max: int64(limits.MaxItemBytes), Attempted: int64(len(r.Payload))}
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if f.busy {
		return 0, store.ErrIncidentBusy
	}
	inc, ok := f.incidents[incidentID]
	if !ok {
		return 0, store.ErrIncidentNotFound
	}
	if inc.OwnerID != ownerID {
		return 0, store.ErrNotOwner
	}
	if inc.Status == store.IncidentStatusClosed {
		return 0, store.ErrIncidentClosed
	}
	existing := f.evidence[incidentID]
	keys := map[string]bool{}
	scopes := map[store.EvidenceScope]bool{}
	var curBytes int64
	for _, r := range existing {
		keys[r.CaptureKey] = true
		scopes[store.EvidenceScope{ClusterID: r.ClusterID, APIGroup: r.APIGroup, Resource: r.Resource, Namespace: r.Namespace}] = true
		curBytes += int64(r.PayloadBytes)
	}
	curCount, curScopes := len(existing), len(scopes)
	var (
		added      []store.IncidentEvidenceRow
		addedBytes int64
	)
	for _, r := range rows {
		if keys[r.CaptureKey] {
			continue
		}
		keys[r.CaptureKey] = true
		r.ID = uuid.New()
		r.IncidentID = incidentID
		r.CollectedAt = f.tick()
		r.PayloadBytes = len(r.Payload)
		if len(r.Redaction) == 0 {
			r.Redaction = json.RawMessage(`{}`)
		}
		scopes[store.EvidenceScope{ClusterID: r.ClusterID, APIGroup: r.APIGroup, Resource: r.Resource, Namespace: r.Namespace}] = true
		added = append(added, r)
		addedBytes += int64(r.PayloadBytes)
	}
	if len(added) == 0 {
		return 0, nil
	}
	if n := curCount + len(added); n > limits.MaxItems {
		return 0, &store.EvidenceLimitError{Limit: store.EvidenceLimitItems, Max: int64(limits.MaxItems), Current: int64(curCount), Attempted: int64(n)}
	}
	if n := curBytes + addedBytes; n > int64(limits.MaxIncidentBytes) {
		return 0, &store.EvidenceLimitError{Limit: store.EvidenceLimitIncidentBytes, Max: int64(limits.MaxIncidentBytes), Current: curBytes, Attempted: n}
	}
	if len(scopes) > limits.MaxScopes && len(scopes) > curScopes {
		return 0, &store.ScopeLimitError{Max: limits.MaxScopes, Current: curScopes, Attempted: len(scopes)}
	}
	f.evidence[incidentID] = append(existing, added...)
	return len(added), nil
}

func (f *fakeStore) ListGrants(ctx context.Context, incidentID uuid.UUID) ([]store.IncidentGrantRow, error) {
	if err := f.enter(ctx, "ListGrants"); err != nil {
		return nil, err
	}
	out := make([]store.IncidentGrantRow, 0)
	for _, g := range f.grants[incidentID] {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].GranteeID < out[j].GranteeID
	})
	return out, nil
}

func (f *fakeStore) AddGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string, canAnnotate bool) error {
	if err := f.enter(ctx, "AddGrant"); err != nil {
		return err
	}
	if ownerID == "" {
		return fmt.Errorf("%w: owner id is required", store.ErrIncidentInvalid)
	}
	if err := store.ValidateGranteeID(granteeID); err != nil {
		return err
	}
	if f.busy {
		return store.ErrIncidentBusy
	}
	inc, ok := f.incidents[incidentID]
	if !ok {
		return store.ErrIncidentNotFound
	}
	if inc.OwnerID != ownerID {
		return store.ErrNotOwner
	}
	if granteeID == ownerID {
		return nil
	}
	if f.grants[incidentID] == nil {
		f.grants[incidentID] = map[string]store.IncidentGrantRow{}
	}
	g, exists := f.grants[incidentID][granteeID]
	if !exists && len(f.grants[incidentID]) >= store.IncidentMaxGrants {
		return store.ErrGrantLimit
	}
	if !exists {
		g = store.IncidentGrantRow{IncidentID: incidentID, GranteeID: granteeID, GrantedBy: ownerID, CreatedAt: f.tick()}
	}
	g.CanAnnotate = canAnnotate
	f.grants[incidentID][granteeID] = g
	return nil
}

func (f *fakeStore) RemoveGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string) error {
	if err := f.enter(ctx, "RemoveGrant"); err != nil {
		return err
	}
	if err := store.ValidateGranteeID(granteeID); err != nil {
		return err
	}
	inc, ok := f.incidents[incidentID]
	if !ok {
		return store.ErrIncidentNotFound
	}
	if inc.OwnerID != ownerID {
		return store.ErrNotOwner
	}
	if _, ok := f.grants[incidentID][granteeID]; !ok {
		return store.ErrGrantNotFound
	}
	delete(f.grants[incidentID], granteeID)
	return nil
}

// fakeCapturer returns a fixed report: for the handler-side path the real
// collector cannot produce (an item that fails row validation).
type fakeCapturer struct {
	report CaptureReport
	err    error
	calls  int
}

func (f *fakeCapturer) Capture(context.Context, CaptureRequest) (CaptureReport, error) {
	f.calls++
	return f.report, f.err
}

// ---------------------------------------------------------------------------
// Harness extensions
// ---------------------------------------------------------------------------

// collect wires the real Collector with the given sources into the handler.
func (hs *harness) collect(t *testing.T, sources ...Source) {
	t.Helper()
	hs.h.collector = newTestCollector(t, testLimits(), sources...)
}

// recordingSource is a complete source that records whether it ran.
func recordingSource(id string, ran *bool, items ...Evidence) Source {
	return stubSource{id: id, fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		*ran = true
		return SourceResult{Items: items, Completeness: CompletenessComplete}, nil
	}}
}

// nsItem is completeItem in a chosen namespace (a distinct scope).
func nsItem(ns, name string) Evidence {
	e := completeItem(name)
	e.Source.Namespace = ns
	return e
}

func captureBody(ns, kind, name string) string {
	return fmt.Sprintf(`{"namespace":%q,"kind":%q,"name":%q}`, ns, kind, name)
}

var defaultCapture = captureBody(testNS, "Deployment", testName)

func (hs *harness) capture(t *testing.T, user *auth.User, id uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return hs.do(t, hs.h.HandleCapture, http.MethodPost, request{user: user, incidentID: id.String(), body: body})
}

func (hs *harness) export(t *testing.T, user *auth.User, id uuid.UUID, format string) *httptest.ResponseRecorder {
	t.Helper()
	q := ""
	if format != "" {
		q = "?format=" + format
	}
	return hs.do(t, hs.h.HandleExport, http.MethodGet, request{user: user, incidentID: id.String(), query: q})
}

func (hs *harness) addGrant(t *testing.T, user *auth.User, id uuid.UUID, grantee string, canAnnotate bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"granteeId":%q,"canAnnotate":%t}`, grantee, canAnnotate)
	return hs.do(t, hs.h.HandleAddGrant, http.MethodPost, request{user: user, incidentID: id.String(), body: body})
}

// removeGrant issues DELETE /incidents/{id}/grants/{granteeID}; the grantee
// is a chi URL param the shared request helper does not know.
func (hs *harness) removeGrant(t *testing.T, user *auth.User, id uuid.UUID, grantee string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/incidents", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("incidentID", id.String())
	rctx.URLParams.Add("granteeID", grantee)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, user)
	ctx = middleware.WithClusterID(ctx, "local")
	w := httptest.NewRecorder()
	hs.h.HandleRemoveGrant(w, r.WithContext(ctx))
	return w
}

func intOf(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("no numeric %q in %v", key, m)
	}
	return int(v)
}

// evidenceIDs returns the ids of a response's evidence items, sorted.
func evidenceIDs(items []any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["id"].(string))
	}
	sort.Strings(out)
	return out
}

// withheldIDs returns "id:reason" of placeholders, sorted.
func withheldIDs(items []any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		m := it.(map[string]any)
		out = append(out, m["id"].(string)+":"+m["withheldReason"].(string))
	}
	sort.Strings(out)
	return out
}

func equalStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s differ:\n got %v\nwant %v", what, got, want)
	}
}

// exportDoc decodes a JSON export (a plain document, not an API envelope).
func exportDoc(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("export is not JSON: %v\n%s", err, w.Body.String())
	}
	if _, ok := out["data"]; ok {
		t.Fatalf("export is wrapped in the API envelope; it must be a plain document")
	}
	return out
}

// detailAll reads the whole incident through the detail endpoint at the
// maximum page size (every fixture here fits one page).
func (hs *harness) detailAll(t *testing.T, user *auth.User, id uuid.UUID) map[string]any {
	t.Helper()
	w := hs.do(t, hs.h.HandleGet, http.MethodGet, request{user: user, incidentID: id.String(), query: "?limit=200"})
	wantStatus(t, w, http.StatusOK)
	return data(t, w)
}

// ---------------------------------------------------------------------------
// Capture (P3, P5, P14, P15)
// ---------------------------------------------------------------------------

func TestCaptureIsOwnerOnly(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))

	// A collaborator with can_annotate can see the incident but not capture
	// into it (P3).
	w := hs.capture(t, bob, id, defaultCapture)
	wantStatus(t, w, http.StatusForbidden)
	// A stranger learns nothing (P1).
	w = hs.capture(t, carol, id, defaultCapture)
	wantStatus(t, w, http.StatusNotFound)
	// An admin without a grant is a stranger.
	w = hs.capture(t, root, id, defaultCapture)
	wantStatus(t, w, http.StatusNotFound)
	if ran {
		t.Fatal("a non-owner's capture ran a source")
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("a non-owner's capture persisted evidence")
	}
	if got := hs.audit.actions(); len(got) != 0 {
		t.Fatalf("refused captures were audited: %v", got)
	}

	w = hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusOK)
	if !ran {
		t.Fatal("the owner's capture did not run the source")
	}
}

func TestCaptureRejectsRemoteClusterHeaderBeforeCapture(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))

	w := hs.do(t, hs.h.HandleCapture, http.MethodPost, request{user: alice, incidentID: id.String(), cluster: "prod-east", body: defaultCapture})
	wantStatus(t, w, http.StatusBadRequest)
	reason, extra := errorOf(t, w)
	if reason != ReasonRemoteCaptureUnsupported {
		t.Fatalf("reason = %q, want %q", reason, ReasonRemoteCaptureUnsupported)
	}
	if extra["selectedCluster"] != "prod-east" {
		t.Fatalf("extra = %v", extra)
	}
	if ran {
		t.Fatal("a remote-cluster capture ran a source")
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("a remote-cluster capture persisted evidence")
	}
}

func TestCaptureRejectsClusterAndUIDFromBody(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))

	for name, body := range map[string]string{
		"clusterId": `{"namespace":"payments","kind":"Deployment","name":"web","clusterId":"prod-east"}`,
		"cluster":   `{"namespace":"payments","kind":"Deployment","name":"web","cluster":"prod-east"}`,
		"uid":       `{"namespace":"payments","kind":"Deployment","name":"web","uid":"11111111-1111-4111-8111-111111111111"}`,
		"targetUid": `{"namespace":"payments","kind":"Deployment","name":"web","target":{"uid":"x"}}`,
	} {
		w := hs.capture(t, alice, id, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s in body: status %d, want 400\n%s", name, w.Code, w.Body.String())
		}
		// Refused by the strict body decode, not downstream by the
		// collector's own remote guard: the field is unknown to the API.
		if !strings.Contains(w.Body.String(), "unknown field") {
			t.Fatalf("%s in body was not refused as an unknown field:\n%s", name, w.Body.String())
		}
	}
	if ran {
		t.Fatal("a body naming a cluster or uid ran a source")
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("a rejected body persisted evidence")
	}
}

func TestCaptureValidatesTargetAgainstSupportedKinds(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))

	for name, body := range map[string]string{
		"node":          captureBody("payments", "Node", "worker-1"),
		"unknown kind":  captureBody("payments", "Widget", "web"),
		"lowercase":     captureBody("payments", "deployment", "web"),
		"empty name":    captureBody("payments", "Deployment", ""),
		"bad name":      captureBody("payments", "Deployment", "Web/../x"),
		"bad namespace": captureBody("pay ments", "Deployment", "web"),
		"not json":      `{"namespace":`,
	} {
		w := hs.capture(t, alice, id, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400\n%s", name, w.Code, w.Body.String())
		}
	}
	if ran {
		t.Fatal("an invalid target ran a source")
	}
}

func TestCapturePersistsCollectorItemsAndAuditsCounts(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web"), completeItem("web-canary")),
		completeSource("events", nsItem(testNS, "web-events")))

	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	if d["completeness"] != "complete" {
		t.Fatalf("completeness = %v", d["completeness"])
	}
	if got := intOf(t, d, "collected"); got != 3 {
		t.Fatalf("collected = %d, want 3", got)
	}
	if got := intOf(t, d, "inserted"); got != 3 {
		t.Fatalf("inserted = %d, want 3", got)
	}
	if got := intOf(t, d, "deduplicated") + intOf(t, d, "dropped"); got != 0 {
		t.Fatalf("deduplicated+dropped = %d, want 0", got)
	}
	if got := len(list(d["sources"])); got != 2 {
		t.Fatalf("sources = %d, want 2", got)
	}
	if _, echoed := d["items"]; echoed {
		t.Fatal("capture response echoes items; evidence is read through the filtered list")
	}
	if got := len(hs.st.evidence[id]); got != 3 {
		t.Fatalf("persisted %d rows, want 3", got)
	}
	for _, r := range hs.st.evidence[id] {
		if r.ClusterID != "local" {
			t.Fatalf("row stored under cluster %q, want local", r.ClusterID)
		}
	}
	// The insert ran under a deadline-bearing context (review obligation).
	if n := len(hs.st.deadlines); n == 0 || !hs.st.deadlines[n-1] {
		t.Fatal("InsertBatch was called without a deadline")
	}
	// Audited with counts only: no namespace, name or kind of the target.
	acts := hs.audit.actions()
	if len(acts) != 1 || acts[0] != "incident_capture:success" {
		t.Fatalf("audit = %v", acts)
	}
	detail := hs.audit.entries[0].Detail
	for _, s := range []string{"inserted 3", "collected 3", "deduplicated 0", "dropped 0", "completeness complete"} {
		if !strings.Contains(detail, s) {
			t.Fatalf("audit detail %q lacks %q", detail, s)
		}
	}
	for _, s := range []string{testNS, testName, "Deployment", "web-events"} {
		if strings.Contains(detail, s) {
			t.Fatalf("audit detail %q names the target (%q)", detail, s)
		}
	}
}

func TestCapturePartialSourceStillPersistsSucceeded(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	failing := stubSource{id: "diagnostics", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		return SourceResult{}, fmt.Errorf("rules engine exploded in namespace %s", testNS)
	}}
	hs.collect(t, failing, completeSource("object", completeItem("web")))

	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	if d["completeness"] != "partial" {
		t.Fatalf("completeness = %v, want partial", d["completeness"])
	}
	if got := intOf(t, d, "inserted"); got != 1 {
		t.Fatalf("inserted = %d, want 1", got)
	}
	byID := map[string]map[string]any{}
	for _, s := range list(d["sources"]) {
		m := s.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["diagnostics"]["completeness"] != "failed" || byID["object"]["completeness"] != "complete" {
		t.Fatalf("sources = %v", byID)
	}
	// The per-source detail is the collector's fixed text, never the
	// adapter's error (which could name a namespace).
	if det, _ := byID["diagnostics"]["detail"].(string); strings.Contains(det, testNS) {
		t.Fatalf("source detail leaks scope: %q", det)
	}
	if got := len(hs.st.evidence[id]); got != 1 {
		t.Fatalf("persisted %d rows, want 1", got)
	}
}

func TestCaptureDropsInvalidItemButPersistsSiblings(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	good := completeItem("web")
	good.CollectedAt, good.CaptureKey = fixedNow, "k-good"
	bad := completeItem("web-bad")
	bad.CollectedAt, bad.CaptureKey = fixedNow, "k-bad"
	bad.Completeness = "sort of" // fails ValidateEvidenceRow
	hs.h.collector = &fakeCapturer{report: CaptureReport{
		Completeness: CompletenessComplete, CollectedAt: fixedNow,
		Sources: []SourceReport{{ID: "object", Completeness: CompletenessComplete, Items: 2}},
		Items:   []Evidence{bad, good},
	}}

	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusOK)
	d := data(t, w)
	if intOf(t, d, "inserted") != 1 || intOf(t, d, "dropped") != 1 || intOf(t, d, "collected") != 2 {
		t.Fatalf("counts = %v", d)
	}
	rows := hs.st.evidence[id]
	if len(rows) != 1 || rows[0].CaptureKey != "k-good" {
		t.Fatalf("persisted rows = %+v, want only k-good", rows)
	}
}

func TestRepeatedCaptureDoesNotDuplicateEvidence(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.collect(t, completeSource("object", completeItem("web"), completeItem("web-canary")))

	first := data(t, hs.capture(t, alice, id, defaultCapture))
	second := data(t, hs.capture(t, alice, id, defaultCapture))
	if intOf(t, first, "inserted") != 2 {
		t.Fatalf("first inserted = %v", first["inserted"])
	}
	if intOf(t, second, "inserted") != 0 || intOf(t, second, "deduplicated") != 2 {
		t.Fatalf("second capture: inserted %v deduplicated %v, want 0 and 2", second["inserted"], second["deduplicated"])
	}
	if got := len(hs.st.evidence[id]); got != 2 {
		t.Fatalf("persisted %d rows after two captures, want 2", got)
	}
}

func TestCaptureRejectsOverSizeBatchBeforeWrite(t *testing.T) {
	t.Run("items", func(t *testing.T) {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		hs.h.limits.MaxItems = 1
		hs.collect(t, completeSource("object", completeItem("a"), completeItem("b")))
		w := hs.capture(t, alice, id, defaultCapture)
		wantStatus(t, w, http.StatusRequestEntityTooLarge)
		reason, extra := errorOf(t, w)
		if reason != ReasonEvidenceLimitExceeded || extra["limit"] != "items" || intOf(t, extra, "current") != 0 || intOf(t, extra, "attempted") != 2 || intOf(t, extra, "max") != 1 {
			t.Fatalf("reason %q extra %v", reason, extra)
		}
		if len(hs.st.evidence[id]) != 0 {
			t.Fatal("an over-limit batch persisted rows")
		}
		if acts := hs.audit.actions(); len(acts) != 1 || acts[0] != "incident_capture:failure" {
			t.Fatalf("audit = %v", acts)
		}
	})
	t.Run("item_bytes reports attempted since current is zero", func(t *testing.T) {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		hs.h.limits.MaxItemBytes = MinMaxBytes
		big := completeItem("a")
		big.Payload = json.RawMessage(`{"kind":"Pod","metadata":{"name":"a","annotations":{"note":"` + strings.Repeat("x", 100) + `"}}}`)
		hs.collect(t, completeSource("object", big))
		w := hs.capture(t, alice, id, defaultCapture)
		wantStatus(t, w, http.StatusRequestEntityTooLarge)
		reason, extra := errorOf(t, w)
		if reason != ReasonEvidenceLimitExceeded || extra["limit"] != "item_bytes" || intOf(t, extra, "current") != 0 || intOf(t, extra, "attempted") != len(big.Payload) {
			t.Fatalf("reason %q extra %v", reason, extra)
		}
	})
	t.Run("incident_bytes", func(t *testing.T) {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		hs.h.limits.MaxIncidentBytes = MinMaxBytes + 10
		hs.h.limits.MaxItemBytes = MinMaxBytes + 10
		hs.collect(t, completeSource("object", completeItem("a"), completeItem("b")))
		w := hs.capture(t, alice, id, defaultCapture)
		wantStatus(t, w, http.StatusRequestEntityTooLarge)
		reason, extra := errorOf(t, w)
		if reason != ReasonEvidenceLimitExceeded || extra["limit"] != "incident_bytes" {
			t.Fatalf("reason %q extra %v", reason, extra)
		}
	})
}

func TestCaptureScopeLimitReturns409(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.h.limits.MaxScopes = 1
	hs.collect(t, completeSource("object", nsItem("a", "x"), nsItem("b", "y")))
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusConflict)
	reason, extra := errorOf(t, w)
	if reason != ReasonScopeLimitExceeded || intOf(t, extra, "max") != 1 || intOf(t, extra, "attempted") != 2 {
		t.Fatalf("reason %q extra %v", reason, extra)
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("a scope-limited batch persisted rows")
	}
}

func TestCaptureIntoClosedIncidentIs409(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	closed := store.IncidentStatusClosed
	if err := hs.st.Update(context.Background(), id, alice.ID, nil, nil, &closed); err != nil {
		t.Fatal(err)
	}
	hs.collect(t, completeSource("object", completeItem("web")))
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusConflict)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentClosed {
		t.Fatalf("reason = %q", reason)
	}
}

func TestCaptureBusyReturns503WithRetryAfter(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.busy = true
	hs.collect(t, completeSource("object", completeItem("web")))
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentBusy {
		t.Fatalf("reason = %q", reason)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on incident_busy")
	}
}

func TestCaptureOwnershipMissesMapToVisibility(t *testing.T) {
	// The incident is deleted between the visibility check and the insert:
	// the store's ErrIncidentNotFound is a 404.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.failOp["InsertBatch"] = store.ErrIncidentNotFound
	hs.collect(t, completeSource("object", completeItem("web")))
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusNotFound)
}

func TestCaptureWithoutDBReturns503(t *testing.T) {
	h := newHandlerWith(nil, nil, nil, &fakeAccess{allow: allowAll}, &fakeAudit{}, nil)
	hs := &harness{st: newFakeStore(), access: &fakeAccess{allow: allowAll}, audit: &fakeAudit{}, h: h}
	hs.collect(t, completeSource("object", completeItem("web")))
	w := hs.capture(t, alice, uuid.New(), defaultCapture)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonPersistenceUnavailable {
		t.Fatalf("reason = %q", reason)
	}

	// A database but no collector: capture is unavailable, not a panic.
	hs = newHarness(t)
	id := hs.seed(t, alice)
	w = hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonCaptureUnavailable {
		t.Fatalf("reason = %q", reason)
	}
}

func TestCaptureCancellationPersistsNothing(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ctx, cancel := context.WithCancel(context.Background())
	// The client goes away while the source is collecting: the collector
	// returns the context error with no items, and nothing is written.
	hs.collect(t, stubSource{id: "object", fn: func(context.Context, CaptureRequest) (SourceResult, error) {
		cancel()
		return SourceResult{Items: []Evidence{completeItem("web")}, Completeness: CompletenessComplete}, nil
	}})
	w := hs.do(t, hs.h.HandleCapture, http.MethodPost, request{user: alice, incidentID: id.String(), body: defaultCapture, ctx: ctx})
	if w.Code == http.StatusOK {
		t.Fatalf("a cancelled capture answered 200: %s", w.Body.String())
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("a cancelled capture persisted evidence")
	}
}

func TestCaptureUnknownSourceIs400(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.collect(t, completeSource("object", completeItem("web")))
	w := hs.capture(t, alice, id, `{"namespace":"payments","kind":"Deployment","name":"web","sources":["object","hubble"]}`)
	wantStatus(t, w, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "hubble") {
		t.Fatalf("body does not name the unknown source: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Evidence list
// ---------------------------------------------------------------------------

func TestEvidenceListAppliesTheSameFilterAndCounts(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, false)
	hs.addRows(
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", "p1"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "b", "p2"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "c", "p3"),
	)
	hs.access.allow = func(_, _, _, ns string) bool { return ns == "a" }
	hs.access.errFor = func(c accessCall) error {
		if c.namespace == "c" {
			return fmt.Errorf("SAR transport down")
		}
		return nil
	}

	w := hs.do(t, hs.h.HandleListEvidence, http.MethodGet, request{user: bob, incidentID: id.String(), query: "?limit=2"})
	wantStatus(t, w, http.StatusOK)
	noRawTotals(t, w)
	d := data(t, w)
	visible, withheld := counts(t, d)
	if visible != 1 || withheld != 2 {
		t.Fatalf("counts = %d/%d, want 1/2 (whole incident)", visible, withheld)
	}
	// Newest first, page of two: p3 (unavailable) and p2 (forbidden).
	ph := withheldOf(t, d)
	if len(ph) != 2 || ph[0]["withheldReason"] != WithheldAuthorizationCheckUnavailable || ph[1]["withheldReason"] != WithheldForbidden {
		t.Fatalf("withheld = %v", ph)
	}
	if n := len(list(d["evidence"])); n != 0 {
		t.Fatalf("page carries %d visible items, want 0", n)
	}
	meta := decode(t, w)["metadata"].(map[string]any)
	if intOf(t, meta, "total") != 1 || meta["continue"] == "" {
		t.Fatalf("metadata = %v", meta)
	}
	next := meta["continue"].(string)
	w = hs.do(t, hs.h.HandleListEvidence, http.MethodGet, request{user: bob, incidentID: id.String(), query: "?limit=2&continue=" + next})
	wantStatus(t, w, http.StatusOK)
	d = data(t, w)
	if ids := evidenceIDs(list(d["evidence"])); len(ids) != 1 {
		t.Fatalf("second page evidence = %v", ids)
	}

	w = hs.do(t, hs.h.HandleListEvidence, http.MethodGet, request{user: carol, incidentID: id.String()})
	wantStatus(t, w, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// Grants (P2, P3)
// ---------------------------------------------------------------------------

func TestGrantAddAndRemoveAreOwnerOnly(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.grant(id, bob, true)

	for name, call := range map[string]func(u *auth.User) *httptest.ResponseRecorder{
		"list": func(u *auth.User) *httptest.ResponseRecorder {
			return hs.do(t, hs.h.HandleListGrants, http.MethodGet, request{user: u, incidentID: id.String()})
		},
		"add":    func(u *auth.User) *httptest.ResponseRecorder { return hs.addGrant(t, u, id, carol.ID, false) },
		"remove": func(u *auth.User) *httptest.ResponseRecorder { return hs.removeGrant(t, u, id, bob.ID) },
	} {
		if w := call(bob); w.Code != http.StatusForbidden {
			t.Fatalf("%s by a collaborator: status %d, want 403\n%s", name, w.Code, w.Body.String())
		}
		if w := call(carol); w.Code != http.StatusNotFound {
			t.Fatalf("%s by a stranger: status %d, want 404\n%s", name, w.Code, w.Body.String())
		}
		if w := call(root); w.Code != http.StatusNotFound {
			t.Fatalf("%s by an admin without a grant: status %d, want 404\n%s", name, w.Code, w.Body.String())
		}
	}
	if _, still := hs.st.grants[id][bob.ID]; !still {
		t.Fatal("a non-owner removed a grant")
	}
	if _, added := hs.st.grants[id][carol.ID]; added {
		t.Fatal("a non-owner added a grant")
	}
	if got := hs.audit.actions(); len(got) != 0 {
		t.Fatalf("refused grant calls were audited: %v", got)
	}
}

func TestGrantAddListRemoveFlowAndRevocationOnNextRequest(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	// An OIDC subject: never resolved against local_users.
	grantee := "oidc:https://idp.example.com/sub/4e2b|bob@example.com"

	w := hs.addGrant(t, alice, id, grantee, false)
	wantStatus(t, w, http.StatusCreated)
	g := data(t, w)
	if g["granteeId"] != grantee || g["canAnnotate"] != false || g["grantedBy"] != alice.ID || g["incidentId"] != id.String() {
		t.Fatalf("grant view = %v", g)
	}

	grantedUser := &auth.User{ID: grantee, Username: "bob", Provider: "oidc", KubernetesUsername: "bob"}
	d := data(t, hs.get(t, grantedUser, id))
	inc := d["incident"].(map[string]any)
	if inc["role"] != "collaborator" || inc["canAnnotate"] != false {
		t.Fatalf("grantee sees %v", inc)
	}

	// Raising can_annotate updates the existing grant.
	w = hs.addGrant(t, alice, id, grantee, true)
	wantStatus(t, w, http.StatusCreated)
	if d := data(t, hs.get(t, grantedUser, id)); d["incident"].(map[string]any)["canAnnotate"] != true {
		t.Fatal("can_annotate update not applied")
	}

	w = hs.do(t, hs.h.HandleListGrants, http.MethodGet, request{user: alice, incidentID: id.String()})
	wantStatus(t, w, http.StatusOK)
	items := list(decode(t, w)["data"])
	if len(items) != 1 || items[0].(map[string]any)["granteeId"] != grantee {
		t.Fatalf("grants list = %v", items)
	}

	w = hs.removeGrant(t, alice, id, grantee)
	wantStatus(t, w, http.StatusNoContent)
	// Revocation is effective on the very next request (P7): there is no
	// session to invalidate.
	wantStatus(t, hs.get(t, grantedUser, id), http.StatusNotFound)
	wantStatus(t, hs.export(t, grantedUser, id, "json"), http.StatusNotFound)

	acts := hs.audit.actions()
	want := []string{"incident_grant_add:success", "incident_grant_add:success", "incident_grant_remove:success"}
	equalStrings(t, "audit", acts, want)
}

func TestSelfGrantIsNoOp(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	w := hs.addGrant(t, alice, id, alice.ID, false)
	wantStatus(t, w, http.StatusNoContent)
	if len(hs.st.grants[id]) != 0 {
		t.Fatal("a self-grant wrote a row")
	}
	if got := hs.audit.actions(); len(got) != 0 {
		t.Fatalf("a self-grant was audited: %v", got)
	}
}

func TestGrantLimitReturns409(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for i := range store.IncidentMaxGrants {
		hs.grant(id, &auth.User{ID: fmt.Sprintf("local:u%d", i)}, false)
	}
	w := hs.addGrant(t, alice, id, "local:one-too-many", false)
	wantStatus(t, w, http.StatusConflict)
	reason, extra := errorOf(t, w)
	if reason != ReasonGrantLimitReached || intOf(t, extra, "max") != store.IncidentMaxGrants {
		t.Fatalf("reason %q extra %v", reason, extra)
	}
	// An existing grantee can still be updated at the cap.
	w = hs.addGrant(t, alice, id, "local:u0", true)
	wantStatus(t, w, http.StatusCreated)
}

func TestGrantRejectsInvalidGranteeIDAndUnknownFields(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for name, body := range map[string]string{
		"empty":         `{"granteeId":"","canAnnotate":true}`,
		"control char":  `{"granteeId":"local:bob\u0007","canAnnotate":true}`,
		"too long":      `{"granteeId":"` + strings.Repeat("a", store.IncidentMaxGranteeIDBytes+1) + `"}`,
		"unknown field": `{"granteeId":"local:bob","canAnnotate":true,"role":"owner"}`,
	} {
		w := hs.do(t, hs.h.HandleAddGrant, http.MethodPost, request{user: alice, incidentID: id.String(), body: body})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400\n%s", name, w.Code, w.Body.String())
		}
	}
	if len(hs.st.grants[id]) != 0 {
		t.Fatal("an invalid request wrote a grant")
	}
	w := hs.removeGrant(t, alice, id, "local:bob\x01")
	wantStatus(t, w, http.StatusBadRequest)
}

func TestRemoveMissingGrantIs404(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	w := hs.removeGrant(t, alice, id, bob.ID)
	wantStatus(t, w, http.StatusNotFound)
	if acts := hs.audit.actions(); len(acts) != 1 || acts[0] != "incident_grant_remove:failure" {
		t.Fatalf("audit = %v", acts)
	}
}

func TestRemoveGrantDecodesEscapedGranteeID(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	grantee := "ldap:cn=bob/ou=eng"
	hs.grant(id, &auth.User{ID: grantee}, false)
	w := hs.removeGrant(t, alice, id, "ldap:cn=bob%2Fou=eng")
	wantStatus(t, w, http.StatusNoContent)
	if _, still := hs.st.grants[id][grantee]; still {
		t.Fatal("escaped grantee id was not decoded")
	}
}

func TestGrantBusyReturns503(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.st.busy = true
	w := hs.addGrant(t, alice, id, bob.ID, false)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentBusy {
		t.Fatalf("reason = %q", reason)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestGrantDoesNotConveyKubernetesAuthority(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "p1"),
		hs.row(id, EvidenceKindEventList, "", "events", "Pod", "payments", "p1"),
	)
	wantStatus(t, hs.addGrant(t, alice, id, bob.ID, true), http.StatusCreated)
	// bob holds a grant but no Kubernetes access anywhere.
	hs.access.allow = func(_, _, _, _ string) bool { return false }

	d := data(t, hs.get(t, bob, id))
	if v, wh := counts(t, d); v != 0 || wh != 2 {
		t.Fatalf("counts = %d/%d, want 0/2", v, wh)
	}
	if len(list(d["evidence"])) != 0 || len(withheldOf(t, d)) != 2 {
		t.Fatalf("grantee without access saw evidence: %v", d)
	}
	for _, format := range []string{"json", "markdown"} {
		w := hs.export(t, bob, id, format)
		wantStatus(t, w, http.StatusOK)
		if strings.Contains(w.Body.String(), `"name":"p1"`) || strings.Contains(w.Body.String(), "\"p1\"") {
			t.Fatalf("%s export leaks the withheld item's name: %s", format, w.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Export (P10, P12, P16)
// ---------------------------------------------------------------------------

// mixedFixture seeds an incident with rows across three namespaces and an
// access checker that allows a, denies b and fails on c, so every
// placeholder reason is present.
func mixedFixture(t *testing.T) (*harness, uuid.UUID) {
	t.Helper()
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", "p1"),
		hs.row(id, EvidenceKindEventList, "", "events", "Pod", "a", "p1"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "b", "p2"),
		hs.row(id, EvidenceKindDiagnosticCheck, "apps", "deployments", "Deployment", "a", "web"),
		hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "c", "p3"),
	)
	hs.access.allow = func(_, _, _, ns string) bool { return ns == "a" }
	hs.access.errFor = func(c accessCall) error {
		if c.namespace == "c" {
			return fmt.Errorf("SAR transport down")
		}
		return nil
	}
	if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, "first note"); err != nil {
		t.Fatal(err)
	}
	return hs, id
}

// assertExportParity asserts the JSON export for u is exactly the detail
// read's evidence set, placeholders and counts for the same u.
func assertExportParity(t *testing.T, hs *harness, u *auth.User, id uuid.UUID) map[string]any {
	t.Helper()
	detail := hs.detailAll(t, u, id)
	w := hs.export(t, u, id, "json")
	wantStatus(t, w, http.StatusOK)
	doc := exportDoc(t, w)

	equalStrings(t, "evidence ids", evidenceIDs(list(doc["evidence"])), evidenceIDs(list(detail["evidence"])))
	equalStrings(t, "withheld placeholders", withheldIDs(list(doc["withheld"])), withheldIDs(list(detail["withheld"])))
	dv, dw := counts(t, detail)
	ev, ew := counts(t, doc)
	if dv != ev || dw != ew {
		t.Fatalf("counts: export %d/%d, detail %d/%d", ev, ew, dv, dw)
	}
	if len(list(doc["evidence"])) != ev || len(list(doc["withheld"])) != ew {
		t.Fatalf("export carries %d/%d items but counts say %d/%d", len(list(doc["evidence"])), len(list(doc["withheld"])), ev, ew)
	}
	// Placeholders carry only the five policy fields, in the export too.
	withheldOf(t, doc)
	if doc["truncated"] != false {
		t.Fatalf("truncated = %v on a small export", doc["truncated"])
	}
	return doc
}

func TestExportMatchesVisibleEvidenceExactly(t *testing.T) {
	hs, id := mixedFixture(t)
	doc := assertExportParity(t, hs, alice, id)
	if n := len(list(doc["evidence"])); n != 3 {
		t.Fatalf("owner export has %d items, want 3 (namespace a)", n)
	}
	by := doc["withheldByReason"].(map[string]any)
	if intOf(t, by, WithheldForbidden) != 1 || intOf(t, by, WithheldAuthorizationCheckUnavailable) != 1 {
		t.Fatalf("withheldByReason = %v", by)
	}
	if doc["exportedBy"] != alice.ID || doc["schema"] != exportSchema {
		t.Fatalf("doc header = %v / %v", doc["exportedBy"], doc["schema"])
	}
	if notes := list(doc["notes"]); len(notes) != 1 || notes[0].(map[string]any)["body"] != "first note" {
		t.Fatalf("notes = %v", notes)
	}
	noRawTotals(t, hs.export(t, alice, id, "json"))
}

func TestExportByCollaboratorAppliesSameFilter(t *testing.T) {
	hs, id := mixedFixture(t)
	hs.grant(id, bob, false)
	ownerDoc := assertExportParity(t, hs, alice, id)
	// bob may read namespace b only (the fake checker is per request, not
	// per user, so the owner's view was taken first): his export must be HIS
	// filtered view, not the owner's.
	hs.access.allow = func(_, _, _, ns string) bool { return ns == "b" }
	hs.access.errFor = nil
	doc := assertExportParity(t, hs, bob, id)
	if ids := evidenceIDs(list(doc["evidence"])); len(ids) != 1 {
		t.Fatalf("collaborator export has %d items, want 1 (namespace b)", len(ids))
	}
	if doc["incident"].(map[string]any)["role"] != "collaborator" {
		t.Fatal("export does not carry the exporter's role")
	}
	if len(list(ownerDoc["evidence"])) == len(list(doc["evidence"])) {
		t.Fatal("owner and collaborator exports are the same set despite different authorization")
	}
}

func TestExportExcludesCredentialsAndSecretValues(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	// A secret-derived row whose (already redacted) payload still carries a
	// sentinel: without `get secrets` it is withheld, and the sentinel must
	// not reach either export in any form.
	secret := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "p1")
	secret.SecretDerived = true
	secret.Redaction = json.RawMessage(`{"secretDerived":true}`)
	secret.Payload = json.RawMessage(`{"kind":"Pod","metadata":{"name":"p1"},"marker":"SENTINEL-DB-PASSWORD-KEY"}`)
	plain := hs.row(id, EvidenceKindEventList, "", "events", "Pod", "payments", "p1")
	hs.addRows(secret, plain)
	hs.access.allow = func(_, _, res, _ string) bool { return res != "secrets" }

	for _, format := range []string{"json", "markdown"} {
		w := hs.export(t, alice, id, format)
		wantStatus(t, w, http.StatusOK)
		body := w.Body.String()
		if strings.Contains(body, "SENTINEL") {
			t.Fatalf("%s export carries a withheld secret-derived payload:\n%s", format, body)
		}
		if !strings.Contains(body, WithheldForbidden) {
			t.Fatalf("%s export does not show the withheld placeholder", format)
		}
	}
	// With `get secrets` the owner sees it (P11.2), as on the detail read.
	hs.access.allow = allowAll
	assertExportParity(t, hs, alice, id)
}

func TestExportRejectsHTMLFormat(t *testing.T) {
	hs, id := mixedFixture(t)
	for _, format := range []string{"html", "HTML", "xml", "pdf", "json,html", "markdown%00html"} {
		w := hs.export(t, alice, id, format)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("format=%s: status %d, want 400\n%s", format, w.Code, w.Body.String())
		}
		if reason, _ := errorOf(t, w); reason != ReasonExportFormatInvalid {
			t.Fatalf("format=%s: reason %q", format, reason)
		}
	}
	if got := hs.audit.actions(); len(got) != 0 {
		t.Fatalf("rejected formats were audited: %v", got)
	}
	// Omitted means json.
	w := hs.export(t, alice, id, "")
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("default Content-Type = %q", ct)
	}
}

// fenceWalk walks markdown lines with CommonMark's backtick-fence rule (an
// opening fence of N backticks is closed only by a line of >= N backticks
// and nothing else) and reports, for each line, whether it was inside a
// fence, plus whether the document ends outside every fence.
func fenceWalk(md string) (inside []bool, balanced bool) {
	open := 0
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		run := 0
		for run < len(trimmed) && trimmed[run] == '`' {
			run++
		}
		onlyBackticks := run == len(trimmed)
		if open == 0 {
			if run >= 3 {
				open = run
				inside = append(inside, true) // the fence line itself
				continue
			}
			inside = append(inside, false)
			continue
		}
		inside = append(inside, true)
		if onlyBackticks && run >= open {
			open = 0
		}
	}
	return inside, open == 0
}

var sentinelRe = regexp.MustCompile(`<script>|<img|^# (pwned|injected)|\]\(javascript:`)

func TestExportEscapesUntrustedTextInMarkdown(t *testing.T) {
	hs := newHarness(t)
	id, err := hs.st.Create(context.Background(), store.IncidentRow{
		OwnerID: alice.ID, Title: "<script>alert(1)</script> ``` # pwned", Summary: "```\n# injected heading\n````\n<img src=x onerror=alert(1)>",
		WindowStart: fixedNow.Add(-time.Hour), RetentionDaysAtCapture: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A note that tries every fence length up to five and a link.
	note := "```\n````\n`````\n[x](javascript:alert(1))\n<script>steal()</script>"
	if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, note); err != nil {
		t.Fatal(err)
	}
	// An evidence item whose name and payload carry markup and backticks.
	row := hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", "p1")
	row.Name = "p1`<img src=x>"
	row.Payload = json.RawMessage(`{"kind":"Pod","metadata":{"name":"p1","annotations":{"a":"` + "```` <script>x</script> ```" + `"}}}`)
	hs.addRows(row)

	w := hs.export(t, alice, id, "markdown")
	wantStatus(t, w, http.StatusOK)
	md := w.Body.String()
	lines := strings.Split(md, "\n")
	inside, balanced := fenceWalk(md)
	if !balanced {
		t.Fatalf("markdown ends inside a fence (content closed or opened one):\n%s", md)
	}
	found := 0
	for i, line := range lines {
		if sentinelRe.MatchString(line) {
			found++
			if !inside[i] {
				t.Fatalf("untrusted text rendered outside a fence at line %d: %q\n%s", i+1, line, md)
			}
		}
		// Outside fences only server-generated text appears: no angle
		// brackets at all.
		if !inside[i] && strings.ContainsAny(line, "<>") {
			t.Fatalf("angle bracket outside a fence at line %d: %q", i+1, line)
		}
	}
	if found < 5 {
		t.Fatalf("expected the sentinels to be present (inside fences), found %d", found)
	}
	if !strings.Contains(md, "``````") {
		t.Fatal("no fence longer than the content's five-backtick run")
	}
}

func TestExportCountsWithheldItemsHonestly(t *testing.T) {
	hs, id := mixedFixture(t)
	w := hs.export(t, alice, id, "markdown")
	wantStatus(t, w, http.StatusOK)
	md := w.Body.String()
	if !strings.Contains(md, "3 readable by you, 2 withheld from you; 3 readable and 2 withheld included") {
		t.Fatalf("markdown does not state the counts honestly:\n%s", md)
	}
	if strings.Count(md, "withheld (forbidden)") != 1 || strings.Count(md, "withheld (authorization_check_unavailable)") != 1 {
		t.Fatalf("markdown placeholders:\n%s", md)
	}
	// Withheld rows' names never appear in either format.
	for _, format := range []string{"json", "markdown"} {
		body := hs.export(t, alice, id, format).Body.String()
		for _, leak := range []string{"p2", "p3", `"namespace":"b"`, `"namespace":"c"`} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s export leaks withheld scope %q", format, leak)
			}
		}
	}
}

func TestExportSetsAttachmentAndNosniffHeaders(t *testing.T) {
	hs, id := mixedFixture(t)
	for format, want := range map[string]struct{ ct, ext string }{
		"json":     {"application/json", "json"},
		"markdown": {"text/markdown; charset=utf-8", "md"},
	} {
		w := hs.export(t, alice, id, format)
		wantStatus(t, w, http.StatusOK)
		if got := w.Header().Get("Content-Type"); got != want.ct {
			t.Fatalf("%s Content-Type = %q, want %q", format, got, want.ct)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options = %q", format, got)
		}
		cd := w.Header().Get("Content-Disposition")
		re := regexp.MustCompile(`^attachment; filename="incident-` + regexp.QuoteMeta(id.String()) + `-\d{8}\.` + want.ext + `"$`)
		if !re.MatchString(cd) {
			t.Fatalf("%s Content-Disposition = %q", format, cd)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q", format, got)
		}
	}
}

func TestExportIsAuditedWithCounts(t *testing.T) {
	hs, id := mixedFixture(t)
	hs.grant(id, bob, false)
	wantStatus(t, hs.export(t, alice, id, "json"), http.StatusOK)
	wantStatus(t, hs.export(t, bob, id, "markdown"), http.StatusOK)
	equalStrings(t, "audit", hs.audit.actions(), []string{"incident_export:success", "incident_export:success"})
	e := hs.audit.entries[0]
	if e.User != alice.Username || e.ResourceKind != "incident" {
		t.Fatalf("entry = %+v", e)
	}
	for _, s := range []string{"format json", "visible 3", "withheld 2", "truncated false"} {
		if !strings.Contains(e.Detail, s) {
			t.Fatalf("audit detail %q lacks %q", e.Detail, s)
		}
	}
	if strings.Contains(e.Detail, "p1") || strings.Contains(e.Detail, "first note") {
		t.Fatalf("audit detail carries evidence or note content: %q", e.Detail)
	}
	// Export reads are the only audited reads (P16); a failed export is a
	// failure entry.
	hs.st.failOp["ListByIncident"] = fmt.Errorf("pg: down")
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if acts := hs.audit.actions(); acts[len(acts)-1] != "incident_export:failure" {
		t.Fatalf("audit = %v", acts)
	}
}

func TestExportPagesThroughEvidenceAndNotes(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	// More rows than the export page size, and more notes than one page.
	for i := range store.IncidentMaxPageSize + 25 {
		hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "payments", fmt.Sprintf("p%03d", i)))
	}
	for i := range store.IncidentMaxPageSize + 3 {
		if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, fmt.Sprintf("note %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusOK)
	doc := exportDoc(t, w)
	if n := len(list(doc["evidence"])); n != store.IncidentMaxPageSize+25 {
		t.Fatalf("export has %d items, want %d", n, store.IncidentMaxPageSize+25)
	}
	if n := len(list(doc["notes"])); n != store.IncidentMaxPageSize+3 {
		t.Fatalf("export has %d notes, want %d", n, store.IncidentMaxPageSize+3)
	}
	if doc["truncated"] != false {
		t.Fatal("a complete export reports truncation")
	}
	// One access check per distinct scope for the whole export (P5).
	if n := len(hs.access.calls); n != 1 {
		t.Fatalf("export issued %d access checks for one scope, want 1", n)
	}
}

func TestExportStaysBoundedAndMarksTruncation(t *testing.T) {
	hs, id := mixedFixture(t)
	for i := range 10 {
		hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", fmt.Sprintf("big%d", i)))
	}
	// Room for about two items (each costs payload + overhead).
	hs.h.exportMax = 2*exportItemOverhead + 200

	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusOK)
	doc := exportDoc(t, w)
	if doc["truncated"] != true {
		t.Fatalf("a bounded export did not report truncation: %v", doc)
	}
	tr := doc["truncation"].(map[string]any)
	v, wh := counts(t, doc)
	included := len(list(doc["evidence"]))
	if included == 0 || included >= v {
		t.Fatalf("included %d of %d visible; expected a strict cut", included, v)
	}
	if intOf(t, tr, "evidenceOmitted") != v-included || intOf(t, tr, "withheldOmitted") != wh-len(list(doc["withheld"])) || tr["notesOmitted"] != true {
		t.Fatalf("truncation = %v (visible %d, included %d)", tr, v, included)
	}
	if w.Body.Len() > 64<<10 {
		t.Fatalf("truncated export is %d bytes", w.Body.Len())
	}

	md := hs.export(t, alice, id, "markdown").Body.String()
	if !strings.Contains(md, "## TRUNCATED") {
		t.Fatalf("markdown lacks the truncation marker:\n%s", md)
	}
	if !strings.Contains(hs.audit.entries[len(hs.audit.entries)-1].Detail, "truncated true") {
		t.Fatal("audit does not record the truncation")
	}
}

func TestExportOfNonVisibleIncidentIs404(t *testing.T) {
	hs, id := mixedFixture(t)
	wantStatus(t, hs.export(t, carol, id, "json"), http.StatusNotFound)
	wantStatus(t, hs.export(t, root, id, "markdown"), http.StatusNotFound)
	wantStatus(t, hs.export(t, alice, uuid.New(), "json"), http.StatusNotFound)
	if got := hs.audit.actions(); len(got) != 0 {
		t.Fatalf("refused exports were audited: %v", got)
	}
}
