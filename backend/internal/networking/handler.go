package networking

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// Handler serves networking-related HTTP endpoints.
type Handler struct {
	K8sClient   *k8s.ClientFactory
	Detector    *Detector
	AuditLogger audit.Logger
	Logger      *slog.Logger
	ClusterID   string
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
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	info := h.Detector.CachedInfo()
	if info == nil {
		info = h.Detector.Detect(r.Context())
	}

	switch info.Name {
	case CNICilium:
		config, err := ReadCiliumConfig(r.Context(), h.K8sClient)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, "failed to read Cilium config", err.Error())
			return
		}
		httputil.WriteData(w, config)

	case CNICalico:
		httputil.WriteData(w, map[string]any{
			"cniType":    CNICalico,
			"editable":   false,
			"message":    "Calico configuration editing is not yet supported",
			"detectedAt": info.DaemonSet,
		})

	case CNIFlannel:
		httputil.WriteData(w, map[string]any{
			"cniType":    CNIFlannel,
			"editable":   false,
			"message":    "Flannel configuration editing is not yet supported",
			"detectedAt": info.DaemonSet,
		})

	default:
		httputil.WriteData(w, map[string]any{
			"cniType":  CNIUnknown,
			"editable": false,
			"message":  "No supported CNI plugin detected",
		})
	}
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

	// Apply changes to cilium-config ConfigMap
	if err := UpdateCiliumConfig(r.Context(), h.K8sClient, req.Changes); err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp:         time.Now().UTC(),
			ClusterID:         h.ClusterID,
			User:              user.Username,
			SourceIP:          r.RemoteAddr,
			Action:            audit.ActionUpdate,
			ResourceKind:      "CiliumConfig",
			ResourceNamespace: "kube-system",
			ResourceName:      "cilium-config",
			Result:            audit.ResultFailure,
		})
		httputil.WriteError(w, http.StatusBadGateway, "failed to update Cilium config", err.Error())
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionUpdate,
		ResourceKind:      "CiliumConfig",
		ResourceNamespace: "kube-system",
		ResourceName:      "cilium-config",
		Result:            audit.ResultSuccess,
	})
	h.Logger.Info("cilium config updated", "user", user.Username, "changedKeys", len(req.Changes))

	// Re-detect to refresh cached features
	h.Detector.Detect(r.Context())

	// Return updated config
	config, err := ReadCiliumConfig(r.Context(), h.K8sClient)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "config updated but failed to re-read", err.Error())
		return
	}
	httputil.WriteData(w, config)
}
