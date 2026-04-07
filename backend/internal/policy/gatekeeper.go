package policy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	maxConstraintCRDs   = 100
	constraintSemaphore = 5
	constraintTimeout   = 5 * time.Second
)

// constraintCRDResult holds both policies and violations extracted from a single
// constraint CRD type, allowing a single API call per CRD instead of two.
type constraintCRDResult struct {
	policies   []NormalizedPolicy
	violations []NormalizedViolation
	err        error
}

// listGatekeeperPoliciesAndViolations fetches all constraint instances in a single
// pass per CRD, extracting both normalized policies and violations from each object.
// This avoids listing the same resources twice.
func listGatekeeperPoliciesAndViolations(ctx context.Context, dynClient dynamic.Interface, constraintCRDs []*k8s.CRDInfo) ([]NormalizedPolicy, []NormalizedViolation, error) {
	crds := constraintCRDs
	if len(crds) > maxConstraintCRDs {
		crds = crds[:maxConstraintCRDs]
	}

	sem := make(chan struct{}, constraintSemaphore)
	results := make(chan constraintCRDResult, len(crds))

	var wg sync.WaitGroup
	for _, crd := range crds {
		wg.Add(1)
		go func(c *k8s.CRDInfo) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			timeoutCtx, cancel := context.WithTimeout(ctx, constraintTimeout)
			defer cancel()

			gvr := schema.GroupVersionResource{
				Group:    "constraints.gatekeeper.sh",
				Version:  c.Version,
				Resource: c.Resource,
			}

			list, err := dynClient.Resource(gvr).List(timeoutCtx, metav1.ListOptions{})
			if err != nil {
				results <- constraintCRDResult{err: fmt.Errorf("listing %s: %w", c.Resource, err)}
				return
			}

			var r constraintCRDResult
			for i := range list.Items {
				r.policies = append(r.policies, NormalizeGatekeeperConstraint(&list.Items[i], c.Kind))
				r.violations = append(r.violations, extractGatekeeperViolations(&list.Items[i], c.Kind)...)
			}
			results <- r
		}(crd)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allPolicies []NormalizedPolicy
	var allViolations []NormalizedViolation
	for r := range results {
		if r.err != nil {
			// Log but don't fail the entire operation for one CRD type
			continue
		}
		allPolicies = append(allPolicies, r.policies...)
		allViolations = append(allViolations, r.violations...)
	}

	return allPolicies, allViolations, nil
}

func NormalizeGatekeeperConstraint(obj *unstructured.Unstructured, constraintKind string) NormalizedPolicy {
	name := obj.GetName()
	annotations := obj.GetAnnotations()

	// Enforcement action
	action, _, _ := unstructured.NestedString(obj.Object, "spec", "enforcementAction")
	if action == "" {
		action = "deny" // Gatekeeper default
	}
	blocking := strings.EqualFold(action, "deny")

	// Total violations from status
	violationCount, _, _ := unstructured.NestedInt64(obj.Object, "status", "totalViolations")

	// Target kinds from spec.match.kinds
	var targetKinds []string
	matchKinds, found, _ := unstructured.NestedSlice(obj.Object, "spec", "match", "kinds")
	if found {
		for _, mk := range matchKinds {
			mkMap, ok := mk.(map[string]interface{})
			if !ok {
				continue
			}
			kinds, found, _ := unstructured.NestedStringSlice(mkMap, "kinds")
			if found {
				targetKinds = append(targetKinds, kinds...)
			}
		}
	}

	// Annotations
	title := annotations["metadata.gatekeeper.sh/title"]
	if title == "" {
		title = name
	}
	description := annotations["description"]
	severity := annotations["metadata.gatekeeper.sh/severity"]
	if severity == "" {
		severity = defaultSeverity
	}
	category := annotations["metadata.gatekeeper.sh/category"]

	return NormalizedPolicy{
		ID:             fmt.Sprintf("gatekeeper::%s/%s", constraintKind, name),
		Name:           title,
		Kind:           constraintKind,
		Action:         action,
		Category:       category,
		Severity:       strings.ToLower(severity),
		Description:    description,
		NativeAction:   action,
		Engine:         EngineGatekeeper,
		Blocking:       blocking,
		Ready:          true, // Gatekeeper constraints are ready once created
		ViolationCount: int(violationCount),
		TargetKinds:    targetKinds,
	}
}

func extractGatekeeperViolations(obj *unstructured.Unstructured, constraintKind string) []NormalizedViolation {
	var violations []NormalizedViolation

	constraintName := obj.GetName()
	action, _, _ := unstructured.NestedString(obj.Object, "spec", "enforcementAction")
	if action == "" {
		action = "deny"
	}
	blocking := strings.EqualFold(action, "deny")

	severity := obj.GetAnnotations()["metadata.gatekeeper.sh/severity"]
	if severity == "" {
		severity = defaultSeverity
	}

	statusViolations, found, _ := unstructured.NestedSlice(obj.Object, "status", "violations")
	if !found {
		return nil
	}

	for _, v := range statusViolations {
		vMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		kind, _, _ := unstructured.NestedString(vMap, "kind")
		name, _, _ := unstructured.NestedString(vMap, "name")
		namespace, _, _ := unstructured.NestedString(vMap, "namespace")
		message, _, _ := unstructured.NestedString(vMap, "message")

		violations = append(violations, NormalizedViolation{
			Policy:    fmt.Sprintf("%s/%s", constraintKind, constraintName),
			Severity:  strings.ToLower(severity),
			Action:    action,
			Message:   message,
			Namespace: namespace,
			Kind:      kind,
			Name:      name,
			Engine:    EngineGatekeeper,
			Blocking:  blocking,
		})
	}

	return violations
}
