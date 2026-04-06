package scanning

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestListKubescapeVulnSummaries_SeverityExtraction(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "spdx.softwarecomposition.org/v1beta1",
			"kind":       "VulnerabilitySummary",
			"metadata": map[string]interface{}{
				"name":              "deploy-web",
				"namespace":         "production",
				"creationTimestamp": "2026-04-05T14:30:00Z",
				"labels": map[string]interface{}{
					"kubescape.io/workload-kind":      "Deployment",
					"kubescape.io/workload-name":      "web",
					"kubescape.io/workload-namespace": "production",
				},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{
						"all": int64(4),
					},
					"high": map[string]interface{}{
						"all": int64(12),
					},
					"medium": map[string]interface{}{
						"all": int64(25),
					},
					"low": map[string]interface{}{
						"all": int64(8),
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			kubescapeVulnSummaryGVR: "VulnerabilitySummaryList",
		},
		obj,
	)

	results, err := ListKubescapeVulnSummaries(context.Background(), dynClient, "production")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	wl := results[0]

	if wl.Kind != "Deployment" || wl.Name != "web" || wl.Namespace != "production" {
		t.Errorf("unexpected workload identity: %s/%s/%s", wl.Kind, wl.Namespace, wl.Name)
	}

	if wl.Scanner != ScannerKubescape {
		t.Errorf("expected scanner %q, got %q", ScannerKubescape, wl.Scanner)
	}

	// Verify severity extraction from nested spec.severities.
	if wl.Total.Critical != 4 {
		t.Errorf("expected critical=4, got %d", wl.Total.Critical)
	}
	if wl.Total.High != 12 {
		t.Errorf("expected high=12, got %d", wl.Total.High)
	}
	if wl.Total.Medium != 25 {
		t.Errorf("expected medium=25, got %d", wl.Total.Medium)
	}
	if wl.Total.Low != 8 {
		t.Errorf("expected low=8, got %d", wl.Total.Low)
	}

	// Single ImageVulnInfo entry (Kubescape is per-workload, not per-container).
	if len(wl.Images) != 1 {
		t.Fatalf("expected 1 image entry, got %d", len(wl.Images))
	}

	if wl.Images[0].Severities.Critical != 4 {
		t.Errorf("expected image critical=4, got %d", wl.Images[0].Severities.Critical)
	}
}

func TestListKubescapeVulnSummaries_MultipleWorkloads(t *testing.T) {
	obj1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "spdx.softwarecomposition.org/v1beta1",
			"kind":       "VulnerabilitySummary",
			"metadata": map[string]interface{}{
				"name":              "deploy-frontend",
				"namespace":         "app",
				"creationTimestamp": "2026-04-04T10:00:00Z",
				"labels": map[string]interface{}{
					"kubescape.io/workload-kind":      "Deployment",
					"kubescape.io/workload-name":      "frontend",
					"kubescape.io/workload-namespace": "app",
				},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{"all": int64(0)},
					"high":     map[string]interface{}{"all": int64(2)},
					"medium":   map[string]interface{}{"all": int64(5)},
					"low":      map[string]interface{}{"all": int64(1)},
				},
			},
		},
	}

	obj2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "spdx.softwarecomposition.org/v1beta1",
			"kind":       "VulnerabilitySummary",
			"metadata": map[string]interface{}{
				"name":              "deploy-backend",
				"namespace":         "app",
				"creationTimestamp": "2026-04-04T11:00:00Z",
				"labels": map[string]interface{}{
					"kubescape.io/workload-kind":      "Deployment",
					"kubescape.io/workload-name":      "backend",
					"kubescape.io/workload-namespace": "app",
				},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{"all": int64(3)},
					"high":     map[string]interface{}{"all": int64(7)},
					"medium":   map[string]interface{}{"all": int64(0)},
					"low":      map[string]interface{}{"all": int64(4)},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			kubescapeVulnSummaryGVR: "VulnerabilitySummaryList",
		},
		obj1, obj2,
	)

	results, err := ListKubescapeVulnSummaries(context.Background(), dynClient, "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Each result should have exactly 1 image entry.
	for i, r := range results {
		if len(r.Images) != 1 {
			t.Errorf("result[%d]: expected 1 image, got %d", i, len(r.Images))
		}
	}
}

func TestListKubescapeVulnSummaries_EmptyNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			kubescapeVulnSummaryGVR: "VulnerabilitySummaryList",
		},
	)

	results, err := ListKubescapeVulnSummaries(context.Background(), dynClient, "empty-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
