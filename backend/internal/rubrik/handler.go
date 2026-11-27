package rubrik

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/internal/urlorigin"
)

// Request limits for the admin endpoints.
const (
	maxRequestBytes   = 64 << 10
	maxTenantURLBytes = 2048
	maxClientIDBytes  = 512
	maxSecretBytes    = 8192
	maxClusterIDBytes = 128
	// adminRSCTimeout bounds an admin request's own RSC call (the cluster
	// list for the mapping picker and the check before a mapping is saved).
	adminRSCTimeout = 30 * time.Second
)

// Audit resource kinds. Entries never carry the tenant URL or the secret.
const (
	auditKindConnection = "rubrik-connection"
	auditKindMapping    = "rubrik-mapping"
)

// Cluster states the status endpoint reports for the selected cluster. An
// admin sees why a cluster is not set up; anyone else sees one neutral state
// (R22), so status reveals nothing about the connection.
const (
	StateMapped         = "mapped"
	StateNotConfigured  = "not_configured"
	StateNotMapped      = "not_mapped"
	StateMappingInvalid = "mapping_invalid"
	StateNotSetUp       = "not_set_up"
	StateAdminOnly      = "admin_only"
	StateUnavailable    = "unavailable"
)

// Connection health values (R3). The poller records contact outcomes on the
// connection row; until it has, a configured connection is "unknown".
const (
	HealthNotConfigured  = "not_configured"
	HealthSecretRequired = "secret_required"
	HealthOK             = "ok"
	HealthAuthFailed     = "auth_failed"
	HealthUnreachable    = "unreachable"
	HealthFailing        = "failing"
	HealthUnknown        = "unknown"
	HealthUnavailable    = "unavailable"
)

// AdminStore is the part of store.RubrikStore the handler reads and writes.
type AdminStore interface {
	GetConnection(ctx context.Context, id string) (store.RubrikConnection, store.RubrikCredentials, error)
	UpsertConnection(ctx context.Context, id string, w store.RubrikConnectionWrite) (store.RubrikConnection, error)
	SetMapping(ctx context.Context, w store.RubrikMappingWrite) (store.RubrikClusterMapping, error)
	GetMapping(ctx context.Context, clusterID string) (store.RubrikClusterMapping, error)
	ListMappings(ctx context.Context, connectionID string) ([]store.RubrikClusterMapping, error)
	DeleteMapping(ctx context.Context, clusterID string) error
}

// ClusterLister lists RSC's Kubernetes clusters; *Operations implements it.
type ClusterLister interface {
	Clusters(ctx context.Context) ([]Cluster, error)
}

// TokenFlusher drops a cached RSC token; *Client implements it.
type TokenFlusher interface {
	Flush()
}

// HandlerDeps are the handler's collaborators. Every field except Logger,
// Probe, ValidateURL, Operations, Writer and Restorer is required, and none
// may be a typed nil.
type HandlerDeps struct {
	Store    AdminStore
	Clusters ClusterRecords
	Service  *Service
	Access   *Access
	RSC      ClusterLister
	Client   TokenFlusher
	Audit    audit.Logger
	Logger   *slog.Logger
	// ConnectionID is store.RubrikDefaultConnectionID in production.
	ConnectionID string
	// Probe checks credentials before a save; nil means Probe.
	Probe func(ctx context.Context, tenantURL, clientID, clientSecret string) error
	// ValidateURL refuses a tenant URL that resolves to a private or reserved
	// address; nil means k8s.ValidateRemoteURLContext.
	ValidateURL func(ctx context.Context, raw string) error
	// Operations records the writes; nil means Store, when it implements
	// OperationStore (*store.RubrikStore does).
	Operations OperationStore
	// Writer sends the writes to RSC; nil means RSC, when it implements
	// RSCWriter (*Operations does). With either missing the write and
	// operation endpoints answer 503.
	Writer RSCWriter
	// Restorer sends restores; nil means RSC, when it implements
	// RSCRestorer (*Operations does). Without it restore answers 503.
	Restorer RSCRestorer
	// SuggestedTenantURL is Helm's rubrik.tenantUrl: admins see it as
	// connection.suggestedTenantUrl while no connection is saved, so the
	// settings page can pre-fill it. It is never stored. "" means none.
	SuggestedTenantURL string
}

// Handler serves /v1/rubrik. It exists only while the provider is enabled
// (KTD12), so the routes are absent otherwise. Build it with NewHandler.
type Handler struct {
	store        AdminStore
	clusters     ClusterRecords
	service      *Service
	access       *Access
	rsc          ClusterLister
	client       TokenFlusher
	audit        audit.Logger
	logger       *slog.Logger
	connectionID string
	probe        func(ctx context.Context, tenantURL, clientID, clientSecret string) error
	validateURL  func(ctx context.Context, raw string) error
	ops          OperationStore
	writer       RSCWriter
	restorer     RSCRestorer
	// suggested is the valid Helm tenant URL, or "".
	suggested string
}

// NewHandler checks d and returns the handler.
func NewHandler(d HandlerDeps) (*Handler, error) {
	switch {
	case d.Store == nil:
		return nil, errors.New("rubrik handler: store is required")
	case d.Clusters == nil:
		return nil, errors.New("rubrik handler: cluster registry is required")
	case d.Service == nil:
		return nil, errors.New("rubrik handler: service is required")
	case d.Access == nil:
		return nil, errors.New("rubrik handler: access rules are required")
	case d.RSC == nil:
		return nil, errors.New("rubrik handler: rsc operations are required")
	case d.Client == nil:
		return nil, errors.New("rubrik handler: client is required")
	case d.Audit == nil:
		return nil, errors.New("rubrik handler: audit logger is required")
	case d.ConnectionID == "":
		return nil, errors.New("rubrik handler: connection id is required")
	}
	h := &Handler{
		store:        d.Store,
		clusters:     d.Clusters,
		service:      d.Service,
		access:       d.Access,
		rsc:          d.RSC,
		client:       d.Client,
		audit:        d.Audit,
		logger:       d.Logger,
		connectionID: d.ConnectionID,
		probe:        d.Probe,
		validateURL:  d.ValidateURL,
		ops:          d.Operations,
		writer:       d.Writer,
		restorer:     d.Restorer,
	}
	if h.ops == nil {
		h.ops, _ = d.Store.(OperationStore)
	}
	if h.writer == nil {
		h.writer, _ = d.RSC.(RSCWriter)
	}
	if h.restorer == nil {
		h.restorer, _ = d.RSC.(RSCRestorer)
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.probe == nil {
		h.probe = Probe
	}
	if h.validateURL == nil {
		h.validateURL = k8s.ValidateRemoteURLContext
	}
	h.suggested = suggestedTenantURL(d.SuggestedTenantURL, h.logger)
	return h, nil
}

// Mount registers the Rubrik routes under /rubrik when h exists. The server
// passes a nil handler while the provider is off, so no Rubrik route exists
// and every /rubrik path is a 404 (R28, KTD12). writeLimit rate-limits the
// writes; nil means none.
func Mount(r chi.Router, h *Handler, writeLimit func(http.Handler) http.Handler) {
	if h == nil {
		return
	}
	r.Route("/rubrik", func(rr chi.Router) {
		h.RegisterRoutes(rr, writeLimit)
	})
}

// RegisterRoutes is the Rubrik route table. Later units add their routes
// here. Status, the reads, the Rubrik writes and the operation read are
// open to every authenticated user, which each request checks against the
// user's Rubrik grants (KTD3, R8); the connection and mapping endpoints are
// admin-only. Every write is rate-limited.
func (h *Handler) RegisterRoutes(r chi.Router, writeLimit func(http.Handler) http.Handler) {
	if writeLimit == nil {
		writeLimit = func(next http.Handler) http.Handler { return next }
	}
	r.Get("/status", h.HandleStatus)
	r.Get("/namespaces", h.HandleNamespaces)
	r.Get("/namespaces/{namespace}", h.HandleNamespace)
	r.Get("/sla-domains", h.HandleSLADomains)
	r.Get("/operations/{id}", h.HandleOperation)
	r.With(writeLimit).Post("/namespaces/{namespace}/snapshots", h.HandleSnapshot)
	r.With(writeLimit).Put("/namespaces/{namespace}/sla", h.HandleSLA)
	r.Get("/namespaces/{namespace}/restore-targets", h.HandleRestoreTargets)
	r.With(writeLimit).Post("/restores", h.HandleRestore)
	r.Group(func(ar chi.Router) {
		ar.Use(middleware.RequireAdmin)
		ar.Get("/rsc-clusters", h.HandleListRSCClusters)
		ar.Get("/mappings", h.HandleListMappings)
		ar.With(writeLimit).Put("/connection", h.HandlePutConnection)
		ar.With(writeLimit).Put("/mappings/{clusterID}", h.HandlePutMapping)
		ar.With(writeLimit).Delete("/mappings/{clusterID}", h.HandleDeleteMapping)
	})
}

// EvictCluster drops every cached RSC answer for clusterID. main.go
// registers it as a ClusterRouter evict hook, so a deleted or re-registered
// cluster leaves nothing behind in the service's cache (object lists are
// never aged out on their own).
func (h *Handler) EvictCluster(clusterID string) {
	id := k8s.NormalizedClusterID(clusterID)
	s := h.service
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		// Keys are "<kind>|<revision>|<cluster>|..."; SLA domains carry no
		// cluster and are kept.
		parts := strings.SplitN(key, "|", 4)
		if len(parts) >= 3 && parts[2] == id {
			delete(s.entries, key)
		}
	}
}

// ---------------------------------------------------------------------------
// Wire shapes

// StatusView answers GET /rubrik/status.
type StatusView struct {
	Enabled       bool            `json:"enabled"`
	Preview       bool            `json:"preview"`
	WritesEnabled bool            `json:"writesEnabled"`
	Cluster       ClusterStatus   `json:"cluster"`
	Connection    *ConnectionView `json:"connection,omitempty"`
}

// ClusterStatus is the selected cluster's Rubrik state. Mapping is set for
// admins only.
type ClusterStatus struct {
	ID      string       `json:"id"`
	State   string       `json:"state"`
	Mapping *MappingView `json:"mapping,omitempty"`
}

// ConnectionView is the stored connection as admins see it. The client
// secret is never part of it (R1).
type ConnectionView struct {
	Configured      bool       `json:"configured"`
	TenantURL       string     `json:"tenantUrl,omitempty"`
	ClientID        string     `json:"clientId,omitempty"`
	HasSecret       bool       `json:"hasSecret"`
	Revision        int64      `json:"revision,omitempty"`
	UpdatedBy       string     `json:"updatedBy,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
	Health          string     `json:"health"`
	LastSuccessAt   *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt   *time.Time `json:"lastFailureAt,omitempty"`
	LastFailureKind string     `json:"lastFailureKind,omitempty"`
	// SuggestedTenantURL is Helm's rubrik.tenantUrl, set only while no
	// connection is saved; the settings page pre-fills it.
	SuggestedTenantURL string `json:"suggestedTenantUrl,omitempty"`
}

// MappingView is one cluster mapping as admins see it.
type MappingView struct {
	ClusterID      string    `json:"clusterId"`
	RSCClusterID   string    `json:"rscClusterId"`
	RSCClusterName string    `json:"rscClusterName"`
	State          string    `json:"state"`
	UpdatedBy      string    `json:"updatedBy"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// RSCClusterView is one RSC Kubernetes cluster for the mapping picker.
type RSCClusterView struct {
	Cluster
	// MappedTo is the k8sCenter cluster already mapped to it, if any.
	MappedTo string `json:"mappedTo,omitempty"`
}

// connectionRequest is PUT /rubrik/connection. ClientSecret is omitted (or
// null) to keep the stored secret, which is allowed only while the tenant
// URL stays on the origin the secret was entered for.
type connectionRequest struct {
	TenantURL    string  `json:"tenantUrl"`
	ClientID     string  `json:"clientId"`
	ClientSecret *string `json:"clientSecret"`
}

// connectionResponse answers a successful save. MappingsCleared is true
// when the tenant origin changed, which removes every mapping. When the
// previous connection could not be read, it is true when the save left none
// of the mappings that existed before it (or the count could not be read).
type connectionResponse struct {
	ConnectionView
	MappingsCleared bool `json:"mappingsCleared"`
}

// mappingRequest is PUT /rubrik/mappings/{clusterID}. ConnectionRevision is
// the revision the admin read when choosing the RSC cluster; a save after
// the connection changed is refused.
type mappingRequest struct {
	RSCClusterID       string `json:"rscClusterId"`
	ConnectionRevision int64  `json:"connectionRevision"`
}

// ---------------------------------------------------------------------------
// Status

// HandleStatus answers GET /rubrik/status for any authenticated user: the
// provider settings and the selected cluster's state, plus the connection
// and mapping details for admins.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	clusterID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(r.Context()))
	out := StatusView{
		Enabled:       true,
		Preview:       true,
		WritesEnabled: h.access.WritesEnabled(),
		Cluster:       ClusterStatus{ID: clusterID},
	}

	viewer, err := h.access.Viewer(user, clusterID)
	switch {
	case errors.Is(err, ErrRemoteAdminOnly):
		out.Cluster.State = StateAdminOnly
		httputil.WriteData(w, out)
		return
	case err != nil:
		h.logger.Error("rubrik status: building viewer failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "Rubrik status is unavailable", "")
		return
	}

	state, target := h.clusterState(r.Context(), clusterID)
	if !viewer.Admin() {
		if state == StateNotConfigured || state == StateNotMapped || state == StateMappingInvalid {
			state = StateNotSetUp
		}
		out.Cluster.State = state
		httputil.WriteData(w, out)
		return
	}

	out.Cluster.State = state
	out.Cluster.Mapping = h.mappingDetail(r.Context(), clusterID, state, target)
	cv := h.connectionView(r.Context())
	out.Connection = &cv
	httputil.WriteData(w, out)
}

// clusterState resolves clusterID's mapping into a status state.
func (h *Handler) clusterState(ctx context.Context, clusterID string) (string, Target) {
	t, err := h.service.Resolve(ctx, clusterID)
	switch {
	case err == nil:
		return StateMapped, t
	case errors.Is(err, ErrNotConfigured):
		return StateNotConfigured, Target{}
	case errors.Is(err, ErrNotMapped):
		return StateNotMapped, Target{}
	case errors.Is(err, ErrMappingInvalid):
		return StateMappingInvalid, Target{}
	default:
		h.logger.Warn("rubrik: resolving cluster mapping failed", "cluster", clusterID, "error", err)
		return StateUnavailable, Target{}
	}
}

// mappingDetail returns the mapping an admin sees for a state, reading an
// invalid mapping from the store since Resolve vouches only for valid ones.
func (h *Handler) mappingDetail(ctx context.Context, clusterID, state string, t Target) *MappingView {
	switch state {
	case StateMapped:
		v := toMappingView(t.Mapping, state)
		return &v
	case StateMappingInvalid:
		m, err := h.store.GetMapping(ctx, clusterID)
		if err != nil {
			return nil
		}
		v := toMappingView(m, state)
		return &v
	}
	return nil
}

// connectionView reads the stored connection for an admin.
func (h *Handler) connectionView(ctx context.Context) ConnectionView {
	c, _, err := h.store.GetConnection(ctx, h.connectionID)
	switch {
	case errors.Is(err, store.ErrRubrikConnectionNotFound):
		// Helm's URL is offered only while nothing is saved: a saved
		// connection is the one source of truth, so a later Helm change
		// never touches it.
		return ConnectionView{Health: HealthNotConfigured, SuggestedTenantURL: h.suggested}
	case err != nil:
		h.logger.Error("rubrik: reading connection failed", "error", err)
		return ConnectionView{Health: HealthUnavailable}
	}
	return toConnectionView(c)
}

func toConnectionView(c store.RubrikConnection) ConnectionView {
	v := ConnectionView{
		Configured:      true,
		TenantURL:       c.TenantURL,
		ClientID:        c.ClientID,
		HasSecret:       c.HasSecret,
		Revision:        c.Revision,
		UpdatedBy:       c.UpdatedBy,
		LastSuccessAt:   c.LastSuccessAt,
		LastFailureAt:   c.LastFailureAt,
		LastFailureKind: c.LastFailureKind,
		Health:          connectionHealth(c),
	}
	if !c.UpdatedAt.IsZero() {
		at := c.UpdatedAt
		v.UpdatedAt = &at
	}
	return v
}

// connectionHealth derives R3's health from the recorded contact outcomes:
// the later of the last success and the last failure wins.
func connectionHealth(c store.RubrikConnection) string {
	if !c.HasSecret {
		return HealthSecretRequired
	}
	failedLast := c.LastFailureAt != nil && (c.LastSuccessAt == nil || c.LastFailureAt.After(*c.LastSuccessAt))
	switch {
	case failedLast && c.LastFailureKind == HealthAuthFailed:
		return HealthAuthFailed
	case failedLast && c.LastFailureKind == HealthUnreachable:
		return HealthUnreachable
	case failedLast:
		return HealthFailing
	case c.LastSuccessAt != nil:
		return HealthOK
	}
	return HealthUnknown
}

func toMappingView(m store.RubrikClusterMapping, state string) MappingView {
	return MappingView{
		ClusterID:      m.ClusterID,
		RSCClusterID:   m.RSCClusterID,
		RSCClusterName: m.RSCClusterName,
		State:          state,
		UpdatedBy:      m.UpdatedBy,
		UpdatedAt:      m.UpdatedAt,
	}
}

// ---------------------------------------------------------------------------
// Connection

// HandlePutConnection answers PUT /rubrik/connection (admin). It validates
// the request, refuses to carry a stored secret to another origin, probes
// the credentials, stores them, drops the cached token and audits the save
// without the URL or secret (R1, R2, R4).
func (h *Handler) HandlePutConnection(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	var req connectionRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.TenantURL = strings.TrimSpace(req.TenantURL)
	req.ClientID = strings.TrimSpace(req.ClientID)
	if msg := validateConnectionRequest(req); msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}
	// checkTenant classifies the address check the way the client does: a
	// refused address is invalid_tenant_url (400), while a lookup that failed
	// or timed out says nothing about the address and is unreachable (502).
	if err := checkTenant(r.Context(), h.validateURL, req.TenantURL); err != nil {
		status, reason, msg := classifyRSCError(err)
		h.logger.Info("rubrik connection: tenant url refused", "reason", reason, "error", err)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
		return
	}

	// The stored connection decides two things before any request leaves:
	// whether an omitted secret may be kept (only on the origin it was
	// entered for, so a changed URL cannot exfiltrate it), and the URL the
	// save is conditional on (a save in between makes this one a 409).
	//
	// A save that carries a new secret needs neither: the store re-reads the
	// stored origin itself under a row lock, without decrypting the old
	// secret. So an unreadable row (for instance a secret encrypted under a
	// key that has since changed) refuses only a save that would keep the
	// stored secret, and a new secret repairs it.
	current, creds, err := h.store.GetConnection(r.Context(), h.connectionID)
	exists := err == nil
	previousUnreadable := false
	if err != nil && !errors.Is(err, store.ErrRubrikConnectionNotFound) {
		if req.ClientSecret == nil {
			h.logger.Error("rubrik connection: reading stored connection failed", "error", err)
			httputil.WriteError(w, http.StatusServiceUnavailable, msgStoreUnavailable, "")
			return
		}
		h.logger.Warn("rubrik connection: stored connection unreadable; saving the new secret over it", "error", err)
		previousUnreadable = true
	}
	probeSecret, keptFrom := "", ""
	if req.ClientSecret != nil {
		probeSecret = *req.ClientSecret
	} else {
		if !exists || !current.HasSecret || urlorigin.Of(current.TenantURL) != urlorigin.Of(req.TenantURL) {
			httputil.WriteErrorWithReason(w, http.StatusBadRequest, msgSecretRequired, "secret_required", nil)
			return
		}
		probeSecret, keptFrom = creds.ClientSecret, current.TenantURL
	}
	originChanged := exists && urlorigin.Of(current.TenantURL) != urlorigin.Of(req.TenantURL)

	if err := h.probe(r.Context(), req.TenantURL, req.ClientID, probeSecret); err != nil {
		status, reason, msg := classifyRSCError(err)
		h.logger.Warn("rubrik connection: credential probe failed", "reason", reason, "error", err)
		h.auditWrite(r, user, audit.ActionRubrikConnectionUpdate, "", auditKindConnection, h.connectionID, audit.ResultFailure, "probe failed: "+reason)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
		return
	}

	// With the previous row unreadable its origin is unknown, and the store
	// clears the mappings when that origin differs from the new one. Count
	// them around the save so the response and audit say what happened.
	mappingsBefore := 0
	if previousUnreadable {
		mappingsBefore = h.mappingCount(r.Context())
	}

	saved, err := h.store.UpsertConnection(r.Context(), h.connectionID, store.RubrikConnectionWrite{
		TenantURL:    req.TenantURL,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		KeptFrom:     keptFrom,
		UpdatedBy:    user.Username,
	})
	switch {
	case errors.Is(err, store.ErrRubrikSecretRequired):
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, msgSecretRequired, "secret_required", nil)
		return
	case errors.Is(err, store.ErrRubrikConnectionChanged):
		h.auditWrite(r, user, audit.ActionRubrikConnectionUpdate, "", auditKindConnection, h.connectionID, audit.ResultFailure, "connection changed concurrently")
		httputil.WriteErrorWithReason(w, http.StatusConflict, msgConnectionChanged, "connection_changed", nil)
		return
	case errors.Is(err, store.ErrRubrikInvalidTenantURL):
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, msgTenantURLInvalid, "invalid_tenant_url", nil)
		return
	case err != nil:
		h.logger.Error("rubrik connection: save failed", "error", err)
		h.auditWrite(r, user, audit.ActionRubrikConnectionUpdate, "", auditKindConnection, h.connectionID, audit.ResultFailure, "store write failed")
		httputil.WriteError(w, http.StatusServiceUnavailable, msgStoreUnavailable, "")
		return
	}
	// The service notices the new revision on its next read on every
	// replica; this replica's token goes now.
	h.client.Flush()

	mappingsCleared := originChanged
	if previousUnreadable {
		// A count that could not be read (-1) reads as cleared: telling an
		// admin to check the mappings is the safe mistake.
		mappingsCleared = mappingsBefore != 0 && h.mappingCount(r.Context()) <= 0
	}
	detail := "connection saved"
	switch {
	case previousUnreadable && mappingsCleared:
		detail = "connection saved; previous connection unreadable, mappings cleared"
	case previousUnreadable:
		detail = "connection saved; previous connection unreadable"
	case originChanged:
		detail = "connection saved; tenant origin changed, mappings cleared"
	case req.ClientSecret != nil:
		detail = "connection saved; secret replaced"
	}
	h.auditWrite(r, user, audit.ActionRubrikConnectionUpdate, "", auditKindConnection, h.connectionID, audit.ResultSuccess, detail)
	httputil.WriteData(w, connectionResponse{ConnectionView: toConnectionView(saved), MappingsCleared: mappingsCleared})
}

// mappingCount returns how many mappings the connection has, or -1 when
// they cannot be listed.
func (h *Handler) mappingCount(ctx context.Context) int {
	mappings, err := h.store.ListMappings(ctx, h.connectionID)
	if err != nil {
		h.logger.Warn("rubrik connection: listing mappings failed", "error", err)
		return -1
	}
	return len(mappings)
}

// validateConnectionRequest returns the client message for a malformed
// request, or "". The address check runs separately.
func validateConnectionRequest(req connectionRequest) string {
	switch {
	case req.TenantURL == "":
		return "tenantUrl is required"
	case len(req.TenantURL) > maxTenantURLBytes:
		return "tenantUrl is too long"
	case req.ClientID == "":
		return "clientId is required"
	case len(req.ClientID) > maxClientIDBytes:
		return "clientId is too long"
	case req.ClientSecret != nil && *req.ClientSecret == "":
		return "clientSecret must not be empty; omit it to keep the stored secret"
	case req.ClientSecret != nil && len(*req.ClientSecret) > maxSecretBytes:
		return "clientSecret is too long"
	}
	return validateTenantURL(req.TenantURL)
}

// validateTenantURL returns the client message for a tenant URL that is not
// an HTTPS origin, or "". The address check runs separately.
func validateTenantURL(raw string) string {
	u, err := url.Parse(raw)
	switch {
	case err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "":
		return msgTenantURLInvalid
	case u.User != nil:
		return "tenantUrl must not contain credentials"
	case u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "?#"):
		return "tenantUrl must not contain a query string or fragment"
	case u.Path != "" && u.Path != "/":
		return "tenantUrl must be the tenant's address without a path"
	}
	return ""
}

// suggestedTenantURL is the Helm-supplied tenant URL (rubrik.tenantUrl) the
// settings page pre-fills while no connection is saved, or "" when it is
// unset or not a valid tenant URL. It is only a suggestion: nothing writes it
// to the store, so an admin's save (address-checked, probed and audited) is
// the one way a connection is made, and once one exists this value is unused.
func suggestedTenantURL(raw string, logger *slog.Logger) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > maxTenantURLBytes || validateTenantURL(raw) != "" {
		// The value is not logged: it is the operator's tenant address.
		logger.Warn("rubrik.tenantUrl is not an HTTPS tenant address without a path; the settings page will not pre-fill it")
		return ""
	}
	return raw
}

// ---------------------------------------------------------------------------
// Mappings

// HandleListRSCClusters answers GET /rubrik/rsc-clusters (admin): RSC's
// Kubernetes clusters for the mapping picker, each with the k8sCenter
// cluster already mapped to it.
func (h *Handler) HandleListRSCClusters(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), adminRSCTimeout)
	defer cancel()
	clusters, err := h.rsc.Clusters(ctx)
	if err != nil {
		status, reason, msg := classifyRSCError(err)
		h.logger.Warn("rubrik: listing rsc clusters failed", "reason", reason, "error", err)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
		return
	}
	mappings, err := h.store.ListMappings(r.Context(), h.connectionID)
	if err != nil {
		h.logger.Error("rubrik: listing mappings failed", "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgStoreUnavailable, "")
		return
	}
	mappedTo := make(map[string]string, len(mappings))
	for _, m := range mappings {
		mappedTo[strings.ToLower(m.RSCClusterID)] = m.ClusterID
	}
	out := make([]RSCClusterView, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, RSCClusterView{Cluster: c, MappedTo: mappedTo[strings.ToLower(c.ID)]})
	}
	httputil.WriteData(w, out)
}

// HandleListMappings answers GET /rubrik/mappings (admin): every mapping of
// the connection with its current state.
func (h *Handler) HandleListMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.store.ListMappings(r.Context(), h.connectionID)
	if err != nil {
		h.logger.Error("rubrik: listing mappings failed", "error", err)
		httputil.WriteError(w, http.StatusServiceUnavailable, msgStoreUnavailable, "")
		return
	}
	out := make([]MappingView, 0, len(mappings))
	for _, m := range mappings {
		state, _ := h.clusterState(r.Context(), m.ClusterID)
		out = append(out, toMappingView(m, state))
	}
	httputil.WriteData(w, out)
}

// HandlePutMapping answers PUT /rubrik/mappings/{clusterID} (admin). It
// confirms RSC lists the chosen cluster (taking its name from RSC, not the
// request), stores the k8sCenter cluster's current registration generation
// with it, and audits the save (R2).
func (h *Handler) HandlePutMapping(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	clusterID, ok := clusterIDParam(w, r)
	if !ok {
		return
	}
	var req mappingRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.RSCClusterID = strings.TrimSpace(req.RSCClusterID)
	if !validUUID(req.RSCClusterID) {
		httputil.WriteError(w, http.StatusBadRequest, "rscClusterId must be an RSC cluster id", "")
		return
	}
	if req.ConnectionRevision <= 0 {
		httputil.WriteError(w, http.StatusBadRequest, "connectionRevision is required", "")
		return
	}

	generation, status, msg := h.currentGeneration(r.Context(), clusterID)
	if status != 0 {
		httputil.WriteError(w, status, msg, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), adminRSCTimeout)
	clusters, err := h.rsc.Clusters(ctx)
	cancel()
	if err != nil {
		status, reason, msg := classifyRSCError(err)
		h.logger.Warn("rubrik mapping: listing rsc clusters failed", "reason", reason, "error", err)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
		return
	}
	var found []Cluster
	for _, c := range clusters {
		if strings.EqualFold(c.ID, req.RSCClusterID) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		httputil.WriteErrorWithReason(w, http.StatusNotFound, "RSC lists no Kubernetes cluster with that id", "rsc_cluster_not_found", nil)
		return
	case 1:
	default:
		httputil.WriteErrorWithReason(w, http.StatusConflict, "RSC lists that id for more than one cluster", "rsc_cluster_ambiguous", nil)
		return
	}

	saved, err := h.store.SetMapping(r.Context(), store.RubrikMappingWrite{
		ClusterID:          clusterID,
		ConnectionID:       h.connectionID,
		ConnectionRevision: req.ConnectionRevision,
		RSCClusterID:       found[0].ID,
		RSCClusterName:     found[0].Name,
		ClusterGeneration:  generation,
		UpdatedBy:          user.Username,
	})
	if status, reason, msg := classifyMappingError(err); status != 0 {
		if status == http.StatusServiceUnavailable {
			h.logger.Error("rubrik mapping: save failed", "cluster", clusterID, "error", err)
		}
		h.auditWrite(r, user, audit.ActionRubrikMappingUpdate, clusterID, auditKindMapping, clusterID, audit.ResultFailure, "mapping not saved: "+reason)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
		return
	}
	h.EvictCluster(clusterID)
	h.auditWrite(r, user, audit.ActionRubrikMappingUpdate, clusterID, auditKindMapping, clusterID, audit.ResultSuccess, "mapped to rsc cluster "+saved.RSCClusterID)
	httputil.WriteData(w, toMappingView(saved, StateMapped))
}

// HandleDeleteMapping answers DELETE /rubrik/mappings/{clusterID} (admin).
func (h *Handler) HandleDeleteMapping(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	clusterID, ok := clusterIDParam(w, r)
	if !ok {
		return
	}
	err := h.store.DeleteMapping(r.Context(), clusterID)
	switch {
	case errors.Is(err, store.ErrRubrikMappingNotFound):
		httputil.WriteErrorWithReason(w, http.StatusNotFound, "the cluster has no Rubrik mapping", "not_mapped", nil)
		return
	case err != nil:
		h.logger.Error("rubrik mapping: delete failed", "cluster", clusterID, "error", err)
		h.auditWrite(r, user, audit.ActionRubrikMappingDelete, clusterID, auditKindMapping, clusterID, audit.ResultFailure, "store write failed")
		httputil.WriteError(w, http.StatusServiceUnavailable, msgStoreUnavailable, "")
		return
	}
	h.EvictCluster(clusterID)
	h.auditWrite(r, user, audit.ActionRubrikMappingDelete, clusterID, auditKindMapping, clusterID, audit.ResultSuccess, "mapping removed")
	w.WriteHeader(http.StatusNoContent)
}

// currentGeneration returns clusterID's registration generation as a
// mapping stores it: the literal "local", or the registration's creation
// instant (KTD6). A non-zero status is the refusal to send.
func (h *Handler) currentGeneration(ctx context.Context, clusterID string) (gen string, status int, msg string) {
	if k8s.IsLocalClusterID(clusterID) {
		return k8s.LocalGeneration, 0, ""
	}
	rec, err := h.clusters.Get(ctx, clusterID)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || (err == nil && rec == nil):
		return "", http.StatusNotFound, "cluster not registered"
	case err != nil:
		h.logger.Error("rubrik mapping: reading cluster registration failed", "cluster", clusterID, "error", err)
		return "", http.StatusServiceUnavailable, msgStoreUnavailable
	}
	return rec.CreatedAt.UTC().Format(time.RFC3339Nano), 0, ""
}

// classifyMappingError maps a SetMapping error to a response; status 0
// means success.
func classifyMappingError(err error) (status int, reason, msg string) {
	switch {
	case err == nil:
		return 0, "", ""
	case errors.Is(err, store.ErrRubrikConnectionChanged):
		return http.StatusConflict, "connection_changed", msgConnectionChanged
	case errors.Is(err, store.ErrRubrikMappingConflict):
		return http.StatusConflict, "rsc_cluster_already_mapped", "that RSC cluster is already mapped to another cluster"
	case errors.Is(err, store.ErrRubrikClusterUnknown):
		return http.StatusNotFound, "cluster_not_registered", "cluster not registered"
	case errors.Is(err, store.ErrRubrikConnectionNotFound):
		return http.StatusConflict, "not_configured", "no Rubrik connection is configured"
	}
	return http.StatusServiceUnavailable, "store_unavailable", msgStoreUnavailable
}

// ---------------------------------------------------------------------------
// Helpers

const (
	msgStoreUnavailable  = "Rubrik settings could not be read or saved"
	msgSecretRequired    = "enter the client secret: none is stored for this tenant address"
	msgConnectionChanged = "the Rubrik connection changed while you were editing; reload and try again"
	msgTenantURLInvalid  = "tenantUrl must be a valid HTTPS URL"
	msgTenantURLPrivate  = "tenantUrl must not resolve to a private or reserved address"
)

// classifyRSCError maps a client error class to a status, reason and fixed
// message. The error itself is only logged: it may name an address.
func classifyRSCError(err error) (status int, reason, msg string) {
	switch {
	case errors.Is(err, ErrInvalidTenantURL):
		return http.StatusBadRequest, "invalid_tenant_url", msgTenantURLPrivate
	case errors.Is(err, ErrAuthFailed):
		return http.StatusBadRequest, HealthAuthFailed, "RSC refused the client ID or secret"
	case errors.Is(err, ErrNotConfigured):
		return http.StatusConflict, "not_configured", "no Rubrik connection is configured"
	case errors.Is(err, ErrConnectionUnavailable):
		return http.StatusServiceUnavailable, "store_unavailable", msgStoreUnavailable
	case errors.Is(err, ErrRateLimited):
		return http.StatusBadGateway, "rate_limited", "RSC is rate limiting requests; try again shortly"
	case errors.Is(err, ErrGraphQL), errors.Is(err, ErrUnreadable):
		return http.StatusBadGateway, "rsc_error", "RSC returned an answer k8sCenter could not use"
	}
	return http.StatusBadGateway, HealthUnreachable, "RSC could not be reached"
}

// decodeBody decodes a size-capped JSON body into v, writing a 400 on
// failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return false
	}
	return true
}

// clusterIDParam reads and normalizes the {clusterID} path parameter.
func clusterIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := chi.URLParam(r, "clusterID")
	if raw == "" || len(raw) > maxClusterIDBytes || strings.Trim(raw, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.") != "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid cluster id", "")
		return "", false
	}
	return k8s.NormalizedClusterID(raw), true
}

// auditWrite records an admin write. Entries carry the action, result and a
// fixed detail, never the tenant URL or the secret (R1). clusterID is empty
// for the installation-wide connection.
func (h *Handler) auditWrite(r *http.Request, user *auth.User, action audit.Action, clusterID, kind, name string, result audit.Result, detail string) {
	if err := h.audit.Log(r.Context(), audit.Entry{
		Timestamp:    time.Now(),
		ClusterID:    clusterID,
		User:         user.Username,
		SourceIP:     r.RemoteAddr,
		ConnectionIP: middleware.SocketPeerFromContext(r.Context()),
		Action:       action,
		ResourceKind: kind,
		ResourceName: name,
		Result:       result,
		Detail:       detail,
	}); err != nil {
		h.logger.Error("rubrik: audit write failed", "action", string(action), "error", err)
	}
}
