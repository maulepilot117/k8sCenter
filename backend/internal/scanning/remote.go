package scanning

// Cluster routing for the scanning routes (#608). The local cluster's
// scanner presence comes from the Discoverer; a remote cluster's comes from
// its own discovery, read as the requesting user through Presence. No
// namespace probe runs on a remote cluster, so a remote status never names
// the scanner's namespace.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// remoteFeature names the feature in a remote discovery failure.
const remoteFeature = "security scanner"

// errTrivyAbsent means the request's cluster does not serve Trivy's
// VulnerabilityReport CRD, which CVE-level detail needs.
var errTrivyAbsent = errors.New("trivy vulnerability reports are not served on the cluster")

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// configured reports whether the handler can read a cluster, writing a 500
// when it cannot. Production always wires Clients and Presence.
func (h *Handler) configured(w http.ResponseWriter) bool {
	if h.Clients != nil && h.Presence != nil {
		return true
	}
	h.Logger.Error("scanning handler has no cluster clients wired")
	httputil.WriteError(w, http.StatusInternalServerError, "scanning is not configured", "")
	return false
}

// scannersPresent narrows ask to the scanners installed on the request's
// cluster. For a remote cluster an unknown answer is an error.
func (h *Handler) scannersPresent(ctx context.Context, user *auth.User, ask scannerSet) (scannerSet, error) {
	if isLocal(ctx) {
		status := h.Discoverer.Status()
		return scannerSet{
			trivy:     ask.trivy && status.Trivy != nil && status.Trivy.Available,
			kubescape: ask.kubescape && status.Kubescape != nil && status.Kubescape.Available,
		}, nil
	}
	return h.remoteScanners(ctx, middleware.ClusterIDFromContext(ctx), user, ask)
}

// remoteScanners asks a remote cluster's discovery, as the user, which of
// the scanners in ask it serves.
func (h *Handler) remoteScanners(ctx context.Context, clusterID string, user *auth.User, ask scannerSet) (scannerSet, error) {
	var present scannerSet
	var err error
	if ask.trivy {
		if present.trivy, err = h.remoteInstalled(ctx, clusterID, user, trivyVulnReportGVR.GroupResource()); err != nil {
			return scannerSet{}, err
		}
	}
	if ask.kubescape {
		if present.kubescape, err = h.remoteInstalled(ctx, clusterID, user, kubescapeVulnSummaryGVR.GroupResource()); err != nil {
			return scannerSet{}, err
		}
	}
	return present, nil
}

// remoteInstalled reports whether a remote cluster serves gr as the user
// sees it. When that cannot be told the error says why: a k8s.TargetError
// when the cluster could not be resolved, k8s.ErrDiscoveryUnavailable
// otherwise.
func (h *Handler) remoteInstalled(ctx context.Context, clusterID string, user *auth.User, gr schema.GroupResource) (bool, error) {
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gr)
	if verdict.Installed != nil {
		return *verdict.Installed, nil
	}
	// The verdict carries only a reason; resolve the target again for the
	// error the response is built from.
	if _, err := h.Clients.TargetSchemaFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups); err != nil {
		return false, k8s.TargetError{Err: err}
	}
	return false, k8s.ErrDiscoveryUnavailable
}

// remoteStatus builds the status of a remote cluster from its discovery.
func (h *Handler) remoteStatus(ctx context.Context, user *auth.User) (ScannerStatus, error) {
	present, err := h.remoteScanners(ctx, middleware.ClusterIDFromContext(ctx), user, scannerSet{trivy: true, kubescape: true})
	if err != nil {
		return ScannerStatus{}, err
	}
	status := ScannerStatus{Detected: ScannerNone, LastChecked: time.Now().UTC().Format(time.RFC3339)}
	if present.trivy {
		status.Trivy = &ScannerDetail{Available: true}
	}
	if present.kubescape {
		status.Kubescape = &ScannerDetail{Available: true}
	}
	switch {
	case present.trivy && present.kubescape:
		status.Detected = ScannerBoth
	case present.trivy:
		status.Detected = ScannerTrivy
	case present.kubescape:
		status.Detected = ScannerKubescape
	}
	return status, nil
}

// scannerGone reports whether err from reading gvr means the scanner's CRD
// is no longer served, so the scanner should be treated as absent. On a
// remote cluster presence is re-read and only a confirmed removal counts;
// the local Discoverer catches up on its next pass.
func (h *Handler) scannerGone(ctx context.Context, clusterID string, user *auth.User, gvr schema.GroupVersionResource, err error) bool {
	if !k8s.IsResourceGone(err) {
		return false
	}
	if k8s.IsLocalClusterID(clusterID) {
		return true
	}
	v := h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, gvr.GroupResource())
	return v.Installed != nil && !*v.Installed
}

// writeReadError answers a failed vulnerability read. The local cluster
// keeps its historical 500 and message; a remote failure is classified
// without relaying the cluster's error text.
func (h *Handler) writeReadError(w http.ResponseWriter, r *http.Request, err error, localMsg string) {
	if isLocal(r.Context()) {
		httputil.WriteError(w, http.StatusInternalServerError, localMsg, "")
		return
	}
	httputil.WriteRemoteLoadError(w, err, remoteFeature)
}
