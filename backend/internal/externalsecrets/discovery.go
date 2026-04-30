package externalsecrets

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// staleDuration is how long a cached discovery probe stays fresh before a
// follow-up Status() call triggers a re-probe. Matches the cert-manager
// precedent — we re-probe rarely because operators don't install/uninstall
// the operator on a 5-min cadence.
const staleDuration = 5 * time.Minute

// Discoverer probes the cluster for ESO CRDs and maintains cached discovery
// state. Concurrent calls to Status are safe; Probe is non-blocking under
// the write lock — see commitStatus for the lock model.
type Discoverer struct {
	k8sClient *k8s.ClientFactory
	logger    *slog.Logger

	mu     sync.RWMutex
	status ESOStatus

	// discoOverride / depListOverride: test-only seams. Production wiring
	// leaves them nil — the real probe reads from the k8s ClientFactory.
	// Keeping seams as func fields (rather than full interface injection)
	// matches the handler.go pattern and avoids inventing a new abstraction.
	discoOverride   func() discovery.DiscoveryInterface
	depListOverride func(ctx context.Context, opts metav1.ListOptions) (*appsv1.DeploymentList, error)
}

// NewDiscoverer creates an ESO discoverer. The cached status starts with a
// zero-value LastChecked so the first Status() call falls through to a
// real Probe — otherwise the discoverer would report Detected=false (the
// zero-value status) for the full staleDuration window after restart.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		k8sClient: k8sClient,
		logger:    logger,
		status: ESOStatus{
			LastChecked: time.Time{},
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
// updates the cached status. Safe to call concurrently. The two blocking
// API-server calls (discovery + Deployments.List) run WITHOUT any lock
// held; only the cache read / write are guarded. A double-check at the
// write site short-circuits piled-up concurrent probes when an earlier
// probe already refreshed the cache.
func (d *Discoverer) Probe(ctx context.Context) ESOStatus {
	now := time.Now().UTC()

	disco := d.discovery()
	listDeps := d.deploymentLister()

	status := ESOStatus{
		LastChecked: now,
	}

	resources, err := disco.ServerResourcesForGroupVersion(GroupName + "/v1")
	if err != nil || resources == nil {
		d.logger.Debug("ESO CRDs not found", "error", err)
		d.commitStatus(status)
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
		d.commitStatus(status)
		return status
	}

	status.Detected = true

	// Best-effort version detection across all namespaces (operators
	// frequently install ESO into a non-conventional namespace; keying
	// detection to a hardcoded namespace would silently fail). The
	// LabelSelector keeps the result set small.
	deps, err := listDeps(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=external-secrets",
	})
	if err == nil && deps != nil && len(deps.Items) > 0 {
		dep := deps.Items[0]
		status.Namespace = dep.Namespace
		if v, ok := dep.Labels["app.kubernetes.io/version"]; ok {
			status.Version = v
		} else if len(dep.Spec.Template.Spec.Containers) > 0 {
			img := dep.Spec.Template.Spec.Containers[0].Image
			if i := strings.LastIndex(img, ":"); i >= 0 && i < len(img)-1 {
				status.Version = img[i+1:]
			}
		}
	}

	d.commitStatus(status)
	d.logger.Info("ESO discovery completed",
		"detected", status.Detected,
		"namespace", status.Namespace,
		"version", status.Version,
	)

	return status
}

func (d *Discoverer) discovery() discovery.DiscoveryInterface {
	if d.discoOverride != nil {
		return d.discoOverride()
	}
	return d.k8sClient.DiscoveryClient()
}

func (d *Discoverer) deploymentLister() func(ctx context.Context, opts metav1.ListOptions) (*appsv1.DeploymentList, error) {
	if d.depListOverride != nil {
		return d.depListOverride
	}
	cs := d.k8sClient.BaseClientset()
	return func(ctx context.Context, opts metav1.ListOptions) (*appsv1.DeploymentList, error) {
		return cs.AppsV1().Deployments("").List(ctx, opts)
	}
}

// commitStatus writes the probe result under the write lock with a
// double-check: if a concurrent probe already refreshed the cache while we
// were doing the network calls, that result wins (it's at least as fresh as
// ours). Without the double-check, two concurrent probes could thrash the
// cached LastChecked back and forth.
func (d *Discoverer) commitStatus(status ESOStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.status.LastChecked.IsZero() && time.Since(d.status.LastChecked) < staleDuration {
		return
	}
	d.status = status
}

// IsAvailable returns true if ESO was detected on the cluster. Cheap — uses
// the cached status; only triggers a re-probe if the cache is stale.
func (d *Discoverer) IsAvailable(ctx context.Context) bool {
	return d.Status(ctx).Detected
}
