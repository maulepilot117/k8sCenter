package networking

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ciliumConfigMapName is the well-known ConfigMap name for Cilium configuration.
const ciliumConfigMapName = "cilium-config"

// ciliumSearchNamespaces are namespaces to search for the Cilium ConfigMap.
var ciliumSearchNamespaces = []string{"kube-system", "cilium"}

// CiliumConfig represents the Cilium configuration response.
type CiliumConfig struct {
	CNIType            string            `json:"cniType"`
	ConfigSource       string            `json:"configSource"`
	ConfigMapName      string            `json:"configMapName"`
	ConfigMapNamespace string            `json:"configMapNamespace"`
	Editable           bool              `json:"editable"`
	Config             map[string]string `json:"config"`
}

// ReadCiliumConfig reads the cilium-config ConfigMap using the provided clientset.
// The caller should pass an impersonated clientset to enforce Kubernetes RBAC.
func ReadCiliumConfig(ctx context.Context, cs kubernetes.Interface) (*CiliumConfig, error) {
	for _, ns := range ciliumSearchNamespaces {
		cm, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		return &CiliumConfig{
			CNIType:            CNICilium,
			ConfigSource:       "configmap",
			ConfigMapName:      ciliumConfigMapName,
			ConfigMapNamespace: ns,
			Editable:           true,
			Config:             cm.Data,
		}, nil
	}

	return nil, fmt.Errorf("cilium-config ConfigMap not found in namespaces %v", ciliumSearchNamespaces)
}

// ciliumConfigAllowlist defines Cilium config keys that are safe to modify via the UI.
// Keys not in this list are rejected to prevent accidental cluster-wide networking outages.
var ciliumConfigAllowlist = map[string]bool{
	"enable-hubble":            true,
	"hubble-disable-tls":       true,
	"hubble-listen-address":    true,
	"enable-bandwidth-manager": true,
	"bpf-map-dynamic-size-ratio": true,
	"debug":                    true,
	"debug-verbose":            true,
	"monitor-aggregation":      true,
	"monitor-aggregation-interval": true,
	"prometheus-serve-addr":    true,
	"operator-prometheus-serve-addr": true,
	"enable-metrics":           true,
	"enable-ipv4":              true,
	"enable-ipv6":              true,
	"preallocate-bpf-maps":    true,
	"enable-endpoint-health-checking": true,
	"enable-health-checking":   true,
	"enable-well-known-identities": true,
	"enable-remote-node-identity": true,
	"install-no-conntrack-iptables-rules": true,
	"auto-direct-node-routes":  true,
}

// ValidateCiliumChanges validates that all keys in the changes map are in the allowlist
// and that values are within acceptable bounds.
func ValidateCiliumChanges(changes map[string]string) error {
	if len(changes) > 10 {
		return fmt.Errorf("too many changes: %d (max 10)", len(changes))
	}
	for k, v := range changes {
		if len(k) > 253 {
			return fmt.Errorf("key too long: %q (max 253 chars)", k[:50]+"...")
		}
		if len(v) > 1024 {
			return fmt.Errorf("value too long for key %q (max 1024 chars)", k)
		}
		if !ciliumConfigAllowlist[k] {
			return fmt.Errorf("key %q is not in the editable allowlist", k)
		}
	}
	return nil
}

// UpdateCiliumConfig applies changes to the cilium-config ConfigMap.
// The caller should pass an impersonated clientset to enforce Kubernetes RBAC.
// Returns the namespace where the update was applied.
func UpdateCiliumConfig(ctx context.Context, cs kubernetes.Interface, changes map[string]string) (string, error) {
	for _, ns := range ciliumSearchNamespaces {
		cm, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		if err != nil {
			continue
		}

		// Apply changes
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		for k, v := range changes {
			cm.Data[k] = v
		}

		_, err = cs.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			return ns, fmt.Errorf("failed to update cilium-config in %s: %w", ns, err)
		}
		return ns, nil
	}

	return "", fmt.Errorf("cilium-config ConfigMap not found in namespaces %v", ciliumSearchNamespaces)
}
