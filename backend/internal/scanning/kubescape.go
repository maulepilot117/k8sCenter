package scanning

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var kubescapeVulnSummaryGVR = schema.GroupVersionResource{
	Group:    "spdx.softwarecomposition.org",
	Version:  "v1beta1",
	Resource: "vulnerabilitysummaries",
}

// ListKubescapeVulnSummaries fetches Kubescape VulnerabilitySummary objects
// in the given namespace and returns scanner-agnostic summaries per workload.
func ListKubescapeVulnSummaries(ctx context.Context, dynClient dynamic.Interface, namespace string) ([]WorkloadVulnSummary, error) {
	list, err := dynClient.Resource(kubescapeVulnSummaryGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing kubescape vulnerability summaries in %s: %w", namespace, err)
	}

	results := make([]WorkloadVulnSummary, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		labels := obj.GetLabels()

		wlKind := labels["kubescape.io/workload-kind"]
		wlName := labels["kubescape.io/workload-name"]
		wlNamespace := labels["kubescape.io/workload-namespace"]

		// Extract severity counts from spec.severities.<level>.all.
		critical, _, _ := unstructured.NestedInt64(obj.Object, "spec", "severities", "critical", "all")
		high, _, _ := unstructured.NestedInt64(obj.Object, "spec", "severities", "high", "all")
		medium, _, _ := unstructured.NestedInt64(obj.Object, "spec", "severities", "medium", "all")
		low, _, _ := unstructured.NestedInt64(obj.Object, "spec", "severities", "low", "all")

		sev := SeveritySummary{
			Critical: int(critical),
			High:     int(high),
			Medium:   int(medium),
			Low:      int(low),
		}

		// Kubescape reports per-workload, not per-container. Use creation timestamp as scan time.
		scanTime := obj.GetCreationTimestamp().Format(metav1.RFC3339Micro)

		// Build a single ImageVulnInfo entry (Kubescape does not separate by container).
		imageInfo := ImageVulnInfo{
			Image:      fmt.Sprintf("%s/%s", wlKind, wlName),
			Severities: sev,
		}

		results = append(results, WorkloadVulnSummary{
			Namespace:   wlNamespace,
			Kind:        wlKind,
			Name:        wlName,
			Images:      []ImageVulnInfo{imageInfo},
			Total:       sev,
			LastScanned: scanTime,
			Scanner:     ScannerKubescape,
		})
	}

	return results, nil
}
