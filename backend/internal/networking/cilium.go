package networking

import (
	"context"
	"fmt"

	"github.com/kubecenter/kubecenter/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// ReadCiliumConfig reads the cilium-config ConfigMap and returns it.
func ReadCiliumConfig(ctx context.Context, k8sClient *k8s.ClientFactory) (*CiliumConfig, error) {
	cs := k8sClient.BaseClientset()

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

// UpdateCiliumConfig patches the cilium-config ConfigMap with the given changes.
func UpdateCiliumConfig(ctx context.Context, k8sClient *k8s.ClientFactory, changes map[string]string) error {
	cs := k8sClient.BaseClientset()

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
			return fmt.Errorf("failed to update cilium-config in %s: %w", ns, err)
		}
		return nil
	}

	return fmt.Errorf("cilium-config ConfigMap not found in namespaces %v", ciliumSearchNamespaces)
}
