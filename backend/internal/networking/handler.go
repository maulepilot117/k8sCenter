package networking

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// k8sNameRegexp validates RFC 1123 DNS label names.
var k8sNameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)

// Handler serves networking-related HTTP endpoints.
type Handler struct {
	K8sClient    *k8s.ClientFactory
	Detector     *Detector
	HubbleClient *HubbleClient
	AuditLogger  audit.Logger
	Logger       *slog.Logger
	ClusterID    string
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

	// Return updated config by reading from the known namespace (avoids redundant probing)
	config, err := ReadCiliumConfigFromNamespace(r.Context(), cs, updatedNS)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "config updated but failed to re-read", "")
		return
	}
	httputil.WriteData(w, config)

	// Refresh cached CNI features asynchronously (non-blocking)
	go h.Detector.Detect(context.Background())
}

// HandleHubbleFlows returns network flows from Hubble Relay filtered by namespace and verdict.
// GET /api/v1/networking/hubble/flows?namespace=default&verdict=DROPPED&limit=100
func (h *Handler) HandleHubbleFlows(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if h.HubbleClient == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "Hubble is not available", "")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace parameter is required", "")
		return
	}
	// Validate namespace against k8s naming rules
	if !k8sNameRegexp.MatchString(namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid namespace name", "")
		return
	}

	// Validate verdict filter
	verdict := r.URL.Query().Get("verdict")
	if verdict != "" && !ValidVerdict(verdict) {
		httputil.WriteError(w, http.StatusBadRequest,
			"invalid verdict filter, must be one of: FORWARDED, DROPPED, ERROR, AUDIT", "")
		return
	}

	// Validate limit
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil || v < 1 || v > 1000 {
			httputil.WriteError(w, http.StatusBadRequest, "limit must be between 1 and 1000", "")
			return
		}
		limit = v
	}

	// RBAC: check user can get pods in the requested namespace (flow visibility = pod observability)
	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to check permissions", "")
		return
	}
	_, err = cs.CoreV1().Pods(namespace).List(r.Context(), k8smetav1.ListOptions{Limit: 1})
	if err != nil {
		httputil.WriteError(w, http.StatusForbidden,
			"you do not have permission to view flows in namespace "+namespace, "")
		return
	}

	flows, err := h.HubbleClient.GetFlows(r.Context(), namespace, verdict, limit)
	if err != nil {
		h.Logger.Error("hubble flow query failed", "error", err, "namespace", namespace)
		httputil.WriteError(w, http.StatusBadGateway, "failed to query Hubble flows", "")
		return
	}

	httputil.WriteData(w, flows)
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
