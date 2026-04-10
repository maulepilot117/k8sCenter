package networking

// CiliumBGPResponse is the response for GET /api/v1/networking/cilium/bgp.
type CiliumBGPResponse struct {
	Configured bool            `json:"configured"`
	Peers      []BGPPeerStatus `json:"peers,omitempty"`
}

// BGPPeerStatus represents the live state of a BGP peer session from CiliumBGPNodeConfig status.
type BGPPeerStatus struct {
	Node             string `json:"node"`
	PeerAddress      string `json:"peerAddress"`
	PeerASN          int64  `json:"peerASN"`
	LocalASN         int64  `json:"localASN"`
	SessionState     string `json:"sessionState"` // established, active, connect, idle, opensent, openconfirm
	RoutesReceived   int    `json:"routesReceived"`
	RoutesAdvertised int    `json:"routesAdvertised"`
}

// CiliumIPAMResponse is the response for GET /api/v1/networking/cilium/ipam.
type CiliumIPAMResponse struct {
	Configured     bool       `json:"configured"`
	Mode           string     `json:"mode,omitempty"`
	PodCIDRs       []string   `json:"podCIDRs,omitempty"`
	Allocated      int        `json:"allocated"`
	Available      int        `json:"available"`
	Total          int        `json:"total"`
	ExhaustionRisk string     `json:"exhaustionRisk,omitempty"` // none, medium, high
	PerNode        []NodeIPAM `json:"perNode,omitempty"`
}

// NodeIPAM represents per-node IPAM allocation data from CiliumNode CRDs.
type NodeIPAM struct {
	Node      string `json:"node"`
	Allocated int    `json:"allocated"`
	Available int    `json:"available"`
	PodCIDR   string `json:"podCIDR"`
}

// CiliumSubsystemsResponse is the response for GET /api/v1/networking/cilium/subsystems.
type CiliumSubsystemsResponse struct {
	Configured  bool             `json:"configured"`
	Encryption  *EncryptionInfo  `json:"encryption,omitempty"`
	Mesh        *MeshInfo        `json:"mesh,omitempty"`
	ClusterMesh *ClusterMeshInfo `json:"clusterMesh,omitempty"`
	Endpoints   *EndpointCounts  `json:"endpoints,omitempty"`
}

// EncryptionInfo describes the Cilium encryption configuration from ConfigMap and CiliumNode CRDs.
type EncryptionInfo struct {
	Enabled        bool            `json:"enabled"`
	Mode           string          `json:"mode"` // wireguard, ipsec
	NodesEncrypted int             `json:"nodesEncrypted"`
	NodesTotal     int             `json:"nodesTotal"`
	WireGuardNodes []WireGuardNode `json:"wireGuardNodes,omitempty"` // Phase B (agent)
}

// WireGuardNode represents per-node WireGuard tunnel state from Cilium agent.
type WireGuardNode struct {
	NodeName   string          `json:"nodeName"`
	PublicKey  string          `json:"publicKey"`
	ListenPort int64           `json:"listenPort"`
	PeerCount  int64           `json:"peerCount"`
	Peers      []WireGuardPeer `json:"peers"`
}

// WireGuardPeer represents a single WireGuard peer tunnel.
type WireGuardPeer struct {
	PublicKey     string `json:"publicKey"`
	Endpoint      string `json:"endpoint"`
	LastHandshake string `json:"lastHandshake"`
	TransferRx    int64  `json:"transferRx"`
	TransferTx    int64  `json:"transferTx"`
}

// MeshInfo describes the detected service mesh engine.
type MeshInfo struct {
	Enabled        bool   `json:"enabled"`
	Engine         string `json:"engine"`                   // cilium, none (istio/linkerd deferred to Phase C)
	DeploymentMode string `json:"deploymentMode,omitempty"` // Phase B (agent)
	TotalRedirects int64  `json:"totalRedirects,omitempty"` // Phase B (agent)
	TotalPorts     int64  `json:"totalPorts,omitempty"`     // Phase B (agent)
}

// ClusterMeshInfo describes whether ClusterMesh is configured.
type ClusterMeshInfo struct {
	Enabled        bool            `json:"enabled"`
	RemoteClusters []RemoteCluster `json:"remoteClusters,omitempty"` // Phase B (agent)
}

// RemoteCluster represents a ClusterMesh remote cluster connection.
type RemoteCluster struct {
	Name              string `json:"name"`
	Connected         bool   `json:"connected"`
	Ready             bool   `json:"ready"`
	Status            string `json:"status"`
	NumNodes          int64  `json:"numNodes"`
	NumEndpoints      int64  `json:"numEndpoints"`
	NumSharedServices int64  `json:"numSharedServices"`
	NumFailures       int64  `json:"numFailures"`
	LastFailure       string `json:"lastFailure,omitempty"`
}

// EndpointCounts aggregates Cilium endpoint states from CiliumEndpoint CRDs.
type EndpointCounts struct {
	Total         int `json:"total"`
	Ready         int `json:"ready"`
	NotReady      int `json:"notReady"`
	Disconnecting int `json:"disconnecting"`
	Waiting       int `json:"waiting"`
}

// CiliumConnectivityResponse is the response for GET /api/v1/networking/cilium/connectivity.
type CiliumConnectivityResponse struct {
	Configured  bool               `json:"configured"`
	ExecEnabled bool               `json:"execEnabled"`
	Nodes       []NodeConnectivity `json:"nodes"`
	CollectedAt string             `json:"collectedAt,omitempty"`
	Partial     bool               `json:"partial"`
}

// NodeConnectivity represents per-node Cilium agent health.
type NodeConnectivity struct {
	NodeName    string `json:"nodeName"`
	HealthState string `json:"healthState"` // Ok, Warning, Failure
	Message     string `json:"message,omitempty"`
}

// computeExhaustionRisk returns the exhaustion risk level based on IPAM utilization.
func computeExhaustionRisk(allocated, total int) string {
	if total <= 0 {
		return "none"
	}
	pct := float64(allocated) / float64(total) * 100
	switch {
	case pct > 90:
		return "high"
	case pct > 75:
		return "medium"
	default:
		return "none"
	}
}
