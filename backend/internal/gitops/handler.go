package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// Handler serves GitOps HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *GitOpsDiscoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
	AuditLogger   audit.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedApps
	cacheGen   uint64 // incremented on invalidation; prevents stale writes
}

type cachedApps struct {
	apps      []NormalizedApp
	fetchedAt time.Time
}

const cacheTTL = 30 * time.Second

// toolGVR resolves a tool prefix to its Kubernetes API group and resource.
func toolGVR(toolPrefix string) (apiGroup, resource string, ok bool) {
	switch toolPrefix {
	case "argo":
		return "argoproj.io", "applications", true
	case "flux-ks":
		return "kustomize.toolkit.fluxcd.io", "kustomizations", true
	case "flux-hr":
		return "helm.toolkit.fluxcd.io", "helmreleases", true
	default:
		return "", "", false
	}
}

// toolPrefixForApp returns the composite ID prefix for a NormalizedApp.
func toolPrefixForApp(app NormalizedApp) string {
	switch {
	case app.Tool == ToolArgoCD:
		return "argo"
	case app.Kind == "HelmRelease":
		return "flux-hr"
	default:
		return "flux-ks"
	}
}

// fetchApps returns cached application data, refreshing if stale.
// Cache is populated using the service account; callers must RBAC-filter.
func (h *Handler) fetchApps(ctx context.Context) ([]NormalizedApp, error) {
	h.cacheMu.RLock()
	if h.cachedData != nil && time.Since(h.cachedData.fetchedAt) < cacheTTL {
		apps := h.cachedData.apps
		h.cacheMu.RUnlock()
		return apps, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("fetch", func() (any, error) {
		return h.doFetch(ctx)
	})
	if err != nil {
		return nil, err
	}
	data := result.(*cachedApps)
	return data.apps, nil
}

// doFetch queries both engines based on discovery and merges results.
func (h *Handler) doFetch(ctx context.Context) (*cachedApps, error) {
	// Capture current generation to detect concurrent invalidations.
	h.cacheMu.RLock()
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	dynClient := h.K8sClient.BaseDynamicClient()
	status := h.Discoverer.Status()

	var allApps []NormalizedApp

	type fetchResult struct {
		apps []NormalizedApp
		err  error
	}

	var wg sync.WaitGroup
	argoCh := make(chan fetchResult, 1)
	fluxCh := make(chan fetchResult, 1)

	// Fetch Argo CD applications
	if status.ArgoCD != nil && status.ArgoCD.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r fetchResult
			r.apps, r.err = ListArgoApplications(ctx, dynClient)
			argoCh <- r
		}()
	} else {
		argoCh <- fetchResult{}
	}

	// Fetch Flux Kustomizations + HelmReleases in parallel
	if status.FluxCD != nil && status.FluxCD.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ks, hr []NormalizedApp
			var ksErr, hrErr error
			var inner sync.WaitGroup
			inner.Add(2)
			go func() {
				defer inner.Done()
				ks, ksErr = ListFluxKustomizations(ctx, dynClient)
			}()
			go func() {
				defer inner.Done()
				hr, hrErr = ListFluxHelmReleases(ctx, dynClient)
			}()
			inner.Wait()

			var r fetchResult
			if ksErr != nil {
				r.err = ksErr
			} else if hrErr != nil {
				r.err = hrErr
			}
			r.apps = append(ks, hr...)
			fluxCh <- r
		}()
	} else {
		fluxCh <- fetchResult{}
	}

	wg.Wait()

	ar := <-argoCh
	fr := <-fluxCh

	if ar.err != nil {
		h.Logger.Warn("argocd fetch error", "error", ar.err)
	} else {
		allApps = append(allApps, ar.apps...)
	}

	if fr.err != nil {
		h.Logger.Warn("flux fetch error", "error", fr.err)
	} else {
		allApps = append(allApps, fr.apps...)
	}

	data := &cachedApps{
		apps:      allApps,
		fetchedAt: time.Now(),
	}

	// Only write cache if no invalidation occurred during fetch.
	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cachedData = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// HandleStatus returns the GitOps tool detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status()

	// Strip details for non-admin users
	if !auth.IsAdmin(user) {
		if status.ArgoCD != nil {
			stripped := *status.ArgoCD
			stripped.Namespace = ""
			stripped.Controllers = nil
			status.ArgoCD = &stripped
		}
		if status.FluxCD != nil {
			stripped := *status.FluxCD
			stripped.Namespace = ""
			stripped.Controllers = nil
			status.FluxCD = &stripped
		}
	}

	httputil.WriteData(w, status)
}

// HandleListApplications returns all normalized GitOps applications.
func (h *Handler) HandleListApplications(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	apps, err := h.fetchApps(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch gitops applications", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch applications", "")
		return
	}

	// RBAC filter
	apps = h.filterAppsByRBAC(r.Context(), user, apps)

	// Apply query param filters
	q := r.URL.Query()
	if tool := q.Get("tool"); tool != "" {
		apps = filterApps(apps, func(a NormalizedApp) bool { return a.Tool == Tool(tool) })
	}
	if ns := q.Get("namespace"); ns != "" {
		apps = filterApps(apps, func(a NormalizedApp) bool {
			return a.Namespace == ns || a.DestinationNamespace == ns
		})
	}
	if ss := q.Get("syncStatus"); ss != "" {
		apps = filterApps(apps, func(a NormalizedApp) bool { return a.SyncStatus == SyncStatus(ss) })
	}
	if hs := q.Get("healthStatus"); hs != "" {
		apps = filterApps(apps, func(a NormalizedApp) bool { return a.HealthStatus == HealthStatus(hs) })
	}

	// Sort: out-of-sync/failed first, then by name
	sort.Slice(apps, func(i, j int) bool {
		si := syncSortOrder(apps[i].SyncStatus)
		sj := syncSortOrder(apps[j].SyncStatus)
		if si != sj {
			return si < sj
		}
		return apps[i].Name < apps[j].Name
	})

	// Build response with summary counts
	httputil.WriteData(w, struct {
		Applications []NormalizedApp `json:"applications"`
		Summary      AppListMetadata `json:"summary"`
	}{
		Applications: apps,
		Summary:      computeMetadata(apps),
	})
}

// HandleGetApplication returns a single application's full detail.
// Uses user impersonation for the API call (not service account).
func (h *Handler) HandleGetApplication(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	toolPrefix, namespace, name, err := parseCompositeID(id)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid application ID", err.Error())
		return
	}

	// Build impersonating dynamic client for this user
	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	var detail *AppDetail
	switch toolPrefix {
	case "argo":
		detail, err = GetArgoAppDetail(r.Context(), dynClient, namespace, name)
	case "flux-ks":
		detail, err = GetFluxAppDetail(r.Context(), dynClient, "Kustomization", namespace, name)
	case "flux-hr":
		detail, err = GetFluxAppDetail(r.Context(), dynClient, "HelmRelease", namespace, name)
	default:
		httputil.WriteError(w, http.StatusBadRequest, "unknown tool prefix", toolPrefix)
		return
	}

	if err != nil {
		h.Logger.Error("failed to get application detail", "id", id, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get application", "")
		return
	}

	httputil.WriteData(w, detail)
}

// filterAppsByRBAC removes apps the user cannot access.
func (h *Handler) filterAppsByRBAC(ctx context.Context, user *auth.User, apps []NormalizedApp) []NormalizedApp {
	// Cache RBAC decisions keyed by tool prefix + namespace
	type accessKey struct {
		prefix    string
		namespace string
	}
	access := make(map[accessKey]bool)
	var filtered []NormalizedApp

	for _, app := range apps {
		ns := app.Namespace
		if ns == "" {
			if auth.IsAdmin(user) {
				filtered = append(filtered, app)
			}
			continue
		}

		prefix := toolPrefixForApp(app)
		key := accessKey{prefix, ns}
		allowed, checked := access[key]
		if !checked {
			apiGroup, resource, ok := toolGVR(prefix)
			if !ok {
				continue
			}
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", apiGroup, resource, ns)
			allowed = err == nil && can
			access[key] = allowed
		}

		if allowed {
			filtered = append(filtered, app)
		}
	}

	return filtered
}

// parseCompositeID splits "argo:namespace:name" into (tool, namespace, name).
func parseCompositeID(id string) (tool, namespace, name string, err error) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("invalid composite ID: %q (expected tool:namespace:name)", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// filterApps returns apps matching the predicate.
func filterApps(apps []NormalizedApp, pred func(NormalizedApp) bool) []NormalizedApp {
	var out []NormalizedApp
	for _, a := range apps {
		if pred(a) {
			out = append(out, a)
		}
	}
	return out
}

// syncSortOrder returns a sort key so out-of-sync/failed apps appear first.
func syncSortOrder(s SyncStatus) int {
	switch s {
	case SyncFailed:
		return 0
	case SyncOutOfSync:
		return 1
	case SyncStalled:
		return 2
	case SyncProgressing:
		return 3
	case SyncUnknown:
		return 4
	case SyncSynced:
		return 5
	default:
		return 6
	}
}

// computeMetadata builds summary counts for the response.
func computeMetadata(apps []NormalizedApp) AppListMetadata {
	m := AppListMetadata{Total: len(apps)}
	for _, a := range apps {
		switch a.SyncStatus {
		case SyncSynced:
			m.Synced++
		case SyncOutOfSync, SyncFailed, SyncStalled:
			m.OutOfSync++
		case SyncProgressing:
			m.Progressing++
		}
		switch a.HealthStatus {
		case HealthDegraded:
			m.Degraded++
		case HealthSuspended:
			m.Suspended++
		}
	}
	return m
}

// invalidateCache clears the cached application list so the next REST call re-fetches.
// We intentionally do NOT call fetchGroup.Forget — an in-flight singleflight fetch
// could repopulate the cache with pre-event data if we start a competing fetch.
// Setting cachedData to nil is sufficient: the in-flight fetch will complete and
// cache its result, but the next call after that will see the stale timestamp and re-fetch.
func (h *Handler) invalidateCache() {
	h.cacheMu.Lock()
	h.cachedData = nil
	h.cacheGen++
	h.cacheMu.Unlock()
}

// InvalidateCache is the exported version for use by CRD event handlers.
func (h *Handler) InvalidateCache() {
	h.invalidateCache()
}

// prepareAction extracts the common preamble for action handlers:
// authenticate user, parse composite ID, RBAC check, build impersonating client.
func (h *Handler) prepareAction(w http.ResponseWriter, r *http.Request) (toolPrefix, ns, name string, dynClient dynamic.Interface, user *auth.User, ok bool) {
	user, ok = httputil.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	var err error
	toolPrefix, ns, name, err = parseCompositeID(id)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid application ID", err.Error())
		ok = false
		return
	}

	apiGroup, resource, valid := toolGVR(toolPrefix)
	if !valid {
		httputil.WriteError(w, http.StatusBadRequest, "unknown tool prefix", toolPrefix)
		ok = false
		return
	}

	// RBAC pre-check
	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "patch", apiGroup, resource, ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to modify this application", "")
		ok = false
		return
	}

	dynClient, err = h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		ok = false
		return
	}

	ok = true
	return
}

// auditLog writes an audit entry for a GitOps action.
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

// HandleSync triggers a sync (Argo CD) or reconcile (Flux CD).
func (h *Handler) HandleSync(w http.ResponseWriter, r *http.Request) {
	toolPrefix, ns, name, dynClient, user, ok := h.prepareAction(w, r)
	if !ok {
		return
	}

	var err error
	var kind string

	switch toolPrefix {
	case "argo":
		kind = "Application"
		_, err = SyncArgoApp(r.Context(), dynClient, ns, name, user.KubernetesUsername)
	case "flux-ks":
		kind = "Kustomization"
		_, err = ReconcileFluxResource(r.Context(), dynClient, FluxKustomizationGVR, ns, name)
	case "flux-hr":
		kind = "HelmRelease"
		_, err = ReconcileFluxResource(r.Context(), dynClient, FluxHelmReleaseGVR, ns, name)
	}

	if err != nil {
		h.auditLog(r, user, audit.ActionGitOpsSync, kind, ns, name, audit.ResultFailure, err.Error())
		// Map specific errors to appropriate HTTP status codes
		if strings.Contains(err.Error(), "already in progress") || strings.Contains(err.Error(), "is suspended") {
			httputil.WriteError(w, http.StatusConflict, err.Error(), "")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, "failed to trigger sync", "")
		}
		return
	}

	h.auditLog(r, user, audit.ActionGitOpsSync, kind, ns, name, audit.ResultSuccess, "tool="+toolPrefix)
	h.invalidateCache()
	httputil.WriteData(w, map[string]string{"message": "Sync triggered for " + name})
}

// HandleSuspend suspends or resumes a GitOps application.
func (h *Handler) HandleSuspend(w http.ResponseWriter, r *http.Request) {
	toolPrefix, ns, name, dynClient, user, ok := h.prepareAction(w, r)
	if !ok {
		return
	}

	var req struct {
		Suspend bool `json:"suspend"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	var err error
	var kind string
	var action audit.Action

	if req.Suspend {
		action = audit.ActionGitOpsSuspend
	} else {
		action = audit.ActionGitOpsResume
	}

	switch toolPrefix {
	case "argo":
		kind = "Application"
		if req.Suspend {
			_, err = SuspendArgoApp(r.Context(), dynClient, ns, name)
		} else {
			_, err = ResumeArgoApp(r.Context(), dynClient, ns, name)
		}
	case "flux-ks":
		kind = "Kustomization"
		_, err = SuspendFluxResource(r.Context(), dynClient, FluxKustomizationGVR, ns, name, req.Suspend)
	case "flux-hr":
		kind = "HelmRelease"
		_, err = SuspendFluxResource(r.Context(), dynClient, FluxHelmReleaseGVR, ns, name, req.Suspend)
	}

	if err != nil {
		h.auditLog(r, user, action, kind, ns, name, audit.ResultFailure, err.Error())
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update suspend state", "")
		return
	}

	h.auditLog(r, user, action, kind, ns, name, audit.ResultSuccess, "tool="+toolPrefix)
	h.invalidateCache()

	msg := "Suspended " + name
	if !req.Suspend {
		msg = "Resumed " + name
	}
	httputil.WriteData(w, map[string]string{"message": msg})
}

// HandleRollback triggers a rollback to a specific revision (Argo CD only).
func (h *Handler) HandleRollback(w http.ResponseWriter, r *http.Request) {
	toolPrefix, ns, name, dynClient, user, ok := h.prepareAction(w, r)
	if !ok {
		return
	}

	// Rollback is Argo CD only
	if toolPrefix != "argo" {
		httputil.WriteError(w, http.StatusMethodNotAllowed, "rollback is only supported for Argo CD applications", "")
		return
	}

	var req struct {
		Revision string `json:"revision"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Revision == "" {
		httputil.WriteError(w, http.StatusBadRequest, "revision is required", "")
		return
	}

	_, err := RollbackArgoApp(r.Context(), dynClient, ns, name, req.Revision, user.KubernetesUsername)
	if err != nil {
		h.auditLog(r, user, audit.ActionGitOpsRollback, "Application", ns, name, audit.ResultFailure, err.Error())
		if strings.Contains(err.Error(), "auto-sync") || strings.Contains(err.Error(), "not found in history") {
			httputil.WriteError(w, http.StatusConflict, err.Error(), "")
		} else {
			httputil.WriteError(w, http.StatusInternalServerError, "failed to rollback", "")
		}
		return
	}

	h.auditLog(r, user, audit.ActionGitOpsRollback, "Application", ns, name, audit.ResultSuccess, "revision="+req.Revision)
	h.invalidateCache()
	httputil.WriteData(w, map[string]string{"message": "Rollback triggered for " + name + " to revision " + req.Revision})
}
