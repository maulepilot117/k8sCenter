package externalsecrets

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	// staleDuration is how long a cached discovery probe stays fresh before a
	// follow-up Status() call triggers a re-probe. Matches the cert-manager
	// precedent — we re-probe rarely because operators don't install/uninstall
	// the operator on a 5-min cadence.
	staleDuration = 5 * time.Minute

	// externalSecretsNS is the conventional namespace ESO ships into via the
	// upstream Helm chart. Used only for version detection; the discoverer
	// works regardless of what namespace ESO actually runs in (it falls back
	// to "version unknown" if the deployment isn't found).
	externalSecretsNS = "external-secrets"
)

// Discoverer probes the cluster for ESO CRDs and maintains cached discovery
// state. Concurrent calls to Status are safe; Probe serializes through the
// write lock.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status ESOStatus
}

// NewDiscoverer creates an ESO discoverer.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: ESOStatus{
			LastChecked: time.Now().UTC(),
		},
	}
}

// Status returns a copy of the cached discovery status. If the cached entry
// is older than staleDuration, the call falls through to Probe. The cache is
// pessimistic about freshness — a re-probe after the TTL is cheap (one
// discovery call), and the alternative ("trust forever") would mask
// uninstalls.
func (d *Discoverer) Status(ctx context.Context) ESOStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration {
		status := d.status
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	return d.Probe(ctx)
}

// Probe checks if external-secrets.io/v1 CRDs exist on the cluster and
// updates the cached status. Safe to call concurrently — only one probe runs
// at a time via the write lock.
func (d *Discoverer) Probe(ctx context.Context) ESOStatus {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	disco := d.k8sClient.DiscoveryClient()

	status := ESOStatus{
		LastChecked: now,
	}

	resources, err := disco.ServerResourcesForGroupVersion(GroupName + "/v1")
	if err != nil || resources == nil {
		d.logger.Debug("ESO CRDs not found", "error", err)
		d.status = status
		return status
	}

	hasExternalSecret := false
	for _, r := range resources.APIResources {
		if r.Kind == "ExternalSecret" {
			hasExternalSecret = true
			break
		}
	}
	if !hasExternalSecret {
		d.logger.Debug("ESO ExternalSecret CRD not found")
		d.status = status
		return status
	}

	status.Detected = true

	// Best-effort version detection from the conventional install namespace.
	// Failure here is silent — a cluster with ESO installed in a non-standard
	// namespace still gets Detected=true, just without a Version string.
	cs := d.k8sClient.BaseClientset()
	deps, err := cs.AppsV1().Deployments(externalSecretsNS).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=external-secrets",
	})
	if err == nil && len(deps.Items) > 0 {
		status.Namespace = externalSecretsNS
		dep := deps.Items[0]
		if v, ok := dep.Labels["app.kubernetes.io/version"]; ok {
			status.Version = v
		} else if len(dep.Spec.Template.Spec.Containers) > 0 {
			img := dep.Spec.Template.Spec.Containers[0].Image
			if i := strings.LastIndex(img, ":"); i >= 0 && i < len(img)-1 {
				status.Version = img[i+1:]
			}
		}
	}

	d.status = status
	d.logger.Info("ESO discovery completed",
		"detected", status.Detected,
		"namespace", status.Namespace,
		"version", status.Version,
	)

	return status
}

// IsAvailable returns true if ESO was detected on the cluster. Cheap — uses
// the cached status; only triggers a re-probe if the cache is stale.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Detected
}
