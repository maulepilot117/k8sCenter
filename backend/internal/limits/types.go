package limits

import (
	"context"

	corev1listers "k8s.io/client-go/listers/core/v1"
)

// ThresholdStatus indicates how close a resource is to its quota.
type ThresholdStatus string

const (
	ThresholdOK       ThresholdStatus = "ok"
	ThresholdWarning  ThresholdStatus = "warning"
	ThresholdCritical ThresholdStatus = "critical"
)

const (
	DefaultWarnThreshold     = 0.80
	DefaultCriticalThreshold = 0.95

	AnnotationWarnThreshold     = "k8scenter.io/warn-threshold"
	AnnotationCriticalThreshold = "k8scenter.io/critical-threshold"
)

// NamespaceSummary is the dashboard row for one namespace.
type NamespaceSummary struct {
	Namespace          string          `json:"namespace"`
	HasQuota           bool            `json:"hasQuota"`
	HasLimitRange      bool            `json:"hasLimitRange"`
	CPUUsedPercent     float64         `json:"cpuUsedPercent,omitempty"`
	MemoryUsedPercent  float64         `json:"memoryUsedPercent,omitempty"`
	HighestUtilization float64         `json:"highestUtilization"`
	Status             ThresholdStatus `json:"status"`
	QuotaCount         int             `json:"quotaCount"`
	LimitRangeCount    int             `json:"limitRangeCount"`
}

// NamespaceLimits is the detail view for one namespace.
type NamespaceLimits struct {
	Namespace   string                 `json:"namespace"`
	Quotas      []NormalizedQuota      `json:"quotas"`
	LimitRanges []NormalizedLimitRange `json:"limitRanges"`
}

// NormalizedQuota wraps a ResourceQuota with computed utilization.
type NormalizedQuota struct {
	Name              string                         `json:"name"`
	Utilization       map[string]ResourceUtilization `json:"utilization"`
	WarnThreshold     float64                        `json:"warnThreshold"`
	CriticalThreshold float64                        `json:"criticalThreshold"`
}

// ResourceUtilization tracks usage for one resource dimension.
type ResourceUtilization struct {
	Used       string          `json:"used"`
	Hard       string          `json:"hard"`
	Percentage float64         `json:"percentage"`
	Status     ThresholdStatus `json:"status"`
}

// NormalizedLimitRange abstracts away k8s API details.
type NormalizedLimitRange struct {
	Name   string           `json:"name"`
	Limits []LimitRangeItem `json:"limits"`
}

// LimitRangeItem is one limit type within a LimitRange.
type LimitRangeItem struct {
	Type                 string            `json:"type"` // Container, Pod, PersistentVolumeClaim
	Default              map[string]string `json:"default,omitempty"`
	DefaultRequest       map[string]string `json:"defaultRequest,omitempty"`
	Min                  map[string]string `json:"min,omitempty"`
	Max                  map[string]string `json:"max,omitempty"`
	MaxLimitRequestRatio map[string]string `json:"maxLimitRequestRatio,omitempty"`
}

// QuotaThresholdEvent is dispatched to Notification Center.
type QuotaThresholdEvent struct {
	Namespace   string          `json:"namespace"`
	QuotaName   string          `json:"quotaName"`
	Resource    string          `json:"resource"`
	Status      ThresholdStatus `json:"status"`
	UsedPercent float64         `json:"usedPercent"`
	Threshold   float64         `json:"threshold"`
	Used        string          `json:"used"`
	Hard        string          `json:"hard"`
}

// InformerSource provides access to ResourceQuota and LimitRange listers.
type InformerSource interface {
	ResourceQuotas() corev1listers.ResourceQuotaLister
	LimitRanges() corev1listers.LimitRangeLister
}

// AccessChecker provides RBAC verification for namespace access.
type AccessChecker interface {
	CanAccess(ctx context.Context, username string, groups []string, verb, resource, namespace string) (bool, error)
}
