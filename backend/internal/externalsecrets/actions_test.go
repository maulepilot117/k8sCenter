package externalsecrets

import (
	"bytes"
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
	if bytes.Contains(w.Body.Bytes(), []byte("baseline")) {
		t.Errorf("501 body carries a baseline: %s", w.Body.String())
	}
}

// forceSyncAccepted is the 202 body shape (plan D6).
type forceSyncAccepted struct {
	Data struct {
		Status      string           `json:"status"`
		Correlation string           `json:"correlation"`
		Baseline    *refreshBaseline `json:"baseline"`
	} `json:"data"`
}

// postForceSync drives the handler for (ns, name) and returns the recorder.
func postForceSync(t *testing.T, h *Handler, ns, name string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/", nil),
		&auth.User{Username: "alice", KubernetesUsername: "u"},
	)
	r = urlWithChiParams(r, map[string]string{"namespace": ns, "name": name})
	h.HandleForceSyncExternalSecret(w, r)
	return w
}

func decodeAccepted(t *testing.T, w *httptest.ResponseRecorder) forceSyncAccepted {
	t.Helper()
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202; body = %s", w.Code, w.Body.String())
	}
	var body forceSyncAccepted
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 202 body: %v / %s", err, w.Body.String())
	}
	if body.Data.Status != "force-syncing" {
		t.Errorf("data.status = %q; want force-syncing (R4: mobile reads it)", body.Data.Status)
	}
	if body.Data.Baseline == nil {
		t.Fatalf("202 body has no baseline: %s", w.Body.String())
	}
	return body
}

// makeSyncedES is an ES that ESO reconciled two minutes ago, outside the
// in-flight window, with every field the baseline records.
func makeSyncedES(ns, name, uid string) (*unstructured.Unstructured, string, string) {
	es := makeES(ns, name, uid)
	es.SetResourceVersion("918273")
	es.SetGeneration(7)
	refreshTime := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	readyLTT := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	es.Object["status"] = map[string]any{
		"refreshTime":           refreshTime,
		"syncedResourceVersion": "1-abc123",
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "SecretSynced", "lastTransitionTime": readyLTT},
		},
	}
	return es, refreshTime, readyLTT
}

func TestForceSync_202IncludesBaseline(t *testing.T) {
	ns, name := "apps", "db-creds"
	es, refreshTime, readyLTT := makeSyncedES(ns, name, "uid-1")
	h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	before := time.Now().UTC()
	body := decodeAccepted(t, postForceSync(t, h, ns, name))
	after := time.Now().UTC()

	b := body.Data.Baseline
	if b.UID != "uid-1" || b.ResourceVersion != "918273" || b.Generation != 7 {
		t.Errorf("baseline identity = %+v; want uid-1 / 918273 / 7", b)
	}
	if b.RefreshTime != refreshTime || b.ReadyLastTransitionTime != readyLTT || b.SyncedResourceVersion != "1-abc123" {
		t.Errorf("baseline status fields = %+v", b)
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, b.RequestedAt)
	if err != nil {
		t.Fatalf("requestedAt %q is not RFC3339: %v", b.RequestedAt, err)
	}
	if requestedAt.Before(before) || requestedAt.After(after) {
		t.Errorf("requestedAt %s outside the request window [%s, %s]", requestedAt, before, after)
	}
}

func TestForceSync_CorrelationStrongWhenRefreshTimePresent(t *testing.T) {
	ns, name := "apps", "db-creds"
	es, _, _ := makeSyncedES(ns, name, "uid-1")
	h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	body := decodeAccepted(t, postForceSync(t, h, ns, name))
	if body.Data.Correlation != "strong" {
		t.Errorf("correlation = %q; want strong (refreshTime present)", body.Data.Correlation)
	}
}

func TestForceSync_CorrelationWeakWhenRefreshTimeAbsent(t *testing.T) {
	ns, name := "apps", "db-creds"
	for label, rt := range map[string]any{"absent": nil, "unparseable": "not-a-time", "empty": ""} {
		t.Run(label, func(t *testing.T) {
			es := makeES(ns, name, "uid-1")
			if rt != nil {
				status, _ := es.Object["status"].(map[string]any)
				status["refreshTime"] = rt
			}
			h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

			body := decodeAccepted(t, postForceSync(t, h, ns, name))
			if body.Data.Correlation != "weak" {
				t.Errorf("correlation = %q; want weak", body.Data.Correlation)
			}
			if body.Data.Baseline.RefreshTime != "" {
				t.Errorf("baseline.refreshTime = %q; a weak baseline must not carry one", body.Data.Baseline.RefreshTime)
			}
		})
	}
}

// The baseline describes the object as it was before the patch landed: a
// baseline read after the patch could already include the sync it is meant
// to be compared against.
func TestForceSync_BaselineCapturedBeforePatch(t *testing.T) {
	ns, name := "apps", "db-creds"
	es, _, _ := makeSyncedES(ns, name, "uid-1")
	dynFake := newEsoFakeDynClient(es)

	var calls []string
	var patchedAt time.Time
	dynFake.PrependReactor("get", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		calls = append(calls, "get")
		return false, nil, nil
	})
	dynFake.PrependReactor("patch", "externalsecrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		calls = append(calls, "patch")
		patchedAt = time.Now().UTC()
		post := es.DeepCopy()
		post.SetResourceVersion("918274")
		return true, post, nil
	})

	h := &Handler{
		Discoverer:    detectedDiscoverer(),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
	}
	body := decodeAccepted(t, postForceSync(t, h, ns, name))

	if len(calls) != 2 || calls[0] != "get" || calls[1] != "patch" {
		t.Fatalf("apiserver calls = %v; want [get patch]", calls)
	}
	if got := body.Data.Baseline.ResourceVersion; got != "918273" {
		t.Errorf("baseline resourceVersion = %q; want the pre-patch 918273", got)
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, body.Data.Baseline.RequestedAt)
	if err != nil {
		t.Fatalf("requestedAt: %v", err)
	}
	if requestedAt.After(patchedAt) {
		t.Errorf("requestedAt %s is after the patch at %s", requestedAt, patchedAt)
	}
}

func TestForceSync_MalformedStatusYieldsWeakBaselineNoPanic(t *testing.T) {
	ns, name := "apps", "db-creds"
	for label, status := range map[string]any{
		"string":            "garbage",
		"array":             []any{"a", int64(1)},
		"conditionsNotList": map[string]any{"conditions": "x", "refreshTime": int64(42)},
		"conditionNotMap":   map[string]any{"conditions": []any{"Ready", int64(7)}},
		"lttWrongType":      map[string]any{"conditions": []any{map[string]any{"type": "Ready", "lastTransitionTime": int64(5)}}},
	} {
		t.Run(label, func(t *testing.T) {
			es := makeES(ns, name, "uid-1")
			es.Object["status"] = status
			h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

			body := decodeAccepted(t, postForceSync(t, h, ns, name))
			b := body.Data.Baseline
			if body.Data.Correlation != "weak" {
				t.Errorf("correlation = %q; want weak", body.Data.Correlation)
			}
			if b.UID != "uid-1" || b.RequestedAt == "" {
				t.Errorf("baseline lost identity on malformed status: %+v", b)
			}
			if b.RefreshTime != "" || b.ReadyLastTransitionTime != "" || b.SyncedResourceVersion != "" {
				t.Errorf("malformed status leaked into baseline: %+v", b)
			}
		})
	}
}

// The 409 is unchanged: mobile's executeAction path keys off error.reason and
// never sees a baseline for a request that was not accepted (R4).
func TestForceSync_AlreadyRefreshing409StillHasNoBaseline(t *testing.T) {
	ns, name := "apps", "db-creds"
	es := makeES(ns, name, "uid-1")
	status, _ := es.Object["status"].(map[string]any)
	status["refreshTime"] = time.Now().UTC().Add(-5 * time.Second).Format(time.RFC3339)
	h, _ := newForceSyncHandler([]runtime.Object{es}, resources.NewAlwaysAllowAccessChecker())

	w := postForceSync(t, h, ns, name)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["data"]; ok {
		t.Errorf("409 body carries data: %s", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("baseline")) {
		t.Errorf("409 body carries a baseline: %s", w.Body.String())
	}
}
