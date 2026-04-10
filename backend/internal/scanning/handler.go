package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

// Handler serves security scanning HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *ScannerDiscoverer
	AccessChecker *resources.AccessChecker
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	nsCache    map[string]*cachedNSData // initialized eagerly, never nil
}

type cachedNSData struct {
	vulns     []WorkloadVulnSummary
	fetchedAt time.Time
}

const (
	cacheTTL        = 30 * time.Second
	cacheMaxEntries = 200 // evict oldest entries beyond this
)

var validNamespace = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// InitCache must be called after construction to initialize the cache map.
// This avoids lazy init under locks.
func (h *Handler) InitCache() {
	h.nsCache = make(map[string]*cachedNSData)
}

// InvalidateCache clears all cached scan data so subsequent requests re-fetch.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.nsCache = make(map[string]*cachedNSData)
	h.cacheMu.Unlock()
	if h.NotifService != nil {
		go h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:   notifications.SourceScan,
			Severity: notifications.SeverityInfo,
			Title:    "Security scan results updated",
			Message:  "New vulnerability scan results available. Check the security dashboard for details.",
		})
	}
}

// fetchVulns returns cached vulnerability data for a namespace, refreshing if stale.
func (h *Handler) fetchVulns(ctx context.Context, namespace string) ([]WorkloadVulnSummary, error) {
	h.cacheMu.RLock()
	if entry := h.nsCache[namespace]; entry != nil && time.Since(entry.fetchedAt) < cacheTTL {
		vulns := entry.vulns
		h.cacheMu.RUnlock()
		return vulns, nil
	}
	h.cacheMu.RUnlock()

	key := "vulns:" + namespace
	result, err, _ := h.fetchGroup.Do(key, func() (any, error) {
		return h.doFetchNS(ctx, namespace)
	})
	if err != nil {
		return nil, err
	}
	data := result.(*cachedNSData)
	return data.vulns, nil
}

// doFetchNS queries both scanners based on discovery and merges results for a namespace.
func (h *Handler) doFetchNS(ctx context.Context, namespace string) (*cachedNSData, error) {
	dynClient := h.K8sClient.BaseDynamicClient()
	status := h.Discoverer.Status()

	var allVulns []WorkloadVulnSummary

	type fetchResult struct {
		vulns []WorkloadVulnSummary
		err   error
	}

	var wg sync.WaitGroup
	trivyCh := make(chan fetchResult, 1)
	kubescapeCh := make(chan fetchResult, 1)

	if status.Trivy != nil && status.Trivy.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r fetchResult
			r.vulns, r.err = ListTrivyVulnSummaries(ctx, dynClient, namespace)
			trivyCh <- r
		}()
	} else {
		trivyCh <- fetchResult{}
	}

	if status.Kubescape != nil && status.Kubescape.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r fetchResult
			r.vulns, r.err = ListKubescapeVulnSummaries(ctx, dynClient, namespace)
			kubescapeCh <- r
		}()
	} else {
		kubescapeCh <- fetchResult{}
	}

	wg.Wait()

	tr := <-trivyCh
	kr := <-kubescapeCh

	if tr.err != nil {
		h.Logger.Warn("trivy fetch error", "namespace", namespace, "error", tr.err)
	} else {
		allVulns = append(allVulns, tr.vulns...)
	}

	if kr.err != nil {
		h.Logger.Warn("kubescape fetch error", "namespace", namespace, "error", kr.err)
	} else {
		allVulns = append(allVulns, kr.vulns...)
	}

	data := &cachedNSData{
		vulns:     allVulns,
		fetchedAt: time.Now(),
	}

	h.cacheMu.Lock()
	h.nsCache[namespace] = data
	// Evict oldest entries if cache exceeds max size
	if len(h.nsCache) > cacheMaxEntries {
		h.evictOldestLocked()
	}
	h.cacheMu.Unlock()

	return data, nil
}

// evictOldestLocked removes the oldest cache entry. Must be called under write lock.
func (h *Handler) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range h.nsCache {
		if oldestKey == "" || v.fetchedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.fetchedAt
		}
	}
	if oldestKey != "" {
		delete(h.nsCache, oldestKey)
	}
}

// HandleStatus returns the security scanner detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status()

	// Strip namespace details for non-admin users
	if !auth.IsAdmin(user) {
		if status.Trivy != nil {
			stripped := *status.Trivy
			stripped.Namespace = ""
			status.Trivy = &stripped
		}
		if status.Kubescape != nil {
			stripped := *status.Kubescape
			stripped.Namespace = ""
			status.Kubescape = &stripped
		}
	}

	httputil.WriteData(w, status)
}

// HandleVulnerabilities returns vulnerability summaries for a namespace.
func (h *Handler) HandleVulnerabilities(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace parameter required", "")
		return
	}

	// Validate namespace format
	if !validNamespace.MatchString(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace", "")
		return
	}

	// RBAC: check per-scanner access and filter results accordingly
	canTrivy := h.canAccessTrivy(r.Context(), user, namespace)
	canKubescape := h.canAccessKubescape(r.Context(), user, namespace)

	if !canTrivy && !canKubescape {
		httputil.WriteError(w, http.StatusForbidden,
			fmt.Sprintf("access denied to scanning data in namespace %q", namespace), "")
		return
	}

	vulns, err := h.fetchVulns(r.Context(), namespace)
	if err != nil {
		h.Logger.Error("failed to fetch vulnerabilities", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch vulnerabilities", "")
		return
	}

	// Filter by per-scanner RBAC — only return data from scanners the user can access
	if !canTrivy || !canKubescape {
		vulns = filterByScannerAccess(vulns, canTrivy, canKubescape)
	}

	httputil.WriteData(w, struct {
		Vulnerabilities []WorkloadVulnSummary `json:"vulnerabilities"`
		Summary         VulnListMetadata      `json:"summary"`
	}{
		Vulnerabilities: vulns,
		Summary:         computeMetadata(vulns),
	})
}

// canAccessTrivy checks if the user can list Trivy VulnerabilityReports in the namespace.
func (h *Handler) canAccessTrivy(ctx context.Context, user *auth.User, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx, user.KubernetesUsername, user.KubernetesGroups,
		"list", "aquasecurity.github.io", "vulnerabilityreports", namespace,
	)
	return err == nil && can
}

// canAccessKubescape checks if the user can list Kubescape VulnerabilitySummaries in the namespace.
func (h *Handler) canAccessKubescape(ctx context.Context, user *auth.User, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx, user.KubernetesUsername, user.KubernetesGroups,
		"list", "spdx.softwarecomposition.org", "vulnerabilitysummaries", namespace,
	)
	return err == nil && can
}

// filterByScannerAccess removes results from scanners the user cannot access.
func filterByScannerAccess(vulns []WorkloadVulnSummary, canTrivy, canKubescape bool) []WorkloadVulnSummary {
	var filtered []WorkloadVulnSummary
	for _, v := range vulns {
		if v.Scanner == ScannerTrivy && canTrivy {
			filtered = append(filtered, v)
		} else if v.Scanner == ScannerKubescape && canKubescape {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

// computeMetadata builds summary counts for the vulnerability list response.
func computeMetadata(vulns []WorkloadVulnSummary) VulnListMetadata {
	m := VulnListMetadata{Total: len(vulns)}
	for _, v := range vulns {
		m.Severity.Critical += v.Total.Critical
		m.Severity.High += v.Total.High
		m.Severity.Medium += v.Total.Medium
		m.Severity.Low += v.Total.Low
	}
	return m
}
