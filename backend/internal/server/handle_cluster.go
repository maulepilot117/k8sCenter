package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	"github.com/kubecenter/kubecenter/pkg/version"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	clusterInfoTimeout   = 10 * time.Second
	clusterInfoReachMsg  = "failed to reach the selected cluster"
	clusterInfoNodesMsg  = "failed to list nodes on the selected cluster"
	clusterInfoNoSession = "authentication required"
)

// remoteClientForInfo resolves the impersonating clientset for a remote
// cluster. ClusterRouter has no local fallback on failure.
func (s *Server) remoteClientForInfo(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
	if s.remoteInfoClient != nil {
		return s.remoteInfoClient(ctx, clusterID, user)
	}
	if s.ClusterRouter == nil {
		return nil, errors.New("no cluster router wired")
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
	// The version read and the node count run at once. A version failure
	// cancels the node read and is reported first, as it was when the two
	// ran in sequence; a node failure never cancels the version read.
	var gitVersion, platform string
	g, gctx := errgroup.WithContext(ctx)
	recoverutil.Go(g, s.Logger, "cluster info remote server version", func() error {
		v, err := cs.Discovery().ServerVersion()
		if err != nil {
			return err
		}
		gitVersion, platform = v.GitVersion, v.Platform
		return nil
	})
	nodeCount, nodeErr := s.remoteNodeCount(gctx, id, user, cs)
	if err := g.Wait(); err != nil {
		s.Logger.Warn("cluster info: remote server version failed", "cluster", id, "error", err)
		writeClusterInfoError(w, clusterInfoReachMsg)
		return
	}
	if nodeErr != nil {
		s.Logger.Warn("cluster info: remote node list failed", "cluster", id, "error", nodeErr)
		writeClusterInfoError(w, clusterInfoNodesMsg)
		return
	}

	writeJSON(w, http.StatusOK, api.Response{
		Data: map[string]any{
			"clusterID":         id,
			"kubernetesVersion": gitVersion,
			"platform":          platform,
			"nodeCount":         nodeCount,
			"kubecenter":        version.Get(),
		},
	})
}

// remoteNodeCount counts the nodes on a remote cluster as the user. The
// count stays nil (JSON null) unless the user was proven allowed to list
// nodes and the whole list was read: the body never carries a count it did
// not observe. err is set only for a list failure other than the cluster's
// own refusal, which is unknown rather than zero.
func (s *Server) remoteNodeCount(ctx context.Context, id string, user *auth.User, cs kubernetes.Interface) (any, error) {
	allowed := false
	if s.ResourceHandler != nil && s.ResourceHandler.AccessChecker != nil {
		var err error
		allowed, err = s.ResourceHandler.AccessChecker.CanAccess(ctx, id, user.KubernetesUsername, user.KubernetesGroups, "list", "nodes", "")
		if err != nil {
			s.Logger.Warn("cluster info: node access check failed", "cluster", id, "error", err)
			allowed = false
		}
	}
	if !allowed {
		return nil, nil
	}
	nodes, truncated, err := k8s.PageList(ctx, metav1.ListOptions{}, func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Node, string, error) {
		list, err := cs.CoreV1().Nodes().List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Continue, nil
	})
	switch {
	case err != nil && apierrors.IsForbidden(err):
		return nil, nil
	case err != nil:
		return nil, err
	case truncated:
		return nil, nil
	}
	return len(nodes), nil
}

func writeClusterInfoError(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadGateway, api.Response{
		Error: &api.APIError{Code: http.StatusBadGateway, Message: message},
	})
}
