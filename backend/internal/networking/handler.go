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
	"time"

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
	Informers    *k8s.InformerManager
	AuditLogger  audit.Logger
	Logger       *slog.Logger
	ClusterID    string

	// Singleflight + cache for subsystems endpoint (encryption, mesh, ClusterMesh, endpoints).
	subsystemGroup singleflight.Group
	subsystemMu    sync.RWMutex
	subsystemCache *cachedSubsystems
}

const subsystemCacheTTL = 30 * time.Second

type cachedSubsystems struct {
	data      *CiliumSubsystemsResponse
	fetchedAt time.Time
}

// InvalidateSubsystemCaches clears the subsystem cache so the next request re-fetches.
// Called after ConfigMap edits to prevent stale data.
func (h *Handler) InvalidateSubsystemCaches() {
	h.subsystemMu.Lock()
	h.subsystemCache = nil
	h.subsystemMu.Unlock()
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

	// Invalidate subsystem cache so next poll gets fresh data
	h.InvalidateSubsystemCaches()
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
func (h *Handler) HandleCiliumBGP(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumBGPResponse{Configured: false})
		return
	}

	disc := h.K8sClient.DiscoveryClient()
	if !hasCRD(disc, bgpClusterConfigGVR) {
		httputil.WriteData(w, CiliumBGPResponse{Configured: false})
		return
	}

	dynClient := h.K8sClient.BaseDynamicClient()

	clusterConfigs, err := readBGPClusterConfigs(r.Context(), dynClient)
	if err != nil {
		h.Logger.Error("failed to read BGP cluster configs", "error", err)
	}

	peerConfigs, err := readBGPPeerConfigs(r.Context(), dynClient)
	if err != nil {
		h.Logger.Error("failed to read BGP peer configs", "error", err)
	}

	peers, err := readBGPNodeConfigs(r.Context(), dynClient)
	if err != nil {
		h.Logger.Error("failed to read BGP node configs", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to read BGP status from CRDs", "")
		return
	}

	httputil.WriteData(w, CiliumBGPResponse{
		Configured: true,
		Config: &BGPConfig{
			ClusterConfigs: clusterConfigs,
			PeerConfigs:    peerConfigs,
		},
		Peers: peers,
	})
}

// HandleCiliumIPAM returns IPAM allocation data from CiliumNode CRDs.
// GET /api/v1/networking/cilium/ipam
func (h *Handler) HandleCiliumIPAM(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	if !h.isCiliumLocal(r) {
		httputil.WriteData(w, CiliumIPAMResponse{Configured: false})
		return
	}

	disc := h.K8sClient.DiscoveryClient()
	if !hasCRD(disc, ciliumNodeGVR) {
		httputil.WriteData(w, CiliumIPAMResponse{Configured: false})
		return
	}

	dynClient := h.K8sClient.BaseDynamicClient()
	nodes, err := readCiliumNodes(r.Context(), dynClient)
	if err != nil {
		h.Logger.Error("failed to read CiliumNode CRDs", "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "failed to read IPAM data from CRDs", "")
		return
	}

	var totalAllocated, totalAvailable int
	var allCIDRs []string
	var perNode []NodeIPAM

	for _, n := range nodes {
		totalAllocated += n.UsedCount
		totalAvailable += n.PoolCount
		allCIDRs = append(allCIDRs, n.PodCIDRs...)

		// Per-node: allocated = used, available = pool - used
		nodeAvail := n.PoolCount - n.UsedCount
		if nodeAvail < 0 {
			nodeAvail = 0
		}
		cidr := ""
		if len(n.PodCIDRs) > 0 {
			cidr = n.PodCIDRs[0]
		}
		perNode = append(perNode, NodeIPAM{
			Node:      n.Name,
			Allocated: n.UsedCount,
			Available: nodeAvail,
			PodCIDR:   cidr,
		})
	}

	total := totalAllocated + totalAvailable

	// Determine IPAM mode from ConfigMap
	mode := "unknown"
	info := h.Detector.CachedInfo()
	if info != nil {
		// Try to read mode from ConfigMap — fall back to cluster-pool as default
		mode = "cluster-pool"
	}

	httputil.WriteData(w, CiliumIPAMResponse{
		Configured:     true,
		Mode:           mode,
		PodCIDRs:       allCIDRs,
		Allocated:      totalAllocated,
		Available:      totalAvailable,
		Total:          total,
		ExhaustionRisk: computeExhaustionRisk(totalAllocated, total),
		PerNode:        perNode,
	})
}

// HandleCiliumSubsystems returns encryption, mesh, ClusterMesh, and endpoint health status.
// GET /api/v1/networking/cilium/subsystems
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
	if h.subsystemCache != nil && time.Since(h.subsystemCache.fetchedAt) < subsystemCacheTTL {
		data := h.subsystemCache.data
		h.subsystemMu.RUnlock()
		httputil.WriteData(w, data)
		return
	}
	h.subsystemMu.RUnlock()

	// Singleflight to coalesce concurrent requests
	result, err, _ := h.subsystemGroup.Do("subsystems", func() (any, error) {
		return h.fetchSubsystems(r.Context())
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
	info := h.Detector.CachedInfo()

	// Encryption from ConfigMap + CiliumNode CRDs
	encInfo := &EncryptionInfo{
		Enabled: info != nil && info.Features.Encryption,
		Mode:    "",
	}
	if encInfo.Enabled && info != nil {
		encInfo.Mode = info.Features.EncryptionMode
	}

	// Count encrypted nodes from CiliumNode CRDs
	dynClient := h.K8sClient.BaseDynamicClient()
	nodes, err := readCiliumNodes(ctx, dynClient)
	if err == nil {
		encInfo.NodesTotal = len(nodes)
		for _, n := range nodes {
			if n.EncryptionKey > 0 {
				encInfo.NodesEncrypted++
			}
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

	// Endpoints from CiliumEndpoint CRDs
	endpoints, err := aggregateEndpoints(ctx, dynClient)
	if err != nil {
		h.Logger.Warn("failed to aggregate CiliumEndpoints", "error", err)
		// Non-fatal: return what we have
	}

	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  encInfo,
		Mesh:        meshInfo,
		ClusterMesh: clusterMeshInfo,
		Endpoints:   &endpoints,
	}

	// Cache the result
	h.subsystemMu.Lock()
	h.subsystemCache = &cachedSubsystems{data: resp, fetchedAt: time.Now()}
	h.subsystemMu.Unlock()

	return resp, nil
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
