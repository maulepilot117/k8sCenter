package topology

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// reasonOverlayUnsupportedRemote and overlayUnsupportedRemoteMessage answer
// an overlay request under a remote cluster selection: the mesh and ESO
// overlays read local-cluster inventories (MeshRouteProvider,
// ESOChainProvider), so they cannot describe a remote cluster.
const (
	reasonOverlayUnsupportedRemote  = "overlay_unsupported_remote"
	overlayUnsupportedRemoteMessage = "topology overlays are available for the local cluster only"
)

// Handler serves topology HTTP endpoints.
type Handler struct {
	Builder       *Builder
	AccessChecker *resources.AccessChecker
	Clients       k8s.ClusterClients
	Logger        *slog.Logger
}

// HandleNamespaceGraph returns the full resource dependency graph for a namespace.
// GET /api/v1/topology/{namespace}
//
// Optional query parameter:
//
//	?overlay=mesh — adds service-to-service mesh edges (Istio VirtualService
//	  and Linkerd ServiceProfile routing) when the caller has list
//	  permission on the underlying CRDs. Without this parameter the response
//	  is byte-identical to the no-overlay path.
//
// On a remote cluster the graph is built from direct, bounded reads of that
// cluster as the user (RemoteLister, within RemoteReadTimeout), never from the
// local informers. The builder's per-kind RBAC gate applies unchanged; a kind
// that is forbidden, exceeds the read cap or runs out of time is left out, the
// latter two marked in Truncated and Errors. A permission check or list that
// fails any other way fails the request through httputil.WriteRemoteError, so
// an unreachable cluster never reads as an empty namespace. Overlays read
// local-cluster inventories, so ?overlay= with a known overlay on a remote
// cluster is a 400 with reason overlay_unsupported_remote.
func (h *Handler) HandleNamespaceGraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := chi.URLParam(r, "namespace")

	user, ok := auth.UserFromContext(ctx)
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	overlay := r.URL.Query().Get("overlay")
	builder := h.Builder

	clusterID := middleware.ClusterIDFromContext(ctx)
	remote := !k8s.IsLocalClusterID(clusterID)
	if remote {
		// Every overlay is answered before the cluster is contacted: an
		// unknown value gets the same 400 the builder gives locally, without
		// a dozen remote lists first.
		switch overlay {
		case "":
		case string(OverlayMesh), string(OverlayESOChain):
			httputil.WriteErrorWithReason(w, http.StatusBadRequest, overlayUnsupportedRemoteMessage,
				reasonOverlayUnsupportedRemote, nil)
			return
		default:
			httputil.WriteError(w, http.StatusBadRequest, "unsupported overlay value", overlay)
			return
		}
		var cancel context.CancelFunc
		ctx, cancel = WithRemoteTimeout(ctx, clusterID)
		defer cancel()
		lister, err := NewRemoteListerFor(ctx, h.Clients, clusterID, user, h.Logger)
		if errors.Is(err, ErrNoClusterClients) {
			h.Logger.Error("topology: remote cluster requested but no cluster clients are wired", "clusterID", clusterID)
			httputil.WriteError(w, http.StatusInternalServerError, "topology is not configured", "")
			return
		}
		if err != nil {
			httputil.WriteTargetError(w, err)
			return
		}
		builder = NewBuilder(lister, nil, h.Logger)
	}

	graph, err := builder.BuildNamespaceGraphWithOverlay(ctx, namespace, user, h.AccessChecker, overlay)
	if err != nil {
		// Validation errors (unsupported overlay value) surface as 400
		// with a stable user-message and the offending value in detail —
		// matching the envelope shape used by /mesh/* peers. A remote
		// cluster's failure is answered with a fixed message (the raw error
		// can carry its API server's address); everything else is a 500.
		if errors.Is(err, ErrUnsupportedOverlay) {
			httputil.WriteError(w, http.StatusBadRequest, "unsupported overlay value", overlay)
			return
		}
		if remote {
			h.Logger.Error("failed to build remote namespace graph", "clusterID", clusterID, "namespace", namespace, "error", err)
			httputil.WriteRemoteError(w, err)
			return
		}
		h.Logger.Error("failed to build namespace graph", "namespace", namespace, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to build topology graph", "")
		return
	}

	httputil.WriteData(w, graph)
}
