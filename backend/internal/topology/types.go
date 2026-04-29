package topology

import "time"

// Graph is the resource dependency graph for a namespace.
//
// Overlay is empty (and JSON-omitted) by default, preserving byte-identical
// responses for callers that don't pass ?overlay=. Set to "mesh" when the
// caller requested ?overlay=mesh and a mesh route provider is wired, even
// if no mesh edges are emitted (e.g., RBAC denies every mesh CRD or no
// mesh is installed). "unavailable" means the caller asked for the overlay
// but the provider was nil or returned an error — base graph is returned
// rather than a 5xx.
type Graph struct {
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
	Truncated  bool   `json:"truncated,omitempty"`
	Overlay    string `json:"overlay,omitempty"`
	ComputedAt string `json:"computedAt"`
}

// Node represents a Kubernetes resource in the graph.
type Node struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Health    Health `json:"health"`
	Summary   string `json:"summary"`
}

// Edge represents a relationship between two resources.
type Edge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Type   EdgeType `json:"type"`
}

// Health indicates the health state of a resource.
type Health string

const (
	HealthHealthy  Health = "healthy"
	HealthDegraded Health = "degraded"
	HealthFailing  Health = "failing"
	HealthUnknown  Health = "unknown"
)

// EdgeType classifies the relationship between resources.
type EdgeType string

const (
	EdgeOwner    EdgeType = "owner"
	EdgeSelector EdgeType = "selector"
	EdgeMount    EdgeType = "mount"
	EdgeIngress  EdgeType = "ingress"
	// Mesh-overlay edge types. EdgeMeshVS connects an Istio VirtualService's
	// host service to each destination service it routes to. EdgeMeshSP is
	// the Linkerd ServiceProfile equivalent. Both are emitted only when the
	// caller passes ?overlay=mesh AND has list permission on the underlying
	// CRD group.
	EdgeMeshVS EdgeType = "mesh_vs"
	EdgeMeshSP EdgeType = "mesh_sp"
)

// NewGraph creates a new empty graph with the current timestamp.
func NewGraph() *Graph {
	return &Graph{
		Nodes:      []Node{},
		Edges:      []Edge{},
		ComputedAt: time.Now().UTC().Format(time.RFC3339),
	}
}
