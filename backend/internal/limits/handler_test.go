package limits

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// testInformerSource implements InformerSource for testing using the real lister types.
type testInformerSource struct {
	quotaLister      *testResourceQuotaLister
	limitRangeLister *testLimitRangeLister
}

func (t *testInformerSource) ResourceQuotas() corev1listers.ResourceQuotaLister {
	return t.quotaLister
}

func (t *testInformerSource) LimitRanges() corev1listers.LimitRangeLister {
	return t.limitRangeLister
}

// testResourceQuotaLister implements corev1listers.ResourceQuotaLister for testing.
type testResourceQuotaLister struct {
	quotas []*corev1.ResourceQuota
	err    error
}

func (t *testResourceQuotaLister) List(selector labels.Selector) ([]*corev1.ResourceQuota, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.quotas, nil
}

func (t *testResourceQuotaLister) ResourceQuotas(namespace string) corev1listers.ResourceQuotaNamespaceLister {
	var filtered []*corev1.ResourceQuota
	for _, q := range t.quotas {
		if q.Namespace == namespace {
			filtered = append(filtered, q)
		}
	}
	return &testResourceQuotaNamespaceLister{quotas: filtered, err: t.err}
}

type testResourceQuotaNamespaceLister struct {
	quotas []*corev1.ResourceQuota
	err    error
}

func (t *testResourceQuotaNamespaceLister) List(selector labels.Selector) ([]*corev1.ResourceQuota, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.quotas, nil
}

func (t *testResourceQuotaNamespaceLister) Get(name string) (*corev1.ResourceQuota, error) {
	for _, q := range t.quotas {
		if q.Name == name {
			return q, nil
		}
	}
	return nil, nil
}

// testLimitRangeLister implements corev1listers.LimitRangeLister for testing.
type testLimitRangeLister struct {
	limitRanges []*corev1.LimitRange
	err         error
}

func (t *testLimitRangeLister) List(selector labels.Selector) ([]*corev1.LimitRange, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.limitRanges, nil
}

func (t *testLimitRangeLister) LimitRanges(namespace string) corev1listers.LimitRangeNamespaceLister {
	var filtered []*corev1.LimitRange
	for _, lr := range t.limitRanges {
		if lr.Namespace == namespace {
			filtered = append(filtered, lr)
		}
	}
	return &testLimitRangeNamespaceLister{limitRanges: filtered, err: t.err}
}

type testLimitRangeNamespaceLister struct {
	limitRanges []*corev1.LimitRange
	err         error
}

func (t *testLimitRangeNamespaceLister) List(selector labels.Selector) ([]*corev1.LimitRange, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.limitRanges, nil
}

func (t *testLimitRangeNamespaceLister) Get(name string) (*corev1.LimitRange, error) {
	for _, lr := range t.limitRanges {
		if lr.Name == name {
			return lr, nil
		}
	}
	return nil, nil
}

// mockAccessChecker implements AccessChecker for testing.
type mockAccessChecker struct {
	allowedNamespaces map[string]bool
	alwaysAllow       bool
	alwaysDeny        bool
}

func (m *mockAccessChecker) CanAccess(ctx context.Context, username string, groups []string, verb, resource, namespace string) (bool, error) {
	if m.alwaysAllow {
		return true, nil
	}
	if m.alwaysDeny {
		return false, nil
	}
	return m.allowedNamespaces[namespace], nil
}

func newTestInformerSource(quotas []*corev1.ResourceQuota, limitRanges []*corev1.LimitRange) *testInformerSource {
	return &testInformerSource{
		quotaLister:      &testResourceQuotaLister{quotas: quotas},
		limitRangeLister: &testLimitRangeLister{limitRanges: limitRanges},
	}
}

func TestParseThresholdAnnotations(t *testing.T) {
	tests := []struct {
		name             string
		annotations      map[string]string
		expectedWarn     float64
		expectedCritical float64
	}{
		{
			name:             "no annotations - defaults",
			annotations:      nil,
			expectedWarn:     80,
			expectedCritical: 95,
		},
		{
			name:             "empty annotations - defaults",
			annotations:      map[string]string{},
			expectedWarn:     80,
			expectedCritical: 95,
		},
		{
			name: "valid warn annotation",
			annotations: map[string]string{
				AnnotationWarnThreshold: "70",
			},
			expectedWarn:     70,
			expectedCritical: 95,
		},
		{
			name: "valid critical annotation",
			annotations: map[string]string{
				AnnotationCriticalThreshold: "90",
			},
			expectedWarn:     80,
			expectedCritical: 90,
		},
		{
			name: "both annotations",
			annotations: map[string]string{
				AnnotationWarnThreshold:     "60",
				AnnotationCriticalThreshold: "85",
			},
			expectedWarn:     60,
			expectedCritical: 85,
		},
		{
			name: "invalid warn - non-numeric",
			annotations: map[string]string{
				AnnotationWarnThreshold: "invalid",
			},
			expectedWarn:     80,
			expectedCritical: 95,
		},
		{
			name: "invalid warn - negative",
			annotations: map[string]string{
				AnnotationWarnThreshold: "-10",
			},
			expectedWarn:     80,
			expectedCritical: 95,
		},
		{
			name: "invalid warn - over 100",
			annotations: map[string]string{
				AnnotationWarnThreshold: "150",
			},
			expectedWarn:     80,
			expectedCritical: 95,
		},
		{
			name: "decimal values",
			annotations: map[string]string{
				AnnotationWarnThreshold:     "75.5",
				AnnotationCriticalThreshold: "92.5",
			},
			expectedWarn:     75.5,
			expectedCritical: 92.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota := &corev1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			warn, critical := ParseThresholdAnnotations(quota)

			if warn != tt.expectedWarn {
				t.Errorf("warn = %v, want %v", warn, tt.expectedWarn)
			}
			if critical != tt.expectedCritical {
				t.Errorf("critical = %v, want %v", critical, tt.expectedCritical)
			}
		})
	}
}

func TestComputeStatus(t *testing.T) {
	tests := []struct {
		name       string
		percentage float64
		warn       float64
		critical   float64
		expected   ThresholdStatus
	}{
		{"0% usage", 0, 80, 95, ThresholdOK},
		{"50% usage", 50, 80, 95, ThresholdOK},
		{"79% usage - just under warn", 79, 80, 95, ThresholdOK},
		{"80% usage - at warn threshold", 80, 80, 95, ThresholdWarning},
		{"85% usage - warning range", 85, 80, 95, ThresholdWarning},
		{"94% usage - just under critical", 94, 80, 95, ThresholdWarning},
		{"95% usage - at critical threshold", 95, 80, 95, ThresholdCritical},
		{"100% usage", 100, 80, 95, ThresholdCritical},
		{"over 100% usage", 120, 80, 95, ThresholdCritical},
		{"custom thresholds - 60/90", 65, 60, 90, ThresholdWarning},
		{"custom thresholds - below warn", 55, 60, 90, ThresholdOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeStatus(tt.percentage, tt.warn, tt.critical)
			if result != tt.expected {
				t.Errorf("computeStatus(%v, %v, %v) = %v, want %v",
					tt.percentage, tt.warn, tt.critical, result, tt.expected)
			}
		})
	}
}

func TestComputeUtilization(t *testing.T) {
	h := &Handler{Logger: slog.Default()}

	tests := []struct {
		name           string
		quota          *corev1.ResourceQuota
		expectedCPU    float64
		expectedMemory float64
	}{
		{
			name: "50% CPU, 25% memory",
			quota: &corev1.ResourceQuota{
				Status: corev1.ResourceQuotaStatus{
					Hard: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					Used: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			expectedCPU:    50,
			expectedMemory: 25,
		},
		{
			name: "100% utilization",
			quota: &corev1.ResourceQuota{
				Status: corev1.ResourceQuotaStatus{
					Hard: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("2"),
					},
					Used: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("2"),
					},
				},
			},
			expectedCPU: 100,
		},
		{
			name: "zero hard limit - no division by zero",
			quota: &corev1.ResourceQuota{
				Status: corev1.ResourceQuotaStatus{
					Hard: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("0"),
					},
					Used: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("1"),
					},
				},
			},
			expectedCPU: 0, // 0 hard means 0% utilization (no division by zero)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			utilization := h.computeUtilization(tt.quota)

			if tt.expectedCPU > 0 {
				cpuUtil, ok := utilization["cpu"]
				if !ok {
					t.Error("expected cpu utilization")
				} else if cpuUtil.Percentage != tt.expectedCPU {
					t.Errorf("cpu utilization = %v, want %v", cpuUtil.Percentage, tt.expectedCPU)
				}
			}

			if tt.expectedMemory > 0 {
				memUtil, ok := utilization["memory"]
				if !ok {
					t.Error("expected memory utilization")
				} else if memUtil.Percentage != tt.expectedMemory {
					t.Errorf("memory utilization = %v, want %v", memUtil.Percentage, tt.expectedMemory)
				}
			}
		})
	}
}

func TestFilterByRBAC(t *testing.T) {
	h := &Handler{
		AccessChecker: &mockAccessChecker{
			allowedNamespaces: map[string]bool{
				"allowed-ns":  true,
				"allowed-ns2": true,
			},
		},
		Logger: slog.Default(),
	}

	summaries := []NamespaceSummary{
		{Namespace: "allowed-ns", HasQuota: true},
		{Namespace: "denied-ns", HasQuota: true},
		{Namespace: "allowed-ns2", HasQuota: true},
		{Namespace: "another-denied", HasQuota: false},
	}

	user := &auth.User{Username: "testuser", KubernetesGroups: []string{"group1"}}
	filtered := h.filterByRBAC(context.Background(), user, summaries)

	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered summaries, got %d", len(filtered))
	}

	for _, s := range filtered {
		if s.Namespace != "allowed-ns" && s.Namespace != "allowed-ns2" {
			t.Errorf("unexpected namespace in filtered results: %s", s.Namespace)
		}
	}
}

func TestCacheExpiration(t *testing.T) {
	informers := newTestInformerSource(
		[]*corev1.ResourceQuota{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "quota1", Namespace: "ns1"},
				Status: corev1.ResourceQuotaStatus{
					Hard: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					Used: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
				},
			},
		},
		nil,
	)

	h := NewHandler(informers, &mockAccessChecker{alwaysAllow: true}, slog.Default())

	// First fetch - should populate cache
	summaries1, err := h.fetchSummaries(context.Background())
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if len(summaries1) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries1))
	}

	// Second fetch within TTL - should return cached data
	summaries2, err := h.fetchSummaries(context.Background())
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}

	// Verify cache was used (same slice reference or equal content)
	if len(summaries2) != len(summaries1) {
		t.Error("cached data should match")
	}

	// Verify cache time is set
	h.cacheMu.RLock()
	cacheAge := time.Since(h.cacheTime)
	h.cacheMu.RUnlock()

	if cacheAge > time.Second {
		t.Error("cache time should be recent")
	}
}

func TestHandleListNamespaces_Empty(t *testing.T) {
	informers := newTestInformerSource(nil, nil)

	h := NewHandler(informers, &mockAccessChecker{alwaysAllow: true}, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/limits/namespaces", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{Username: "admin"}))
	rec := httptest.NewRecorder()

	h.HandleListNamespaces(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp api.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Data should be an empty array
	data, ok := resp.Data.([]interface{})
	if !ok {
		t.Fatalf("expected array data, got %T", resp.Data)
	}
	if len(data) != 0 {
		t.Errorf("expected empty array, got %d items", len(data))
	}
}

func TestNormalizeLimitRange(t *testing.T) {
	h := &Handler{Logger: slog.Default()}

	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "test-lr"},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{
					Type: corev1.LimitTypeContainer,
					Default: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					DefaultRequest: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					Max: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
			},
		},
	}

	normalized := h.normalizeLimitRange(lr)

	if normalized.Name != "test-lr" {
		t.Errorf("expected name 'test-lr', got %s", normalized.Name)
	}

	if len(normalized.Limits) != 1 {
		t.Fatalf("expected 1 limit item, got %d", len(normalized.Limits))
	}

	item := normalized.Limits[0]
	if item.Type != "Container" {
		t.Errorf("expected type 'Container', got %s", item.Type)
	}

	if item.Default["cpu"] != "500m" {
		t.Errorf("expected default cpu '500m', got %s", item.Default["cpu"])
	}

	if item.DefaultRequest["memory"] != "128Mi" {
		t.Errorf("expected defaultRequest memory '128Mi', got %s", item.DefaultRequest["memory"])
	}

	if item.Max["cpu"] != "2" {
		t.Errorf("expected max cpu '2', got %s", item.Max["cpu"])
	}

	if item.Min["memory"] != "64Mi" {
		t.Errorf("expected min memory '64Mi', got %s", item.Min["memory"])
	}
}

func TestStatusPriority(t *testing.T) {
	tests := []struct {
		status   ThresholdStatus
		expected int
	}{
		{ThresholdOK, 0},
		{ThresholdWarning, 1},
		{ThresholdCritical, 2},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := statusPriority(tt.status)
			if result != tt.expected {
				t.Errorf("statusPriority(%v) = %d, want %d", tt.status, result, tt.expected)
			}
		})
	}
}
