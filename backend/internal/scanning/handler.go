package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Handler serves security scanning HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *ScannerDiscoverer
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	nsCache    map[string]*cachedNSData
}

type cachedNSData struct {
	vulns     []WorkloadVulnSummary
	fetchedAt time.Time
}

const cacheTTL = 30 * time.Second

// ensureCache initializes the namespace cache map on first access.
func (h *Handler) ensureCache() {
	if h.nsCache == nil {
		h.nsCache = make(map[string]*cachedNSData)
	}
}

// fetchVulns returns cached vulnerability data for a namespace, refreshing if stale.
// Cache is populated using the service account; callers must RBAC-check before calling.
func (h *Handler) fetchVulns(ctx context.Context, namespace string) ([]WorkloadVulnSummary, error) {
	h.cacheMu.RLock()
	h.ensureCache()
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
	h.ensureCache()
	h.nsCache[namespace] = data
	h.cacheMu.Unlock()

	return data, nil
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

	// RBAC: verify user can access scanning CRDs in this namespace
	if !h.canAccessScanning(r.Context(), user, namespace) {
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

	// Sort by critical+high count descending (worst first)
	sort.Slice(vulns, func(i, j int) bool {
		si := vulns[i].Total.Critical + vulns[i].Total.High
		sj := vulns[j].Total.Critical + vulns[j].Total.High
		return si > sj
	})

	httputil.WriteData(w, struct {
		Vulnerabilities []WorkloadVulnSummary `json:"vulnerabilities"`
		Summary         VulnListMetadata      `json:"summary"`
	}{
		Vulnerabilities: vulns,
		Summary:         computeMetadata(vulns),
	})
}

// canAccessScanning checks whether the user can list vulnerability reports
// from either Trivy (aquasecurity.github.io) or Kubescape (spdx.softwarecomposition.org).
// Access to either scanner grants access.
func (h *Handler) canAccessScanning(ctx context.Context, user *auth.User, namespace string) bool {
	// Trivy CRD RBAC check
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx, user.KubernetesUsername, user.KubernetesGroups,
		"list", "aquasecurity.github.io", "vulnerabilityreports", namespace,
	)
	if err == nil && can {
		return true
	}

	// Kubescape CRD RBAC check
	can, err = h.AccessChecker.CanAccessGroupResource(
		ctx, user.KubernetesUsername, user.KubernetesGroups,
		"list", "spdx.softwarecomposition.org", "vulnerabilitysummaries", namespace,
	)
	return err == nil && can
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
