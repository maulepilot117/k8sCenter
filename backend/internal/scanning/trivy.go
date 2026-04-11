package scanning

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// severityRank returns a numeric rank for sorting (lower = more severe).
func severityRank(severity string) int {
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		return 0
	case "HIGH":
		return 1
	case "MEDIUM":
		return 2
	case "LOW":
		return 3
	default:
		return 4 // UNKNOWN and anything unexpected
	}
}

// selectCVSSScore picks the best CVSS v3 score from Trivy's vendor-keyed CVSS map.
// Prefers NVD, falls back to known vendors, then to the first available.
// Returns nil when no CVSS data is present.
func selectCVSSScore(cvss map[string]interface{}) *float64 {
	if len(cvss) == 0 {
		return nil
	}
	preferred := []string{"nvd", "redhat", "ubuntu", "debian"}
	for _, vendor := range preferred {
		if score := extractV3Score(cvss, vendor); score != nil {
			return score
		}
	}
	// Fall back to the first vendor with a score.
	for vendor := range cvss {
		if score := extractV3Score(cvss, vendor); score != nil {
			return score
		}
	}
	return nil
}

// extractV3Score pulls V3Score from a nested CVSS vendor map.
func extractV3Score(cvss map[string]interface{}, vendor string) *float64 {
	vendorMap, ok := cvss[vendor].(map[string]interface{})
	if !ok {
		return nil
	}
	raw, ok := vendorMap["V3Score"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		return &v
	case int64:
		f := float64(v)
		return &f
	case int:
		f := float64(v)
		return &f
	}
	return nil
}

// GetTrivyWorkloadVulnDetails fetches full CVE details for a specific workload
// from Trivy VulnerabilityReport CRDs. Returns grouped-by-image results sorted
// by severity (CRITICAL→LOW→UNKNOWN) then by CVSS score descending.
func GetTrivyWorkloadVulnDetails(
	ctx context.Context,
	dynClient dynamic.Interface,
	namespace, kind, name string,
) (*WorkloadVulnDetail, error) {
	// Filter reports via label selectors. Trivy labels use these keys:
	//   trivy-operator.resource.namespace
	//   trivy-operator.resource.kind
	//   trivy-operator.resource.name
	labelSelector := fmt.Sprintf(
		"trivy-operator.resource.namespace=%s,trivy-operator.resource.kind=%s,trivy-operator.resource.name=%s",
		namespace, kind, name,
	)

	list, err := dynClient.Resource(trivyVulnReportGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing trivy vulnerability reports for %s/%s/%s: %w", namespace, kind, name, err)
	}

	detail := &WorkloadVulnDetail{
		Namespace: namespace,
		Kind:      kind,
		Name:      name,
		Scanner:   ScannerTrivy,
		Images:    []ImageVulnDetail{},
	}

	for i := range list.Items {
		obj := &list.Items[i]
		labels := obj.GetLabels()
		container := labels["trivy-operator.container.name"]

		// Build image reference from artifact.
		repo, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "repository")
		tag, _, _ := unstructured.NestedString(obj.Object, "report", "artifact", "tag")
		image := repo
		if tag != "" {
			image = repo + ":" + tag
		}
		if image == "" {
			image = container
		}

		scanTime, _, _ := unstructured.NestedString(obj.Object, "report", "updateTimestamp")
		if scanTime > detail.LastScanned {
			detail.LastScanned = scanTime
		}

		// Extract the vulnerabilities[] array.
		vulnsRaw, _, _ := unstructured.NestedSlice(obj.Object, "report", "vulnerabilities")
		cves := make([]CVEDetail, 0, len(vulnsRaw))
		for _, v := range vulnsRaw {
			vmap, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			cve := extractCVEDetail(vmap)
			if cve.ID == "" {
				continue
			}
			cves = append(cves, cve)
		}

		// Sort CVEs: severity asc (0=critical first), CVSS desc, ID asc.
		sort.SliceStable(cves, func(i, j int) bool {
			si, sj := severityRank(cves[i].Severity), severityRank(cves[j].Severity)
			if si != sj {
				return si < sj
			}
			ci := float64(0)
			if cves[i].CVSSScore != nil {
				ci = *cves[i].CVSSScore
			}
			cj := float64(0)
			if cves[j].CVSSScore != nil {
				cj = *cves[j].CVSSScore
			}
			if ci != cj {
				return ci > cj
			}
			return cves[i].ID < cves[j].ID
		})

		detail.Images = append(detail.Images, ImageVulnDetail{
			Name:            image,
			Container:       container,
			Vulnerabilities: cves,
		})
	}

	return detail, nil
}

// extractCVEDetail maps a single Trivy vulnerability map to a CVEDetail.
func extractCVEDetail(v map[string]interface{}) CVEDetail {
	getStr := func(key string) string {
		s, _ := v[key].(string)
		return s
	}

	cve := CVEDetail{
		ID:               getStr("vulnerabilityID"),
		Severity:         strings.ToUpper(getStr("severity")),
		Package:          getStr("resource"),
		InstalledVersion: getStr("installedVersion"),
		FixedVersion:     getStr("fixedVersion"),
		Title:            getStr("title"),
		PrimaryLink:      getStr("primaryLink"),
	}
	if cve.Severity == "" {
		cve.Severity = "UNKNOWN"
	}

	// Prefer nested CVSS map; fall back to top-level "score" field.
	if cvssMap, ok := v["cvss"].(map[string]interface{}); ok {
		cve.CVSSScore = selectCVSSScore(cvssMap)
	}
	if cve.CVSSScore == nil {
		if score, ok := v["score"].(float64); ok {
			cve.CVSSScore = &score
		}
	}

	return cve
}
