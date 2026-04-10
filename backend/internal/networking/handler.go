package networking

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// k8sNameRegexp validates RFC 1123 DNS label names.
var k8sNameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)

// Handler serves networking-related HTTP endpoints.
type Handler struct {
	K8sClient    *k8s.ClientFactory
	Detector     *Detector
	HubbleClient *HubbleClient
	AuditLogger  audit.Logger
	Logger       *slog.Logger
	ClusterID    string

	// Generation counter — incremented on cache invalidation; fetch functions
	// capture the value before starting work and discard the result if it changed.
	cacheGen uint64

	// Singleflight + cache for subsystems endpoint (encryption, mesh, ClusterMesh, endpoints).
	subsystemGroup singleflight.Group
	subsystemMu    sync.RWMutex
	subsystemCache *cachedSubsystems

	// Singleflight + cache for BGP endpoint.
	bgpGroup singleflight.Group
	bgpMu    sync.RWMutex
	bgpCache *cachedBGP

	// Singleflight + cache for IPAM endpoint.
	ipamGroup singleflight.Group
	ipamMu    sync.RWMutex
	ipamCache *cachedIPAM

	// Phase B: opt-in Cilium agent exec collector (nil when disabled).
	AgentCollector *CiliumAgentCollector
}

const cacheTTL = 30 * time.Second

type cachedSubsystems struct {
	data      *CiliumSubsystemsResponse
	fetchedAt time.Time
}

type cachedBGP struct {
	data      *CiliumBGPResponse
	fetchedAt time.Time
}

type cachedIPAM struct {
	data      *CiliumIPAMResponse
	fetchedAt time.Time
}

// InvalidateCaches clears all caches so the next request re-fetches.
// Called after ConfigMap edits to prevent stale data.
func (h *Handler) InvalidateCaches() {
	h.subsystemMu.Lock()
	h.subsystemCache = nil
	h.subsystemMu.Unlock()

	h.bgpMu.Lock()
	h.bgpCache = nil
	h.bgpMu.Unlock()

	h.ipamMu.Lock()
	h.ipamCache = nil
	h.ipamMu.Unlock()

	if h.AgentCollector != nil {
		h.AgentCollector.InvalidateCache()
	}

	atomic.AddUint64(&h.cacheGen, 1)
}

// isCiliumLocal returns true if the detected CNI is Cilium and no remote cluster is selected.
func (h *Handler) isCiliumLocal(r *http.Request) bool {
	info := h.Detector.CachedInfo()
	if info == nil || info.Name != CNICilium {
		return false
	}
	clusterID := middleware.ClusterIDFromContext(r.Context())
	return clusterID == "" || clusterID == "local"
}

// HandleCNIStatus returns the detected CNI plugin information.
// GET /api/v1/networking/cni
func (h *Handler) HandleCNIStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	refresh := r.URL.Query().Get("refresh") == "true"

	var info *CNIInfo
	if refresh {
		info = h.Detector.Detect(r.Context())
	} else {
		info = h.Detector.CachedInfo()
		if info == nil {
			info = h.Detector.Detect(r.Context())
		}
	}

	httputil.WriteData(w, info)
}

// HandleCNIConfig returns the current CNI configuration.
// GET /api/v1/networking/cni/config
func (h *Handler) HandleCNIConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	info := h.Detector.CachedInfo()
	if info == nil {
		info = h.Detector.Detect(r.Context())
	}

	if info.Name != CNICilium {
		httputil.WriteData(w, map[string]any{
			"cniType":  info.Name,
			"editable": false,
			"message":  "Configuration editing is only supported for Cilium",
		})
		return
	}

	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create impersonated client", "")
		return
	}
	config, err := ReadCiliumConfig(r.Context(), cs)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to read Cilium config", "")
		return
	}
	httputil.WriteData(w, config)
}

// CiliumConfigUpdate is the request body for PUT /api/v1/networking/cni/config.
type CiliumConfigUpdate struct {
	Changes   map[string]string `json:"changes"`
	Confirmed bool              `json:"confirmed"`
}

// HandleUpdateCNIConfig applies CNI configuration changes (Cilium only).
// PUT /api/v1/networking/cni/config
func (h *Handler) HandleUpdateCNIConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	info := h.Detector.CachedInfo()
	if info == nil || info.Name != CNICilium {
		httputil.WriteError(w, http.StatusBadRequest,
			"CNI configuration editing is only supported for Cilium", "")
		return
	}

	var req CiliumConfigUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if !req.Confirmed {
		httputil.WriteError(w, http.StatusBadRequest,
			"CNI configuration changes require explicit confirmation", "Set confirmed: true to proceed")
		return
	}

	if len(req.Changes) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "no changes provided", "")
		return
	}

	// Validate changes against allowlist
	if err := ValidateCiliumChanges(req.Changes); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	// Use impersonated client to enforce Kubernetes RBAC
	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create impersonated client", "")
		return
	}

	// Apply changes to cilium-config ConfigMap
	updatedNS, err := UpdateCiliumConfig(r.Context(), cs, req.Changes)
	if err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp:         time.Now().UTC(),
			ClusterID:         h.ClusterID,
			User:              user.Username,
			SourceIP:          r.RemoteAddr,
			Action:            audit.ActionUpdate,
			ResourceKind:      "CiliumConfig",
			ResourceNamespace: updatedNS,
			ResourceName:      "cilium-config",
			Result:            audit.ResultFailure,
			Detail:            formatChangedKeys(req.Changes),
		})
		httputil.WriteError(w, http.StatusBadGateway, "failed to update Cilium config", "")
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionUpdate,
		ResourceKind:      "CiliumConfig",
		ResourceNamespace: updatedNS,
		ResourceName:      "cilium-config",
		Result:            audit.ResultSuccess,
		Detail:            formatChangedKeys(req.Changes),
	})
	h.Logger.Info("cilium config updated", "user", user.Username, "changedKeys", len(req.Changes))

	// Return updated config by reading from the known namespace (avoids redundant probing)
	config, err := ReadCiliumConfigFromNamespace(r.Context(), cs, updatedNS)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "config updated but failed to re-read", "")
		return
	}
	httputil.WriteData(w, config)

	// Refresh cached CNI features asynchronously (non-blocking)
	go h.Detector.Detect(context.Background())

	// Invalidate all caches so next poll gets fresh data
	h.InvalidateCaches()
}

// HandleHubbleFlows returns network flows from Hubble Relay filtered by namespace and verdict.
// GET /api/v1/networking/hubble/flows?namespace=default&verdict=DROPPED&limit=100
func (h *Handler) HandleHubbleFlows(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if h.HubbleClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Hubble is not available", "")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace parameter is required", "")
		return
	}
	// Validate namespace against k8s naming rules
	if !k8sNameRegexp.MatchString(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace name", "")
		return
	}

	// Validate verdict filter
	verdict := r.URL.Query().Get("verdict")
	if verdict != "" && !ValidVerdict(verdict) {
		httputil.WriteError(w, http.StatusBadRequest,
			"invalid verdict filter, must be one of: FORWARDED, DROPPED, ERROR, AUDIT", "")
		return
	}

	// Validate limit
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil || v < 1 || v > 1000 {
			httputil.WriteError(w, http.StatusBadRequest, "limit must be between 1 and 1000", "")
			return
		}
		limit = v
	}

	// RBAC: check user can get pods in the requested namespace (flow visibility = pod observability)
	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to check permissions", "")
		return
	}
	_, err = cs.CoreV1().Pods(namespace).List(r.Context(), k8smetav1.ListOptions{Limit: 1})
	if err != nil {
		httputil.WriteError(w, http.StatusForbidden,
			"you do not have permission to view flows in namespace "+namespace, "")
		return
	}

	flows, err := h.HubbleClient.GetFlows(r.Context(), namespace, verdict, limit)
	if err != nil {
		h.Logger.Error("hubble flow query failed", "error", err, "namespace", namespace)
		httputil.WriteError(w, http.StatusBadGateway, "failed to query Hubble flows", "")
		return
	}

	httputil.WriteData(w, flows)
}

// HandleCiliumBGP returns BGP peering configuration and live peer status from Cilium CRDs.
// GET /api/v1/networking/cilium/bgp
// Note: uses BaseDynamicClient for read-only CRD access, consistent with policy/gitops/notification handlers.
func (h *Handler) HandleCiliumBGP(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumBGPResponse{Configured: false})
		return
	}

	// Check cache first
	h.bgpMu.RLock()
	if h.bgpCache != nil && time.Since(h.bgpCache.fetchedAt) < cacheTTL {
		data := h.bgpCache.data
		h.bgpMu.RUnlock()
		httputil.WriteData(w, data)
		return
	}
	h.bgpMu.RUnlock()

	// Singleflight to coalesce concurrent requests.
	// Detached context prevents one caller's cancellation from cascading to all waiters.
	result, err, _ := h.bgpGroup.Do("bgp", func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), cacheTTL+10*time.Second)
		defer cancel()
		return h.fetchBGP(ctx)
	})
	if err != nil {
		h.Logger.Error("failed to fetch BGP status", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to read BGP status from CRDs", "")
		return
	}

	data := result.(*CiliumBGPResponse)
	httputil.WriteData(w, data)
}

func (h *Handler) fetchBGP(ctx context.Context) (*CiliumBGPResponse, error) {
	gen := atomic.LoadUint64(&h.cacheGen)

	disc := h.K8sClient.DiscoveryClient()
	if !hasCRD(disc, bgpNodeConfigGVR) {
		resp := &CiliumBGPResponse{Configured: false}
		if atomic.LoadUint64(&h.cacheGen) == gen {
			h.bgpMu.Lock()
			h.bgpCache = &cachedBGP{data: resp, fetchedAt: time.Now()}
			h.bgpMu.Unlock()
		}
		return resp, nil
	}

	dynClient := h.K8sClient.BaseDynamicClient()
	peers, err := readBGPNodeConfigs(ctx, dynClient)
	if err != nil {
		return nil, err
	}

	// Ensure non-nil slice for consistent JSON output
	if peers == nil {
		peers = []BGPPeerStatus{}
	}

	resp := &CiliumBGPResponse{
		Configured: true,
		Peers:      peers,
	}

	if atomic.LoadUint64(&h.cacheGen) == gen {
		h.bgpMu.Lock()
		h.bgpCache = &cachedBGP{data: resp, fetchedAt: time.Now()}
		h.bgpMu.Unlock()
	}

	return resp, nil
}

// HandleCiliumIPAM returns IPAM allocation data from CiliumNode CRDs.
// GET /api/v1/networking/cilium/ipam
// Note: uses BaseDynamicClient for read-only CRD access, consistent with policy/gitops/notification handlers.
func (h *Handler) HandleCiliumIPAM(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumIPAMResponse{Configured: false})
		return
	}

	// Check cache first
	h.ipamMu.RLock()
	if h.ipamCache != nil && time.Since(h.ipamCache.fetchedAt) < cacheTTL {
		data := h.ipamCache.data
		h.ipamMu.RUnlock()
		httputil.WriteData(w, data)
		return
	}
	h.ipamMu.RUnlock()

	// Singleflight to coalesce concurrent requests.
	// Detached context prevents one caller's cancellation from cascading to all waiters.
	result, err, _ := h.ipamGroup.Do("ipam", func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), cacheTTL+10*time.Second)
		defer cancel()
		return h.fetchIPAM(ctx)
	})
	if err != nil {
		h.Logger.Error("failed to fetch IPAM data", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to read IPAM data from CRDs", "")
		return
	}

	data := result.(*CiliumIPAMResponse)
	httputil.WriteData(w, data)
}

func (h *Handler) fetchIPAM(ctx context.Context) (*CiliumIPAMResponse, error) {
	gen := atomic.LoadUint64(&h.cacheGen)

	disc := h.K8sClient.DiscoveryClient()
	if !hasCRD(disc, ciliumNodeGVR) {
		resp := &CiliumIPAMResponse{Configured: false}
		if atomic.LoadUint64(&h.cacheGen) == gen {
			h.ipamMu.Lock()
			h.ipamCache = &cachedIPAM{data: resp, fetchedAt: time.Now()}
			h.ipamMu.Unlock()
		}
		return resp, nil
	}

	dynClient := h.K8sClient.BaseDynamicClient()
	nodes, err := readCiliumNodes(ctx, dynClient)
	if err != nil {
		return nil, err
	}

	var totalAllocated, totalAvailable int
	var allCIDRs []string
	var perNode []NodeIPAM

	for _, n := range nodes {
		totalAllocated += n.UsedCount

		// Available = pool capacity minus used; clamp to zero
		avail := n.PoolCount - n.UsedCount
		if avail < 0 {
			avail = 0
		}
		totalAvailable += avail

		allCIDRs = append(allCIDRs, n.PodCIDRs...)

		cidr := ""
		if len(n.PodCIDRs) > 0 {
			cidr = n.PodCIDRs[0]
		}
		perNode = append(perNode, NodeIPAM{
			Node:      n.Name,
			Allocated: n.UsedCount,
			Available: avail,
			PodCIDR:   cidr,
		})
	}

	total := totalAllocated + totalAvailable

	// Determine IPAM mode from cached Cilium ConfigMap detection
	mode := "cluster-pool" // default
	info := h.Detector.CachedInfo()
	if info != nil && info.Features.IPAMMode != "" {
		mode = info.Features.IPAMMode
	}

	// Ensure non-nil slices for consistent JSON output
	if allCIDRs == nil {
		allCIDRs = []string{}
	}
	if perNode == nil {
		perNode = []NodeIPAM{}
	}

	resp := &CiliumIPAMResponse{
		Configured:     true,
		Mode:           mode,
		PodCIDRs:       allCIDRs,
		Allocated:      totalAllocated,
		Available:      totalAvailable,
		Total:          total,
		ExhaustionRisk: computeExhaustionRisk(totalAllocated, total),
		PerNode:        perNode,
	}

	if atomic.LoadUint64(&h.cacheGen) == gen {
		h.ipamMu.Lock()
		h.ipamCache = &cachedIPAM{data: resp, fetchedAt: time.Now()}
		h.ipamMu.Unlock()
	}

	return resp, nil
}

// HandleCiliumSubsystems returns encryption, mesh, ClusterMesh, and endpoint health status.
// GET /api/v1/networking/cilium/subsystems
// Note: uses BaseDynamicClient for read-only CRD access, consistent with policy/gitops/notification handlers.
func (h *Handler) HandleCiliumSubsystems(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumSubsystemsResponse{Configured: false})
		return
	}

	// Check cache first
	h.subsystemMu.RLock()
	if h.subsystemCache != nil && time.Since(h.subsystemCache.fetchedAt) < cacheTTL {
		data := h.subsystemCache.data
		h.subsystemMu.RUnlock()
		httputil.WriteData(w, data)
		return
	}
	h.subsystemMu.RUnlock()

	// Singleflight to coalesce concurrent requests.
	// Detached context prevents one caller's cancellation from cascading to all waiters.
	result, err, _ := h.subsystemGroup.Do("subsystems", func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), agentOuterTimeout+10*time.Second)
		defer cancel()
		return h.fetchSubsystems(ctx)
	})
	if err != nil {
		h.Logger.Error("failed to fetch subsystems", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to read Cilium subsystem status", "")
		return
	}

	data := result.(*CiliumSubsystemsResponse)
	httputil.WriteData(w, data)
}

func (h *Handler) fetchSubsystems(ctx context.Context) (*CiliumSubsystemsResponse, error) {
	gen := atomic.LoadUint64(&h.cacheGen)

	info := h.Detector.CachedInfo()

	// Encryption from ConfigMap + CiliumNode CRDs
	encInfo := &EncryptionInfo{
		Enabled: info != nil && info.Features.Encryption,
		Mode:    "",
	}
	if encInfo.Enabled && info != nil {
		encInfo.Mode = info.Features.EncryptionMode
	}

	// Fetch CiliumNodes, CiliumEndpoints, and agent data concurrently
	dynClient := h.K8sClient.BaseDynamicClient()

	var nodes []ciliumNodeIPAM
	var endpoints EndpointCounts
	var agentResult *agentCollectionResult
	var agentErr error

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		nodes, err = readCiliumNodes(gCtx, dynClient)
		return err
	})
	g.Go(func() error {
		var err error
		endpoints, err = aggregateEndpoints(gCtx, dynClient)
		return err
	})
	// Agent collection runs in parallel with CRD reads (opt-in, never fails the group)
	if h.AgentCollector != nil {
		g.Go(func() error {
			agentResult, agentErr = h.AgentCollector.Collect(gCtx)
			return nil // agent errors captured separately
		})
	}
	if err := g.Wait(); err != nil {
		// Log but continue with partial data — one may have succeeded
		h.Logger.Warn("partial failure fetching subsystem data", "error", err)
	}

	// Count encrypted nodes from CiliumNode data (nil-safe if readCiliumNodes failed)
	encInfo.NodesTotal = len(nodes)
	for _, n := range nodes {
		if n.EncryptionKey > 0 {
			encInfo.NodesEncrypted++
		}
	}

	// Mesh from Detector
	meshEngine := h.Detector.DetectMesh()
	meshInfo := &MeshInfo{
		Enabled: meshEngine != "none",
		Engine:  meshEngine,
	}

	// ClusterMesh from ConfigMap
	clusterMeshInfo := &ClusterMeshInfo{
		Enabled: info != nil && info.Features.ClusterMesh,
	}

	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  encInfo,
		Mesh:        meshInfo,
		ClusterMesh: clusterMeshInfo,
		Endpoints:   &endpoints,
	}

	// Agent enrichment (additive — never replaces CRD data)
	if agentErr != nil {
		h.Logger.Warn("agent collection failed, returning CRD-only data", "error", agentErr)
	} else if agentResult != nil {
		mergeAgentIntoSubsystems(resp, agentResult)
	}

	// Cache the result if generation hasn't changed
	if atomic.LoadUint64(&h.cacheGen) == gen {
		h.subsystemMu.Lock()
		h.subsystemCache = &cachedSubsystems{data: resp, fetchedAt: time.Now()}
		h.subsystemMu.Unlock()
	}

	return resp, nil
}

// mergeAgentIntoSubsystems enriches a CRD-based subsystems response with data
// collected from Cilium agent pods. Only successful nodes contribute; failed
// nodes retain their CRD-only data.
func mergeAgentIntoSubsystems(resp *CiliumSubsystemsResponse, agent *agentCollectionResult) {
	if agent == nil {
		return
	}

	for _, node := range agent.nodes {
		if node.status == nil {
			continue
		}

		// Encryption: WireGuard peer data
		if resp.Encryption != nil && node.status.Encryption != nil && node.status.Encryption.Wireguard != nil {
			for _, iface := range node.status.Encryption.Wireguard.Interfaces {
				wgNode := WireGuardNode{
					NodeName:   node.nodeName,
					PublicKey:  iface.PublicKey,
					ListenPort: iface.ListenPort,
					PeerCount:  iface.PeerCount,
				}
				for _, peer := range iface.Peers {
					wgNode.Peers = append(wgNode.Peers, WireGuardPeer{
						PublicKey:     peer.PublicKey,
						Endpoint:      peer.Endpoint,
						LastHandshake: peer.LastHandshakeTime,
						TransferRx:    peer.TransferRx,
						TransferTx:    peer.TransferTx,
					})
				}
				if wgNode.Peers == nil {
					wgNode.Peers = []WireGuardPeer{}
				}
				resp.Encryption.WireGuardNodes = append(resp.Encryption.WireGuardNodes, wgNode)
			}
		}

		// Mesh: Envoy proxy data — DeploymentMode from first node (uniform across cluster),
		// TotalRedirects/TotalPorts aggregated across all nodes.
		if resp.Mesh != nil && node.status.Proxy != nil {
			if resp.Mesh.DeploymentMode == "" {
				resp.Mesh.DeploymentMode = node.status.Proxy.DeploymentMode
			}
			resp.Mesh.TotalRedirects += node.status.Proxy.TotalRedirects
			resp.Mesh.TotalPorts += node.status.Proxy.TotalPorts
		}

		// ClusterMesh: remote clusters (take from first node that reports it)
		if resp.ClusterMesh != nil && node.status.ClusterMesh != nil && resp.ClusterMesh.RemoteClusters == nil {
			for _, rc := range node.status.ClusterMesh.Clusters {
				resp.ClusterMesh.RemoteClusters = append(resp.ClusterMesh.RemoteClusters, RemoteCluster{
					Name:              rc.Name,
					Connected:         rc.Connected,
					Ready:             rc.Ready,
					Status:            rc.Status,
					NumNodes:          rc.NumNodes,
					NumEndpoints:      rc.NumEndpoints,
					NumSharedServices: rc.NumSharedServices,
					NumFailures:       rc.NumFailures,
					LastFailure:       rc.LastFailure,
				})
			}
		}
	}
}

// HandleCiliumConnectivity returns per-node Cilium agent health status.
// GET /api/v1/networking/cilium/connectivity
func (h *Handler) HandleCiliumConnectivity(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumConnectivityResponse{
			Configured: false,
			Nodes:      []NodeConnectivity{},
		})
		return
	}

	if h.AgentCollector == nil {
		httputil.WriteData(w, CiliumConnectivityResponse{
			Configured: true,
			Nodes:      []NodeConnectivity{},
		})
		return
	}

	agentResult, err := h.AgentCollector.Collect(r.Context())
	if err != nil {
		h.Logger.Error("agent collection failed for connectivity", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to collect Cilium agent diagnostics", "")
		return
	}

	var nodes []NodeConnectivity
	for _, node := range agentResult.nodes {
		nc := NodeConnectivity{
			NodeName:    node.nodeName,
			HealthState: "Unknown",
		}
		if node.status != nil && node.status.Cluster != nil && node.status.Cluster.CiliumHealth != nil {
			nc.HealthState = node.status.Cluster.CiliumHealth.State
			nc.Message = node.status.Cluster.CiliumHealth.Msg
		} else if node.err != "" {
			nc.HealthState = "Failure"
			nc.Message = "agent exec failed"
		}
		nodes = append(nodes, nc)
	}
	if nodes == nil {
		nodes = []NodeConnectivity{}
	}

	httputil.WriteData(w, CiliumConnectivityResponse{
		Configured:  true,
		ExecEnabled: true,
		Nodes:       nodes,
		CollectedAt: agentResult.collected.UTC().Format(time.RFC3339),
		Partial:     agentResult.partial,
	})
}

// formatChangedKeys returns a sorted, comma-separated list of changed key names for audit logging.
func formatChangedKeys(changes map[string]string) string {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "changed keys: " + strings.Join(keys, ", ")
}
