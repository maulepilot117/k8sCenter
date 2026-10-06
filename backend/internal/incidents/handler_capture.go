package incidents

// Capture and evidence-list handlers (Release D, U23b; Q1 P3, P5, P14, P15).
//
// Capture is owner-only (P3): a collaborator, who can see the incident, gets
// 403; anyone else gets the same 404 a missing id gets (P1). The target is
// {namespace, kind, name} and nothing else: the cluster is ALWAYS the local
// one (plan A-12: a remote X-Cluster-ID is refused before anything is
// collected) and the object's UID is never taken from the caller (the
// collector derives identity from a SAR-gated impersonated read, Q1 P9), so
// the body is decoded strictly and a clusterId or uid field is a 400.
//
// The collector returns an honest report; this handler persists it. Each
// item is re-validated with store.ValidateEvidenceRow and an invalid one is
// dropped and counted, so one bad item never fails its siblings (partial
// capture truth), and the store's InsertBatch precedence is mapped to one
// status per cause (writeStoreFailure). A request cancelled before the
// insert persists nothing: Capture returns the context error with no items.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// storeWriteTimeout bounds the InsertBatch, AddGrant and RemoveGrant calls
// (review obligation: deadline-bearing contexts into the lock-taking
// writes). It sits above the store's 5s lock timeout so a busy incident is
// reported as incident_busy by the store rather than as a deadline here.
const storeWriteTimeout = 10 * time.Second

// Capture request budget. The browser reaches POST /incidents/{id}/capture
// through the frontend proxy, which gives up after 30 s (PROXY_TIMEOUT_MS in
// frontend/server/api-proxy.ts); the server WriteTimeout is 60 s
// (internal/server/server.go) and is not the binding limit. The whole handler
// path, measured from handler entry, therefore has to finish inside
// captureRequestBudget, which leaves 3 s of headroom under the proxy:
//
//	collector deadline (CaptureTimeout) + captureGrace
//	  + insert work (at least captureMinInsertWork)
//	  + the detached COMMIT bound (store.IncidentCommitTimeout)
//	<= captureRequestBudget
//
// The collector runs under the earlier of the configured CaptureTimeout and
// captureNotAfter (budget - commit bound - minimum insert work - grace, from
// handler entry), so slow work before collection shortens the collection
// instead of the insert. ResolveSettings clamps CaptureTimeout to
// maxCaptureTimeout (budget - grace - minimum insert work - commit bound =
// 14.75 s); the default is 14 s. The insert then gets the lesser of
// storeWriteTimeout and what is left of the budget before its COMMIT (see
// captureInsertDeadline), so even a capture that used its whole collection
// budget cannot push the request past captureRequestBudget. The commit runs
// detached from the caller's context and is bounded by
// store.IncidentCommitTimeout, which captureInsertDeadline reserves.
const (
	captureRequestBudget = 27 * time.Second
	// captureMinInsertWork is the insert time a maximal collection must still
	// leave: the store's lock wait (store.IncidentLockTimeout, 5 s) plus 2 s
	// for the writes, so a busy incident is reported as incident_busy by the
	// store rather than as a deadline here.
	captureMinInsertWork = store.IncidentLockTimeout + 2*time.Second
)

// isQueryCanceled reports a PostgreSQL query_canceled (SQLSTATE 57014), which
// pgx can return instead of a context error when a context deadline or cancel
// interrupts a statement.
func isQueryCanceled(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "57014"
}

// minCollectionWindow is the least collector time worth starting a capture
// for. It equals the CaptureTimeout floor (minTimeout, 1 s): below it the
// collector could not meet even the smallest configurable deadline, so the
// request is refused as retryable instead of returning an empty capture.
const minCollectionWindow = time.Second

// captureNotAfter is the latest the collector may run until: the budget's end
// minus the COMMIT bound, the minimum insert work and the collector grace.
func captureNotAfter(start time.Time) time.Time {
	return start.Add(captureRequestBudget - store.IncidentCommitTimeout - captureMinInsertWork - captureGrace)
}

// clock returns the current time; tests inject h.now to simulate a slow
// collection without waiting for it.
func (h *Handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// captureInsertDeadline is the deadline for InsertBatch's work: the lesser of
// now+storeWriteTimeout and the budget's end minus the COMMIT bound, so the
// commit that follows ends inside captureRequestBudget measured from start.
func captureInsertDeadline(start, now time.Time) time.Time {
	byStore := now.Add(storeWriteTimeout)
	byBudget := start.Add(captureRequestBudget - store.IncidentCommitTimeout)
	if byBudget.Before(byStore) {
		return byBudget
	}
	return byStore
}

// captureRequest is the POST /incidents/{id}/capture body. There is
// deliberately no cluster and no uid field: the body is decoded with
// DisallowUnknownFields, so sending either is a 400 rather than silently
// ignored.
type captureRequest struct {
	Namespace string   `json:"namespace"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Sources   []string `json:"sources,omitempty"`
}

// CaptureResponse is the capture result: the collector's per-source report
// (fixed, scope-free details) and what the store did with its items. Items
// themselves are not echoed; the evidence list is the read path and applies
// the read-time filter like every other representation (P10).
type CaptureResponse struct {
	Completeness Completeness   `json:"completeness"`
	CollectedAt  time.Time      `json:"collectedAt"`
	Sources      []SourceReport `json:"sources"`
	// Collected is how many items the collector returned.
	Collected int `json:"collected"`
	// Inserted is how many new rows the store wrote; Deduplicated is how many
	// items already existed for this incident (same capture_key, P15) and
	// were skipped; Dropped is how many failed row validation here.
	Inserted     int `json:"inserted"`
	Deduplicated int `json:"deduplicated"`
	Dropped      int `json:"dropped"`
}

// EvidencePage is the GET /incidents/{id}/evidence response: the caller's
// whole-incident counts and one page of evidence filtered by the same
// decision, with placeholders for the page's withheld items.
type EvidencePage struct {
	Counts   EvidenceCounts     `json:"counts"`
	Evidence []Evidence         `json:"evidence"`
	Withheld []WithheldEvidence `json:"withheld"`
}

// captureTarget validates the body's target against the kinds the collector
// supports (exactly the kinds diagnostics resolves: diagnostics.TargetResource
// is the supported set, and Nodes are not in it) and returns the TargetRef
// with the group, version and plural resource the SAR gates need.
func captureTarget(req captureRequest) (TargetRef, error) {
	if !resources.ValidateK8sName(req.Namespace) || req.Namespace == "" {
		return TargetRef{}, errors.New("namespace must be a DNS-1123 name")
	}
	if !resources.ValidateK8sName(req.Name) || req.Name == "" {
		return TargetRef{}, errors.New("name must be a DNS-1123 name")
	}
	group, version, resource, ok := diagnostics.TargetResource(req.Kind)
	if !ok {
		return TargetRef{}, fmt.Errorf("kind %q is not supported for capture", req.Kind)
	}
	return TargetRef{APIGroup: group, Version: version, Resource: resource, Kind: req.Kind, Namespace: req.Namespace, Name: req.Name}, nil
}

// HandleCapture collects evidence about one local-cluster object into the
// incident (owner-only) and persists it.
// POST /api/v1/incidents/{incidentID}/capture
func (h *Handler) HandleCapture(w http.ResponseWriter, r *http.Request) {
	start := h.clock() // the budget is measured from handler entry
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	if h.collector == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, "incident evidence capture unavailable", ReasonCaptureUnavailable, nil)
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok {
		return
	}
	if !requireOwner(w, c, "capture evidence") {
		return
	}
	// A closed investigation is frozen: refused here before any impersonated
	// read runs. InsertBatch re-checks under the row lock, which is the
	// authoritative guard against a close that races this read.
	if c.row.Status == store.IncidentStatusClosed {
		h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence",
			"incident "+c.row.ID.String()+": refused, incident is closed")
		h.writeStoreFailure(w, "capture into closed incident", store.ErrIncidentClosed)
		return
	}
	// The header-selected cluster is refused before any read (A-12). The
	// body cannot name a cluster at all.
	if selected := middleware.ClusterIDFromContext(r.Context()); !k8s.IsLocalClusterID(selected) {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "evidence capture is supported on the local cluster only",
			ReasonRemoteCaptureUnsupported, map[string]any{"selectedCluster": selected})
		return
	}
	var req captureRequest
	if !decodeStrictBody(w, r, &req) {
		return
	}
	target, err := captureTarget(req)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid capture target", err.Error())
		return
	}
	id := c.row.ID
	detail := "incident " + id.String()
	release, ok := acquire(h.captureSlots)
	if !ok {
		h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence",
			detail+": refused, capture bulkhead full")
		writeBusy(w, "too many captures in progress; retry shortly")
		return
	}
	defer release()

	// The collector bounds the sources with its own CaptureTimeout (and a
	// short grace); this context bounds the whole operation, insert
	// included, to captureRequestBudget from handler entry. It outlives the
	// collector's deadline (CaptureTimeout is clamped to leave the insert
	// its share), so a capture that hits that deadline still returns its
	// partial report instead of the context error.
	ctx, cancel := context.WithDeadline(r.Context(), start.Add(captureRequestBudget))
	defer cancel()
	// Work before collection (authorization, loading the incident, decoding)
	// counts against the budget. If less than minCollectionWindow of the
	// collector's share is left, answer retryable without running any source
	// (a shorter window would only produce an empty 200 capture); otherwise
	// cap the collector's deadline at what is left.
	notAfter := captureNotAfter(start)
	if notAfter.Sub(h.clock()) < minCollectionWindow {
		h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence",
			detail+": refused, capture budget exhausted before collection")
		writeBusy(w, "the capture budget was used up before collection started; nothing was recorded; retry")
		return
	}
	report, err := h.collector.Capture(ctx, CaptureRequest{ClusterID: k8s.LocalClusterID, User: user, Target: target, Sources: req.Sources, NotAfter: notAfter})
	if err != nil {
		h.writeCaptureFailure(w, r, user, c.row, err)
		return
	}

	rows := make([]store.IncidentEvidenceRow, 0, len(report.Items))
	dropped := 0
	for i, item := range report.Items {
		row, err := item.Row()
		if err == nil {
			err = store.ValidateEvidenceRow(row)
		}
		if err != nil {
			dropped++
			h.logger.Warn("incident capture item failed row validation; dropped", "incidentId", id, "item", i, "kind", item.EvidenceKind, "error", err)
			continue
		}
		rows = append(rows, row)
	}
	inserted := 0
	if len(rows) > 0 {
		insertCtx, cancelInsert := context.WithDeadline(ctx, captureInsertDeadline(start, h.clock()))
		inserted, err = h.evidence.InsertBatch(insertCtx, id, user.ID, rows, h.limits.EvidenceLimits())
		cancelInsert()
		switch {
		case errors.Is(err, store.ErrCommitOutcomeUnknown):
			// The COMMIT's result is ambiguous (no reply, the commit bound
			// passed, or a FATAL/PANIC, class 57 or class 08 reply that can
			// follow a local commit): the batch may be durable.
			// Never claim "nothing was recorded"; a retry is safe (P15
			// de-duplication).
			h.logger.Warn("incident capture commit outcome unknown", "incidentId", id, "error", err)
			h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence",
				detail+": outcome unknown (commit result ambiguous)")
			w.Header().Set("Retry-After", "1")
			httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
				"the capture may or may not have been recorded; retrying is safe (duplicates are ignored)",
				ReasonCaptureOutcomeUnknown, nil)
			return
		case (errors.Is(err, context.DeadlineExceeded) || isQueryCanceled(err)) && r.Context().Err() == nil:
			// Our own insert deadline fired while the client is still there.
			// It binds when the store was slow before its lock_timeout applied
			// (pool acquisition, session setup) or when the request budget
			// ran out; pgx may report it as a deadline error or as SQLSTATE
			// 57014 (query_canceled). Nothing was committed (a commit-time
			// ambiguity is ErrCommitOutcomeUnknown, handled above, and the
			// store raises these only before its COMMIT), so this is
			// retryable.
			//
			// This discrimination assumes r.Context() is cancelled only by a
			// client disconnect: there is no per-route timeout middleware. If
			// one is added, a deadline error from it would look like our own
			// insert deadline here, so revisit.
			h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence",
				detail+": ran out of time before it could be saved")
			writeBusy(w, "the capture ran out of time before it could be saved; nothing was recorded; retry")
			return
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), isQueryCanceled(err):
			// The client went away between the collector returning and the
			// insert (a deadline error or query_canceled with a dead request
			// context is a disconnect, not our deadline). The store
			// raises a context error only before its COMMIT (the commit
			// runs detached and bounded), so the transaction rolled back
			// and this is the same "nothing was recorded" outcome as a
			// cancellation during collection.
			if isQueryCanceled(err) {
				err = context.Canceled // writeCaptureFailure maps context errors
			}
			h.writeCaptureFailure(w, r, user, c.row, err)
			return
		case err != nil:
			h.auditLog(r, user, ActionIncidentCapture, audit.ResultFailure, c.row.ClusterID, "incidentEvidence", detail)
			h.writeStoreFailure(w, "insert incident evidence", err)
			return
		}
	}
	resp := CaptureResponse{
		Completeness: report.Completeness, CollectedAt: report.CollectedAt, Sources: report.Sources,
		Collected: len(report.Items), Inserted: inserted, Deduplicated: len(rows) - inserted, Dropped: dropped,
	}
	h.auditLog(r, user, ActionIncidentCapture, audit.ResultSuccess, c.row.ClusterID, "incidentEvidence",
		fmt.Sprintf("%s: completeness %s, collected %d, inserted %d, deduplicated %d, dropped %d",
			detail, resp.Completeness, resp.Collected, resp.Inserted, resp.Deduplicated, resp.Dropped))
	httputil.WriteData(w, resp)
}

// writeCaptureFailure maps a Collector.Capture error. Nothing was persisted
// on any of these paths.
func (h *Handler) writeCaptureFailure(w http.ResponseWriter, r *http.Request, u *auth.User, row *store.IncidentRow, err error) {
	var unknown *UnknownSourceError
	switch {
	case errors.Is(err, ErrRemoteCaptureUnsupported):
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "evidence capture is supported on the local cluster only", ReasonRemoteCaptureUnsupported, nil)
	case errors.As(err, &unknown):
		httputil.WriteError(w, http.StatusBadRequest, "unknown evidence source", unknown.ID)
	case errors.Is(err, ErrInvalidCaptureRequest):
		httputil.WriteError(w, http.StatusBadRequest, "invalid capture request", err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		h.auditLog(r, u, ActionIncidentCapture, audit.ResultFailure, row.ClusterID, "incidentEvidence", "incident "+row.ID.String()+": cancelled")
		httputil.WriteError(w, http.StatusServiceUnavailable, "capture was cancelled before it completed; nothing was recorded", "")
	default:
		h.logger.Error("incident capture failed", "incidentId", row.ID, "error", err)
		h.auditLog(r, u, ActionIncidentCapture, audit.ResultFailure, row.ClusterID, "incidentEvidence", "incident "+row.ID.String())
		httputil.WriteError(w, http.StatusInternalServerError, "incident capture failed", "")
	}
}

// evidencePage reads one page of an incident's evidence and the caller's
// whole-incident counts through ONE memo (so the page and the counts agree
// and each scope costs one check). It writes the error response itself.
func (h *Handler) evidencePage(w http.ResponseWriter, r *http.Request, u *auth.User, incidentID uuid.UUID) (EvidencePage, string, bool) {
	limit, cursor, ok := pageQuery(w, r)
	if !ok {
		return EvidencePage{}, "", false
	}
	ctx := r.Context()
	rows, next, err := h.evidence.ListByIncident(ctx, incidentID, limit, cursor)
	if err != nil {
		h.writeStoreFailure(w, "list incident evidence", err)
		return EvidencePage{}, "", false
	}
	memo, cancel := h.newScopeMemo(ctx, u)
	defer cancel()
	counts, err := h.countEvidence(ctx, memo, incidentID)
	if err != nil {
		h.writeStoreFailure(w, "count incident evidence", err)
		return EvidencePage{}, "", false
	}
	visible, withheld, err := memo.filter(rows)
	if err != nil {
		h.logger.Error("incident evidence could not be decoded", "incidentId", incidentID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "incident evidence unavailable", "")
		return EvidencePage{}, "", false
	}
	return EvidencePage{Counts: counts, Evidence: visible, Withheld: withheld}, next, true
}

// HandleListEvidence returns one page of the incident's evidence the caller
// may read (owner or collaborator), with the same placeholders and counts
// as the detail read.
// GET /api/v1/incidents/{incidentID}/evidence?limit=&continue=
func (h *Handler) HandleListEvidence(w http.ResponseWriter, r *http.Request) {
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
		Data:     page,
		Metadata: &api.Metadata{Total: page.Counts.Visible, Continue: next},
	})
}
