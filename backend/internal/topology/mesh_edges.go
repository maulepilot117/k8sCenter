package topology

import (
	"strings"

	"github.com/kubecenter/kubecenter/internal/servicemesh"
)

// MeshEdgeStats reports outcomes from a buildMeshEdges call so the caller
// can surface diagnostic information ("we considered N routes, M sources
// didn't resolve, K destinations didn't resolve") rather than silently
// dropping unresolved hosts.
type MeshEdgeStats struct {
	Truncated         bool
	Considered        int
	UnresolvedSources int
	UnresolvedDests   int
}

// buildMeshEdges converts mesh routing CRDs into service-to-service edges
// against an existing nameIndex of "Kind/Name" -> UID. The function is
// pure: no I/O, no logging, no RBAC. Callers are responsible for filtering
// routes they're not allowed to surface.
//
// Routes outside the requested namespace are skipped. The source service
// of each edge is resolved by walking r.Hosts in order and taking the
// first host that resolves to a Service node in the index. Targets come
// from r.Destinations[].Host. Hosts that don't resolve (cross-namespace
// targets, external hosts, custom cluster-domain FQDNs) are counted in
// MeshEdgeStats so a caller can surface "N routes had unresolved hosts"
// instead of leaving a silent empty graph.
//
// Edges are deduplicated by (source, target, type) so multiple VS objects
// that converge on the same source/target pair only emit one edge. Self
// edges (source == target) are emitted: Istio canary patterns route a
// single host to subsets of the same Service via DestinationRule weights.
// Emission stops at maxEdges with stats.Truncated=true.
func buildMeshEdges(
	routes []servicemesh.TrafficRoute,
	namespace string,
	nameIndex map[string]string,
	maxEdges int,
) (edges []Edge, stats MeshEdgeStats) {
	if len(routes) == 0 || namespace == "" || nameIndex == nil {
		return nil, stats
	}

	seen := make(map[string]struct{})
	add := func(source, target string, t EdgeType) bool {
		if source == "" || target == "" {
			return false
		}
		key := source + "->" + target + "/" + string(t)
		if _, dup := seen[key]; dup {
			return false
		}
		if maxEdges > 0 && len(edges) >= maxEdges {
			return true // signal cap reached
		}
		seen[key] = struct{}{}
		edges = append(edges, Edge{Source: source, Target: target, Type: t})
		return false
	}

	for _, r := range routes {
		if r.Namespace != namespace {
			continue
		}

		var edgeType EdgeType
		switch r.Mesh {
		case servicemesh.MeshIstio:
			// DestinationRule and Gateway don't fit the source->target
			// shape in D1; only VirtualService routes emit mesh_vs edges.
			if r.Kind != "VirtualService" {
				continue
			}
			edgeType = EdgeMeshVS
		case servicemesh.MeshLinkerd:
			// Only ServiceProfile carries route.destinations in v1.
			if r.Kind != "ServiceProfile" {
				continue
			}
			edgeType = EdgeMeshSP
		default:
			continue
		}

		stats.Considered++

		var sourceUID string
		for _, host := range r.Hosts {
			if uid, ok := resolveServiceHost(host, namespace, nameIndex); ok {
				sourceUID = uid
				break
			}
		}
		if sourceUID == "" {
			stats.UnresolvedSources++
			continue
		}

		for _, dest := range r.Destinations {
			targetUID, ok := resolveServiceHost(dest.Host, namespace, nameIndex)
			if !ok {
				stats.UnresolvedDests++
				continue
			}
			if capped := add(sourceUID, targetUID, edgeType); capped {
				stats.Truncated = true
				return edges, stats
			}
		}
	}

	return edges, stats
}

// resolveServiceHost looks up a mesh route's host string against the
// topology nameIndex. It accepts the three common Kubernetes service-
// host shapes:
//
//	bare name:        my-svc
//	namespaced:       my-svc.foo
//	fully qualified:  my-svc.foo.svc.<cluster-domain>
//
// Lookup is case-insensitive: DNS hostnames are case-insensitive and
// Istio VS hosts are user-supplied free-form text, but Kubernetes
// Service names are RFC 1123 lowercase. We lowercase the host first so
// an operator who types "MyService.foo" in a VirtualService still
// resolves to the corresponding Service.
//
// Custom cluster domains are supported transparently. Rather than
// matching the literal "cluster.local" tail (which fails for clusters
// using --cluster-domain=k8s.example.com or similar), we split at the
// first ".svc." separator and take everything before it. This works for
// any cluster domain because the ".svc." separator is canonical in the
// Kubernetes service-FQDN format regardless of the trailing domain.
//
// Only hosts that resolve to a Service node in the requested namespace
// match. Cross-namespace hosts (my-svc.bar from inside namespace foo)
// and external hosts (api.example.com) return ok=false.
func resolveServiceHost(host, namespace string, nameIndex map[string]string) (string, bool) {
	if host == "" || namespace == "" {
		return "", false
	}

	// Lowercase before any matching so case differences in user-supplied
	// VS hosts don't silently drop edges.
	bare := strings.ToLower(host)
	// Strip the cluster-domain suffix by splitting at ".svc." (the
	// canonical k8s service-FQDN separator). "name.namespace.svc.<any>"
	// becomes "name.namespace" without depending on the cluster domain
	// being "cluster.local". Plain ".svc" with nothing after it is
	// handled by the trailing CutSuffix.
	if idx := strings.Index(bare, ".svc."); idx >= 0 {
		bare = bare[:idx]
	} else if trimmed, ok := strings.CutSuffix(bare, ".svc"); ok {
		bare = trimmed
	}

	// At this point the candidate is either "name" or "name.namespace".
	parts := strings.Split(bare, ".")
	switch len(parts) {
	case 1:
		// Bare name — same namespace assumed.
		if uid, ok := nameIndex["Service/"+parts[0]]; ok {
			return uid, true
		}
	case 2:
		// name.namespace — only resolve when namespace matches.
		if parts[1] != namespace {
			return "", false
		}
		if uid, ok := nameIndex["Service/"+parts[0]]; ok {
			return uid, true
		}
	}
	return "", false
}
