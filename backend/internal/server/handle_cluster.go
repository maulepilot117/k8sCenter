package server

import (
	"net/http"

	"github.com/kubecenter/kubecenter/pkg/api"
	"github.com/kubecenter/kubecenter/pkg/version"
	"k8s.io/apimachinery/pkg/labels"
)

// handleClusterInfo returns basic cluster information.
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
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
