package externalsecrets

import (
	"context"

	"github.com/kubecenter/kubecenter/internal/topology"
)

// ESOChainDetected lets the topology overlay distinguish "ESO is not
// installed" from "ESO is installed but this namespace has no visible chain".
func (h *Handler) ESOChainDetected(ctx context.Context) bool {
	return h.Discoverer != nil && h.Discoverer.IsAvailable(ctx)
}

// ESOChainSnapshot adapts the handler's shared cached inventory to the
// topology package's DTOs. The snapshot is intentionally not user-filtered;
// topology.Builder owns the RBAC checks so graph behavior stays consistent
// with the mesh overlay.
func (h *Handler) ESOChainSnapshot(ctx context.Context) (topology.ESOChainSnapshot, error) {
	data, err := h.getCached(ctx)
	if err != nil {
		return topology.ESOChainSnapshot{}, err
	}

	out := topology.ESOChainSnapshot{
		ExternalSecrets: make([]topology.ESOChainExternalSecret, 0, len(data.externalSecrets)),
		Stores:          make([]topology.ESOChainStore, 0, len(data.stores)),
		ClusterStores:   make([]topology.ESOChainStore, 0, len(data.clusterStores)),
	}
	for _, es := range data.externalSecrets {
		out.ExternalSecrets = append(out.ExternalSecrets, topology.ESOChainExternalSecret{
			Namespace:        es.Namespace,
			Name:             es.Name,
			UID:              es.UID,
			StoreRefName:     es.StoreRef.Name,
			StoreRefKind:     es.StoreRef.Kind,
			TargetSecretName: es.TargetSecretName,
			Status:           string(es.Status),
		})
	}
	for _, store := range data.stores {
		out.Stores = append(out.Stores, topology.ESOChainStore{
			Namespace:    store.Namespace,
			Name:         store.Name,
			UID:          store.UID,
			Scope:        store.Scope,
			Provider:     store.Provider,
			ProviderSpec: store.ProviderSpec,
			Status:       string(store.Status),
		})
	}
	for _, store := range data.clusterStores {
		out.ClusterStores = append(out.ClusterStores, topology.ESOChainStore{
			Namespace:    store.Namespace,
			Name:         store.Name,
			UID:          store.UID,
			Scope:        store.Scope,
			Provider:     store.Provider,
			ProviderSpec: store.ProviderSpec,
			Status:       string(store.Status),
		})
	}
	return out, nil
}
