package topology

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func rsWith(uid string, spec *int32, status int32) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid), Name: uid},
		Spec:       appsv1.ReplicaSetSpec{Replicas: spec},
		Status:     appsv1.ReplicaSetStatus{Replicas: status},
	}
}

func TestMarkIdleReplicaSets(t *testing.T) {
	zero, two := int32(0), int32(2)
	sets := []*appsv1.ReplicaSet{
		rsWith("old", &zero, 0),      // old revision: idle
		rsWith("draining", &zero, 1), // scaled down, a pod still terminating
		rsWith("current", &two, 2),   // running
		rsWith("defaulted", nil, 0),  // unset replicas defaults to 1
	}
	graph := &Graph{}
	for _, rs := range sets {
		graph.Nodes = append(graph.Nodes, Node{ID: string(rs.UID), Kind: "ReplicaSet", Name: rs.Name})
	}
	graph.Nodes = append(graph.Nodes, Node{ID: "old", Kind: "Deployment"})

	markIdleReplicaSets(graph, sets)

	want := map[string]bool{"old": true, "draining": false, "current": false, "defaulted": false}
	for _, n := range graph.Nodes {
		if n.Kind != "ReplicaSet" {
			if n.Idle {
				t.Errorf("%s %s marked idle", n.Kind, n.ID)
			}
			continue
		}
		if n.Idle != want[n.ID] {
			t.Errorf("ReplicaSet %s: Idle = %v, want %v", n.ID, n.Idle, want[n.ID])
		}
	}
}
