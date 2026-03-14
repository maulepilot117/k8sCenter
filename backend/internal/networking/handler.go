package networking

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
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

	// Re-detect to refresh cached features
	h.Detector.Detect(r.Context())

	// Return updated config
	config, err := ReadCiliumConfig(r.Context(), cs)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "config updated but failed to re-read", "")
		return
	}
	httputil.WriteData(w, config)
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
