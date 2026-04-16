package policy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	// KyvernoClusterPolicyGVR is the GVR for Kyverno ClusterPolicy resources.
	KyvernoClusterPolicyGVR = schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "clusterpolicies"}
	// KyvernoPolicyGVR is the GVR for Kyverno Policy resources.
	KyvernoPolicyGVR = schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "policies"}
	// PolicyReportGVR is the GVR for PolicyReport resources.
	PolicyReportGVR = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports"}
	// ClusterPolicyReportGVR is the GVR for ClusterPolicyReport resources.
	ClusterPolicyReportGVR = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "clusterpolicyreports"}
)

// listKyvernoPolicies fetches Kyverno ClusterPolicies and namespaced Policies,
// returning them as normalized policy objects.
func listKyvernoPolicies(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedPolicy, error) {
	var policies []NormalizedPolicy

	// Cluster-scoped policies
	clusterList, err := dynClient.Resource(KyvernoClusterPolicyGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing kyverno cluster policies: %w", err)
	}
	for i := range clusterList.Items {
		policies = append(policies, NormalizeKyvernoPolicy(&clusterList.Items[i], true))
	}

	// Namespace-scoped policies
	nsList, err := dynClient.Resource(KyvernoPolicyGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing kyverno policies: %w", err)
	}
	for i := range nsList.Items {
		policies = append(policies, NormalizeKyvernoPolicy(&nsList.Items[i], false))
	}

	return policies, nil
}

func NormalizeKyvernoPolicy(obj *unstructured.Unstructured, clusterScoped bool) NormalizedPolicy {
	name := obj.GetName()
	ns := obj.GetNamespace()
	annotations := obj.GetAnnotations()

	// Extract spec fields
	action, _, _ := unstructured.NestedString(obj.Object, "spec", "validationFailureAction")
	if action == "" {
		action = "Audit"
	}
	blocking := strings.EqualFold(action, "Enforce")

	// Ready status: Kyverno 1.8+ exposes readiness via status.conditions[type=Ready].
	ready := false
	conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if found {
		if c := k8s.FindCondition(k8s.ExtractConditions(conditions), "Ready"); c != nil {
			ready = strings.EqualFold(c.Status, "True")
		}
	}

	// Rule count
	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	ruleCount := len(rules)

	// Target kinds: Kyverno 1.8+ requires `match.any` or `match.all`, each a slice
	// of `{resources: {kinds: [...]}}` blocks. Deduped via a set.
	kindSet := make(map[string]struct{})
	collectKinds := func(items []interface{}) {
		for _, item := range items {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if kinds, found, _ := unstructured.NestedStringSlice(m, "resources", "kinds"); found {
				for _, k := range kinds {
					kindSet[k] = struct{}{}
				}
			}
		}
	}
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]interface{})
		if !ok {
			continue
		}
		if anyList, found, _ := unstructured.NestedSlice(ruleMap, "match", "any"); found {
			collectKinds(anyList)
		}
		if allList, found, _ := unstructured.NestedSlice(ruleMap, "match", "all"); found {
			collectKinds(allList)
		}
	}
	var targetKinds []string
	for k := range kindSet {
		targetKinds = append(targetKinds, k)
	}

	// Annotations
	severity := annotations["policies.kyverno.io/severity"]
	if severity == "" {
		severity = defaultSeverity
	}
	category := annotations["policies.kyverno.io/category"]
	description := annotations["policies.kyverno.io/description"]
	title := annotations["policies.kyverno.io/title"]
	if title == "" {
		title = name
	}

	kind := "ClusterPolicy"
	id := fmt.Sprintf("kyverno::%s", name)
	if !clusterScoped {
		kind = "Policy"
		id = fmt.Sprintf("kyverno:%s/%s", ns, name)
	}

	return NormalizedPolicy{
		ID:           id,
		Name:         title,
		Namespace:    ns,
		Kind:         kind,
		Action:       action,
		Category:     category,
		Severity:     strings.ToLower(severity),
		Description:  description,
		NativeAction: action,
		Engine:       EngineKyverno,
		Blocking:     blocking,
		Ready:        ready,
		RuleCount:    ruleCount,
		// Violations reference policies by raw k8s name in PolicyReport.results[].policy.
		MatchKey:    name,
		TargetKinds: targetKinds,
	}
}

// listKyvernoViolations fetches PolicyReports and ClusterPolicyReports,
// extracting failed/warning results as normalized violations.
func listKyvernoViolations(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedViolation, error) {
	var violations []NormalizedViolation

	// Namespace-scoped policy reports
	nsList, err := dynClient.Resource(PolicyReportGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range nsList.Items {
			violations = append(violations, extractKyvernoViolations(&nsList.Items[i])...)
		}
	}

	// Cluster-scoped policy reports
	clusterList, err := dynClient.Resource(ClusterPolicyReportGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range clusterList.Items {
			violations = append(violations, extractKyvernoViolations(&clusterList.Items[i])...)
		}
	}

	return violations, nil
}

func extractKyvernoViolations(report *unstructured.Unstructured) []NormalizedViolation {
	var violations []NormalizedViolation

	results, found, _ := unstructured.NestedSlice(report.Object, "results")
	if !found {
		return nil
	}

	// Modern Kyverno writes one PolicyReport per resource, with the resource
	// identified by the top-level `scope` field ({apiVersion, kind, name, namespace}).
	// Fall back to the report's own namespace for namespaced reports.
	reportKind, _, _ := unstructured.NestedString(report.Object, "scope", "kind")
	reportName, _, _ := unstructured.NestedString(report.Object, "scope", "name")
	reportNamespace, _, _ := unstructured.NestedString(report.Object, "scope", "namespace")
	if reportNamespace == "" {
		reportNamespace = report.GetNamespace()
	}

	for _, result := range results {
		resultMap, ok := result.(map[string]interface{})
		if !ok {
			continue
		}

		resultStr, _, _ := unstructured.NestedString(resultMap, "result")
		if resultStr != "fail" && resultStr != "warn" {
			continue
		}

		policyName, _, _ := unstructured.NestedString(resultMap, "policy")
		rule, _, _ := unstructured.NestedString(resultMap, "rule")
		message, _, _ := unstructured.NestedString(resultMap, "message")
		severity, _, _ := unstructured.NestedString(resultMap, "severity")
		if severity == "" {
			severity = defaultSeverity
		}

		// Kyverno writes timestamp as a nested {seconds, nanos} object.
		timestamp := ""
		if seconds, found, _ := unstructured.NestedInt64(resultMap, "timestamp", "seconds"); found {
			nanos, _, _ := unstructured.NestedInt64(resultMap, "timestamp", "nanos")
			timestamp = time.Unix(seconds, nanos).UTC().Format(time.RFC3339)
		}

		// Prefer per-result `resources[]` (legacy / ClusterPolicyReport style),
		// otherwise fall back to the report-level `scope`.
		resKind, resName, resNamespace := reportKind, reportName, reportNamespace
		if resourcesList, _, _ := unstructured.NestedSlice(resultMap, "resources"); len(resourcesList) > 0 {
			if resMap, ok := resourcesList[0].(map[string]interface{}); ok {
				if k, _, _ := unstructured.NestedString(resMap, "kind"); k != "" {
					resKind = k
				}
				if n, _, _ := unstructured.NestedString(resMap, "name"); n != "" {
					resName = n
				}
				if ns, _, _ := unstructured.NestedString(resMap, "namespace"); ns != "" {
					resNamespace = ns
				}
			}
		}

		violations = append(violations, NormalizedViolation{
			Policy:    policyName,
			Rule:      rule,
			Severity:  strings.ToLower(severity),
			Action:    resultStr,
			Message:   message,
			Namespace: resNamespace,
			Kind:      resKind,
			Name:      resName,
			Timestamp: timestamp,
			Engine:    EngineKyverno,
			Blocking:  resultStr == "fail",
		})
	}

	return violations
}
