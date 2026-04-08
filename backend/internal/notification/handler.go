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

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
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

	items, err := ListProviders(fetchCtx, h.K8sClient.BaseDynamicClient())
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

	items, err := ListAlerts(fetchCtx, h.K8sClient.BaseDynamicClient())
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

	items, err := ListReceivers(fetchCtx, h.K8sClient.BaseDynamicClient())
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

// InvalidateAll clears all three caches.
func (h *Handler) InvalidateAll() {
	h.InvalidateProviders()
	h.InvalidateAlerts()
	h.InvalidateReceivers()
}

// InvalidateCache is kept for backward compatibility with CRD event handlers.
func (h *Handler) InvalidateCache() {
	h.InvalidateAll()
}

// ---------- RBAC filtering ----------

// namespacedItem is implemented by normalized types that carry a Namespace field.
type namespacedItem interface {
	getNamespace() string
}

func (p NormalizedProvider) getNamespace() string { return p.Namespace }
func (a NormalizedAlert) getNamespace() string    { return a.Namespace }
func (r NormalizedReceiver) getNamespace() string { return r.Namespace }

// filterByRBAC returns only items the user has permission to list in the given resource.
func filterByRBAC[T namespacedItem](ctx context.Context, checker *resources.AccessChecker, user *auth.User, items []T, resource string) []T {
	type accessKey struct{ namespace string }
	access := make(map[accessKey]bool)
	var filtered []T
	for _, item := range items {
		key := accessKey{item.getNamespace()}
		allowed, checked := access[key]
		if !checked {
			can, err := checker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "notification.toolkit.fluxcd.io", resource, item.getNamespace())
			allowed = err == nil && can
			access[key] = allowed
		}
		if allowed {
			filtered = append(filtered, item)
		}
	}
	return filtered
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

	// Fetch all three independently — partial success is acceptable.
	providers, provErr := h.fetchProviders()
	alerts, alertErr := h.fetchAlerts()
	receivers, recErr := h.fetchReceivers()

	// Available if at least one resource type was fetchable.
	available := provErr == nil || alertErr == nil || recErr == nil

	ns := NotificationStatus{
		Available: available,
	}
	if available {
		if provErr == nil {
			ns.ProviderCount = len(filterByRBAC(r.Context(), h.AccessChecker, user, providers, "providers"))
		}
		if alertErr == nil {
			ns.AlertCount = len(filterByRBAC(r.Context(), h.AccessChecker, user, alerts, "alerts"))
		}
		if recErr == nil {
			ns.ReceiverCount = len(filterByRBAC(r.Context(), h.AccessChecker, user, receivers, "receivers"))
		}
	}

	httputil.WriteData(w, ns)
}

// ---------- providers ----------

// HandleListProviders returns all Flux notification providers, RBAC-filtered.
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	allProviders, err := h.fetchProviders()
	if err != nil {
		h.Logger.Error("failed to fetch notification providers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch providers", "")
		return
	}

	providers := filterByRBAC(r.Context(), h.AccessChecker, user, allProviders, "providers")

	if ns := r.URL.Query().Get("namespace"); ns != "" {
		var filtered []NormalizedProvider
		for _, p := range providers {
			if p.Namespace == ns {
				filtered = append(filtered, p)
			}
		}
		providers = filtered
	}

	httputil.WriteData(w, struct {
		Providers []NormalizedProvider `json:"providers"`
		Total     int                 `json:"total"`
	}{
		Providers: providers,
		Total:     len(providers),
	})
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "create", "notification.toolkit.fluxcd.io", "providers", input.Namespace)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to create providers in this namespace", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	provider, err := CreateProvider(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Provider", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "create", "Provider", input.Namespace, input.Name)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Provider", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.InvalidateProviders()
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "update", "notification.toolkit.fluxcd.io", "providers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to update this provider", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	provider, err := UpdateProvider(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionUpdate, "Provider", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "update", "Provider", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Provider", ns, name, audit.ResultSuccess, "")
	h.InvalidateProviders()
	httputil.WriteData(w, provider)
}

// HandleDeleteProvider deletes a Flux notification Provider.
func (h *Handler) HandleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "delete", "notification.toolkit.fluxcd.io", "providers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to delete this provider", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := DeleteProvider(r.Context(), dynClient, ns, name); err != nil {
		h.auditLog(r, user, audit.ActionDelete, "Provider", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "delete", "Provider", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Provider", ns, name, audit.ResultSuccess, "")
	h.InvalidateProviders()
	httputil.WriteData(w, map[string]string{"message": "Deleted provider " + name})
}

// HandleSuspendProvider suspends or resumes a Flux notification Provider.
func (h *Handler) HandleSuspendProvider(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "patch", "notification.toolkit.fluxcd.io", "providers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to modify this provider", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := SuspendProvider(r.Context(), dynClient, ns, name, req.Suspend); err != nil {
		h.auditLog(r, user, ActionNotificationSuspend, "Provider", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "patch", "Provider", ns, name)
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, "Provider", ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.InvalidateProviders()

	msg := "Suspended provider " + name
	if !req.Suspend {
		msg = "Resumed provider " + name
	}
	httputil.WriteData(w, map[string]string{"message": msg})
}

// ---------- alerts ----------

// HandleListAlerts returns all Flux notification alerts, RBAC-filtered.
func (h *Handler) HandleListAlerts(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	allAlerts, err := h.fetchAlerts()
	if err != nil {
		h.Logger.Error("failed to fetch notification alerts", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch alerts", "")
		return
	}

	alerts := filterByRBAC(r.Context(), h.AccessChecker, user, allAlerts, "alerts")

	if ns := r.URL.Query().Get("namespace"); ns != "" {
		var filtered []NormalizedAlert
		for _, a := range alerts {
			if a.Namespace == ns {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	}

	httputil.WriteData(w, struct {
		Alerts []NormalizedAlert `json:"alerts"`
		Total  int              `json:"total"`
	}{
		Alerts: alerts,
		Total:  len(alerts),
	})
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "create", "notification.toolkit.fluxcd.io", "alerts", input.Namespace)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to create alerts in this namespace", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	alert, err := CreateAlert(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Alert", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "create", "Alert", input.Namespace, input.Name)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Alert", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.InvalidateAlerts()
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "update", "notification.toolkit.fluxcd.io", "alerts", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to update this alert", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	alert, err := UpdateAlert(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionUpdate, "Alert", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "update", "Alert", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Alert", ns, name, audit.ResultSuccess, "")
	h.InvalidateAlerts()
	httputil.WriteData(w, alert)
}

// HandleDeleteAlert deletes a Flux notification Alert.
func (h *Handler) HandleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "delete", "notification.toolkit.fluxcd.io", "alerts", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to delete this alert", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := DeleteAlert(r.Context(), dynClient, ns, name); err != nil {
		h.auditLog(r, user, audit.ActionDelete, "Alert", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "delete", "Alert", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Alert", ns, name, audit.ResultSuccess, "")
	h.InvalidateAlerts()
	httputil.WriteData(w, map[string]string{"message": "Deleted alert " + name})
}

// HandleSuspendAlert suspends or resumes a Flux notification Alert.
func (h *Handler) HandleSuspendAlert(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "patch", "notification.toolkit.fluxcd.io", "alerts", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to modify this alert", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := SuspendAlert(r.Context(), dynClient, ns, name, req.Suspend); err != nil {
		h.auditLog(r, user, ActionNotificationSuspend, "Alert", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "patch", "Alert", ns, name)
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, "Alert", ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.InvalidateAlerts()

	msg := "Suspended alert " + name
	if !req.Suspend {
		msg = "Resumed alert " + name
	}
	httputil.WriteData(w, map[string]string{"message": msg})
}

// ---------- receivers ----------

// HandleListReceivers returns all Flux notification receivers, RBAC-filtered.
func (h *Handler) HandleListReceivers(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	allReceivers, err := h.fetchReceivers()
	if err != nil {
		h.Logger.Error("failed to fetch notification receivers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch receivers", "")
		return
	}

	receivers := filterByRBAC(r.Context(), h.AccessChecker, user, allReceivers, "receivers")

	if ns := r.URL.Query().Get("namespace"); ns != "" {
		var filtered []NormalizedReceiver
		for _, rv := range receivers {
			if rv.Namespace == ns {
				filtered = append(filtered, rv)
			}
		}
		receivers = filtered
	}

	httputil.WriteData(w, struct {
		Receivers []NormalizedReceiver `json:"receivers"`
		Total     int                 `json:"total"`
	}{
		Receivers: receivers,
		Total:     len(receivers),
	})
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "create", "notification.toolkit.fluxcd.io", "receivers", input.Namespace)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to create receivers in this namespace", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	receiver, err := CreateReceiver(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Receiver", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "create", "Receiver", input.Namespace, input.Name)
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Receiver", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.InvalidateReceivers()
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

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "update", "notification.toolkit.fluxcd.io", "receivers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to update this receiver", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	receiver, err := UpdateReceiver(r.Context(), dynClient, ns, name, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionUpdate, "Receiver", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "update", "Receiver", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Receiver", ns, name, audit.ResultSuccess, "")
	h.InvalidateReceivers()
	httputil.WriteData(w, receiver)
}

// HandleDeleteReceiver deletes a Flux notification Receiver.
func (h *Handler) HandleDeleteReceiver(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "delete", "notification.toolkit.fluxcd.io", "receivers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to delete this receiver", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := DeleteReceiver(r.Context(), dynClient, ns, name); err != nil {
		h.auditLog(r, user, audit.ActionDelete, "Receiver", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "delete", "Receiver", ns, name)
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Receiver", ns, name, audit.ResultSuccess, "")
	h.InvalidateReceivers()
	httputil.WriteData(w, map[string]string{"message": "Deleted receiver " + name})
}

// HandleSuspendReceiver suspends or resumes a Flux notification Receiver.
func (h *Handler) HandleSuspendReceiver(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "patch", "notification.toolkit.fluxcd.io", "receivers", ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to modify this receiver", "")
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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	if err := SuspendReceiver(r.Context(), dynClient, ns, name, req.Suspend); err != nil {
		h.auditLog(r, user, ActionNotificationSuspend, "Receiver", ns, name, audit.ResultFailure, err.Error())
		h.writeK8sError(w, err, "patch", "Receiver", ns, name)
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, "Receiver", ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.InvalidateReceivers()

	msg := "Suspended receiver " + name
	if !req.Suspend {
		msg = "Resumed receiver " + name
	}
	httputil.WriteData(w, map[string]string{"message": msg})
}
