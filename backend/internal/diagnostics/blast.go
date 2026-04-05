package diagnostics

import (
	"fmt"

	"github.com/kubecenter/kubecenter/internal/topology"
)

// BlastResult contains the resources affected if a target resource fails.
type BlastResult struct {
	DirectlyAffected    []AffectedResource `json:"directlyAffected"`
	PotentiallyAffected []AffectedResource `json:"potentiallyAffected"`
}

// AffectedResource describes a resource impacted by a failure.
type AffectedResource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Health string `json:"health"`
	Impact string `json:"impact"`
}

// ComputeBlastRadius performs BFS downstream (children) and upstream (parents)
// from the target node to determine which resources would be affected by its failure.
func ComputeBlastRadius(graph *topology.Graph, targetID string) *BlastResult {
	result := &BlastResult{
		DirectlyAffected:    []AffectedResource{},
		PotentiallyAffected: []AffectedResource{},
	}

	if graph == nil || targetID == "" {
		return result
	}

	// Build adjacency maps
	children := make(map[string][]edgeInfo)  // source -> targets (downstream)
	parents := make(map[string][]edgeInfo)    // target -> sources (upstream)
	for _, e := range graph.Edges {
		children[e.Source] = append(children[e.Source], edgeInfo{nodeID: e.Target, edgeType: e.Type})
		parents[e.Target] = append(parents[e.Target], edgeInfo{nodeID: e.Source, edgeType: e.Type})
	}

	// Build node lookup
	nodeMap := make(map[string]*topology.Node, len(graph.Nodes))
	for i := range graph.Nodes {
		nodeMap[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	// BFS downstream (children) — directly affected resources
	visited := map[string]bool{targetID: true}
	queue := []string{targetID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range children[current] {
			if visited[edge.nodeID] {
				continue
			}
			visited[edge.nodeID] = true
			queue = append(queue, edge.nodeID)
			if node, ok := nodeMap[edge.nodeID]; ok {
				result.DirectlyAffected = append(result.DirectlyAffected, AffectedResource{
					Kind:   node.Kind,
					Name:   node.Name,
					Health: string(node.Health),
					Impact: impactDescription(edge.edgeType, "downstream"),
				})
			}
		}
	}

	// BFS upstream (parents) — potentially affected resources
	visited = map[string]bool{targetID: true}
	queue = []string{targetID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range parents[current] {
			if visited[edge.nodeID] {
				continue
			}
			visited[edge.nodeID] = true
			queue = append(queue, edge.nodeID)
			if node, ok := nodeMap[edge.nodeID]; ok {
				result.PotentiallyAffected = append(result.PotentiallyAffected, AffectedResource{
					Kind:   node.Kind,
					Name:   node.Name,
					Health: string(node.Health),
					Impact: impactDescription(edge.edgeType, "upstream"),
				})
			}
		}
	}

	return result
}

// edgeInfo holds a neighbor node ID and the edge type connecting to it.
type edgeInfo struct {
	nodeID   string
	edgeType topology.EdgeType
}

// impactDescription returns a human-readable impact string based on edge type and direction.
func impactDescription(edgeType topology.EdgeType, direction string) string {
	switch edgeType {
	case topology.EdgeOwner:
		if direction == "downstream" {
			return "Owned resource — will fail with parent"
		}
		return "Owner — may be degraded by child failure"
	case topology.EdgeSelector:
		if direction == "downstream" {
			return "Selected resource — traffic may be affected"
		}
		return "Selector source — routing depends on this resource"
	case topology.EdgeMount:
		if direction == "downstream" {
			return "Mounted volume — data dependency"
		}
		return "Mount consumer — depends on this volume"
	case topology.EdgeIngress:
		if direction == "downstream" {
			return "Backend service — ingress traffic affected"
		}
		return "Ingress — external traffic may be affected"
	default:
		return fmt.Sprintf("Related (%s)", direction)
	}
}
