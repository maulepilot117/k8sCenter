package limits

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
)

const cacheTTL = 30 * time.Second

// Handler serves namespace limits HTTP endpoints.
type Handler struct {
	Informers     InformerSource
	AccessChecker AccessChecker
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedLimitsData
	cacheTime  time.Time
}

type cachedLimitsData struct {
	summaries []NamespaceSummary
}

// NewHandler creates a Handler for namespace limits endpoints.
func NewHandler(informers InformerSource, accessChecker AccessChecker, logger *slog.Logger) *Handler {
	return &Handler{
		Informers:     informers,
		AccessChecker: accessChecker,
		Logger:        logger,
	}
}

// HandleStatus handles GET /limits/status (discovery endpoint).
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	available := h.Informers != nil
	httputil.WriteData(w, map[string]bool{"available": available})
}

// HandleListNamespaces handles GET /limits/namespaces (dashboard).
func (h *Handler) HandleListNamespaces(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	summaries, err := h.fetchSummaries(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch namespace summaries", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch namespace limits", "")
		return
	}

	// Filter by RBAC
	filtered := h.filterByRBAC(r.Context(), user, summaries)

	// Sort by highest utilization descending
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].HighestUtilization > filtered[j].HighestUtilization
	})

	httputil.WriteData(w, filtered)
}

// HandleGetNamespace handles GET /limits/namespaces/{namespace} (detail).
func (h *Handler) HandleGetNamespace(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	if namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace is required", "")
		return
	}

	// Check RBAC for both resource types — allow if user has permission for either
	quotaAllowed, err1 := h.AccessChecker.CanAccess(r.Context(), user.Username, user.KubernetesGroups, "get", "resourcequotas", namespace)
	if err1 != nil {
		h.Logger.Error("RBAC check failed for resourcequotas", "namespace", namespace, "error", err1)
	}
	limitRangeAllowed, err2 := h.AccessChecker.CanAccess(r.Context(), user.Username, user.KubernetesGroups, "get", "limitranges", namespace)
	if err2 != nil {
		h.Logger.Error("RBAC check failed for limitranges", "namespace", namespace, "error", err2)
	}

	// If both checks failed with errors, return error
	if err1 != nil && err2 != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "authorization check failed", "")
		return
	}

	// Require permission for at least one resource type
	if !quotaAllowed && !limitRangeAllowed {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	detail, err := h.getNamespaceDetail(r.Context(), namespace)
	if err != nil {
		h.Logger.Error("failed to get namespace limits", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch namespace limits", "")
		return
	}

	httputil.WriteData(w, detail)
}

// fetchSummaries returns cached summaries or fetches fresh data.
// Concurrent callers are coalesced via singleflight.
func (h *Handler) fetchSummaries(ctx context.Context) ([]NamespaceSummary, error) {
	h.cacheMu.RLock()
	if h.cachedData != nil && time.Since(h.cacheTime) < cacheTTL {
		summaries := h.cachedData.summaries
		h.cacheMu.RUnlock()
		return summaries, nil
	}
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("summaries", func() (any, error) {
		return h.doFetchSummaries(ctx)
	})
	if err != nil {
		return nil, err
	}
	return result.([]NamespaceSummary), nil
}

func (h *Handler) doFetchSummaries(ctx context.Context) ([]NamespaceSummary, error) {
	quotas, err := h.Informers.ResourceQuotas().List(labels.Everything())
	if err != nil {
		return nil, err
	}

	limitRanges, err := h.Informers.LimitRanges().List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// Group by namespace
	quotasByNS := make(map[string][]*corev1.ResourceQuota)
	for _, q := range quotas {
		quotasByNS[q.Namespace] = append(quotasByNS[q.Namespace], q)
	}

	limitRangesByNS := make(map[string][]*corev1.LimitRange)
	for _, lr := range limitRanges {
		limitRangesByNS[lr.Namespace] = append(limitRangesByNS[lr.Namespace], lr)
	}

	// Build unique namespace set
	nsSet := make(map[string]struct{})
	for ns := range quotasByNS {
		nsSet[ns] = struct{}{}
	}
	for ns := range limitRangesByNS {
		nsSet[ns] = struct{}{}
	}

	summaries := make([]NamespaceSummary, 0, len(nsSet))
	for ns := range nsSet {
		nsQuotas := quotasByNS[ns]
		nsLimitRanges := limitRangesByNS[ns]

		summary := NamespaceSummary{
			Namespace:       ns,
			HasQuota:        len(nsQuotas) > 0,
			HasLimitRange:   len(nsLimitRanges) > 0,
			QuotaCount:      len(nsQuotas),
			LimitRangeCount: len(nsLimitRanges),
			Status:          ThresholdOK,
		}

		var highestUtil float64
		var cpuPct, memPct float64
		var hasCPU, hasMem bool

		for _, q := range nsQuotas {
			utilization := h.computeUtilization(q)
			warn, critical := ParseThresholdAnnotations(q)

			for resName, util := range utilization {
				if util.Percentage > highestUtil {
					highestUtil = util.Percentage
				}

				// Track CPU and memory specifically for dashboard display
				if resName == "cpu" || resName == "requests.cpu" || resName == "limits.cpu" {
					if util.Percentage > cpuPct {
						cpuPct = util.Percentage
						hasCPU = true
					}
				}
				if resName == "memory" || resName == "requests.memory" || resName == "limits.memory" {
					if util.Percentage > memPct {
						memPct = util.Percentage
						hasMem = true
					}
				}

				// Check threshold status
				status := computeStatus(util.Percentage, warn, critical)
				if statusPriority(status) > statusPriority(summary.Status) {
					summary.Status = status
				}
			}
		}

		summary.HighestUtilization = highestUtil
		if hasCPU {
			summary.CPUUsedPercent = cpuPct
		}
		if hasMem {
			summary.MemoryUsedPercent = memPct
		}

		summaries = append(summaries, summary)
	}

	// Update cache
	h.cacheMu.Lock()
	h.cachedData = &cachedLimitsData{summaries: summaries}
	h.cacheTime = time.Now()
	h.cacheMu.Unlock()

	return summaries, nil
}

func (h *Handler) filterByRBAC(ctx context.Context, user *auth.User, summaries []NamespaceSummary) []NamespaceSummary {
	filtered := make([]NamespaceSummary, 0, len(summaries))

	// Cache RBAC results by namespace to avoid O(n) API calls
	// Check both resourcequotas and limitranges — allow if user has permission for either
	type accessResult struct {
		quotaAllowed      bool
		limitRangeAllowed bool
	}
	accessCache := make(map[string]accessResult)

	for _, s := range summaries {
		result, cached := accessCache[s.Namespace]
		if !cached {
			quotaAllowed, err1 := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "resourcequotas", s.Namespace)
			if err1 != nil {
				h.Logger.Warn("RBAC check failed for resourcequotas", "namespace", s.Namespace, "error", err1)
			}
			limitRangeAllowed, err2 := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "limitranges", s.Namespace)
			if err2 != nil {
				h.Logger.Warn("RBAC check failed for limitranges", "namespace", s.Namespace, "error", err2)
			}
			result = accessResult{
				quotaAllowed:      err1 == nil && quotaAllowed,
				limitRangeAllowed: err2 == nil && limitRangeAllowed,
			}
			accessCache[s.Namespace] = result
		}

		// Allow access if user has permission for either resource type
		if result.quotaAllowed || result.limitRangeAllowed {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (h *Handler) getNamespaceDetail(ctx context.Context, namespace string) (*NamespaceLimits, error) {
	quotas, err := h.Informers.ResourceQuotas().ResourceQuotas(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	limitRanges, err := h.Informers.LimitRanges().LimitRanges(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	detail := &NamespaceLimits{
		Namespace:   namespace,
		Quotas:      make([]NormalizedQuota, 0, len(quotas)),
		LimitRanges: make([]NormalizedLimitRange, 0, len(limitRanges)),
	}

	for _, q := range quotas {
		detail.Quotas = append(detail.Quotas, h.normalizeQuota(q))
	}

	for _, lr := range limitRanges {
		detail.LimitRanges = append(detail.LimitRanges, h.normalizeLimitRange(lr))
	}

	return detail, nil
}

func (h *Handler) computeUtilization(quota *corev1.ResourceQuota) map[string]ResourceUtilization {
	utilization := make(map[string]ResourceUtilization)
	warn, critical := ParseThresholdAnnotations(quota)

	for resName, hard := range quota.Status.Hard {
		used := quota.Status.Used[resName]

		hardVal := hard.AsApproximateFloat64()
		usedVal := used.AsApproximateFloat64()

		var pct float64
		if hardVal > 0 {
			pct = (usedVal / hardVal) * 100
		}

		utilization[string(resName)] = ResourceUtilization{
			Used:       used.String(),
			Hard:       hard.String(),
			Percentage: pct,
			Status:     computeStatus(pct, warn, critical),
		}
	}

	return utilization
}

func (h *Handler) normalizeQuota(quota *corev1.ResourceQuota) NormalizedQuota {
	warn, critical := ParseThresholdAnnotations(quota)
	return NormalizedQuota{
		Name:              quota.Name,
		Utilization:       h.computeUtilization(quota),
		WarnThreshold:     warn,
		CriticalThreshold: critical,
	}
}

func (h *Handler) normalizeLimitRange(lr *corev1.LimitRange) NormalizedLimitRange {
	normalized := NormalizedLimitRange{
		Name:   lr.Name,
		Limits: make([]LimitRangeItem, 0, len(lr.Spec.Limits)),
	}

	for _, limit := range lr.Spec.Limits {
		item := LimitRangeItem{
			Type: string(limit.Type),
		}

		if len(limit.Default) > 0 {
			item.Default = resourceListToMap(limit.Default)
		}
		if len(limit.DefaultRequest) > 0 {
			item.DefaultRequest = resourceListToMap(limit.DefaultRequest)
		}
		if len(limit.Min) > 0 {
			item.Min = resourceListToMap(limit.Min)
		}
		if len(limit.Max) > 0 {
			item.Max = resourceListToMap(limit.Max)
		}
		if len(limit.MaxLimitRequestRatio) > 0 {
			item.MaxLimitRequestRatio = resourceListToMap(limit.MaxLimitRequestRatio)
		}

		normalized.Limits = append(normalized.Limits, item)
	}

	return normalized
}

func resourceListToMap(rl corev1.ResourceList) map[string]string {
	m := make(map[string]string, len(rl))
	for k, v := range rl {
		m[string(k)] = v.String()
	}
	return m
}

// ParseThresholdAnnotations reads warn/critical thresholds from quota annotations.
// Returns defaults if annotations are missing or invalid.
func ParseThresholdAnnotations(quota *corev1.ResourceQuota) (warn, critical float64) {
	warn = DefaultWarnThreshold * 100 // Convert to percentage
	critical = DefaultCriticalThreshold * 100

	if quota.Annotations == nil {
		return
	}

	if v, ok := quota.Annotations[AnnotationWarnThreshold]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
			warn = f
		}
	}

	if v, ok := quota.Annotations[AnnotationCriticalThreshold]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
			critical = f
		}
	}

	return
}

func computeStatus(percentage, warnThreshold, criticalThreshold float64) ThresholdStatus {
	if percentage >= criticalThreshold {
		return ThresholdCritical
	}
	if percentage >= warnThreshold {
		return ThresholdWarning
	}
	return ThresholdOK
}

func statusPriority(s ThresholdStatus) int {
	switch s {
	case ThresholdCritical:
		return 2
	case ThresholdWarning:
		return 1
	default:
		return 0
	}
}
