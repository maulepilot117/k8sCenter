package incidents

// Review round 1 of PR #583: bulkheads, percent-encoded grantee ids through
// the real router, truncation branches, the closed pre-check, insert
// cancellation, and later-page export failures.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// fill occupies every slot of a bulkhead; the returned func drains it.
func fill(slots chan struct{}) func() {
	n := 0
	for {
		select {
		case slots <- struct{}{}:
			n++
		default:
			return func() {
				for range n {
					<-slots
				}
			}
		}
	}
}

func TestExportBulkheadFullIs503BusyAndSlotsAreReleased(t *testing.T) {
	hs, id := mixedFixture(t)
	if cap(hs.h.exportSlots) != exportConcurrency || exportConcurrency != 2 {
		t.Fatalf("export bulkhead size = %d, want 2", cap(hs.h.exportSlots))
	}
	drain := fill(hs.h.exportSlots)
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentBusy {
		t.Fatalf("reason = %q", reason)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on a full bulkhead")
	}
	// Round 2: refusals are audited as failures with a reason, counts only.
	if got := hs.audit.actions(); len(got) != 1 || got[0] != "incident_export:failure" || !strings.Contains(hs.audit.entries[0].Detail, "bulkhead full") {
		t.Fatalf("refused export audit = %v", got)
	}
	drain()

	// Released after success and after a failure: the next request gets in.
	wantStatus(t, hs.export(t, alice, id, "markdown"), http.StatusOK)
	if len(hs.h.exportSlots) != 0 {
		t.Fatal("slot not released after a successful export")
	}
	hs.st.failOp["ListByIncident"] = fmt.Errorf("pg: down")
	wantStatus(t, hs.export(t, alice, id, "json"), http.StatusServiceUnavailable)
	if len(hs.h.exportSlots) != 0 {
		t.Fatal("slot not released after a failed export")
	}
	delete(hs.st.failOp, "ListByIncident")
	wantStatus(t, hs.export(t, alice, id, "json"), http.StatusOK)
}

func TestCaptureBulkheadFullIs503BusyAndSlotsAreReleased(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))
	if cap(hs.h.captureSlots) != captureConcurrency || captureConcurrency != 4 {
		t.Fatalf("capture bulkhead size = %d, want 4", cap(hs.h.captureSlots))
	}
	drain := fill(hs.h.captureSlots)
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentBusy {
		t.Fatalf("reason = %q", reason)
	}
	if w.Header().Get("Retry-After") == "" || ran {
		t.Fatalf("full bulkhead: Retry-After %q, source ran %t", w.Header().Get("Retry-After"), ran)
	}
	drain()
	wantStatus(t, hs.capture(t, alice, id, defaultCapture), http.StatusOK)
	hs.st.busy = true
	wantStatus(t, hs.capture(t, alice, id, captureBody(testNS, "Deployment", "other")), http.StatusServiceUnavailable)
	hs.st.busy = false
	if len(hs.h.captureSlots) != 0 {
		t.Fatal("slot not released after success and failure")
	}
	wantStatus(t, hs.capture(t, alice, id, captureBody(testNS, "Deployment", "other")), http.StatusOK)
}

func TestExportStreamsTheSameDocumentShape(t *testing.T) {
	hs, id := mixedFixture(t)
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusOK)
	doc := exportDoc(t, w)
	want := []string{"counts", "evidence", "exportedAt", "exportedBy", "incident", "notes", "schema", "truncated", "withheld", "withheldByReason"}
	got := make([]string, 0, len(doc))
	for k := range doc {
		got = append(got, k)
	}
	sort.Strings(got)
	equalStrings(t, "top-level keys", got, want)
	if doc["schema"] != "k8scenter.incident.export/v1" {
		t.Fatalf("schema = %v", doc["schema"])
	}
	// The streamed encoder indents like the previous MarshalIndent and ends
	// the file with a newline.
	body := w.Body.String()
	if !strings.HasPrefix(body, "{\n  \"schema\"") || !strings.HasSuffix(body, "}\n") {
		t.Fatalf("unexpected framing:\n%.60s ... %q", body, body[len(body)-3:])
	}
}

// grantRouter mounts the grant handlers on a real chi router with the
// production route patterns, so the {granteeID} param is produced by chi's
// own path handling (RawPath vs Path), with the caller injected.
func (hs *harness) grantRouter(user *auth.User) http.Handler {
	with := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.ContextWithUser(r.Context(), user)
			next(w, r.WithContext(middleware.WithClusterID(ctx, "local")))
		}
	}
	mux := chi.NewRouter()
	mux.Post("/incidents/{incidentID}/grants", with(hs.h.HandleAddGrant))
	mux.Delete("/incidents/{incidentID}/grants/{granteeID}", with(hs.h.HandleRemoveGrant))
	return mux
}

func TestRemoveGrantRoundTripsEncodedIDsThroughTheRouter(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	mux := hs.grantRouter(alice)
	for _, grantee := range []string{"50%off", "a%41b", "ldap:cn=bob/ou=eng", "oidc:https://idp.example.com/sub|x@y"} {
		body := fmt.Sprintf(`{"granteeId":%q,"canAnnotate":false}`, grantee)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/incidents/"+id.String()+"/grants", strings.NewReader(body)))
		if w.Code != http.StatusCreated {
			t.Fatalf("add %q: status %d\n%s", grantee, w.Code, w.Body.String())
		}
		if _, ok := hs.st.grants[id][grantee]; !ok {
			t.Fatalf("add %q: grant not stored under the exact id", grantee)
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/incidents/"+id.String()+"/grants/"+url.PathEscape(grantee), nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("remove %q (path %q): status %d\n%s", grantee, url.PathEscape(grantee), w.Code, w.Body.String())
		}
		if _, still := hs.st.grants[id][grantee]; still {
			t.Fatalf("remove %q: grant still present", grantee)
		}
	}
}

func TestExportTruncationNotesOnlyCut(t *testing.T) {
	hs, id := mixedFixture(t)
	// Every evidence row and placeholder fits; the single note does not.
	fits := 0
	for _, r := range hs.st.evidence[id] {
		if r.Namespace == "a" { // readable by alice in this fixture
			fits += len(r.Payload) + exportItemOverhead
		} else {
			fits += exportWithheldOverhead
		}
	}
	hs.h.exportMax = fits + 10
	doc := exportDoc(t, hs.export(t, alice, id, "json"))
	v, wh := counts(t, doc)
	if len(list(doc["evidence"])) != v || len(list(doc["withheld"])) != wh {
		t.Fatalf("evidence should be complete: %d/%d of %d/%d", len(list(doc["evidence"])), len(list(doc["withheld"])), v, wh)
	}
	if doc["truncated"] != true {
		t.Fatal("note cut not reported")
	}
	tr := doc["truncation"].(map[string]any)
	if intOf(t, tr, "evidenceOmitted") != 0 || intOf(t, tr, "withheldOmitted") != 0 || tr["notesOmitted"] != true {
		t.Fatalf("truncation = %v", tr)
	}
	if len(list(doc["notes"])) != 0 {
		t.Fatal("a note that did not fit was included")
	}
}

func TestExportTruncationCutAtWithheldPlaceholder(t *testing.T) {
	hs, id := mixedFixture(t)
	// Newest row first is p3 in namespace c: a placeholder, which costs
	// exportWithheldOverhead; a budget below that cuts before any row.
	hs.h.exportMax = exportWithheldOverhead - 1
	doc := exportDoc(t, hs.export(t, alice, id, "json"))
	if len(list(doc["evidence"])) != 0 || len(list(doc["withheld"])) != 0 {
		t.Fatalf("expected nothing included: %v", doc)
	}
	tr := doc["truncation"].(map[string]any)
	if intOf(t, tr, "evidenceOmitted") != 3 || intOf(t, tr, "withheldOmitted") != 2 || tr["notesOmitted"] != true {
		t.Fatalf("truncation = %v", tr)
	}
	md := hs.export(t, alice, id, "markdown").Body.String()
	if !strings.Contains(md, "3 readable evidence items and 2 withheld placeholders were omitted; notes omitted: true") {
		t.Fatalf("markdown marker:\n%s", md)
	}
}

func TestExportTruncationWithoutNotesDoesNotClaimNotesOmitted(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	for i := range 5 {
		hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", fmt.Sprintf("p%d", i)))
	}
	hs.h.exportMax = exportItemOverhead + 100 // one item
	doc := exportDoc(t, hs.export(t, alice, id, "json"))
	tr := doc["truncation"].(map[string]any)
	if len(list(doc["evidence"])) != 1 || intOf(t, tr, "evidenceOmitted") != 4 || tr["notesOmitted"] != false {
		t.Fatalf("truncation = %v, evidence %d", tr, len(list(doc["evidence"])))
	}
}

// racingStore adds a row to the incident after the scope rows were counted,
// before the first page is read.
type racingStore struct {
	*fakeStore
	incidentID uuid.UUID
	done       bool
}

func (s *racingStore) ListByIncident(ctx context.Context, id uuid.UUID, limit int, cursor string) ([]store.IncidentEvidenceRow, string, error) {
	if !s.done {
		s.done = true
		r := store.IncidentEvidenceRow{
			ID: uuid.New(), IncidentID: s.incidentID, EvidenceKind: EvidenceKindObjectSummary, Mode: store.EvidenceModeSnapshot,
			ClusterID: "local", Resource: "pods", SourceKind: "Pod", Namespace: "a", Name: "late", SourceUID: "uid-late",
			CollectedAt: s.fakeStore.tick(), Completeness: store.EvidenceCompletenessComplete, Redaction: json.RawMessage(`{}`),
			Payload: json.RawMessage(`{"kind":"Pod"}`), CaptureKey: "k-late",
		}
		s.fakeStore.evidence[s.incidentID] = append(s.fakeStore.evidence[s.incidentID], r)
	}
	return s.fakeStore.ListByIncident(ctx, id, limit, cursor)
}

func TestExportOmittedCountsNeverNegative(t *testing.T) {
	// A capture committing between the counts read and the page read makes
	// the page longer than the counts: the omitted counts clamp at zero.
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", "p1"))
	if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, strings.Repeat("n", 2000)); err != nil {
		t.Fatal(err)
	}
	hs.h.evidence = &racingStore{fakeStore: hs.st, incidentID: id}
	hs.h.exportMax = 2*exportItemOverhead + 200 // both rows fit, the note does not
	doc := exportDoc(t, hs.export(t, alice, id, "json"))
	v, _ := counts(t, doc)
	if v != 1 || len(list(doc["evidence"])) != 2 {
		t.Fatalf("fixture: counts %d, included %d (want 1 and 2)", v, len(list(doc["evidence"])))
	}
	tr := doc["truncation"].(map[string]any)
	if intOf(t, tr, "evidenceOmitted") != 0 {
		t.Fatalf("evidenceOmitted = %v, want 0 (clamped)", tr["evidenceOmitted"])
	}
}

func TestCaptureIntoClosedIncidentIsRefusedBeforeAnyRead(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	closed := store.IncidentStatusClosed
	if err := hs.st.Update(context.Background(), id, alice.ID, nil, nil, &closed); err != nil {
		t.Fatal(err)
	}
	ran := false
	hs.collect(t, recordingSource("object", &ran, completeItem("web")))
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusConflict)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentClosed {
		t.Fatalf("reason = %q", reason)
	}
	if ran {
		t.Fatal("a capture into a closed incident ran an impersonated read")
	}
	if len(hs.st.evidence[id]) != 0 {
		t.Fatal("persisted into a closed incident")
	}
}

func TestCaptureInsertCancelledIsReportedAsNothingRecorded(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		hs.collect(t, completeSource("object", completeItem("web")))
		hs.st.failOp["InsertBatch"] = fmt.Errorf("insert incident_evidence: %w", cause)
		w := hs.capture(t, alice, id, defaultCapture)
		wantStatus(t, w, http.StatusServiceUnavailable)
		if !strings.Contains(w.Body.String(), "nothing was recorded") {
			t.Fatalf("%v: body %s", cause, w.Body.String())
		}
		if reason, _ := errorOf(t, w); reason == ReasonStoreUnavailable {
			t.Fatalf("%v mapped to the generic store failure", cause)
		}
		// A live request that hit our own insert deadline is audited as such
		// (and answered retryable busy); a cancellation as cancelled.
		wantDetail := "cancelled"
		if errors.Is(cause, context.DeadlineExceeded) {
			wantDetail = "ran out of time before it could be saved"
		}
		acts := hs.audit.actions()
		if len(acts) != 1 || acts[0] != "incident_capture:failure" || !strings.Contains(hs.audit.entries[0].Detail, wantDetail) {
			t.Fatalf("audit = %v / %q, want detail containing %q", acts, hs.audit.entries[0].Detail, wantDetail)
		}
	}
}

// pageFailStore fails the Nth call of one list method.
type pageFailStore struct {
	*fakeStore
	failEvidenceCall int
	failNotesCall    int
	evidenceCalls    int
	notesCalls       int
}

func (s *pageFailStore) ListByIncident(ctx context.Context, id uuid.UUID, limit int, cursor string) ([]store.IncidentEvidenceRow, string, error) {
	s.evidenceCalls++
	if s.evidenceCalls == s.failEvidenceCall {
		return nil, "", fmt.Errorf("pg: connection reset on page %d", s.evidenceCalls)
	}
	return s.fakeStore.ListByIncident(ctx, id, limit, cursor)
}

func (s *pageFailStore) ListNotes(ctx context.Context, id uuid.UUID, limit int, cursor string) ([]store.IncidentNoteRow, string, error) {
	s.notesCalls++
	if s.notesCalls == s.failNotesCall {
		return nil, "", fmt.Errorf("pg: connection reset on notes page %d", s.notesCalls)
	}
	return s.fakeStore.ListNotes(ctx, id, limit, cursor)
}

func TestExportFailingOnALaterPageIsACleanFailure(t *testing.T) {
	for name, cfg := range map[string]pageFailStore{
		"evidence page 2": {failEvidenceCall: 2},
		"notes page 2":    {failNotesCall: 2},
	} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t)
			id := hs.seed(t, alice)
			for i := range store.IncidentMaxPageSize + 5 {
				hs.addRows(hs.row(id, EvidenceKindObjectSummary, "", "pods", "Pod", "a", fmt.Sprintf("p%03d", i)))
			}
			for i := range store.IncidentMaxPageSize + 5 {
				if _, err := hs.st.CreateNote(context.Background(), id, alice.ID, fmt.Sprintf("note %d", i)); err != nil {
					t.Fatal(err)
				}
			}
			// Each export resets the call counters so page 2 fails every time.
			for _, format := range []string{"json", "markdown"} {
				ps := &pageFailStore{fakeStore: hs.st, failEvidenceCall: cfg.failEvidenceCall, failNotesCall: cfg.failNotesCall}
				hs.h.evidence, hs.h.incidents = ps, ps
				w := hs.export(t, alice, id, format)
				wantStatus(t, w, http.StatusServiceUnavailable)
				if reason, _ := errorOf(t, w); reason != ReasonStoreUnavailable {
					t.Fatalf("%s: reason %q", format, reason)
				}
				if w.Header().Get("Content-Disposition") != "" {
					t.Fatalf("%s: a failed export set download headers", format)
				}
				if strings.Contains(w.Body.String(), "p000") {
					t.Fatalf("%s: partial content leaked into the error response", format)
				}
				if cfg.failEvidenceCall == 2 && ps.evidenceCalls != 2 {
					t.Fatalf("%s: the failure was not on evidence page 2 (calls %d)", format, ps.evidenceCalls)
				}
				if cfg.failNotesCall == 2 && ps.notesCalls != 2 {
					t.Fatalf("%s: the failure was not on notes page 2 (calls %d)", format, ps.notesCalls)
				}
			}
			acts := hs.audit.actions()
			if len(acts) != 2 || acts[0] != "incident_export:failure" || acts[1] != "incident_export:failure" {
				t.Fatalf("audit = %v", acts)
			}
		})
	}
}
