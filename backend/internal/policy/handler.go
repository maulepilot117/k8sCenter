package policy

import (
	"context"
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
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"k8s.io/client-go/dynamic"
)

// Handler serves policy HTTP endpoints.
type Handler struct {
	Discoverer    *PolicyDiscoverer
	ClusterRouter *k8s.ClusterRouter
	CRDDiscovery  *k8s.CRDDiscovery
	AccessChecker *resources.AccessChecker
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedPolicyData
}

type cachedPolicyData struct {
	policies   []NormalizedPolicy
	violations []NormalizedViolation
	fetchedAt  time.Time
}

const policyCacheTTL = 30 * time.Second

// impersonatingDynamic returns a dynamic client that impersonates the given user,
// routed to the correct cluster based on the request context.
func (h *Handler) impersonatingDynamic(ctx context.Context, user *auth.User) (dynamic.Interface, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.ClusterRouter.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// fetchPoliciesAndViolations returns cached policy/violation data, refreshing
// if the cache is stale. Concurrent callers are coalesced via singleflight.
func (h *Handler) fetchPoliciesAndViolations(ctx context.Context, user *auth.User) ([]NormalizedPolicy, []NormalizedViolation, error) {
	h.cacheMu.RLock()
	if h.cachedData != nil && time.Since(h.cachedData.fetchedAt) < policyCacheTTL {
		p, v := h.cachedData.policies, h.cachedData.violations
		h.cacheMu.RUnlock()
		return p, v, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("fetch", func() (any, error) {
		return h.doFetch(ctx, user)
	})
	if err != nil {
		return nil, nil, err
	}
	data := result.(*cachedPolicyData)
	return data.policies, data.violations, nil
}

// doFetch queries both engines based on discovery status and merges results.
func (h *Handler) doFetch(ctx context.Context, user *auth.User) (*cachedPolicyData, error) {
	dynClient, err := h.impersonatingDynamic(ctx, user)
	if err != nil {
		return nil, err
	}

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
			r.policies, r.err = listGatekeeperPolicies(ctx, dynClient, constraintCRDs)
			if r.err == nil {
				r.violations, r.err = listGatekeeperViolations(ctx, dynClient, constraintCRDs)
			}
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

	h.cacheMu.Lock()
	h.cachedData = data
	h.cacheMu.Unlock()

	return data, nil
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

	policies, _, err := h.fetchPoliciesAndViolations(r.Context(), user)
	if err != nil {
		h.Logger.Error("failed to fetch policies", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch policies", "")
		return
	}

	// Sort by severity weight descending
	sorted := make([]NormalizedPolicy, len(policies))
	copy(sorted, policies)
	sort.Slice(sorted, func(i, j int) bool {
		wi := SeverityWeights[sorted[i].Severity]
		wj := SeverityWeights[sorted[j].Severity]
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

	_, violations, err := h.fetchPoliciesAndViolations(r.Context(), user)
	if err != nil {
		h.Logger.Error("failed to fetch violations", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch violations", "")
		return
	}

	// Filter violations by RBAC — user must be able to list pods in the namespace
	filtered := h.filterViolationsByRBAC(r.Context(), user, violations)

	// Sort by severity weight descending
	sort.Slice(filtered, func(i, j int) bool {
		wi := SeverityWeights[filtered[i].Severity]
		wj := SeverityWeights[filtered[j].Severity]
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

	policies, violations, err := h.fetchPoliciesAndViolations(r.Context(), user)
	if err != nil {
		h.Logger.Error("failed to fetch policy data", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to compute compliance", "")
		return
	}

	// Filter violations by RBAC
	filtered := h.filterViolationsByRBAC(r.Context(), user, violations)

	scope := r.URL.Query().Get("namespace")
	score := computeCompliance(policies, filtered, scope)

	httputil.WriteData(w, score)
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
		weight := float64(SeverityWeights[p.Severity])
		if weight == 0 {
			weight = float64(SeverityWeights[DefaultSeverity])
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
