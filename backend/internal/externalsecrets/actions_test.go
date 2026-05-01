package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// recordingAudit captures every audit.Entry written through it. Used by
// force-sync tests to assert action / result / detail JSON shape without
// touching the real slog audit logger.
type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recordingAudit) last() audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return audit.Entry{}
	}
	return r.entries[len(r.entries)-1]
}

func newForceSyncHandler(esObjs []runtime.Object, accessChecker *resources.AccessChecker) (*Handler, *recordingAudit) {
	dynFake := newEsoFakeDynClient(esObjs...)
	rec := &recordingAudit{}
	return &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: accessChecker,
		AuditLogger:   rec,
		Logger:        slog.Default(),
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}, rec
}

func makeESWithAnnotations(ns, name, uid string, anns map[string]string) *unstructured.Unstructured {
	es := makeES(ns, name, uid)
	meta, _ := es.Object["metadata"].(map[string]any)
	annsAny := map[string]any{}
	for k, v := range anns {
		annsAny[k] = v
	}
	meta["annotations"] = annsAny
	return es
}

func TestForceSync_HappyPath(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")

	h, rec := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}

	dyn, _ := h.dynForUser("u", nil)
	got, err := dyn.Resource(ExternalSecretGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get post-patch: %v", err)
	}
	anns := got.GetAnnotations()
	if anns["force-sync"] == "" {
		t.Fatalf("force-sync annotation missing post-patch: anns=%v", anns)
	}

	entry := rec.last()
	if entry.Action != audit.ActionESOForceSync || entry.Result != audit.ResultSuccess {
		t.Errorf("audit entry = %+v; want force_sync/success", entry)
	}
	var detail forceSyncResult
	if err := json.Unmarshal([]byte(entry.Detail), &detail); err != nil {
		t.Fatalf("decode audit detail: %v / %s", err, entry.Detail)
	}
	if detail.Result != "success" || detail.RequestedBy != "alice" || detail.Target.UID != "uid-1" {
		t.Errorf("audit detail = %+v; want success/alice/uid-1", detail)
	}
}

func TestForceSync_PreservesOperatorAnnotations(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeESWithAnnotations(ns, name, "uid-1", map[string]string{
		"kubecenter.io/eso-stale-after-minutes": "60",
		"kubecenter.io/eso-alert-on-recovery":   "true",
	})

	h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}

	dyn, _ := h.dynForUser("u", nil)
	got, err := dyn.Resource(ExternalSecretGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get post-patch: %v", err)
	}
	anns := got.GetAnnotations()
	if anns["force-sync"] == "" {
		t.Errorf("force-sync annotation missing")
	}
	if anns["kubecenter.io/eso-stale-after-minutes"] != "60" {
		t.Errorf("stale-after-minutes lost: %q", anns["kubecenter.io/eso-stale-after-minutes"])
	}
	if anns["kubecenter.io/eso-alert-on-recovery"] != "true" {
		t.Errorf("alert-on-recovery lost: %q", anns["kubecenter.io/eso-alert-on-recovery"])
	}
}

func TestForceSync_RBACDenied(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")

	h, rec := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysDenyAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
	entry := rec.last()
	if entry.Result != audit.ResultDenied {
		t.Errorf("audit result = %q; want denied", entry.Result)
	}
	var detail forceSyncResult
	_ = json.Unmarshal([]byte(entry.Detail), &detail)
	if detail.Reason != "rbac_denied" {
		t.Errorf("audit reason = %q; want rbac_denied", detail.Reason)
	}
}

func TestForceSync_AlreadyRefreshing(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	// Simulate a successful sync 5s ago — within inFlightWindow.
	status, _ := es.Object["status"].(map[string]any)
	status["refreshTime"] = time.Now().UTC().Add(-5 * time.Second).Format(time.RFC3339)

	h, rec := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; body = %s; want 409", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Reason != "already_refreshing" {
		t.Errorf("body reason = %q; want already_refreshing", body.Error.Reason)
	}

	entry := rec.last()
	if entry.Action != audit.ActionESOForceSync {
		t.Errorf("audit action = %q", entry.Action)
	}
	var detail forceSyncResult
	_ = json.Unmarshal([]byte(entry.Detail), &detail)
	if detail.Reason != "skipped:already_refreshing" {
		t.Errorf("audit reason = %q; want skipped:already_refreshing", detail.Reason)
	}
	if detail.Target.UID != "uid-1" {
		t.Errorf("audit target uid = %q; want uid-1 (UID populated even on skip)", detail.Target.UID)
	}
}

func TestForceSync_StaleRefreshTimeIsNotInFlight(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	// refreshTime older than the in-flight window — should NOT 409.
	status, _ := es.Object["status"].(map[string]any)
	status["refreshTime"] = time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)

	h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202; body = %s", w.Code, w.Body.String())
	}
}

func TestForceSync_NotFound(t *testing.T) {
	ns, name := "apps", "missing"

	h, rec := newForceSyncHandler(nil, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
	entry := rec.last()
	var detail forceSyncResult
	_ = json.Unmarshal([]byte(entry.Detail), &detail)
	if detail.Reason != "not_found" {
		t.Errorf("audit reason = %q; want not_found", detail.Reason)
	}
}

func TestForceSync_PatchForbiddenAtAPI(t *testing.T) {
	// AccessChecker permits the action (so we get past the pre-check), but the
	// API-server-side patch returns 403 — modelling impersonation hitting a
	// missing RBAC role on the target verb.
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")

	dynFake := newEsoFakeDynClient(es)
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: GroupName, Resource: "externalsecrets"},
			name,
			errors.New("user cannot patch externalsecret"),
		)
	})

	rec := &recordingAudit{}
	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		AuditLogger:   rec,
		Logger:        slog.Default(),
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
	entry := rec.last()
	if entry.Result != audit.ResultDenied {
		t.Errorf("audit result = %q; want denied", entry.Result)
	}
}

// patchForceSyncPinned with a UID that doesn't match the live object returns
// errUIDDrifted instead of patching. Guards against ES delete/recreate
// between scope-resolve and worker patch — protects audit honesty.
// See todo #349.
func TestPatchForceSyncPinned_UIDDrift(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-LIVE")
	dynFake := newEsoFakeDynClient(es)

	h := &Handler{Logger: slog.Default()}
	uid, err := h.patchForceSyncPinned(context.Background(), dynFake, ns, name, "uid-PINNED")
	if !errors.Is(err, errUIDDrifted) {
		t.Errorf("error = %v; want errUIDDrifted", err)
	}
	if uid != "uid-LIVE" {
		t.Errorf("returned uid = %q; want live uid", uid)
	}
}

// Pinned UID matching live UID succeeds — single-resource happy path.
func TestPatchForceSyncPinned_UIDMatch(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	dynFake := newEsoFakeDynClient(es)

	h := &Handler{Logger: slog.Default()}
	_, err := h.patchForceSyncPinned(context.Background(), dynFake, ns, name, "uid-1")
	if err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

// patchForceSync retries on transient apiserver errors (timeout, throttle,
// service unavailable) up to maxPatchRetries times, then returns the final
// error. The patch is idempotent so retries are safe. See todo #343.
func TestPatchForceSync_RetriesTransient(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	dynFake := newEsoFakeDynClient(es)

	var patchCalls int
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		if patchCalls < 3 {
			return true, nil, apierrors.NewTooManyRequests("APF throttled", 1)
		}
		// 3rd attempt succeeds — return the underlying object so the fake
		// proceeds normally.
		return false, nil, nil
	})

	h := &Handler{Logger: slog.Default()}
	uid, err := h.patchForceSync(context.Background(), dynFake, ns, name)
	if err != nil {
		t.Fatalf("patchForceSync after retries: %v", err)
	}
	if uid != "uid-1" {
		t.Errorf("uid = %q; want uid-1", uid)
	}
	if patchCalls != 3 {
		t.Errorf("patch calls = %d; want 3 (2 transient + 1 success)", patchCalls)
	}
}

// Persistent transient errors return the last error after maxPatchRetries.
func TestPatchForceSync_RetriesExhausted(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	dynFake := newEsoFakeDynClient(es)

	var patchCalls int
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		return true, nil, apierrors.NewServiceUnavailable("apiserver rolling restart")
	})

	h := &Handler{Logger: slog.Default()}
	_, err := h.patchForceSync(context.Background(), dynFake, ns, name)
	if !apierrors.IsServiceUnavailable(err) {
		t.Errorf("error = %v; want service unavailable", err)
	}
	if patchCalls != maxPatchRetries {
		t.Errorf("patch calls = %d; want %d", patchCalls, maxPatchRetries)
	}
}

// Non-transient errors do not retry — they short-circuit immediately.
func TestPatchForceSync_NoRetryOnPermanent(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	dynFake := newEsoFakeDynClient(es)

	var patchCalls int
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "externalsecrets"}, name, errors.New("denied"),
		)
	})

	h := &Handler{Logger: slog.Default()}
	_, err := h.patchForceSync(context.Background(), dynFake, ns, name)
	if !apierrors.IsForbidden(err) {
		t.Errorf("error = %v; want forbidden", err)
	}
	if patchCalls != 1 {
		t.Errorf("patch calls = %d; want 1 (no retry on 403)", patchCalls)
	}
}

// Future-dated refreshTime must not collapse `time.Since() < inFlightWindow`
// into a permanent in-flight false positive. NTP step or malicious controller
// scenario. See #355 item 2.
func TestPatchForceSync_FutureRefreshTimeNotInFlight(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	// Set status.refreshTime 5 minutes into the future.
	es.Object["status"] = map[string]any{
		"refreshTime": time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
	}
	dynFake := newEsoFakeDynClient(es)

	h := &Handler{Logger: slog.Default()}
	_, err := h.patchForceSync(context.Background(), dynFake, ns, name)
	if errors.Is(err, errAlreadyRefreshing) {
		t.Errorf("future-dated refreshTime falsely classified in-flight")
	}
	if err != nil {
		t.Fatalf("patch should succeed: %v", err)
	}
}

// Force-sync against a non-local X-Cluster-ID returns 501 *before* any patch
// or audit row is emitted. Phase E writes only the local cluster; honoring
// the header would silently desync the audit row from reality. See todo #339.
func TestForceSync_RejectsNonLocalCluster(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	h, rec := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = r.WithContext(middleware.WithClusterID(r.Context(), "prod-cluster"))
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d; want 501", w.Code)
	}
	if len(rec.entries) != 0 {
		t.Errorf("audit entries = %d; want 0 (guard fires before audit)", len(rec.entries))
	}
	// Confirm no patch landed on the local object.
	dyn, _ := h.dynForUser("u", nil)
	got, err := dyn.Resource(ExternalSecretGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if anns := got.GetAnnotations(); anns["force-sync"] != "" {
		t.Errorf("force-sync annotation present on local ES; guard failed: anns=%v", anns)
	}
}
