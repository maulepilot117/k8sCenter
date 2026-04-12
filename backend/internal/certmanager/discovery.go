package certmanager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	staleDuration = 5 * time.Minute
	certManagerNS = "cert-manager"
)

// Discoverer probes the cluster for cert-manager CRDs and maintains cached discovery state.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status CertManagerStatus
}

// NewDiscoverer creates a new cert-manager discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: CertManagerStatus{
			LastChecked: time.Now().UTC(),
		},
	}
}

// Status returns a copy of the cached cert-manager status.
// If the cache is stale (older than staleDuration), it triggers a re-probe.
func (d *Discoverer) Status(ctx context.Context) CertManagerStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration {
		status := d.status
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	return d.Probe(ctx)
}

// Probe checks if cert-manager.io/v1 CRDs exist and updates cached state.
func (d *Discoverer) Probe(ctx context.Context) CertManagerStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	disco := d.k8sClient.DiscoveryClient()

	status := CertManagerStatus{
		LastChecked: now,
	}

	// Check for cert-manager CRDs
	cmResources, err := disco.ServerResourcesForGroupVersion("cert-manager.io/v1")
	if err != nil || cmResources == nil {
		d.logger.Debug("cert-manager CRDs not found", "error", err)
		d.status = status
		return status
	}

	// Check if Certificate kind exists
	hasCert := false
	for _, r := range cmResources.APIResources {
		if r.Kind == "Certificate" {
			hasCert = true
			break
		}
	}

	if !hasCert {
		d.logger.Debug("cert-manager Certificate CRD not found")
		d.status = status
		return status
	}

	status.Detected = true

	// Probe the cert-manager namespace for deployment and version
	cs := d.k8sClient.BaseClientset()
	deps, err := cs.AppsV1().Deployments(certManagerNS).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=cert-manager",
	})
	if err == nil && len(deps.Items) > 0 {
		status.Namespace = certManagerNS
		dep := deps.Items[0]
		if v, ok := dep.Labels["app.kubernetes.io/version"]; ok {
			status.Version = v
		} else if len(dep.Spec.Template.Spec.Containers) > 0 {
			img := dep.Spec.Template.Spec.Containers[0].Image
			for i := len(img) - 1; i >= 0; i-- {
				if img[i] == ':' {
					status.Version = img[i+1:]
					break
				}
			}
		}
	}

	d.status = status
	d.logger.Info("cert-manager discovery completed",
		"detected", status.Detected,
		"namespace", status.Namespace,
		"version", status.Version,
	)

	return status
}

// IsAvailable returns true if cert-manager was detected.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Detected
}
