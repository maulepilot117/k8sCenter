package topology

import "time"

// Graph is the resource dependency graph for a namespace.
//
// Overlay is OverlayNone (JSON-omitted) by default, preserving byte-identical
// responses for callers that don't pass ?overlay=. See Overlay docs for the
// other values. Truncated signals that some Nodes were dropped at the
// maxNodes cap; EdgesTruncated signals that some mesh-overlay edges were
// dropped at the maxMeshEdges cap. The two flags are independent so
// consumers (e.g. blast-radius BFS) can tell "graph missing nodes" from
// "graph complete, only some mesh edges capped". Errors carries any
// per-stage warnings the build accumulated (currently: mesh-overlay
// host-resolution drops); never holds raw Kubernetes error bodies.
type Graph struct {
	Nodes          []Node            `json:"nodes"`
	Edges          []Edge            `json:"edges"`
	Truncated      bool              `json:"truncated,omitempty"`
	EdgesTruncated bool              `json:"edgesTruncated,omitempty"`
	Overlay        Overlay           `json:"overlay,omitempty"`
	Errors         map[string]string `json:"errors,omitempty"`
	ComputedAt     string            `json:"computedAt"`
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

// Overlay names which optional layer of edges has been folded into the
// graph. Always returned as a typed value so consumers can discriminate
// without string literals.
//
//	OverlayNone        — no overlay requested (zero value, JSON-omitted)
//	OverlayMesh        — overlay requested AND a mesh is installed; edges
//	                     reflect the routes the user has CRD list permission
//	                     for (may be empty)
//	OverlayESOChain     — External Secrets Operator chain overlay requested
//	OverlayUnavailable  — overlay requested but couldn't be applied: provider
//	                     unwired, fetch errored, or no backing CRDs are installed
type Overlay string

const (
	OverlayNone        Overlay = ""
	OverlayMesh        Overlay = "mesh"
	OverlayESOChain    Overlay = "eso-chain"
	OverlayUnavailable Overlay = "unavailable"
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

	// ESO-chain overlay edge types. EdgeESOAuth connects an auth Secret to
	// the Store that references it. EdgeESOSync connects a Store to an
	// ExternalSecret and an ExternalSecret to its synced Secret. EdgeESOConsumer
	// connects a synced Secret to Pods that consume it.
	EdgeESOAuth     EdgeType = "eso_auth"
	EdgeESOSync     EdgeType = "eso_sync"
	EdgeESOConsumer EdgeType = "eso_consumer"
)

// NewGraph creates a new empty graph with the current timestamp.
func NewGraph() *Graph {
	return &Graph{
		Nodes:      []Node{},
		Edges:      []Edge{},
		ComputedAt: time.Now().UTC().Format(time.RFC3339),
	}
}
