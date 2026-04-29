package topology

import (
	"strings"

	"github.com/kubecenter/kubecenter/internal/servicemesh"
)

// buildMeshEdges converts mesh routing CRDs into service-to-service edges
// against an existing nameIndex of "Kind/Name" -> UID. The function is pure:
// no I/O, no logging, no RBAC. Callers are responsible for filtering routes
// they're not allowed to surface.
//
// Routes outside the requested namespace are skipped. The source service of
// each edge is resolved from route.Hosts[0]; targets come from
// route.Destinations[].Host. Hosts that don't resolve to a Service node in
// the index (typically external hosts or cross-namespace targets) are
// silently dropped — that's a feature, not a bug.
//
// Edges are deduplicated by (source, target, type) so multiple VS objects
// that converge on the same source/target pair only emit one edge. Emission
// stops at maxEdges with truncated=true; reuse the existing
// Graph.Truncated flag at the call site.
func buildMeshEdges(
	routes []servicemesh.TrafficRoute,
	namespace string,
	nameIndex map[string]string,
	maxEdges int,
) (edges []Edge, truncated bool) {
	if len(routes) == 0 || namespace == "" || nameIndex == nil {
		return nil, false
	}

	seen := make(map[string]struct{})
	add := func(source, target string, t EdgeType) bool {
		if source == "" || target == "" || source == target {
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
			// DestinationRule and Gateway don't fit the source->target shape
			// in D1; only VirtualService routes emit mesh_vs edges.
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

		var sourceUID string
		for _, host := range r.Hosts {
			if uid, ok := resolveServiceHost(host, namespace, nameIndex); ok {
				sourceUID = uid
				break
			}
		}
		if sourceUID == "" {
			continue
		}

		for _, dest := range r.Destinations {
			targetUID, ok := resolveServiceHost(dest.Host, namespace, nameIndex)
			if !ok {
				continue
			}
			if capped := add(sourceUID, targetUID, edgeType); capped {
				return edges, true
			}
		}
	}

	return edges, false
}

// resolveServiceHost looks up a mesh route's host string against the topology
// nameIndex. It accepts the three common Kubernetes service-host shapes:
//
//	bare name:        my-svc
//	namespaced:       my-svc.foo
//	fully qualified:  my-svc.foo.svc.cluster.local
//
// Only hosts that resolve to a Service node in the requested namespace match.
// Cross-namespace hosts (my-svc.bar from inside namespace foo) and external
// hosts (api.example.com) return ok=false.
//
// Custom cluster domains (clusterDomain != "cluster.local") are a known
// limitation inherited from Phase B; these hosts won't resolve and the edges
// will be silently skipped.
func resolveServiceHost(host, namespace string, nameIndex map[string]string) (string, bool) {
	if host == "" || namespace == "" {
		return "", false
	}

	// Strip the FQDN suffix. We accept .svc.cluster.local and .svc; the
	// shorter forms still flow through the dot-split below.
	bare := host
	for _, suffix := range []string{".svc.cluster.local", ".svc"} {
		if trimmed, ok := strings.CutSuffix(bare, suffix); ok {
			bare = trimmed
			break
		}
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
