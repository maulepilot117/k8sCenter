package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextensionsinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	crdResyncPeriod   = 5 * time.Minute
	countCacheTTL     = 30 * time.Second
	countConcurrency  = 5
)

// coreAPIDenylist contains API groups that are served by built-in k8s resources
// and must not be exposed through the CRD discovery service.
var coreAPIDenylist = map[string]bool{
	"":                                true, // core (pods, services, etc.)
	"apps":                            true,
	"batch":                           true,
	"autoscaling":                     true,
	"networking.k8s.io":               true,
	"rbac.authorization.k8s.io":       true,
	"storage.k8s.io":                  true,
	"apiextensions.k8s.io":            true,
	"admissionregistration.k8s.io":    true,
	"coordination.k8s.io":             true,
	"discovery.k8s.io":                true,
	"events.k8s.io":                   true,
	"flowcontrol.apiserver.k8s.io":    true,
	"node.k8s.io":                     true,
	"policy":                          true,
	"scheduling.k8s.io":               true,
	"certificates.k8s.io":             true,
	"authentication.k8s.io":           true,
	"authorization.k8s.io":            true,
	"apiregistration.k8s.io":          true,
	"resource.k8s.io":                 true,
	"internal.apiserver.k8s.io":       true,
	"storagemigration.k8s.io":         true,
}

// CRDInfo holds metadata about a discovered Custom Resource Definition.
type CRDInfo struct {
	Group                    string
	Version                  string
	Resource                 string
	Kind                     string
	Scope                    string // "Namespaced" or "Cluster"
	Served                   bool
	StorageVersion           bool
	AdditionalPrinterColumns []apiextensionsv1.CustomResourceColumnDefinition
}

// CRDDiscovery watches CRD resources via an apiextensions informer and maintains
// an in-memory lookup map for discovered CRDs. It caches instance counts with a
// configurable TTL and uses bounded concurrency for count refreshes.
type CRDDiscovery struct {
	informerFactory apiextensionsinformers.SharedInformerFactory
	dynClient       dynamic.Interface
	logger          *slog.Logger

	mu   sync.RWMutex
	crds map[string]*CRDInfo // keyed by "group/resource"

	countMu      sync.Mutex
	countCache   map[string]int
	countUpdated time.Time
}

// NewCRDDiscovery creates a CRDDiscovery that uses an apiextensions informer to
// watch CRD resources. The restConfig is used to create the apiextensions client.
func NewCRDDiscovery(restConfig *rest.Config, dynClient dynamic.Interface, logger *slog.Logger) (*CRDDiscovery, error) {
	apiextClient, err := apiextensionsclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating apiextensions client: %w", err)
	}

	factory := apiextensionsinformers.NewSharedInformerFactory(apiextClient, crdResyncPeriod)

	d := &CRDDiscovery{
		informerFactory: factory,
		dynClient:       dynClient,
		logger:          logger,
		crds:            make(map[string]*CRDInfo),
		countCache:      make(map[string]int),
	}

	informer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { d.onCRDAdd(obj) },
		UpdateFunc: func(_, obj interface{}) { d.onCRDAdd(obj) },
		DeleteFunc: func(obj interface{}) { d.onCRDDelete(obj) },
	}); err != nil {
		return nil, fmt.Errorf("adding CRD event handler: %w", err)
	}

	logger.Info("CRD discovery created")
	return d, nil
}

// Start begins the apiextensions informer in a goroutine. The informer stops
// when the context is cancelled.
func (d *CRDDiscovery) Start(ctx context.Context) {
	d.informerFactory.Start(ctx.Done())
	d.informerFactory.WaitForCacheSync(ctx.Done())
	d.logger.Info("CRD discovery informer synced", "crdCount", len(d.crds))
}

// ListCRDs returns all discovered CRDs grouped by API group.
// Core API groups are excluded.
func (d *CRDDiscovery) ListCRDs() map[string][]*CRDInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string][]*CRDInfo)
	for _, info := range d.crds {
		result[info.Group] = append(result[info.Group], info)
	}
	return result
}

// ResolveGVR returns the GroupVersionResource for a given group and resource name.
// Returns false if the group is denied (core API) or the CRD is not found.
func (d *CRDDiscovery) ResolveGVR(group, resource string) (schema.GroupVersionResource, bool) {
	if coreAPIDenylist[group] {
		return schema.GroupVersionResource{}, false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	key := group + "/" + resource
	info, ok := d.crds[key]
	if !ok {
		return schema.GroupVersionResource{}, false
	}
	return schema.GroupVersionResource{
		Group:    info.Group,
		Version:  info.Version,
		Resource: info.Resource,
	}, true
}

// GetCRDInfo returns the CRDInfo for a given group and resource, or nil if not found
// or denied.
func (d *CRDDiscovery) GetCRDInfo(group, resource string) *CRDInfo {
	if coreAPIDenylist[group] {
		return nil
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.crds[group+"/"+resource]
}

// IsNamespaced returns true if the CRD with the given group and resource is
// namespace-scoped. Returns false for cluster-scoped CRDs or if the CRD is not found.
func (d *CRDDiscovery) IsNamespaced(group, resource string) bool {
	info := d.GetCRDInfo(group, resource)
	if info == nil {
		return false
	}
	return info.Scope == "Namespaced"
}

// GetCounts returns cached instance counts for all discovered CRDs. If the cache
// is older than countCacheTTL, it refreshes counts using bounded concurrency.
func (d *CRDDiscovery) GetCounts(ctx context.Context) map[string]int {
	d.countMu.Lock()
	defer d.countMu.Unlock()

	if time.Since(d.countUpdated) < countCacheTTL && len(d.countCache) > 0 {
		result := make(map[string]int, len(d.countCache))
		for k, v := range d.countCache {
			result[k] = v
		}
		return result
	}

	// Snapshot current CRDs under read lock.
	d.mu.RLock()
	gvrs := make(map[string]schema.GroupVersionResource, len(d.crds))
	for key, info := range d.crds {
		gvrs[key] = schema.GroupVersionResource{
			Group:    info.Group,
			Version:  info.Version,
			Resource: info.Resource,
		}
	}
	d.mu.RUnlock()

	counts := d.fetchCounts(ctx, gvrs)
	d.countCache = counts
	d.countUpdated = time.Now()

	result := make(map[string]int, len(counts))
	for k, v := range counts {
		result[k] = v
	}
	return result
}

// fetchCounts queries instance counts for the given GVRs with bounded concurrency.
func (d *CRDDiscovery) fetchCounts(ctx context.Context, gvrs map[string]schema.GroupVersionResource) map[string]int {
	type countResult struct {
		key   string
		count int
	}

	sem := make(chan struct{}, countConcurrency)
	results := make(chan countResult, len(gvrs))

	var wg sync.WaitGroup
	for key, gvr := range gvrs {
		wg.Add(1)
		go func(k string, g schema.GroupVersionResource) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			list, err := d.dynClient.Resource(g).List(ctx, metav1.ListOptions{
				Limit: 1, // We only need the count from metadata.
			})
			if err != nil {
				d.logger.Debug("failed to count CRD instances",
					"gvr", g.String(), "error", err)
				return
			}

			count := len(list.Items)
			if list.GetContinue() != "" || list.GetRemainingItemCount() != nil {
				// There are more items; use remainingItemCount if available.
				if remaining := list.GetRemainingItemCount(); remaining != nil {
					count = int(*remaining) + len(list.Items)
				} else {
					// Fall back to unlimited list to get the real count.
					fullList, err := d.dynClient.Resource(g).List(ctx, metav1.ListOptions{})
					if err == nil {
						count = len(fullList.Items)
					}
				}
			}

			results <- countResult{key: k, count: count}
		}(key, gvr)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	counts := make(map[string]int, len(gvrs))
	for r := range results {
		counts[r.key] = r.count
	}
	return counts
}

// onCRDAdd processes a CRD add or update event from the informer.
func (d *CRDDiscovery) onCRDAdd(obj interface{}) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		d.logger.Warn("CRD informer received non-CRD object", "type", fmt.Sprintf("%T", obj))
		return
	}

	group := crd.Spec.Group
	if coreAPIDenylist[group] {
		return
	}

	info := crdInfoFromSpec(crd)
	if info == nil {
		return
	}

	key := info.Group + "/" + info.Resource

	d.mu.Lock()
	d.crds[key] = info
	d.mu.Unlock()

	d.logger.Info("CRD discovered", "group", info.Group, "resource", info.Resource, "kind", info.Kind, "scope", info.Scope)
}

// onCRDDelete processes a CRD delete event from the informer, including
// tombstone unwrapping for missed delete events.
func (d *CRDDiscovery) onCRDDelete(obj interface{}) {
	// Handle tombstone (DeletedFinalStateUnknown) for missed deletes.
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		d.logger.Warn("CRD delete: could not cast object", "type", fmt.Sprintf("%T", obj))
		return
	}

	resource := crd.Spec.Names.Plural
	group := crd.Spec.Group
	key := group + "/" + resource

	d.mu.Lock()
	delete(d.crds, key)
	d.mu.Unlock()

	d.countMu.Lock()
	delete(d.countCache, key)
	d.countMu.Unlock()

	d.logger.Info("CRD removed", "group", group, "resource", resource)
}

// crdInfoFromSpec extracts CRDInfo from a CRD spec, selecting the served storage
// version. Returns nil if no served version is found.
func crdInfoFromSpec(crd *apiextensionsv1.CustomResourceDefinition) *CRDInfo {
	var servedVersion *apiextensionsv1.CustomResourceDefinitionVersion
	for i := range crd.Spec.Versions {
		v := &crd.Spec.Versions[i]
		if !v.Served {
			continue
		}
		if v.Storage {
			servedVersion = v
			break
		}
		if servedVersion == nil {
			servedVersion = v
		}
	}

	if servedVersion == nil {
		return nil
	}

	scope := "Namespaced"
	if crd.Spec.Scope == apiextensionsv1.ClusterScoped {
		scope = "Cluster"
	}

	return &CRDInfo{
		Group:                    crd.Spec.Group,
		Version:                  servedVersion.Name,
		Resource:                 crd.Spec.Names.Plural,
		Kind:                     crd.Spec.Names.Kind,
		Scope:                    scope,
		Served:                   servedVersion.Served,
		StorageVersion:           servedVersion.Storage,
		AdditionalPrinterColumns: servedVersion.AdditionalPrinterColumns,
	}
}
