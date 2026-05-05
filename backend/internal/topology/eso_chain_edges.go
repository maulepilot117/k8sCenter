package topology

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ESOChainSnapshot is the cached ESO inventory needed by the topology overlay.
// It intentionally contains only metadata and provider specs; Secret values
// are never read or held by the topology package.
type ESOChainSnapshot struct {
	ExternalSecrets []ESOChainExternalSecret
	Stores          []ESOChainStore
	ClusterStores   []ESOChainStore
}

type ESOChainExternalSecret struct {
	Namespace        string
	Name             string
	UID              string
	StoreRefName     string
	StoreRefKind     string
	TargetSecretName string
	Status           string
}

type ESOChainStore struct {
	Namespace    string
	Name         string
	UID          string
	Scope        string
	Provider     string
	ProviderSpec map[string]any
	Status       string
}

type esoChainAccess struct {
	stores        bool
	clusterStores bool
	secrets       bool
	pods          bool
}

func buildESOChainEdges(
	snapshot ESOChainSnapshot,
	namespace string,
	nameIndex map[string]string,
	pods []*corev1.Pod,
	access esoChainAccess,
	maxEdges int,
) ([]Edge, bool) {
	if namespace == "" {
		return nil, false
	}

	secretConsumers := indexPodSecretConsumers(pods)
	storesByKey := make(map[string]ESOChainStore, len(snapshot.Stores))
	for _, store := range snapshot.Stores {
		if store.Namespace == namespace {
			storesByKey[store.Namespace+"/"+store.Name] = store
		}
	}
	clusterStoresByName := make(map[string]ESOChainStore, len(snapshot.ClusterStores))
	for _, store := range snapshot.ClusterStores {
		clusterStoresByName[store.Name] = store
	}

	edges := []Edge{}
	seen := map[string]struct{}{}
	add := func(source, target string, edgeType EdgeType) bool {
		if source == "" || target == "" {
			return false
		}
		key := source + "->" + target + "/" + string(edgeType)
		if _, ok := seen[key]; ok {
			return false
		}
		if maxEdges > 0 && len(edges) >= maxEdges {
			return true
		}
		seen[key] = struct{}{}
		edges = append(edges, Edge{Source: source, Target: target, Type: edgeType})
		return false
	}

	for _, es := range snapshot.ExternalSecrets {
		if es.Namespace != namespace || es.UID == "" {
			continue
		}

		store, storeVisible := resolveESOStore(es, namespace, storesByKey, clusterStoresByName, access)
		if storeVisible {
			for _, authName := range authSecretNames(store.ProviderSpec) {
				if access.secrets {
					if capped := add(secretNodeID(namespace, authName), store.UID, EdgeESOAuth); capped {
						return edges, true
					}
				}
			}
			if capped := add(store.UID, es.UID, EdgeESOSync); capped {
				return edges, true
			}
		}

		if !access.secrets || es.TargetSecretName == "" {
			continue
		}
		targetSecretID := secretNodeID(namespace, es.TargetSecretName)
		if capped := add(es.UID, targetSecretID, EdgeESOSync); capped {
			return edges, true
		}
		if !access.pods {
			continue
		}
		for _, podUID := range secretConsumers[es.TargetSecretName] {
			if _, ok := nameIndex["Pod/"+podUID.name]; !ok {
				continue
			}
			if capped := add(targetSecretID, podUID.uid, EdgeESOConsumer); capped {
				return edges, true
			}
		}
	}

	return edges, false
}

func appendESOChainNodes(
	graph *Graph,
	snapshot ESOChainSnapshot,
	namespace string,
	nameIndex map[string]string,
	access esoChainAccess,
) {
	addNode := func(node Node, indexKey string) {
		if node.ID == "" || len(graph.Nodes) >= maxNodes {
			graph.Truncated = true
			return
		}
		for _, existing := range graph.Nodes {
			if existing.ID == node.ID {
				return
			}
		}
		graph.Nodes = append(graph.Nodes, node)
		if indexKey != "" {
			nameIndex[indexKey] = node.ID
		}
	}

	for _, store := range snapshot.Stores {
		if !access.stores || store.Namespace != namespace {
			continue
		}
		addNode(Node{
			ID:        store.UID,
			Kind:      "SecretStore",
			Name:      store.Name,
			Namespace: store.Namespace,
			Health:    esoStatusHealth(store.Status),
			Summary:   compactSummary(store.Provider, store.Status),
		}, "SecretStore/"+store.Name)
		if access.secrets {
			for _, authName := range authSecretNames(store.ProviderSpec) {
				addSecretNode(addNode, namespace, authName)
			}
		}
	}

	for _, store := range snapshot.ClusterStores {
		if !access.clusterStores {
			continue
		}
		addNode(Node{
			ID:        store.UID,
			Kind:      "ClusterSecretStore",
			Name:      store.Name,
			Namespace: "",
			Health:    esoStatusHealth(store.Status),
			Summary:   compactSummary(store.Provider, store.Status),
		}, "ClusterSecretStore/"+store.Name)
		if access.secrets {
			for _, authName := range authSecretNames(store.ProviderSpec) {
				addSecretNode(addNode, namespace, authName)
			}
		}
	}

	for _, es := range snapshot.ExternalSecrets {
		if es.Namespace != namespace || es.UID == "" {
			continue
		}
		addNode(Node{
			ID:        es.UID,
			Kind:      "ExternalSecret",
			Name:      es.Name,
			Namespace: es.Namespace,
			Health:    esoStatusHealth(es.Status),
			Summary:   compactSummary(es.StoreRefKind+"/"+es.StoreRefName, es.Status),
		}, "ExternalSecret/"+es.Name)
		if access.secrets && es.TargetSecretName != "" {
			addSecretNode(addNode, namespace, es.TargetSecretName)
		}
	}
}

func resolveESOStore(
	es ESOChainExternalSecret,
	namespace string,
	storesByKey map[string]ESOChainStore,
	clusterStoresByName map[string]ESOChainStore,
	access esoChainAccess,
) (ESOChainStore, bool) {
	switch es.StoreRefKind {
	case "", "SecretStore":
		if !access.stores {
			return ESOChainStore{}, false
		}
		store, ok := storesByKey[namespace+"/"+es.StoreRefName]
		return store, ok && store.UID != ""
	case "ClusterSecretStore":
		if !access.clusterStores {
			return ESOChainStore{}, false
		}
		store, ok := clusterStoresByName[es.StoreRefName]
		return store, ok && store.UID != ""
	default:
		return ESOChainStore{}, false
	}
}

func addSecretNode(add func(Node, string), namespace, name string) {
	if namespace == "" || name == "" {
		return
	}
	add(Node{
		ID:        secretNodeID(namespace, name),
		Kind:      "Secret",
		Name:      name,
		Namespace: namespace,
		Health:    HealthUnknown,
		Summary:   "secret reference",
	}, "Secret/"+name)
}

func secretNodeID(namespace, name string) string {
	return "core/Secret/" + namespace + "/" + name
}

type podRef struct {
	name string
	uid  string
}

func indexPodSecretConsumers(pods []*corev1.Pod) map[string][]podRef {
	out := map[string][]podRef{}
	add := func(pod *corev1.Pod, secretName string) {
		if pod == nil || secretName == "" {
			return
		}
		out[secretName] = append(out[secretName], podRef{name: pod.Name, uid: string(pod.UID)})
	}
	for _, pod := range pods {
		for _, pull := range pod.Spec.ImagePullSecrets {
			add(pod, pull.Name)
		}
		for _, vol := range pod.Spec.Volumes {
			if vol.Secret != nil {
				add(pod, vol.Secret.SecretName)
			}
		}
		for _, c := range pod.Spec.InitContainers {
			addContainerSecretRefs(pod, c, add)
		}
		for _, c := range pod.Spec.Containers {
			addContainerSecretRefs(pod, c, add)
		}
		for _, c := range pod.Spec.EphemeralContainers {
			addContainerSecretRefs(pod, corev1.Container{
				Env:          c.Env,
				EnvFrom:      c.EnvFrom,
				VolumeMounts: c.VolumeMounts,
			}, add)
		}
	}
	return out
}

func addContainerSecretRefs(pod *corev1.Pod, c corev1.Container, add func(*corev1.Pod, string)) {
	for _, envFrom := range c.EnvFrom {
		if envFrom.SecretRef != nil {
			add(pod, envFrom.SecretRef.Name)
		}
	}
	for _, env := range c.Env {
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			add(pod, env.ValueFrom.SecretKeyRef.Name)
		}
	}
}

func authSecretNames(providerSpec map[string]any) []string {
	seen := map[string]struct{}{}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if name, ok := x["name"].(string); ok && name != "" && looksLikeSecretRef(x) {
				if _, dup := seen[name]; !dup {
					seen[name] = struct{}{}
					out = append(out, name)
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(providerSpec)
	return out
}

func looksLikeSecretRef(m map[string]any) bool {
	for key := range m {
		k := strings.ToLower(key)
		if k == "key" || k == "secretkey" || k == "secretref" || strings.Contains(k, "secret") {
			return true
		}
	}
	return false
}

func esoStatusHealth(status string) Health {
	switch status {
	case "Synced":
		return HealthHealthy
	case "SyncFailed":
		return HealthFailing
	case "Refreshing", "Stale", "Drifted":
		return HealthDegraded
	default:
		return HealthUnknown
	}
}

func compactSummary(left, right string) string {
	left = strings.Trim(left, "/")
	right = strings.TrimSpace(right)
	switch {
	case left == "" && right == "":
		return ""
	case left == "":
		return right
	case right == "":
		return left
	default:
		return fmt.Sprintf("%s, %s", left, right)
	}
}
