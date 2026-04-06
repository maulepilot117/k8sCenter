package policy

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	kyvernoClusterPolicyGVR = schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "clusterpolicies"}
	kyvernoPolicyGVR        = schema.GroupVersionResource{Group: "kyverno.io", Version: "v1", Resource: "policies"}
	policyReportGVR         = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports"}
	clusterPolicyReportGVR  = schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "clusterpolicyreports"}
)

// listKyvernoPolicies fetches Kyverno ClusterPolicies and namespaced Policies,
// returning them as normalized policy objects.
func listKyvernoPolicies(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedPolicy, error) {
	var policies []NormalizedPolicy

	// Cluster-scoped policies
	clusterList, err := dynClient.Resource(kyvernoClusterPolicyGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing kyverno cluster policies: %w", err)
	}
	for i := range clusterList.Items {
		policies = append(policies, normalizeKyvernoPolicy(&clusterList.Items[i], true))
	}

	// Namespace-scoped policies
	nsList, err := dynClient.Resource(kyvernoPolicyGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing kyverno policies: %w", err)
	}
	for i := range nsList.Items {
		policies = append(policies, normalizeKyvernoPolicy(&nsList.Items[i], false))
	}

	return policies, nil
}

func normalizeKyvernoPolicy(obj *unstructured.Unstructured, clusterScoped bool) NormalizedPolicy {
	name := obj.GetName()
	ns := obj.GetNamespace()
	annotations := obj.GetAnnotations()

	// Extract spec fields
	action, _, _ := unstructured.NestedString(obj.Object, "spec", "validationFailureAction")
	if action == "" {
		action = "Audit"
	}
	blocking := strings.EqualFold(action, "Enforce")

	// Ready status
	ready, _, _ := unstructured.NestedBool(obj.Object, "status", "ready")

	// Rule count
	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	ruleCount := len(rules)

	// Extract target kinds from rules
	var targetKinds []string
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]interface{})
		if !ok {
			continue
		}
		matchResources, found, _ := unstructured.NestedMap(ruleMap, "match", "any")
		if !found {
			matchResources, _, _ = unstructured.NestedMap(ruleMap, "match", "resources")
		}
		if matchResources != nil {
			kinds, found, _ := unstructured.NestedStringSlice(matchResources, "kinds")
			if found {
				targetKinds = append(targetKinds, kinds...)
			}
		}
	}

	// Annotations
	severity := annotations["policies.kyverno.io/severity"]
	if severity == "" {
		severity = DefaultSeverity
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
		TargetKinds:  targetKinds,
	}
}

// listKyvernoViolations fetches PolicyReports and ClusterPolicyReports,
// extracting failed/warning results as normalized violations.
func listKyvernoViolations(ctx context.Context, dynClient dynamic.Interface) ([]NormalizedViolation, error) {
	var violations []NormalizedViolation

	// Namespace-scoped policy reports
	nsList, err := dynClient.Resource(policyReportGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range nsList.Items {
			violations = append(violations, extractKyvernoViolations(&nsList.Items[i])...)
		}
	}

	// Cluster-scoped policy reports
	clusterList, err := dynClient.Resource(clusterPolicyReportGVR).List(ctx, metav1.ListOptions{})
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
		timestamp, _, _ := unstructured.NestedString(resultMap, "timestamp")
		if severity == "" {
			severity = DefaultSeverity
		}

		// Resource reference
		resKind, _, _ := unstructured.NestedString(resultMap, "resources", "kind")
		resName, _, _ := unstructured.NestedString(resultMap, "resources", "name")
		resNamespace, _, _ := unstructured.NestedString(resultMap, "resources", "namespace")

		// Fallback: some reports use a flat resource structure
		if resKind == "" {
			resKind, _, _ = unstructured.NestedString(resultMap, "resourceKind")
			resName, _, _ = unstructured.NestedString(resultMap, "resourceName")
			resNamespace, _, _ = unstructured.NestedString(resultMap, "resourceNamespace")
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
