package networking

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/kubecenter/kubecenter/internal/k8s"
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
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	Namespace       string    `json:"namespace"`
	DaemonSet       string    `json:"daemonSet"`
	Status          CNIStatus `json:"status"`
	Features        CNIFeatures `json:"features,omitempty"`
	HasCRDs         bool      `json:"hasCRDs"`
	DetectionMethod string    `json:"detectionMethod"`
}

// CNIStatus describes the health of the CNI DaemonSet.
type CNIStatus struct {
	Ready   int32 `json:"ready"`
	Desired int32 `json:"desired"`
	Healthy bool  `json:"healthy"`
}

// CNIFeatures describes detected CNI capabilities (Cilium-specific for now).
type CNIFeatures struct {
	Hubble         bool   `json:"hubble"`
	Encryption     bool   `json:"encryption"`
	EncryptionMode string `json:"encryptionMode,omitempty"`
	ClusterMesh    bool   `json:"clusterMesh"`
	WireGuard      bool   `json:"wireguard"`
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

		name := strings.ToLower(ds.Name)
		switch {
		case name == "cilium" || strings.HasPrefix(name, "cilium-"):
			if name == "cilium" || name == "cilium-agent" {
				version := extractImageVersion(ds.Spec.Template.Spec.Containers, "cilium-agent")
				if version == "" {
					version = extractImageVersion(ds.Spec.Template.Spec.Containers, "")
				}
				info := &CNIInfo{
					Name:      CNICilium,
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
				// Check for Cilium CRDs
				info.HasCRDs = d.checkCRDGroup("cilium.io")
				if info.HasCRDs {
					info.DetectionMethod = "daemonset+crd"
				}
				// Detect features from cilium-config ConfigMap
				info.Features = d.detectCiliumFeatures(ctx)
				return info
			}

		case name == "calico-node" || strings.HasPrefix(name, "calico-"):
			if name == "calico-node" {
				version := extractImageVersion(ds.Spec.Template.Spec.Containers, "calico-node")
				info := &CNIInfo{
					Name:      CNICalico,
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
				info.HasCRDs = d.checkCRDGroup("crd.projectcalico.org")
				if info.HasCRDs {
					info.DetectionMethod = "daemonset+crd"
				}
				return info
			}

		case name == "kube-flannel-ds" || name == "flannel":
			version := extractImageVersion(ds.Spec.Template.Spec.Containers, "kube-flannel")
			info := &CNIInfo{
				Name:      CNIFlannel,
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
			return info
		}
	}

	// Strategy 2: CRD-only check (DaemonSet might be in unexpected namespace)
	if d.checkCRDGroup("cilium.io") {
		return &CNIInfo{Name: CNICilium, HasCRDs: true, DetectionMethod: "crd-only"}
	}
	if d.checkCRDGroup("crd.projectcalico.org") {
		return &CNIInfo{Name: CNICalico, HasCRDs: true, DetectionMethod: "crd-only"}
	}

	return &CNIInfo{Name: CNIUnknown, DetectionMethod: "none"}
}

// checkCRDGroup checks if an API group is registered.
func (d *Detector) checkCRDGroup(group string) bool {
	disc := d.k8sClient.DiscoveryClient()
	if disc == nil {
		return false
	}
	groups, err := disc.ServerGroups()
	if err != nil {
		return false
	}
	for _, g := range groups.Groups {
		if g.Name == group {
			return true
		}
	}
	return false
}

// detectCiliumFeatures reads the cilium-config ConfigMap to detect enabled features.
func (d *Detector) detectCiliumFeatures(ctx context.Context) CNIFeatures {
	features := CNIFeatures{}
	cs := d.k8sClient.BaseClientset()

	for _, ns := range ciliumSearchNamespaces {
		cm, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		features.Hubble = cm.Data["enable-hubble"] == "true"
		features.Encryption = cm.Data["enable-encryption"] == "true"
		features.EncryptionMode = cm.Data["encryption-type"]
		features.ClusterMesh = cm.Data["cluster-mesh-config"] != ""
		features.WireGuard = cm.Data["encryption-type"] == "wireguard"
		return features
	}

	return features
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

