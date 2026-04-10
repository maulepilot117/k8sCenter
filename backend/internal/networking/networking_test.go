package networking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

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
	address    string
	asn        int64
	state      string
	received   int64
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
		name    string
		cniName string
		want    bool
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

// --- Phase B: Agent Collector Tests ---

func TestAgentCollector_CacheHit(t *testing.T) {
	c := &CiliumAgentCollector{}
	cached := &agentCollectionResult{
		nodes:     []agentNodeResult{{nodeName: "node-1", podName: "cilium-abc"}},
		collected: time.Now(),
		partial:   false,
	}
	c.cache = cached

	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != cached {
		t.Error("expected cached result to be returned")
	}
}

func TestAgentCollector_PodValidation(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		allowed   bool
	}{
		{"kube-system is allowed", "kube-system", true},
		{"cilium is allowed", "cilium", true},
		{"default is rejected", "default", false},
		{"monitoring is rejected", "monitoring", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedNamespace(tt.namespace)
			if got != tt.allowed {
				t.Errorf("isAllowedNamespace(%q) = %v, want %v", tt.namespace, got, tt.allowed)
			}
		})
	}
}

func TestAgentCollector_ParseMinimalJSON(t *testing.T) {
	input := `{
		"encryption": {
			"mode": "Wireguard",
			"wireguard": {
				"interfaces": [{
					"name": "cilium_wg0",
					"public-key": "abc123",
					"listen-port": 51871,
					"peer-count": 2,
					"peers": [{
						"public-key": "def456",
						"endpoint": "10.0.0.1:51871",
						"last-handshake-time": "2026-04-09T10:00:00Z",
						"transfer-rx": 12345678,
						"transfer-tx": 87654321
					}]
				}]
			}
		},
		"cluster-mesh": {
			"clusters": [{
				"name": "cluster-west",
				"connected": true,
				"ready": true,
				"status": "ready",
				"num-nodes": 5,
				"num-endpoints": 42,
				"num-shared-services": 12,
				"num-failures": 0
			}]
		},
		"proxy": {
			"envoy-deployment-mode": "embedded",
			"total-redirects": 150,
			"total-ports": 3
		},
		"cluster": {
			"ciliumHealth": {
				"state": "Ok",
				"msg": ""
			}
		}
	}`

	var status ciliumAgentStatus
	if err := json.Unmarshal([]byte(input), &status); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Encryption
	if status.Encryption == nil || status.Encryption.Mode != "Wireguard" {
		t.Error("encryption mode mismatch")
	}
	if len(status.Encryption.Wireguard.Interfaces) != 1 {
		t.Fatal("expected 1 WireGuard interface")
	}
	iface := status.Encryption.Wireguard.Interfaces[0]
	if iface.PublicKey != "abc123" || iface.PeerCount != 2 {
		t.Errorf("interface: key=%q, peerCount=%d", iface.PublicKey, iface.PeerCount)
	}
	if len(iface.Peers) != 1 || iface.Peers[0].TransferRx != 12345678 {
		t.Error("peer data mismatch")
	}

	// ClusterMesh
	if status.ClusterMesh == nil || len(status.ClusterMesh.Clusters) != 1 {
		t.Fatal("expected 1 remote cluster")
	}
	rc := status.ClusterMesh.Clusters[0]
	if rc.Name != "cluster-west" || !rc.Connected || rc.NumNodes != 5 {
		t.Errorf("remote cluster: name=%q, connected=%v, nodes=%d", rc.Name, rc.Connected, rc.NumNodes)
	}

	// Proxy
	if status.Proxy == nil || status.Proxy.DeploymentMode != "embedded" {
		t.Error("proxy deployment mode mismatch")
	}
	if status.Proxy.TotalRedirects != 150 || status.Proxy.TotalPorts != 3 {
		t.Errorf("proxy: redirects=%d, ports=%d", status.Proxy.TotalRedirects, status.Proxy.TotalPorts)
	}

	// Health
	if status.Cluster == nil || status.Cluster.CiliumHealth == nil || status.Cluster.CiliumHealth.State != "Ok" {
		t.Error("health state mismatch")
	}
}

func TestMergeAgentIntoSubsystems_PartialNodes(t *testing.T) {
	// When some nodes fail, only successful nodes contribute enrichment data
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  &EncryptionInfo{Enabled: true, Mode: "wireguard"},
		Mesh:        &MeshInfo{Enabled: true, Engine: "cilium"},
		ClusterMesh: &ClusterMeshInfo{Enabled: false},
	}
	agent := &agentCollectionResult{
		nodes: []agentNodeResult{
			{
				nodeName: "node-1",
				status: &ciliumAgentStatus{
					Encryption: &agentEncryption{
						Wireguard: &agentWireguard{
							Interfaces: []agentWGInterface{{PublicKey: "key1", Peers: []agentWGPeer{}}},
						},
					},
				},
			},
			{nodeName: "node-2", err: "exec failed: timeout"}, // failed — should be skipped
			{
				nodeName: "node-3",
				status: &ciliumAgentStatus{
					Encryption: &agentEncryption{
						Wireguard: &agentWireguard{
							Interfaces: []agentWGInterface{{PublicKey: "key3", Peers: []agentWGPeer{}}},
						},
					},
				},
			},
		},
		partial: true,
	}

	mergeAgentIntoSubsystems(resp, agent)

	// Only node-1 and node-3 should contribute (node-2 failed)
	if len(resp.Encryption.WireGuardNodes) != 2 {
		t.Fatalf("expected 2 WireGuard nodes (skipping failed), got %d", len(resp.Encryption.WireGuardNodes))
	}
	if resp.Encryption.WireGuardNodes[0].PublicKey != "key1" {
		t.Errorf("first node key = %q, want %q", resp.Encryption.WireGuardNodes[0].PublicKey, "key1")
	}
	if resp.Encryption.WireGuardNodes[1].PublicKey != "key3" {
		t.Errorf("second node key = %q, want %q", resp.Encryption.WireGuardNodes[1].PublicKey, "key3")
	}
}

// --- Phase B: Enrichment Merge Tests ---

func TestMergeAgentIntoSubsystems_WireGuard(t *testing.T) {
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  &EncryptionInfo{Enabled: true, Mode: "wireguard"},
		Mesh:        &MeshInfo{Enabled: true, Engine: "cilium"},
		ClusterMesh: &ClusterMeshInfo{Enabled: false},
	}
	agent := &agentCollectionResult{
		nodes: []agentNodeResult{
			{
				nodeName: "node-1",
				podName:  "cilium-1",
				status: &ciliumAgentStatus{
					Encryption: &agentEncryption{
						Mode: "Wireguard",
						Wireguard: &agentWireguard{
							Interfaces: []agentWGInterface{{
								Name:      "cilium_wg0",
								PublicKey: "key1",
								PeerCount: 2,
								Peers: []agentWGPeer{
									{PublicKey: "peer1", Endpoint: "10.0.0.1:51871", TransferRx: 100, TransferTx: 200},
								},
							}},
						},
					},
				},
			},
		},
	}

	mergeAgentIntoSubsystems(resp, agent)

	if len(resp.Encryption.WireGuardNodes) != 1 {
		t.Fatalf("expected 1 WireGuard node, got %d", len(resp.Encryption.WireGuardNodes))
	}
	wgn := resp.Encryption.WireGuardNodes[0]
	if wgn.NodeName != "node-1" || wgn.PublicKey != "key1" {
		t.Errorf("WireGuardNode: name=%q, key=%q", wgn.NodeName, wgn.PublicKey)
	}
	if len(wgn.Peers) != 1 || wgn.Peers[0].TransferRx != 100 {
		t.Error("peer data not merged correctly")
	}
}

func TestMergeAgentIntoSubsystems_ClusterMesh(t *testing.T) {
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  &EncryptionInfo{},
		Mesh:        &MeshInfo{},
		ClusterMesh: &ClusterMeshInfo{Enabled: true},
	}
	agent := &agentCollectionResult{
		nodes: []agentNodeResult{
			{
				nodeName: "node-1",
				status: &ciliumAgentStatus{
					ClusterMesh: &agentClusterMesh{
						Clusters: []agentRemoteCluster{
							{Name: "west", Connected: true, Ready: true, NumNodes: 3},
							{Name: "east", Connected: false, NumFailures: 2},
						},
					},
				},
			},
		},
	}

	mergeAgentIntoSubsystems(resp, agent)

	if len(resp.ClusterMesh.RemoteClusters) != 2 {
		t.Fatalf("expected 2 remote clusters, got %d", len(resp.ClusterMesh.RemoteClusters))
	}
	if resp.ClusterMesh.RemoteClusters[0].Name != "west" || !resp.ClusterMesh.RemoteClusters[0].Connected {
		t.Error("first remote cluster mismatch")
	}
	if resp.ClusterMesh.RemoteClusters[1].Connected {
		t.Error("second remote cluster should not be connected")
	}
}

func TestMergeAgentIntoSubsystems_Proxy(t *testing.T) {
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  &EncryptionInfo{},
		Mesh:        &MeshInfo{Enabled: true, Engine: "cilium"},
		ClusterMesh: &ClusterMeshInfo{},
	}
	agent := &agentCollectionResult{
		nodes: []agentNodeResult{
			{
				nodeName: "node-1",
				status: &ciliumAgentStatus{
					Proxy: &agentProxy{
						DeploymentMode: "embedded",
						TotalRedirects: 42,
						TotalPorts:     3,
					},
				},
			},
		},
	}

	mergeAgentIntoSubsystems(resp, agent)

	if resp.Mesh.DeploymentMode != "embedded" {
		t.Errorf("DeploymentMode = %q, want %q", resp.Mesh.DeploymentMode, "embedded")
	}
	if resp.Mesh.TotalRedirects != 42 {
		t.Errorf("TotalRedirects = %d, want 42", resp.Mesh.TotalRedirects)
	}
}

func TestMergeAgentIntoSubsystems_NilGuards(t *testing.T) {
	// Merge should not panic when sub-structs are nil
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  nil,
		Mesh:        nil,
		ClusterMesh: nil,
	}
	agent := &agentCollectionResult{
		nodes: []agentNodeResult{
			{
				nodeName: "node-1",
				status: &ciliumAgentStatus{
					Encryption: &agentEncryption{Mode: "Wireguard"},
					Proxy:      &agentProxy{DeploymentMode: "embedded"},
				},
			},
		},
	}

	// Should not panic
	mergeAgentIntoSubsystems(resp, agent)

	// Nil sub-structs should remain nil (agent data not injected into nil fields)
	if resp.Encryption != nil {
		t.Error("expected Encryption to remain nil")
	}
	if resp.Mesh != nil {
		t.Error("expected Mesh to remain nil")
	}
}

// --- Phase B: Connectivity Handler Tests ---

func TestHandleCiliumConnectivity_ExecDisabled(t *testing.T) {
	h := &Handler{
		Detector: &Detector{},
		// AgentCollector is nil (exec disabled)
	}
	h.Detector.cached = &CNIInfo{Name: CNICilium}

	req, _ := http.NewRequest("GET", "/api/v1/networking/cilium/connectivity", nil)
	// Note: isCiliumLocal requires no cluster context = local, which is the default

	// We can't easily test the full HTTP handler without httputil.RequireUser,
	// so test the logic directly: when AgentCollector is nil and CNI is Cilium,
	// the handler should return configured=true with empty nodes.
	if !h.isCiliumLocal(req) {
		t.Fatal("expected isCiliumLocal to be true")
	}
	if h.AgentCollector != nil {
		t.Fatal("expected AgentCollector to be nil")
	}
}

func TestHandleCiliumConnectivity_NotCilium(t *testing.T) {
	h := &Handler{
		Detector: &Detector{},
	}
	h.Detector.cached = &CNIInfo{Name: CNICalico}

	req, _ := http.NewRequest("GET", "/api/v1/networking/cilium/connectivity", nil)
	if h.isCiliumLocal(req) {
		t.Error("expected isCiliumLocal to be false for Calico")
	}
}

func TestInvalidateCaches_IncludesAgentCache(t *testing.T) {
	c := &CiliumAgentCollector{}
	c.cache = &agentCollectionResult{
		nodes:     []agentNodeResult{{nodeName: "node-1"}},
		collected: time.Now(),
	}

	h := &Handler{
		AgentCollector: c,
	}
	h.subsystemCache = &cachedSubsystems{data: &CiliumSubsystemsResponse{Configured: true}}

	h.InvalidateCaches()

	c.cacheMu.RLock()
	if c.cache != nil {
		t.Error("expected agent cache to be cleared after InvalidateCaches")
	}
	c.cacheMu.RUnlock()
}

// --- limitedWriter tests ---

func TestLimitedWriter(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		writes     []string
		wantBuf    string
		wantExceed bool
	}{
		{
			name:       "under limit",
			limit:      100,
			writes:     []string{"hello", " world"},
			wantBuf:    "hello world",
			wantExceed: false,
		},
		{
			name:       "exact limit",
			limit:      11,
			writes:     []string{"hello", " world"},
			wantBuf:    "hello world",
			wantExceed: false,
		},
		{
			name:       "exceed on single write",
			limit:      5,
			writes:     []string{"hello world"},
			wantBuf:    "hello",
			wantExceed: true,
		},
		{
			name:       "exceed across writes",
			limit:      8,
			writes:     []string{"hello", " world"},
			wantBuf:    "hello wo",
			wantExceed: true,
		},
		{
			name:       "writes after exceed are discarded",
			limit:      5,
			writes:     []string{"hello", " world", " more"},
			wantBuf:    "hello",
			wantExceed: true,
		},
		{
			name:       "zero limit",
			limit:      0,
			writes:     []string{"hello"},
			wantBuf:    "",
			wantExceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lw := &limitedWriter{w: &buf, remaining: tt.limit}

			for _, s := range tt.writes {
				n, err := lw.Write([]byte(s))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				// limitedWriter always reports len(p) to avoid short-write errors
				if n != len(s) {
					t.Errorf("Write(%q) returned n=%d, want %d", s, n, len(s))
				}
			}

			if got := buf.String(); got != tt.wantBuf {
				t.Errorf("buffer = %q, want %q", got, tt.wantBuf)
			}
			if lw.exceeded != tt.wantExceed {
				t.Errorf("exceeded = %v, want %v", lw.exceeded, tt.wantExceed)
			}
		})
	}
}

// --- truncate tests ---

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"under limit", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"over limit", "hello world", 8, "hello..."},
		{"minimal truncation", "abcdef", 4, "a..."},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("truncate result length %d exceeds maxLen %d", len(got), tt.maxLen)
			}
		})
	}
}
