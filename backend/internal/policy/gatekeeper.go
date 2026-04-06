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

var constraintTemplateGVR = schema.GroupVersionResource{
	Group: "templates.gatekeeper.sh", Version: "v1", Resource: "constrainttemplates",
}

// listGatekeeperPolicies fetches constraint templates and their instances,
// returning normalized policy objects.
func listGatekeeperPolicies(ctx context.Context, dynClient dynamic.Interface, constraintCRDs []*k8s.CRDInfo) ([]NormalizedPolicy, error) {
	var policies []NormalizedPolicy

	// Cap the number of constraint CRDs to prevent unbounded work
	crds := constraintCRDs
	if len(crds) > maxConstraintCRDs {
		crds = crds[:maxConstraintCRDs]
	}

	type result struct {
		policies []NormalizedPolicy
		err      error
	}

	sem := make(chan struct{}, constraintSemaphore)
	results := make(chan result, len(crds))

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
				results <- result{err: fmt.Errorf("listing %s: %w", c.Resource, err)}
				return
			}

			var pols []NormalizedPolicy
			for i := range list.Items {
				pols = append(pols, normalizeGatekeeperConstraint(&list.Items[i], c.Kind))
			}
			results <- result{policies: pols}
		}(crd)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			// Log but don't fail the entire operation for one CRD type
			continue
		}
		policies = append(policies, r.policies...)
	}

	return policies, nil
}

func normalizeGatekeeperConstraint(obj *unstructured.Unstructured, constraintKind string) NormalizedPolicy {
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
		severity = DefaultSeverity
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

// listGatekeeperViolations fetches violations from constraint status fields.
func listGatekeeperViolations(ctx context.Context, dynClient dynamic.Interface, constraintCRDs []*k8s.CRDInfo) ([]NormalizedViolation, error) {
	var violations []NormalizedViolation

	crds := constraintCRDs
	if len(crds) > maxConstraintCRDs {
		crds = crds[:maxConstraintCRDs]
	}

	type result struct {
		violations []NormalizedViolation
		err        error
	}

	sem := make(chan struct{}, constraintSemaphore)
	results := make(chan result, len(crds))

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
				results <- result{err: err}
				return
			}

			var viols []NormalizedViolation
			for i := range list.Items {
				viols = append(viols, extractGatekeeperViolations(&list.Items[i], c.Kind)...)
			}
			results <- result{violations: viols}
		}(crd)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			continue
		}
		violations = append(violations, r.violations...)
	}

	return violations, nil
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
		severity = DefaultSeverity
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
