package topology

import "time"

// Graph is the resource dependency graph for a namespace.
type Graph struct {
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
	Truncated  bool   `json:"truncated,omitempty"`
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
)

// NewGraph creates a new empty graph with the current timestamp.
func NewGraph() *Graph {
	return &Graph{
		Nodes:      []Node{},
		Edges:      []Edge{},
		ComputedAt: time.Now().UTC().Format(time.RFC3339),
	}
}
