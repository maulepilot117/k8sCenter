package incidents

// HTTP handlers for incident records and their notes, and the read-time
// authorization filter for evidence (Release D, U23a). This file is the Q1
// authorization boundary for everything an incident discloses:
//
//   - visibility (P1): an incident is visible to its owner or an explicit
//     grantee and to nobody else, admins included. Anyone else gets the same
//     404 a missing id gets, so the id space does not leak. A caller who can
//     see the incident but attempts an owner-only action gets 403: they
//     already know it exists.
//   - FilterEvidence (P4, P5, P6, P7, P8, P10, P11, R-l): the ONLY filter
//     for evidence. Every representation of evidence (U23b's export too)
//     passes its rows through it. It re-checks the caller's CURRENT
//     authorization per distinct scope, on the cluster STORED on the row and
//     never the request header, and withholds a row as `forbidden` (denied)
//     or `authorization_check_unavailable` (the check itself failed; never
//     folded into forbidden, unlike Release E receipts).
//
// Routes (registered in server/routes.go, always, so a deployment without
// PostgreSQL answers 503 incident_persistence_unavailable rather than chi's
// bare 404):
//
//	GET    /api/v1/incidents                               HandleList
//	POST   /api/v1/incidents                               HandleCreate
//	GET    /api/v1/incidents/{incidentID}                  HandleGet
//	PUT    /api/v1/incidents/{incidentID}                  HandleUpdate
//	DELETE /api/v1/incidents/{incidentID}                  HandleDelete
//	GET    /api/v1/incidents/{incidentID}/notes            HandleListNotes
//	POST   /api/v1/incidents/{incidentID}/notes            HandleCreateNote
//	PUT    /api/v1/incidents/{incidentID}/notes/{noteID}   HandleUpdateNote
//	DELETE /api/v1/incidents/{incidentID}/notes/{noteID}   HandleDeleteNote
//
// Capture, grants and export are U23b.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// Audit actions (P16). Declared here rather than in audit/logger.go to stay
// within the unit's file budget (plan R-7 / A-11); a housekeeping PR promotes
// them. audit.Action is an open string type, so these are first-class values.
const (
	ActionIncidentCreate     audit.Action = "incident_create"
	ActionIncidentUpdate     audit.Action = "incident_update"
	ActionIncidentDelete     audit.Action = "incident_delete"
	ActionIncidentNoteCreate audit.Action = "incident_note_create"
	ActionIncidentNoteUpdate audit.Action = "incident_note_update"
	ActionIncidentNoteDelete audit.Action = "incident_note_delete"
)

// Machine-readable error reasons.
const (
	// ReasonPersistenceUnavailable: the deployment has no PostgreSQL, so
	// incidents cannot exist (503, extra.requires = "postgresql").
	ReasonPersistenceUnavailable = "incident_persistence_unavailable"
	// ReasonStoreUnavailable: the database is configured but a call failed.
	ReasonStoreUnavailable = "incident_store_unavailable"
	// ReasonIncidentBusy: a lock wait on the incident timed out; the client
	// retries after the Retry-After header.
	ReasonIncidentBusy = "incident_busy"
	// ReasonNoteRevisionConflict: the note was edited since the caller read
	// it; extra.currentRevision carries the revision to retry with.
	ReasonNoteRevisionConflict = "note_revision_conflict"
)

const (
	// DefaultRetentionDays is the retention stamped on new incidents until
	// U25a makes it configurable (Q1 P13). SetRetentionDays is that seam.
	DefaultRetentionDays = 30
	// maxBodyBytes bounds every request body: the largest field is a note
	// body of 20000 characters.
	maxBodyBytes = 256 << 10
)

// Role is how the caller qualified to see an incident (P1).
type Role string

const (
	RoleOwner        Role = "owner"
	RoleCollaborator Role = "collaborator"
)

// The store slices the handlers use. Unexported, like changes.receiptReader,
// so tests run against in-memory fakes; NewHandler takes the concrete stores.
type incidentStore interface {
	Create(ctx context.Context, r store.IncidentRow) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*store.IncidentRow, error)
	ListVisible(ctx context.Context, userID string, limit int, cursor string) ([]store.IncidentRow, string, error)
	Update(ctx context.Context, id uuid.UUID, ownerID, title, summary, status string) error
	Delete(ctx context.Context, id uuid.UUID, ownerID string) error
	CreateNote(ctx context.Context, incidentID uuid.UUID, authorID, body string) (store.IncidentNoteRow, error)
	ListNotes(ctx context.Context, incidentID uuid.UUID) ([]store.IncidentNoteRow, error)
	UpdateNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID, body string, expectedRevision int) (store.IncidentNoteRow, error)
	DeleteNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID string) error
}

type evidenceStore interface {
	ListByIncident(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]store.IncidentEvidenceRow, string, error)
}

type grantStore interface {
	GetGrant(ctx context.Context, incidentID uuid.UUID, userID string) (*store.IncidentGrantRow, error)
}

// accessChecker is the one *resources.AccessChecker method FilterEvidence
// calls (never CanAccess: correction C7). Unexported so a test can observe
// which CLUSTER a check was sent to.
type accessChecker interface {
	CanAccessGroupResource(ctx context.Context, clusterID, username string, groups []string,
		verb, apiGroup, resource, namespace string) (bool, error)
}

// Handler serves incident records, notes and filtered evidence.
type Handler struct {
	incidents incidentStore // nil when no database
	evidence  evidenceStore // nil when no database
	grants    grantStore    // nil when no database
	collector *Collector    // used by U23b's capture endpoint
	access    accessChecker
	audit     audit.Logger
	limits    Limits
	// retentionDays is stamped on each new incident (retention_days_at_capture).
	retentionDays int
	logger        *slog.Logger
}

// NewHandler builds the handler. The stores may be nil (no database): every
// endpoint then answers 503 ReasonPersistenceUnavailable. A nil access
// checker withholds every evidence item as authorization_check_unavailable.
// A nil audit logger audits nothing.
func NewHandler(incidents *store.IncidentStore, evidence *store.IncidentEvidenceStore, grants *store.IncidentGrantStore,
	collector *Collector, access *resources.AccessChecker, auditLogger audit.Logger, limits Limits, logger *slog.Logger) *Handler {
	h := &Handler{collector: collector, audit: auditLogger, limits: limits, retentionDays: DefaultRetentionDays, logger: logger}
	// Assign only non-nil pointers so a nil *store.X never becomes a
	// non-nil interface that panics on first use instead of answering 503.
	if incidents != nil {
		h.incidents = incidents
	}
	if evidence != nil {
		h.evidence = evidence
	}
	if grants != nil {
		h.grants = grants
	}
	if access != nil {
		h.access = access
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

// newHandlerWith is the test seam: any stores, any access checker.
func newHandlerWith(incidents incidentStore, evidence evidenceStore, grants grantStore,
	access accessChecker, auditLogger audit.Logger, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{incidents: incidents, evidence: evidence, grants: grants, access: access,
		audit: auditLogger, limits: DefaultLimits(), retentionDays: DefaultRetentionDays, logger: logger}
}

// SetRetentionDays sets the retention stamped on new incidents. It is the
// configuration seam for U25a; days is clamped to the store's range.
func (h *Handler) SetRetentionDays(days int) {
	h.retentionDays = store.ClampIncidentRetentionDays(days)
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// IncidentView is the incident record as the API returns it. Role and
// CanAnnotate are the CALLER's standing on it, so the UI can gate controls.
type IncidentView struct {
	ID            string     `json:"id"`
	OwnerID       string     `json:"ownerId"`
	ClusterID     string     `json:"clusterId"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Status        string     `json:"status"`
	WindowStart   time.Time  `json:"windowStart"`
	WindowEnd     *time.Time `json:"windowEnd,omitempty"`
	EvidenceBytes int64      `json:"evidenceBytes"`
	EvidenceCount int        `json:"evidenceCount"`
	ScopeCount    int        `json:"scopeCount"`
	RetentionDays int        `json:"retentionDays"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ClosedAt      *time.Time `json:"closedAt,omitempty"`
	Role          Role       `json:"role"`
	CanAnnotate   bool       `json:"canAnnotate"`
}

// IncidentDetail is the GET /incidents/{id} response: the record, one page of
// evidence the caller may read, placeholders for the page's withheld items,
// and counts of both. Metadata.Total is the visible count and
// Metadata.Continue the cursor for the next evidence page.
type IncidentDetail struct {
	Incident IncidentView       `json:"incident"`
	Evidence []Evidence         `json:"evidence"`
	Withheld []WithheldEvidence `json:"withheld"`
	Counts   EvidenceCounts     `json:"counts"`
}

// EvidenceCounts are per page: what the caller may read and what was
// withheld (P10). Neither says anything about a withheld item's scope.
type EvidenceCounts struct {
	Visible  int `json:"visible"`
	Withheld int `json:"withheld"`
}

// NoteView is a note as the API returns it.
type NoteView struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incidentId"`
	AuthorID   string    `json:"authorId"`
	Body       string    `json:"body"`
	Revision   int       `json:"revision"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// createIncidentRequest is the POST /incidents body. The owner is the
// authenticated caller and the cluster is the local one (Release D capture is
// local-only); neither is accepted from the body.
type createIncidentRequest struct {
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	WindowStart time.Time  `json:"windowStart"`
	WindowEnd   *time.Time `json:"windowEnd,omitempty"`
}

// updateIncidentRequest is the PUT /incidents/{id} body. An empty status
// keeps the current one.
type updateIncidentRequest struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

// noteRequest is the body of a note create (Body) or update (Body plus the
// Revision the caller last read).
type noteRequest struct {
	Body     string `json:"body"`
	Revision int    `json:"revision"`
}

func incidentView(r *store.IncidentRow, role Role, canAnnotate bool) IncidentView {
	return IncidentView{
		ID: r.ID.String(), OwnerID: r.OwnerID, ClusterID: r.ClusterID, Title: r.Title, Summary: r.Summary,
		Status: r.Status, WindowStart: r.WindowStart, WindowEnd: r.WindowEnd, EvidenceBytes: r.EvidenceBytes,
		EvidenceCount: r.EvidenceCount, ScopeCount: r.ScopeCount, RetentionDays: r.RetentionDaysAtCapture,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ClosedAt: r.ClosedAt, Role: role, CanAnnotate: canAnnotate,
	}
}

func noteView(n store.IncidentNoteRow) NoteView {
	return NoteView{ID: n.ID.String(), IncidentID: n.IncidentID.String(), AuthorID: n.AuthorID, Body: n.Body,
		Revision: n.Revision, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt}
}

// ---------------------------------------------------------------------------
// FilterEvidence (P4, P5, P6, P7, P8, P10, P11, R-l)
// ---------------------------------------------------------------------------

// scopeDecision is one memoized access check.
type scopeDecision struct {
	allowed     bool
	unavailable bool // the check itself failed; never a denial
}

// scopeMemo memoizes `get` decisions per scope for ONE FilterEvidence call
// (R-1: no cross-request cache beyond the AccessChecker's own 60s). After a
// check fails on a cluster, every later uncached scope on that cluster is
// unavailable without another call: an unreachable cluster costs one failed
// dial per request, and the rows are still reported as unchecked, not as
// forbidden.
type scopeMemo struct {
	ctx     context.Context
	access  accessChecker
	user    *auth.User
	logger  *slog.Logger
	cache   map[store.EvidenceScope]scopeDecision
	errored map[string]bool // cluster id → a check failed there
}

func (m *scopeMemo) decide(sc store.EvidenceScope) scopeDecision {
	if d, ok := m.cache[sc]; ok {
		return d
	}
	var d scopeDecision
	switch {
	case m.access == nil, m.errored[sc.ClusterID]:
		d.unavailable = true
	default:
		// The STORED cluster, never the request header (P6).
		allowed, err := m.access.CanAccessGroupResource(m.ctx, sc.ClusterID,
			m.user.KubernetesUsername, m.user.KubernetesGroups, "get", sc.APIGroup, sc.Resource, sc.Namespace)
		if err != nil {
			m.errored[sc.ClusterID] = true
			d.unavailable = true
			m.logger.Warn("incident evidence access check failed; withholding as authorization_check_unavailable",
				"cluster", sc.ClusterID, "user", m.user.ID, "group", sc.APIGroup, "resource", sc.Resource,
				"namespace", sc.Namespace, "error", err)
		} else {
			d.allowed = allowed
		}
	}
	m.cache[sc] = d
	return d
}

// readDecision returns "" when the caller may read the row, otherwise the
// withheld reason. Every required scope is evaluated (memoized, so at most
// one check per distinct scope per request): a definite denial on any scope
// is `forbidden` whatever the others said; otherwise a failed check on any
// scope is `authorization_check_unavailable`.
func (m *scopeMemo) readDecision(row store.IncidentEvidenceRow) string {
	// Stored scope plus, for a diagnostic row stored under a related
	// resource, the target's own resource (R-l). Event rows were gated at
	// capture with `list events`; the read path re-checks `get events` on
	// the same scope (R-m).
	scopes, err := RequiredReadScopes(row)
	if err != nil {
		m.logger.Warn("incident evidence row cannot be scoped; withholding", "evidenceId", row.ID, "error", err)
		return WithheldAuthorizationCheckUnavailable
	}
	if row.SecretDerived {
		// P11.2: get on secrets in the same namespace on the same stored
		// cluster, for every reader, the owner included.
		scopes = append(scopes, store.EvidenceScope{ClusterID: row.ClusterID, APIGroup: "", Resource: "secrets", Namespace: row.Namespace})
	}
	unavailable := false
	for _, sc := range scopes {
		d := m.decide(sc)
		switch {
		case d.unavailable:
			unavailable = true
		case !d.allowed:
			return WithheldForbidden
		}
	}
	if unavailable {
		return WithheldAuthorizationCheckUnavailable
	}
	return ""
}

// FilterEvidence is the ONLY authorization filter for evidence (Q1 P10):
// every representation of evidence calls it. It partitions rows into the
// items u may read now and placeholders for the rest, both in input order.
// The decision is the caller's CURRENT authorization (P4, P7) for each
// row's stored scope on the row's stored cluster (P6); the live object is
// never consulted, so its deletion changes nothing (P8). A placeholder
// carries only id, evidenceKind, collectedAt, withheld and withheldReason.
// An undecodable stored row is an error (a server fault, not a policy
// outcome).
func (h *Handler) FilterEvidence(ctx context.Context, u *auth.User, rows []store.IncidentEvidenceRow) ([]Evidence, []WithheldEvidence, error) {
	if h.access == nil && len(rows) > 0 {
		h.logger.Warn("incidents have no access checker; withholding every evidence item")
	}
	memo := &scopeMemo{ctx: ctx, access: h.access, user: u, logger: h.logger,
		cache: map[store.EvidenceScope]scopeDecision{}, errored: map[string]bool{}}
	visible := make([]Evidence, 0, len(rows))
	withheld := make([]WithheldEvidence, 0)
	for _, row := range rows {
		if reason := memo.readDecision(row); reason != "" {
			withheld = append(withheld, WithheldEvidence{
				ID: row.ID.String(), EvidenceKind: row.EvidenceKind, CollectedAt: row.CollectedAt,
				Withheld: true, WithheldReason: reason,
			})
			continue
		}
		e, err := EvidenceFromRow(row)
		if err != nil {
			return nil, nil, fmt.Errorf("evidence %s: %w", row.ID, err)
		}
		visible = append(visible, e)
	}
	return visible, withheld, nil
}

// ---------------------------------------------------------------------------
// Shared handler steps
// ---------------------------------------------------------------------------

// requireStore is the truthful no-database gate (correction C4). Routes are
// always registered, so a deployment without PostgreSQL answers 503 with a
// machine-readable reason rather than an ambiguous 404.
func (h *Handler) requireStore(w http.ResponseWriter) bool {
	if h.incidents == nil || h.evidence == nil || h.grants == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"incident persistence unavailable", ReasonPersistenceUnavailable,
			map[string]any{"requires": "postgresql"})
		return false
	}
	return true
}

// begin is the opening of every handler: an authenticated caller, then the
// store gate (in that order, so an unauthenticated caller learns nothing
// about the deployment).
func (h *Handler) begin(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return nil, false
	}
	if !h.requireStore(w) {
		return nil, false
	}
	return user, true
}

// parseID validates a path id with uuid.Parse (correction C6:
// resources.ValidateURLParams does not cover these params) and answers 400.
func parseID(w http.ResponseWriter, r *http.Request, param, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid "+what+" id", what+" ids are UUIDs")
		return uuid.Nil, false
	}
	return id, true
}

// caller is the resolved standing of the authenticated user on an incident.
type caller struct {
	row         *store.IncidentRow
	role        Role
	canAnnotate bool
}

// visibility resolves P1 and P3 for u on incident id: (nil, nil) when the
// incident does not exist or u is neither owner nor grantee, which the
// handlers answer identically. A grant's can_annotate decides note writes.
func (h *Handler) visibility(ctx context.Context, id uuid.UUID, u *auth.User) (*caller, error) {
	row, err := h.incidents.Get(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	if u.ID != "" && row.OwnerID == u.ID {
		return &caller{row: row, role: RoleOwner, canAnnotate: true}, nil
	}
	grant, err := h.grants.GetGrant(ctx, id, u.ID)
	if err != nil || grant == nil {
		return nil, err
	}
	return &caller{row: row, role: RoleCollaborator, canAnnotate: grant.CanAnnotate}, nil
}

// loadVisible parses {incidentID} and applies the visibility gate. A
// non-visible incident is answered exactly like a missing one (404, same
// body), so incident ids are not enumerable; a store fault is 503.
func (h *Handler) loadVisible(w http.ResponseWriter, r *http.Request, u *auth.User) (*caller, bool) {
	id, ok := parseID(w, r, "incidentID", "incident")
	if !ok {
		return nil, false
	}
	c, err := h.visibility(r.Context(), id, u)
	if err != nil {
		h.writeStoreFailure(w, "resolve incident visibility", err)
		return nil, false
	}
	if c == nil {
		h.writeNotFound(w)
		return nil, false
	}
	return c, true
}

func (h *Handler) writeNotFound(w http.ResponseWriter) {
	httputil.WriteError(w, http.StatusNotFound, "incident not found", "")
}

// writeStoreFailure maps a store error to a response. Validation and cursor
// errors are the caller's (400, with the store's own message); ownership and
// authorship misses are 403 (only reached after the visibility gate, so the
// caller already knows the incident exists) or 404; a lock timeout is a
// retryable 503; anything else is logged and answered 503 without detail.
func (h *Handler) writeStoreFailure(w http.ResponseWriter, op string, err error) {
	var conflict *store.NoteRevisionConflictError
	switch {
	case errors.As(err, &conflict):
		httputil.WriteErrorWithReason(w, http.StatusConflict, "note was modified since it was read",
			ReasonNoteRevisionConflict, map[string]any{"currentRevision": conflict.Current})
	case errors.Is(err, store.ErrIncidentBusy):
		w.Header().Set("Retry-After", "1")
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "incident is busy; retry shortly", ReasonIncidentBusy, nil)
	case errors.Is(err, store.ErrIncidentInvalid):
		httputil.WriteError(w, http.StatusBadRequest, "invalid incident input", err.Error())
	case errors.Is(err, store.ErrInvalidIncidentCursor), errors.Is(err, store.ErrInvalidEvidenceCursor):
		httputil.WriteError(w, http.StatusBadRequest, "invalid continue cursor", "")
	case errors.Is(err, store.ErrIncidentNotFound):
		h.writeNotFound(w)
	case errors.Is(err, store.ErrNoteNotFound):
		httputil.WriteError(w, http.StatusNotFound, "incident note not found", "")
	case errors.Is(err, store.ErrNotOwner):
		httputil.WriteError(w, http.StatusForbidden, "only the incident owner may do this", "")
	case errors.Is(err, store.ErrNotNoteAuthor):
		httputil.WriteError(w, http.StatusForbidden, "only the note author may edit or delete it", "")
	default:
		h.logger.Error("incident store error", "op", op, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "incident store unavailable", ReasonStoreUnavailable, nil)
	}
}

// decodeBody reads a bounded JSON body into dst; 400 or 413 on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large", "")
			return false
		}
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return false
	}
	return true
}

// pageQuery parses ?limit= and ?continue=. A non-integer limit is a 400;
// the store clamps its range.
func pageQuery(w http.ResponseWriter, r *http.Request) (limit int, cursor string, ok bool) {
	q := r.URL.Query()
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid limit", "limit must be an integer")
			return 0, "", false
		}
		limit = n
	}
	return limit, q.Get("continue"), true
}

// auditLog records a write (P16). Detail is bounded and carries only ids:
// no title, no note body, no evidence.
func (h *Handler) auditLog(r *http.Request, u *auth.User, action audit.Action, result audit.Result, clusterID, kind, detail string) {
	if h.audit == nil {
		return
	}
	entry := audit.Entry{
		Timestamp:    time.Now().UTC(),
		ClusterID:    clusterID,
		User:         u.Username,
		SourceIP:     r.RemoteAddr,
		Action:       action,
		ResourceKind: kind,
		Result:       result,
		Detail:       detail,
	}
	if err := h.audit.Log(r.Context(), entry); err != nil {
		h.logger.Warn("incident audit log failed", "action", action, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Incident handlers
// ---------------------------------------------------------------------------

// HandleList returns the incidents the caller owns or holds a grant on,
// newest first, with the caller's role on each. Metadata.Total is the count
// on this page; every listed incident is visible by definition (P1), and a
// grant revoked between the list read and the role lookup drops the row.
// GET /api/v1/incidents?limit=&continue=
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := pageQuery(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, next, err := h.incidents.ListVisible(ctx, user.ID, limit, cursor)
	if err != nil {
		h.writeStoreFailure(w, "list incidents", err)
		return
	}
	items := make([]IncidentView, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.OwnerID == user.ID {
			items = append(items, incidentView(row, RoleOwner, true))
			continue
		}
		grant, err := h.grants.GetGrant(ctx, row.ID, user.ID)
		if err != nil {
			h.writeStoreFailure(w, "read incident grant", err)
			return
		}
		if grant == nil {
			continue
		}
		items = append(items, incidentView(row, RoleCollaborator, grant.CanAnnotate))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     items,
		Metadata: &api.Metadata{Total: len(items), Continue: next},
	})
}

// HandleCreate opens an incident owned by the caller on the local cluster
// (Release D capture is local-only). The body's title, summary and window
// are validated by the store; its retention is the configured default.
// POST /api/v1/incidents
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	var req createIncidentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	ctx := r.Context()
	id, err := h.incidents.Create(ctx, store.IncidentRow{
		OwnerID: user.ID, ClusterID: k8s.LocalClusterID, Title: req.Title, Summary: req.Summary,
		WindowStart: req.WindowStart, WindowEnd: req.WindowEnd, RetentionDaysAtCapture: h.retentionDays,
	})
	if err != nil {
		h.auditLog(r, user, ActionIncidentCreate, audit.ResultFailure, k8s.LocalClusterID, "incident", "")
		h.writeStoreFailure(w, "create incident", err)
		return
	}
	row, err := h.incidents.Get(ctx, id)
	if err == nil && row == nil {
		err = fmt.Errorf("incident %s vanished after insert", id)
	}
	if err != nil {
		h.writeStoreFailure(w, "read created incident", err)
		return
	}
	h.auditLog(r, user, ActionIncidentCreate, audit.ResultSuccess, row.ClusterID, "incident", "incident "+id.String())
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: incidentView(row, RoleOwner, true)})
}

// HandleGet returns the incident record with one page of its evidence,
// filtered by FilterEvidence. Evidence is read page by page through
// ListByIncident (never materialized whole), so a 500-item incident costs
// one bounded read per page.
// GET /api/v1/incidents/{incidentID}?limit=&continue=
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	limit, cursor, ok := pageQuery(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, next, err := h.evidence.ListByIncident(ctx, c.row.ID, limit, cursor)
	if err != nil {
		h.writeStoreFailure(w, "list incident evidence", err)
		return
	}
	visible, withheld, err := h.FilterEvidence(ctx, user, rows)
	if err != nil {
		h.logger.Error("incident evidence could not be decoded", "incidentId", c.row.ID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "incident evidence unavailable", "")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data: IncidentDetail{
			Incident: incidentView(c.row, c.role, c.canAnnotate),
			Evidence: visible,
			Withheld: withheld,
			Counts:   EvidenceCounts{Visible: len(visible), Withheld: len(withheld)},
		},
		Metadata: &api.Metadata{Total: len(visible), Continue: next},
	})
}

// HandleUpdate replaces the title, summary and status (P3: owner-only; a
// collaborator gets 403, a stranger 404).
// PUT /api/v1/incidents/{incidentID}
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if c.role != RoleOwner {
		httputil.WriteError(w, http.StatusForbidden, "only the incident owner may edit it", "")
		return
	}
	var req updateIncidentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Status == "" {
		req.Status = c.row.Status
	}
	ctx := r.Context()
	id := c.row.ID
	detail := "incident " + id.String()
	if err := h.incidents.Update(ctx, id, user.ID, req.Title, req.Summary, req.Status); err != nil {
		h.auditLog(r, user, ActionIncidentUpdate, audit.ResultFailure, c.row.ClusterID, "incident", detail)
		h.writeStoreFailure(w, "update incident", err)
		return
	}
	row, err := h.incidents.Get(ctx, id)
	if err == nil && row == nil {
		err = store.ErrIncidentNotFound
	}
	if err != nil {
		h.writeStoreFailure(w, "read updated incident", err)
		return
	}
	h.auditLog(r, user, ActionIncidentUpdate, audit.ResultSuccess, row.ClusterID, "incident", detail)
	httputil.WriteData(w, incidentView(row, RoleOwner, true))
}

// HandleDelete removes the incident and everything under it (P3:
// owner-only).
// DELETE /api/v1/incidents/{incidentID}
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if c.role != RoleOwner {
		httputil.WriteError(w, http.StatusForbidden, "only the incident owner may delete it", "")
		return
	}
	detail := "incident " + c.row.ID.String()
	if err := h.incidents.Delete(r.Context(), c.row.ID, user.ID); err != nil {
		h.auditLog(r, user, ActionIncidentDelete, audit.ResultFailure, c.row.ClusterID, "incident", detail)
		h.writeStoreFailure(w, "delete incident", err)
		return
	}
	h.auditLog(r, user, ActionIncidentDelete, audit.ResultSuccess, c.row.ClusterID, "incident", detail)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Note handlers (P3): every call re-checks visibility first; writes need the
// owner or a grant with can_annotate; edit and delete are author-only (the
// store enforces that in SQL).
// ---------------------------------------------------------------------------

// HandleListNotes returns the incident's notes, oldest first, at most the
// store's cap; Metadata.Truncated says when the cap was hit.
// GET /api/v1/incidents/{incidentID}/notes
func (h *Handler) HandleListNotes(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	notes, err := h.incidents.ListNotes(r.Context(), c.row.ID)
	if err != nil {
		h.writeStoreFailure(w, "list incident notes", err)
		return
	}
	items := make([]NoteView, 0, len(notes))
	for _, n := range notes {
		items = append(items, noteView(n))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     items,
		Metadata: &api.Metadata{Total: len(items), Truncated: len(items) >= store.IncidentMaxNotesListed},
	})
}

// requireAnnotate is the note-write gate after visibility: owner, or a
// grant with can_annotate.
func requireAnnotate(w http.ResponseWriter, c *caller) bool {
	if !c.canAnnotate {
		httputil.WriteError(w, http.StatusForbidden, "this grant does not allow annotating the incident", "")
		return false
	}
	return true
}

// HandleCreateNote adds a note by the caller.
// POST /api/v1/incidents/{incidentID}/notes
func (h *Handler) HandleCreateNote(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if !requireAnnotate(w, c) {
		return
	}
	var req noteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	detail := "incident " + c.row.ID.String()
	n, err := h.incidents.CreateNote(r.Context(), c.row.ID, user.ID, req.Body)
	if err != nil {
		h.auditLog(r, user, ActionIncidentNoteCreate, audit.ResultFailure, c.row.ClusterID, "incidentNote", detail)
		h.writeStoreFailure(w, "create incident note", err)
		return
	}
	h.auditLog(r, user, ActionIncidentNoteCreate, audit.ResultSuccess, c.row.ClusterID, "incidentNote", "note "+n.ID.String()+" on "+detail)
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: noteView(n)})
}

// HandleUpdateNote replaces the body of the caller's own note when the
// body's revision is still current (409 note_revision_conflict otherwise).
// PUT /api/v1/incidents/{incidentID}/notes/{noteID}
func (h *Handler) HandleUpdateNote(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if !requireAnnotate(w, c) {
		return
	}
	noteID, ok := parseID(w, r, "noteID", "note")
	if !ok {
		return
	}
	var req noteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	detail := "note " + noteID.String() + " on incident " + c.row.ID.String()
	n, err := h.incidents.UpdateNote(r.Context(), c.row.ID, noteID, user.ID, req.Body, req.Revision)
	if err != nil {
		h.auditLog(r, user, ActionIncidentNoteUpdate, audit.ResultFailure, c.row.ClusterID, "incidentNote", detail)
		h.writeStoreFailure(w, "update incident note", err)
		return
	}
	h.auditLog(r, user, ActionIncidentNoteUpdate, audit.ResultSuccess, c.row.ClusterID, "incidentNote", detail)
	httputil.WriteData(w, noteView(n))
}

// HandleDeleteNote removes the caller's own note.
// DELETE /api/v1/incidents/{incidentID}/notes/{noteID}
func (h *Handler) HandleDeleteNote(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if !requireAnnotate(w, c) {
		return
	}
	noteID, ok := parseID(w, r, "noteID", "note")
	if !ok {
		return
	}
	detail := "note " + noteID.String() + " on incident " + c.row.ID.String()
	if err := h.incidents.DeleteNote(r.Context(), c.row.ID, noteID, user.ID); err != nil {
		h.auditLog(r, user, ActionIncidentNoteDelete, audit.ResultFailure, c.row.ClusterID, "incidentNote", detail)
		h.writeStoreFailure(w, "delete incident note", err)
		return
	}
	h.auditLog(r, user, ActionIncidentNoteDelete, audit.ResultSuccess, c.row.ClusterID, "incidentNote", detail)
	w.WriteHeader(http.StatusNoContent)
}
