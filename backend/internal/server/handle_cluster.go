package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	"github.com/kubecenter/kubecenter/pkg/version"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	clusterInfoTimeout   = 10 * time.Second
	clusterInfoPageSize  = 500
	clusterInfoMaxPages  = 10
	clusterInfoReachMsg  = "failed to reach the selected cluster"
	clusterInfoNodesMsg  = "failed to list nodes on the selected cluster"
	clusterInfoNoSession = "authentication required"
)

// clusterInfoRemoteClient overrides remote client resolution in tests (a real
// resolution needs a cluster registry and a reachable API server). It is nil
// in production, which always routes through ClusterRouter. Tests that set it
// must not run in parallel.
var clusterInfoRemoteClient func(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error)

var errNoClusterRouter = errors.New("no cluster router wired")

// remoteClientForInfo resolves the impersonating clientset for a remote
// cluster. ClusterRouter has no local fallback on failure.
func (s *Server) remoteClientForInfo(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
	if clusterInfoRemoteClient != nil {
		return clusterInfoRemoteClient(ctx, clusterID, user)
	}
	if s.ClusterRouter == nil {
		return nil, errNoClusterRouter
	}
	return s.ClusterRouter.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
}

// handleClusterInfo returns basic information about the selected cluster. The
// local cluster is answered from the service account and the informer cache;
// a remote cluster is answered as the requesting user through the cluster
// router, with no fallback to local data on any failure.
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.Response{
			Error: &api.APIError{Code: 401, Message: clusterInfoNoSession},
		})
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	if !k8s.IsLocalClusterID(clusterID) {
		s.handleRemoteClusterInfo(w, r, clusterID, user)
		return
	}

	// k8s-routing: local - deliberately the service account's own discovery.
	cs := s.K8sClient.BaseClientset()

	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to query cluster info"},
		})
		s.Logger.Error("failed to get server version", "error", err)
		return
	}

	nodes, err := s.Informers.Factory().Core().V1().Nodes().Lister().List(labels.Everything())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Response{
			Error: &api.APIError{Code: 500, Message: "failed to list nodes"},
		})
		s.Logger.Error("failed to list nodes from informer", "error", err)
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"clusterID":         s.Config.ClusterID,
			"kubernetesVersion": serverVersion.GitVersion,
			"platform":          serverVersion.Platform,
			"nodeCount":         len(nodes),
			"kubecenter":        version.Get(),
		},
	})
}

func (s *Server) handleRemoteClusterInfo(w http.ResponseWriter, r *http.Request, clusterID string, user *auth.User) {
	ctx, cancel := context.WithTimeout(r.Context(), clusterInfoTimeout)
	defer cancel()
	id := k8s.NormalizedClusterID(clusterID)

	cs, err := s.remoteClientForInfo(ctx, id, user)
	if err != nil {
		s.Logger.Warn("cluster info: resolving remote client failed", "cluster", id, "error", err)
		writeClusterInfoError(w, clusterInfoReachMsg)
		return
	}
	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		s.Logger.Warn("cluster info: remote server version failed", "cluster", id, "error", err)
		writeClusterInfoError(w, clusterInfoReachMsg)
		return
	}

	// nodeCount stays nil (JSON null) unless the user was proven allowed to
	// list nodes and the whole list was read: the body never carries a count
	// it did not observe.
	var nodeCount any
	allowed := false
	if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
		allowed, err = s.ResourceHandler.AccessChecker.CanAccess(ctx, id, user.KubernetesUsername, user.KubernetesGroups, "list", "nodes", "")
		if err != nil {
			s.Logger.Warn("cluster info: node access check failed", "cluster", id, "error", err)
			allowed = false
		}
	}
	if allowed {
		count, complete, err := countRemoteNodes(ctx, cs)
		switch {
		case err != nil && apierrors.IsForbidden(err):
			// Denied by the cluster itself: unknown, not zero.
		case err != nil:
			s.Logger.Warn("cluster info: remote node list failed", "cluster", id, "error", err)
			writeClusterInfoError(w, clusterInfoNodesMsg)
			return
		case complete:
			nodeCount = count
		}
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"clusterID":         id,
			"kubernetesVersion": serverVersion.GitVersion,
			"platform":          serverVersion.Platform,
			"nodeCount":         nodeCount,
			"kubecenter":        version.Get(),
		},
	})
}

// countRemoteNodes pages through nodes. complete is false when the page cap
// was reached with more left to read, so a partial count is never reported.
func countRemoteNodes(ctx context.Context, cs kubernetes.Interface) (count int, complete bool, err error) {
	opts := metav1.ListOptions{Limit: clusterInfoPageSize}
	for page := 0; page < clusterInfoMaxPages; page++ {
		list, err := cs.CoreV1().Nodes().List(ctx, opts)
		if err != nil {
			return 0, false, err
		}
		count += len(list.Items)
		if list.Continue == "" {
			return count, true, nil
		}
		opts.Continue = list.Continue
	}
	return count, false, nil
}

func writeClusterInfoError(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadGateway, api.Response{
		Error: &api.APIError{Code: http.StatusBadGateway, Message: message},
	})
}
