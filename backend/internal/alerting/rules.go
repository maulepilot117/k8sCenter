package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

var prometheusRuleGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1",
	Resource: "prometheusrules",
}

const managedByLabel = "app.kubernetes.io/managed-by"
const managedByValue = "kubecenter"

var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

// How long the local cluster's PrometheusRule presence is reused before
// discovery is read again. An absence is re-read sooner, so a CRD installed
// after startup is picked up quickly; a removal is seen within minutes.
const (
	localRecheckInstalled = 5 * time.Minute
	localRecheckAbsent    = 30 * time.Second
)

var (
	// ErrNotInstalled means the request's cluster does not serve the
	// PrometheusRule CRD.
	ErrNotInstalled = errors.New("PrometheusRule CRD is not installed on this cluster")
	// errNotManaged refuses a delete of a rule KubeCenter did not create.
	errNotManaged = errors.New("cannot delete PrometheusRule not managed by KubeCenter")
	// errInvalidRule wraps a rule the request supplied that cannot be sent.
	errInvalidRule = errors.New("invalid PrometheusRule")
)

// RulesManager handles PrometheusRule CRD CRUD operations on the cluster
// each request selects (R-8 U9). Only the rule objects follow the selected
// cluster: whether they fire depends on that cluster's own
// prometheus-operator, and the alert feeds stay on the local Alertmanager.
type RulesManager struct {
	clients  k8s.ClusterClients
	presence *k8s.Presence
	logger   *slog.Logger
	now      func() time.Time

	localMu        sync.Mutex
	localInstalled bool
	localCheckedAt time.Time
}

// NewRulesManager creates a new rules manager.
func NewRulesManager(clients k8s.ClusterClients, presence *k8s.Presence, logger *slog.Logger) *RulesManager {
	return &RulesManager{
		clients:  clients,
		presence: presence,
		logger:   logger,
		now:      time.Now,
	}
}

// requireInstalled returns nil when the request's cluster serves the
// PrometheusRule CRD, ErrNotInstalled when it does not, and otherwise the
// reason it could not be told.
func (rm *RulesManager) requireInstalled(ctx context.Context, username string, groups []string) error {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) {
		if !rm.localAvailable(ctx, username, groups) {
			return ErrNotInstalled
		}
		return nil
	}
	// Presence remembers an absence briefly and invalidates the cached
	// schema when that lapses, so a CRD installed later is seen within
	// seconds rather than when the schema cache expires.
	verdict := rm.presence.Check(ctx, clusterID, username, groups, prometheusRuleGVR.GroupResource())
	if verdict.Installed != nil {
		if !*verdict.Installed {
			return ErrNotInstalled
		}
		return nil
	}
	// The verdict carries only a reason; resolve the target again for the
	// error the response is built from.
	if _, err := rm.clients.TargetSchemaFor(ctx, clusterID, username, groups); err != nil {
		return k8s.TargetError{Err: err}
	}
	return k8s.ErrDiscoveryUnavailable
}

// localAvailable reports whether the local cluster serves the CRD. The
// answer is reused for a while, never forever: a CRD removed after startup
// stops being reported as installed.
func (rm *RulesManager) localAvailable(ctx context.Context, username string, groups []string) bool {
	rm.localMu.Lock()
	defer rm.localMu.Unlock()

	now := rm.now()
	ttl := localRecheckAbsent
	if rm.localInstalled {
		ttl = localRecheckInstalled
	}
	if !rm.localCheckedAt.IsZero() && now.Sub(rm.localCheckedAt) < ttl {
		return rm.localInstalled
	}

	installed := false
	target, err := rm.clients.TargetSchemaFor(ctx, k8s.LocalClusterID, username, groups)
	if err == nil {
		var resources *metav1.APIResourceList
		resources, err = target.Discovery.ServerResourcesForGroupVersion(prometheusRuleGVR.GroupVersion().String())
		if err == nil {
			for _, r := range resources.APIResources {
				if r.Kind == "PrometheusRule" {
					installed = true
					break
				}
			}
		}
	}
	if err != nil {
		rm.logger.Debug("PrometheusRule CRD not available", "error", err)
	}
	if installed != rm.localInstalled {
		rm.logger.Info("PrometheusRule CRD availability changed", "available", installed)
	}
	rm.localInstalled = installed
	rm.localCheckedAt = now
	return installed
}

// client returns a dynamic client impersonating the user on the request's
// cluster once the CRD is known to be served there.
func (rm *RulesManager) client(ctx context.Context, username string, groups []string) (dynamic.Interface, error) {
	if err := rm.requireInstalled(ctx, username, groups); err != nil {
		return nil, err
	}
	dynClient, err := rm.clients.DynamicClientForCluster(ctx, middleware.ClusterIDFromContext(ctx), username, groups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	return dynClient, nil
}

// removed reports whether err from a remote call means the CRD went away
// after discovery was cached, re-reading that cluster's discovery so later
// checks see the removal too.
func (rm *RulesManager) removed(ctx context.Context, username string, groups []string, err error) bool {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) || !k8s.IsResourceGone(err) {
		return false
	}
	verdict := rm.presence.Recheck(ctx, clusterID, username, groups, prometheusRuleGVR.GroupResource())
	return verdict.Installed != nil && !*verdict.Installed
}

// RuleSummary is a lightweight view of a PrometheusRule for listing.
type RuleSummary struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	RulesCount int    `json:"rulesCount"`
	CreatedAt  string `json:"createdAt"`
	ManagedBy  string `json:"managedBy"`
}

// List returns all KubeCenter-managed PrometheusRule resources, or
// ErrNotInstalled when the cluster does not serve the CRD.
func (rm *RulesManager) List(ctx context.Context, username string, groups []string, namespace string) ([]RuleSummary, error) {
	dynClient, err := rm.client(ctx, username, groups)
	if err != nil {
		return nil, err
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
		if rm.removed(ctx, username, groups, err) {
			return nil, ErrNotInstalled
		}
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
	dynClient, err := rm.client(ctx, username, groups)
	if err != nil {
		return nil, err
	}

	obj, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting PrometheusRule: %w", err)
	}

	return obj.Object, nil
}

// Create creates a new PrometheusRule from raw JSON/YAML content.
func (rm *RulesManager) Create(ctx context.Context, username string, groups []string, namespace string, content map[string]interface{}) (map[string]interface{}, error) {
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
		return nil, fmt.Errorf("%w: metadata.name is required", errInvalidRule)
	}
	if !k8sNameRegex.MatchString(name) {
		return nil, fmt.Errorf("%w: name must match [a-z0-9]([a-z0-9-]*[a-z0-9])?", errInvalidRule)
	}

	// Inject managed-by label
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[managedByLabel] = managedByValue
	obj.SetLabels(labels)

	dynClient, err := rm.client(ctx, username, groups)
	if err != nil {
		return nil, err
	}

	created, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating PrometheusRule: %w", err)
	}

	return created.Object, nil
}

// Update applies changes to a PrometheusRule via server-side apply.
func (rm *RulesManager) Update(ctx context.Context, username string, groups []string, namespace, name string, content map[string]interface{}) (map[string]interface{}, error) {
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
		return nil, fmt.Errorf("%w: %v", errInvalidRule, err)
	}

	dynClient, err := rm.client(ctx, username, groups)
	if err != nil {
		return nil, err
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
	dynClient, err := rm.client(ctx, username, groups)
	if err != nil {
		return err
	}

	// Verify managed-by label before deletion
	obj, err := dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting PrometheusRule for deletion check: %w", err)
	}

	labels := obj.GetLabels()
	if labels == nil || labels[managedByLabel] != managedByValue {
		return errNotManaged
	}

	// Use ResourceVersion precondition to guard against TOCTOU race:
	// if the resource was modified between the GET and DELETE, the API server
	// will reject the delete with a Conflict error.
	rv := obj.GetResourceVersion()
	return dynClient.Resource(prometheusRuleGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			ResourceVersion: &rv,
		},
	})
}
