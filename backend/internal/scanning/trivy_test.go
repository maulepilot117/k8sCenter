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
					"trivy-operator.resource.namespace": "default",
					"trivy-operator.container.name":     "app",
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
					"trivy-operator.resource.namespace": "default",
					"trivy-operator.container.name":     "sidecar",
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
					"trivy-operator.resource.namespace": "prod",
					"trivy-operator.container.name":     "main",
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
					"trivy-operator.resource.namespace": "prod",
					"trivy-operator.container.name":     "postgres",
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

func TestSelectCVSSScore(t *testing.T) {
	tests := []struct {
		name  string
		cvss  map[string]interface{}
		want  *float64
	}{
		{
			name: "prefers nvd",
			cvss: map[string]interface{}{
				"nvd":    map[string]interface{}{"V3Score": 9.8},
				"redhat": map[string]interface{}{"V3Score": 7.5},
			},
			want: ptrFloat(9.8),
		},
		{
			name: "falls back to redhat",
			cvss: map[string]interface{}{
				"redhat": map[string]interface{}{"V3Score": 7.5},
			},
			want: ptrFloat(7.5),
		},
		{
			name: "falls back to any vendor",
			cvss: map[string]interface{}{
				"alpine": map[string]interface{}{"V3Score": 5.0},
			},
			want: ptrFloat(5.0),
		},
		{
			name: "nil when empty",
			cvss: map[string]interface{}{},
			want: nil,
		},
		{
			name: "nil when vendor has no V3Score",
			cvss: map[string]interface{}{
				"nvd": map[string]interface{}{"V2Score": 4.0},
			},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectCVSSScore(tc.cvss)
			if tc.want == nil && got != nil {
				t.Errorf("expected nil, got %v", *got)
			}
			if tc.want != nil && got == nil {
				t.Errorf("expected %v, got nil", *tc.want)
			}
			if tc.want != nil && got != nil && *tc.want != *got {
				t.Errorf("expected %v, got %v", *tc.want, *got)
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	cases := map[string]int{
		"CRITICAL": 0,
		"HIGH":     1,
		"MEDIUM":   2,
		"LOW":      3,
		"UNKNOWN":  4,
		"":         4,
		"weird":    4,
		"critical": 0, // case-insensitive
	}
	for input, want := range cases {
		if got := severityRank(input); got != want {
			t.Errorf("severityRank(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestGetTrivyWorkloadVulnDetails_SortsAndGroups(t *testing.T) {
	// Report with multiple CVEs of mixed severity and one without a CVSS score.
	report := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "deploy-nginx-app",
				"namespace": "default",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "Deployment",
					"trivy-operator.resource.name":      "nginx",
					"trivy-operator.resource.namespace": "default",
					"trivy-operator.container.name":     "app",
				},
			},
			"report": map[string]interface{}{
				"artifact": map[string]interface{}{
					"repository": "nginx",
					"tag":        "1.25",
				},
				"updateTimestamp": "2026-04-01T10:00:00Z",
				"vulnerabilities": []interface{}{
					map[string]interface{}{
						"vulnerabilityID":  "CVE-2024-0002",
						"severity":         "HIGH",
						"resource":         "curl",
						"installedVersion": "7.68.0",
						"fixedVersion":     "",
						"title":            "curl flaw",
						"primaryLink":      "https://avd.aquasec.com/nvd/cve-2024-0002",
						"cvss": map[string]interface{}{
							"nvd": map[string]interface{}{"V3Score": 7.5},
						},
					},
					map[string]interface{}{
						"vulnerabilityID":  "CVE-2024-0001",
						"severity":         "CRITICAL",
						"resource":         "openssl",
						"installedVersion": "1.1.1k-1",
						"fixedVersion":     "1.1.1n-1",
						"title":            "openssl flaw",
						"primaryLink":      "https://avd.aquasec.com/nvd/cve-2024-0001",
						"cvss": map[string]interface{}{
							"nvd": map[string]interface{}{"V3Score": 9.8},
						},
					},
					map[string]interface{}{
						"vulnerabilityID":  "CVE-2024-0003",
						"severity":         "LOW",
						"resource":         "zlib",
						"installedVersion": "1.2.11",
						"fixedVersion":     "1.2.13",
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
		report,
	)

	detail, err := GetTrivyWorkloadVulnDetails(context.Background(), dynClient, "default", "Deployment", "nginx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detail.Scanner != ScannerTrivy {
		t.Errorf("expected scanner trivy, got %q", detail.Scanner)
	}
	if detail.LastScanned != "2026-04-01T10:00:00Z" {
		t.Errorf("unexpected lastScanned: %q", detail.LastScanned)
	}
	if len(detail.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(detail.Images))
	}

	img := detail.Images[0]
	if img.Name != "nginx:1.25" {
		t.Errorf("expected image nginx:1.25, got %q", img.Name)
	}
	if img.Container != "app" {
		t.Errorf("expected container app, got %q", img.Container)
	}
	if len(img.Vulnerabilities) != 3 {
		t.Fatalf("expected 3 CVEs, got %d", len(img.Vulnerabilities))
	}

	// Expected order: CRITICAL (9.8), HIGH (7.5), LOW (no score).
	wantOrder := []string{"CVE-2024-0001", "CVE-2024-0002", "CVE-2024-0003"}
	for i, want := range wantOrder {
		if img.Vulnerabilities[i].ID != want {
			t.Errorf("position %d: expected %s, got %s", i, want, img.Vulnerabilities[i].ID)
		}
	}

	// First CVE should have CVSS score, last CVE should be nil.
	if img.Vulnerabilities[0].CVSSScore == nil || *img.Vulnerabilities[0].CVSSScore != 9.8 {
		t.Errorf("expected CVE-2024-0001 CVSS 9.8, got %v", img.Vulnerabilities[0].CVSSScore)
	}
	if img.Vulnerabilities[2].CVSSScore != nil {
		t.Errorf("expected CVE-2024-0003 to have nil CVSS, got %v", *img.Vulnerabilities[2].CVSSScore)
	}

	// Fix availability: CVE-0001 and CVE-0003 have fixes, CVE-0002 doesn't.
	if img.Vulnerabilities[0].FixedVersion != "1.1.1n-1" {
		t.Errorf("expected CVE-0001 fixedVersion set, got %q", img.Vulnerabilities[0].FixedVersion)
	}
	if img.Vulnerabilities[1].FixedVersion != "" {
		t.Errorf("expected CVE-0002 fixedVersion empty, got %q", img.Vulnerabilities[1].FixedVersion)
	}
}

func TestGetTrivyWorkloadVulnDetails_MultipleImages(t *testing.T) {
	makeReport := func(container, repo, tag, cveID string) *unstructured.Unstructured {
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "aquasecurity.github.io/v1alpha1",
				"kind":       "VulnerabilityReport",
				"metadata": map[string]interface{}{
					"name":      "deploy-app-" + container,
					"namespace": "default",
					"labels": map[string]interface{}{
						"trivy-operator.resource.kind":      "Deployment",
						"trivy-operator.resource.name":      "app",
						"trivy-operator.resource.namespace": "default",
						"trivy-operator.container.name":     container,
					},
				},
				"report": map[string]interface{}{
					"artifact": map[string]interface{}{
						"repository": repo,
						"tag":        tag,
					},
					"updateTimestamp": "2026-04-01T10:00:00Z",
					"vulnerabilities": []interface{}{
						map[string]interface{}{
							"vulnerabilityID": cveID,
							"severity":        "HIGH",
							"resource":        "pkg",
						},
					},
				},
			},
		}
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
		makeReport("main", "myrepo/app", "v1", "CVE-A"),
		makeReport("sidecar", "envoy", "v1.28", "CVE-B"),
	)

	detail, err := GetTrivyWorkloadVulnDetails(context.Background(), dynClient, "default", "Deployment", "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(detail.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(detail.Images))
	}

	// Assert per-image identity: CVE ID should match its container.
	byContainer := map[string]ImageVulnDetail{}
	for _, img := range detail.Images {
		byContainer[img.Container] = img
	}
	main, ok := byContainer["main"]
	if !ok {
		t.Fatalf("expected image for container 'main', got containers: %v", keys(byContainer))
	}
	if main.Name != "myrepo/app:v1" {
		t.Errorf("main image name: expected %q, got %q", "myrepo/app:v1", main.Name)
	}
	if len(main.Vulnerabilities) != 1 || main.Vulnerabilities[0].ID != "CVE-A" {
		t.Errorf("main CVE: expected [CVE-A], got %+v", main.Vulnerabilities)
	}
	sidecar, ok := byContainer["sidecar"]
	if !ok {
		t.Fatalf("expected image for container 'sidecar', got containers: %v", keys(byContainer))
	}
	if sidecar.Name != "envoy:v1.28" {
		t.Errorf("sidecar image name: expected %q, got %q", "envoy:v1.28", sidecar.Name)
	}
	if len(sidecar.Vulnerabilities) != 1 || sidecar.Vulnerabilities[0].ID != "CVE-B" {
		t.Errorf("sidecar CVE: expected [CVE-B], got %+v", sidecar.Vulnerabilities)
	}
}

func keys(m map[string]ImageVulnDetail) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestGetTrivyWorkloadVulnDetails_TopLevelScoreFallback verifies that when a
// vulnerability has no nested cvss map but carries a top-level "score" field,
// that value is used as the CVSS score.
func TestGetTrivyWorkloadVulnDetails_TopLevelScoreFallback(t *testing.T) {
	report := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "ds-node-agent",
				"namespace": "default",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "DaemonSet",
					"trivy-operator.resource.name":      "node-agent",
					"trivy-operator.resource.namespace": "default",
					"trivy-operator.container.name":     "agent",
				},
			},
			"report": map[string]interface{}{
				"artifact": map[string]interface{}{"repository": "agent", "tag": "v1"},
				"vulnerabilities": []interface{}{
					map[string]interface{}{
						"vulnerabilityID": "CVE-TOP",
						"severity":        "MEDIUM",
						"resource":        "glibc",
						"score":           5.5, // top-level, no nested cvss map
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{trivyVulnReportGVR: "VulnerabilityReportList"},
		report,
	)

	detail, err := GetTrivyWorkloadVulnDetails(context.Background(), dynClient, "default", "DaemonSet", "node-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(detail.Images) != 1 || len(detail.Images[0].Vulnerabilities) != 1 {
		t.Fatalf("expected 1 image with 1 CVE, got %+v", detail.Images)
	}
	cve := detail.Images[0].Vulnerabilities[0]
	if cve.CVSSScore == nil || *cve.CVSSScore != 5.5 {
		t.Errorf("expected CVSS 5.5 from top-level score, got %v", cve.CVSSScore)
	}
}

// TestGetTrivyWorkloadVulnDetails_SkipsMalformedEntries ensures the loop
// silently drops non-map entries and entries missing vulnerabilityID.
func TestGetTrivyWorkloadVulnDetails_SkipsMalformedEntries(t *testing.T) {
	report := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "aquasecurity.github.io/v1alpha1",
			"kind":       "VulnerabilityReport",
			"metadata": map[string]interface{}{
				"name":      "deploy-app-main",
				"namespace": "default",
				"labels": map[string]interface{}{
					"trivy-operator.resource.kind":      "Deployment",
					"trivy-operator.resource.name":      "app",
					"trivy-operator.resource.namespace": "default",
					"trivy-operator.container.name":     "main",
				},
			},
			"report": map[string]interface{}{
				"artifact": map[string]interface{}{"repository": "app", "tag": "v1"},
				"vulnerabilities": []interface{}{
					"not-a-map", // non-map entry, should be skipped
					map[string]interface{}{
						// missing vulnerabilityID, should be skipped
						"severity": "HIGH",
						"resource": "x",
					},
					map[string]interface{}{
						"vulnerabilityID": "CVE-OK",
						"severity":        "LOW",
						"resource":        "pkg",
					},
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{trivyVulnReportGVR: "VulnerabilityReportList"},
		report,
	)

	detail, err := GetTrivyWorkloadVulnDetails(context.Background(), dynClient, "default", "Deployment", "app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(detail.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(detail.Images))
	}
	cves := detail.Images[0].Vulnerabilities
	if len(cves) != 1 {
		t.Fatalf("expected 1 CVE (malformed entries skipped), got %d: %+v", len(cves), cves)
	}
	if cves[0].ID != "CVE-OK" {
		t.Errorf("expected CVE-OK, got %q", cves[0].ID)
	}
}

// TestSelectCVSSScore_DeterministicFallback verifies that when no preferred
// vendor is present, alphabetical ordering of remaining vendors yields
// deterministic results regardless of Go's map iteration order.
func TestSelectCVSSScore_DeterministicFallback(t *testing.T) {
	cvss := map[string]interface{}{
		"zzz":    map[string]interface{}{"V3Score": 9.0},
		"alpine": map[string]interface{}{"V3Score": 5.0},
		"custom": map[string]interface{}{"V3Score": 7.0},
	}
	// Expected: alphabetical first with V3Score = "alpine" → 5.0
	got := selectCVSSScore(cvss)
	if got == nil {
		t.Fatalf("expected non-nil score")
	}
	if *got != 5.0 {
		t.Errorf("expected 5.0 (alpine) via deterministic fallback, got %v", *got)
	}
}

func TestGetTrivyWorkloadVulnDetails_EmptyReports(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			trivyVulnReportGVR: "VulnerabilityReportList",
		},
	)

	detail, err := GetTrivyWorkloadVulnDetails(context.Background(), dynClient, "default", "Deployment", "nothing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail == nil {
		t.Fatalf("expected non-nil detail")
	}
	if len(detail.Images) != 0 {
		t.Errorf("expected 0 images for empty report, got %d", len(detail.Images))
	}
	if detail.Namespace != "default" || detail.Kind != "Deployment" || detail.Name != "nothing" {
		t.Errorf("unexpected identity: %s/%s/%s", detail.Namespace, detail.Kind, detail.Name)
	}
}

func ptrFloat(f float64) *float64 { return &f }
