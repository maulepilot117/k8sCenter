package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// inFlightWindow is the heuristic window used by HandleForceSyncExternalSecret
// to detect "already refreshing." A successful sync within the last 30s is
// treated as an in-flight reconcile and returns 409 already_refreshing rather
// than re-triggering. ESO does not reliably set Ready=Unknown during
// reconcile (Ready=False/True flips happen at end of reconcile), so we lean
// on the most recent refreshTime instead. A sync that legitimately takes
// longer than 30s won't show as in-flight; documented operator caveat.
const inFlightWindow = 30 * time.Second

// forceSyncResult captures the audit detail JSON written for force-sync
// outcomes. Renders inline via the audit log viewer.
type forceSyncResult struct {
	RequestedBy string         `json:"requestedBy"`
	Target      forceSyncTgt   `json:"target"`
	Result      string         `json:"result"`
	Reason      string         `json:"reason,omitempty"`
}

type forceSyncTgt struct {
	Namespace string `json:"ns"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// errAlreadyRefreshing is returned by patchForceSync when the target ES has
// a refreshTime within inFlightWindow. Callers map this to HTTP 409.
var errAlreadyRefreshing = errors.New("already_refreshing")

// HandleForceSyncExternalSecret patches the ESO `force-sync` annotation on a
// single ExternalSecret via the impersonating dynamic client. ESO's reconciler
// observes the new annotation value and triggers an immediate sync.
//
// Strategic-merge patch is used so pre-existing operator annotations (e.g.
// `kubecenter.io/eso-stale-after-minutes`) survive the patch — JSON-merge
// against `metadata.annotations` only overlays the named keys.
//
//   POST /externalsecrets/externalsecrets/{namespace}/{name}/force-sync
//
// Outcomes:
//   - 202 Accepted: patch applied; cache invalidated; audit Result=success.
//   - 403 Forbidden: user lacks `update externalsecret`; audit Result=denied.
//   - 404 Not Found: target ES absent.
//   - 409 Conflict {reason: "already_refreshing"}: refreshTime within
//     inFlightWindow; audit Result=skipped:already_refreshing.
//   - 503 Service Unavailable: ESO not detected.
func (h *Handler) HandleForceSyncExternalSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.Discoverer.IsAvailable(r.Context()) {
		httputil.WriteError(w, http.StatusServiceUnavailable, "ESO not detected", "")
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	// RBAC pre-check — the impersonating client below would also enforce
	// this, but the pre-check spares us a round-trip and lets us emit a
	// clean denied audit row.
	if !h.canAccess(r.Context(), user, "update", "externalsecrets", ns) {
		h.auditForceSync(r, user, ns, name, "", audit.ResultDenied, "rbac_denied")
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.dynForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("create impersonating dynamic client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	uid, err := h.patchForceSync(r.Context(), dynClient, ns, name)
	switch {
	case errors.Is(err, errAlreadyRefreshing):
		h.auditForceSync(r, user, ns, name, uid, audit.ResultFailure, "skipped:already_refreshing")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    http.StatusConflict,
				"message": "already refreshing",
				"reason":  "already_refreshing",
			},
		})
		return
	case apierrors.IsForbidden(err):
		h.auditForceSync(r, user, ns, name, uid, audit.ResultDenied, "rbac_denied")
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	case apierrors.IsNotFound(err):
		h.auditForceSync(r, user, ns, name, uid, audit.ResultFailure, "not_found")
		httputil.WriteError(w, http.StatusNotFound, "external secret not found", "")
		return
	case err != nil:
		h.Logger.Error("force-sync patch failed",
			"namespace", ns, "name", name, "error", err)
		h.auditForceSync(r, user, ns, name, uid, audit.ResultFailure, "patch_error")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to force-sync external secret", "")
		return
	}

	h.auditForceSync(r, user, ns, name, uid, audit.ResultSuccess, "")
	h.InvalidateCache()

	w.WriteHeader(http.StatusAccepted)
	httputil.WriteData(w, map[string]string{"status": "force-syncing"})
}

// patchForceSync GETs the target ES, checks the in-flight window, then
// applies a merge-patch that adds the `force-sync` annotation. Returns the
// target's UID (used for audit metadata) plus any patch error. The UID is
// returned even on errAlreadyRefreshing so the audit row can correlate.
func (h *Handler) patchForceSync(ctx context.Context, client dynamic.Interface, ns, name string) (string, error) {
	obj, err := client.Resource(ExternalSecretGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	uid := string(obj.GetUID())

	// In-flight detection: check status.refreshTime against inFlightWindow.
	// Missing / unparseable refreshTime is treated as "not in flight" — fall
	// through to the patch.
	if status, ok := obj.Object["status"].(map[string]any); ok {
		if rt, ok := status["refreshTime"].(string); ok && rt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, rt); parseErr == nil {
				if time.Since(parsed) < inFlightWindow {
					return uid, errAlreadyRefreshing
				}
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	patch := fmt.Appendf(nil,
		`{"metadata":{"annotations":{"force-sync":%q}}}`, now,
	)

	_, err = client.Resource(ExternalSecretGVR).Namespace(ns).Patch(
		ctx, name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		return uid, err
	}
	return uid, nil
}

// auditForceSync writes a single audit row capturing the force-sync outcome.
// The Detail JSON shape matches what the audit-log viewer renders inline.
func (h *Handler) auditForceSync(
	r *http.Request, user *auth.User, ns, name, uid string, result audit.Result, reason string,
) {
	if h.AuditLogger == nil {
		return
	}
	detail := forceSyncResult{
		RequestedBy: user.Username,
		Target:      forceSyncTgt{Namespace: ns, Name: name, UID: uid},
		Result:      string(result),
		Reason:      reason,
	}
	detailJSON, _ := json.Marshal(detail)
	_ = h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionESOForceSync,
		ResourceKind:      "ExternalSecret",
		ResourceNamespace: ns,
		ResourceName:      name,
		Result:            result,
		Detail:            string(detailJSON),
	})
}
