package gitops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Handler serves GitOps HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *GitOpsDiscoverer
	ClusterRouter *k8s.ClusterRouter
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedApps
}

type cachedApps struct {
	apps      []NormalizedApp
	fetchedAt time.Time
}

const cacheTTL = 30 * time.Second

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

	// Fetch Flux Kustomizations + HelmReleases
	if status.FluxCD != nil && status.FluxCD.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r fetchResult
			ks, ksErr := ListFluxKustomizations(ctx, dynClient)
			if ksErr != nil {
				r.err = ksErr
				fluxCh <- r
				return
			}
			hr, hrErr := ListFluxHelmReleases(ctx, dynClient)
			if hrErr != nil {
				r.err = hrErr
				fluxCh <- r
				return
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

	h.cacheMu.Lock()
	h.cachedData = data
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
		apps = filterByTool(apps, Tool(tool))
	}
	if ns := q.Get("namespace"); ns != "" {
		apps = filterByNamespace(apps, ns)
	}
	if ss := q.Get("syncStatus"); ss != "" {
		apps = filterBySyncStatus(apps, SyncStatus(ss))
	}
	if hs := q.Get("healthStatus"); hs != "" {
		apps = filterByHealthStatus(apps, HealthStatus(hs))
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
func (h *Handler) HandleGetApplication(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	tool, namespace, name, err := parseCompositeID(id)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid application ID", err.Error())
		return
	}

	// RBAC check for the specific namespace
	if !h.canAccessApp(r.Context(), user, tool, namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient := h.K8sClient.BaseDynamicClient()

	var detail *AppDetail
	switch {
	case tool == "argo":
		detail, err = GetArgoAppDetail(r.Context(), dynClient, namespace, name)
	case tool == "flux-ks":
		detail, err = GetFluxAppDetail(r.Context(), dynClient, "Kustomization", namespace, name)
	case tool == "flux-hr":
		detail, err = GetFluxAppDetail(r.Context(), dynClient, "HelmRelease", namespace, name)
	default:
		httputil.WriteError(w, http.StatusBadRequest, "unknown tool prefix", tool)
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
	nsAccess := make(map[string]bool)
	var filtered []NormalizedApp

	for _, app := range apps {
		ns := app.Namespace
		if ns == "" {
			if auth.IsAdmin(user) {
				filtered = append(filtered, app)
			}
			continue
		}

		allowed, checked := nsAccess[ns]
		if !checked {
			allowed = h.checkAppAccess(ctx, user, app.Tool, ns)
			nsAccess[ns] = allowed
		}

		if allowed {
			filtered = append(filtered, app)
		}
	}

	return filtered
}

// checkAppAccess checks if the user can list the relevant CRD in the namespace.
func (h *Handler) checkAppAccess(ctx context.Context, user *auth.User, tool Tool, namespace string) bool {
	var apiGroup, resource string
	switch tool {
	case ToolArgoCD:
		apiGroup = "argoproj.io"
		resource = "applications"
	case ToolFluxCD:
		// For simplicity, check kustomizations access as a proxy
		apiGroup = "kustomize.toolkit.fluxcd.io"
		resource = "kustomizations"
	default:
		return false
	}

	can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", apiGroup, resource, namespace)
	if err != nil {
		return false
	}
	return can
}

// canAccessApp checks RBAC for a single app by its tool prefix and namespace.
func (h *Handler) canAccessApp(ctx context.Context, user *auth.User, toolPrefix, namespace string) bool {
	switch toolPrefix {
	case "argo":
		can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "get", "argoproj.io", "applications", namespace)
		return err == nil && can
	case "flux-ks":
		can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "get", "kustomize.toolkit.fluxcd.io", "kustomizations", namespace)
		return err == nil && can
	case "flux-hr":
		can, err := h.AccessChecker.CanAccessGroupResource(ctx, user.KubernetesUsername, user.KubernetesGroups, "get", "helm.toolkit.fluxcd.io", "helmreleases", namespace)
		return err == nil && can
	default:
		return false
	}
}

// parseCompositeID splits "argo:namespace:name" into (tool, namespace, name).
func parseCompositeID(id string) (tool, namespace, name string, err error) {
	// Format: "tool:namespace:name" — e.g. "argo:argocd:my-app" or "flux-ks:flux-system:my-ks"
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("invalid composite ID: %q (expected tool:namespace:name)", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// Filter helpers

func filterByTool(apps []NormalizedApp, tool Tool) []NormalizedApp {
	var out []NormalizedApp
	for _, a := range apps {
		if a.Tool == tool {
			out = append(out, a)
		}
	}
	return out
}

func filterByNamespace(apps []NormalizedApp, ns string) []NormalizedApp {
	var out []NormalizedApp
	for _, a := range apps {
		if a.Namespace == ns || a.DestinationNamespace == ns {
			out = append(out, a)
		}
	}
	return out
}

func filterBySyncStatus(apps []NormalizedApp, ss SyncStatus) []NormalizedApp {
	var out []NormalizedApp
	for _, a := range apps {
		if a.SyncStatus == ss {
			out = append(out, a)
		}
	}
	return out
}

func filterByHealthStatus(apps []NormalizedApp, hs HealthStatus) []NormalizedApp {
	var out []NormalizedApp
	for _, a := range apps {
		if a.HealthStatus == hs {
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
