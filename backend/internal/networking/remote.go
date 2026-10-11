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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	once sync.Once
	cni  *remotecache.Cache[*CNIInfo]
}

func (h *Handler) remote() *remoteCaches {
	h.remoteState.once.Do(func() {
		h.remoteState.cni = remotecache.New[*CNIInfo](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return &h.remoteState
}

// EvictRemoteCache drops every identity's cached networking reads for
// clusterID. Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remote().cni.EvictCluster(clusterID)
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
	schema, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	groups, err := schema.Discovery.ServerGroups()
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
