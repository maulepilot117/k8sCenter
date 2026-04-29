package servicemesh

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	staleDuration    = 5 * time.Minute
	recheckInterval  = 60 * time.Second
	istioSystemNS    = "istio-system"
	linkerdControlNS = "linkerd"
	istioDeployLabel = "app=istiod"
	linkerdDeployLbl = "linkerd.io/control-plane-component=identity"
	versionUnknown   = "unknown"
)

// DiscoveryChangeCallback fires when Probe detects a change in the mesh
// detection state (e.g., MeshNone → MeshIstio after install). Wired by
// main() to invalidate cross-package caches that depend on mesh data.
type DiscoveryChangeCallback func(prev, current MeshType)

// Discoverer probes the cluster for Istio and Linkerd control planes
// and maintains cached mesh-status state with a lazy re-probe on stale reads.
type Discoverer struct {
	cs     kubernetes.Interface
	disco  discovery.DiscoveryInterface
	logger *slog.Logger

	mu       sync.RWMutex
	status   MeshStatus
	onChange DiscoveryChangeCallback
}

// NewDiscoverer creates a new service-mesh discoverer. Passing a nil
// ClientFactory is supported for tests that exercise state-machine behavior;
// Probe will short-circuit to an empty status in that case.
func NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer {
	d := &Discoverer{
		logger: logger,
		status: MeshStatus{LastChecked: time.Now().UTC()},
	}
	if k8sClient != nil {
		d.cs = k8sClient.BaseClientset()
		d.disco = k8sClient.DiscoveryClient()
	}
	return d
}

// newDiscovererForTest wires a Discoverer directly to fake clients. Used only
// by unit tests in this package.
func newDiscovererForTest(cs kubernetes.Interface, disco discovery.DiscoveryInterface, logger *slog.Logger) *Discoverer {
	return &Discoverer{
		cs:     cs,
		disco:  disco,
		logger: logger,
		status: MeshStatus{LastChecked: time.Now().UTC()},
	}
}

// Status returns the cached mesh status. If the cache is older than
// staleDuration, Status triggers a Probe on the calling goroutine.
func (d *Discoverer) Status(ctx context.Context) MeshStatus {
	d.mu.RLock()
	if time.Since(d.status.LastChecked) < staleDuration {
		status := d.status
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	return d.Probe(ctx)
}

// IsInstalled returns true if either mesh was detected.
func (d *Discoverer) IsInstalled(ctx context.Context) bool {
	s := d.Status(ctx)
	return s.Istio != nil || s.Linkerd != nil
}

// SetOnChange registers a callback invoked when Probe detects a change
// in mesh detection state. Use this from main() to invalidate downstream
// caches (e.g., the handler's 30s route cache, the topology overlay) so
// a fresh install doesn't lag behind the discovery cycle.
func (d *Discoverer) SetOnChange(cb DiscoveryChangeCallback) {
	d.mu.Lock()
	d.onChange = cb
	d.mu.Unlock()
}

// RunDiscoveryLoop probes immediately, then every recheckInterval. The
// loop exits cleanly when ctx is canceled. Without this loop the
// Discoverer is purely lazy (probe-on-stale-read), which means a
// post-startup mesh install isn't noticed until the next user request.
func (d *Discoverer) RunDiscoveryLoop(ctx context.Context) {
	d.Probe(ctx)
	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Probe(ctx)
		}
	}
}

// Probe runs CRD discovery + control-plane deployment probes and refreshes
// the cached status. Callers may invoke Probe directly to force a refresh.
//
// When a mesh probe hits a transient discovery error (anything other than
// "GroupVersion not found"), the previous cached MeshInfo is preserved so
// temporary API-server hiccups do not flap the UI's detection state.
func (d *Discoverer) Probe(ctx context.Context) MeshStatus {
	// Short-circuit for the test constructor with nil client.
	if d.disco == nil {
		d.mu.Lock()
		d.status = MeshStatus{LastChecked: time.Now().UTC()}
		s := d.status
		d.mu.Unlock()
		return s
	}

	istioInfo, istioErr := d.probeIstio(ctx)
	linkerdInfo, linkerdErr := d.probeLinkerd(ctx)

	d.mu.Lock()

	prev := d.status
	status := MeshStatus{LastChecked: time.Now().UTC()}

	if istioErr != nil {
		d.logger.Warn("istio discovery failed; preserving cached state", "error", istioErr)
		status.Istio = prev.Istio
	} else {
		status.Istio = istioInfo
	}
	if linkerdErr != nil {
		d.logger.Warn("linkerd discovery failed; preserving cached state", "error", linkerdErr)
		status.Linkerd = prev.Linkerd
	} else {
		status.Linkerd = linkerdInfo
	}
	status.Detected = detectionFrom(status.Istio, status.Linkerd)

	d.status = status
	cb := d.onChange
	prevDetected := prev.Detected
	d.mu.Unlock()

	d.logger.Info("service mesh discovery completed",
		"detected", status.Detected,
		"istioInstalled", status.Istio != nil && status.Istio.Installed,
		"linkerdInstalled", status.Linkerd != nil && status.Linkerd.Installed,
	)

	// Fire the change callback OUTSIDE the lock so callbacks (typically
	// cache invalidations on the handler) don't block subsequent probes.
	// Only fire on a real transition; a steady state keeps caches warm.
	if cb != nil && prevDetected != status.Detected {
		cb(prevDetected, status.Detected)
	}
	return status
}

// probeIstio returns (nil, nil) when Istio is not installed, (info, nil) when
// detected, and (nil, err) on a hard discovery error that the caller should
// treat as transient.
func (d *Discoverer) probeIstio(ctx context.Context) (*MeshInfo, error) {
	present, err := d.hasGroupVersionKind("networking.istio.io/v1", "VirtualService")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}

	// CRDs imply install; default to Version=unknown and fill in from the
	// istiod deployment when it is reachable.
	info := &MeshInfo{Installed: true, Version: versionUnknown, Mode: MeshModeSidecar}

	deps, err := d.cs.AppsV1().Deployments(istioSystemNS).List(ctx, metav1.ListOptions{
		LabelSelector: istioDeployLabel,
	})
	if err == nil && len(deps.Items) > 0 {
		info.Namespace = istioSystemNS
		if v := versionForDeployment(deps.Items[0], "app.kubernetes.io/version"); v != "" {
			info.Version = v
		}
	}

	// Ambient mode heuristic: ztunnel DaemonSet in istio-system.
	ds, err := d.cs.AppsV1().DaemonSets(istioSystemNS).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ztunnel",
	})
	if err == nil && len(ds.Items) > 0 {
		info.Mode = MeshModeAmbient
	}

	return info, nil
}

func (d *Discoverer) probeLinkerd(ctx context.Context) (*MeshInfo, error) {
	present, err := d.hasGroupVersionKind("policy.linkerd.io/v1beta3", "Server")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}

	info := &MeshInfo{Installed: true, Version: versionUnknown}

	deps, err := d.cs.AppsV1().Deployments(linkerdControlNS).List(ctx, metav1.ListOptions{
		LabelSelector: linkerdDeployLbl,
	})
	if err == nil && len(deps.Items) > 0 {
		info.Namespace = linkerdControlNS
		if v := versionForDeployment(deps.Items[0], "linkerd.io/control-plane-version"); v != "" {
			info.Version = v
		}
	}

	return info, nil
}

// hasGroupVersionKind returns whether the cluster's discovery layer reports
// a named Kind under the given GroupVersion. A missing GroupVersion is
// returned as (false, nil); any other discovery error is returned verbatim
// so the caller can preserve cached state instead of flipping to "not installed".
func (d *Discoverer) hasGroupVersionKind(groupVersion, kind string) (bool, error) {
	resources, err := d.disco.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if resources == nil {
		return false, nil
	}
	for _, r := range resources.APIResources {
		if r.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

// detectionFrom collapses per-mesh detection into the MeshType discriminator.
func detectionFrom(istio, linkerd *MeshInfo) MeshType {
	switch {
	case istio != nil && linkerd != nil:
		return MeshBoth
	case istio != nil:
		return MeshIstio
	case linkerd != nil:
		return MeshLinkerd
	default:
		return MeshNone
	}
}

// versionFromDeploymentImage extracts the tag from the first container image
// (e.g., "docker.io/istio/pilot:1.24.0" -> "1.24.0"). Returns empty if the
// image has no tag or the container list is empty.
func versionFromDeploymentImage(containers []corev1.Container) string {
	if len(containers) == 0 {
		return ""
	}
	img := containers[0].Image
	idx := strings.LastIndexByte(img, ':')
	if idx < 0 || idx == len(img)-1 {
		return ""
	}
	return img[idx+1:]
}

// versionForDeployment prefers the image tag and falls back to the named
// label. Returns empty when neither is usable — callers should default to
// versionUnknown in that case.
func versionForDeployment(dep appsv1.Deployment, fallbackLabel string) string {
	if v := versionFromDeploymentImage(dep.Spec.Template.Spec.Containers); v != "" {
		return v
	}
	if v, ok := dep.Labels[fallbackLabel]; ok {
		return v
	}
	return ""
}
