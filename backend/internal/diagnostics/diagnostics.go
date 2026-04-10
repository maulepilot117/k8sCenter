package diagnostics

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubecenter/kubecenter/internal/topology"
)

// Severity indicates how critical a diagnostic finding is.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Result is the outcome of a single diagnostic rule check.
type Result struct {
	RuleName    string   `json:"ruleName"`
	Status      string   `json:"status"` // "pass", "warn", "fail"
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Detail      string   `json:"detail,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
	Links       []Link   `json:"links,omitempty"`
}

// Link references a related Kubernetes resource.
type Link struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// CheckFunc runs a single diagnostic check against a target.
type CheckFunc func(ctx context.Context, target *DiagnosticTarget) Result

// DiagnosticTarget holds the resource being diagnosed and its related objects.
type DiagnosticTarget struct {
	Kind      string
	Name      string
	Namespace string
	Object    runtime.Object
	Pods      []*corev1.Pod
	Events    []*corev1.Event
}

// ruleEntry binds a named rule to its check function and applicable resource kinds.
type ruleEntry struct {
	name      string
	severity  Severity
	appliesTo []string
	check     CheckFunc
}

// rules is the global registry of diagnostic rules, populated by init() in rules.go.
var rules []ruleEntry

// registerRule appends a rule to the global registry.
func registerRule(name string, severity Severity, appliesTo []string, check CheckFunc) {
	rules = append(rules, ruleEntry{
		name:      name,
		severity:  severity,
		appliesTo: appliesTo,
		check:     check,
	})
}

// RunDiagnostics executes all applicable rules against the given target.
// Each rule runs with panic recovery and a 5-second timeout.
// Results are sorted: failures first, then warnings, then passes.
func RunDiagnostics(ctx context.Context, target *DiagnosticTarget) []Result {
	var results []Result

	for _, rule := range rules {
		if !ruleAppliesTo(rule, target.Kind) {
			continue
		}

		result := runSafeCheck(ctx, rule, target)
		results = append(results, result)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return statusOrder(results[i].Status) < statusOrder(results[j].Status)
	})

	return results
}

// ruleAppliesTo checks whether a rule is relevant for the given kind.
func ruleAppliesTo(rule ruleEntry, kind string) bool {
	for _, k := range rule.appliesTo {
		if k == kind {
			return true
		}
	}
	return false
}

// runSafeCheck runs a single rule with panic recovery and a 5-second timeout.
func runSafeCheck(ctx context.Context, rule ruleEntry, target *DiagnosticTarget) Result {
	ch := make(chan Result, 1)
	ruleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("diagnostic rule panicked", "rule", rule.name, "panic", fmt.Sprintf("%v", r))
				ch <- Result{
					RuleName: rule.name,
					Status:   "fail",
					Severity: rule.severity,
					Message:  fmt.Sprintf("Rule %q encountered an internal error", rule.name),
				}
			}
		}()
		result := rule.check(ruleCtx, target)
		result.RuleName = rule.name
		result.Severity = rule.severity
		ch <- result
	}()

	select {
	case result := <-ch:
		return result
	case <-ruleCtx.Done():
		return Result{
			RuleName: rule.name,
			Status:   "fail",
			Severity: rule.severity,
			Message:  fmt.Sprintf("Rule %q timed out after 5s", rule.name),
		}
	}
}

// statusOrder returns a sort key so that "fail" < "warn" < "pass".
func statusOrder(status string) int {
	switch status {
	case "fail":
		return 0
	case "warn":
		return 1
	case "pass":
		return 2
	default:
		return 3
	}
}

// Resolve fetches the target resource and its related pods from the lister.
func Resolve(ctx context.Context, lister topology.ResourceLister, namespace, kind, name string) (*DiagnosticTarget, error) {
	target := &DiagnosticTarget{
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
	}

	// Fetch the target object
	obj, err := fetchObject(ctx, lister, namespace, kind, name)
	if err != nil {
		return nil, fmt.Errorf("fetching %s/%s: %w", kind, name, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%s %q not found in namespace %q", kind, name, namespace)
	}
	target.Object = obj

	// Fetch related pods
	pods, err := resolveRelatedPods(ctx, lister, namespace, kind, name, obj)
	if err != nil {
		slog.Warn("failed to resolve related pods", "kind", kind, "name", name, "error", err)
	}
	target.Pods = pods

	// Note: ResourceLister does not expose ListEvents, so we skip event population.
	// Events can be added later if the interface is extended.

	return target, nil
}

// fetchObject retrieves a single resource by kind and name from the lister.
func fetchObject(ctx context.Context, lister topology.ResourceLister, namespace, kind, name string) (runtime.Object, error) {
	switch kind {
	case "Deployment":
		items, err := lister.ListDeployments(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	case "StatefulSet":
		items, err := lister.ListStatefulSets(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	case "DaemonSet":
		items, err := lister.ListDaemonSets(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	case "Pod":
		items, err := lister.ListPods(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	case "Service":
		items, err := lister.ListServices(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	case "PersistentVolumeClaim":
		items, err := lister.ListPVCs(ctx, namespace)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Name == name {
				return item, nil
			}
		}
	default:
		return nil, fmt.Errorf("unsupported kind %q", kind)
	}
	return nil, nil
}

// resolveRelatedPods finds pods associated with the target resource.
func resolveRelatedPods(ctx context.Context, lister topology.ResourceLister, namespace, kind, name string, obj runtime.Object) ([]*corev1.Pod, error) {
	allPods, err := lister.ListPods(ctx, namespace)
	if err != nil {
		return nil, err
	}

	switch kind {
	case "Pod":
		// The target itself is a pod
		for _, p := range allPods {
			if p.Name == name {
				return []*corev1.Pod{p}, nil
			}
		}
		return nil, nil

	case "Deployment":
		// Deployment -> ReplicaSet -> Pod (match via ownerReference chain)
		replicaSets, err := lister.ListReplicaSets(ctx, namespace)
		if err != nil {
			return nil, err
		}
		// Find ReplicaSets owned by this Deployment
		rsNames := make(map[string]bool)
		for _, rs := range replicaSets {
			for _, ref := range rs.OwnerReferences {
				if ref.Kind == "Deployment" && ref.Name == name {
					rsNames[rs.Name] = true
				}
			}
		}
		// Find Pods owned by those ReplicaSets
		var pods []*corev1.Pod
		for _, p := range allPods {
			for _, ref := range p.OwnerReferences {
				if ref.Kind == "ReplicaSet" && rsNames[ref.Name] {
					pods = append(pods, p)
					break
				}
			}
		}
		return pods, nil

	case "StatefulSet", "DaemonSet":
		// Direct ownerReference from Pod to StatefulSet/DaemonSet
		var pods []*corev1.Pod
		for _, p := range allPods {
			for _, ref := range p.OwnerReferences {
				if ref.Kind == kind && ref.Name == name {
					pods = append(pods, p)
					break
				}
			}
		}
		return pods, nil

	case "Service":
		// Match pods by service selector
		svc, ok := obj.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			return nil, nil
		}
		selector := labels.Set(svc.Spec.Selector).AsSelector()
		var pods []*corev1.Pod
		for _, p := range allPods {
			if selector.Matches(labels.Set(p.Labels)) {
				pods = append(pods, p)
			}
		}
		return pods, nil

	default:
		return nil, nil
	}
}
