package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const staleDuration = 5 * time.Minute

// Discoverer probes the cluster for Gateway API CRDs and maintains cached discovery state.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status GatewayAPIStatus
}

// NewDiscoverer creates a new Gateway API discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: GatewayAPIStatus{
			LastChecked: time.Now().UTC(),
		},
	}
}

// Status returns a copy of the cached Gateway API status.
// If the cache is stale (older than staleDuration), it triggers a re-probe.
func (d *Discoverer) Status(ctx context.Context) GatewayAPIStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration {
		status := d.status
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	return d.Probe(ctx)
}

// IsAvailable returns true if Gateway API CRDs were detected.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Available
}

// Probe checks if gateway.networking.k8s.io CRDs exist on the local cluster
// and updates cached state.
func (d *Discoverer) Probe(ctx context.Context) GatewayAPIStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	// nolint:cluster-routing local path: the Discoverer only ever probes the local cluster; remote status comes from Handler.remoteStatus.
	disco := d.k8sClient.DiscoveryClient()

	var lists []*metav1.APIResourceList
	for _, version := range []string{"v1", "v1alpha2"} {
		list, err := disco.ServerResourcesForGroupVersion(APIGroup + "/" + version)
		if err != nil || list == nil {
			d.logger.Debug("gateway API group version not served", "version", version, "error", err)
			continue
		}
		lists = append(lists, list)
	}

	status := statusFromLists(lists)
	status.LastChecked = time.Now().UTC()
	d.status = status
	d.logger.Info("gateway API discovery completed",
		"available", status.Available,
		"version", status.Version,
		"kinds", status.InstalledKinds,
	)
	return status
}

// kindToResource maps Gateway API kinds to the lowercase plural resource
// names InstalledKinds reports (matching frontend GatewayResourceKind).
var kindToResource = map[string]string{
	"GatewayClass": "gatewayclasses",
	"Gateway":      "gateways",
	"HTTPRoute":    "httproutes",
	"GRPCRoute":    "grpcroutes",
	"TCPRoute":     "tcproutes",
	"TLSRoute":     "tlsroutes",
	"UDPRoute":     "udproutes",
}

// statusFromLists derives the Gateway API status from a cluster's discovery
// lists, local or remote. Gateway API is available when v1 serves both
// Gateway and GatewayClass. Each non-HTTP route kind is recorded at the
// version the cluster serves it: TLSRoute at v1 when promoted there,
// otherwise v1alpha2. LastChecked is left to the caller.
func statusFromLists(lists []*metav1.APIResourceList) GatewayAPIStatus {
	kinds := map[string]map[string]bool{} // version -> kind set
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil || gv.Group != APIGroup {
			continue
		}
		if kinds[gv.Version] == nil {
			kinds[gv.Version] = map[string]bool{}
		}
		for _, r := range l.APIResources {
			// Skip sub-resources (contain "/").
			if !strings.Contains(r.Name, "/") {
				kinds[gv.Version][r.Kind] = true
			}
		}
	}

	v1, v1alpha2 := kinds["v1"], kinds["v1alpha2"]
	if !v1["Gateway"] || !v1["GatewayClass"] {
		return GatewayAPIStatus{}
	}

	status := GatewayAPIStatus{
		Available: true,
		Version:   "v1",
		routeGVRs: map[routeKind]schema.GroupVersionResource{},
	}
	add := func(kind, version string) {
		resource := kindToResource[kind]
		status.InstalledKinds = append(status.InstalledKinds, resource)
		if rk := routeKind(resource); routeKindToKind[resource] != "" {
			status.routeGVRs[rk] = schema.GroupVersionResource{Group: APIGroup, Version: version, Resource: resource}
		}
	}
	for _, kind := range []string{"GatewayClass", "Gateway", "HTTPRoute", "GRPCRoute"} {
		if v1[kind] {
			add(kind, "v1")
		}
	}
	switch {
	case v1["TLSRoute"]:
		add("TLSRoute", "v1")
	case v1alpha2["TLSRoute"]:
		add("TLSRoute", "v1alpha2")
	}
	for _, kind := range []string{"TCPRoute", "UDPRoute"} {
		if v1alpha2[kind] {
			add(kind, "v1alpha2")
		}
	}
	return status
}
