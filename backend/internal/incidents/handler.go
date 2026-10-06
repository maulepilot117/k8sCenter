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
//   - counts (P10): the store's raw evidence_bytes, evidence_count and
//     scope_count are never returned. They describe what the caller may not
//     see. Every representation of the record carries instead the counts of
//     evidence the CALLER may read, computed over the whole incident through
//     the same filter; byte totals are not exposed at all (a per-caller
//     visible-bytes sum has no consumer and would be one more number to
//     reason about).
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
//	GET    /api/v1/incidents/{incidentID}/evidence         HandleListEvidence   (handler_capture.go)
//	POST   /api/v1/incidents/{incidentID}/capture          HandleCapture        (handler_capture.go)
//	GET    /api/v1/incidents/{incidentID}/grants           HandleListGrants     (handler_grants.go)
//	POST   /api/v1/incidents/{incidentID}/grants           HandleAddGrant       (handler_grants.go)
//	DELETE /api/v1/incidents/{incidentID}/grants/{granteeID} HandleRemoveGrant  (handler_grants.go)
//	GET    /api/v1/incidents/{incidentID}/export           HandleExport         (handler_export.go)
//
// metadata.total means, per endpoint: on the list, the incidents on this
// page (every listed incident is visible by definition); on the detail
// read and the evidence list, the whole-incident count of evidence the
// caller may read (the evidence array is one page of it, continued by
// metadata.continue); on the notes list, the notes on this page.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
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
	ActionIncidentCreate audit.Action = "incident_create"
	// ActionIncidentCreateReplayed (U25c): a create carried a clientRequestId
	// that already made an incident, so nothing was written and the existing
	// incident was returned. Kept apart from incident_create so a create is
	// never audited twice.
	ActionIncidentCreateReplayed audit.Action = "incident_create_replayed"
	ActionIncidentUpdate         audit.Action = "incident_update"
	ActionIncidentDelete         audit.Action = "incident_delete"
	ActionIncidentNoteCreate     audit.Action = "incident_note_create"
	ActionIncidentNoteUpdate     audit.Action = "incident_note_update"
	ActionIncidentNoteDelete     audit.Action = "incident_note_delete"
	// U23b (handler_capture.go, handler_grants.go, handler_export.go).
	ActionIncidentCapture     audit.Action = "incident_capture"
	ActionIncidentGrantAdd    audit.Action = "incident_grant_add"
	ActionIncidentGrantRemove audit.Action = "incident_grant_remove"
	ActionIncidentExport      audit.Action = "incident_export"
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
	// ReasonCaptureUnavailable: no evidence collector is wired (503).
	ReasonCaptureUnavailable = "incident_capture_unavailable"
	// ReasonRemoteCaptureUnsupported: X-Cluster-ID names a remote cluster;
	// Release D capture is local-only (400, plan A-12).
	ReasonRemoteCaptureUnsupported = "remote_capture_unsupported"
	// ReasonEvidenceLimitExceeded: a byte or item ceiling (413, Q1 P14);
	// extra carries limit, max, current and attempted.
	ReasonEvidenceLimitExceeded = "evidence_limit_exceeded"
	// ReasonScopeLimitExceeded: the distinct-scope cap (409, Q1 P5).
	ReasonScopeLimitExceeded = "scope_limit_exceeded"
	// ReasonIncidentClosed: evidence cannot be captured into a closed
	// incident (409).
	ReasonIncidentClosed = "incident_closed"
	// ReasonGrantLimitReached: the incident has IncidentMaxGrants grantees (409).
	ReasonGrantLimitReached = "grant_limit_reached"
	// ReasonExportFormatInvalid: ?format is not json or markdown (400; no
	// HTML export exists, Q1 P12).
	ReasonExportFormatInvalid = "export_format_invalid"
	// ReasonCaptureOutcomeUnknown: the store's COMMIT result is ambiguous
	// (503 + Retry-After); the capture may or may not be durable, and a
	// retry is safe because duplicates are ignored.
	ReasonCaptureOutcomeUnknown = "incident_capture_outcome_unknown"
	// ReasonInvalidClientRequestID: POST /incidents carried a clientRequestId
	// that is not a canonical, non-nil UUID string (400).
	ReasonInvalidClientRequestID = "invalid_client_request_id"
)

const (
	// DefaultRetentionDays is the retention stamped on new incidents until
	// U25a makes it configurable (Q1 P13). SetRetentionDays is that seam.
	DefaultRetentionDays = 30
	// maxBodyBytes bounds every request body: the largest field is a note
	// body of 20000 characters.
	maxBodyBytes = 256 << 10
	// accessCheckTimeout is the budget for ALL access checks of one request.
	// A cold read of an incident at the scope cap costs at most 20 checks
	// plus the secrets checks; a stalled cluster makes the checks fail
	// within this budget and the rows read as
	// authorization_check_unavailable, instead of the server's request
	// timeout cutting the whole response. It sits under the 30s BFF proxy
	// cap and the 60s request timeout.
	accessCheckTimeout = 15 * time.Second
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
	CreateWithRequestID(ctx context.Context, r store.IncidentRow, requestID uuid.UUID) (uuid.UUID, bool, error)
	Get(ctx context.Context, id uuid.UUID) (*store.IncidentRow, error)
	ListVisible(ctx context.Context, userID string, limit int, cursor string) ([]store.IncidentRow, string, error)
	Update(ctx context.Context, id uuid.UUID, ownerID string, title, summary, status *string) error
	Delete(ctx context.Context, id uuid.UUID, ownerID string) error
	CreateNote(ctx context.Context, incidentID uuid.UUID, authorID, body string) (store.IncidentNoteRow, error)
	ListNotes(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]store.IncidentNoteRow, string, error)
	UpdateNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID, body string, expectedRevision int) (store.IncidentNoteRow, error)
	DeleteNote(ctx context.Context, incidentID, noteID uuid.UUID, authorID string) error
}

type evidenceStore interface {
	ListByIncident(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]store.IncidentEvidenceRow, string, error)
	ListScopeRowsByIncident(ctx context.Context, incidentID uuid.UUID) ([]store.IncidentEvidenceRow, error)
	InsertBatch(ctx context.Context, incidentID uuid.UUID, ownerID string, rows []store.IncidentEvidenceRow, limits store.EvidenceLimits) (int, error)
}

type grantStore interface {
	GetGrant(ctx context.Context, incidentID uuid.UUID, userID string) (*store.IncidentGrantRow, error)
	GrantsFor(ctx context.Context, userID string, incidentIDs []uuid.UUID) (map[uuid.UUID]store.IncidentGrantRow, error)
	ListGrants(ctx context.Context, incidentID uuid.UUID) ([]store.IncidentGrantRow, error)
	AddGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string, canAnnotate bool) error
	RemoveGrant(ctx context.Context, incidentID uuid.UUID, ownerID, granteeID string) error
}

// capturer is the one *Collector method HandleCapture calls. Unexported so
// tests drive the real Collector with stub sources (collector_test.go) or
// a fake.
type capturer interface {
	Capture(ctx context.Context, req CaptureRequest) (CaptureReport, error)
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
	access    accessChecker
	// collector runs captures (nil: capture answers 503 ReasonCaptureUnavailable).
	collector capturer
	// limits are the capture bounds InsertBatch enforces and the capture
	// timeout; the same Limits the collector was built with (plan A-6).
	limits Limits
	audit  audit.Logger
	// retentionDays is stamped on each new incident (retention_days_at_capture).
	retentionDays int
	// now is the capture budget's clock; nil means time.Now (test seam).
	now func() time.Time
	// accessTimeout is accessCheckTimeout; a field so tests can shorten it.
	accessTimeout time.Duration
	// exportMax is exportMaxBytes; a field so tests can shrink it.
	exportMax int
	// exportSlots and captureSlots are the process-wide bulkheads (see
	// acquire); a full one answers 503 incident_busy with Retry-After.
	exportSlots  chan struct{}
	captureSlots chan struct{}
	// exportByUser counts each user's in-flight exports (at most
	// exportPerUser); entries are removed at zero, so it never grows past
	// the number of users exporting right now. Guarded by exportMu.
	exportMu     sync.Mutex
	exportByUser map[string]int
	logger       *slog.Logger
}

// Bulkhead sizes. The export path's peak heap is exportConcurrency x
// (exportMaxBytes of accumulated content + one serialized element + a
// 64 KiB write buffer): the JSON is written element by element, never as a
// whole-document buffer. exportPerUser keeps one user from holding every
// global slot. captureConcurrency bounds how many captures fan out
// impersonated reads at once (each already bounded by
// Limits.MaxConcurrency sources).
//
// All three caps are per process. With N backend replicas a user can run N
// exports at once (one per replica) and the cluster-wide peak export memory
// is N times the per-replica bound. That is acceptable at the chart's
// default replicaCount of 1; scaling out multiplies both.
const (
	exportConcurrency  = 2
	exportPerUser      = 1
	captureConcurrency = 4
)

// acquireUserExport takes one of the caller's exportPerUser slots without
// waiting; release deletes the entry when the count returns to zero.
func (h *Handler) acquireUserExport(userID string) (release func(), ok bool) {
	h.exportMu.Lock()
	defer h.exportMu.Unlock()
	if h.exportByUser[userID] >= exportPerUser {
		return nil, false
	}
	h.exportByUser[userID]++
	return func() {
		h.exportMu.Lock()
		defer h.exportMu.Unlock()
		if h.exportByUser[userID] <= 1 {
			delete(h.exportByUser, userID)
		} else {
			h.exportByUser[userID]--
		}
	}, true
}

// NewHandler builds the handler. The stores may be nil (no database): every
// endpoint then answers 503 ReasonPersistenceUnavailable. A nil collector
// makes capture answer 503 ReasonCaptureUnavailable; limits must be the
// Limits the collector was built with (it validated them). A nil access
// checker withholds every evidence item as authorization_check_unavailable.
// A nil audit logger audits nothing.
func NewHandler(incidents *store.IncidentStore, evidence *store.IncidentEvidenceStore, grants *store.IncidentGrantStore,
	collector *Collector, limits Limits, access *resources.AccessChecker, auditLogger audit.Logger, logger *slog.Logger) *Handler {
	h := &Handler{limits: limits, audit: auditLogger, retentionDays: DefaultRetentionDays, accessTimeout: accessCheckTimeout,
		exportMax: exportMaxBytes, exportSlots: make(chan struct{}, exportConcurrency),
		captureSlots: make(chan struct{}, captureConcurrency), exportByUser: map[string]int{}, logger: logger}
	if collector != nil {
		h.collector = collector
	}
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
	return &Handler{incidents: incidents, evidence: evidence, grants: grants, access: access, limits: DefaultLimits(),
		audit: auditLogger, retentionDays: DefaultRetentionDays, accessTimeout: accessCheckTimeout, exportMax: exportMaxBytes,
		exportSlots: make(chan struct{}, exportConcurrency), captureSlots: make(chan struct{}, captureConcurrency),
		exportByUser: map[string]int{}, logger: logger}
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
// It deliberately carries none of the store's evidence totals (P10): those
// are in EvidenceCounts, computed per caller.
type IncidentView struct {
	ID            string     `json:"id"`
	OwnerID       string     `json:"ownerId"`
	ClusterID     string     `json:"clusterId"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	Status        string     `json:"status"`
	WindowStart   time.Time  `json:"windowStart"`
	WindowEnd     *time.Time `json:"windowEnd,omitempty"`
	RetentionDays int        `json:"retentionDays"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	ClosedAt      *time.Time `json:"closedAt,omitempty"`
	Role          Role       `json:"role"`
	CanAnnotate   bool       `json:"canAnnotate"`
}

// EvidenceCounts are whole-incident counts for the CALLER: what they may
// read now and what is withheld from them (P10). Neither says anything about
// a withheld item's scope.
type EvidenceCounts struct {
	Visible  int `json:"visible"`
	Withheld int `json:"withheld"`
}

// IncidentSummary is the POST /incidents and PUT /incidents/{id} response:
// the record and the caller's counts. Counts is nil only when the counting
// read failed after a committed write (the write is still reported).
type IncidentSummary struct {
	Incident IncidentView    `json:"incident"`
	Counts   *EvidenceCounts `json:"counts,omitempty"`
}

// IncidentDetail is the GET /incidents/{id} response: the record, the
// caller's whole-incident counts, and one page of evidence the caller may
// read with placeholders for the page's withheld items.
type IncidentDetail struct {
	Incident IncidentView       `json:"incident"`
	Counts   EvidenceCounts     `json:"counts"`
	Evidence []Evidence         `json:"evidence"`
	Withheld []WithheldEvidence `json:"withheld"`
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
//
// ClientRequestID (U25c) is an optional idempotency key the client generates
// once per create attempt and resends on every retry of it. Absent or null,
// the create behaves as it always did.
type createIncidentRequest struct {
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	WindowStart     time.Time  `json:"windowStart"`
	WindowEnd       *time.Time `json:"windowEnd,omitempty"`
	ClientRequestID *string    `json:"clientRequestId,omitempty"`
}

// parseClientRequestID validates a clientRequestId strictly: exactly the
// 36-character hyphenated UUID form (either hex case) and not the nil UUID.
// uuid.Parse alone also accepts braced, urn: and unhyphenated spellings,
// which would let one key be written several ways.
func parseClientRequestID(raw string) (uuid.UUID, bool) {
	if len(raw) != 36 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// updateIncidentRequest is the PUT /incidents/{id} body. A field left out
// keeps its current value; an explicit empty string is a value.
type updateIncidentRequest struct {
	Title   *string `json:"title"`
	Summary *string `json:"summary"`
	Status  *string `json:"status"`
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
		Status: r.Status, WindowStart: r.WindowStart, WindowEnd: r.WindowEnd, RetentionDays: r.RetentionDaysAtCapture,
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

// scopeMemo memoizes `get` decisions per scope for ONE request (R-1: no
// cross-request cache beyond the AccessChecker's own 60s). Every scope gets
// exactly one attempt; an error on one scope says nothing about another, so
// nothing short-circuits on it. What bounds the request is ctx, which the
// handler derives with accessCheckTimeout: once it is done, every uncached
// scope is unavailable without a call. That is deliberately fail-closed:
// the AccessChecker's own 60s cache might have answered some of those
// scopes without a round trip, but the memo cannot tell a cached scope
// from one that would dial, and a budget that has already run out must not
// be spent on finding out. The caller sees authorization_check_unavailable
// (retryable), never a guess.
type scopeMemo struct {
	ctx       context.Context
	access    accessChecker
	user      *auth.User
	logger    *slog.Logger
	cache     map[store.EvidenceScope]scopeDecision
	exhausted bool // logged once
}

// newScopeMemo starts a memo under the request's access budget. The returned
// cancel releases the budget; the caller defers it.
func (h *Handler) newScopeMemo(ctx context.Context, u *auth.User) (*scopeMemo, context.CancelFunc) {
	bctx, cancel := context.WithTimeout(ctx, h.accessTimeout)
	return &scopeMemo{ctx: bctx, access: h.access, user: u, logger: h.logger,
		cache: map[store.EvidenceScope]scopeDecision{}}, cancel
}

func (m *scopeMemo) decide(sc store.EvidenceScope) scopeDecision {
	if d, ok := m.cache[sc]; ok {
		return d
	}
	var d scopeDecision
	switch {
	case m.access == nil:
		d.unavailable = true
	case m.ctx.Err() != nil:
		if !m.exhausted {
			m.exhausted = true
			m.logger.Warn("incident evidence access budget exhausted; withholding every unchecked scope as authorization_check_unavailable",
				"user", m.user.ID, "error", m.ctx.Err())
		}
		d.unavailable = true
	default:
		// The STORED cluster, never the request header (P6).
		allowed, err := m.access.CanAccessGroupResource(m.ctx, sc.ClusterID,
			m.user.KubernetesUsername, m.user.KubernetesGroups, "get", sc.APIGroup, sc.Resource, sc.Namespace)
		if err != nil {
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
// is `forbidden` whatever the others said, because no retry could change
// it; otherwise a failed check on any scope is
// `authorization_check_unavailable`.
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

// filter partitions rows into the items the caller may read and placeholders
// for the rest, both in input order. An undecodable stored row is an error (a
// server fault, not a policy outcome).
func (m *scopeMemo) filter(rows []store.IncidentEvidenceRow) ([]Evidence, []WithheldEvidence, error) {
	visible := make([]Evidence, 0, len(rows))
	withheld := make([]WithheldEvidence, 0)
	for _, row := range rows {
		if reason := m.readDecision(row); reason != "" {
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

// count applies the same decision to scope-only rows and returns only
// numbers.
func (m *scopeMemo) count(rows []store.IncidentEvidenceRow) EvidenceCounts {
	var c EvidenceCounts
	for _, row := range rows {
		if m.readDecision(row) == "" {
			c.Visible++
		} else {
			c.Withheld++
		}
	}
	return c
}

// FilterEvidence is the ONLY authorization filter for evidence (Q1 P10):
// every representation of evidence calls it (the detail read shares its
// memo with the whole-incident count through the same decision). It
// partitions rows into the items u may read now and placeholders for the
// rest, both in input order. The decision is the caller's CURRENT
// authorization (P4, P7) for each row's stored scope on the row's stored
// cluster (P6); the live object is never consulted, so its deletion changes
// nothing (P8). A placeholder carries only id, evidenceKind, collectedAt,
// withheld and withheldReason.
func (h *Handler) FilterEvidence(ctx context.Context, u *auth.User, rows []store.IncidentEvidenceRow) ([]Evidence, []WithheldEvidence, error) {
	memo, cancel := h.newScopeMemo(ctx, u)
	defer cancel()
	return memo.filter(rows)
}

// countEvidence computes the caller's whole-incident counts through memo.
func (h *Handler) countEvidence(ctx context.Context, memo *scopeMemo, incidentID uuid.UUID) (EvidenceCounts, error) {
	rows, err := h.evidence.ListScopeRowsByIncident(ctx, incidentID)
	if err != nil {
		return EvidenceCounts{}, err
	}
	return memo.count(rows), nil
}

// countsBestEffort is countEvidence for the echo of a committed write: a
// failure is logged and reported as unknown (nil), never as a failed write.
func (h *Handler) countsBestEffort(ctx context.Context, u *auth.User, incidentID uuid.UUID) *EvidenceCounts {
	memo, cancel := h.newScopeMemo(ctx, u)
	defer cancel()
	c, err := h.countEvidence(ctx, memo, incidentID)
	if err != nil {
		h.logger.Warn("incident evidence counts unavailable after write", "incidentId", incidentID, "error", err)
		return nil
	}
	return &c
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

// requireOwner is the P3 gate after visibility: the caller already knows
// the incident exists, so a non-owner is told 403 "only the incident owner
// may <what>".
func requireOwner(w http.ResponseWriter, c *caller, what string) bool {
	if c.role != RoleOwner {
		httputil.WriteError(w, http.StatusForbidden, "only the incident owner may "+what, "")
		return false
	}
	return true
}

func (h *Handler) writeNotFound(w http.ResponseWriter) {
	httputil.WriteError(w, http.StatusNotFound, "incident not found", "")
}

// writeBusy is the retryable 503: a lock wait timed out in the store, or a
// bulkhead (export, capture) is full.
func writeBusy(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", "1")
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, message, ReasonIncidentBusy, nil)
}

// acquire takes a slot from a bulkhead without waiting. ok is false when it
// is full; otherwise the caller defers release. Bulkheads bound the
// process-wide concurrency of the two endpoints whose cost is not a
// constant: an export holds up to exportMaxBytes of content on the heap
// while it streams, and a capture fans out impersonated reads. The
// per-user rate limiter cannot bound either across users, and N parallel
// exports would otherwise hold N x 16 MiB in a 256 Mi pod.
func acquire(slots chan struct{}) (release func(), ok bool) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, true
	default:
		return nil, false
	}
}

// writeStoreFailure maps a store error to a response. Validation and cursor
// errors are the caller's (400, with the store's own message); ownership and
// authorship misses are 403 (only reached after the visibility gate, so the
// caller already knows the incident exists) or 404; a lock timeout is a
// retryable 503; anything else is logged and answered 503 without detail.
func (h *Handler) writeStoreFailure(w http.ResponseWriter, op string, err error) {
	var (
		conflict   *store.NoteRevisionConflictError
		evLimit    *store.EvidenceLimitError
		scopeLimit *store.ScopeLimitError
	)
	switch {
	case errors.As(err, &conflict):
		httputil.WriteErrorWithReason(w, http.StatusConflict, "note was modified since it was read",
			ReasonNoteRevisionConflict, map[string]any{"currentRevision": conflict.Current})
	case errors.As(err, &evLimit):
		// InsertBatch precedence (store): item_bytes before the lock, items
		// and incident_bytes after the insert. For item_bytes Current is 0
		// (the offending item's size is Attempted); every case reports all
		// three so the client can say which bound was hit (Q1 P14).
		httputil.WriteErrorWithReason(w, http.StatusRequestEntityTooLarge, "incident evidence limit exceeded",
			ReasonEvidenceLimitExceeded, map[string]any{
				"limit": evLimit.Limit, "max": evLimit.Max, "current": evLimit.Current, "attempted": evLimit.Attempted,
			})
	case errors.As(err, &scopeLimit):
		httputil.WriteErrorWithReason(w, http.StatusConflict, "incident evidence scope limit exceeded",
			ReasonScopeLimitExceeded, map[string]any{
				"max": scopeLimit.Max, "current": scopeLimit.Current, "attempted": scopeLimit.Attempted,
			})
	case errors.Is(err, store.ErrIncidentClosed):
		httputil.WriteErrorWithReason(w, http.StatusConflict, "incident is closed; reopen it to capture evidence", ReasonIncidentClosed, nil)
	case errors.Is(err, store.ErrGrantLimit):
		httputil.WriteErrorWithReason(w, http.StatusConflict, "incident grant limit reached", ReasonGrantLimitReached,
			map[string]any{"max": store.IncidentMaxGrants})
	case errors.Is(err, store.ErrGrantNotFound):
		httputil.WriteError(w, http.StatusNotFound, "incident grant not found", "")
	case errors.Is(err, store.ErrIncidentBusy):
		writeBusy(w, "incident is busy; retry shortly")
	case errors.Is(err, store.ErrIncidentInvalid):
		httputil.WriteError(w, http.StatusBadRequest, "invalid incident input", err.Error())
	case errors.Is(err, store.ErrInvalidIncidentCursor), errors.Is(err, store.ErrInvalidEvidenceCursor), errors.Is(err, store.ErrInvalidNoteCursor):
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
// Unknown fields are ignored (a typo is not a claim).
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst, false)
}

// decodeStrictBody is decodeBody with unknown fields refused: for bodies
// where an extra field would be a security-relevant claim (a cluster, a
// UID, a role) rather than a harmless typo.
func decodeStrictBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst, true)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
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
// newest first, with the caller's role on each; collaborator roles are
// resolved with one grant query for the page. A grant revoked between the
// list read and that query drops the row (it is no longer visible, P7).
// Rows carry no evidence counts: those cost a scope read per incident and
// are on the detail read.
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
	shared := make([]uuid.UUID, 0)
	for i := range rows {
		if rows[i].OwnerID != user.ID {
			shared = append(shared, rows[i].ID)
		}
	}
	grants := map[uuid.UUID]store.IncidentGrantRow{}
	if len(shared) > 0 {
		if grants, err = h.grants.GrantsFor(ctx, user.ID, shared); err != nil {
			h.writeStoreFailure(w, "read incident grants", err)
			return
		}
	}
	items := make([]IncidentView, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.OwnerID == user.ID {
			items = append(items, incidentView(row, RoleOwner, true))
			continue
		}
		if grant, ok := grants[row.ID]; ok {
			items = append(items, incidentView(row, RoleCollaborator, grant.CanAnnotate))
		}
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     items,
		Metadata: &api.Metadata{Total: len(items), Continue: next},
	})
}

// HandleCreate opens an incident owned by the caller on the local cluster
// (Release D capture is local-only). The body's title, summary and window
// are validated by the store; its retention is the configured default. The
// write is audited as soon as it commits; if the read-back then fails the
// response is still 201, built from the id and the request.
//
// Idempotency (U25c): with a clientRequestId, a retry of a create whose
// response was lost returns the incident the first attempt made, 200 instead
// of 201, same body shape. Nothing is written and the stored incident is
// returned as it is, even if the retry's title, summary or window differ
// (first write wins). The replay is audited as incident_create_replayed, so
// one incident is never audited as two creates. A replay whose read-back
// fails is a retryable 503, never a body built from the request: the
// request's fields are not what was stored.
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
	var requestID uuid.UUID
	if req.ClientRequestID != nil {
		if requestID, ok = parseClientRequestID(*req.ClientRequestID); !ok {
			httputil.WriteErrorWithReason(w, http.StatusBadRequest,
				"clientRequestId must be a non-nil UUID in its 36-character hyphenated form",
				ReasonInvalidClientRequestID, nil)
			return
		}
	}
	ctx := r.Context()
	intended := store.IncidentRow{
		OwnerID: user.ID, ClusterID: k8s.LocalClusterID, Title: req.Title, Summary: req.Summary,
		WindowStart: req.WindowStart, WindowEnd: req.WindowEnd, RetentionDaysAtCapture: h.retentionDays,
	}
	var (
		id      uuid.UUID
		created = true
		err     error
	)
	if requestID == uuid.Nil {
		id, err = h.incidents.Create(ctx, intended)
	} else {
		id, created, err = h.incidents.CreateWithRequestID(ctx, intended, requestID)
	}
	if err != nil {
		h.auditLog(r, user, ActionIncidentCreate, audit.ResultFailure, k8s.LocalClusterID, "incident", "")
		h.writeStoreFailure(w, "create incident", err)
		return
	}
	if !created {
		h.auditLog(r, user, ActionIncidentCreateReplayed, audit.ResultSuccess, k8s.LocalClusterID, "incident", "incident "+id.String())
		h.writeReplayedCreate(w, r, user, id)
		return
	}
	h.auditLog(r, user, ActionIncidentCreate, audit.ResultSuccess, k8s.LocalClusterID, "incident", "incident "+id.String())

	row, err := h.incidents.Get(ctx, id)
	if err == nil && row == nil {
		err = fmt.Errorf("incident %s not readable after insert", id)
	}
	if err != nil {
		h.logger.Warn("incident created but could not be read back; answering from the request", "incidentId", id, "error", err)
		intended.ID = id
		intended.Status = store.IncidentStatusOpen
		row = &intended
	}
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: IncidentSummary{
		Incident: incidentView(row, RoleOwner, true),
		Counts:   h.countsBestEffort(ctx, user, id),
	}})
}

// writeReplayedCreate answers a create replay with the stored incident, 200.
// The key is per owner, so the caller owns it. A read fault is the usual
// 503; an incident deleted since the replay matched is a retryable 503 too,
// because a retry with the same clientRequestId now creates a new one.
func (h *Handler) writeReplayedCreate(w http.ResponseWriter, r *http.Request, user *auth.User, id uuid.UUID) {
	ctx := r.Context()
	row, err := h.incidents.Get(ctx, id)
	if err != nil {
		h.writeStoreFailure(w, "read replayed incident", err)
		return
	}
	if row == nil {
		writeBusy(w, "incident was deleted while the create was replayed; retry")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: IncidentSummary{
		Incident: incidentView(row, RoleOwner, true),
		Counts:   h.countsBestEffort(ctx, user, id),
	}})
}

// HandleGet returns the incident record, the caller's whole-incident
// evidence counts, and one page of its evidence filtered by the same
// decision. Evidence is read page by page through ListByIncident (never
// materialized whole); the count reads scope columns only.
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
	page, next, ok := h.evidencePage(w, r, user, c.row.ID)
	if !ok {
		return
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data: IncidentDetail{
			Incident: incidentView(c.row, c.role, c.canAnnotate),
			Counts:   page.Counts,
			Evidence: page.Evidence,
			Withheld: page.Withheld,
		},
		Metadata: &api.Metadata{Total: page.Counts.Visible, Continue: next},
	})
}

// HandleUpdate replaces the title, summary and status (P3: owner-only; a
// collaborator gets 403, a stranger 404). A field left out of the body keeps
// its current value. The write is audited as soon as it commits; if the
// read-back then fails the response is still 200, built from the row as it
// was known plus the fields written.
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
	if !requireOwner(w, c, "edit it") {
		return
	}
	var req updateIncidentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// The pointers go straight to the store: an omitted field is merged by
	// COALESCE in the UPDATE, never from this request's (possibly stale)
	// read, so interleaved partial updates keep each other's fields.
	ctx := r.Context()
	id := c.row.ID
	detail := "incident " + id.String()
	if err := h.incidents.Update(ctx, id, user.ID, req.Title, req.Summary, req.Status); err != nil {
		h.auditLog(r, user, ActionIncidentUpdate, audit.ResultFailure, c.row.ClusterID, "incident", detail)
		h.writeStoreFailure(w, "update incident", err)
		return
	}
	h.auditLog(r, user, ActionIncidentUpdate, audit.ResultSuccess, c.row.ClusterID, "incident", detail)

	row, err := h.incidents.Get(ctx, id)
	if err == nil && row == nil {
		err = fmt.Errorf("incident %s not readable after update", id)
	}
	if err != nil {
		// Best-known view: the row as read before the write with this
		// request's fields applied (a concurrent partial update's fields
		// may be missing from it; the stored row is right).
		h.logger.Warn("incident updated but could not be read back; answering from the known row", "incidentId", id, "error", err)
		known := *c.row
		if req.Title != nil {
			known.Title = *req.Title
		}
		if req.Summary != nil {
			known.Summary = *req.Summary
		}
		if req.Status != nil {
			known.Status = *req.Status
		}
		switch {
		case known.Status == store.IncidentStatusClosed && known.ClosedAt == nil:
			now := time.Now().UTC()
			known.ClosedAt = &now
		case known.Status != store.IncidentStatusClosed:
			known.ClosedAt = nil
		}
		row = &known
	}
	httputil.WriteData(w, IncidentSummary{
		Incident: incidentView(row, RoleOwner, true),
		Counts:   h.countsBestEffort(ctx, user, id),
	})
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
	if !requireOwner(w, c, "delete it") {
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

// HandleListNotes returns one page of the incident's notes, oldest first;
// metadata.continue leads to the next page, so every note is reachable.
// GET /api/v1/incidents/{incidentID}/notes?limit=&continue=
func (h *Handler) HandleListNotes(w http.ResponseWriter, r *http.Request) {
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
	notes, next, err := h.incidents.ListNotes(r.Context(), c.row.ID, limit, cursor)
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
		Metadata: &api.Metadata{Total: len(items), Continue: next},
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
