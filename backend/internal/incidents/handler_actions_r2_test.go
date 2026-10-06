package incidents

// Review round 2 of PR #583: per-element JSON streaming (round trip and an
// allocation guard), the per-user export cap, commit-outcome ambiguity,
// audited refusals, and the remaining testing gaps.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// buildDoc runs the export builder for user directly, so a test can compare
// the streamed bytes with the in-memory document.
func (hs *harness) buildDoc(t *testing.T, user *auth.User, id uuid.UUID) *ExportDocument {
	t.Helper()
	c, err := hs.h.visibility(context.Background(), id, user)
	if err != nil || c == nil {
		t.Fatalf("visibility: %v / %v", c, err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/incidents", nil)
	doc, err := hs.h.buildExport(r, c, user)
	if err != nil {
		t.Fatalf("buildExport: %v", err)
	}
	return doc
}

func TestExportJSONIsWrittenPerElementAndRoundTrips(t *testing.T) {
	hs, id := mixedFixture(t)
	// All three evidence kinds are visible in namespace a; a budget that
	// fits the evidence but not the note yields a truncation block too.
	fits := 0
	for _, r := range hs.st.evidence[id] {
		if r.Namespace == "a" {
			fits += len(r.Payload) + exportItemOverhead
		} else {
			fits += exportWithheldOverhead
		}
	}
	hs.h.exportMax = fits + 10
	doc := hs.buildDoc(t, alice, id)
	if !doc.Truncated || doc.Truncation == nil || len(doc.Evidence) != 3 || len(doc.Withheld) != 2 {
		t.Fatalf("fixture: truncated %t, %d evidence, %d withheld", doc.Truncated, len(doc.Evidence), len(doc.Withheld))
	}
	kinds := map[string]bool{}
	for _, e := range doc.Evidence {
		kinds[e.EvidenceKind] = true
	}
	if len(kinds) != 3 {
		t.Fatalf("fixture lacks all three kinds: %v", kinds)
	}

	var buf bytes.Buffer
	bw := bufio.NewWriterSize(&buf, exportWriteBuffer)
	if err := writeExportJSON(bw, doc); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	// Byte-equal to the old representation once whitespace is removed: same
	// field names, order, escaping and element encoding.
	want, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, buf.Bytes()); err != nil {
		t.Fatalf("streamed JSON is invalid: %v\n%s", err, buf.String())
	}
	if !bytes.Equal(compact.Bytes(), want) {
		t.Fatalf("streamed document differs from json.Marshal(doc):\n got %s\nwant %s", compact.Bytes(), want)
	}
	// And it decodes back into the same struct.
	var back ExportDocument
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(back)
	if !bytes.Equal(again, want) {
		t.Fatalf("round trip through ExportDocument lost something:\n got %s\nwant %s", again, want)
	}
	// The handler emits the same bytes as the writer for the same document
	// (modulo exportedAt, which is the clock).
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusOK)
	var viaHandler ExportDocument
	if err := json.Unmarshal(w.Body.Bytes(), &viaHandler); err != nil {
		t.Fatal(err)
	}
	viaHandler.ExportedAt = doc.ExportedAt
	h1, _ := json.Marshal(viaHandler)
	if !bytes.Equal(h1, want) {
		t.Fatalf("handler output differs from the writer's document")
	}
	if !strings.HasPrefix(w.Body.String(), "{\n  \"schema\": \"k8scenter.incident.export/v1\",\n") || !strings.HasSuffix(w.Body.String(), "}\n") {
		t.Fatalf("envelope framing:\n%.80s", w.Body.String())
	}
}

// largeDoc builds a document whose content is n items of payloadBytes each.
func largeDoc(n, payloadBytes int) (*ExportDocument, int) {
	doc := &ExportDocument{Schema: exportSchema, ExportedAt: fixedNow, ExportedBy: alice.ID,
		WithheldByReason: map[string]int{}, Withheld: []WithheldEvidence{}, Notes: []NoteView{}}
	filler := strings.Repeat("x", payloadBytes-40)
	content := 0
	for i := range n {
		payload := json.RawMessage(`{"kind":"Pod","metadata":{"name":"p"},"d":"` + filler + `"}`)
		doc.Evidence = append(doc.Evidence, Evidence{
			ID: uuid.New().String(), EvidenceKind: EvidenceKindObjectSummary, Mode: ModeSnapshot,
			Source:      SourceRef{ClusterID: "local", Resource: "pods", Kind: "Pod", Namespace: "a", Name: fmt.Sprintf("p%d", i)},
			CollectedAt: fixedNow, Completeness: CompletenessComplete, Payload: payload, PayloadBytes: len(payload),
		})
		content += len(payload)
	}
	return doc, content
}

// allocatedBy measures the bytes allocated by fn with the collector held
// off, so pooled encoder buffers are not reclaimed mid-run and the number
// is reproducible.
func allocatedBy(fn func()) uint64 {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestExportJSONWriterAllocationIsBoundedByOneElement(t *testing.T) {
	doc, content := largeDoc(300, 16<<10)
	doc.Truncated = true
	doc.Truncation = &ExportTruncation{}
	// Warm the encoder's pooled buffers once, as any earlier request would.
	bw := bufio.NewWriterSize(io.Discard, exportWriteBuffer)
	_ = writeExportJSON(bw, doc)
	_ = bw.Flush()

	allocated := allocatedBy(func() {
		bw := bufio.NewWriterSize(io.Discard, exportWriteBuffer)
		if err := writeExportJSON(bw, doc); err != nil {
			t.Error(err)
		}
		_ = bw.Flush()
	})
	// Per-element serialization reuses one scratch buffer, so it allocates
	// next to nothing per element (more under -race, where sync.Pool drops
	// the encoder's pooled state at random); a whole-document encode grows
	// a buffer by doubling to the full content and then indents it, about
	// 8x the content. 1.5x separates the two with margin in both modes.
	ratio := float64(allocated) / float64(content)
	t.Logf("allocated %d bytes for %d bytes of content (%.2fx)", allocated, content, ratio)
	if ratio > 1.5 {
		t.Fatalf("writing %d bytes of content allocated %d bytes (%.2fx); the writer is buffering more than one element", content, allocated, ratio)
	}
}

func TestPerUserExportCapIs503AndDoesNotTouchGlobalSlots(t *testing.T) {
	hs, id := mixedFixture(t)
	hs.grant(id, bob, false)
	if exportPerUser != 1 {
		t.Fatalf("exportPerUser = %d, want 1", exportPerUser)
	}
	release, ok := hs.h.acquireUserExport(alice.ID)
	if !ok {
		t.Fatal("first per-user acquire refused")
	}
	// alice's second concurrent export is refused without touching the
	// global bulkhead; bob still proceeds.
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonIncidentBusy || w.Header().Get("Retry-After") == "" {
		t.Fatalf("reason %q, Retry-After %q", reason, w.Header().Get("Retry-After"))
	}
	if len(hs.h.exportSlots) != 0 {
		t.Fatal("a per-user refusal consumed a global slot")
	}
	if acts := hs.audit.actions(); len(acts) != 1 || acts[0] != "incident_export:failure" || !strings.Contains(hs.audit.entries[0].Detail, "already in progress") {
		t.Fatalf("audit = %v", acts)
	}
	wantStatus(t, hs.export(t, bob, id, "json"), http.StatusOK)
	release()
	wantStatus(t, hs.export(t, alice, id, "markdown"), http.StatusOK)
	hs.h.exportMu.Lock()
	n := len(hs.h.exportByUser)
	hs.h.exportMu.Unlock()
	if n != 0 {
		t.Fatalf("exportByUser holds %d entries after every export finished", n)
	}
}

func TestCaptureCommitOutcomeUnknownIsNeverClaimedAsNothingRecorded(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	hs.collect(t, completeSource("object", completeItem("web")))
	hs.st.failOp["InsertBatch"] = fmt.Errorf("%w: commit incident evidence tx: %w", store.ErrCommitOutcomeUnknown, context.DeadlineExceeded)
	w := hs.capture(t, alice, id, defaultCapture)
	wantStatus(t, w, http.StatusServiceUnavailable)
	reason, _ := errorOf(t, w)
	if reason != ReasonCaptureOutcomeUnknown {
		t.Fatalf("reason = %q", reason)
	}
	body := w.Body.String()
	if strings.Contains(body, "nothing was recorded") || !strings.Contains(body, "may or may not have been recorded") {
		t.Fatalf("body = %s", body)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on an unknown outcome")
	}
	acts := hs.audit.actions()
	if len(acts) != 1 || acts[0] != "incident_capture:failure" || !strings.Contains(hs.audit.entries[0].Detail, "outcome unknown") {
		t.Fatalf("audit = %v / %q", acts, hs.audit.entries[0].Detail)
	}
}

func TestCaptureRefusalsAreAudited(t *testing.T) {
	t.Run("closed incident", func(t *testing.T) {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		closed := store.IncidentStatusClosed
		if err := hs.st.Update(context.Background(), id, alice.ID, nil, nil, &closed); err != nil {
			t.Fatal(err)
		}
		hs.collect(t, completeSource("object", completeItem("web")))
		wantStatus(t, hs.capture(t, alice, id, defaultCapture), http.StatusConflict)
		acts := hs.audit.actions()
		if len(acts) != 1 || acts[0] != "incident_capture:failure" || !strings.Contains(hs.audit.entries[0].Detail, "closed") {
			t.Fatalf("audit = %v", acts)
		}
		if d := hs.audit.entries[0].Detail; strings.Contains(d, testNS) || strings.Contains(d, testName) {
			t.Fatalf("audit detail names the target: %q", d)
		}
	})
	t.Run("capture bulkhead full", func(t *testing.T) {
		hs := newHarness(t)
		id := hs.seed(t, alice)
		hs.collect(t, completeSource("object", completeItem("web")))
		drain := fill(hs.h.captureSlots)
		defer drain()
		wantStatus(t, hs.capture(t, alice, id, defaultCapture), http.StatusServiceUnavailable)
		acts := hs.audit.actions()
		if len(acts) != 1 || acts[0] != "incident_capture:failure" || !strings.Contains(hs.audit.entries[0].Detail, "bulkhead full") {
			t.Fatalf("audit = %v", acts)
		}
	})
}

func TestRemoveGrantRoundTripsLiteralPercentWithEncodedSlash(t *testing.T) {
	hs := newHarness(t)
	id := hs.seed(t, alice)
	mux := hs.grantRouter(alice)
	grantee := "a%41b/c"
	escaped := url.PathEscape(grantee) // a%2541b%2Fc: RawPath is kept for the %2F
	if !strings.Contains(escaped, "%2F") || !strings.Contains(escaped, "%25") {
		t.Fatalf("fixture escape = %q", escaped)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/incidents/"+id.String()+"/grants",
		strings.NewReader(fmt.Sprintf(`{"granteeId":%q,"canAnnotate":false}`, grantee))))
	wantStatus(t, w, http.StatusCreated)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/incidents/"+id.String()+"/grants/"+escaped, nil))
	wantStatus(t, w, http.StatusNoContent)
	if _, still := hs.st.grants[id][grantee]; still {
		t.Fatal("grant with a literal percent and an encoded slash was not revoked")
	}
}

func TestExportNotesProbeFailureAfterTruncationIsACleanFailure(t *testing.T) {
	hs, id := mixedFixture(t)
	hs.h.exportMax = exportWithheldOverhead - 1 // evidence truncated before the first row
	ps := &pageFailStore{fakeStore: hs.st, failNotesCall: 1}
	hs.h.evidence, hs.h.incidents = ps, ps
	w := hs.export(t, alice, id, "json")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if reason, _ := errorOf(t, w); reason != ReasonStoreUnavailable {
		t.Fatalf("reason = %q", reason)
	}
	if w.Header().Get("Content-Disposition") != "" || ps.notesCalls != 1 {
		t.Fatalf("headers %q, notes calls %d", w.Header().Get("Content-Disposition"), ps.notesCalls)
	}
	if acts := hs.audit.actions(); len(acts) != 1 || acts[0] != "incident_export:failure" {
		t.Fatalf("audit = %v", acts)
	}
}

// failingWriter delivers headers, then fails every body write: a client
// that disconnected mid-download.
type failingWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (f *failingWriter) Write([]byte) (int, error) {
	f.writes++
	return 0, errors.New("write tcp: broken pipe")
}

func TestExportStreamFailureAfterHeadersIsLoggedAtWarn(t *testing.T) {
	for _, format := range []string{"json", "markdown"} {
		hs, id := mixedFixture(t)
		var logs bytes.Buffer
		hs.h = newHandlerWith(hs.st, hs.st, hs.st, hs.access, hs.audit, slog.New(slog.NewTextHandler(&logs, nil)))
		fw := &failingWriter{ResponseRecorder: httptest.NewRecorder()}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/incidents?format="+format, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("incidentID", id.String())
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = middleware.WithClusterID(auth.ContextWithUser(ctx, alice), "local")
		hs.h.HandleExport(fw, r.WithContext(ctx))
		if fw.Code != http.StatusOK || fw.writes == 0 {
			t.Fatalf("%s: status %d, writes %d", format, fw.Code, fw.writes)
		}
		if fw.Header().Get("Content-Disposition") == "" {
			t.Fatalf("%s: headers were not set before the first byte", format)
		}
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "stream interrupted") {
			t.Fatalf("%s: no WARN for the interrupted stream:\n%s", format, logs.String())
		}
		if strings.Contains(logs.String(), "level=ERROR") {
			t.Fatalf("%s: a client disconnect was logged at ERROR:\n%s", format, logs.String())
		}
		if acts := hs.audit.actions(); len(acts) != 1 || acts[0] != "incident_export:success" {
			t.Fatalf("%s: audit = %v", format, acts)
		}
	}
}

// streamedCompact runs writeExportJSON and compacts its output.
func streamedCompact(t *testing.T, doc *ExportDocument) []byte {
	t.Helper()
	var buf bytes.Buffer
	bw := bufio.NewWriterSize(&buf, exportWriteBuffer)
	if err := writeExportJSON(bw, doc); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, buf.Bytes()); err != nil {
		t.Fatalf("streamed JSON is invalid: %v\n%s", err, buf.String())
	}
	return compact.Bytes()
}

// TestExportJSONEnvelopeParity (PR #583 round 3): the hand-written
// envelope is byte-equal, once compacted, to json.Marshal of the same
// document for every envelope shape: populated and not truncated, all
// three arrays empty, and truncated (the only shape with "truncation").
func TestExportJSONEnvelopeParity(t *testing.T) {
	hs, id := mixedFixture(t)
	full := hs.buildDoc(t, alice, id)
	if full.Truncated || full.Truncation != nil || len(full.Evidence) == 0 || len(full.Withheld) == 0 || len(full.Notes) == 0 {
		t.Fatalf("fixture: want a populated, untruncated document; got truncated %t, %d/%d/%d",
			full.Truncated, len(full.Evidence), len(full.Withheld), len(full.Notes))
	}
	hs.h.exportMax = exportWithheldOverhead - 1 // nothing fits
	truncated := hs.buildDoc(t, alice, id)
	if !truncated.Truncated || truncated.Truncation == nil {
		t.Fatal("fixture: want a truncated document")
	}
	empty := &ExportDocument{Schema: exportSchema, ExportedAt: fixedNow, ExportedBy: alice.ID,
		WithheldByReason: map[string]int{}, Evidence: []Evidence{}, Withheld: []WithheldEvidence{}, Notes: []NoteView{}}

	// Every field non-zero, truncation included, so no omitempty field can
	// hide from the comparison.
	everything := *full
	everything.Truncated = true
	everything.Truncation = &ExportTruncation{EvidenceOmitted: 2, WithheldOmitted: 1, NotesOmitted: true}
	v := reflect.ValueOf(everything)
	for i := range v.NumField() {
		if f := v.Field(i); f.IsZero() || (f.Kind() == reflect.Slice || f.Kind() == reflect.Map) && f.Len() == 0 {
			t.Fatalf("fixture: ExportDocument.%s is empty in the every-field document", v.Type().Field(i).Name)
		}
	}

	for _, tc := range []struct {
		name string
		doc  *ExportDocument
	}{{"populated, not truncated", full}, {"all arrays empty", empty}, {"truncated", truncated}, {"every field non-zero", &everything}} {
		t.Run(tc.name, func(t *testing.T) {
			want, err := json.Marshal(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			if got := streamedCompact(t, tc.doc); !bytes.Equal(got, want) {
				t.Fatalf("streamed envelope differs from json.Marshal:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestExportEnvelopeCoversEveryDocumentField fails when ExportDocument
// gains (or reorders) a JSON field that writeExportJSON's hand-written
// envelope (exportEnvelopeFields) does not emit in the same order.
func TestExportEnvelopeCoversEveryDocumentField(t *testing.T) {
	var tags []string
	typ := reflect.TypeFor[ExportDocument]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		tags = append(tags, name)
	}
	if !reflect.DeepEqual(tags, exportEnvelopeFields[:]) {
		t.Fatalf("ExportDocument JSON fields %v; the streamed envelope writes %v", tags, exportEnvelopeFields)
	}
}
