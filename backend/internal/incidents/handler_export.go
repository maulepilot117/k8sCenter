package incidents

// Export (Release D, U23b; Q1 P10, P12, P16). Export is the exfiltration
// boundary, so it is a server-rendered projection of EXACTLY what the same
// caller sees on the detail read: every evidence row goes through the same
// scopeMemo decision FilterEvidence uses, withheld rows appear as counted
// placeholders with their reason, and notes are included. Credentials
// cannot appear because none are stored (P11.1).
//
// Formats are json and markdown only; there is no HTML output path. In
// markdown, EVERY string of untrusted origin (title, summary, user ids,
// evidence items, note bodies) is emitted inside a fenced code block whose
// fence is longer than any backtick run in the content, so no stored text
// can close the fence and nothing is ever rendered as markup. Lines
// outside fences carry only server-generated values (UUIDs, timestamps,
// enum strings, counts).
//
// Bound: the export is read page by page (ListByIncident; never
// ListAllByIncident, since jsonb readback can reach 64x the stored payload
// bytes) and stops once the accumulated content reaches exportMaxBytes.
// The export then says so: JSON carries `truncated: true` with the counts
// of what was left out, markdown ends with an explicit marker. A truncated
// export is honest about being partial rather than refused.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// ExportFormatJSON and ExportFormatMarkdown are the only formats.
	ExportFormatJSON     = "json"
	ExportFormatMarkdown = "markdown"
	// exportSchema names the document shape for consumers.
	exportSchema = "k8scenter.incident.export/v1"
	// exportMaxBytes caps the content an export carries, counted as stored
	// payload and text bytes plus a fixed overhead per item, so the
	// rendered document stays within a small constant factor of it (JSON
	// payloads are embedded verbatim; note bodies and detail strings are
	// JSON-escaped, at most 6x for pathological text, and are bounded by
	// the store).
	exportMaxBytes = 16 << 20
	// exportItemOverhead and exportNoteOverhead approximate the envelope
	// around one item's payload / one note's body.
	exportItemOverhead = 1024
	exportNoteOverhead = 256
	// exportWithheldOverhead is a placeholder's size.
	exportWithheldOverhead = 160
)

// ExportTruncation says what a truncated export left out. Evidence counts
// are exact (the whole-incident counts are known); notes are only known to
// have been cut.
type ExportTruncation struct {
	EvidenceOmitted int  `json:"evidenceOmitted"`
	WithheldOmitted int  `json:"withheldOmitted"`
	NotesOmitted    bool `json:"notesOmitted"`
}

// ExportDocument is the JSON export. It is written as a plain document (no
// api.Response envelope): it is a file, not an API reply.
type ExportDocument struct {
	Schema     string         `json:"schema"`
	ExportedAt time.Time      `json:"exportedAt"`
	ExportedBy string         `json:"exportedBy"`
	Incident   IncidentView   `json:"incident"`
	Counts     EvidenceCounts `json:"counts"`
	// WithheldByReason tallies the placeholders in Withheld by reason.
	WithheldByReason map[string]int     `json:"withheldByReason"`
	Evidence         []Evidence         `json:"evidence"`
	Withheld         []WithheldEvidence `json:"withheld"`
	Notes            []NoteView         `json:"notes"`
	Truncated        bool               `json:"truncated"`
	Truncation       *ExportTruncation  `json:"truncation,omitempty"`
}

// exportBudget tracks the content bytes an export has accumulated.
type exportBudget struct {
	used int
	max  int
}

// take reserves n bytes; false when they do not fit (nothing is reserved).
func (b *exportBudget) take(n int) bool {
	if b.used+n > b.max {
		return false
	}
	b.used += n
	return true
}

// buildExport reads the incident's evidence (every page, through one memo)
// and notes under the byte budget and returns the document. An error is a
// store or decode fault.
func (h *Handler) buildExport(r *http.Request, c *caller, u *auth.User) (*ExportDocument, error) {
	ctx := r.Context()
	id := c.row.ID
	memo, cancel := h.newScopeMemo(ctx, u)
	defer cancel()
	counts, err := h.countEvidence(ctx, memo, id)
	if err != nil {
		return nil, fmt.Errorf("count incident evidence: %w", err)
	}
	doc := &ExportDocument{
		Schema: exportSchema, ExportedAt: time.Now().UTC(), ExportedBy: u.ID,
		Incident: incidentView(c.row, c.role, c.canAnnotate), Counts: counts,
		WithheldByReason: map[string]int{}, Evidence: []Evidence{}, Withheld: []WithheldEvidence{}, Notes: []NoteView{},
	}
	budget := exportBudget{max: h.exportMax}
	truncated := false

	cursor := ""
pages:
	for {
		rows, next, err := h.evidence.ListByIncident(ctx, id, store.IncidentMaxPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("list incident evidence: %w", err)
		}
		visible, withheld, err := memo.filter(rows)
		if err != nil {
			return nil, fmt.Errorf("decode incident evidence: %w", err)
		}
		// Keep the page's input order: walk rows, taking from whichever
		// partition the row landed in.
		vi, wi := 0, 0
		for _, row := range rows {
			rid := row.ID.String()
			switch {
			case vi < len(visible) && visible[vi].ID == rid:
				e := visible[vi]
				vi++
				if !budget.take(len(e.Payload) + len(e.CompletenessDetail) + exportItemOverhead) {
					truncated = true
					break pages
				}
				doc.Evidence = append(doc.Evidence, e)
			case wi < len(withheld) && withheld[wi].ID == rid:
				p := withheld[wi]
				wi++
				if !budget.take(exportWithheldOverhead) {
					truncated = true
					break pages
				}
				doc.Withheld = append(doc.Withheld, p)
				doc.WithheldByReason[p.WithheldReason]++
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	notesCut := false
	if !truncated {
		cursor = ""
	notes:
		for {
			notes, next, err := h.incidents.ListNotes(ctx, id, store.IncidentMaxPageSize, cursor)
			if err != nil {
				return nil, fmt.Errorf("list incident notes: %w", err)
			}
			for _, n := range notes {
				if !budget.take(len(n.Body) + exportNoteOverhead) {
					notesCut = true
					break notes
				}
				doc.Notes = append(doc.Notes, noteView(n))
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	if truncated || notesCut {
		doc.Truncated = true
		doc.Truncation = &ExportTruncation{
			EvidenceOmitted: counts.Visible - len(doc.Evidence),
			WithheldOmitted: counts.Withheld - len(doc.Withheld),
			NotesOmitted:    truncated || notesCut,
		}
	}
	return doc, nil
}

// HandleExport renders the incident as a downloadable json or markdown
// document for the owner or a collaborator (visibility, then the same
// per-item filter as every read). ?format defaults to json; anything other
// than json or markdown, html included, is 400 export_format_invalid.
// GET /api/v1/incidents/{incidentID}/export?format=json|markdown
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = ExportFormatJSON
	}
	if format != ExportFormatJSON && format != ExportFormatMarkdown {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "unsupported export format",
			ReasonExportFormatInvalid, map[string]any{"supported": []string{ExportFormatJSON, ExportFormatMarkdown}})
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	doc, err := h.buildExport(r, c, user)
	if err != nil {
		h.auditLog(r, user, ActionIncidentExport, audit.ResultFailure, c.row.ClusterID, "incident", "incident "+c.row.ID.String())
		h.logger.Error("incident export failed", "incidentId", c.row.ID, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "incident export unavailable", ReasonStoreUnavailable, nil)
		return
	}

	var (
		body        []byte
		contentType string
		ext         string
	)
	if format == ExportFormatJSON {
		body, err = json.MarshalIndent(doc, "", "  ")
		if err != nil {
			h.logger.Error("incident export could not be encoded", "incidentId", c.row.ID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "incident export unavailable", "")
			return
		}
		contentType, ext = "application/json", "json"
	} else {
		body = renderMarkdown(doc)
		contentType, ext = "text/markdown; charset=utf-8", "md"
	}
	h.auditLog(r, user, ActionIncidentExport, audit.ResultSuccess, c.row.ClusterID, "incident",
		fmt.Sprintf("incident %s: format %s, visible %d, withheld %d, included %d evidence, %d withheld, %d notes, truncated %t",
			c.row.ID, format, doc.Counts.Visible, doc.Counts.Withheld, len(doc.Evidence), len(doc.Withheld), len(doc.Notes), doc.Truncated))

	filename := fmt.Sprintf("incident-%s-%s.%s", c.row.ID, doc.ExportedAt.Format("20060102"), ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// Markdown
// ---------------------------------------------------------------------------

// longestBacktickRun returns the length of the longest run of consecutive
// backticks in s.
func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return longest
}

// fenceFor returns a backtick fence strictly longer than any backtick run
// in s (and at least three), so no line of s can close it (CommonMark: a
// closing fence must be at least as long as the opening one).
func fenceFor(s string) string {
	n := longestBacktickRun(s) + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// writeFenced emits untrusted text as a fenced code block. The info string
// is a fixed word; the content is written verbatim. A trailing newline is
// added when missing so the closing fence starts its own line.
func writeFenced(b *bytes.Buffer, info, s string) {
	fence := fenceFor(s)
	b.WriteString(fence)
	b.WriteString(info)
	b.WriteByte('\n')
	b.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(fence)
	b.WriteString("\n\n")
}

// renderMarkdown renders the document. Only server-generated values appear
// outside fences: UUIDs, RFC 3339 timestamps, enum strings and integers.
// Everything of untrusted origin (title, summary, ids of people, evidence
// items, note bodies) is fenced.
func renderMarkdown(doc *ExportDocument) []byte {
	var b bytes.Buffer
	inc := doc.Incident
	fmt.Fprintf(&b, "# Incident %s\n\n", inc.ID)
	fmt.Fprintf(&b, "- Schema: %s\n", exportSchema)
	fmt.Fprintf(&b, "- Exported at: %s\n", doc.ExportedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Exporter role: %s\n", inc.Role)
	fmt.Fprintf(&b, "- Status: %s\n", inc.Status)
	fmt.Fprintf(&b, "- Window start: %s\n", inc.WindowStart.Format(time.RFC3339))
	if inc.WindowEnd != nil {
		fmt.Fprintf(&b, "- Window end: %s\n", inc.WindowEnd.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- Created: %s\n", inc.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Updated: %s\n", inc.UpdatedAt.Format(time.RFC3339))
	if inc.ClosedAt != nil {
		fmt.Fprintf(&b, "- Closed: %s\n", inc.ClosedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- Retention days: %d\n\n", inc.RetentionDays)

	b.WriteString("## Owner\n\n")
	writeFenced(&b, "text", inc.OwnerID)
	b.WriteString("## Exported by\n\n")
	writeFenced(&b, "text", doc.ExportedBy)
	b.WriteString("## Cluster\n\n")
	writeFenced(&b, "text", inc.ClusterID)
	b.WriteString("## Title\n\n")
	writeFenced(&b, "text", inc.Title)
	b.WriteString("## Summary\n\n")
	writeFenced(&b, "text", inc.Summary)

	fmt.Fprintf(&b, "## Evidence\n\n%d readable by you, %d withheld from you; %d readable and %d withheld included below.\n\n",
		doc.Counts.Visible, doc.Counts.Withheld, len(doc.Evidence), len(doc.Withheld))
	for i, e := range doc.Evidence {
		fmt.Fprintf(&b, "### %d. %s (%s), collected %s\n\n", i+1, e.EvidenceKind, e.Mode, e.CollectedAt.Format(time.RFC3339))
		item, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			item = []byte(`{"error":"item could not be encoded"}`)
		}
		writeFenced(&b, "json", string(item))
	}
	if len(doc.Withheld) > 0 {
		b.WriteString("### Withheld\n\n")
		for _, p := range doc.Withheld {
			fmt.Fprintf(&b, "- %s: %s collected %s, withheld (%s)\n", p.ID, p.EvidenceKind, p.CollectedAt.Format(time.RFC3339), p.WithheldReason)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Notes\n\n%d included.\n\n", len(doc.Notes))
	for _, n := range doc.Notes {
		fmt.Fprintf(&b, "### Note %s (revision %d, created %s, updated %s)\n\n", n.ID, n.Revision,
			n.CreatedAt.Format(time.RFC3339), n.UpdatedAt.Format(time.RFC3339))
		b.WriteString("Author:\n\n")
		writeFenced(&b, "text", n.AuthorID)
		writeFenced(&b, "text", n.Body)
	}

	if doc.Truncated {
		t := doc.Truncation
		fmt.Fprintf(&b, "## TRUNCATED\n\nThis export reached its size bound and is incomplete: %d readable evidence items and %d withheld placeholders were omitted; notes omitted: %t.\n",
			t.EvidenceOmitted, t.WithheldOmitted, t.NotesOmitted)
	}
	return b.Bytes()
}
