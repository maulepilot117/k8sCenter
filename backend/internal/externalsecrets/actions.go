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

// errUIDDrifted is returned by patchForceSyncPinned when the live ES at
// (namespace, name) has a different UID than the operator pinned at scope-
// resolve time. Means the ES was deleted and recreated between resolve and
// patch — patching the new UID would silently mutate a resource the
// operator never confirmed. See todo #349.
var errUIDDrifted = errors.New("uid_drifted")

// rejectNonLocalClusterWrite blocks ESO write actions targeted at a
// non-local cluster. Phase E does not route writes through ClusterRouter —
// the dynamic client always points at the local ClientFactory — so honoring
// X-Cluster-ID would silently desync the audit row (records the header
// value) from the actual mutation (always hits local). See todo #339.
//
// Returns false (and writes a 501 response) when the caller must abort.
// Returns true when execution may proceed.
func rejectNonLocalClusterWrite(w http.ResponseWriter, r *http.Request) bool {
	clusterID := middleware.ClusterIDFromContext(r.Context())
	if clusterID != "" && clusterID != "local" {
		httputil.WriteError(w, http.StatusNotImplemented,
			"ESO write actions are local-cluster only in v1",
			"X-Cluster-ID="+clusterID+" is not supported for force-sync or bulk refresh")
		return false
	}
	return true
}

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

	if !rejectNonLocalClusterWrite(w, r) {
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
	// #355 item 3: K8s RBAC distinguishes `patch` from `update`. The actual
	// API call is a Patch, so the pre-check verb must match — otherwise a
	// user with `patch` but not `update` gets a false denial.
	if !h.canAccess(r.Context(), user, "patch", "externalsecrets", ns) {
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
		httputil.WriteErrorWithReason(w, http.StatusConflict, "already refreshing", "already_refreshing", nil)
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
	// #355 item 7: don't InvalidateCache on every single force-sync — a 50-ES
	// click-storm would trigger 50 SA fetchAlls. The 30s cache TTL is plenty
	// fresh. Bulk worker still invalidates once at the end of a job.

	httputil.WriteJSON(w, http.StatusAccepted, map[string]any{
		"data": map[string]string{"status": "force-syncing"},
	})
}

// perPatchTimeout bounds each Get+Patch attempt against the kube-apiserver.
// A wedged apiserver (network partition, etcd lockup) would otherwise block
// the worker for the underlying TCP timeout (often 60s+). At 5000 targets
// that head-of-line blocks every queued bulk job. See todo #346.
const perPatchTimeout = 10 * time.Second

// maxPatchRetries is the upper bound on retry attempts for transient errors
// (timeout / throttled / unavailable). The Patch is idempotent — `force-sync:
// <now>` collapses to one ESO reconcile regardless of how many times it
// lands — so retries on transient classifications are safe. cert-manager's
// HandleRenew uses a similar bound. See todo #343.
const maxPatchRetries = 3

// patchRetryBaseDelay is the initial sleep between retry attempts. Doubled
// per attempt: 250ms, 500ms, 1000ms. Total worst-case overhead is ~1.75s
// per target, dwarfed by interCallDelay × N for typical jobs.
const patchRetryBaseDelay = 250 * time.Millisecond

// patchForceSync GETs the target ES, checks the in-flight window, then
// applies a merge-patch that adds the `force-sync` annotation. Returns the
// target's UID (used for audit metadata) plus any patch error. The UID is
// returned even on errAlreadyRefreshing so the audit row can correlate.
//
// The Get+Patch is wrapped in perPatchTimeout to bound apiserver hangs, and
// transient apiserver errors (timeout, throttle, unavailable) are retried
// up to maxPatchRetries times with exponential backoff.
//
// Pass expectedUID = "" for un-pinned semantics (single force-sync handler
// has no pin). The bulk worker passes the pinned UID; mismatch returns
// errUIDDrifted so the worker records a `uid_drifted` outcome instead of
// silently patching a recreated resource.
func (h *Handler) patchForceSync(ctx context.Context, client dynamic.Interface, ns, name string) (string, error) {
	return h.patchForceSyncPinned(ctx, client, ns, name, "")
}

func (h *Handler) patchForceSyncPinned(ctx context.Context, client dynamic.Interface, ns, name, expectedUID string) (string, error) {
	var uid string
	var lastErr error
	delay := patchRetryBaseDelay
	for attempt := range maxPatchRetries {
		uid, lastErr = h.patchForceSyncOnce(ctx, client, ns, name, expectedUID)
		if !isTransientPatchError(lastErr) {
			return uid, lastErr
		}
		// Don't sleep past parent ctx cancellation.
		if attempt == maxPatchRetries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return uid, lastErr
		case <-time.After(delay):
		}
		delay *= 2
	}
	return uid, lastErr
}

// patchForceSyncOnce is the single-attempt patch primitive. Each call gets
// its own perPatchTimeout-bounded context.
func (h *Handler) patchForceSyncOnce(ctx context.Context, client dynamic.Interface, ns, name, expectedUID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, perPatchTimeout)
	defer cancel()

	obj, err := client.Resource(ExternalSecretGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	uid := string(obj.GetUID())

	// Pin enforcement: when the caller pinned a UID at scope-resolve time,
	// reject if the live ES has a different UID (deleted-and-recreated
	// race). Returns the LIVE uid alongside errUIDDrifted so the worker can
	// log both for forensic clarity. See todo #349.
	if expectedUID != "" && uid != expectedUID {
		return uid, errUIDDrifted
	}

	// In-flight detection: check status.refreshTime against inFlightWindow.
	// Missing / unparseable refreshTime is treated as "not in flight" — fall
	// through to the patch.
	if status, ok := obj.Object["status"].(map[string]any); ok {
		if rt, ok := status["refreshTime"].(string); ok && rt != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, rt); parseErr == nil {
				// Clamp to non-negative — a future-dated refreshTime (NTP
				// step or malicious controller) would otherwise produce a
				// negative duration that satisfies `< inFlightWindow`
				// indefinitely. See todo #355 item 2.
				since := time.Since(parsed)
				if since >= 0 && since < inFlightWindow {
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

// isTransientPatchError returns true for k8s API errors worth retrying.
// Mirrors the transient_* prefix produced by classifyPatchError.
func isTransientPatchError(err error) bool {
	if err == nil {
		return false
	}
	return apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err)
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
