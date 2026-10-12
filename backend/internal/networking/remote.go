package networking

// Remote-cluster reads for the networking views (#608 slice 5). A remote
// cluster has no informers and k8sCenter's service account has no standing
// there, so every read is made live as the requesting user and held briefly
// in a per-(cluster, identity) cache. Nothing here ever falls back to the
// local cluster or the local Detector.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// remoteReadTimeout bounds one remote detection, every read included.
const remoteReadTimeout = 10 * time.Second

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// remoteCaches holds the per-(cluster, identity) caches of remote reads.
type remoteCaches struct {
	once       sync.Once
	cni        *remotecache.Cache[*CNIInfo]
	bgp        *remotecache.Cache[*CiliumBGPResponse]
	ipam       *remotecache.Cache[*CiliumIPAMResponse]
	subsystems *remotecache.Cache[*CiliumSubsystemsResponse]
	// agents runs remote agent exec; nil when agent exec is disabled or no
	// remote stream transport is wired.
	agents *remoteAgentCollector
}

func (h *Handler) remote() *remoteCaches {
	h.remoteState.once.Do(func() {
		h.remoteState.cni = remotecache.New[*CNIInfo](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		h.remoteState.bgp = remotecache.New[*CiliumBGPResponse](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		h.remoteState.ipam = remotecache.New[*CiliumIPAMResponse](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		h.remoteState.subsystems = remotecache.New[*CiliumSubsystemsResponse](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		// Remote agent exec follows the local opt-in: it exists only when
		// the local collector does.
		if h.AgentCollector != nil && h.Streams != nil && h.Clients != nil {
			h.remoteState.agents = newRemoteAgentCollector(h.Clients, h.Streams, h.AuditLogger, h.Logger, h.AgentCollector.maxConcurrent)
		}
	})
	return &h.remoteState
}

// EvictRemoteCache drops every identity's cached networking reads for
// clusterID. Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	c := h.remote()
	c.cni.EvictCluster(clusterID)
	c.bgp.EvictCluster(clusterID)
	c.ipam.EvictCluster(clusterID)
	c.subsystems.EvictCluster(clusterID)
	if c.agents != nil {
		c.agents.EvictCluster(clusterID)
	}
}

// remoteCNI returns the request cluster's CNI as the user sees it. refresh
// drops the cluster's cached detection first; it never runs the local
// Detector. The cached value is shared: callers must not modify it.
func (h *Handler) remoteCNI(ctx context.Context, user *auth.User, refresh bool) (*CNIInfo, error) {
	if h.Clients == nil {
		return nil, errRemoteNotConfigured
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	cache := h.remote().cni
	if refresh {
		cache.EvictCluster(clusterID)
	}
	return cache.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*CNIInfo, error) {
		return h.detectRemote(ctx, clusterID, user)
	})
}

// errRemoteNotConfigured is a remote selection with no ClusterClients
// wired. It only happens on a wiring mistake.
var errRemoteNotConfigured = errors.New("remote networking is not configured")

// detectRemote detects a remote cluster's CNI as the user, with the same
// rules as the local Detector: a CNI agent DaemonSet in one of the CNI
// namespaces first, then CRD groups alone. An answer that could not be
// settled is an error, never "unknown": a refused DaemonSet list or Cilium
// config read is forbidden, an unreadable discovery is
// k8s.ErrDiscoveryUnavailable, and an unresolved cluster is a
// k8s.TargetError.
func (h *Handler) detectRemote(ctx context.Context, clusterID string, user *auth.User) (*CNIInfo, error) {
	client, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()

	var info *CNIInfo
	var refused error
	for _, ns := range cniSearchNamespaces {
		list, err := client.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{Limit: k8s.RemoteListPageSize})
		if apierrors.IsForbidden(err) {
			// Another namespace may still hold the CNI; a refusal only
			// matters when none does.
			if refused == nil {
				refused = err
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list daemonsets in %s: %w", ns, err)
		}
		for i := range list.Items {
			if info = cniInfoFromDaemonSet(&list.Items[i]); info != nil {
				break
			}
		}
		if info != nil {
			break
		}
	}

	groups, groupsErr := h.remoteServerGroups(ctx, clusterID, user)

	if info == nil {
		if refused != nil {
			return nil, refused
		}
		if groupsErr != nil {
			return nil, groupsErr
		}
		for _, cni := range []string{CNICilium, CNICalico} {
			if hasAPIGroup(groups, cniCRDGroup(cni)) {
				return &CNIInfo{Name: cni, HasCRDs: true, DetectionMethod: "crd-only"}, nil
			}
		}
		return &CNIInfo{Name: CNIUnknown, DetectionMethod: "none"}, nil
	}

	if group := cniCRDGroup(info.Name); group != "" {
		if groupsErr != nil {
			// The CNI itself is known; only the CRD marker is not.
			h.Logger.Warn("remote CNI discovery failed; reporting no CRDs", "cluster", clusterID, "error", groupsErr)
		}
		info.HasCRDs = hasAPIGroup(groups, group)
		if info.HasCRDs {
			info.DetectionMethod = "daemonset+crd"
		}
	}
	if info.Name == CNICilium {
		cm, err := client.CoreV1().ConfigMaps(info.Namespace).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			// Cilium without its ConfigMap: no features to report.
		case err != nil:
			return nil, fmt.Errorf("get %s/%s: %w", info.Namespace, ciliumConfigMapName, err)
		default:
			info.Features = ciliumFeaturesFromConfig(cm.Data)
		}
	}
	return info, nil
}

// remoteServerGroups returns the remote cluster's API groups as the user
// sees them.
func (h *Handler) remoteServerGroups(ctx context.Context, clusterID string, user *auth.User) ([]metav1.APIGroup, error) {
	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	groups, err := target.Discovery.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", k8s.ErrDiscoveryUnavailable, err)
	}
	return groups.Groups, nil
}

// writeRemoteFailure answers a failed remote networking read with a fixed
// message naming what could not be read; the raw error, which can carry the
// remote API server's address or its own text, is only logged.
func (h *Handler) writeRemoteFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	if h.Logger != nil {
		h.Logger.Warn("remote networking read failed", "what", what, "cluster", middleware.ClusterIDFromContext(r.Context()), "error", err)
	}
	switch {
	case errors.Is(err, errRemoteNotConfigured):
		httputil.WriteError(w, http.StatusNotImplemented, "remote networking is not available", "")
	case apierrors.IsForbidden(err):
		httputil.WriteErrorWithReason(w, http.StatusForbidden,
			"you do not have permission to read the "+what+" on the selected cluster", string(k8s.ReasonForbidden), nil)
	default:
		httputil.WriteRemoteLoadError(w, err, what)
	}
}

// Reasons a Cilium config update can be refused or fail (#608 KTD5/KTD6).
const (
	reasonPinRequired        = "cluster_pin_required"
	reasonPinMismatch        = "cluster_pin_mismatch"
	reasonGenerationMismatch = "cluster_generation_mismatch"
	reasonConflict           = "conflict"
	reasonOutcomeUnknown     = "outcome_unknown"
)

func writePinMismatch(w http.ResponseWriter) {
	httputil.WriteErrorWithReason(w, http.StatusConflict,
		"the cluster this configuration was loaded from is not the cluster this request targets; reload and try again", reasonPinMismatch, nil)
}

func writeGenerationMismatch(w http.ResponseWriter) {
	httputil.WriteErrorWithReason(w, http.StatusConflict,
		"the cluster has been re-registered since this configuration was loaded; reload and try again", reasonGenerationMismatch, nil)
}

// remoteCiliumConfigMap reads cilium-config on a remote cluster from the
// namespace detection found the agent in. When detection named none (a
// CRD-only answer) the search is kube-system, then cilium, and only a
// NotFound moves it on: a refused or failed read stops the search, so a
// leftover cilium-config elsewhere is never shown or written in its place.
func remoteCiliumConfigMap(ctx context.Context, cs kubernetes.Interface, info *CNIInfo) (*corev1.ConfigMap, error) {
	namespaces := ciliumSearchNamespaces()
	if info.Namespace != "" {
		namespaces = []string{info.Namespace}
	}
	var err error
	for _, ns := range namespaces {
		var cm *corev1.ConfigMap
		cm, err = cs.CoreV1().ConfigMaps(ns).Get(ctx, ciliumConfigMapName, metav1.GetOptions{})
		if err == nil {
			return cm, nil
		}
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
	}
	return nil, err
}

func ciliumConfigFrom(cm *corev1.ConfigMap, target *k8s.TargetSchema) *CiliumConfig {
	return &CiliumConfig{
		CNIType:            CNICilium,
		ConfigSource:       "configmap",
		ConfigMapName:      ciliumConfigMapName,
		ConfigMapNamespace: cm.Namespace,
		Editable:           true,
		Config:             cm.Data,
		TargetCluster:      target.ClusterID,
		TargetGeneration:   target.Generation,
	}
}

// serveRemoteCNIConfig answers GET /networking/cni/config for a remote
// cluster: its cilium-config as the user sees it, with the pin an edit must
// echo back.
func (h *Handler) serveRemoteCNIConfig(w http.ResponseWriter, r *http.Request, user *auth.User) {
	const what = "Cilium configuration"
	ctx := r.Context()
	info, err := h.remoteCNI(ctx, user, false)
	if err != nil {
		h.writeRemoteFailure(w, r, what, err)
		return
	}
	if info.Name != CNICilium {
		writeConfigNotCilium(w, info.Name)
		return
	}
	clusterID := middleware.ClusterIDFromContext(ctx)
	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeRemoteFailure(w, r, what, k8s.TargetError{Err: err})
		return
	}
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.writeRemoteFailure(w, r, what, k8s.TargetError{Err: err})
		return
	}
	cm, err := remoteCiliumConfigMap(ctx, cs, info)
	if err != nil {
		h.writeRemoteFailure(w, r, what, err)
		return
	}
	httputil.WriteData(w, ciliumConfigFrom(cm, target))
}

// updateRemoteCNIConfig answers PUT /networking/cni/config for a remote
// cluster. The edit must carry the pin from the config read, and is
// refused, before anything is read or written, when the pin is missing or
// names another cluster or registration. Every attempt past validation is
// audited with the remote cluster's id.
func (h *Handler) updateRemoteCNIConfig(w http.ResponseWriter, r *http.Request, user *auth.User) {
	const what = "Cilium configuration"
	ctx := r.Context()
	clusterID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(ctx))

	req, ok := decodeConfigUpdate(w, r)
	if !ok {
		return
	}
	refuse := func(detail string) {
		h.auditConfigUpdate(r, user, clusterID, "", audit.ResultFailure, "refused: "+detail)
	}
	if h.Clients == nil {
		h.writeRemoteFailure(w, r, what, errRemoteNotConfigured)
		return
	}
	if req.TargetCluster == "" || req.TargetGeneration == "" {
		refuse(reasonPinRequired)
		httputil.WriteErrorWithReason(w, http.StatusPreconditionRequired,
			"a remote configuration edit must name the cluster it was loaded from; reload and try again", reasonPinRequired, nil)
		return
	}
	if k8s.NormalizedClusterID(req.TargetCluster) != clusterID {
		refuse(reasonPinMismatch)
		writePinMismatch(w)
		return
	}
	if !validateConfigUpdate(w, req) {
		return
	}
	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		refuse("cluster could not be resolved")
		h.writeRemoteFailure(w, r, what, k8s.TargetError{Err: err})
		return
	}
	if req.TargetGeneration != target.Generation {
		refuse(reasonGenerationMismatch)
		writeGenerationMismatch(w)
		return
	}
	info, err := h.remoteCNI(ctx, user, false)
	if err != nil {
		refuse("CNI could not be detected")
		h.writeRemoteFailure(w, r, what, err)
		return
	}
	if info.Name != CNICilium {
		httputil.WriteError(w, http.StatusBadRequest, "CNI configuration editing is only supported for Cilium", "")
		return
	}
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		refuse("cluster could not be resolved")
		h.writeRemoteFailure(w, r, what, k8s.TargetError{Err: err})
		return
	}
	cm, err := remoteCiliumConfigMap(ctx, cs, info)
	if err != nil {
		refuse("cilium-config could not be read")
		h.writeRemoteFailure(w, r, what, err)
		return
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	for k, v := range req.Changes {
		cm.Data[k] = v
	}
	detail := formatChangedKeys(req.Changes)
	updated, err := cs.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		h.writeRemoteUpdateFailure(w, r, user, clusterID, cm.Namespace, detail, err)
		return
	}
	// A remote cluster sends no informer events: drop this cluster's cached
	// detection, which reflects the config before the change.
	h.EvictRemoteCache(clusterID)
	h.auditConfigUpdate(r, user, clusterID, cm.Namespace, audit.ResultSuccess, detail)
	h.Logger.Info("remote cilium config updated", "user", user.Username, "cluster", clusterID, "changedKeys", len(req.Changes))
	httputil.WriteData(w, ciliumConfigFrom(updated, target))
}

// writeRemoteUpdateFailure audits and answers a failed cilium-config
// update. An API answer is classified; a transport failure leaves the
// outcome unknown, since the write may have landed.
func (h *Handler) writeRemoteUpdateFailure(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID, namespace, detail string, err error) {
	h.Logger.Warn("remote cilium config update failed", "cluster", clusterID, "error", err)
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		h.auditConfigUpdate(r, user, clusterID, namespace, audit.ResultFailure, detail+"; outcome unknown")
		httputil.WriteErrorWithReason(w, http.StatusBadGateway,
			"the selected cluster stopped answering during the update; reload to check whether the change was applied", reasonOutcomeUnknown, nil)
		return
	}
	h.auditConfigUpdate(r, user, clusterID, namespace, audit.ResultFailure, detail)
	switch {
	case apierrors.IsConflict(err):
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"the Cilium configuration changed on the cluster; reload and try again", reasonConflict, nil)
	case apierrors.IsForbidden(err):
		httputil.WriteErrorWithReason(w, http.StatusForbidden,
			"you do not have permission to update the Cilium configuration on the selected cluster", string(k8s.ReasonForbidden), nil)
	default:
		httputil.WriteRemoteError(w, err)
	}
}

// agentsSource is the coverage source for remote agent exec data.
const agentsSource = "cilium-agents"

// reasonAgentDataUnavailable marks agent exec data a remote subsystems
// response cannot include because no remote stream transport is wired.
const reasonAgentDataUnavailable = "agent_data_unavailable"

// agentNamespace is where to look for a remote cluster's Cilium agent pods:
// where detection found the agent, else kube-system, Cilium's default.
func agentNamespace(info *CNIInfo) string {
	if info.Namespace != "" {
		return info.Namespace
	}
	return ciliumSearchNamespaces()[0]
}

// serveRemoteConnectivity answers GET /networking/cilium/connectivity for a
// remote cluster by exec into its agent pods as the user (KTD7), under the
// same opt-in as local. A user who may not exec into the agents gets one
// "not permitted" answer, not a failed row per node.
func (h *Handler) serveRemoteConnectivity(w http.ResponseWriter, r *http.Request, user *auth.User) {
	const what = "Cilium agent connectivity"
	if h.Clients == nil {
		h.writeRemoteFailure(w, r, what, errRemoteNotConfigured)
		return
	}
	ctx := r.Context()
	info, err := h.remoteCNI(ctx, user, false)
	if err != nil {
		h.writeRemoteFailure(w, r, what, err)
		return
	}
	if info.Name != CNICilium {
		httputil.WriteData(w, CiliumConnectivityResponse{Configured: false, Nodes: []NodeConnectivity{}})
		return
	}
	if h.AgentCollector == nil {
		httputil.WriteData(w, CiliumConnectivityResponse{Configured: true, Nodes: []NodeConnectivity{}})
		return
	}
	agents := h.remote().agents
	if agents == nil {
		h.writeRemoteFailure(w, r, what, errRemoteNotConfigured)
		return
	}
	res, err := agents.Collect(ctx, middleware.ClusterIDFromContext(ctx), user, agentNamespace(info))
	switch {
	case err == nil:
		httputil.WriteData(w, connectivityResponseFor(res))
	case apierrors.IsForbidden(err):
		h.Logger.Warn("remote agent exec refused", "cluster", middleware.ClusterIDFromContext(ctx), "error", err)
		httputil.WriteErrorWithReason(w, http.StatusForbidden,
			"you do not have permission to exec into Cilium agents on the selected cluster", string(k8s.ReasonForbidden), nil)
	case errors.Is(err, errNoAgentPods):
		httputil.WriteError(w, http.StatusBadGateway, "no running Cilium agent pods were found on the selected cluster", "")
	default:
		h.writeRemoteFailure(w, r, what, err)
	}
}

// remoteServes reports whether a remote cluster serves gvr at its exact
// version, as the user sees it. Only a NotFound for the group version is a
// definite absence; any other discovery failure leaves the answer unknown
// (k8s.ErrDiscoveryUnavailable).
func (h *Handler) remoteServes(ctx context.Context, clusterID string, user *auth.User, gvr schema.GroupVersionResource) (bool, error) {
	target, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return false, k8s.TargetError{Err: err}
	}
	list, err := target.Discovery.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %w", k8s.ErrDiscoveryUnavailable, err)
	}
	return k8s.ServesGVR([]*metav1.APIResourceList{list}, gvr), nil
}

// remoteCiliumDynamic resolves the dynamic client for a remote cluster known
// to run Cilium. ok is false when detection definitely found another CNI;
// the caller then answers "not configured".
func (h *Handler) remoteCiliumDynamic(ctx context.Context, clusterID string, user *auth.User) (info *CNIInfo, dyn dynamic.Interface, ok bool, err error) {
	info, err = h.remoteCNI(ctx, user, false)
	if err != nil || info.Name != CNICilium {
		return info, nil, false, err
	}
	dyn, err = h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return info, nil, false, k8s.TargetError{Err: err}
	}
	return info, dyn, true, nil
}

// serveRemoteCached answers a remote Cilium read from the selected cluster's
// per-identity cache, fetching it on a miss. The cache is selected only once
// remote clients are known to be wired, as each read did before it moved here.
func serveRemoteCached[T any](
	h *Handler, w http.ResponseWriter, r *http.Request, user *auth.User, what string,
	cacheOf func(*remoteCaches) *remotecache.Cache[T],
	fetch func(ctx context.Context, clusterID string, user *auth.User) (T, error),
) {
	if h.Clients == nil {
		h.writeRemoteFailure(w, r, what, errRemoteNotConfigured)
		return
	}
	clusterID := middleware.ClusterIDFromContext(r.Context())
	resp, err := cacheOf(h.remote()).Get(r.Context(), clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (T, error) {
		return fetch(ctx, clusterID, user)
	})
	if err != nil {
		h.writeRemoteFailure(w, r, what, err)
		return
	}
	httputil.WriteData(w, resp)
}

// serveRemoteBGP answers GET /networking/cilium/bgp for a remote cluster.
func (h *Handler) serveRemoteBGP(w http.ResponseWriter, r *http.Request, user *auth.User) {
	serveRemoteCached(h, w, r, user, "BGP status", func(c *remoteCaches) *remotecache.Cache[*CiliumBGPResponse] { return c.bgp }, h.fetchRemoteBGP)
}

func (h *Handler) fetchRemoteBGP(ctx context.Context, clusterID string, user *auth.User) (*CiliumBGPResponse, error) {
	_, dyn, ok, err := h.remoteCiliumDynamic(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &CiliumBGPResponse{Configured: false}, nil
	}
	// BGP is read at one fixed CRD version, so presence is checked at it.
	serves, err := h.remoteServes(ctx, clusterID, user, bgpNodeConfigGVR)
	if err != nil {
		return nil, err
	}
	if !serves {
		return &CiliumBGPResponse{Configured: false}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()
	peers, err := readBGPNodeConfigs(ctx, dyn)
	if err != nil {
		return nil, err
	}
	if peers == nil {
		peers = []BGPPeerStatus{}
	}
	return &CiliumBGPResponse{Configured: true, Peers: peers}, nil
}

// serveRemoteIPAM answers GET /networking/cilium/ipam for a remote cluster.
// The IPAM mode comes from that cluster's cilium-config.
func (h *Handler) serveRemoteIPAM(w http.ResponseWriter, r *http.Request, user *auth.User) {
	serveRemoteCached(h, w, r, user, "IPAM data", func(c *remoteCaches) *remotecache.Cache[*CiliumIPAMResponse] { return c.ipam }, h.fetchRemoteIPAM)
}

func (h *Handler) fetchRemoteIPAM(ctx context.Context, clusterID string, user *auth.User) (*CiliumIPAMResponse, error) {
	info, dyn, ok, err := h.remoteCiliumDynamic(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &CiliumIPAMResponse{Configured: false}, nil
	}
	serves, err := h.remoteServes(ctx, clusterID, user, ciliumNodeGVR)
	if err != nil {
		return nil, err
	}
	if !serves {
		return &CiliumIPAMResponse{Configured: false}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()
	nodes, err := readCiliumNodes(ctx, dyn)
	if err != nil {
		return nil, err
	}
	return ipamResponseFor(nodes, info.Features.IPAMMode), nil
}

// serveRemoteSubsystems answers GET /networking/cilium/subsystems for a
// remote cluster. Encryption, mesh and ClusterMesh settings come from its
// cilium-config; a CiliumNode or CiliumEndpoint list the cluster refuses or
// fails is reported as coverage, never as zero counts.
func (h *Handler) serveRemoteSubsystems(w http.ResponseWriter, r *http.Request, user *auth.User) {
	serveRemoteCached(h, w, r, user, "Cilium subsystem status", func(c *remoteCaches) *remotecache.Cache[*CiliumSubsystemsResponse] { return c.subsystems }, h.fetchRemoteSubsystems)
}

func (h *Handler) fetchRemoteSubsystems(ctx context.Context, clusterID string, user *auth.User) (*CiliumSubsystemsResponse, error) {
	info, dyn, ok, err := h.remoteCiliumDynamic(ctx, clusterID, user)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &CiliumSubsystemsResponse{Configured: false}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, remoteReadTimeout)
	defer cancel()

	const nodesSource, endpointsSource = "ciliumnodes", "ciliumendpoints"
	var nodes []ciliumNodeIPAM
	var endpoints EndpointCounts
	var agentResult *agentCollectionResult
	lists := []k8s.NamedList{
		{Label: "networking remote list ciliumnodes", Run: func() (err error) {
			nodes, err = readCiliumNodes(ctx, dyn)
			return err
		}},
		{Label: "networking remote list ciliumendpoints", Run: func() (err error) {
			endpoints, err = aggregateEndpoints(ctx, dyn)
			return err
		}},
	}
	agents := h.remote().agents
	if agents != nil {
		lists = append(lists, k8s.NamedList{Label: "networking remote agent collect", Run: func() (err error) {
			agentResult, err = agents.Collect(ctx, clusterID, user, agentNamespace(info))
			return err
		}})
	}
	errs := k8s.RunLists(h.Logger, lists)
	failed := map[string]error{nodesSource: errs[0], endpointsSource: errs[1]}

	engine := meshEngineFor(info.Features)
	resp := &CiliumSubsystemsResponse{
		Configured:  true,
		Encryption:  encryptionInfoFor(info.Features, nodes),
		Mesh:        &MeshInfo{Enabled: engine != "none", Engine: engine},
		ClusterMesh: &ClusterMeshInfo{Enabled: info.Features.ClusterMesh},
		Coverage:    k8s.CoverageOf(failed, nodesSource, endpointsSource),
	}
	if errs[1] == nil {
		resp.Endpoints = &endpoints
	}
	switch {
	case agents != nil && errs[2] == nil:
		mergeAgentIntoSubsystems(resp, agentResult)
	case agents != nil:
		resp.Coverage = append(resp.Coverage, k8s.CoverageOf(map[string]error{agentsSource: errs[2]}, agentsSource)...)
	case h.AgentCollector != nil:
		// Agent exec is enabled but no remote stream transport is wired.
		resp.Coverage = append(resp.Coverage, k8s.SourceCoverage{Source: agentsSource, Status: "unavailable", ReasonCode: reasonAgentDataUnavailable})
	}
	return resp, nil
}
