package networking

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/kubecenter/kubecenter/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// CNI type constants.
const (
	CNICilium  = "cilium"
	CNICalico  = "calico"
	CNIFlannel = "flannel"
	CNIUnknown = "unknown"
)

// cniSearchNamespaces are namespaces to scan for CNI DaemonSets.
var cniSearchNamespaces = []string{"kube-system", "cilium", "calico-system", "kube-flannel"}

// CNIInfo represents the detected CNI plugin information.
type CNIInfo struct {
	Name      string      `json:"name"`
	Version   string      `json:"version"`
	Namespace string      `json:"namespace"`
	DaemonSet string      `json:"daemonSet"`
	Status    CNIStatus   `json:"status"`
	Features  CNIFeatures `json:"features,omitempty"`
	HasCRDs   bool        `json:"hasCRDs"`
	// CRDsUnknown is set when a remote cluster's discovery could not be read,
	// so HasCRDs false means "unknown", not "absent". Never set locally.
	CRDsUnknown     bool   `json:"crdsUnknown,omitempty"`
	DetectionMethod string `json:"detectionMethod"`
}

// CNIStatus describes the health of the CNI DaemonSet.
type CNIStatus struct {
	Ready   int32 `json:"ready"`
	Desired int32 `json:"desired"`
	Healthy bool  `json:"healthy"`
}

// CNIFeatures describes detected CNI capabilities (Cilium-specific for now).
type CNIFeatures struct {
	Hubble          bool   `json:"hubble"`
	HubbleRelayAddr string `json:"hubbleRelayAddr,omitempty"`
	Encryption      bool   `json:"encryption"`
	EncryptionMode  string `json:"encryptionMode,omitempty"`
	ClusterMesh     bool   `json:"clusterMesh"`
	WireGuard       bool   `json:"wireguard"`
	EnvoyEnabled    bool   `json:"envoyEnabled"`
	IPAMMode        string `json:"ipamMode,omitempty"`
}

// Detector probes the cluster for the installed CNI plugin.
type Detector struct {
	k8sClient *k8s.ClientFactory
	informers *k8s.InformerManager
	logger    *slog.Logger

	mu     sync.RWMutex
	cached *CNIInfo
}

// NewDetector creates a CNI detector.
func NewDetector(k8sClient *k8s.ClientFactory, informers *k8s.InformerManager, logger *slog.Logger) *Detector {
	return &Detector{
		k8sClient: k8sClient,
		informers: informers,
		logger:    logger,
	}
}

// Detect probes the cluster for the installed CNI plugin and caches the result.
func (d *Detector) Detect(ctx context.Context) *CNIInfo {
	info := d.detect(ctx)

	d.mu.Lock()
	d.cached = info
	d.mu.Unlock()

	return info
}

// CachedInfo returns the last detection result.
func (d *Detector) CachedInfo() *CNIInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cached != nil {
		c := *d.cached
		return &c
	}
	return nil
}

func (d *Detector) detect(ctx context.Context) *CNIInfo {
	// Fetch server groups once for all CRD group checks
	serverGroups := d.fetchServerGroups()

	// Strategy 1: DaemonSet scan across known namespaces
	allDS, err := d.informers.DaemonSets().List(labels.Everything())
	if err != nil {
		d.logger.Error("failed to list daemonsets for CNI detection", "error", err)
		return &CNIInfo{Name: CNIUnknown, DetectionMethod: "failed"}
	}

	for _, ds := range allDS {
		// Only check CNI namespaces
		if !slices.Contains(cniSearchNamespaces, ds.Namespace) {
			continue
		}
		info := cniInfoFromDaemonSet(ds)
		if info == nil {
			continue
		}
		if group := cniCRDGroup(info.Name); group != "" {
			// Check for the CNI's CRDs using pre-fetched groups
			info.HasCRDs = hasAPIGroup(serverGroups, group)
			if info.HasCRDs {
				info.DetectionMethod = "daemonset+crd"
			}
		}
		if info.Name == CNICilium {
			// Detect features from cilium-config ConfigMap
			info.Features = d.detectCiliumFeatures(ctx)
		}
		return info
	}

	// Strategy 2: CRD-only check (DaemonSet might be in unexpected namespace)
	if hasAPIGroup(serverGroups, "cilium.io") {
		return &CNIInfo{Name: CNICilium, HasCRDs: true, DetectionMethod: "crd-only"}
	}
	if hasAPIGroup(serverGroups, "crd.projectcalico.org") {
		return &CNIInfo{Name: CNICalico, HasCRDs: true, DetectionMethod: "crd-only"}
	}

	return &CNIInfo{Name: CNIUnknown, DetectionMethod: "none"}
}

// cniInfoFromDaemonSet returns the CNI a DaemonSet runs, or nil when it is
// not a CNI agent DaemonSet. HasCRDs and Features are left for the caller,
// which reads them from its own cluster.
func cniInfoFromDaemonSet(ds *appsv1.DaemonSet) *CNIInfo {
	var cni, container string
	switch name := strings.ToLower(ds.Name); name {
	case "cilium", "cilium-agent":
		cni, container = CNICilium, "cilium-agent"
	case "calico-node":
		cni, container = CNICalico, "calico-node"
	case "kube-flannel-ds", "flannel":
		cni, container = CNIFlannel, "kube-flannel"
	default:
		return nil
	}
	version := extractImageVersion(ds.Spec.Template.Spec.Containers, container)
	if version == "" && cni == CNICilium {
		version = extractImageVersion(ds.Spec.Template.Spec.Containers, "")
	}
	return &CNIInfo{
		Name:      cni,
		Version:   version,
		Namespace: ds.Namespace,
		DaemonSet: ds.Name,
		Status: CNIStatus{
			Ready:   ds.Status.NumberReady,
			Desired: ds.Status.DesiredNumberScheduled,
			Healthy: ds.Status.NumberReady == ds.Status.DesiredNumberScheduled && ds.Status.DesiredNumberScheduled > 0,
		},
		DetectionMethod: "daemonset",
	}
}

// cniCRDGroup returns the API group whose presence marks a CNI's CRDs, or ""
// for a CNI detected without CRDs.
func cniCRDGroup(cni string) string {
	switch cni {
	case CNICilium:
		return "cilium.io"
	case CNICalico:
		return "crd.projectcalico.org"
	}
	return ""
}

// ciliumFeaturesFromConfig reads Cilium's enabled features from the
// cilium-config ConfigMap data. The Hubble Relay address is not part of it.
func ciliumFeaturesFromConfig(data map[string]string) CNIFeatures {
	return CNIFeatures{
		Hubble:         data["enable-hubble"] == "true",
		Encryption:     data["enable-encryption"] == "true",
		EncryptionMode: data["encryption-type"],
		ClusterMesh:    data["cluster-mesh-config"] != "",
		WireGuard:      data["encryption-type"] == "wireguard",
		EnvoyEnabled:   data["enable-envoy-config"] == "true",
		IPAMMode:       data["ipam"],
	}
}

// fetchServerGroups retrieves the API server groups once for reuse.
func (d *Detector) fetchServerGroups() []metav1.APIGroup {
	// nolint:cluster-routing local path: the Detector detects the local cluster; a remote cluster is detected by detectRemote (remote.go)
	disc := d.k8sClient.DiscoveryClient()
	if disc == nil {
		return nil
	}
	groups, err := disc.ServerGroups()
	if err != nil {
		d.logger.Debug("failed to fetch server groups", "error", err)
		return nil
	}
	return groups.Groups
}

// hasAPIGroup checks if the given group name exists in the provided server groups list.
func hasAPIGroup(groups []metav1.APIGroup, name string) bool {
	for _, g := range groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// detectCiliumFeatures reads the cilium-config ConfigMap to detect enabled features.
func (d *Detector) detectCiliumFeatures(ctx context.Context) CNIFeatures {
	features := CNIFeatures{}
	// nolint:cluster-routing local path: the Detector detects the local cluster; a remote cluster is detected by detectRemote (remote.go)
	cs := d.k8sClient.BaseClientset()

	for _, ns := range ciliumSearchNamespaces() {
		cm, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		features = ciliumFeaturesFromConfig(cm.Data)

		// Discover Hubble Relay service address
		if features.Hubble {
			if svc, err := cs.CoreV1().Services(ns).Get(ctx, "hubble-relay", metav1.GetOptions{}); err == nil {
				port := int32(80)
				if len(svc.Spec.Ports) > 0 {
					port = svc.Spec.Ports[0].Port
				}
				features.HubbleRelayAddr = fmt.Sprintf("hubble-relay.%s.svc.cluster.local:%d", ns, port)
				d.logger.Info("hubble relay discovered", "addr", features.HubbleRelayAddr)
			}
		}

		return features
	}

	return features
}

// DetectMesh returns the active service mesh engine based on cached feature detection.
// Returns "cilium" if Cilium's Envoy-based mesh is enabled, "none" otherwise.
// Istio/Linkerd detection is deferred to Phase C (needs per-mesh adapter + test environment).
func (d *Detector) DetectMesh() string {
	info := d.CachedInfo()
	if info == nil {
		return "none"
	}
	return meshEngineFor(info.Features)
}

// meshEngineFor returns the service mesh engine Cilium's features enable:
// "cilium" when its Envoy-based mesh is on, "none" otherwise.
func meshEngineFor(f CNIFeatures) string {
	if f.EnvoyEnabled {
		return "cilium"
	}
	return "none"
}

// extractImageVersion extracts the version tag from a container image.
func extractImageVersion(containers []corev1.Container, containerName string) string {
	for _, c := range containers {
		if containerName != "" && c.Name != containerName {
			continue
		}
		parts := strings.SplitN(c.Image, ":", 2)
		if len(parts) == 2 {
			return strings.TrimPrefix(parts[1], "v")
		}
		return ""
	}
	return ""
}
