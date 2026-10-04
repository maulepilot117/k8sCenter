package changes

// HTTP handlers for change receipts (Release E U29a). Together with
// redaction.go this is the Q1 authorization boundary: everything a receipt
// discloses to a caller other than the one who wrote it passes through
// authorizeReceiptRead (the envelope gate) and then redactObjects /
// redactChecks / redactOwnership (read-time re-authorization per object).
//
// Routes are registered in U29b; the handlers expect:
//
//	GET  /api/v1/changes                     HandleList
//	GET  /api/v1/changes/{id}                HandleGet
//	GET  /api/v1/changes/{id}/verification   HandleVerification
//	POST /api/v1/changes/ownership           HandleResolveOwnership
//
// Cluster access always goes through ClusterTargeter.TargetFor for the
// RECEIPT's cluster (never the request header's), so a remote receipt is
// re-authorized and verified against the cluster it changed, and only after
// mayReachCluster: a non-admin never reaches a remote cluster through a
// receipt.

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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitops"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// timeStamp is the time type the wire views use; an alias so redaction.go
// needs no time import of its own.
type timeStamp = time.Time

// receiptReader is the read slice of *store.ChangeReceiptStore the handlers
// use. Unexported, like receiptStore in service.go, so handler tests run
// against an in-memory fake; NewHandler takes the concrete store.
type receiptReader interface {
	Get(ctx context.Context, id uuid.UUID) (*store.ChangeReceipt, error)
	ListForOwner(ctx context.Context, p store.ReceiptQueryParams) ([]store.ChangeReceipt, int, error)
	GrantsFor(ctx context.Context, id uuid.UUID) ([]string, error)
}

// OwnershipResolver is the narrow view of *gitops.Handler this package needs,
// declared here so changes does not depend on the whole gitops handler.
type OwnershipResolver interface {
	ResolveOwnership(ctx context.Context, user *auth.User, clusterID string,
		dyn dynamic.Interface, refs []gitops.ObjectRef) ([]gitops.OwnershipResult, error)
}

// ClusterTargeter is the subset of *k8s.ClusterRouter the handlers use,
// mirroring yaml's clusterTargeter: an interface only so tests can inject a
// remote target, which cannot be built against a test server from outside
// package k8s. Production always assigns a *k8s.ClusterRouter.
type ClusterTargeter interface {
	TargetFor(ctx context.Context, clusterID, username string, groups []string) (*k8s.ClientPair, *k8s.TargetSchema, error)
}

// accessChecker is the one *resources.AccessChecker method this package
// calls. Unexported so a test can observe which cluster a check was sent to;
// NewHandler takes the concrete checker.
type accessChecker interface {
	CanAccessGroupResource(ctx context.Context, clusterID, username string, groups []string,
		verb, apiGroup, resource, namespace string) (bool, error)
}

// Request bounds.
const (
	// maxOwnershipRefs caps the objects one ownership request may ask about.
	// Above it is a 400, never a truncation.
	maxOwnershipRefs = 50
	// maxOwnershipBodyBytes caps the ownership request body.
	maxOwnershipBodyBytes = 1 << 20
	// clusterCallTimeout bounds one ownership resolution and one verification
	// pass. It sits under the 30s BFF proxy cap so the client sees this
	// server's 504 and not the proxy's, and it keeps a stalled remote cluster
	// from pinning the request goroutine for as long as the route allows.
	clusterCallTimeout = 20 * time.Second
	// maxListPage bounds ?page=. Offset pagination multiplies page by page
	// size; a page far beyond any receipt count is a client error, not a
	// store outage. 100k pages at the maximum page size is 20M receipts,
	// well past the 30-day retention.
	maxListPage = 100_000
)

// localGeneration is the generation TargetSchemaFor reports for the local
// cluster; it is what a local receipt recorded, so it is compared without a
// cluster call.
const localGeneration = "local"

// Handler serves change receipts and ownership resolution.
type Handler struct {
	service  *Service
	receipts receiptReader
	gitops   OwnershipResolver
	clusters ClusterTargeter
	access   accessChecker
	logger   *slog.Logger
	// clusterTimeout is clusterCallTimeout; a field so tests can shorten it.
	clusterTimeout time.Duration
}

// NewHandler builds the handler. receipts and access may be nil; the
// handlers then answer 503 (no database) and redact every object (no
// authorization source) respectively. gitops and clusters may be nil only
// when the corresponding features are unwired; the handlers that need them
// answer 503. Pass an untyped nil, not a nil *gitops.Handler.
func NewHandler(svc *Service, receipts *store.ChangeReceiptStore, g OwnershipResolver,
	clusters ClusterTargeter, access *resources.AccessChecker, logger *slog.Logger) *Handler {
	h := &Handler{service: svc, gitops: g, clusters: clusters, logger: logger, clusterTimeout: clusterCallTimeout}
	if receipts != nil {
		h.receipts = receipts
	}
	if access != nil {
		h.access = access
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

// newHandlerWith is the test seam: any reader, any access checker.
func newHandlerWith(svc *Service, receipts receiptReader, g OwnershipResolver,
	clusters ClusterTargeter, access accessChecker, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: svc, receipts: receipts, gitops: g, clusters: clusters,
		access: access, logger: logger, clusterTimeout: clusterCallTimeout}
}

// ---------------------------------------------------------------------------
// Wire types owned by the handlers (the per-object views live in redaction.go)
// ---------------------------------------------------------------------------

// ReceiptDetail is the GET /changes/{id} response: the envelope plus the
// redacted objects, stored verification checks and ownership. For a
// Secret-bearing receipt read by anyone but the owner, ContentDigest is
// blank: an unsalted digest of a bundle whose Secret rows are filtered is an
// offline confirmation oracle for low-entropy values.
type ReceiptDetail struct {
	ReceiptView
	// Access is how the caller qualified: "owner", "grantee" or "admin".
	Access            string              `json:"access"`
	Summary           ReceiptSummary      `json:"summary"`
	Objects           []ReceiptObjectView `json:"objects"`
	RedactedObjects   int                 `json:"redactedObjects"`
	Checks            []CheckView         `json:"checks"`
	RedactedChecks    int                 `json:"redactedChecks"`
	Ownership         []OwnershipView     `json:"ownership"`
	RedactedOwnership int                 `json:"redactedOwnership"`
}

// VerificationView is the GET /changes/{id}/verification response.
type VerificationView struct {
	State             store.VerificationState `json:"state"`
	Checks            []CheckView             `json:"checks"`
	RedactedChecks    int                     `json:"redactedChecks"`
	RetryAfterSeconds int                     `json:"retryAfterSeconds,omitempty"`
}

// OwnershipRequest is the POST /changes/ownership body. ClusterID is
// optional; when present it must equal the request's X-Cluster-ID.
type OwnershipRequest struct {
	ClusterID string               `json:"clusterId,omitempty"`
	Objects   []OwnershipObjectRef `json:"objects"`
}

// OwnershipObjectRef identifies one object to resolve. Version is optional;
// Resource is never accepted from the client (it is resolved through the
// target cluster's RESTMapper).
type OwnershipObjectRef struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// OwnershipResponse is the POST /changes/ownership response. Results are in
// request order, one per object.
type OwnershipResponse struct {
	ClusterID string                   `json:"clusterId"`
	Results   []gitops.OwnershipResult `json:"results"`
}

// ---------------------------------------------------------------------------
// Per-request access memo
// ---------------------------------------------------------------------------

type accessKey struct{ group, resource, namespace string }

// accessMemo memoizes `get` checks per (group, resource, namespace) for one
// request against one cluster, the way gitops/handler.go memoizes its own
// decisions. An access-check error is not a denial, but it must not render
// as an allowance: it is logged once, answered false, and every later
// uncached tuple is answered false without another call, so an unreachable
// cluster costs one failed dial per request rather than one per tuple.
type accessMemo struct {
	ctx       context.Context
	checker   accessChecker
	clusterID string
	user      *auth.User
	logger    *slog.Logger
	cache     map[accessKey]bool
	errored   bool
}

func (m *accessMemo) allowed(group, resource, namespace string) bool {
	k := accessKey{group, resource, namespace}
	if v, ok := m.cache[k]; ok {
		return v
	}
	if m.errored {
		return false
	}
	ok, err := m.checker.CanAccessGroupResource(m.ctx, m.clusterID,
		m.user.KubernetesUsername, m.user.KubernetesGroups, "get", group, resource, namespace)
	if err != nil {
		ok = false
		m.errored = true
		m.logger.Warn("change receipt access check failed; redacting every unchecked object",
			"cluster", m.clusterID, "user", m.user.ID, "error", err)
	}
	m.cache[k] = ok
	return ok
}

// objectAccessFor builds the request's objectAccess for the RECEIPT's
// cluster. With no access checker, or for a caller who may not reach the
// cluster, everything is hidden and no check is issued.
func (h *Handler) objectAccessFor(ctx context.Context, user *auth.User, clusterID string) objectAccess {
	if h.access == nil {
		h.logger.Warn("change receipts have no access checker; redacting every object")
		return denyAll
	}
	if !mayReachCluster(user, clusterID) {
		return denyAll
	}
	m := &accessMemo{ctx: ctx, checker: h.access, clusterID: clusterID, user: user,
		logger: h.logger, cache: map[accessKey]bool{}}
	return m.allowed
}

// ---------------------------------------------------------------------------
// Shared handler steps
// ---------------------------------------------------------------------------

// requireService is the shared opening of every handler: an authenticated
// caller and a receipt store.
func (h *Handler) requireService(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return nil, false
	}
	if h.service == nil || !h.service.Available() || h.receipts == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "change receipts require a database", "")
		return nil, false
	}
	return user, true
}

// parseReceiptID accepts a UUIDv4 and nothing else.
func parseReceiptID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.Version() != 4 {
		return uuid.Nil, false
	}
	return id, true
}

// loadAuthorized resolves {id}, loads the receipt and applies the envelope
// gate. A receipt the caller may not read is answered exactly like one that
// does not exist (404), so receipt ids are not enumerable. Grants are read
// only when owner and admin both fail.
func (h *Handler) loadAuthorized(w http.ResponseWriter, r *http.Request, user *auth.User) (*store.ChangeReceipt, ReadDecision, bool) {
	id, ok := parseReceiptID(chi.URLParam(r, "id"))
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "invalid receipt id", "receipt ids are UUIDv4")
		return nil, ReadDenied, false
	}
	ctx := r.Context()
	rec, err := h.receipts.Get(ctx, id)
	if err != nil {
		h.writeStoreError(w, "read change receipt", err)
		return nil, ReadDenied, false
	}
	if rec == nil {
		h.writeNotFound(w)
		return nil, ReadDenied, false
	}
	decision := authorizeReceiptRead(user, rec, nil)
	if decision == ReadDenied {
		grants, err := h.receipts.GrantsFor(ctx, id)
		if err != nil {
			h.writeStoreError(w, "read change receipt grants", err)
			return nil, ReadDenied, false
		}
		decision = authorizeReceiptRead(user, rec, grants)
	}
	if decision == ReadDenied {
		h.logger.Info("change receipt read denied", "receiptId", id, "user", user.ID)
		h.writeNotFound(w)
		return nil, ReadDenied, false
	}
	return rec, decision, true
}

func (h *Handler) writeNotFound(w http.ResponseWriter) {
	httputil.WriteError(w, http.StatusNotFound, "change receipt not found", "")
}

func (h *Handler) writeStoreError(w http.ResponseWriter, op string, err error) {
	h.logger.Error("change receipt store error", "op", op, "error", err)
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
		"change receipt store unavailable", ReasonReceiptStoreUnavailable, nil)
}

// writeTargetError reports a TargetFor failure the way gitops does: a local
// failure is this server's 500, a remote one is classified by
// httputil.WriteRemoteError.
func (h *Handler) writeTargetError(w http.ResponseWriter, clusterID string, err error) {
	if k8s.IsLocalClusterID(clusterID) {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create kubernetes client", err.Error())
		return
	}
	httputil.WriteRemoteError(w, err)
}

// requireClusterAccess gates live access to clusterID: a wired router and
// mayReachCluster. The 403 message is middleware.ClusterContext's, so a
// client sees the same refusal whichever gate it hit.
func (h *Handler) requireClusterAccess(w http.ResponseWriter, user *auth.User, clusterID string) bool {
	if h.clusters == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "cluster routing is not configured", "")
		return false
	}
	if !mayReachCluster(user, clusterID) {
		httputil.WriteError(w, http.StatusForbidden, "admin role required for remote cluster access", "")
		return false
	}
	return true
}

// currentGeneration resolves the target's generation for
// ReceiptView.TargetGenerationChanged. Best effort: "" (flag stays false)
// when it cannot be resolved, never an error. A local receipt needs no
// cluster call; a remote one is resolved only for a caller who may reach it.
func (h *Handler) currentGeneration(ctx context.Context, user *auth.User, clusterID string) string {
	if k8s.IsLocalClusterID(clusterID) {
		return localGeneration
	}
	if h.clusters == nil || !mayReachCluster(user, clusterID) {
		return ""
	}
	_, target, err := h.clusters.TargetFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil || target == nil {
		h.logger.Debug("change receipt: target generation unavailable", "cluster", clusterID, "error", err)
		return ""
	}
	return target.Generation
}

// storedOwnership decodes the receipt's preview-time ownership snapshot.
// Undecodable JSON (there should be none) is treated as empty and logged.
func (h *Handler) storedOwnership(r *store.ChangeReceipt) []gitops.OwnershipResult {
	if len(r.Ownership) == 0 {
		return nil
	}
	var out []gitops.OwnershipResult
	if err := json.Unmarshal(r.Ownership, &out); err != nil {
		h.logger.Warn("change receipt: stored ownership is not a []OwnershipResult", "receiptId", r.ID, "error", err)
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// HandleGet returns one receipt: envelope, redacted objects, redacted stored
// verification checks and redacted ownership.
// GET /api/v1/changes/{id}
func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	rec, decision, ok := h.loadAuthorized(w, r, user)
	if !ok {
		return
	}
	ctx := r.Context()
	allowed := h.objectAccessFor(ctx, user, rec.ClusterID)
	objects, redactedObjects := redactObjects(rec.Objects, rec.ContainsSecret, allowed)
	checks, redactedChecks := redactChecks(storedVerdict(rec).Checks, allowed)
	ownership, redactedOwnership := redactOwnership(h.storedOwnership(rec), rec.Objects, objects, allowed)

	view := NewReceiptView(rec, h.currentGeneration(ctx, user, rec.ClusterID))
	if rec.ContainsSecret && decision != ReadAsOwner {
		view.ContentDigest = ""
	}
	httputil.WriteData(w, ReceiptDetail{
		ReceiptView:       view,
		Access:            decision.String(),
		Summary:           summarize(rec),
		Objects:           objects,
		RedactedObjects:   redactedObjects,
		Checks:            checks,
		RedactedChecks:    redactedChecks,
		Ownership:         ownership,
		RedactedOwnership: redactedOwnership,
	})
}

// HandleVerification performs one verification pass for the receipt under
// the caller's identity on the RECEIPT's cluster and returns the redacted
// checks. For the owner the pass is VerifyOnce and the verdict is persisted:
// a GET that writes is intended (plan D6: stateless polling, no background
// watcher; the write is derived state, not an operator action, so it is not
// audited). Every other reader gets the same evaluation live and nothing is
// stored. A receipt whose verification is already final returns the stored
// verdict without touching the cluster, for every reader.
//
// A non-admin reading a remote receipt whose verdict is not yet final is
// answered 403 (middleware.ClusterContext's rule), so the status for that
// caller changes once the owner's verdict freezes: clients must treat the
// 403 as the cluster-access gate, not as a receipt permission failure.
// GET /api/v1/changes/{id}/verification
func (h *Handler) HandleVerification(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	rec, decision, ok := h.loadAuthorized(w, r, user)
	if !ok {
		return
	}
	ctx := r.Context()

	var res *VerificationResult
	if rec.VerificationState.IsFinal() {
		res = storedVerdict(rec)
	} else {
		if !h.requireClusterAccess(w, user, rec.ClusterID) {
			return
		}
		vctx, cancel := context.WithTimeout(ctx, h.clusterTimeout)
		defer cancel()
		pair, _, err := h.clusters.TargetFor(vctx, rec.ClusterID, user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			h.writeTargetError(w, rec.ClusterID, err)
			return
		}
		res, err = h.service.VerifyOnce(vctx, rec, pair.Dynamic, VerifyOptions{Persist: mayPersistVerdict(decision)})
		if err == nil {
			// A pass cut short by cancellation or the budget is not a verdict:
			// its reads were aborted. Report the context error for every
			// reader alike instead of a result built from them.
			err = vctx.Err()
		}
		if err != nil {
			h.writeVerifyError(w, rec.ID, err)
			return
		}
	}

	checks, redacted := redactChecks(res.Checks, h.objectAccessFor(ctx, user, rec.ClusterID))
	if res.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(res.RetryAfterSeconds))
	}
	httputil.WriteData(w, VerificationView{
		State: res.State, Checks: checks, RedactedChecks: redacted, RetryAfterSeconds: res.RetryAfterSeconds,
	})
}

// writeVerifyError maps a verification pass's errors identically for every
// reader. Context errors are matched first, wrapped or not: a
// cancelled client gets nothing (there is no one to write to), the request
// budget is a 504. Nothing about the receipt or the cluster is echoed.
func (h *Handler) writeVerifyError(w http.ResponseWriter, id uuid.UUID, err error) {
	var unavailable *StoreUnavailableError
	switch {
	case errors.Is(err, context.Canceled):
		h.logger.Debug("change receipt verification abandoned: request cancelled", "receiptId", id)
	case errors.Is(err, context.DeadlineExceeded):
		httputil.WriteError(w, http.StatusGatewayTimeout, "verification timed out", "")
	case errors.As(err, &unavailable):
		h.logger.Error("change receipt verification: store unavailable", "receiptId", id, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"change receipt store unavailable", ReasonReceiptStoreUnavailable, nil)
	case errors.Is(err, ErrInvalidRequest):
		httputil.WriteError(w, http.StatusBadRequest, "verification cannot run for this receipt", "")
	default:
		h.logger.Error("change receipt verification failed", "receiptId", id, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "verification failed", "")
	}
}

// HandleList returns the caller's OWN receipts, newest first, as envelope
// views. Grant rows are not consulted: a shared receipt is reachable by id
// only. Pagination is ?page=&pageSize=; page is bounded by maxListPage (400
// above it), page size by the store's bounds; both are echoed in metadata
// with the total.
// GET /api/v1/changes
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, ok := intQuery(q.Get("page"))
	if !ok || page > maxListPage {
		httputil.WriteError(w, http.StatusBadRequest, "invalid page",
			fmt.Sprintf("page must be an integer between 1 and %d", maxListPage))
		return
	}
	pageSize, ok := intQuery(q.Get("pageSize"))
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "invalid pageSize", "pageSize must be an integer")
		return
	}
	params := store.ReceiptQueryParams{OwnerID: user.ID, Page: page, PageSize: pageSize}
	params.Normalize()

	ctx := r.Context()
	rows, total, err := h.receipts.ListForOwner(ctx, params)
	if err != nil {
		h.writeStoreError(w, "list change receipts", err)
		return
	}

	// One generation lookup per distinct cluster, not per row.
	generations := map[string]string{}
	items := make([]ReceiptView, 0, len(rows))
	for i := range rows {
		rec := &rows[i]
		gen, seen := generations[rec.ClusterID]
		if !seen {
			gen = h.currentGeneration(ctx, user, rec.ClusterID)
			generations[rec.ClusterID] = gen
		}
		items = append(items, NewReceiptView(rec, gen))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     items,
		Metadata: &api.Metadata{Total: total, Page: params.Page, PageSize: params.PageSize},
	})
}

// intQuery parses an optional integer query value; absent is 0.
func intQuery(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// HandleResolveOwnership answers which GitOps controller manages each listed
// object on the request's cluster (X-Cluster-ID), under the caller's own
// identity. Resource and Version are resolved through the TARGET cluster's
// RESTMapper so the live-object hints are read; an unmappable kind is still
// passed on and the resolver returns its own verdict for it. The remote
// admin gate is applied here as well as by middleware.ClusterContext, so the
// handler is safe however U29b mounts it.
// POST /api/v1/changes/ownership
func (h *Handler) HandleResolveOwnership(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	if h.gitops == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ownership resolution is not configured", "")
		return
	}
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)
	if !h.requireClusterAccess(w, user, clusterID) {
		return
	}

	var req OwnershipRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxOwnershipBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large", "")
			return
		}
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	if req.ClusterID != "" && k8s.NormalizedClusterID(req.ClusterID) != k8s.NormalizedClusterID(clusterID) {
		httputil.WriteError(w, http.StatusBadRequest, "clusterId does not match X-Cluster-ID", "")
		return
	}
	switch {
	case len(req.Objects) == 0:
		httputil.WriteError(w, http.StatusBadRequest, "objects is required", "")
		return
	case len(req.Objects) > maxOwnershipRefs:
		httputil.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("at most %d objects per request", maxOwnershipRefs), "")
		return
	}
	for i, o := range req.Objects {
		if o.Kind == "" || o.Name == "" {
			httputil.WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("objects[%d]: kind and name are required", i), "")
			return
		}
	}

	pair, target, err := h.clusters.TargetFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeTargetError(w, clusterID, err)
		return
	}
	refs := make([]gitops.ObjectRef, 0, len(req.Objects))
	for _, o := range req.Objects {
		ref := gitops.ObjectRef{
			ClusterID: pair.ClusterID, Group: o.Group, Version: o.Version,
			Kind: o.Kind, Namespace: o.Namespace, Name: o.Name,
		}
		if target != nil && target.Mapper != nil {
			var versions []string
			if o.Version != "" {
				versions = []string{o.Version}
			}
			if m, err := target.Mapper.RESTMapping(schema.GroupKind{Group: o.Group, Kind: o.Kind}, versions...); err == nil {
				ref.Resource = m.Resource.Resource
				ref.Version = m.Resource.Version
			}
		}
		refs = append(refs, ref)
	}

	rctx, cancel := context.WithTimeout(ctx, h.clusterTimeout)
	defer cancel()
	results, err := h.gitops.ResolveOwnership(rctx, user, pair.ClusterID, pair.Dynamic, refs)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			// The client went away; nothing useful can be written.
			h.logger.Debug("ownership resolution abandoned: request cancelled", "cluster", pair.ClusterID)
			return
		case errors.Is(err, context.DeadlineExceeded):
			httputil.WriteError(w, http.StatusGatewayTimeout, "ownership resolution timed out", "")
		default:
			h.logger.Error("ownership resolution failed", "cluster", pair.ClusterID, "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "ownership resolution failed", "")
		}
		return
	}
	if results == nil {
		results = []gitops.OwnershipResult{}
	}
	httputil.WriteData(w, OwnershipResponse{ClusterID: pair.ClusterID, Results: results})
}
