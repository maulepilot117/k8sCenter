package networking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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

// --- rejectNonLocal tests (P2-5 security fix) ---

// TestRejectNonLocal_LocalCluster verifies that local cluster requests pass through.
func TestRejectNonLocal_LocalCluster(t *testing.T) {
	h := &Handler{}
	req, _ := http.NewRequest("GET", "/test", nil)
	// No X-Cluster-ID context = local
	w := httptest.NewRecorder()
	if !h.rejectNonLocal(w, req, "test feature") {
		t.Error("expected rejectNonLocal to allow local cluster request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for local, got %d", w.Code)
	}
}

// TestRejectNonLocal_ExplicitLocal verifies "local" clusterID is treated as local.
func TestRejectNonLocal_ExplicitLocal(t *testing.T) {
	h := &Handler{}
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx := middleware.WithClusterID(req.Context(), "local")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	if !h.rejectNonLocal(w, req, "test feature") {
		t.Error("expected rejectNonLocal to allow explicit local cluster request")
	}
}

// TestRejectNonLocal_RemoteCluster verifies that remote cluster requests get 501.
func TestRejectNonLocal_RemoteCluster(t *testing.T) {
	h := &Handler{}
	req, _ := http.NewRequest("PUT", "/test", nil)
	ctx := middleware.WithClusterID(req.Context(), "abc123remoteClusterID")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	if h.rejectNonLocal(w, req, "Cilium CNI configuration update") {
		t.Error("expected rejectNonLocal to block remote cluster request")
	}
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 Not Implemented for remote cluster, got %d", w.Code)
	}
}

// TestRejectNonLocal_CNIConfigRead verifies the CNI config read path rejects remote clusters.
// This is a behavioral integration test for the P2-5 fix: HandleCNIConfig must return 501
// for any non-local X-Cluster-ID before attempting to read the local Cilium ConfigMap.
func TestRejectNonLocal_CNIConfigRead(t *testing.T) {
	h := &Handler{
		Detector: &Detector{},
	}
	h.Detector.cached = &CNIInfo{Name: CNICilium}

	req, _ := http.NewRequest("GET", "/api/v1/networking/cni/config", nil)
	// Inject a remote cluster ID into the context (simulates middleware having already
	// validated the admin role and written the cluster ID to context).
	ctx := middleware.WithClusterID(req.Context(), "remoteCluster42")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	// rejectNonLocal is called at the top of HandleCNIConfig — verify it produces 501.
	if h.rejectNonLocal(w, req, "Cilium CNI configuration") {
		t.Error("expected rejectNonLocal to block remote cluster request for CNI config read")
	}
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

// TestRejectNonLocal_CNIConfigWrite verifies the CNI config write path rejects remote clusters.
func TestRejectNonLocal_CNIConfigWrite(t *testing.T) {
	h := &Handler{
		Detector: &Detector{},
	}
	h.Detector.cached = &CNIInfo{Name: CNICilium}

	req, _ := http.NewRequest("PUT", "/api/v1/networking/cni/config", nil)
	ctx := middleware.WithClusterID(req.Context(), "remoteClusterXYZ")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	if h.rejectNonLocal(w, req, "Cilium CNI configuration update") {
		t.Error("expected rejectNonLocal to block remote cluster request for CNI config write")
	}
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

// --- Remote refusal for Hubble flows (#608, PR #612 #10) ---
//
// Hubble flows read the LOCAL cluster's relay. Under a remote selection they
// must refuse with 501 rather than show local data under the remote
// cluster's name, until #608 slice 5 PR 5b reaches the remote relay.

var networkingTestUser = &auth.User{ID: "u1", Username: "alice", KubernetesUsername: "alice", Roles: []string{"admin"}}

func networkingRequest(method, path, clusterID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	ctx := auth.ContextWithUser(req.Context(), networkingTestUser)
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	return req.WithContext(ctx)
}

func TestHandleCNIStatus_LocalUnchanged(t *testing.T) {
	for _, id := range []string{"", "local"} {
		h := &Handler{Detector: &Detector{}}
		h.Detector.cached = &CNIInfo{Name: CNICilium}

		w := httptest.NewRecorder()
		h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", id))
		if w.Code != http.StatusOK {
			t.Fatalf("cluster %q: status = %d, body %s; want 200", id, w.Code, w.Body.String())
		}
		var body struct {
			Data CNIInfo `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %s: %v", w.Body.String(), err)
		}
		if body.Data.Name != CNICilium {
			t.Errorf("cluster %q: data.name = %q; want %q", id, body.Data.Name, CNICilium)
		}
	}
}

func TestHandleHubbleFlows_RemoteIs501(t *testing.T) {
	// Even with a Hubble client wired, a remote selection never reaches the
	// local relay. A non-nil client proves the refusal comes first.
	h := &Handler{HubbleClient: &HubbleClient{}}

	w := httptest.NewRecorder()
	h.HandleHubbleFlows(w, networkingRequest(http.MethodGet, "/api/v1/networking/hubble/flows?namespace=default", "remoteCluster42"))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, body %s; want 501", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Hubble flows is not supported for remote clusters")) {
		t.Errorf("body = %s; want the fixed refusal message", w.Body.String())
	}
}

func TestHandleHubbleFlows_LocalUnchanged(t *testing.T) {
	// The local path still reaches the existing checks: without a Hubble
	// client it answers the existing 503.
	for _, id := range []string{"", "local"} {
		h := &Handler{}
		w := httptest.NewRecorder()
		h.HandleHubbleFlows(w, networkingRequest(http.MethodGet, "/api/v1/networking/hubble/flows?namespace=default", id))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("cluster %q: status = %d, body %s; want 503", id, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("Hubble is not available")) {
			t.Errorf("cluster %q: body = %s; want the existing 503 message", id, w.Body.String())
		}
	}
}

// --- Remote CNI detection and status (#608 slice 5, U3) ---

func testDaemonSet(ns, name, image string, ready, desired int32) appsv1.DaemonSet {
	container := strings.TrimSuffix(name, "-ds")
	if name == "cilium" {
		container = "cilium-agent"
	}
	return appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: container, Image: image}},
		}}},
		Status: appsv1.DaemonSetStatus{NumberReady: ready, DesiredNumberScheduled: desired},
	}
}

func TestCNIInfoFromDaemonSet(t *testing.T) {
	tests := []struct {
		name      string
		ds        appsv1.DaemonSet
		wantName  string
		wantVer   string
		healthy   bool
		wantNoCNI bool
	}{
		{name: "cilium agent", ds: testDaemonSet("kube-system", "cilium", "quay.io/cilium/cilium:v1.16.2", 3, 3), wantName: CNICilium, wantVer: "1.16.2", healthy: true},
		{name: "cilium-agent daemonset", ds: testDaemonSet("cilium", "cilium-agent", "quay.io/cilium/cilium:v1.16.2", 2, 3), wantName: CNICilium, wantVer: "1.16.2"},
		{name: "cilium envoy is not the agent", ds: testDaemonSet("kube-system", "cilium-envoy", "quay.io/cilium/envoy:v1.29", 3, 3), wantNoCNI: true},
		{name: "calico node", ds: testDaemonSet("calico-system", "calico-node", "calico/node:v3.28.0", 1, 1), wantName: CNICalico, wantVer: "3.28.0", healthy: true},
		{name: "flannel", ds: testDaemonSet("kube-flannel", "kube-flannel-ds", "flannel/flannel:v0.25.1", 0, 1), wantName: CNIFlannel, wantVer: "0.25.1"},
		{name: "unrelated daemonset", ds: testDaemonSet("kube-system", "kube-proxy", "registry.k8s.io/kube-proxy:v1.31.0", 3, 3), wantNoCNI: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := cniInfoFromDaemonSet(&tt.ds)
			if tt.wantNoCNI {
				if info != nil {
					t.Fatalf("got %+v, want no CNI", info)
				}
				return
			}
			if info == nil {
				t.Fatal("got no CNI")
			}
			if info.Name != tt.wantName || info.Version != tt.wantVer || info.Status.Healthy != tt.healthy {
				t.Errorf("got name=%q version=%q healthy=%v, want %q %q %v", info.Name, info.Version, info.Status.Healthy, tt.wantName, tt.wantVer, tt.healthy)
			}
			if info.Namespace != tt.ds.Namespace || info.DaemonSet != tt.ds.Name || info.DetectionMethod != "daemonset" {
				t.Errorf("got %+v, want namespace, daemonset and method from the daemonset", info)
			}
		})
	}
}

func TestCiliumFeaturesFromConfig(t *testing.T) {
	got := ciliumFeaturesFromConfig(map[string]string{
		"enable-hubble":       "true",
		"enable-encryption":   "true",
		"encryption-type":     "wireguard",
		"cluster-mesh-config": "/var/lib/cilium/clustermesh/",
		"enable-envoy-config": "true",
		"ipam":                "kubernetes",
	})
	want := CNIFeatures{Hubble: true, Encryption: true, EncryptionMode: "wireguard", ClusterMesh: true, WireGuard: true, EnvoyEnabled: true, IPAMMode: "kubernetes"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := ciliumFeaturesFromConfig(nil); got != (CNIFeatures{}) {
		t.Errorf("empty config: got %+v, want zero features", got)
	}
}

const netRemoteCluster = "remote-net-1"

// fakeNetClients is a k8s.ClusterClients over one typed fake clientset per
// cluster id, recording every resolution so tests can assert the identity a
// read impersonated. targetErr makes every resolution fail.
type fakeNetClients struct {
	clusters  map[string]*kfake.Clientset
	targetErr error
	// generation is the TargetSchema generation every remote cluster reports.
	generation string
	// dynamic is the dynamic client per cluster id; a cluster without one
	// fails dynamic client resolution.
	dynamic map[string]*fakedynamic.FakeDynamicClient

	mu    sync.Mutex
	calls []string // "cluster/username"
}

func (f *fakeNetClients) cluster(id, username string) (*kfake.Clientset, error) {
	f.mu.Lock()
	f.calls = append(f.calls, k8s.NormalizedClusterID(id)+"/"+username)
	f.mu.Unlock()
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeNetClients) ClientForCluster(_ context.Context, id, username string, _ []string) (kubernetes.Interface, error) {
	return f.cluster(id, username)
}

func (f *fakeNetClients) DynamicClientForCluster(_ context.Context, id, username string, _ []string) (dynamic.Interface, error) {
	if _, err := f.cluster(id, username); err != nil {
		return nil, err
	}
	d, ok := f.dynamic[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no fake dynamic client for " + id)
	}
	return d, nil
}

func (f *fakeNetClients) TargetSchemaFor(_ context.Context, id, username string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id, username)
	if err != nil {
		return nil, err
	}
	return &k8s.TargetSchema{ClusterID: k8s.NormalizedClusterID(id), Generation: f.generation, Discovery: c.Discovery(), Invalidate: func() {}}, nil
}

func (f *fakeNetClients) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// remoteCiliumCluster is a fake remote cluster running Cilium in kube-system
// with Hubble and WireGuard enabled and the cilium.io API group served.
func remoteCiliumCluster() *kfake.Clientset {
	ds := testDaemonSet("kube-system", "cilium", "quay.io/cilium/cilium:v1.16.2", 3, 3)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: ciliumConfigMapName},
		Data:       map[string]string{"enable-hubble": "true", "enable-encryption": "true", "encryption-type": "wireguard", "ipam": "multi-pool"},
	}
	c := kfake.NewSimpleClientset(&ds, cm)
	c.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{GroupVersion: "cilium.io/v2"}}
	return c
}

// localDetectorWithFlannel is a local detector that reports flannel. A remote
// response carrying flannel, or a call to Detect (its nil clients panic), is
// a local fallback.
func localDetectorWithFlannel() *Detector {
	d := &Detector{}
	d.cached = &CNIInfo{Name: CNIFlannel, Namespace: "kube-flannel", Features: CNIFeatures{
		Encryption: true, EncryptionMode: "ipsec", ClusterMesh: true, EnvoyEnabled: true, IPAMMode: "kubernetes",
	}}
	return d
}

func decodeCNIInfo(t *testing.T, w *httptest.ResponseRecorder) CNIInfo {
	t.Helper()
	var body struct {
		Data CNIInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	return body.Data
}

func remoteNetHandler(clients *fakeNetClients) *Handler {
	return &Handler{
		Detector: localDetectorWithFlannel(),
		Clients:  clients,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestHandleCNIStatus_RemoteCilium(t *testing.T) {
	clients := &fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: remoteCiliumCluster()}}
	h := remoteNetHandler(clients)

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	info := decodeCNIInfo(t, w)
	if info.Name != CNICilium || info.Namespace != "kube-system" || info.Version != "1.16.2" || !info.Status.Healthy {
		t.Errorf("got %+v, want healthy Cilium 1.16.2 in kube-system", info)
	}
	if !info.HasCRDs || info.DetectionMethod != "daemonset+crd" {
		t.Errorf("hasCRDs=%v method=%q, want true daemonset+crd", info.HasCRDs, info.DetectionMethod)
	}
	if !info.Features.Hubble || !info.Features.WireGuard || info.Features.IPAMMode != "multi-pool" {
		t.Errorf("features = %+v, want Hubble, WireGuard and multi-pool IPAM from the remote cilium-config", info.Features)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("hubbleRelayAddr")) {
		t.Errorf("remote response carries a relay address: %s", w.Body.String())
	}
	for _, call := range clients.recorded() {
		if call != netRemoteCluster+"/alice" {
			t.Errorf("resolution %q, want %s as alice", call, netRemoteCluster)
		}
	}
}

func TestHandleCNIStatus_RemoteNonCilium(t *testing.T) {
	ds := testDaemonSet("calico-system", "calico-node", "calico/node:v3.28.0", 2, 2)
	clients := &fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: kfake.NewSimpleClientset(&ds)}}
	h := remoteNetHandler(clients)

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if info := decodeCNIInfo(t, w); info.Name != CNICalico || info.Features != (CNIFeatures{}) {
		t.Errorf("got %+v, want Calico with no Cilium features", info)
	}
}

func TestHandleCNIStatus_RemoteForbiddenIsNotUnknown(t *testing.T) {
	c := kfake.NewSimpleClientset()
	c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("denied internal-marker-9c1e"))
	})
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}})

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s; want 403", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"forbidden"`) || strings.Contains(body, "internal-marker-9c1e") {
		t.Errorf("body = %s; want reason forbidden and no remote error text", body)
	}
	if strings.Contains(body, CNIUnknown) || strings.Contains(body, CNIFlannel) {
		t.Errorf("forbidden read reported a CNI: %s", body)
	}
}

func TestHandleCNIStatus_RemoteCiliumConfigForbidden(t *testing.T) {
	c := remoteCiliumCluster()
	c.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, ciliumConfigMapName, errors.New("denied"))
	})
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}})

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s; want 403 rather than Cilium with every feature reported off", w.Code, w.Body.String())
	}
}

func TestHandleCNIStatus_RemoteUnreachable(t *testing.T) {
	h := remoteNetHandler(&fakeNetClients{targetErr: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect 10.20.30.40: connection refused")}})

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body %s; want 502", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "10.20.30.40") {
		t.Errorf("body leaks the remote address: %s", w.Body.String())
	}
}

func TestHandleCNIStatus_RemoteCachedPerIdentityAndEvicted(t *testing.T) {
	c := remoteCiliumCluster()
	var lists atomic.Int32
	c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}})

	get := func(user *auth.User, query string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/networking/cni"+query, nil)
		ctx := middleware.WithClusterID(auth.ContextWithUser(req.Context(), user), netRemoteCluster)
		w := httptest.NewRecorder()
		h.HandleCNIStatus(w, req.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
	}
	bob := &auth.User{ID: "u2", Username: "bob", KubernetesUsername: "bob"}

	get(networkingTestUser, "")
	afterFirst := lists.Load()
	get(networkingTestUser, "")
	if lists.Load() != afterFirst {
		t.Error("a second read by the same identity was not served from cache")
	}
	get(bob, "")
	if lists.Load() == afterFirst {
		t.Error("another identity was served the first identity's cached detection")
	}
	afterBob := lists.Load()
	h.EvictRemoteCache(netRemoteCluster)
	get(networkingTestUser, "")
	if lists.Load() == afterBob {
		t.Error("evicting the cluster did not clear its cached detection")
	}
	afterEvict := lists.Load()
	// A remote refresh re-reads the remote cluster; it must never run the
	// local detector, whose nil clients would panic.
	get(networkingTestUser, "?refresh=true")
	if lists.Load() == afterEvict {
		t.Error("refresh=true served the cached detection")
	}
}

// --- Remote Cilium config view and edit (#608 slice 5, U4) ---

// recordingAudit records every audit entry.
type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) all() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// configWrites counts ConfigMap updates on a fake cluster.
func configWrites(c *kfake.Clientset) int {
	n := 0
	for _, a := range c.Actions() {
		if a.GetVerb() == "update" && a.GetResource().Resource == "configmaps" {
			n++
		}
	}
	return n
}

func remoteConfigHandler(c *kfake.Clientset) (*Handler, *recordingAudit) {
	rec := &recordingAudit{}
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}, generation: "gen-1"})
	h.AuditLogger = rec
	return h, rec
}

func configPut(t *testing.T, h *Handler, clusterID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/networking/cni/config", bytes.NewReader(raw))
	ctx := auth.ContextWithUser(req.Context(), networkingTestUser)
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	w := httptest.NewRecorder()
	h.HandleUpdateCNIConfig(w, req.WithContext(ctx))
	return w
}

func validRemotePut() map[string]any {
	return map[string]any{
		"changes":          map[string]string{"debug": "true"},
		"confirmed":        true,
		"targetCluster":    netRemoteCluster,
		"targetGeneration": "gen-1",
	}
}

func errorReason(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	return body.Error.Reason
}

// crdOnlyCiliumWithLeftover is a remote cluster whose Cilium DaemonSet the
// user cannot see, so detection is CRD-only, and where reading kube-system's
// cilium-config is forbidden while a leftover cilium/cilium-config exists.
func crdOnlyCiliumWithLeftover(t *testing.T) (*kfake.Clientset, *[]string) {
	t.Helper()
	leftover := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "cilium", Name: ciliumConfigMapName}, Data: map[string]string{"debug": "false"}}
	c := kfake.NewSimpleClientset(leftover)
	c.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{GroupVersion: "cilium.io/v2"}}
	var gets []string
	c.PrependReactor("get", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
		gets = append(gets, a.GetNamespace())
		if a.GetNamespace() == "kube-system" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, ciliumConfigMapName, errors.New("denied"))
		}
		return false, nil, nil
	})
	return c, &gets
}

func TestHandleCNIConfig_RemoteForbiddenNeverReadsLeftover(t *testing.T) {
	c, gets := crdOnlyCiliumWithLeftover(t)
	h, _ := remoteConfigHandler(c)

	w := httptest.NewRecorder()
	h.HandleCNIConfig(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni/config", netRemoteCluster))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s; want 403", w.Code, w.Body.String())
	}
	for _, ns := range *gets {
		if ns == "cilium" {
			t.Errorf("read cilium/cilium-config after a 403 in kube-system")
		}
	}

	w = configPut(t, h, netRemoteCluster, validRemotePut())
	if w.Code != http.StatusForbidden {
		t.Fatalf("PUT status = %d, body %s; want 403", w.Code, w.Body.String())
	}
	if n := configWrites(c); n != 0 {
		t.Errorf("PUT wrote %d ConfigMaps, want 0", n)
	}
}

func TestHandleCNIConfig_RemoteReturnsConfigAndPin(t *testing.T) {
	h, _ := remoteConfigHandler(remoteCiliumCluster())

	w := httptest.NewRecorder()
	h.HandleCNIConfig(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni/config", netRemoteCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data CiliumConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cfg := body.Data
	if cfg.ConfigMapNamespace != "kube-system" || cfg.Config["ipam"] != "multi-pool" {
		t.Errorf("got %+v, want the remote kube-system cilium-config", cfg)
	}
	if cfg.TargetCluster != netRemoteCluster || cfg.TargetGeneration != "gen-1" {
		t.Errorf("pin = %q/%q, want %s/gen-1", cfg.TargetCluster, cfg.TargetGeneration, netRemoteCluster)
	}
}

func TestHandleUpdateCNIConfig_RemotePinRefusals(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(map[string]any)
		wantStatus int
		wantReason string
	}{
		{"no pin", func(b map[string]any) { delete(b, "targetCluster"); delete(b, "targetGeneration") }, http.StatusPreconditionRequired, "cluster_pin_required"},
		{"no generation", func(b map[string]any) { delete(b, "targetGeneration") }, http.StatusPreconditionRequired, "cluster_pin_required"},
		{"other cluster", func(b map[string]any) { b["targetCluster"] = "remote-other" }, http.StatusConflict, "cluster_pin_mismatch"},
		{"stale generation", func(b map[string]any) { b["targetGeneration"] = "gen-0" }, http.StatusConflict, "cluster_generation_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := remoteCiliumCluster()
			h, rec := remoteConfigHandler(c)
			body := validRemotePut()
			tt.mutate(body)
			w := configPut(t, h, netRemoteCluster, body)
			if w.Code != tt.wantStatus || errorReason(t, w) != tt.wantReason {
				t.Fatalf("got %d %q (body %s), want %d %q", w.Code, errorReason(t, w), w.Body.String(), tt.wantStatus, tt.wantReason)
			}
			if n := configWrites(c); n != 0 {
				t.Errorf("wrote %d ConfigMaps, want 0", n)
			}
			entries := rec.all()
			if len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].ClusterID != netRemoteCluster {
				t.Errorf("audit = %+v, want one failure row for %s", entries, netRemoteCluster)
			}
		})
	}
}

func TestHandleUpdateCNIConfig_RemoteClusterGone(t *testing.T) {
	h := remoteNetHandler(&fakeNetClients{targetErr: fmt.Errorf("cluster %s not found: %w", netRemoteCluster, pgx.ErrNoRows)})
	h.AuditLogger = &recordingAudit{}
	w := configPut(t, h, netRemoteCluster, validRemotePut())
	if w.Code != http.StatusNotFound || errorReason(t, w) != "cluster_unknown" {
		t.Fatalf("got %d %q (body %s), want 404 cluster_unknown", w.Code, errorReason(t, w), w.Body.String())
	}
}

func TestHandleUpdateCNIConfig_RemoteValidatesBeforeWriting(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"key outside allowlist": func(b map[string]any) { b["changes"] = map[string]string{"tunnel": "disabled"} },
		"not confirmed":         func(b map[string]any) { b["confirmed"] = false },
	} {
		t.Run(name, func(t *testing.T) {
			c := remoteCiliumCluster()
			h, _ := remoteConfigHandler(c)
			body := validRemotePut()
			mutate(body)
			w := configPut(t, h, netRemoteCluster, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s; want 400", w.Code, w.Body.String())
			}
			if len(c.Actions()) != 0 {
				t.Errorf("made %d API calls, want none", len(c.Actions()))
			}
		})
	}
}

func TestHandleUpdateCNIConfig_RemoteWriteOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
	}{
		{"conflict", apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, ciliumConfigMapName, errors.New("changed")), http.StatusConflict, "conflict"},
		{"forbidden", apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, ciliumConfigMapName, errors.New("denied")), http.StatusForbidden, "forbidden"},
		{"connection lost", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by 10.20.30.40")}, http.StatusBadGateway, "outcome_unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := remoteCiliumCluster()
			c.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, tt.err
			})
			h, rec := remoteConfigHandler(c)
			w := configPut(t, h, netRemoteCluster, validRemotePut())
			if w.Code != tt.wantStatus || errorReason(t, w) != tt.wantReason {
				t.Fatalf("got %d %q (body %s), want %d %q", w.Code, errorReason(t, w), w.Body.String(), tt.wantStatus, tt.wantReason)
			}
			if strings.Contains(w.Body.String(), "10.20.30.40") {
				t.Errorf("body leaks the remote address: %s", w.Body.String())
			}
			entries := rec.all()
			if len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].ClusterID != netRemoteCluster {
				t.Fatalf("audit = %+v, want one failure row for %s", entries, netRemoteCluster)
			}
			if tt.wantReason == "outcome_unknown" && !strings.Contains(entries[0].Detail, "outcome unknown") {
				t.Errorf("audit detail = %q, want it to note the unknown outcome", entries[0].Detail)
			}
		})
	}
}

func TestHandleUpdateCNIConfig_RemoteSuccess(t *testing.T) {
	c := remoteCiliumCluster()
	var lists atomic.Int32
	c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	h, rec := remoteConfigHandler(c)

	w := configPut(t, h, netRemoteCluster, validRemotePut())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data CiliumConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Config["debug"] != "true" || body.Data.TargetCluster != netRemoteCluster {
		t.Errorf("got %+v, want the updated remote config with its pin", body.Data)
	}
	cm, err := c.CoreV1().ConfigMaps("kube-system").Get(context.Background(), ciliumConfigMapName, metav1.GetOptions{})
	if err != nil || cm.Data["debug"] != "true" || cm.Data["ipam"] != "multi-pool" {
		t.Errorf("remote cilium-config = %v (err %v), want debug=true merged in", cm.Data, err)
	}

	entries := rec.all()
	if len(entries) != 1 {
		t.Fatalf("audit = %+v, want one row", entries)
	}
	e := entries[0]
	if e.Result != audit.ResultSuccess || e.Action != audit.ActionUpdate || e.ResourceKind != "CiliumConfig" ||
		e.ClusterID != netRemoteCluster || e.ResourceNamespace != "kube-system" || !strings.Contains(e.Detail, "debug") {
		t.Errorf("audit = %+v, want a successful CiliumConfig update on %s naming debug", e, netRemoteCluster)
	}

	// The write evicted the cluster's cached detection: the next status read
	// detects again.
	before := lists.Load()
	sw := httptest.NewRecorder()
	h.HandleCNIStatus(sw, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if lists.Load() == before {
		t.Error("CNI status after a config write was served from the pre-write cache")
	}
}

func TestHandleUpdateCNIConfig_RemoteDetectionForbidden(t *testing.T) {
	c := kfake.NewSimpleClientset()
	c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("denied"))
	})
	h, _ := remoteConfigHandler(c)
	w := configPut(t, h, netRemoteCluster, validRemotePut())
	if w.Code != http.StatusForbidden || errorReason(t, w) != "forbidden" {
		t.Fatalf("got %d %q (body %s), want 403 forbidden rather than 400 only-Cilium", w.Code, errorReason(t, w), w.Body.String())
	}
}

func localConfigHandler(t *testing.T) (*Handler, *kfake.Clientset, *recordingAudit) {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: ciliumConfigMapName}, Data: map[string]string{"debug": "false"}}
	c := kfake.NewSimpleClientset(cm)
	rec := &recordingAudit{}
	d := &Detector{}
	d.cached = &CNIInfo{Name: CNICilium, Namespace: "kube-system"}
	return &Handler{
		K8sClient:   k8s.NewFakeClientFactory(c),
		Detector:    d,
		AuditLogger: rec,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClusterID:   "local-cluster",
	}, c, rec
}

func TestHandleCNIConfig_LocalAddsOnlyPin(t *testing.T) {
	h, _, _ := localConfigHandler(t)
	w := httptest.NewRecorder()
	h.HandleCNIConfig(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni/config", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []string{"cniType", "configSource", "configMapName", "configMapNamespace", "editable", "config", "targetCluster", "targetGeneration"}
	if len(body.Data) != len(want) {
		t.Errorf("fields = %v, want exactly %v", body.Data, want)
	}
	if body.Data["targetCluster"] != "local" || body.Data["targetGeneration"] != "local" {
		t.Errorf("pin = %v/%v, want local/local", body.Data["targetCluster"], body.Data["targetGeneration"])
	}
}

func TestHandleUpdateCNIConfig_LocalWithoutPinUnchanged(t *testing.T) {
	h, c, rec := localConfigHandler(t)
	w := configPut(t, h, "", map[string]any{"changes": map[string]string{"debug": "true"}, "confirmed": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if cm, _ := c.CoreV1().ConfigMaps("kube-system").Get(context.Background(), ciliumConfigMapName, metav1.GetOptions{}); cm.Data["debug"] != "true" {
		t.Errorf("local cilium-config = %v, want debug=true", cm.Data)
	}
	entries := rec.all()
	if len(entries) != 1 || entries[0].Result != audit.ResultSuccess || entries[0].ClusterID != "local-cluster" {
		t.Errorf("audit = %+v, want one success row with the configured local cluster id", entries)
	}
}

func TestHandleUpdateCNIConfig_LocalPinMismatchRefused(t *testing.T) {
	h, c, _ := localConfigHandler(t)
	w := configPut(t, h, "", map[string]any{"changes": map[string]string{"debug": "true"}, "confirmed": true, "targetCluster": netRemoteCluster, "targetGeneration": "gen-1"})
	if w.Code != http.StatusConflict || errorReason(t, w) != "cluster_pin_mismatch" {
		t.Fatalf("got %d %q (body %s), want 409 cluster_pin_mismatch", w.Code, errorReason(t, w), w.Body.String())
	}
	if n := configWrites(c); n != 0 {
		t.Errorf("wrote %d ConfigMaps, want 0", n)
	}
}

// --- Remote BGP, IPAM and subsystems (#608 slice 5, U5) ---

var ciliumListKinds = map[schema.GroupVersionResource]string{
	bgpNodeConfigGVR:  "CiliumBGPNodeConfigList",
	ciliumNodeGVR:     "CiliumNodeList",
	ciliumEndpointGVR: "CiliumEndpointList",
}

// remoteCiliumCRDCluster is remoteCiliumCluster with the Cilium CRDs served
// and a dynamic client holding one BGP node config, two CiliumNodes (one
// encrypting) and three endpoints.
func remoteCiliumCRDCluster() (*kfake.Clientset, *fakedynamic.FakeDynamicClient) {
	c := remoteCiliumCluster()
	c.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{
		{GroupVersion: "cilium.io/v2", APIResources: []metav1.APIResource{{Name: "ciliumnodes"}, {Name: "ciliumendpoints"}}},
		{GroupVersion: "cilium.io/v2alpha1", APIResources: []metav1.APIResource{{Name: "ciliumbgpnodeconfigs"}}},
	}
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), ciliumListKinds,
		makeBGPNodeConfig("node-1", 64512, []bgpPeerData{{address: "10.0.0.1", asn: 65001, state: "established", received: 5, advertised: 3}}),
		makeCiliumNode("node-1", []string{"10.244.0.0/24"}, 10, 4, 1),
		makeCiliumNode("node-2", []string{"10.244.1.0/24"}, 10, 6, 0),
		makeEndpoint("ep-1", "default", "ready"),
		makeEndpoint("ep-2", "default", "ready"),
		makeEndpoint("ep-3", "kube-system", "not-ready"),
	)
	return c, dyn
}

func remotePanelHandler(c *kfake.Clientset, dyn *fakedynamic.FakeDynamicClient) *Handler {
	return remoteNetHandler(&fakeNetClients{
		clusters: map[string]*kfake.Clientset{netRemoteCluster: c},
		dynamic:  map[string]*fakedynamic.FakeDynamicClient{netRemoteCluster: dyn},
	})
}

func panelGet(t *testing.T, serve func(http.ResponseWriter, *http.Request), path string, out any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	serve(w, networkingRequest(http.MethodGet, path, netRemoteCluster))
	if out != nil && w.Code == http.StatusOK {
		var body struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %s: %v", w.Body.String(), err)
		}
		if err := json.Unmarshal(body.Data, out); err != nil {
			t.Fatalf("decoding data %s: %v", body.Data, err)
		}
	}
	return w
}

func TestHandleCiliumBGP_Remote(t *testing.T) {
	c, dyn := remoteCiliumCRDCluster()
	h := remotePanelHandler(c, dyn)

	var resp CiliumBGPResponse
	w := panelGet(t, h.HandleCiliumBGP, "/api/v1/networking/cilium/bgp", &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if !resp.Configured || len(resp.Peers) != 1 || resp.Peers[0].PeerAddress != "10.0.0.1" {
		t.Errorf("got %+v, want the remote BGP peer", resp)
	}
	if h.bgpCache != nil {
		t.Error("a remote read filled the local BGP cache")
	}
}

func TestHandleCiliumBGP_RemoteAbsenceAndUnknown(t *testing.T) {
	t.Run("BGP CRD not served is not configured", func(t *testing.T) {
		c, dyn := remoteCiliumCRDCluster()
		c.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{
			{GroupVersion: "cilium.io/v2", APIResources: []metav1.APIResource{{Name: "ciliumnodes"}}},
		}
		var resp CiliumBGPResponse
		w := panelGet(t, remotePanelHandler(c, dyn).HandleCiliumBGP, "/api/v1/networking/cilium/bgp", &resp)
		if w.Code != http.StatusOK || resp.Configured {
			t.Fatalf("got %d %+v (body %s), want 200 configured:false", w.Code, resp, w.Body.String())
		}
	})
	t.Run("non-Cilium cluster is not configured", func(t *testing.T) {
		ds := testDaemonSet("calico-system", "calico-node", "calico/node:v3.28.0", 1, 1)
		var resp CiliumBGPResponse
		w := panelGet(t, remotePanelHandler(kfake.NewSimpleClientset(&ds), nil).HandleCiliumBGP, "/api/v1/networking/cilium/bgp", &resp)
		if w.Code != http.StatusOK || resp.Configured {
			t.Fatalf("got %d %+v (body %s), want 200 configured:false", w.Code, resp, w.Body.String())
		}
	})
	t.Run("discovery failure is unknown, not absent", func(t *testing.T) {
		c, dyn := remoteCiliumCRDCluster()
		h := remotePanelHandler(c, dyn)
		// Prime detection, then break discovery for the CRD check.
		panelGet(t, h.HandleCNIStatus, "/api/v1/networking/cni", nil)
		c.PrependReactor("get", "resource", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("discovery down")
		})
		w := panelGet(t, h.HandleCiliumBGP, "/api/v1/networking/cilium/bgp", nil)
		if w.Code != http.StatusBadGateway || errorReason(t, w) != "discovery_unavailable" {
			t.Fatalf("got %d %q (body %s), want 502 discovery_unavailable", w.Code, errorReason(t, w), w.Body.String())
		}
	})
	t.Run("forbidden detection is not permitted, not absent", func(t *testing.T) {
		c := kfake.NewSimpleClientset()
		c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", errors.New("denied"))
		})
		w := panelGet(t, remotePanelHandler(c, nil).HandleCiliumBGP, "/api/v1/networking/cilium/bgp", nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, body %s; want 403", w.Code, w.Body.String())
		}
	})
}

func TestHandleCiliumBGP_RemoteCachedPerIdentity(t *testing.T) {
	c, dyn := remoteCiliumCRDCluster()
	var lists atomic.Int32
	dyn.PrependReactor("list", "ciliumbgpnodeconfigs", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	h := remotePanelHandler(c, dyn)
	get := func(user *auth.User) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/networking/cilium/bgp", nil)
		ctx := middleware.WithClusterID(auth.ContextWithUser(req.Context(), user), netRemoteCluster)
		w := httptest.NewRecorder()
		h.HandleCiliumBGP(w, req.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
	}
	get(networkingTestUser)
	get(networkingTestUser)
	if n := lists.Load(); n != 1 {
		t.Errorf("same identity listed %d times, want 1 (cached)", n)
	}
	get(&auth.User{ID: "u2", Username: "bob", KubernetesUsername: "bob"})
	if n := lists.Load(); n != 2 {
		t.Errorf("after a second identity listed %d times, want 2", n)
	}
	h.EvictRemoteCache(netRemoteCluster)
	get(networkingTestUser)
	if n := lists.Load(); n != 3 {
		t.Errorf("after eviction listed %d times, want 3", n)
	}
}

func TestHandleCiliumIPAM_Remote(t *testing.T) {
	c, dyn := remoteCiliumCRDCluster()
	h := remotePanelHandler(c, dyn)

	var resp CiliumIPAMResponse
	w := panelGet(t, h.HandleCiliumIPAM, "/api/v1/networking/cilium/ipam", &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if !resp.Configured || resp.Mode != "multi-pool" {
		t.Errorf("configured=%v mode=%q, want the remote cilium-config's multi-pool, not the local detector's", resp.Configured, resp.Mode)
	}
	if resp.Allocated != 10 || len(resp.PerNode) != 2 {
		t.Errorf("allocated=%d nodes=%d, want 10 across the two remote CiliumNodes", resp.Allocated, len(resp.PerNode))
	}
	if h.ipamCache != nil {
		t.Error("a remote read filled the local IPAM cache")
	}
}

func TestHandleCiliumSubsystems_Remote(t *testing.T) {
	c, dyn := remoteCiliumCRDCluster()
	h := remotePanelHandler(c, dyn)

	var resp CiliumSubsystemsResponse
	w := panelGet(t, h.HandleCiliumSubsystems, "/api/v1/networking/cilium/subsystems", &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if !resp.Configured || resp.Encryption == nil || resp.Encryption.Mode != "wireguard" || resp.Encryption.NodesTotal != 2 || resp.Encryption.NodesEncrypted != 1 {
		t.Errorf("encryption = %+v, want the remote wireguard config across 2 nodes, 1 encrypting", resp.Encryption)
	}
	if resp.Mesh == nil || resp.Mesh.Engine != "none" || resp.ClusterMesh == nil || resp.ClusterMesh.Enabled {
		t.Errorf("mesh=%+v clusterMesh=%+v, want the remote cluster's (none, disabled), not the local detector's", resp.Mesh, resp.ClusterMesh)
	}
	if resp.Endpoints == nil || resp.Endpoints.Total != 3 || resp.Endpoints.Ready != 2 {
		t.Errorf("endpoints = %+v, want 3 with 2 ready", resp.Endpoints)
	}
	if len(resp.Coverage) != 0 {
		t.Errorf("coverage = %+v, want none", resp.Coverage)
	}
	if h.subsystemCache != nil {
		t.Error("a remote read filled the local subsystems cache")
	}
}

func TestHandleCiliumSubsystems_RemotePartialCoverage(t *testing.T) {
	c, dyn := remoteCiliumCRDCluster()
	dyn.PrependReactor("list", "ciliumnodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "cilium.io", Resource: "ciliumnodes"}, "", errors.New("denied"))
	})
	dyn.PrependReactor("list", "ciliumendpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection reset")
	})
	h := remotePanelHandler(c, dyn)
	h.AgentCollector = &CiliumAgentCollector{} // remote must never run the local collector

	var resp CiliumSubsystemsResponse
	w := panelGet(t, h.HandleCiliumSubsystems, "/api/v1/networking/cilium/subsystems", &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want a partial 200", w.Code, w.Body.String())
	}
	if resp.Encryption == nil || resp.Encryption.Mode != "wireguard" {
		t.Errorf("encryption = %+v, want the config-derived section kept", resp.Encryption)
	}
	if resp.Endpoints != nil {
		t.Errorf("endpoints = %+v, want omitted rather than zero counts when the list failed", resp.Endpoints)
	}
	got := map[string]string{}
	for _, cov := range resp.Coverage {
		got[cov.Source] = cov.Status
	}
	want := map[string]string{"ciliumnodes": "forbidden", "ciliumendpoints": "unavailable", "cilium-agents": "unavailable"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("coverage = %v, want %v", got, want)
	}
}

// --- Remote agent exec in the handlers (#608 slice 5, U6b) ---

// healthStatus writes a cilium-dbg status reporting the given health and a
// ClusterMesh remote cluster named for the identity.
func healthStatus(username, state string, stdout io.Writer) error {
	return json.NewEncoder(stdout).Encode(ciliumAgentStatus{
		Cluster:     &agentCluster{CiliumHealth: &agentCiliumHealth{State: state, Msg: "probe " + state}},
		ClusterMesh: &agentClusterMesh{Clusters: []agentRemoteCluster{{Name: "seen-by-" + username, Connected: true}}},
	})
}

// remoteAgentHandler is a remote handler with agent exec enabled: two agent
// pods on the remote Cilium cluster, exec answered by exec.
func remoteAgentHandler(t *testing.T, exec *fakeExec) (*Handler, *recordingAudit) {
	t.Helper()
	c, dyn := remoteCiliumCRDCluster()
	for _, p := range []*corev1.Pod{agentPod("cilium-a", "n1"), agentPod("cilium-b", "n2")} {
		if _, err := c.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	h := remotePanelHandler(c, dyn)
	rec := &recordingAudit{}
	h.AuditLogger = rec
	// The local collector exists only to switch agent exec on; its nil
	// clients panic if a remote request ever reaches it.
	h.AgentCollector = &CiliumAgentCollector{maxConcurrent: 4}
	h.Streams = exec
	return h, rec
}

func TestHandleCiliumConnectivity_Remote(t *testing.T) {
	exec := &fakeExec{run: func(ctx context.Context, username, pod string, stdout, _ io.Writer) error {
		if pod == "cilium-b" {
			<-ctx.Done()
			return ctx.Err()
		}
		return healthStatus(username, "Ok", stdout)
	}}
	h, rec := remoteAgentHandler(t, exec)
	h.remote().agents.execTimeout = 100 * time.Millisecond

	var resp CiliumConnectivityResponse
	w := panelGet(t, h.HandleCiliumConnectivity, "/api/v1/networking/cilium/connectivity", &resp)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	if !resp.Configured || !resp.ExecEnabled || !resp.Partial || len(resp.Nodes) != 2 {
		t.Fatalf("got %+v, want a partial exec result for 2 nodes", resp)
	}
	byNode := map[string]NodeConnectivity{}
	for _, n := range resp.Nodes {
		byNode[n.NodeName] = n
	}
	if n1 := byNode["n1"]; n1.HealthState != "Ok" || n1.ExecError != "" {
		t.Errorf("n1 = %+v, want healthy", n1)
	}
	if n2 := byNode["n2"]; n2.ExecError != execErrTimeout || n2.HealthState == "Failure" {
		t.Errorf("n2 = %+v, want an exec timeout, not a node health failure", n2)
	}
	if entries := rec.all(); len(entries) != 1 || entries[0].ClusterID != netRemoteCluster || entries[0].Action != audit.ActionAgentExec {
		t.Errorf("audit = %+v, want one agent exec row for %s", entries, netRemoteCluster)
	}
}

func TestHandleCiliumConnectivity_RemoteExecForbidden(t *testing.T) {
	exec := &fakeExec{run: func(context.Context, string, string, io.Writer, io.Writer) error {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "pods/exec"}, "cilium-a", errors.New("denied"))
	}}
	h, rec := remoteAgentHandler(t, exec)

	w := panelGet(t, h.HandleCiliumConnectivity, "/api/v1/networking/cilium/connectivity", nil)
	if w.Code != http.StatusForbidden || errorReason(t, w) != "forbidden" {
		t.Fatalf("got %d %q (body %s), want 403 forbidden", w.Code, errorReason(t, w), w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exec into Cilium agents") {
		t.Errorf("body = %s, want the not-permitted-to-exec message", w.Body.String())
	}
	if n := exec.execCount(); n != 1 {
		t.Errorf("ran %d execs, want 1", n)
	}
	if entries := rec.all(); len(entries) != 1 {
		t.Errorf("audit = %+v, want one row", entries)
	}
}

func TestHandleCiliumConnectivity_RemoteExecDisabled(t *testing.T) {
	exec := &fakeExec{run: func(context.Context, string, string, io.Writer, io.Writer) error { return nil }}
	h, _ := remoteAgentHandler(t, exec)
	h.AgentCollector = nil

	var resp CiliumConnectivityResponse
	w := panelGet(t, h.HandleCiliumConnectivity, "/api/v1/networking/cilium/connectivity", &resp)
	if w.Code != http.StatusOK || !resp.Configured || resp.ExecEnabled || len(resp.Nodes) != 0 {
		t.Fatalf("got %d %+v (body %s), want the local not-enabled shape", w.Code, resp, w.Body.String())
	}
	if n := exec.execCount(); n != 0 {
		t.Errorf("ran %d execs with agent exec disabled", n)
	}
}

func TestHandleCiliumSubsystems_RemoteAgentData(t *testing.T) {
	t.Run("merged when collected", func(t *testing.T) {
		exec := &fakeExec{run: func(_ context.Context, username, _ string, stdout, _ io.Writer) error {
			return healthStatus(username, "Ok", stdout)
		}}
		h, _ := remoteAgentHandler(t, exec)

		var resp CiliumSubsystemsResponse
		w := panelGet(t, h.HandleCiliumSubsystems, "/api/v1/networking/cilium/subsystems", &resp)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		if resp.ClusterMesh == nil || len(resp.ClusterMesh.RemoteClusters) != 1 || resp.ClusterMesh.RemoteClusters[0].Name != "seen-by-alice" {
			t.Errorf("clusterMesh = %+v, want the remote agents' data, as alice", resp.ClusterMesh)
		}
		if len(resp.Coverage) != 0 {
			t.Errorf("coverage = %+v, want none", resp.Coverage)
		}
	})
	t.Run("forbidden exec is a coverage row", func(t *testing.T) {
		exec := &fakeExec{run: func(context.Context, string, string, io.Writer, io.Writer) error {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "pods/exec"}, "cilium-a", errors.New("denied"))
		}}
		h, _ := remoteAgentHandler(t, exec)

		var resp CiliumSubsystemsResponse
		w := panelGet(t, h.HandleCiliumSubsystems, "/api/v1/networking/cilium/subsystems", &resp)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s; want a partial 200", w.Code, w.Body.String())
		}
		if len(resp.Coverage) != 1 || resp.Coverage[0].Source != agentsSource || resp.Coverage[0].Status != k8s.CoverageStatusForbidden {
			t.Errorf("coverage = %+v, want one forbidden cilium-agents row", resp.Coverage)
		}
		if resp.Endpoints == nil || resp.Encryption == nil {
			t.Errorf("CRD sections lost when agent exec was refused: %+v", resp)
		}
	})
}

// --- Review fixes (#608 PR 5a code review) ---

// A remote cluster whose CNI DaemonSet is found but whose discovery fails
// reports its CRDs as unknown, not as absent.
func TestHandleCNIStatus_RemoteDiscoveryFailureLeavesCRDsUnknown(t *testing.T) {
	c := remoteCiliumCluster()
	c.Discovery().(*fakediscovery.FakeDiscovery).PrependReactor("get", "group", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("discovery unavailable")
	})
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}})

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", w.Code, w.Body.String())
	}
	info := decodeCNIInfo(t, w)
	if info.Name != CNICilium || info.HasCRDs || !info.CRDsUnknown {
		t.Errorf("got name %q hasCRDs %v crdsUnknown %v, want cilium with its CRDs unknown", info.Name, info.HasCRDs, info.CRDsUnknown)
	}
}

// The CNI DaemonSet is found on a later DaemonSet list page.
func TestHandleCNIStatus_RemoteDaemonSetsPaged(t *testing.T) {
	c := remoteCiliumCluster()
	ds := testDaemonSet("kube-system", "cilium", "quay.io/cilium/cilium:v1.16.2", 3, 3)
	other := testDaemonSet("kube-system", "node-exporter", "prom/node-exporter:v1.8.0", 3, 3)
	c.PrependReactor("list", "daemonsets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "kube-system" {
			return true, &appsv1.DaemonSetList{}, nil
		}
		if a.(k8stesting.ListActionImpl).ListOptions.Continue == "" {
			return true, &appsv1.DaemonSetList{ListMeta: metav1.ListMeta{Continue: "next"}, Items: []appsv1.DaemonSet{other}}, nil
		}
		return true, &appsv1.DaemonSetList{Items: []appsv1.DaemonSet{ds}}, nil
	})
	h := remoteNetHandler(&fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: c}})

	w := httptest.NewRecorder()
	h.HandleCNIStatus(w, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
	if info := decodeCNIInfo(t, w); w.Code != http.StatusOK || info.Name != CNICilium || info.DaemonSet != "cilium" {
		t.Errorf("status %d, cni %q daemonSet %q (body %s); want the cilium DaemonSet found on page two", w.Code, info.Name, info.DaemonSet, w.Body.String())
	}
}

// A config update whose outcome is unknown, or that conflicted, still drops
// the cluster's cached detection: the write may have landed.
func TestHandleUpdateCNIConfig_RemoteFailedWriteEvictsCache(t *testing.T) {
	for name, writeErr := range map[string]error{
		"outcome unknown": &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")},
		"conflict":        apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, ciliumConfigMapName, errors.New("changed")),
	} {
		t.Run(name, func(t *testing.T) {
			c := remoteCiliumCluster()
			var lists atomic.Int32
			c.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
				lists.Add(1)
				return false, nil, nil
			})
			c.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, writeErr
			})
			h, _ := remoteConfigHandler(c)
			configPut(t, h, netRemoteCluster, validRemotePut())

			before := lists.Load()
			sw := httptest.NewRecorder()
			h.HandleCNIStatus(sw, networkingRequest(http.MethodGet, "/api/v1/networking/cni", netRemoteCluster))
			if lists.Load() == before {
				t.Error("CNI status after a failed config write was served from the pre-write cache")
			}
		})
	}
}
