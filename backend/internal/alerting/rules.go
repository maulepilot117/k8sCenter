package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

var prometheusRuleGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1",
	Resource: "prometheusrules",
}

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "kubecenter"

var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

// K8sClientFactory is the interface for creating k8s clients (subset of k8s.ClientFactory).
type K8sClientFactory interface {
	DynamicClientForUser(username string, groups []string) (dynamic.Interface, error)
	DiscoveryClient() discovery.DiscoveryInterface
}

// RulesManager handles PrometheusRule CRD CRUD operations.
type RulesManager struct {
	clientFactory K8sClientFactory
	logger        *slog.Logger
	available     bool
	checkOnce     sync.Once
}

// NewRulesManager creates a new rules manager.
func NewRulesManager(clientFactory K8sClientFactory, logger *slog.Logger) *RulesManager {
	return &RulesManager{
		clientFactory: clientFactory,
		logger:        logger,
	}
}

// Available returns whether the PrometheusRule CRD is installed in the cluster.
func (rm *RulesManager) Available() bool {
	rm.checkOnce.Do(func() {
		resources, err := rm.clientFactory.DiscoveryClient().ServerResourcesForGroupVersion("monitoring.coreos.com/v1")
		if err != nil {
			rm.logger.Debug("PrometheusRule CRD not available", "error", err)
			return
		}
		for _, r := range resources.APIResources {
			if r.Kind == "PrometheusRule" {
				rm.available = true
				rm.logger.Info("PrometheusRule CRD available")
				return
			}
		}
	})
	return rm.available
}

// RuleSummary is a lightweight view of a PrometheusRule for listing.
type RuleSummary struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	RulesCount int    `json:"rulesCount"`
	CreatedAt  string `json:"createdAt"`
	ManagedBy  string `json:"managedBy"`
}

// List returns all KubeCenter-managed PrometheusRule resources.
func (rm *RulesManager) List(ctx context.Context, username string, groups []string, namespace string) ([]RuleSummary, error) {
	if !rm.Available() {
		return []RuleSummary{}, nil
	}

	dynClient, err := rm.clientFactory.DynamicClientForUser(username, groups)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	var list *unstructured.UnstructuredList
	listOpts := metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	}

	if namespace != "" {
		list, err = dynClient.Resource(prometheusRuleGVR).Namespace(namespace).List(ctx, listOpts)
	} else {
		list, err = dynClient.Resource(prometheusRuleGVR).List(ctx, listOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("listing PrometheusRules: %w", err)
	}

	summaries := make([]RuleSummary, 0, len(list.Items))
	for _, item := range list.Items {
		rulesCount := 0
		if groups, found, _ := unstructured.NestedSlice(item.Object, "spec", "groups"); found {
			for _, g := range groups {
				if gMap, ok := g.(map[string]interface{}); ok {
					if rules, found, _ := unstructured.NestedSlice(gMap, "rules"); found {
						rulesCount += len(rules)
					}
				}
			}
		}

		managedBy := ""
		if labels := item.GetLabels(); labels != nil {
			managedBy = labels[managedByLabel]
		}

		summaries = append(summaries, RuleSummary{
			Name:       item.GetName(),
			Namespace:  item.GetNamespace(),
			RulesCount: rulesCount,
			CreatedAt:  item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
			ManagedBy:  managedBy,
		})
	}

	return summaries, nil
}

// Get returns a single PrometheusRule as raw JSON.
func (rm *RulesManager) Get(ctx context.Context, username string, groups []string, namespace, name string) (map[string]interface{}, error) {
	if !rm.Available() {
		return nil, fmt.Errorf("PrometheusRule CRD is not available")
	}

	dynClient, err := rm.clientFactory.DynamicClientForUser(username, groups)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	obj, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting PrometheusRule: %w", err)
	}

	return obj.Object, nil
}

// Create creates a new PrometheusRule from raw JSON/YAML content.
func (rm *RulesManager) Create(ctx context.Context, username string, groups []string, namespace string, content map[string]interface{}) (map[string]interface{}, error) {
	if !rm.Available() {
		return nil, fmt.Errorf("PrometheusRule CRD is not available")
	}

	obj := &unstructured.Unstructured{Object: content}

	// Ensure apiVersion and kind
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PrometheusRule",
	})

	// Validate name
	name := obj.GetName()
	if name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}
	if !k8sNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid resource name: must match [a-z0-9]([a-z0-9-]*[a-z0-9])?")
	}

	// Inject managed-by label
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[managedByLabel] = managedByValue
	obj.SetLabels(labels)

	dynClient, err := rm.clientFactory.DynamicClientForUser(username, groups)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	created, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating PrometheusRule: %w", err)
	}

	return created.Object, nil
}

// Update applies changes to a PrometheusRule via server-side apply.
func (rm *RulesManager) Update(ctx context.Context, username string, groups []string, namespace, name string, content map[string]interface{}) (map[string]interface{}, error) {
	if !rm.Available() {
		return nil, fmt.Errorf("PrometheusRule CRD is not available")
	}

	obj := &unstructured.Unstructured{Object: content}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PrometheusRule",
	})
	obj.SetName(name)
	obj.SetNamespace(namespace)

	// Ensure managed-by label
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[managedByLabel] = managedByValue
	obj.SetLabels(labels)

	patchBytes, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("marshaling patch: %w", err)
	}

	dynClient, err := rm.clientFactory.DynamicClientForUser(username, groups)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	result, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Patch(
		ctx, name, types.ApplyPatchType, patchBytes,
		metav1.PatchOptions{FieldManager: "kubecenter"},
	)
	if err != nil {
		return nil, fmt.Errorf("updating PrometheusRule: %w", err)
	}

	return result.Object, nil
}

// Delete removes a PrometheusRule, but only if it has the managed-by label.
func (rm *RulesManager) Delete(ctx context.Context, username string, groups []string, namespace, name string) error {
	if !rm.Available() {
		return fmt.Errorf("PrometheusRule CRD is not available")
	}

	dynClient, err := rm.clientFactory.DynamicClientForUser(username, groups)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	// Verify managed-by label before deletion
	obj, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting PrometheusRule for deletion check: %w", err)
	}

	labels := obj.GetLabels()
	if labels == nil || labels[managedByLabel] != managedByValue {
		return fmt.Errorf("cannot delete PrometheusRule not managed by KubeCenter")
	}

	return dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
