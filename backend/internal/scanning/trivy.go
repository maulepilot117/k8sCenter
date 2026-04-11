package scanning

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// imageRefFromArtifact builds a container image reference from a Trivy report's
// report.artifact fields, falling back to the container name when repository is empty.
func imageRefFromArtifact(obj map[string]interface{}, containerName string) string {
	repo, _, _ := unstructured.NestedString(obj, "report", "artifact", "repository")
	tag, _, _ := unstructured.NestedString(obj, "report", "artifact", "tag")
	if repo == "" {
		return containerName
	}
	if tag == "" {
		return repo
	}
	return repo + ":" + tag
}

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

		image := imageRefFromArtifact(obj.Object, container)

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
// Prefers NVD, falls back to RedHat/Ubuntu/Debian, then to any remaining vendor
// in alphabetical order for determinism. Returns nil when no V3Score is present.
//
// Trivy's report.vulnerabilities[].cvss is serialized as YAML and decoded via
// unstructured, so numeric values arrive as float64 — int branches are dead.
func selectCVSSScore(cvss map[string]interface{}) *float64 {
	if len(cvss) == 0 {
		return nil
	}
	get := func(vendor string) *float64 {
		m, _ := cvss[vendor].(map[string]interface{})
		if f, ok := m["V3Score"].(float64); ok {
			return &f
		}
		return nil
	}
	for _, vendor := range []string{"nvd", "redhat", "ubuntu", "debian"} {
		if s := get(vendor); s != nil {
			return s
		}
	}
	// Deterministic fallback: sort remaining vendors so the same input always
	// yields the same output regardless of Go's map iteration order.
	remaining := make([]string, 0, len(cvss))
	for vendor := range cvss {
		remaining = append(remaining, vendor)
	}
	sort.Strings(remaining)
	for _, vendor := range remaining {
		if s := get(vendor); s != nil {
			return s
		}
	}
	return nil
}

// GetTrivyWorkloadVulnDetails fetches full CVE details for a specific workload
// from Trivy VulnerabilityReport CRDs. Returns grouped-by-image results sorted
// by severity (CRITICAL→LOW→UNKNOWN) then by CVSS score descending.
//
// Callers must validate namespace/kind/name before invoking — inputs flow into
// a label selector and must not contain selector metacharacters. In practice,
// scanning/handler.go applies regex validation and the resources.ValidateURLParams
// middleware before this function is reached.
func GetTrivyWorkloadVulnDetails(
	ctx context.Context,
	dynClient dynamic.Interface,
	namespace, kind, name string,
) (*WorkloadVulnDetail, error) {
	// Build the label selector via the typed API so it can't be mis-escaped.
	selector := labels.SelectorFromSet(labels.Set{
		"trivy-operator.resource.namespace": namespace,
		"trivy-operator.resource.kind":      kind,
		"trivy-operator.resource.name":      name,
	}).String()

	list, err := dynClient.Resource(trivyVulnReportGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing trivy vulnerability reports for %s/%s/%s: %w", namespace, kind, name, err)
	}

	detail := &WorkloadVulnDetail{
		Namespace: namespace,
		Kind:      kind,
		Name:      name,
		Scanner:   ScannerTrivy,
		Images:    make([]ImageVulnDetail, 0, len(list.Items)),
	}

	for i := range list.Items {
		obj := &list.Items[i]
		reportLabels := obj.GetLabels()
		container := reportLabels["trivy-operator.container.name"]

		image := imageRefFromArtifact(obj.Object, container)

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
		// Comparator defines a total order so stability isn't required.
		slices.SortFunc(cves, func(a, b CVEDetail) int {
			if n := cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)); n != 0 {
				return n
			}
			ai, bi := float64(0), float64(0)
			if a.CVSSScore != nil {
				ai = *a.CVSSScore
			}
			if b.CVSSScore != nil {
				bi = *b.CVSSScore
			}
			if n := cmp.Compare(bi, ai); n != 0 { // descending on score
				return n
			}
			return cmp.Compare(a.ID, b.ID)
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

	// Trivy emits severities in uppercase; severityRank normalizes case at
	// sort time as defense-in-depth, so ToUpper here would be redundant.
	cve := CVEDetail{
		ID:               getStr("vulnerabilityID"),
		Severity:         getStr("severity"),
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
