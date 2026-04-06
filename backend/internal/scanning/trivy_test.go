package scanning

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestListTrivyVulnSummaries_GroupsByWorkload(t *testing.T) {
	// Two VulnerabilityReports for the same Deployment (nginx), one per container.
	report1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "deploy-nginx-app",
				"namespace": "default",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "Deployment",
					"trivy-operator.resource.name":      "nginx",
					"trivy-operator.resource.namespace":  "default",
					"trivy-operator.container.name":      "app",
				},
			},
			"report": map[string]interface{}{
				"summary": map[string]interface{}{
					"criticalCount": int64(2),
					"highCount":     int64(5),
					"mediumCount":   int64(10),
					"lowCount":      int64(3),
				},
				"artifact": map[string]interface{}{
					"repository": "nginx",
					"tag":        "1.25",
				},
				"updateTimestamp": "2026-04-01T10:00:00Z",
			},
		},
	}

	report2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "deploy-nginx-sidecar",
				"namespace": "default",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "Deployment",
					"trivy-operator.resource.name":      "nginx",
					"trivy-operator.resource.namespace":  "default",
					"trivy-operator.container.name":      "sidecar",
				},
			},
			"report": map[string]interface{}{
				"summary": map[string]interface{}{
					"criticalCount": int64(1),
					"highCount":     int64(3),
					"mediumCount":   int64(7),
					"lowCount":      int64(2),
				},
				"artifact": map[string]interface{}{
					"repository": "envoyproxy/envoy",
					"tag":        "v1.28",
				},
				"updateTimestamp": "2026-04-01T12:00:00Z",
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
		report1, report2,
	)

	results, err := ListTrivyVulnSummaries(context.Background(), dynClient, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 workload summary, got %d", len(results))
	}

	wl := results[0]

	if wl.Kind != "Deployment" || wl.Name != "nginx" || wl.Namespace != "default" {
		t.Errorf("unexpected workload identity: %s/%s/%s", wl.Kind, wl.Namespace, wl.Name)
	}

	if wl.Scanner != ScannerTrivy {
		t.Errorf("expected scanner %q, got %q", ScannerTrivy, wl.Scanner)
	}

	// Should have 2 image entries (one per container).
	if len(wl.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(wl.Images))
	}

	// Verify first image.
	if wl.Images[0].Image != "nginx:1.25" {
		t.Errorf("expected image %q, got %q", "nginx:1.25", wl.Images[0].Image)
	}
	if wl.Images[0].Severities.Critical != 2 {
		t.Errorf("expected image[0] critical=2, got %d", wl.Images[0].Severities.Critical)
	}

	// Verify second image.
	if wl.Images[1].Image != "envoyproxy/envoy:v1.28" {
		t.Errorf("expected image %q, got %q", "envoyproxy/envoy:v1.28", wl.Images[1].Image)
	}

	// Verify totals are summed across containers.
	if wl.Total.Critical != 3 {
		t.Errorf("expected total critical=3, got %d", wl.Total.Critical)
	}
	if wl.Total.High != 8 {
		t.Errorf("expected total high=8, got %d", wl.Total.High)
	}
	if wl.Total.Medium != 17 {
		t.Errorf("expected total medium=17, got %d", wl.Total.Medium)
	}
	if wl.Total.Low != 5 {
		t.Errorf("expected total low=5, got %d", wl.Total.Low)
	}

	// Last scanned should be the later timestamp.
	if wl.LastScanned != "2026-04-01T12:00:00Z" {
		t.Errorf("expected lastScanned %q, got %q", "2026-04-01T12:00:00Z", wl.LastScanned)
	}
}

func TestListTrivyVulnSummaries_EmptyNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
	)

	results, err := ListTrivyVulnSummaries(context.Background(), dynClient, "empty-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestListTrivyVulnSummaries_MultipleWorkloads(t *testing.T) {
	// Two reports for different workloads — should produce 2 summaries.
	report1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "deploy-api-main",
				"namespace": "prod",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "Deployment",
					"trivy-operator.resource.name":      "api",
					"trivy-operator.resource.namespace":  "prod",
					"trivy-operator.container.name":      "main",
				},
			},
			"report": map[string]interface{}{
				"summary": map[string]interface{}{
					"criticalCount": int64(0),
					"highCount":     int64(1),
					"mediumCount":   int64(0),
					"lowCount":      int64(0),
				},
				"artifact": map[string]interface{}{
					"repository": "myrepo/api",
					"tag":        "latest",
				},
				"updateTimestamp": "2026-04-01T08:00:00Z",
			},
		},
	}

	report2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "statefulset-db-main",
				"namespace": "prod",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "StatefulSet",
					"trivy-operator.resource.name":      "db",
					"trivy-operator.resource.namespace":  "prod",
					"trivy-operator.container.name":      "postgres",
				},
			},
			"report": map[string]interface{}{
				"summary": map[string]interface{}{
					"criticalCount": int64(1),
					"highCount":     int64(0),
					"mediumCount":   int64(2),
					"lowCount":      int64(5),
				},
				"artifact": map[string]interface{}{
					"repository": "postgres",
					"tag":        "16",
				},
				"updateTimestamp": "2026-04-02T09:00:00Z",
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
		report1, report2,
	)

	results, err := ListTrivyVulnSummaries(context.Background(), dynClient, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 workload summaries, got %d", len(results))
	}

	// First workload: api Deployment.
	if results[0].Kind != "Deployment" || results[0].Name != "api" {
		t.Errorf("unexpected first workload: %s/%s", results[0].Kind, results[0].Name)
	}

	// Second workload: db StatefulSet.
	if results[1].Kind != "StatefulSet" || results[1].Name != "db" {
		t.Errorf("unexpected second workload: %s/%s", results[1].Kind, results[1].Name)
	}
}

// restrictAction verifies the fake client was called with the right namespace.
func TestListTrivyVulnSummaries_NamespaceScoped(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
	)

	_, err := ListTrivyVulnSummaries(context.Background(), dynClient, "specific-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actions := dynClient.Actions()
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}

	listAction := actions[0]
	if listAction.GetNamespace() != "specific-ns" {
		t.Errorf("expected namespace %q, got %q", "specific-ns", listAction.GetNamespace())
	}
}
