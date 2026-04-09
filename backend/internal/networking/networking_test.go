package networking

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
)

func TestExtractImageVersion(t *testing.T) {
	tests := []struct {
		name          string
		containers    []corev1.Container
		containerName string
		want          string
	}{
		{
			name: "versioned image with v prefix",
			containers: []corev1.Container{
				{Name: "cilium-agent", Image: "quay.io/cilium/cilium:v1.15.3"},
			},
			containerName: "cilium-agent",
			want:          "1.15.3",
		},
		{
			name: "versioned image without v prefix",
			containers: []corev1.Container{
				{Name: "calico-node", Image: "docker.io/calico/node:3.27.0"},
			},
			containerName: "calico-node",
			want:          "3.27.0",
		},
		{
			name: "no tag",
			containers: []corev1.Container{
				{Name: "cilium-agent", Image: "quay.io/cilium/cilium"},
			},
			containerName: "cilium-agent",
			want:          "",
		},
		{
			name: "wrong container name",
			containers: []corev1.Container{
				{Name: "other", Image: "nginx:1.25"},
			},
			containerName: "cilium-agent",
			want:          "",
		},
		{
			name: "empty container name matches first",
			containers: []corev1.Container{
				{Name: "agent", Image: "cilium:v1.14.0"},
			},
			containerName: "",
			want:          "1.14.0",
		},
		{
			name:          "empty containers",
			containers:    nil,
			containerName: "cilium-agent",
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractImageVersion(tt.containers, tt.containerName)
			if got != tt.want {
				t.Errorf("extractImageVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCachedInfo_InitiallyNil(t *testing.T) {
	d := &Detector{}
	if d.CachedInfo() != nil {
		t.Error("expected CachedInfo to be nil initially")
	}
}

func TestCachedInfo_ReturnsCopy(t *testing.T) {
	d := &Detector{}
	d.cached = &CNIInfo{Name: CNICilium, Version: "1.0"}

	info1 := d.CachedInfo()
	info2 := d.CachedInfo()

	if info1 == info2 {
		t.Error("expected CachedInfo to return different pointers (copies)")
	}
	if info1.Name != CNICilium || info1.Version != "1.0" {
		t.Error("expected cached values to match")
	}
}

func TestDetectMesh_CiliumEnvoyEnabled(t *testing.T) {
	d := &Detector{}
	d.cached = &CNIInfo{
		Name:     CNICilium,
		Features: CNIFeatures{EnvoyEnabled: true},
	}
	if got := d.DetectMesh(); got != "cilium" {
		t.Errorf("DetectMesh() = %q, want %q", got, "cilium")
	}
}

func TestDetectMesh_NoMesh(t *testing.T) {
	d := &Detector{}
	d.cached = &CNIInfo{
		Name:     CNICilium,
		Features: CNIFeatures{EnvoyEnabled: false},
	}
	if got := d.DetectMesh(); got != "none" {
		t.Errorf("DetectMesh() = %q, want %q", got, "none")
	}
}

func TestDetectMesh_NilCache(t *testing.T) {
	d := &Detector{}
	if got := d.DetectMesh(); got != "none" {
		t.Errorf("DetectMesh() = %q, want %q", got, "none")
	}
}

func TestComputeExhaustionRisk(t *testing.T) {
	tests := []struct {
		name      string
		allocated int
		total     int
		want      string
	}{
		{"zero total", 0, 0, "none"},
		{"low usage", 10, 100, "none"},
		{"74 percent", 74, 100, "none"},
		{"75 percent exactly", 75, 100, "none"},
		{"76 percent", 76, 100, "medium"},
		{"85 percent", 85, 100, "medium"},
		{"90 percent", 90, 100, "medium"},
		{"91 percent", 91, 100, "high"},
		{"full", 100, 100, "high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeExhaustionRisk(tt.allocated, tt.total)
			if got != tt.want {
				t.Errorf("computeExhaustionRisk(%d, %d) = %q, want %q", tt.allocated, tt.total, got, tt.want)
			}
		})
	}
}

func TestAggregateEndpoints(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClient(scheme,
		makeEndpoint("ep-1", "default", "ready"),
		makeEndpoint("ep-2", "default", "ready"),
		makeEndpoint("ep-3", "kube-system", "not-ready"),
		makeEndpoint("ep-4", "default", "disconnecting"),
		makeEndpoint("ep-5", "default", "waiting-for-identity"),
	)

	counts, err := aggregateEndpoints(context.Background(), dynClient)
	if err != nil {
		t.Fatalf("aggregateEndpoints() error: %v", err)
	}

	if counts.Total != 5 {
		t.Errorf("Total = %d, want 5", counts.Total)
	}
	if counts.Ready != 2 {
		t.Errorf("Ready = %d, want 2", counts.Ready)
	}
	if counts.NotReady != 1 {
		t.Errorf("NotReady = %d, want 1", counts.NotReady)
	}
	if counts.Disconnecting != 1 {
		t.Errorf("Disconnecting = %d, want 1", counts.Disconnecting)
	}
	if counts.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1", counts.Waiting)
	}
}

func makeEndpoint(name, namespace, state string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumEndpoint",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"status": map[string]interface{}{
				"state": state,
			},
		},
	}
}

func TestReadBGPNodeConfigs(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClient(scheme,
		makeBGPNodeConfig("node-1", 64512, []bgpPeerData{
			{address: "10.0.0.1", asn: 65001, state: "established", received: 5, advertised: 3},
			{address: "10.0.0.2", asn: 65001, state: "active", received: 0, advertised: 0},
		}),
	)

	peers, err := readBGPNodeConfigs(context.Background(), dynClient)
	if err != nil {
		t.Fatalf("readBGPNodeConfigs() error: %v", err)
	}

	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}

	if peers[0].Node != "node-1" {
		t.Errorf("peer[0].Node = %q, want %q", peers[0].Node, "node-1")
	}
	if peers[0].SessionState != "established" {
		t.Errorf("peer[0].SessionState = %q, want %q", peers[0].SessionState, "established")
	}
	if peers[0].RoutesReceived != 5 {
		t.Errorf("peer[0].RoutesReceived = %d, want 5", peers[0].RoutesReceived)
	}
	if peers[1].SessionState != "active" {
		t.Errorf("peer[1].SessionState = %q, want %q", peers[1].SessionState, "active")
	}
}

type bgpPeerData struct {
	address  string
	asn      int64
	state    string
	received int64
	advertised int64
}

func makeBGPNodeConfig(name string, localASN int64, peers []bgpPeerData) *unstructured.Unstructured {
	var peerSlice []interface{}
	for _, p := range peers {
		peerSlice = append(peerSlice, map[string]interface{}{
			"peerAddress":  p.address,
			"peerASN":      p.asn,
			"peeringState": p.state,
			"routeCount": []interface{}{
				map[string]interface{}{
					"afi":        "ipv4",
					"safi":       "unicast",
					"received":   p.received,
					"advertised": p.advertised,
				},
			},
		})
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2alpha1",
			"kind":       "CiliumBGPNodeConfig",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"status": map[string]interface{}{
				"bgpInstances": []interface{}{
					map[string]interface{}{
						"name":     "default",
						"localASN": localASN,
						"peers":    peerSlice,
					},
				},
			},
		},
	}
}

func TestReadCiliumNodes_IPAM(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClient(scheme,
		makeCiliumNode("node-1", []string{"10.244.0.0/24"}, 256, 64, 1),
		makeCiliumNode("node-2", []string{"10.244.1.0/24"}, 256, 58, 0),
	)

	nodes, err := readCiliumNodes(context.Background(), dynClient)
	if err != nil {
		t.Fatalf("readCiliumNodes() error: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}

	if nodes[0].Name != "node-1" {
		t.Errorf("node[0].Name = %q, want %q", nodes[0].Name, "node-1")
	}
	if nodes[0].PoolCount != 256 {
		t.Errorf("node[0].PoolCount = %d, want 256", nodes[0].PoolCount)
	}
	if nodes[0].UsedCount != 64 {
		t.Errorf("node[0].UsedCount = %d, want 64", nodes[0].UsedCount)
	}
	if nodes[0].EncryptionKey != 1 {
		t.Errorf("node[0].EncryptionKey = %d, want 1", nodes[0].EncryptionKey)
	}
	if nodes[1].EncryptionKey != 0 {
		t.Errorf("node[1].EncryptionKey = %d, want 0", nodes[1].EncryptionKey)
	}
}

func makeCiliumNode(name string, podCIDRs []string, poolSize, usedSize int, encKey int64) *unstructured.Unstructured {
	pool := make(map[string]interface{})
	for i := 0; i < poolSize; i++ {
		pool[fmt.Sprintf("10.244.0.%d", i)] = ""
	}
	used := make(map[string]interface{})
	for i := 0; i < usedSize; i++ {
		used[fmt.Sprintf("10.244.0.%d", i)] = fmt.Sprintf("default/pod-%d", i)
	}

	cidrs := make([]interface{}, len(podCIDRs))
	for i, c := range podCIDRs {
		cidrs[i] = c
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNode",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{
				"ipam": map[string]interface{}{
					"podCIDRs": cidrs,
					"pool":     pool,
				},
				"encryption": map[string]interface{}{
					"key": encKey,
				},
			},
			"status": map[string]interface{}{
				"ipam": map[string]interface{}{
					"used": used,
				},
			},
		},
	}
}

func TestInvalidateCaches(t *testing.T) {
	h := &Handler{}
	h.subsystemCache = &cachedSubsystems{
		data: &CiliumSubsystemsResponse{Configured: true},
	}
	h.bgpCache = &cachedBGP{
		data: &CiliumBGPResponse{Configured: true},
	}
	h.ipamCache = &cachedIPAM{
		data: &CiliumIPAMResponse{Configured: true},
	}

	h.InvalidateCaches()

	h.subsystemMu.RLock()
	if h.subsystemCache != nil {
		t.Error("expected subsystem cache to be nil after invalidation")
	}
	h.subsystemMu.RUnlock()

	h.bgpMu.RLock()
	if h.bgpCache != nil {
		t.Error("expected bgp cache to be nil after invalidation")
	}
	h.bgpMu.RUnlock()

	h.ipamMu.RLock()
	if h.ipamCache != nil {
		t.Error("expected ipam cache to be nil after invalidation")
	}
	h.ipamMu.RUnlock()
}

// Ensure hasCRD works with a mock discovery that returns specific resources.
func TestHasCRD(t *testing.T) {
	// hasCRD returns false when discovery is nil
	if hasCRD(nil, schema.GroupVersionResource{Group: "cilium.io", Version: "v2alpha1", Resource: "ciliumbgpclusterconfigs"}) {
		t.Error("expected hasCRD to return false for nil discovery")
	}
}

// isCiliumLocal tests
func TestIsCiliumLocal(t *testing.T) {
	tests := []struct {
		name      string
		cniName   string
		want      bool
	}{
		{"cilium", CNICilium, true},
		{"calico", CNICalico, false},
		{"unknown", CNIUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				Detector: &Detector{},
			}
			h.Detector.cached = &CNIInfo{Name: tt.cniName}

			// Create a minimal request (no cluster context = local)
			req, _ := http.NewRequest("GET", "/test", nil)
			got := h.isCiliumLocal(req)
			if got != tt.want {
				t.Errorf("isCiliumLocal() = %v, want %v for CNI %q", got, tt.want, tt.cniName)
			}
		})
	}
}

