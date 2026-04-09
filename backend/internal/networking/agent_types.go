package networking

import "time"

// ciliumAgentStatus is a minimal subset of cilium-dbg's StatusResponse.
type ciliumAgentStatus struct {
	Encryption *agentEncryption  `json:"encryption,omitempty"`
	ClusterMesh *agentClusterMesh `json:"cluster-mesh,omitempty"`
	Proxy      *agentProxy       `json:"proxy,omitempty"`
	Cluster    *agentCluster     `json:"cluster,omitempty"`
}

type agentEncryption struct {
	Mode      string          `json:"mode,omitempty"`
	Msg       string          `json:"msg,omitempty"`
	Wireguard *agentWireguard `json:"wireguard,omitempty"`
}

type agentWireguard struct {
	Interfaces []agentWGInterface `json:"interfaces"`
}

type agentWGInterface struct {
	Name       string         `json:"name,omitempty"`
	PublicKey  string         `json:"public-key,omitempty"`
	ListenPort int64          `json:"listen-port,omitempty"`
	PeerCount  int64          `json:"peer-count,omitempty"`
	Peers      []agentWGPeer  `json:"peers"`
}

type agentWGPeer struct {
	PublicKey         string `json:"public-key,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
	LastHandshakeTime string `json:"last-handshake-time,omitempty"`
	TransferRx        int64  `json:"transfer-rx,omitempty"`
	TransferTx        int64  `json:"transfer-tx,omitempty"`
}

type agentClusterMesh struct {
	Clusters []agentRemoteCluster `json:"clusters"`
}

type agentRemoteCluster struct {
	Name              string `json:"name,omitempty"`
	Connected         bool   `json:"connected,omitempty"`
	Ready             bool   `json:"ready,omitempty"`
	Status            string `json:"status,omitempty"`
	NumNodes          int64  `json:"num-nodes,omitempty"`
	NumEndpoints      int64  `json:"num-endpoints,omitempty"`
	NumSharedServices int64  `json:"num-shared-services,omitempty"`
	NumFailures       int64  `json:"num-failures,omitempty"`
	LastFailure       string `json:"last-failure,omitempty"`
}

type agentProxy struct {
	DeploymentMode string `json:"envoy-deployment-mode,omitempty"`
	TotalRedirects int64  `json:"total-redirects,omitempty"`
	TotalPorts     int64  `json:"total-ports,omitempty"`
}

type agentCluster struct {
	CiliumHealth *agentCiliumHealth `json:"ciliumHealth,omitempty"`
}

type agentCiliumHealth struct {
	State string `json:"state,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// agentCollectionResult holds parsed results from all agent pods.
type agentCollectionResult struct {
	nodes     []agentNodeResult
	collected time.Time
	partial   bool
}

type agentNodeResult struct {
	nodeName string
	podName  string
	status   *ciliumAgentStatus
	err      string
}
