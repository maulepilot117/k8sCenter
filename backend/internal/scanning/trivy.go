package scanning

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var trivyVulnReportGVR = schema.GroupVersionResource{
	Group:    "aquasecurity.github.io",
	Version:  "v1alpha1",
	Resource: "vulnerabilityreports",
}

// ListTrivyVulnSummaries fetches Trivy VulnerabilityReports in the given namespace
// and returns scanner-agnostic summaries grouped by workload.
func ListTrivyVulnSummaries(ctx context.Context, dynClient dynamic.Interface, namespace string) ([]WorkloadVulnSummary, error) {
	list, err := dynClient.Resource(trivyVulnReportGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing trivy vulnerability reports in %s: %w", namespace, err)
	}

	// Group reports by workload key (kind/name).
	type workloadKey struct {
		Kind string
		Name string
	}
	type workloadAccum struct {
		Namespace   string
		Kind        string
		Name        string
		Images      []ImageVulnInfo
		Total       SeveritySummary
		LastScanned string
	}

	grouped := make(map[workloadKey]*workloadAccum)
	order := make([]workloadKey, 0)

	for i := range list.Items {
		obj := &list.Items[i]
		labels := obj.GetLabels()

		wlKind := labels["trivy-operator.resource.kind"]
		wlName := labels["trivy-operator.resource.name"]
		wlNamespace := labels["trivy-operator.resource.namespace"]
		container := labels["trivy-operator.container.name"]

		// Extract severity counts from report.summary.
		critical, _, _ := unstructured.NestedInt64(obj.Object, "report", "summary", "criticalCount")
		high, _, _ := unstructured.NestedInt64(obj.Object, "report", "summary", "highCount")
		medium, _, _ := unstructured.NestedInt64(obj.Object, "report", "summary", "mediumCount")
		low, _, _ := unstructured.NestedInt64(obj.Object, "report", "summary", "lowCount")

		// Build image reference from artifact.
		repo, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "repository")
		tag, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "tag")
		image := repo
		if tag != "" {
			image = repo + ":" + tag
		}

		// Use container name as fallback image label when artifact is missing.
		if image == "" {
			image = container
		}

		scanTime, _, _ := unstructured.NestedString(obj.Object, "report", "updateTimestamp")

		sev := SeveritySummary{
			Critical: int(critical),
			High:     int(high),
			Medium:   int(medium),
			Low:      int(low),
		}

		key := workloadKey{Kind: wlKind, Name: wlName}
		acc, exists := grouped[key]
		if !exists {
			acc = &workloadAccum{
				Namespace: wlNamespace,
				Kind:      wlKind,
				Name:      wlName,
			}
			grouped[key] = acc
			order = append(order, key)
		}

		acc.Images = append(acc.Images, ImageVulnInfo{
			Image:      image,
			Severities: sev,
		})

		acc.Total.Critical += sev.Critical
		acc.Total.High += sev.High
		acc.Total.Medium += sev.Medium
		acc.Total.Low += sev.Low

		// Keep the latest scan time.
		if scanTime > acc.LastScanned {
			acc.LastScanned = scanTime
		}
	}

	// Build result slice preserving insertion order.
	results := make([]WorkloadVulnSummary, 0, len(order))
	for _, key := range order {
		acc := grouped[key]
		results = append(results, WorkloadVulnSummary{
			Namespace:   acc.Namespace,
			Kind:        acc.Kind,
			Name:        acc.Name,
			Images:      acc.Images,
			Total:       acc.Total,
			LastScanned: acc.LastScanned,
			Scanner:     ScannerTrivy,
		})
	}

	return results, nil
}
