package diagnostics

import (
	"testing"

	"github.com/kubecenter/kubecenter/internal/topology"
)

func TestComputeBlastRadius_SkipsIdleReplicaSets(t *testing.T) {
	graph := &topology.Graph{
		Nodes: []topology.Node{
			{ID: "deploy", Kind: "Deployment", Name: "web"},
			{ID: "rs-new", Kind: "ReplicaSet", Name: "web-new"},
			{ID: "rs-old", Kind: "ReplicaSet", Name: "web-old", Idle: true},
			{ID: "pod", Kind: "Pod", Name: "web-new-abc"},
			{ID: "stale-pod", Kind: "Pod", Name: "web-old-xyz"},
		},
		Edges: []topology.Edge{
			{Source: "deploy", Target: "rs-new", Type: topology.EdgeOwner},
			{Source: "deploy", Target: "rs-old", Type: topology.EdgeOwner},
			{Source: "rs-new", Target: "pod", Type: topology.EdgeOwner},
			{Source: "rs-old", Target: "stale-pod", Type: topology.EdgeOwner},
		},
	}

	got := ComputeBlastRadius(graph, "deploy")

	names := map[string]bool{}
	for _, a := range got.DirectlyAffected {
		names[a.Name] = true
	}
	if len(got.DirectlyAffected) != 2 || !names["web-new"] || !names["web-new-abc"] {
		t.Fatalf("DirectlyAffected = %+v, want web-new and web-new-abc only", got.DirectlyAffected)
	}
}
