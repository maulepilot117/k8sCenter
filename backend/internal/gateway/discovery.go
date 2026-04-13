package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

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

// Probe checks if gateway.networking.k8s.io CRDs exist and updates cached state.
func (d *Discoverer) Probe(ctx context.Context) GatewayAPIStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	status := GatewayAPIStatus{
		LastChecked: time.Now().UTC(),
	}

	disco := d.k8sClient.DiscoveryClient()

	// Check for Gateway API v1 CRDs.
	v1Resources, err := disco.ServerResourcesForGroupVersion("gateway.networking.k8s.io/v1")
	if err != nil || v1Resources == nil {
		d.logger.Debug("gateway API CRDs not found", "error", err)
		d.status = status
		return status
	}

	// Build a set of available Kind names, skipping sub-resources (contain "/").
	v1Kinds := make(map[string]bool)
	for _, r := range v1Resources.APIResources {
		if strings.Contains(r.Name, "/") {
			continue
		}
		v1Kinds[r.Kind] = true
	}

	// Require both Gateway and GatewayClass as minimum for availability.
	if !v1Kinds["Gateway"] || !v1Kinds["GatewayClass"] {
		d.logger.Debug("gateway API missing required kinds (Gateway, GatewayClass)")
		d.status = status
		return status
	}

	status.Available = true
	status.Version = "v1"

	// Collect installed kinds as lowercase plural resource names (matching frontend GatewayResourceKind).
	kindToResource := map[string]string{
		"GatewayClass": "gatewayclasses",
		"Gateway":      "gateways",
		"HTTPRoute":    "httproutes",
		"GRPCRoute":    "grpcroutes",
		"TCPRoute":     "tcproutes",
		"TLSRoute":     "tlsroutes",
		"UDPRoute":     "udproutes",
	}
	for _, kind := range []string{"GatewayClass", "Gateway", "HTTPRoute", "GRPCRoute"} {
		if v1Kinds[kind] {
			status.InstalledKinds = append(status.InstalledKinds, kindToResource[kind])
		}
	}

	// Check if TLSRoute is already at v1 (some clusters promote it early).
	hasTLSRoute := false
	if v1Kinds["TLSRoute"] {
		status.InstalledKinds = append(status.InstalledKinds, kindToResource["TLSRoute"])
		hasTLSRoute = true
	}

	// Probe v1alpha2 for experimental route kinds.
	v1a2Resources, err := disco.ServerResourcesForGroupVersion("gateway.networking.k8s.io/v1alpha2")
	if err == nil && v1a2Resources != nil {
		for _, r := range v1a2Resources.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			switch r.Kind {
			case "TCPRoute", "UDPRoute":
				status.InstalledKinds = append(status.InstalledKinds, kindToResource[r.Kind])
			case "TLSRoute":
				// Only add if not already found in v1.
				if !hasTLSRoute {
					status.InstalledKinds = append(status.InstalledKinds, kindToResource[r.Kind])
				}
			}
		}
	}

	d.status = status
	d.logger.Info("gateway API discovery completed",
		"available", status.Available,
		"version", status.Version,
		"kinds", status.InstalledKinds,
	)

	return status
}
