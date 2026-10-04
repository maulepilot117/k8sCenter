package changes

// HTTP handlers for change receipts (Release E U29a). This file is the Q1
// authorization and redaction boundary: everything a receipt discloses to a
// caller other than the one who wrote it passes through authorizeReceiptRead
// (the envelope gate) and then redactObjects / redactChecks /
// redactOwnership (read-time re-authorization per object). The pure
// functions are kept free of HTTP and I/O so redaction_fuzz_test.go can
// drive them directly.
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
// re-authorized and verified against the cluster it changed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

// Limits of HandleResolveOwnership.
const (
	// maxOwnershipRefs caps the objects one request may ask about. Above it is
	// a 400, never a truncation.
	maxOwnershipRefs = 50
	// maxOwnershipBodyBytes caps the request body.
	maxOwnershipBodyBytes = 1 << 20
	// ownershipResolveTimeout bounds one resolution. It sits under the 30s BFF
	// proxy cap so the client sees this server's 504 and not the proxy's.
	ownershipResolveTimeout = 20 * time.Second
)

// localGeneration is the generation TargetSchemaFor reports for the local
// cluster; it is what a local receipt recorded, so it is compared without a
// cluster call.
const localGeneration = "local"

// Handler serves change receipts and ownership resolution.
type Handler struct {
	service          *Service
	receipts         receiptReader
	gitops           OwnershipResolver
	clusters         ClusterTargeter
	access           accessChecker
	logger           *slog.Logger
	ownershipTimeout time.Duration
}

// NewHandler builds the handler. receipts and access may be nil; the
// handlers then answer 503 (no database) and redact every object (no
// authorization source) respectively. gitops and clusters may be nil only
// when the corresponding features are unwired; the handlers that need them
// answer 503. Pass an untyped nil, not a nil *gitops.Handler.
func NewHandler(svc *Service, receipts *store.ChangeReceiptStore, g OwnershipResolver,
	clusters ClusterTargeter, access *resources.AccessChecker, logger *slog.Logger) *Handler {
	h := &Handler{service: svc, gitops: g, clusters: clusters, logger: logger, ownershipTimeout: ownershipResolveTimeout}
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
		access: access, logger: logger, ownershipTimeout: ownershipResolveTimeout}
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// Redaction reasons carried by ReceiptObjectView.Reason and
// CheckView.RedactionReason. Stable wire values.
const (
	// RedactionForbidden: the caller does not currently hold `get` on the
	// object's resource in its namespace on the receipt's cluster, or that
	// could not be determined (an access-check error fails closed).
	RedactionForbidden = "forbidden"
	// RedactionSecretFiltered: the object is a Secret and the caller does not
	// currently hold `get` on secrets in its namespace.
	RedactionSecretFiltered = "secret-filtered"
)

// ReceiptObjectView is the wire form of one recorded object. Every field
// except Index, Redacted and Reason is dropped when Redacted is true.
type ReceiptObjectView struct {
	Index     int    `json:"index"`
	Redacted  bool   `json:"redacted,omitempty"`
	Reason    string `json:"reason,omitempty"` // RedactionForbidden | RedactionSecretFiltered
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
	Action    string `json:"action,omitempty"`
	// Error is the stored error text. Never emitted for a Secret-bearing
	// receipt or a Secret object: admission and validation messages echo
	// submitted values. ErrorClass is emitted instead (and alongside Error
	// when the stored text carries a recognizable class prefix).
	Error      string `json:"error,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
}

// CheckView is the wire form of one verification check. A check on an object
// the caller may not read keeps only its identity-free fields (checkId,
// status, severity, reason, observedAt) and says so; message, detail,
// remediation, source and evidence all name the object and are dropped.
type CheckView struct {
	CheckID         string            `json:"checkId"`
	Status          CheckStatus       `json:"status"`
	Severity        Severity          `json:"severity"`
	Reason          string            `json:"reason"`
	Redacted        bool              `json:"redacted,omitempty"`
	RedactionReason string            `json:"redactionReason,omitempty"`
	Message         string            `json:"message,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	Remediation     string            `json:"remediation,omitempty"`
	Source          *SourceRef        `json:"source,omitempty"`
	Evidence        map[string]string `json:"evidence,omitempty"`
	ObservedAt      time.Time         `json:"observedAt"`
}

// OwnershipView is one stored ownership entry after read-time
// re-authorization. RedactedApps counts confirming applications removed
// because the caller may not currently get them.
type OwnershipView struct {
	gitops.OwnershipResult
	RedactedApps int `json:"redactedApps,omitempty"`
}

// ReceiptSummary is the historical outcome count of a receipt, computed from
// every recorded object BEFORE redaction: counts leak no identity and the
// record must not change with the reader. NotRecorded is the number of
// submitted documents without a recorded outcome.
type ReceiptSummary struct {
	Total       int `json:"total"`
	Created     int `json:"created"`
	Configured  int `json:"configured"`
	Unchanged   int `json:"unchanged"`
	Failed      int `json:"failed"`
	NotRecorded int `json:"notRecorded"`
}

// ReceiptDetail is the GET /changes/{id} response: the envelope plus the
// redacted objects, stored verification checks and ownership.
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
// Pure authorization: the Q1 policy
// ---------------------------------------------------------------------------

// ReadDecision is the envelope-level gate: ownership, explicit grant, admin.
type ReadDecision int

const (
	ReadDenied ReadDecision = iota
	ReadAsOwner
	ReadAsGrantee
	ReadAsAdmin
)

// String is the wire value of ReceiptDetail.Access.
func (d ReadDecision) String() string {
	switch d {
	case ReadAsOwner:
		return "owner"
	case ReadAsGrantee:
		return "grantee"
	case ReadAsAdmin:
		return "admin"
	default:
		return ""
	}
}

// authorizeReceiptRead decides envelope visibility only. It never inspects
// object contents; per-object visibility is redactObjects' job. A caller
// without an identity is denied whatever the grants say, so an empty grantee
// id can never match.
func authorizeReceiptRead(user *auth.User, r *store.ChangeReceipt, grants []string) ReadDecision {
	if user == nil || r == nil || user.ID == "" {
		return ReadDenied
	}
	if r.OwnerID == user.ID {
		return ReadAsOwner
	}
	if auth.IsAdmin(user) {
		return ReadAsAdmin
	}
	for _, g := range grants {
		if g == user.ID {
			return ReadAsGrantee
		}
	}
	return ReadDenied
}

// objectAccess reports whether the caller CURRENTLY holds `get` on
// (group, resource, namespace) in the receipt's cluster. Any doubt is false.
type objectAccess func(group, resource, namespace string) bool

// denyAll is the objectAccess used when there is no authorization source.
func denyAll(string, string, string) bool { return false }

// objectRedaction returns why an object must be hidden from the caller, or ""
// when it may be shown. A Secret needs `get` on core secrets in its namespace
// first (RedactionSecretFiltered); every object then needs `get` on its own
// resource (RedactionForbidden). An object without a recorded resource cannot
// be re-authorized and is hidden.
func objectRedaction(kind, group, resource, namespace string, allowed objectAccess) string {
	if allowed == nil {
		allowed = denyAll
	}
	if isSecretKind(kind) && !allowed("", "secrets", namespace) {
		return RedactionSecretFiltered
	}
	if resource == "" || !allowed(group, resource, namespace) {
		return RedactionForbidden
	}
	return ""
}

// redactObjects applies Q1's read-time re-authorization. allowed reports the
// caller's CURRENT k8s authorization; containsSecret triggers the stricter
// Secret filtering. A hidden object keeps ONLY its index and the reason. The
// output has one view per input, in input order.
func redactObjects(objs []store.ReceiptObject, containsSecret bool, allowed objectAccess) ([]ReceiptObjectView, int) {
	out := make([]ReceiptObjectView, 0, len(objs))
	redacted := 0
	for _, o := range objs {
		if reason := objectRedaction(o.Kind, o.Group, o.Resource, o.Namespace, allowed); reason != "" {
			out = append(out, ReceiptObjectView{Index: o.Index, Redacted: true, Reason: reason})
			redacted++
			continue
		}
		v := ReceiptObjectView{
			Index: o.Index, Group: o.Group, Version: o.Version, Resource: o.Resource,
			Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID, Action: o.Action,
		}
		if o.Error != "" {
			v.ErrorClass = errorClassOf(o.Error)
			if containsSecret || isSecretKind(o.Kind) {
				// Never the text, whatever the store holds. Classification is
				// derived here so it does not depend on the store's own
				// sanitizing having run.
				if v.ErrorClass == "" {
					v.ErrorClass = ErrorClassOther
				}
			} else {
				v.Error = o.Error
			}
		}
		out = append(out, v)
	}
	return out, redacted
}

// errorClassOf recognizes the "<class>: ..." prefix sanitizedError writes and
// returns the class; any other text is unclassified ("").
func errorClassOf(errText string) string {
	class, _, ok := strings.Cut(errText, ":")
	if !ok {
		return ""
	}
	switch class {
	case ErrorClassConflict, ErrorClassForbidden, ErrorClassInvalid, ErrorClassNotFound, ErrorClassOther:
		return class
	}
	return ""
}

// redactChecks applies the per-object rule to verification checks: a check's
// Source names the object it observed and its Evidence maps Kind/Name to a
// UID, so a check on a hidden object is reduced to a stub.
func redactChecks(checks []CheckResult, allowed objectAccess) ([]CheckView, int) {
	out := make([]CheckView, 0, len(checks))
	redacted := 0
	for _, c := range checks {
		v := CheckView{CheckID: c.CheckID, Status: c.Status, Severity: c.Severity, Reason: c.Reason, ObservedAt: c.ObservedAt}
		src := c.Source
		if reason := objectRedaction(src.Kind, src.Group, src.Resource, src.Namespace, allowed); reason != "" {
			v.Redacted = true
			v.RedactionReason = reason
			redacted++
		} else {
			v.Message = c.Message
			v.Detail = c.Detail
			v.Remediation = c.Remediation
			v.Source = &src
			v.Evidence = c.Evidence
		}
		out = append(out, v)
	}
	return out, redacted
}

// objectKey identifies an object the way ownership results do: without
// resource, version or uid, which the ownership path may leave empty.
type objectKey struct{ group, kind, namespace, name string }

// redactOwnership keeps a stored ownership entry only when its object is
// among the objects the caller may currently read, and within it only the
// confirming applications the caller may currently get (Argo Applications,
// Flux Kustomizations and HelmReleases are checked by their own GVR in their
// namespace). Evidence naming a removed application goes with it. The rule
// is the same for owner, grantee and admin: an application the caller can no
// longer list is not disclosed through a receipt they wrote earlier.
func redactOwnership(stored []gitops.OwnershipResult, visible []ReceiptObjectView, allowed objectAccess) ([]OwnershipView, int) {
	if allowed == nil {
		allowed = denyAll
	}
	shown := make(map[objectKey]bool, len(visible))
	for _, v := range visible {
		if !v.Redacted {
			shown[objectKey{v.Group, v.Kind, v.Namespace, v.Name}] = true
		}
	}
	out := make([]OwnershipView, 0, len(stored))
	redacted := 0
	for _, res := range stored {
		o := res.Object
		if !shown[objectKey{o.Group, o.Kind, o.Namespace, o.Name}] {
			redacted++
			continue
		}
		v := OwnershipView{OwnershipResult: res}
		v.Apps = nil
		kept := map[string]bool{}
		for _, app := range res.Apps {
			group, resource, ok := appResource(app.Kind)
			if !ok || !allowed(group, resource, app.Namespace) {
				v.RedactedApps++
				continue
			}
			v.Apps = append(v.Apps, app)
			kept[app.AppID] = true
		}
		v.Evidence = nil
		for _, ev := range res.Evidence {
			if ev.AppID == "" || kept[ev.AppID] {
				v.Evidence = append(v.Evidence, ev)
			}
		}
		out = append(out, v)
	}
	return out, redacted
}

// appResource maps a confirming application's kind to the (group, resource)
// its `get` check runs against. An unknown kind cannot be re-authorized.
func appResource(kind string) (group, resource string, ok bool) {
	switch kind {
	case "Application":
		return gitops.ArgoApplicationGVR.Group, gitops.ArgoApplicationGVR.Resource, true
	case "Kustomization":
		return gitops.FluxKustomizationGVR.Group, gitops.FluxKustomizationGVR.Resource, true
	case "HelmRelease":
		return gitops.FluxHelmReleaseGVR.Group, gitops.FluxHelmReleaseGVR.Resource, true
	}
	return "", "", false
}

// summarize counts every recorded object, before redaction.
func summarize(r *store.ChangeReceipt) ReceiptSummary {
	s := ReceiptSummary{Total: r.DocumentCount}
	for _, o := range r.Objects {
		switch o.Action {
		case ActionCreated:
			s.Created++
		case ActionConfigured:
			s.Configured++
		case ActionUnchanged:
			s.Unchanged++
		default:
			s.Failed++
		}
	}
	if n := r.DocumentCount - len(r.Objects); n > 0 {
		s.NotRecorded = n
	}
	return s
}

// ---------------------------------------------------------------------------
// Per-request access memo
// ---------------------------------------------------------------------------

type accessKey struct{ group, resource, namespace string }

// accessMemo memoizes `get` checks per (group, resource, namespace) for one
// request against one cluster, the way gitops/handler.go memoizes its own
// decisions. An access-check error is not a denial, but it must not render
// as an allowance: it is logged once and answered false.
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
	ok, err := m.checker.CanAccessGroupResource(m.ctx, m.clusterID,
		m.user.KubernetesUsername, m.user.KubernetesGroups, "get", group, resource, namespace)
	if err != nil {
		ok = false
		if !m.errored {
			m.errored = true
			m.logger.Warn("change receipt access check failed; redacting",
				"cluster", m.clusterID, "user", m.user.ID, "error", err)
		}
	}
	m.cache[k] = ok
	return ok
}

// objectAccessFor builds the request's objectAccess for the RECEIPT's
// cluster. With no access checker everything is hidden. A non-admin caller is
// never checked against a remote cluster (middleware.ClusterContext requires
// the admin role for remote access; a receipt read must not bypass it), so
// their remote receipts show redacted objects only.
func (h *Handler) objectAccessFor(ctx context.Context, user *auth.User, clusterID string) objectAccess {
	if h.access == nil {
		h.logger.Warn("change receipts have no access checker; redacting every object")
		return denyAll
	}
	if !k8s.IsLocalClusterID(clusterID) && !auth.IsAdmin(user) {
		return denyAll
	}
	m := &accessMemo{ctx: ctx, checker: h.access, clusterID: clusterID, user: user,
		logger: h.logger, cache: map[accessKey]bool{}}
	return m.allowed
}

// ---------------------------------------------------------------------------
// Handlers
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

// requireClusterAccess enforces middleware.ClusterContext's rule for a
// receipt's cluster: live access to a remote cluster needs the admin role.
func (h *Handler) requireClusterAccess(w http.ResponseWriter, user *auth.User, clusterID string) bool {
	if h.clusters == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "cluster routing is not configured", "")
		return false
	}
	if !k8s.IsLocalClusterID(clusterID) && !auth.IsAdmin(user) {
		httputil.WriteError(w, http.StatusForbidden, "admin role required for remote cluster access", "")
		return false
	}
	return true
}

// currentGeneration resolves the target's generation for
// ReceiptView.TargetGenerationChanged. Best effort: "" (flag stays false)
// when it cannot be resolved, never an error. A local receipt needs no
// cluster call; a remote one is resolved only for an admin.
func (h *Handler) currentGeneration(ctx context.Context, user *auth.User, clusterID string) string {
	if k8s.IsLocalClusterID(clusterID) {
		return localGeneration
	}
	if h.clusters == nil || !auth.IsAdmin(user) {
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
	ownership, redactedOwnership := redactOwnership(h.storedOwnership(rec), objects, allowed)

	httputil.WriteData(w, ReceiptDetail{
		ReceiptView:       NewReceiptView(rec, h.currentGeneration(ctx, user, rec.ClusterID)),
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
// checks. For the owner or an admin the pass is VerifyOnce and the verdict is
// persisted: a GET that writes is intended (plan D6: stateless polling, no
// background watcher). A grantee gets the same evaluation live and nothing is
// stored. A receipt whose verification is already final returns the stored
// verdict without touching the cluster.
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
		pair, _, err := h.clusters.TargetFor(ctx, rec.ClusterID, user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			h.writeTargetError(w, rec.ClusterID, err)
			return
		}
		res, err = h.verify(ctx, rec, pair.Dynamic, mayPersistVerdict(decision))
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

// mayPersistVerdict is the write side of the Q1 policy: only the receipt's
// owner or an admin may record a verdict. A grantee gets a live evaluation
// under their own identity that is never stored, so a grantee's
// read_forbidden can never overwrite what the owner would have seen.
func mayPersistVerdict(d ReadDecision) bool {
	return d == ReadAsOwner || d == ReadAsAdmin
}

// verify runs one evaluation pass over the receipt's cluster. With persist
// it is VerifyOnce (plan D6); without it the same rules are applied and the
// result is returned without touching the store. The non-persisting branch
// mirrors VerifyOnce's evaluation step and is replaced by VerifyOnce's own
// non-persisting option when the service grows one.
func (h *Handler) verify(ctx context.Context, rec *store.ChangeReceipt, dyn dynamic.Interface, persist bool) (*VerificationResult, error) {
	if persist {
		return h.service.VerifyOnce(ctx, rec, dyn)
	}
	if dyn == nil {
		return nil, fmt.Errorf("%w: dynamic client is required", ErrInvalidRequest)
	}
	if rec.CompletedAt == nil || !rec.State.IsTerminal() {
		return &VerificationResult{State: store.VerifyPending, Checks: []CheckResult{}}, nil
	}
	now := h.service.now()
	checks := make([]CheckResult, 0, len(rec.Objects))
	for _, o := range rec.Objects {
		if o.Action == ActionFailed {
			continue
		}
		checks = append(checks, h.service.verifyObject(ctx, dyn, rec.ClusterID, o, now))
	}
	if now.Sub(*rec.CompletedAt) > verificationWindow {
		for i := range checks {
			if isRetryable(checks[i]) {
				checks[i] = freezeExpired(checks[i])
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res := &VerificationResult{State: aggregateVerification(checks), Checks: checks}
	if res.State == store.VerifyVerifying {
		res.RetryAfterSeconds = verifyRetryAfterSeconds
	}
	return res, nil
}

// writeVerifyError maps VerifyOnce's errors. Nothing about the receipt or
// the cluster is echoed.
func (h *Handler) writeVerifyError(w http.ResponseWriter, id uuid.UUID, err error) {
	var unavailable *StoreUnavailableError
	switch {
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
// only. Pagination is ?page=&pageSize=, normalized by the store's bounds and
// echoed in metadata with the total.
// GET /api/v1/changes
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, ok := intQuery(q.Get("page"))
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "invalid page", "page must be an integer")
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
// passed on and the resolver returns its own verdict for it.
// POST /api/v1/changes/ownership
func (h *Handler) HandleResolveOwnership(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireService(w, r)
	if !ok {
		return
	}
	if h.gitops == nil || h.clusters == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ownership resolution is not configured", "")
		return
	}
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)

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

	rctx, cancel := context.WithTimeout(ctx, h.ownershipTimeout)
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
