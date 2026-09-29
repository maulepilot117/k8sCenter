package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/gitprovider"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// NotificationEmitter is the part of the notification service GitOps uses.
type NotificationEmitter interface {
	Emit(ctx context.Context, n notifications.Notification)
}

// Handler serves GitOps HTTP endpoints for the cluster a request selects.
// The local cluster is read through a service-account cache and the local
// Discoverer; a remote cluster through Clients, as the requesting identity,
// with its lists held briefly per identity in remote.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *GitOpsDiscoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger
	AuditLogger   audit.Logger
	CommitCache   *gitprovider.CommitCache
	NotifService  NotificationEmitter
	Clients       k8s.ClusterClients
	Presence      *k8s.Presence

	remoteOnce sync.Once
	remote     *remotecache.Cache[*snapshot]

	// baseDynOverride is a test-only seam for the local service-account
	// client; production leaves it nil.
	baseDynOverride dynamic.Interface

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedApps
	cacheGen   uint64 // incremented on invalidation; prevents stale writes

	appSetFetchGroup singleflight.Group
	appSetMu         sync.RWMutex
	cachedAppSets    *cachedAppSetData
	appSetCacheGen   uint64
}

type cachedApps struct {
	apps      []NormalizedApp
	fetchedAt time.Time
}

type cachedAppSetData struct {
	appSets   []NormalizedAppSet
	fetchedAt time.Time
}

const cacheTTL = 30 * time.Second

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// toolGVR resolves a tool prefix to its Kubernetes API group and resource.
func toolGVR(toolPrefix string) (apiGroup, resource string, ok bool) {
	switch toolPrefix {
	case "argo":
		return "argoproj.io", "applications", true
	case "flux-ks":
		return "kustomize.toolkit.fluxcd.io", "kustomizations", true
	case "flux-hr":
		return "helm.toolkit.fluxcd.io", "helmreleases", true
	case "argo-as":
		return "argoproj.io", "applicationsets", true
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

	dynClient := h.baseDyn()
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
			recoverutil.Safe(h.Logger, "gitops argo-fetch", func() {
				r.apps, r.err = ListArgoApplications(ctx, dynClient)
			})
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
			var r fetchResult
			recoverutil.Safe(h.Logger, "gitops flux-fetch", func() {
				var ks, hr []NormalizedApp
				var ksErr, hrErr error
				var inner sync.WaitGroup
				inner.Add(2)
				go func() {
					defer inner.Done()
					recoverutil.Safe(h.Logger, "gitops flux-ks-fetch", func() {
						ks, ksErr = ListFluxKustomizations(ctx, dynClient)
					})
				}()
				go func() {
					defer inner.Done()
					recoverutil.Safe(h.Logger, "gitops flux-hr-fetch", func() {
						hr, hrErr = ListFluxHelmReleases(ctx, dynClient)
					})
				}()
				inner.Wait()
				if ksErr != nil {
					r.err = ksErr
				} else if hrErr != nil {
					r.err = hrErr
				}
				r.apps = append(ks, hr...)
			})
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

	var status GitOpsStatus
	if isLocal(r.Context()) {
		status = h.Discoverer.Status()
	} else {
		// On a remote cluster a failure to read discovery is a status with a
		// reason, not an error (R-8 KTD5).
		var err error
		status, _, err = h.remoteDiscovery(r.Context(), middleware.ClusterIDFromContext(r.Context()), user)
		if err != nil {
			status = GitOpsStatus{Detected: ToolNone, Reason: string(reasonFor(err)), LastChecked: time.Now().UTC().Format(time.RFC3339)}
		}
	}

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

	apps, coverage, err := h.loadApps(r.Context(), user)
	if err != nil {
		h.writeLoadError(w, r, err, "failed to fetch applications")
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
		Applications []NormalizedApp  `json:"applications"`
		Summary      AppListMetadata  `json:"summary"`
		Coverage     []SourceCoverage `json:"coverage,omitempty"`
	}{
		Applications: apps,
		Summary:      computeMetadata(apps),
		Coverage:     coverage,
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

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
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
		writeClusterError(w, r, err, "failed to get application")
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
			can, err := h.AccessChecker.CanAccessGroupResource(ctx, middleware.ClusterIDFromContext(ctx), user.KubernetesUsername, user.KubernetesGroups, "list", apiGroup, resource, ns)
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
// The id may arrive URL-encoded from chi.URLParam, so unescape first.
func parseCompositeID(id string) (tool, namespace, name string, err error) {
	decoded, uerr := url.PathUnescape(id)
	if uerr != nil {
		decoded = id // fall back to raw value
	}
	parts := strings.SplitN(decoded, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("invalid composite ID: %q (expected tool:namespace:name)", decoded)
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

// notifySyncChanged emits the sync-status notification for clusterID ("" when
// the change came from a local informer event).
func (h *Handler) notifySyncChanged(clusterID string) {
	if h.NotifService == nil {
		return
	}
	go recoverutil.Safe(h.Logger, "gitops notify", func() {
		h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:    notifications.SourceGitOps,
			Severity:  notifications.SeverityInfo,
			Title:     "GitOps sync status changed",
			Message:   "A GitOps application sync status has changed. Check the GitOps dashboard for details.",
			ClusterID: clusterID,
		})
	})
}

// InvalidateCache is the exported version for use by CRD event handlers.
func (h *Handler) InvalidateCache() {
	h.invalidateCache()
	h.notifySyncChanged("")
}

// afterAppWrite makes the next read see an application write: the local
// cache is dropped, or on a remote cluster, which sends no informer events,
// every identity's cached view of it (R-8 KTD7). The notification names the
// cluster that was written.
func (h *Handler) afterAppWrite(ctx context.Context) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) {
		h.invalidateCache()
	} else {
		h.EvictRemoteCache(clusterID)
	}
	h.notifySyncChanged(clusterID)
}

// afterAppSetWrite is afterAppWrite for ApplicationSets, which notify nothing.
func (h *Handler) afterAppSetWrite(ctx context.Context) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) {
		h.invalidateAppSetCache()
	} else {
		h.EvictRemoteCache(clusterID)
	}
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
	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups, "patch", apiGroup, resource, ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to modify this application", "")
		ok = false
		return
	}

	dynClient, ok = h.dynamicClient(w, r, user)
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
		h.auditLog(r, user, audit.ActionGitOpsSync, kind, ns, name, auditResult(err), err.Error())
		// Map specific errors to appropriate HTTP status codes
		if strings.Contains(err.Error(), "already in progress") || strings.Contains(err.Error(), "is suspended") {
			httputil.WriteError(w, http.StatusConflict, err.Error(), "")
		} else {
			writeClusterError(w, r, err, "failed to trigger sync")
		}
		return
	}

	h.auditLog(r, user, audit.ActionGitOpsSync, kind, ns, name, audit.ResultSuccess, "tool="+toolPrefix)
	h.afterAppWrite(r.Context())
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
		h.auditLog(r, user, action, kind, ns, name, auditResult(err), err.Error())
		writeClusterError(w, r, err, "failed to update suspend state")
		return
	}

	h.auditLog(r, user, action, kind, ns, name, audit.ResultSuccess, "tool="+toolPrefix)
	h.afterAppWrite(r.Context())

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
		h.auditLog(r, user, audit.ActionGitOpsRollback, "Application", ns, name, auditResult(err), err.Error())
		if strings.Contains(err.Error(), "auto-sync") || strings.Contains(err.Error(), "not found in history") {
			httputil.WriteError(w, http.StatusConflict, err.Error(), "")
		} else {
			writeClusterError(w, r, err, "failed to rollback")
		}
		return
	}

	h.auditLog(r, user, audit.ActionGitOpsRollback, "Application", ns, name, audit.ResultSuccess, "revision="+req.Revision)
	h.afterAppWrite(r.Context())
	httputil.WriteData(w, map[string]string{"message": "Rollback triggered for " + name + " to revision " + req.Revision})
}

// fetchAppSets returns cached ApplicationSet data, refreshing if stale.
func (h *Handler) fetchAppSets(ctx context.Context) ([]NormalizedAppSet, error) {
	h.appSetMu.RLock()
	if h.cachedAppSets != nil && time.Since(h.cachedAppSets.fetchedAt) < cacheTTL {
		appSets := h.cachedAppSets.appSets
		h.appSetMu.RUnlock()
		return appSets, nil
	}
	h.appSetMu.RUnlock()

	result, err, _ := h.appSetFetchGroup.Do("fetch-appsets", func() (any, error) {
		return h.doFetchAppSets(ctx)
	})
	if err != nil {
		return nil, err
	}
	data := result.(*cachedAppSetData)
	return data.appSets, nil
}

func (h *Handler) doFetchAppSets(ctx context.Context) (*cachedAppSetData, error) {
	h.appSetMu.RLock()
	gen := h.appSetCacheGen
	h.appSetMu.RUnlock()

	appSets, err := ListArgoAppSets(ctx, h.baseDyn())
	if err != nil {
		return nil, err
	}

	data := &cachedAppSetData{
		appSets:   appSets,
		fetchedAt: time.Now(),
	}

	h.appSetMu.Lock()
	if h.appSetCacheGen == gen {
		h.cachedAppSets = data
	}
	h.appSetMu.Unlock()

	return data, nil
}

func (h *Handler) invalidateAppSetCache() {
	h.appSetMu.Lock()
	h.cachedAppSets = nil
	h.appSetCacheGen++
	h.appSetMu.Unlock()
}

// InvalidateAppSetCache is the exported version for use by CRD event handlers.
func (h *Handler) InvalidateAppSetCache() {
	h.invalidateAppSetCache()
}

// HandleListAppSets returns all normalized ApplicationSets with child app summaries.
func (h *Handler) HandleListAppSets(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var appSets []NormalizedAppSet
	if isLocal(r.Context()) {
		var err error
		appSets, err = h.fetchAppSets(r.Context())
		if err != nil {
			h.Logger.Error("failed to fetch applicationsets", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch applicationsets", "")
			return
		}
	} else {
		snap, err := h.load(r.Context(), user)
		if err != nil {
			h.writeLoadError(w, r, err, "failed to fetch applicationsets")
			return
		}
		if err := snap.failed["applicationsets"]; err != nil {
			httputil.WriteRemoteError(w, err)
			return
		}
		appSets = snap.appSets
	}

	// RBAC filter
	var filtered []NormalizedAppSet
	for _, as := range appSets {
		if as.Namespace == "" {
			if auth.IsAdmin(user) {
				filtered = append(filtered, as)
			}
			continue
		}
		can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups, "list", "argoproj.io", "applicationsets", as.Namespace)
		if err == nil && can {
			filtered = append(filtered, as)
		}
	}

	// Fetch child apps per appset using label selector with user impersonation.
	// There is no service-account fallback: counts must reflect what this
	// user may list on this cluster.
	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}
	for i := range filtered {
		as := &filtered[i]
		labelSelector := fmt.Sprintf("argocd.argoproj.io/application-set-name=%s", as.Name)
		list, err := dynClient.Resource(ArgoApplicationGVR).Namespace("").List(r.Context(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			h.Logger.Warn("failed to list child apps for appset", "appset", as.Name, "error", err)
			continue
		}
		as.GeneratedAppCount = len(list.Items)

		// Build summary from child apps
		childNormalized := make([]NormalizedApp, 0, len(list.Items))
		for j := range list.Items {
			childNormalized = append(childNormalized, NormalizeArgoApp(&list.Items[j]))
		}
		as.Summary = computeMetadata(childNormalized)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})

	httputil.WriteData(w, struct {
		ApplicationSets []NormalizedAppSet `json:"applicationSets"`
		Total           int                `json:"total"`
	}{
		ApplicationSets: filtered,
		Total:           len(filtered),
	})
}

// HandleGetAppSet returns a single ApplicationSet's full detail including child applications.
func (h *Handler) HandleGetAppSet(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	toolPrefix, namespace, name, err := parseCompositeID(id)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid applicationset ID", err.Error())
		return
	}

	if toolPrefix != "argo-as" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid tool prefix for applicationset", toolPrefix)
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	detail, err := GetArgoAppSetDetail(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get applicationset detail", "id", id, "error", err)
		writeClusterError(w, r, err, "failed to get applicationset")
		return
	}

	// Fetch child applications via label selector
	labelSelector := fmt.Sprintf("argocd.argoproj.io/application-set-name=%s", name)
	list, err := dynClient.Resource(ArgoApplicationGVR).Namespace("").List(r.Context(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		h.Logger.Warn("failed to list child apps for appset detail", "appset", name, "error", err)
	} else {
		childApps := make([]NormalizedApp, 0, len(list.Items))
		for i := range list.Items {
			childApps = append(childApps, NormalizeArgoApp(&list.Items[i]))
		}
		detail.Applications = childApps
		detail.AppSet.GeneratedAppCount = len(childApps)
		detail.AppSet.Summary = computeMetadata(childApps)
	}

	httputil.WriteData(w, detail)
}

// HandleRefreshAppSet triggers a refresh on an ApplicationSet.
func (h *Handler) HandleRefreshAppSet(w http.ResponseWriter, r *http.Request) {
	toolPrefix, ns, name, dynClient, user, ok := h.prepareAction(w, r)
	if !ok {
		return
	}

	if toolPrefix != "argo-as" {
		httputil.WriteError(w, http.StatusBadRequest, "refresh is only supported for ApplicationSets", "")
		return
	}

	err := RefreshArgoAppSet(r.Context(), dynClient, ns, name)
	if err != nil {
		h.auditLog(r, user, audit.ActionGitOpsSync, "ApplicationSet", ns, name, auditResult(err), err.Error())
		writeClusterError(w, r, err, "failed to refresh applicationset")
		return
	}

	h.auditLog(r, user, audit.ActionGitOpsSync, "ApplicationSet", ns, name, audit.ResultSuccess, "action=refresh")
	h.afterAppSetWrite(r.Context())
	httputil.WriteData(w, map[string]string{"message": "Refresh triggered for " + name})
}

// HandleDeleteAppSet deletes an ApplicationSet.
func (h *Handler) HandleDeleteAppSet(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	toolPrefix, ns, name, err := parseCompositeID(id)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid applicationset ID", err.Error())
		return
	}

	if toolPrefix != "argo-as" {
		httputil.WriteError(w, http.StatusBadRequest, "delete is only supported for ApplicationSets", "")
		return
	}

	apiGroup, resource, _ := toolGVR(toolPrefix)

	can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups, "delete", apiGroup, resource, ns)
	if err != nil || !can {
		httputil.WriteError(w, http.StatusForbidden, "you do not have permission to delete this applicationset", "")
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	err = DeleteArgoAppSet(r.Context(), dynClient, ns, name)
	if err != nil {
		h.auditLog(r, user, audit.ActionDelete, "ApplicationSet", ns, name, auditResult(err), err.Error())
		writeClusterError(w, r, err, "failed to delete applicationset")
		return
	}

	h.auditLog(r, user, audit.ActionDelete, "ApplicationSet", ns, name, audit.ResultSuccess, "")
	h.afterAppSetWrite(r.Context())
	httputil.WriteData(w, map[string]string{"message": "Deleted applicationset " + name})
}

// HandleGetCommits returns commit metadata for a set of SHAs from a git repository.
// GET /api/v1/gitops/commits?repoURL=<url>&shas=<sha1,sha2,...>
func (h *Handler) HandleGetCommits(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if h.CommitCache == nil || !h.CommitCache.HasGitHub() {
		httputil.WriteData(w, gitprovider.ToResponse(nil, nil))
		return
	}

	repoURL := r.URL.Query().Get("repoURL")
	shasParam := r.URL.Query().Get("shas")

	if repoURL == "" || shasParam == "" {
		httputil.WriteError(w, http.StatusBadRequest, "repoURL and shas parameters are required", "")
		return
	}

	// Parse and normalize the repo URL
	ref, err := gitprovider.ParseRepoURL(repoURL)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid repoURL", "")
		return
	}

	// Parse SHAs, filter to valid hex format, cap at 50
	shas := strings.Split(shasParam, ",")
	var validSHAs []string
	for _, s := range shas {
		s = strings.TrimSpace(s)
		if shaPattern.MatchString(s) {
			validSHAs = append(validSHAs, s)
		}
	}
	shas = validSHAs
	if len(shas) == 0 {
		httputil.WriteData(w, gitprovider.ToResponse(nil, nil))
		return
	}
	if len(shas) > 50 {
		shas = shas[:50]
	}

	// RBAC: validate repoURL matches at least one app visible to this user
	// on the same cluster the request selects.
	apps, _, err := h.loadApps(r.Context(), user)
	if err != nil {
		h.writeLoadError(w, r, err, "failed to validate access")
		return
	}
	apps = h.filterAppsByRBAC(r.Context(), user, apps)

	canonicalURL := ref.CanonicalURL()
	if !repoVisibleToUser(apps, repoURL, canonicalURL) {
		httputil.WriteError(w, http.StatusForbidden, "no visible application uses this repository", "")
		return
	}

	// Fetch commits (cache + GitHub API)
	commits, unavailable := h.CommitCache.GetCommits(r.Context(), canonicalURL, ref.Owner, ref.Repo, shas)
	httputil.WriteData(w, gitprovider.ToResponse(commits, unavailable))
}

// repoVisibleToUser checks if any user-visible app uses the given repo URL.
func repoVisibleToUser(apps []NormalizedApp, rawURL, canonicalURL string) bool {
	for _, app := range apps {
		if app.Source.RepoURL == "" {
			continue
		}
		// Try exact match first (fast path)
		if app.Source.RepoURL == rawURL {
			return true
		}
		// Parse and normalize the app's repo URL for canonical comparison
		appRef, err := gitprovider.ParseRepoURL(app.Source.RepoURL)
		if err != nil {
			continue
		}
		if appRef.CanonicalURL() == canonicalURL {
			return true
		}
	}
	return false
}

// listTimeout bounds one fetch of every remote GitOps list.
const listTimeout = 10 * time.Second

// appSources are the lists that make up the applications view.
var appSources = []string{"applications", "kustomizations", "helmreleases"}

// errDiscoveryUnavailable means a remote cluster's discovery could not be
// read, so which GitOps tools it serves is unknown.
var errDiscoveryUnavailable = errors.New("gitops: discovery on the selected cluster is unavailable")

// errListPanicked stands in for the result of a list whose goroutine
// panicked; recoverutil logs the panic itself.
var errListPanicked = errors.New("gitops: list panicked")

// targetError is a failure to resolve a client or schema for the selected
// cluster, answered by httputil.WriteTargetError rather than as a failure of
// a call the cluster received.
type targetError struct{ err error }

func (e targetError) Error() string { return e.err.Error() }
func (e targetError) Unwrap() error { return e.err }

// snapshot is one remote cluster's GitOps state as one identity sees it.
type snapshot struct {
	status  GitOpsStatus
	apps    []NormalizedApp
	appSets []NormalizedAppSet
	// failed holds the error each failed list returned, keyed by resource
	// name. A list whose resource type is gone counts as empty, not failed.
	failed map[string]error
}

func (h *Handler) remoteCache() *remotecache.Cache[*snapshot] {
	h.remoteOnce.Do(func() {
		h.remote = remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return h.remote
}

// EvictRemoteCache drops every identity's cached view of clusterID.
// Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remoteCache().EvictCluster(clusterID)
}

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// baseDyn is the local service-account dynamic client.
func (h *Handler) baseDyn() dynamic.Interface {
	if h.baseDynOverride != nil {
		return h.baseDynOverride
	}
	// nolint:cluster-routing local path: the service-account cache serves the local cluster only; remote reads go through fetchRemote.
	return h.K8sClient.BaseDynamicClient()
}

// dynamicClient returns a dynamic client impersonating the user on the
// request's cluster, writing the error response when it cannot.
func (h *Handler) dynamicClient(w http.ResponseWriter, r *http.Request, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.Clients.DynamicClientForCluster(r.Context(), middleware.ClusterIDFromContext(r.Context()), user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		if isLocal(r.Context()) {
			h.Logger.Error("failed to create impersonating client", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		} else {
			httputil.WriteTargetError(w, err)
		}
		return nil, false
	}
	return client, true
}

// writeClusterError answers a failed call to the request's cluster. The
// local cluster keeps its historical 500 and message; a remote cluster's
// error is classified without leaking its text.
func writeClusterError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteRemoteError(w, err)
}

// auditResult records a refusal by the cluster as denied rather than failed.
func auditResult(err error) audit.Result {
	if apierrors.IsForbidden(err) {
		return audit.ResultDenied
	}
	return audit.ResultFailure
}

// reasonFor maps a remote discovery failure to its status reason.
func reasonFor(err error) k8s.ReasonCode {
	var target targetError
	if errors.As(err, &target) {
		return k8s.ClassifyTargetErr(target.err)
	}
	return k8s.ReasonDiscoveryUnavailable
}

// writeLoadError answers a failure to read the cluster's GitOps state.
func (h *Handler) writeLoadError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	var target targetError
	switch {
	case isLocal(r.Context()):
		h.Logger.Error(localMsg, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
	case errors.As(err, &target):
		httputil.WriteTargetError(w, target.err)
	case errors.Is(err, errDiscoveryUnavailable):
		httputil.WriteErrorWithReason(w, http.StatusBadGateway, "GitOps discovery on the selected cluster failed", string(k8s.ReasonDiscoveryUnavailable), nil)
	default:
		httputil.WriteRemoteError(w, err)
	}
}

// coverageOf describes each of sources, in order, that has an error in
// failed. It carries a reason code only, never the error text.
func coverageOf(failed map[string]error, sources ...string) []SourceCoverage {
	var out []SourceCoverage
	for _, src := range sources {
		err := failed[src]
		if err == nil {
			continue
		}
		c := SourceCoverage{Source: src, Status: "unavailable", ReasonCode: string(k8s.ReasonUnreachable)}
		switch {
		case apierrors.IsForbidden(err):
			c.Status, c.ReasonCode = "forbidden", string(k8s.ReasonForbidden)
		case apierrors.IsUnauthorized(err):
			c.ReasonCode = string(k8s.ReasonCredentialsInvalid)
		}
		out = append(out, c)
	}
	return out
}

// loadApps returns the applications the request's cluster serves: the
// service-account cache for the local cluster, or a per-identity read of a
// remote one, with coverage for any remote list that failed.
func (h *Handler) loadApps(ctx context.Context, user *auth.User) ([]NormalizedApp, []SourceCoverage, error) {
	if isLocal(ctx) {
		apps, err := h.fetchApps(ctx)
		return apps, nil, err
	}
	snap, err := h.load(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	return snap.apps, coverageOf(snap.failed, appSources...), nil
}

// load returns the remote cluster's GitOps snapshot for the user.
func (h *Handler) load(ctx context.Context, user *auth.User) (*snapshot, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.remoteCache().Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
		return h.fetchRemote(ctx, clusterID, user)
	})
}

// remoteDiscovery reads which GitOps tools a remote cluster serves, as the
// user sees it, and returns the discovery lists it read. A definite absence
// is a status with Reason discovery_missing, not an error.
func (h *Handler) remoteDiscovery(ctx context.Context, clusterID string, user *auth.User) (GitOpsStatus, []*metav1.APIResourceList, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	missing := GitOpsStatus{Detected: ToolNone, Reason: string(k8s.ReasonDiscoveryMissing), LastChecked: now}

	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so a tool installed later is seen within
	// seconds rather than when the schema cache expires.
	absent := true
	for _, gvr := range []schema.GroupVersionResource{ArgoApplicationGVR, FluxKustomizationGVR, FluxHelmReleaseGVR} {
		verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
		if verdict.Installed == nil || *verdict.Installed {
			absent = false
		}
	}
	if absent {
		return missing, nil, nil
	}

	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return GitOpsStatus{}, nil, targetError{err}
	}
	lists, unavailable, failedGroups := k8s.DiscoveryLists(target.Discovery)
	if unavailable {
		return GitOpsStatus{}, nil, errDiscoveryUnavailable
	}
	for _, group := range toolGroups {
		if failedGroups[group] {
			return GitOpsStatus{}, nil, errDiscoveryUnavailable
		}
	}
	status := statusFromLists(lists)
	if status.Detected == ToolNone {
		return missing, lists, nil
	}
	status.LastChecked = now
	return status, lists, nil
}

// fetchRemote reads a remote cluster's GitOps applications and
// ApplicationSets as the user. Each list succeeds or fails on its own; only
// when every list fails is the fetch itself an error.
func (h *Handler) fetchRemote(ctx context.Context, clusterID string, user *auth.User) (*snapshot, error) {
	status, lists, err := h.remoteDiscovery(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{status: status, apps: []NormalizedApp{}, appSets: []NormalizedAppSet{}, failed: map[string]error{}}

	type source struct {
		gvr  schema.GroupVersionResource
		list func(context.Context, dynamic.Interface) error // appends to snap under mu
	}
	var mu sync.Mutex
	appsFrom := func(fn func(context.Context, dynamic.Interface) ([]NormalizedApp, error)) func(context.Context, dynamic.Interface) error {
		return func(ctx context.Context, dyn dynamic.Interface) error {
			apps, err := fn(ctx, dyn)
			if err == nil {
				mu.Lock()
				snap.apps = append(snap.apps, apps...)
				mu.Unlock()
			}
			return err
		}
	}
	candidates := []source{
		{ArgoApplicationGVR, appsFrom(ListArgoApplications)},
		{FluxKustomizationGVR, appsFrom(ListFluxKustomizations)},
		{FluxHelmReleaseGVR, appsFrom(ListFluxHelmReleases)},
		{ArgoApplicationSetGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			appSets, err := ListArgoAppSets(ctx, dyn)
			if err == nil {
				mu.Lock()
				snap.appSets = appSets
				mu.Unlock()
			}
			return err
		}},
	}
	var sources []source
	for _, src := range candidates {
		if k8s.GVRPresentIn(lists, src.gvr.Group, src.gvr.Resource) {
			sources = append(sources, src)
		}
	}
	if len(sources) == 0 {
		return snap, nil
	}

	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, targetError{err}
	}

	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	errs := make([]error, len(sources))
	var g errgroup.Group
	for i, src := range sources {
		errs[i] = errListPanicked // overwritten unless the list panics
		recoverutil.Go(&g, h.Logger, "gitops list "+src.gvr.Resource, func() error {
			errs[i] = src.list(listCtx, dyn)
			return nil
		})
	}
	_ = g.Wait() // every list records its own outcome in errs

	for i, src := range sources {
		switch err := errs[i]; {
		case err == nil:
		case k8s.IsResourceGone(err):
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, src.gvr.GroupResource())
		default:
			snap.failed[src.gvr.Resource] = err
		}
	}
	if len(snap.failed) == len(sources) {
		return nil, errs[0]
	}
	return snap, nil
}
