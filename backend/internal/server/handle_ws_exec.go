package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// execWSRoute is the in-band exec route: it must name its cluster, and only
// the local cluster runs a shell until remote exec ships.
var execWSRoute = wsRoute{requireCluster: true, auditAction: audit.ActionCreate, auditKind: "Pod/exec"}

// wsExecHandler serves WS /api/v1/ws/exec/{namespace}/{name}/{container}.
// Every request authenticates in-band; an Authorization header is ignored.
func (s *Server) wsExecHandler() http.Handler {
	return middleware.WSClusterContext(http.HandlerFunc(s.handleWSExec))
}

// handleWSExec opens a pod shell after in-band auth. Protocol: the client
// sends an auth frame naming its cluster, then input and resize frames.
func (s *Server) handleWSExec(w http.ResponseWriter, r *http.Request) {
	if resources.ExecAtCapacity() {
		http.Error(w, "too many exec connections", http.StatusServiceUnavailable)
		return
	}

	conn, user, reqCtx := s.wsAuthAndUpgradeFor(w, r, execWSRoute)
	if conn == nil {
		return
	}
	defer conn.Close()

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	container := chi.URLParam(r, "container")
	if ns == "" || name == "" || !resources.ValidateK8sName(ns) || !resources.ValidateK8sName(name) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid namespace or pod name"})
		return
	}
	if container != "" && !resources.ValidContainerName(container) {
		conn.WriteJSON(map[string]any{"type": "error", "message": "invalid container name"})
		return
	}

	// refuse ends the session with a reasoned frame and a failure audit row
	// naming the pod.
	refuse := func(clusterID, reason, message string) {
		if s.AuditLogger != nil {
			entry := s.newAuditEntry(r, user.Username, audit.ActionCreate, audit.ResultFailure)
			entry.ClusterID = s.auditClusterID(clusterID)
			entry.ResourceKind = execWSRoute.auditKind
			entry.ResourceNamespace = ns
			entry.ResourceName = name
			entry.Detail = "ws exec refused: " + reason
			s.AuditLogger.Log(reqCtx, entry)
		}
		conn.WriteJSON(map[string]any{"type": "error", "message": message, "reason": reason})
	}

	// The helper refuses a remote in-band cluster; a remote X-Cluster-ID
	// header with no in-band cluster reaches here and is refused the same way.
	clusterID := middleware.ClusterIDFromContext(reqCtx)
	if !k8s.IsLocalClusterID(clusterID) {
		refuse(clusterID, string(k8s.ReasonUnsupportedPlatform), "pod exec is not available on remote clusters")
		return
	}

	allowed, err := s.ResourceHandler.AccessChecker.CanAccess(
		reqCtx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "create", "pods/exec", ns,
	)
	if err != nil {
		s.Logger.Error("exec access check failed", "error", err, "user", user.Username)
		conn.WriteJSON(map[string]any{"type": "error", "message": "failed to check permissions"})
		return
	}
	if !allowed {
		refuse(clusterID, string(k8s.ReasonForbidden), "you do not have permission to create pods/exec in namespace "+ns)
		return
	}

	s.ResourceHandler.RunExecSession(r.WithContext(reqCtx), conn, user, ns, name, container)
}
