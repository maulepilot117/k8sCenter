package velero

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// staleDuration is how long before cached status is considered stale.
	staleDuration = 5 * time.Minute
	// veleroNamespace is the default namespace where Velero is installed.
	veleroNamespace = "velero"
)

// Discoverer probes the cluster for Velero CRDs and maintains cached discovery state.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status VeleroStatus
}

// NewDiscoverer creates a new Velero discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: VeleroStatus{
			LastChecked: time.Now().UTC(),
		},
	}
}

// Status returns a copy of the cached Velero status.
// If the cache is stale (older than staleDuration), it triggers a re-probe.
func (d *Discoverer) Status(ctx context.Context) VeleroStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration {
		status := d.status
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	// Cache is stale, re-probe
	return d.Probe(ctx)
}

// Probe checks if velero.io/v1 CRDs exist and updates cached state.
func (d *Discoverer) Probe(ctx context.Context) VeleroStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	disco := d.k8sClient.DiscoveryClient()

	status := VeleroStatus{
		LastChecked: now,
	}

	// Check for Velero CRDs: look for Backup kind in velero.io/v1
	veleroResources, err := disco.ServerResourcesForGroupVersion("velero.io/v1")
	if err != nil || veleroResources == nil {
		d.logger.Debug("Velero CRDs not found", "error", err)
		d.status = status
		return status
	}

	// Check if Backup resource exists
	hasBackup := false
	for _, r := range veleroResources.APIResources {
		if r.Kind == "Backup" {
			hasBackup = true
			break
		}
	}

	if !hasBackup {
		d.logger.Debug("Velero Backup CRD not found")
		d.status = status
		return status
	}

	status.Detected = true

	// Probe the velero namespace for deployment and version
	cs := d.k8sClient.BaseClientset()
	deps, err := cs.AppsV1().Deployments(veleroNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "component=velero",
	})
	if err == nil && len(deps.Items) > 0 {
		status.Namespace = veleroNamespace
		// Extract version from deployment labels or container image
		dep := deps.Items[0]
		if v, ok := dep.Labels["app.kubernetes.io/version"]; ok {
			status.Version = v
		} else if len(dep.Spec.Template.Spec.Containers) > 0 {
			// Try to extract version from image tag
			img := dep.Spec.Template.Spec.Containers[0].Image
			if idx := len(img) - 1; idx > 0 {
				for i := len(img) - 1; i >= 0; i-- {
					if img[i] == ':' {
						status.Version = img[i+1:]
						break
					}
				}
			}
		}
	}

	// Count BSLs
	dynClient := d.k8sClient.BaseDynamicClient()
	bslList, err := dynClient.Resource(BackupStorageLocationGVR).Namespace(veleroNamespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		status.BSLCount = len(bslList.Items)
	}

	// Count VSLs
	vslList, err := dynClient.Resource(VolumeSnapshotLocationGVR).Namespace(veleroNamespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		status.VSLCount = len(vslList.Items)
	}

	d.status = status
	d.logger.Info("Velero discovery completed",
		"detected", status.Detected,
		"namespace", status.Namespace,
		"version", status.Version,
		"bslCount", status.BSLCount,
		"vslCount", status.VSLCount,
	)

	return status
}

// IsAvailable returns true if Velero was detected.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Detected
}
