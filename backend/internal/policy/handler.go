package policy

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

// Handler serves policy HTTP endpoints.
type Handler struct {
	K8sClient       *k8s.ClientFactory
	Discoverer      *PolicyDiscoverer
	ClusterRouter   *k8s.ClusterRouter
	CRDDiscovery    *k8s.CRDDiscovery
	AccessChecker   *resources.AccessChecker
	ComplianceStore *store.ComplianceStore
	NotifService    *notifications.NotificationService
	Logger          *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedPolicyData
	cacheGen   uint64 // incremented on invalidation; prevents stale writes
}

type cachedPolicyData struct {
	policies   []NormalizedPolicy
	violations []NormalizedViolation
	fetchedAt  time.Time
}

const policyCacheTTL = 30 * time.Second

// fetchPoliciesAndViolations returns cached policy/violation data, refreshing
// if the cache is stale. Concurrent callers are coalesced via singleflight.
// The cache is populated using the service account (full visibility); callers
// must filter results by RBAC before returning to users.
func (h *Handler) fetchPoliciesAndViolations(ctx context.Context) ([]NormalizedPolicy, []NormalizedViolation, error) {
	h.cacheMu.RLock()
	if h.cachedData != nil && time.Since(h.cachedData.fetchedAt) < policyCacheTTL {
		p, v := h.cachedData.policies, h.cachedData.violations
		h.cacheMu.RUnlock()
		return p, v, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("fetch", func() (any, error) {
		return h.doFetch(ctx)
	})
	if err != nil {
		return nil, nil, err
	}
	data := result.(*cachedPolicyData)
	return data.policies, data.violations, nil
}

// InvalidateCache clears the cached policy/violation data so the next REST call re-fetches.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cachedData = nil
	h.cacheGen++
	h.cacheMu.Unlock()
	if h.NotifService != nil {
		go h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:   notifications.SourcePolicy,
			Severity: notifications.SeverityInfo,
			Title:    "Policy engine change detected",
			Message:  "Policy engine reported changes. Check the policy dashboard for details.",
		})
	}
}

// doFetch queries both engines based on discovery status and merges results.
// It uses the service account's dynamic client for full cluster visibility.
func (h *Handler) doFetch(ctx context.Context) (*cachedPolicyData, error) {
	// Capture current generation to detect concurrent invalidations.
	h.cacheMu.RLock()
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	dynClient := h.K8sClient.BaseDynamicClient()

	status := h.Discoverer.Status()

	var allPolicies []NormalizedPolicy
	var allViolations []NormalizedViolation

	type fetchResult struct {
		policies   []NormalizedPolicy
		violations []NormalizedViolation
		err        error
	}

	var wg sync.WaitGroup
	kyvernoCh := make(chan fetchResult, 1)
	gatekeeperCh := make(chan fetchResult, 1)

	// Fetch Kyverno policies/violations
	if status.Kyverno != nil && status.Kyverno.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r fetchResult
			r.policies, r.err = listKyvernoPolicies(ctx, dynClient)
			if r.err == nil {
				r.violations, r.err = listKyvernoViolations(ctx, dynClient)
			}
			kyvernoCh <- r
		}()
	} else {
		kyvernoCh <- fetchResult{}
	}

	// Fetch Gatekeeper policies/violations
	if status.Gatekeeper != nil && status.Gatekeeper.Available {
		wg.Add(1)
		go func() {
			defer wg.Done()
			constraintCRDs := h.Discoverer.GatekeeperConstraintCRDs()
			var r fetchResult
			r.policies, r.violations, r.err = listGatekeeperPoliciesAndViolations(ctx, dynClient, constraintCRDs)
			gatekeeperCh <- r
		}()
	} else {
		gatekeeperCh <- fetchResult{}
	}

	wg.Wait()

	kr := <-kyvernoCh
	gr := <-gatekeeperCh

	if kr.err != nil {
		h.Logger.Warn("kyverno fetch error", "error", kr.err)
	} else {
		allPolicies = append(allPolicies, kr.policies...)
		allViolations = append(allViolations, kr.violations...)
	}

	if gr.err != nil {
		h.Logger.Warn("gatekeeper fetch error", "error", gr.err)
	} else {
		allPolicies = append(allPolicies, gr.policies...)
		allViolations = append(allViolations, gr.violations...)
	}

	data := &cachedPolicyData{
		policies:   allPolicies,
		violations: allViolations,
		fetchedAt:  time.Now(),
	}

	// Only write cache if no invalidation occurred during fetch.
	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cachedData = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// FetchUnfiltered returns all policies and violations using the service account.
// This implements the PolicyFetcher interface for the ComplianceRecorder.
func (h *Handler) FetchUnfiltered(ctx context.Context) ([]NormalizedPolicy, []NormalizedViolation, error) {
	return h.fetchPoliciesAndViolations(ctx)
}

// HandleStatus returns the policy engine discovery status.
// GET /api/v1/policies/status
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status()

	// Strip namespace details for non-admin users
	if !auth.IsAdmin(user) {
		if status.Kyverno != nil {
			stripped := *status.Kyverno
			stripped.Namespace = ""
			status.Kyverno = &stripped
		}
		if status.Gatekeeper != nil {
			stripped := *status.Gatekeeper
			stripped.Namespace = ""
			status.Gatekeeper = &stripped
		}
	}

	httputil.WriteData(w, status)
}

// HandleListPolicies returns all normalized policies sorted by severity (desc).
// GET /api/v1/policies
func (h *Handler) HandleListPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	policies, _, err := h.fetchPoliciesAndViolations(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch policies", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch policies", "")
		return
	}

	// Filter policies by RBAC
	policies = h.filterPoliciesByRBAC(r.Context(), user, policies)

	// Sort by severity weight descending
	sorted := make([]NormalizedPolicy, len(policies))
	copy(sorted, policies)
	sort.Slice(sorted, func(i, j int) bool {
		wi := severityWeights[sorted[i].Severity]
		wj := severityWeights[sorted[j].Severity]
		if wi != wj {
			return wi > wj
		}
		return sorted[i].Name < sorted[j].Name
	})

	httputil.WriteData(w, sorted)
}

// HandleListViolations returns violations filtered by the user's namespace RBAC.
// GET /api/v1/policies/violations
func (h *Handler) HandleListViolations(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	_, violations, err := h.fetchPoliciesAndViolations(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch violations", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch violations", "")
		return
	}

	// Filter violations by RBAC — user must be able to list pods in the namespace
	filtered := h.filterViolationsByRBAC(r.Context(), user, violations)

	// Sort by severity weight descending
	sort.Slice(filtered, func(i, j int) bool {
		wi := severityWeights[filtered[i].Severity]
		wj := severityWeights[filtered[j].Severity]
		if wi != wj {
			return wi > wj
		}
		return filtered[i].Policy < filtered[j].Policy
	})

	httputil.WriteData(w, filtered)
}

// HandleCompliance returns a weighted compliance score.
// GET /api/v1/policies/compliance?namespace=
func (h *Handler) HandleCompliance(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	policies, violations, err := h.fetchPoliciesAndViolations(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch policy data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to compute compliance", "")
		return
	}

	// Filter by RBAC
	policies = h.filterPoliciesByRBAC(r.Context(), user, policies)
	filtered := h.filterViolationsByRBAC(r.Context(), user, violations)

	scope := r.URL.Query().Get("namespace")
	score := computeCompliance(policies, filtered, scope)

	httputil.WriteData(w, score)
}

// HandleComplianceHistory returns historical compliance score snapshots.
// GET /api/v1/policies/compliance/history?days=30
func (h *Handler) HandleComplianceHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if h.ComplianceStore == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "compliance history requires a database", "")
		return
	}

	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			days = n
		}
	}
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}

	clusterID := "local" // v1: local cluster only

	snapshots, err := h.ComplianceStore.QueryHistory(r.Context(), clusterID, days)
	if err != nil {
		h.Logger.Error("failed to query compliance history", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to query compliance history", "")
		return
	}

	// Convert to API response format
	type historyPoint struct {
		Date  string  `json:"date"`
		Score float64 `json:"score"`
		Pass  int     `json:"pass"`
		Fail  int     `json:"fail"`
		Warn  int     `json:"warn"`
		Total int     `json:"total"`
	}

	points := make([]historyPoint, len(snapshots))
	for i, s := range snapshots {
		points[i] = historyPoint{
			Date:  s.Date.Format("2006-01-02"),
			Score: s.OverallScore,
			Pass:  s.Pass,
			Fail:  s.Fail,
			Warn:  s.Warn,
			Total: s.Total,
		}
	}

	httputil.WriteData(w, points)
}

// filterViolationsByRBAC removes violations the user cannot see. Uses "list pods"
// as a namespace access proxy — the same pattern used by dashboard-summary.
func (h *Handler) filterViolationsByRBAC(ctx context.Context, user *auth.User, violations []NormalizedViolation) []NormalizedViolation {
	// Cache RBAC decisions per namespace within this request
	nsAccess := make(map[string]bool)
	var filtered []NormalizedViolation

	for _, v := range violations {
		ns := v.Namespace
		if ns == "" {
			// Cluster-scoped violations: only admins see them
			if auth.IsAdmin(user) {
				filtered = append(filtered, v)
			}
			continue
		}

		allowed, checked := nsAccess[ns]
		if !checked {
			can, err := h.AccessChecker.CanAccess(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", ns)
			if err != nil {
				allowed = false
			} else {
				allowed = can
			}
			nsAccess[ns] = allowed
		}

		if allowed {
			filtered = append(filtered, v)
		}
	}

	return filtered
}

// filterPoliciesByRBAC removes policies the user cannot see based on namespace access.
// Cluster-scoped policies are visible to all authenticated users (they apply globally).
func (h *Handler) filterPoliciesByRBAC(ctx context.Context, user *auth.User, policies []NormalizedPolicy) []NormalizedPolicy {
	nsAccess := make(map[string]bool)
	var filtered []NormalizedPolicy

	for _, p := range policies {
		if p.Namespace == "" {
			// Cluster-scoped policies visible to all (they apply globally)
			filtered = append(filtered, p)
			continue
		}

		allowed, checked := nsAccess[p.Namespace]
		if !checked {
			can, err := h.AccessChecker.CanAccess(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", p.Namespace)
			if err != nil {
				allowed = false
			} else {
				allowed = can
			}
			nsAccess[p.Namespace] = allowed
		}

		if allowed {
			filtered = append(filtered, p)
		}
	}

	return filtered
}

// computeCompliance calculates a weighted compliance score from policies and violations.
func computeCompliance(policies []NormalizedPolicy, violations []NormalizedViolation, scope string) ComplianceScore {
	// Filter violations to scope
	var scopedViolations []NormalizedViolation
	for _, v := range violations {
		if scope == "" || v.Namespace == scope {
			scopedViolations = append(scopedViolations, v)
		}
	}

	bySeverity := make(map[string]SeverityCounts)
	var totalWeightedPass, totalWeightedAll float64
	passCount, failCount, warnCount := 0, 0, 0

	// Build violation lookup: policy name -> count of blocking violations
	violationsByPolicy := make(map[string]int)
	for _, v := range scopedViolations {
		if v.Blocking {
			failCount++
		} else {
			warnCount++
		}
		violationsByPolicy[v.Policy]++
	}

	for _, p := range policies {
		weight := float64(severityWeights[p.Severity])
		if weight == 0 {
			weight = float64(severityWeights[defaultSeverity])
		}

		vCount := violationsByPolicy[p.Name]
		sev := p.Severity
		sc := bySeverity[sev]
		sc.Total++

		if vCount == 0 {
			passCount++
			sc.Pass++
			totalWeightedPass += weight
		} else {
			sc.Fail++
		}
		totalWeightedAll += weight
		bySeverity[sev] = sc
	}

	score := float64(100)
	if totalWeightedAll > 0 {
		score = (totalWeightedPass / totalWeightedAll) * 100
	}

	return ComplianceScore{
		Scope:      scope,
		Score:      score,
		Pass:       passCount,
		Fail:       failCount,
		Warn:       warnCount,
		Total:      len(policies),
		BySeverity: bySeverity,
	}
}
