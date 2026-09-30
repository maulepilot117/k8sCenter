package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// ActionNotificationSuspend is the audit action for suspending/resuming notification resources.
const ActionNotificationSuspend audit.Action = "notification_suspend"

const cacheTTL = 30 * time.Second

// Handler serves Flux Notification Controller HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
	AuditLogger   audit.Logger
	Clients       k8s.ClusterClients
	Presence      *k8s.Presence

	remoteOnce sync.Once
	remote     *remotecache.Cache[*snapshot]

	// baseDynOverride is a test-only seam for the local service-account
	// client; production leaves it nil.
	baseDynOverride dynamic.Interface

	providerCache resourceCache[NormalizedProvider]
	alertCache    resourceCache[NormalizedAlert]
	receiverCache resourceCache[NormalizedReceiver]

	providerGroup singleflight.Group
	alertGroup    singleflight.Group
	receiverGroup singleflight.Group
}

// resourceCache holds a per-resource-type cache with its own mutex, TTL, and generation counter.
type resourceCache[T any] struct {
	mu        sync.RWMutex
	items     []T
	fetchedAt time.Time
	gen       uint64
}

// ---------- cache layer ----------

// fetchProviders returns cached providers, refreshing if stale.
func (h *Handler) fetchProviders() ([]NormalizedProvider, error) {
	h.providerCache.mu.RLock()
	if h.providerCache.items != nil && time.Since(h.providerCache.fetchedAt) < cacheTTL {
		items := h.providerCache.items
		h.providerCache.mu.RUnlock()
		return items, nil
	}
	h.providerCache.mu.RUnlock()

	result, err, _ := h.providerGroup.Do("providers", func() (any, error) {
		return h.doFetchProviders()
	})
	if err != nil {
		return nil, err
	}
	return result.([]NormalizedProvider), nil
}

func (h *Handler) doFetchProviders() ([]NormalizedProvider, error) {
	h.providerCache.mu.RLock()
	gen := h.providerCache.gen
	h.providerCache.mu.RUnlock()

	fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	items, err := ListProviders(fetchCtx, h.baseDyn())
	if err != nil {
		return nil, err
	}

	h.providerCache.mu.Lock()
	if h.providerCache.gen == gen {
		h.providerCache.items = items
		h.providerCache.fetchedAt = time.Now()
	}
	h.providerCache.mu.Unlock()
	return items, nil
}

// fetchAlerts returns cached alerts, refreshing if stale.
func (h *Handler) fetchAlerts() ([]NormalizedAlert, error) {
	h.alertCache.mu.RLock()
	if h.alertCache.items != nil && time.Since(h.alertCache.fetchedAt) < cacheTTL {
		items := h.alertCache.items
		h.alertCache.mu.RUnlock()
		return items, nil
	}
	h.alertCache.mu.RUnlock()

	result, err, _ := h.alertGroup.Do("alerts", func() (any, error) {
		return h.doFetchAlerts()
	})
	if err != nil {
		return nil, err
	}
	return result.([]NormalizedAlert), nil
}

func (h *Handler) doFetchAlerts() ([]NormalizedAlert, error) {
	h.alertCache.mu.RLock()
	gen := h.alertCache.gen
	h.alertCache.mu.RUnlock()

	fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	items, err := ListAlerts(fetchCtx, h.baseDyn())
	if err != nil {
		return nil, err
	}

	h.alertCache.mu.Lock()
	if h.alertCache.gen == gen {
		h.alertCache.items = items
		h.alertCache.fetchedAt = time.Now()
	}
	h.alertCache.mu.Unlock()
	return items, nil
}

// fetchReceivers returns cached receivers, refreshing if stale.
func (h *Handler) fetchReceivers() ([]NormalizedReceiver, error) {
	h.receiverCache.mu.RLock()
	if h.receiverCache.items != nil && time.Since(h.receiverCache.fetchedAt) < cacheTTL {
		items := h.receiverCache.items
		h.receiverCache.mu.RUnlock()
		return items, nil
	}
	h.receiverCache.mu.RUnlock()

	result, err, _ := h.receiverGroup.Do("receivers", func() (any, error) {
		return h.doFetchReceivers()
	})
	if err != nil {
		return nil, err
	}
	return result.([]NormalizedReceiver), nil
}

func (h *Handler) doFetchReceivers() ([]NormalizedReceiver, error) {
	h.receiverCache.mu.RLock()
	gen := h.receiverCache.gen
	h.receiverCache.mu.RUnlock()

	fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	items, err := ListReceivers(fetchCtx, h.baseDyn())
	if err != nil {
		return nil, err
	}

	h.receiverCache.mu.Lock()
	if h.receiverCache.gen == gen {
		h.receiverCache.items = items
		h.receiverCache.fetchedAt = time.Now()
	}
	h.receiverCache.mu.Unlock()
	return items, nil
}

// InvalidateProviders clears only the providers cache.
func (h *Handler) InvalidateProviders() {
	h.providerCache.mu.Lock()
	h.providerCache.items = nil
	h.providerCache.gen++
	h.providerCache.mu.Unlock()
}

// InvalidateAlerts clears only the alerts cache.
func (h *Handler) InvalidateAlerts() {
	h.alertCache.mu.Lock()
	h.alertCache.items = nil
	h.alertCache.gen++
	h.alertCache.mu.Unlock()
}

// InvalidateReceivers clears only the receivers cache.
func (h *Handler) InvalidateReceivers() {
	h.receiverCache.mu.Lock()
	h.receiverCache.items = nil
	h.receiverCache.gen++
	h.receiverCache.mu.Unlock()
}

// ---------- RBAC filtering ----------

// namespacedItem is implemented by normalized types that carry a Namespace field.
type namespacedItem interface {
	getNamespace() string
}

func (p NormalizedProvider) getNamespace() string { return p.Namespace }
func (a NormalizedAlert) getNamespace() string    { return a.Namespace }
func (r NormalizedReceiver) getNamespace() string { return r.Namespace }

// filterByRBAC returns only items the user has permission to list in the
// given resource. On a remote cluster a check that could not be made is an
// error rather than a denial, so an unreachable cluster is not reported as
// an empty list; the local cluster keeps treating it as a denial.
func filterByRBAC[T namespacedItem](ctx context.Context, checker *resources.AccessChecker, user *auth.User, items []T, resource string) ([]T, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	local := k8s.IsLocalClusterID(clusterID)
	access := make(map[string]bool)
	var filtered []T
	for _, item := range items {
		ns := item.getNamespace()
		allowed, checked := access[ns]
		if !checked {
			can, err := checker.CanAccessGroupResource(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", FluxProviderGVR.Group, resource, ns)
			if err != nil && !local {
				return nil, err
			}
			allowed = err == nil && can
			access[ns] = allowed
		}
		if allowed {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// ---------- audit helper ----------

func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, ns, name string, result audit.Result, detail string) {
	if h.AuditLogger == nil {
		return
	}
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: ns,
		ResourceName:      name,
		Result:            result,
		Detail:            detail,
	})
}

// writeK8sError maps a Kubernetes API error to an appropriate HTTP status code
// and writes a user-friendly error response.
func (h *Handler) writeK8sError(w http.ResponseWriter, err error, verb, kind, ns, name string) {
	if apierrors.IsNotFound(err) {
		httputil.WriteError(w, http.StatusNotFound, kind+" '"+name+"' not found in namespace '"+ns+"'", "")
		return
	}
	if apierrors.IsForbidden(err) {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to "+verb+" "+kind+" '"+name+"'", "")
		return
	}
	if apierrors.IsAlreadyExists(err) {
		httputil.WriteError(w, http.StatusConflict, kind+" '"+name+"' already exists in namespace '"+ns+"'", "")
		return
	}
	if apierrors.IsConflict(err) {
		httputil.WriteError(w, http.StatusConflict, "conflict updating "+kind+" '"+name+"' — resource was modified", "")
		return
	}
	if apierrors.IsInvalid(err) {
		httputil.WriteError(w, http.StatusUnprocessableEntity, "invalid "+kind+" specification", "")
		return
	}
	httputil.WriteError(w, http.StatusInternalServerError, "failed to "+verb+" "+kind, "")
}

// ---------- status ----------

// HandleStatus returns the Flux Notification Controller availability status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if !isLocal(r.Context()) {
		httputil.WriteData(w, h.remoteStatus(r.Context(), user))
		return
	}

	// Fetch all three independently — partial success is acceptable.
	providers, provErr := h.fetchProviders()
	alerts, alertErr := h.fetchAlerts()
	receivers, recErr := h.fetchReceivers()

	// Available if at least one resource type was fetchable.
	available := provErr == nil || alertErr == nil || recErr == nil

	ns := NotificationStatus{
		Available: available,
	}
	// The local cluster treats a failed RBAC check as a denial, so
	// filterByRBAC returns no error here.
	if available {
		if provErr == nil {
			items, _ := filterByRBAC(r.Context(), h.AccessChecker, user, providers, "providers")
			ns.ProviderCount = len(items)
		}
		if alertErr == nil {
			items, _ := filterByRBAC(r.Context(), h.AccessChecker, user, alerts, "alerts")
			ns.AlertCount = len(items)
		}
		if recErr == nil {
			items, _ := filterByRBAC(r.Context(), h.AccessChecker, user, receivers, "receivers")
			ns.ReceiverCount = len(items)
		}
	}

	httputil.WriteData(w, ns)
}

// ---------- shared route bodies ----------

// serveList answers a list route for gvr on the request's cluster,
// RBAC-filtered and narrowed to ?namespace= when given.
func serveList[T namespacedItem](h *Handler, w http.ResponseWriter, r *http.Request, gvr schema.GroupVersionResource, local func() ([]T, error), pick func(*snapshot) []T) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	all, err := loadItems(h, r.Context(), user, gvr, local, pick)
	if err != nil {
		h.writeLoadError(w, r, err, gvr.Resource)
		return
	}
	items, err := filterByRBAC(r.Context(), h.AccessChecker, user, all, gvr.Resource)
	if err != nil {
		writeAccessCheckError(w, err)
		return
	}

	if ns := r.URL.Query().Get("namespace"); ns != "" {
		var filtered []T
		for _, item := range items {
			if item.getNamespace() == ns {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	httputil.WriteData(w, map[string]any{
		gvr.Resource: items,
		"total":      len(items),
	})
}

// serveDelete answers a delete route for one object of gvr, deleted by del.
func (h *Handler) serveDelete(w http.ResponseWriter, r *http.Request, gvr schema.GroupVersionResource, kind, noun string,
	del func(ctx context.Context, dyn dynamic.Interface, ns, name string) error) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.allowed(w, r, user, "delete", gvr.Resource, ns, "you do not have permission to delete this "+noun) {
		return
	}

	dynClient, ok := h.writeClient(w, r, user, gvr)
	if !ok {
		return
	}

	if err := del(r.Context(), dynClient, ns, name); err != nil {
		h.failWrite(w, r, user, err, audit.ActionDelete, "delete", kind, ns, name, gvr)
		return
	}

	h.auditLog(r, user, audit.ActionDelete, kind, ns, name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), gvr)
	httputil.WriteData(w, map[string]string{"message": "Deleted " + noun + " " + name})
}

// serveSuspend answers a suspend or resume route for one object of gvr,
// patched by suspend.
func (h *Handler) serveSuspend(w http.ResponseWriter, r *http.Request, gvr schema.GroupVersionResource, kind, noun string,
	suspend func(ctx context.Context, dyn dynamic.Interface, ns, name string, suspend bool) error) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.allowed(w, r, user, "patch", gvr.Resource, ns, "you do not have permission to modify this "+noun) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req struct {
		Suspend bool `json:"suspend"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	dynClient, ok := h.writeClient(w, r, user, gvr)
	if !ok {
		return
	}

	if err := suspend(r.Context(), dynClient, ns, name, req.Suspend); err != nil {
		h.failWrite(w, r, user, err, ActionNotificationSuspend, "patch", kind, ns, name, gvr)
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, kind, ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.invalidate(r.Context(), gvr)

	msg := "Suspended " + noun + " " + name
	if !req.Suspend {
		msg = "Resumed " + noun + " " + name
	}
	httputil.WriteData(w, map[string]string{"message": msg})
}

// ---------- providers ----------

// HandleListProviders returns all Flux notification providers, RBAC-filtered.
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	serveList(h, w, r, FluxProviderGVR, h.fetchProviders, func(s *snapshot) []NormalizedProvider { return s.providers })
}

// HandleCreateProvider creates a new Flux notification Provider.
func (h *Handler) HandleCreateProvider(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input ProviderInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if err := ValidateProviderInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	if !h.allowed(w, r, user, "create", "providers", input.Namespace, "you do not have permission to create providers in this namespace") {
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxProviderGVR)
	if !ok {
		return
	}

	provider, err := CreateProvider(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionCreate, "create", "Provider", input.Namespace, input.Name, FluxProviderGVR)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Provider", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxProviderGVR)
	httputil.WriteData(w, provider)
}

// HandleUpdateProvider updates an existing Flux notification Provider.
func (h *Handler) HandleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.allowed(w, r, user, "update", "providers", ns, "you do not have permission to update this provider") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input ProviderInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	input.Namespace = ns
	input.Name = name

	if err := ValidateProviderInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxProviderGVR)
	if !ok {
		return
	}

	provider, err := UpdateProvider(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionUpdate, "update", "Provider", ns, name, FluxProviderGVR)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Provider", ns, name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxProviderGVR)
	httputil.WriteData(w, provider)
}

// HandleDeleteProvider deletes a Flux notification Provider.
func (h *Handler) HandleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	h.serveDelete(w, r, FluxProviderGVR, "Provider", "provider", DeleteProvider)
}

// HandleSuspendProvider suspends or resumes a Flux notification Provider.
func (h *Handler) HandleSuspendProvider(w http.ResponseWriter, r *http.Request) {
	h.serveSuspend(w, r, FluxProviderGVR, "Provider", "provider", SuspendProvider)
}

// ---------- alerts ----------

// HandleListAlerts returns all Flux notification alerts, RBAC-filtered.
func (h *Handler) HandleListAlerts(w http.ResponseWriter, r *http.Request) {
	serveList(h, w, r, FluxAlertGVR, h.fetchAlerts, func(s *snapshot) []NormalizedAlert { return s.alerts })
}

// HandleCreateAlert creates a new Flux notification Alert.
func (h *Handler) HandleCreateAlert(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input AlertInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if err := ValidateAlertInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	if !h.allowed(w, r, user, "create", "alerts", input.Namespace, "you do not have permission to create alerts in this namespace") {
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxAlertGVR)
	if !ok {
		return
	}

	alert, err := CreateAlert(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionCreate, "create", "Alert", input.Namespace, input.Name, FluxAlertGVR)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Alert", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxAlertGVR)
	httputil.WriteData(w, alert)
}

// HandleUpdateAlert updates an existing Flux notification Alert.
func (h *Handler) HandleUpdateAlert(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.allowed(w, r, user, "update", "alerts", ns, "you do not have permission to update this alert") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input AlertInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	input.Namespace = ns
	input.Name = name

	if err := ValidateAlertInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxAlertGVR)
	if !ok {
		return
	}

	alert, err := UpdateAlert(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionUpdate, "update", "Alert", ns, name, FluxAlertGVR)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Alert", ns, name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxAlertGVR)
	httputil.WriteData(w, alert)
}

// HandleDeleteAlert deletes a Flux notification Alert.
func (h *Handler) HandleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	h.serveDelete(w, r, FluxAlertGVR, "Alert", "alert", DeleteAlert)
}

// HandleSuspendAlert suspends or resumes a Flux notification Alert.
func (h *Handler) HandleSuspendAlert(w http.ResponseWriter, r *http.Request) {
	h.serveSuspend(w, r, FluxAlertGVR, "Alert", "alert", SuspendAlert)
}

// ---------- receivers ----------

// HandleListReceivers returns all Flux notification receivers, RBAC-filtered.
func (h *Handler) HandleListReceivers(w http.ResponseWriter, r *http.Request) {
	serveList(h, w, r, FluxReceiverGVR, h.fetchReceivers, func(s *snapshot) []NormalizedReceiver { return s.receivers })
}

// HandleCreateReceiver creates a new Flux notification Receiver.
func (h *Handler) HandleCreateReceiver(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input ReceiverInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if err := ValidateReceiverInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	if !h.allowed(w, r, user, "create", "receivers", input.Namespace, "you do not have permission to create receivers in this namespace") {
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxReceiverGVR)
	if !ok {
		return
	}

	receiver, err := CreateReceiver(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionCreate, "create", "Receiver", input.Namespace, input.Name, FluxReceiverGVR)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Receiver", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxReceiverGVR)
	httputil.WriteData(w, receiver)
}

// HandleUpdateReceiver updates an existing Flux notification Receiver.
func (h *Handler) HandleUpdateReceiver(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.allowed(w, r, user, "update", "receivers", ns, "you do not have permission to update this receiver") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var input ReceiverInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	input.Namespace = ns
	input.Name = name

	if err := ValidateReceiverInput(input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	dynClient, ok := h.writeClient(w, r, user, FluxReceiverGVR)
	if !ok {
		return
	}

	receiver, err := UpdateReceiver(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.failWrite(w, r, user, err, audit.ActionUpdate, "update", "Receiver", ns, name, FluxReceiverGVR)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Receiver", ns, name, audit.ResultSuccess, "")
	h.invalidate(r.Context(), FluxReceiverGVR)
	httputil.WriteData(w, receiver)
}

// HandleDeleteReceiver deletes a Flux notification Receiver.
func (h *Handler) HandleDeleteReceiver(w http.ResponseWriter, r *http.Request) {
	h.serveDelete(w, r, FluxReceiverGVR, "Receiver", "receiver", DeleteReceiver)
}

// HandleSuspendReceiver suspends or resumes a Flux notification Receiver.
func (h *Handler) HandleSuspendReceiver(w http.ResponseWriter, r *http.Request) {
	h.serveSuspend(w, r, FluxReceiverGVR, "Receiver", "receiver", SuspendReceiver)
}
