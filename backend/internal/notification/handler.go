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

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cached     *cachedNotifications
	cacheGen   uint64 // incremented on invalidation; prevents stale writes
}

type cachedNotifications struct {
	providers []NormalizedProvider
	alerts    []NormalizedAlert
	receivers []NormalizedReceiver
	fetchedAt time.Time
}

// ---------- cache layer ----------

// fetchAll returns cached notification data, refreshing if stale.
// Cache is populated using the service account; callers must RBAC-filter.
func (h *Handler) fetchAll(ctx context.Context) (*cachedNotifications, error) {
	h.cacheMu.RLock()
	if h.cached != nil && time.Since(h.cached.fetchedAt) < cacheTTL {
		c := h.cached
		h.cacheMu.RUnlock()
		return c, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("fetch", func() (any, error) {
		return h.doFetch(ctx)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedNotifications), nil
}

// doFetch queries providers, alerts, and receivers in parallel, then caches the result.
func (h *Handler) doFetch(ctx context.Context) (*cachedNotifications, error) {
	// Capture current generation to detect concurrent invalidations.
	h.cacheMu.RLock()
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	dynClient := h.K8sClient.BaseDynamicClient()

	type fetchResult[T any] struct {
		items []T
		err   error
	}

	var wg sync.WaitGroup
	providerCh := make(chan fetchResult[NormalizedProvider], 1)
	alertCh := make(chan fetchResult[NormalizedAlert], 1)
	receiverCh := make(chan fetchResult[NormalizedReceiver], 1)

	wg.Add(3)
	go func() {
		defer wg.Done()
		var r fetchResult[NormalizedProvider]
		r.items, r.err = ListProviders(ctx, dynClient)
		providerCh <- r
	}()
	go func() {
		defer wg.Done()
		var r fetchResult[NormalizedAlert]
		r.items, r.err = ListAlerts(ctx, dynClient)
		alertCh <- r
	}()
	go func() {
		defer wg.Done()
		var r fetchResult[NormalizedReceiver]
		r.items, r.err = ListReceivers(ctx, dynClient)
		receiverCh <- r
	}()
	wg.Wait()

	pr := <-providerCh
	ar := <-alertCh
	rr := <-receiverCh

	// Log errors but don't fail the whole fetch — partial data is acceptable.
	if pr.err != nil {
		h.Logger.Warn("notification provider fetch error", "error", pr.err)
	}
	if ar.err != nil {
		h.Logger.Warn("notification alert fetch error", "error", ar.err)
	}
	if rr.err != nil {
		h.Logger.Warn("notification receiver fetch error", "error", rr.err)
	}

	// If ALL three failed, return the first error.
	if pr.err != nil && ar.err != nil && rr.err != nil {
		return nil, fmt.Errorf("all notification fetches failed: providers: %w", pr.err)
	}

	data := &cachedNotifications{
		providers: pr.items,
		alerts:    ar.items,
		receivers: rr.items,
		fetchedAt: time.Now(),
	}

	// Only write cache if no invalidation occurred during fetch.
	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cached = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// invalidateCache clears the cached data so the next request re-fetches.
func (h *Handler) invalidateCache() {
	h.cacheMu.Lock()
	h.cached = nil
	h.cacheGen++
	h.cacheMu.Unlock()
}

// InvalidateCache is the exported version for use by CRD event handlers.
func (h *Handler) InvalidateCache() {
	h.invalidateCache()
}

// ---------- RBAC filtering ----------

func (h *Handler) filterProvidersByRBAC(ctx context.Context, user *auth.User, providers []NormalizedProvider) []NormalizedProvider {
	type accessKey struct{ namespace string }
	access := make(map[accessKey]bool)
	var filtered []NormalizedProvider
	for _, p := range providers {
		key := accessKey{p.Namespace}
		allowed, checked := access[key]
		if !checked {
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "notification.toolkit.fluxcd.io", "providers", p.Namespace)
			allowed = err == nil && can
			access[key] = allowed
		}
		if allowed {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func (h *Handler) filterAlertsByRBAC(ctx context.Context, user *auth.User, alerts []NormalizedAlert) []NormalizedAlert {
	type accessKey struct{ namespace string }
	access := make(map[accessKey]bool)
	var filtered []NormalizedAlert
	for _, a := range alerts {
		key := accessKey{a.Namespace}
		allowed, checked := access[key]
		if !checked {
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "notification.toolkit.fluxcd.io", "alerts", a.Namespace)
			allowed = err == nil && can
			access[key] = allowed
		}
		if allowed {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func (h *Handler) filterReceiversByRBAC(ctx context.Context, user *auth.User, receivers []NormalizedReceiver) []NormalizedReceiver {
	type accessKey struct{ namespace string }
	access := make(map[accessKey]bool)
	var filtered []NormalizedReceiver
	for _, r := range receivers {
		key := accessKey{r.Namespace}
		allowed, checked := access[key]
		if !checked {
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "notification.toolkit.fluxcd.io", "receivers", r.Namespace)
			allowed = err == nil && can
			access[key] = allowed
		}
		if allowed {
			filtered = append(filtered, r)
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

// ---------- status ----------

// HandleStatus returns the Flux Notification Controller availability status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := h.fetchAll(r.Context())
	available := err == nil

	status := NotificationStatus{
		Available: available,
	}
	if available {
		status.ProviderCount = len(data.providers)
		status.AlertCount = len(data.alerts)
		status.ReceiverCount = len(data.receivers)
	}

	httputil.WriteData(w, status)
}

// ---------- providers ----------

// HandleListProviders returns all Flux notification providers, RBAC-filtered.
func (h *Handler) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	data, err := h.fetchAll(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch notification providers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch providers", "")
		return
	}

	providers := h.filterProvidersByRBAC(r.Context(), user, data.providers)

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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	provider, err := CreateProvider(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Provider", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create provider", "")
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Provider", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update provider", "")
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Provider", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete provider", "")
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Provider", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update suspend state", "")
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, "Provider", ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.invalidateCache()

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

	data, err := h.fetchAll(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch notification alerts", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch alerts", "")
		return
	}

	alerts := h.filterAlertsByRBAC(r.Context(), user, data.alerts)

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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	alert, err := CreateAlert(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Alert", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create alert", "")
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Alert", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update alert", "")
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Alert", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete alert", "")
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Alert", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update suspend state", "")
		return
	}

	h.auditLog(r, user, ActionNotificationSuspend, "Alert", ns, name, audit.ResultSuccess, fmt.Sprintf("suspend=%v", req.Suspend))
	h.invalidateCache()

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

	data, err := h.fetchAll(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch notification receivers", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch receivers", "")
		return
	}

	receivers := h.filterReceiversByRBAC(r.Context(), user, data.receivers)

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

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	receiver, err := CreateReceiver(r.Context(), dynClient, input.Namespace, input)
	if err != nil {
		h.auditLog(r, user, audit.ActionCreate, "Receiver", input.Namespace, input.Name, audit.ResultFailure, err.Error())
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create receiver", "")
		return
	}

	h.auditLog(r, user, audit.ActionCreate, "Receiver", input.Namespace, input.Name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update receiver", "")
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "Receiver", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete receiver", "")
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "Receiver", ns, name, audit.ResultSuccess, "")
	h.invalidateCache()
	httputil.WriteData(w, map[string]string{"message": "Deleted receiver " + name})
}
