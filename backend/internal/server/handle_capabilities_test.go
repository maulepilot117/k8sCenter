package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	fakekube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/store"
)

// --- Fixtures -----------------------------------------------------------
//
// The handler's LOCAL branch gets its discovery client from the real
// *k8s.ClientFactory (cr.localFactory.DiscoveryClient() == the base
// clientset's real *discovery.DiscoveryClient — a concrete type that cannot
// be swapped for a hand-rolled fake). To exercise real discovery-tolerance
// behavior (TestCapabilities_DiscoveryPartialErrorIsNotMissing) these tests
// stand up a tiny httptest server speaking the legacy (unaggregated)
// discovery protocol and point a real *kubernetes.Clientset at it — no
// mocking of k8sCenter's own logic, only of the Kubernetes API server it
// talks to.
//
// The REMOTE branch is a different story: ClusterRouter.TargetSchemaFor and
// AccessChecker both require a concrete *store.ClusterStore to reach a
// non-local target, and the SSRF policy in ValidateRemoteURL unconditionally
// blocks loopback addresses — so a remote cluster cannot be pointed at a
// local httptest server the way local can. TestCapabilities_
// UnreachableIsNotUnsupported and TestCapabilities_StaleProbeYieldsNullReachable
// therefore exercise resolveReachability + buildCapability directly: real
// production functions, driven with a fake clusterRecordGetter instead of a
// live PostgreSQL-backed store (mirrors internal/k8s's own clusterGetter
// seam, added for the identical reason).

type discoveryFixture struct {
	hasNodes bool
	hasESO   bool
	failESO  bool
}

// newFakeDiscoveryServer serves just enough of the legacy discovery protocol
// for client-go's ServerGroupsAndResources to exercise the real per-
// group-version fetch path (discovery_client.go's fetchGroupVersionResources):
// GET /api and /apis list groups, then one GET per group/version returns
// that group's APIResourceList. When failESO is set, the external-secrets.io
// group/version 500s, producing client-go's genuine partial-result-plus-error
// shape (a non-nil resource list from every group that DID load, alongside a
// non-nil *ErrGroupDiscoveryFailed) instead of a synthetic stand-in for it.
func newFakeDiscoveryServer(t *testing.T, fx discoveryFixture) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryJSON(w, metav1.APIVersions{Versions: []string{"v1"}})
	})
	mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		list := metav1.APIResourceList{GroupVersion: "v1"}
		if fx.hasNodes {
			list.APIResources = append(list.APIResources, metav1.APIResource{
				Name: "nodes", SingularName: "node", Namespaced: false, Kind: "Node",
			})
		}
		writeDiscoveryJSON(w, list)
	})
	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		groups := metav1.APIGroupList{}
		if fx.hasESO {
			groups.Groups = append(groups.Groups, metav1.APIGroup{
				Name: "external-secrets.io",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "external-secrets.io/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: "external-secrets.io/v1beta1", Version: "v1beta1",
				},
			})
		}
		writeDiscoveryJSON(w, groups)
	})
	mux.HandleFunc("/apis/external-secrets.io/v1beta1", func(w http.ResponseWriter, r *http.Request) {
		if fx.failESO {
			http.Error(w, "simulated group-version discovery failure", http.StatusInternalServerError)
			return
		}
		list := metav1.APIResourceList{GroupVersion: "external-secrets.io/v1beta1"}
		list.APIResources = append(list.APIResources, metav1.APIResource{
			Name: "externalsecrets", SingularName: "externalsecret", Namespaced: true, Kind: "ExternalSecret",
		})
		writeDiscoveryJSON(w, list)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeDiscoveryJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newLocalCapabilitiesFactory builds a *k8s.ClientFactory whose discovery
// client is a real *kubernetes.Clientset pointed at a fake discovery server,
// per the package doc comment above.
func newLocalCapabilitiesFactory(t *testing.T, fx discoveryFixture) *k8s.ClientFactory {
	t.Helper()
	discSrv := newFakeDiscoveryServer(t, fx)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: discSrv.URL})
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}
	dyn := dynfake.NewSimpleDynamicClient(scheme.Scheme)
	return k8s.NewTestClientFactoryWithDynamic(cs, dyn)
}

// newCapabilitiesTestServer wires a Server whose ResourceHandler/ClusterRouter
// are backed by newLocalCapabilitiesFactory, suitable for exercising the
// handler's LOCAL-cluster path end-to-end over real HTTP. ac defaults to
// NewAlwaysAllowAccessChecker when nil.
func newCapabilitiesTestServer(t *testing.T, fx discoveryFixture, ac *resources.AccessChecker) *Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := newLocalCapabilitiesFactory(t, fx)
	router := k8s.NewClusterRouter(factory, nil, "", logger)

	if ac == nil {
		ac = resources.NewAlwaysAllowAccessChecker()
	}

	srv := testServer(t)
	srv.ClusterRouter = router
	srv.ResourceHandler = &resources.Handler{
		K8sClient:     factory,
		ClusterRouter: router,
		AccessChecker: ac,
		AuditLogger:   audit.NewSlogLogger(logger),
		Logger:        logger,
		TaskManager:   resources.NewTaskManager(),
	}
	return srv
}

// capabilitiesIssueToken issues an access token for a synthetic user.
func capabilitiesIssueToken(t *testing.T, srv *Server, username string, admin bool) string {
	t.Helper()
	roles := []string{"viewer"}
	if admin {
		roles = []string{"admin"}
	}
	u := &auth.User{
		ID:                 username,
		Username:           username,
		KubernetesUsername: username,
		KubernetesGroups:   []string{"dev"},
		Roles:              roles,
	}
	tok, err := srv.TokenManager.IssueAccessToken(u)
	if err != nil {
		t.Fatalf("IssueAccessToken(%s): %v", username, err)
	}
	return tok
}

// capabilitiesRequest issues GET /api/v1/capabilities/{pathClusterID} through
// the real router (real Auth/CSRF/ClusterContext middleware included).
// headerClusterID is only set on the request when non-empty, so callers can
// exercise the "no X-Cluster-ID header at all" local-cluster shape too.
func capabilitiesRequest(t *testing.T, srv *Server, token, pathClusterID, headerClusterID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities/"+pathClusterID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if headerClusterID != "" {
		req.Header.Set("X-Cluster-ID", headerClusterID)
	}
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	return w
}

func decodeCapabilities(t *testing.T, w *httptest.ResponseRecorder) CapabilitiesResponse {
	t.Helper()
	var body struct {
		Data CapabilitiesResponse `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v (status=%d body=%s)", err, w.Code, w.Body.String())
	}
	return body.Data
}

func findCapability(t *testing.T, resp CapabilitiesResponse, op string) Capability {
	t.Helper()
	for _, c := range resp.Capabilities {
		if c.Operation == op {
			return c
		}
	}
	t.Fatalf("operation %q not found in response capabilities: %+v", op, resp.Capabilities)
	return Capability{}
}

func findOp(t *testing.T, id string) capabilityOp {
	t.Helper()
	for _, op := range capabilityOperations {
		if op.ID == id {
			return op
		}
	}
	t.Fatalf("operation %q not present in capabilityOperations", id)
	return capabilityOp{}
}

// syntheticRemoteOp / syntheticRemoteProbeOp exist purely to drive
// buildCapability's remote-branch dimension-priority logic (reachable ->
// discovery -> authz -> ok) directly, with a real, falsifiable verdict.
//
// As of task review round 1 (finding #3), every row in the REAL
// capabilityOperations table is honestly RemoteSupported: false — none of
// yaml.validate/diff/export/apply or dashboard.summary claim remote support
// today, because every one of them still 501s/400s the moment it's actually
// called. That is correct product behavior, but it means no real operation
// can reach buildCapability's reachable/discovery/authz branches on the
// remote class anymore: buildCapability's very first check
// (!row.PlatformSupported) now short-circuits every real op to
// unsupported_platform before any of that logic runs. These two synthetic,
// test-only rows (never added to the real table) are what let this file
// keep pinning that logic with real verdicts instead of losing the
// coverage — see TestCapabilities_UnreachableIsNotUnsupported,
// TestCapabilities_StaleProbeYieldsNullReachable, and the "remote-only"
// cases in TestCapabilities_ReasonCodesAreClosed.
var syntheticRemoteOp = capabilityOp{
	ID: "test.synthetic", Label: "synthetic (test-only, not a real operation)",
	LocalSupported: true, RemoteSupported: true,
	AuthVerb: "get", AuthGroup: "", AuthResource: "configmaps",
}

var syntheticRemoteProbeOp = capabilityOp{
	ID: "test.synthetic-probe", Label: "synthetic probe (test-only, not a real operation)",
	LocalSupported: true, RemoteSupported: true,
	Probe:    &gvrProbe{Group: "", Resource: "nodes"},
	AuthVerb: "get", AuthGroup: "", AuthResource: "configmaps",
}

// fakeClusterRecordGetter is a hermetic clusterRecordGetter — see the
// package doc comment at the top of this file for why the remote-cluster
// reachability tests use this instead of a live *store.ClusterStore.
type fakeClusterRecordGetter struct {
	rec *store.ClusterRecord
	err error
}

func (f *fakeClusterRecordGetter) Get(context.Context, string) (*store.ClusterRecord, error) {
	return f.rec, f.err
}

// --- Tests ---------------------------------------------------------------

func TestCapabilities_HeaderPathMismatchRejected(t *testing.T) {
	srv := testServer(t)
	token := capabilitiesIssueToken(t, srv, "admin-1", true)

	w := capabilitiesRequest(t, srv, token, "remote-a", "remote-b")

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Data  json.RawMessage `json:"data"`
		Error struct {
			Reason string         `json:"reason"`
			Extra  map[string]any `json:"extra"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Reason != "cluster_target_mismatch" {
		t.Errorf("reason = %q; want %q", body.Error.Reason, "cluster_target_mismatch")
	}
	if body.Data != nil {
		t.Errorf("data = %s; want no capability rows on a mismatch", body.Data)
	}
	if body.Error.Extra["pathClusterId"] != "remote-a" || body.Error.Extra["headerClusterId"] != "remote-b" {
		t.Errorf("extra = %+v; want pathClusterId=remote-a headerClusterId=remote-b", body.Error.Extra)
	}
}

func TestCapabilities_LocalRequiresNoAdmin(t *testing.T) {
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true, hasESO: true}, nil)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false) // non-admin

	w := capabilitiesRequest(t, srv, token, "local", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 for a non-admin user asking about the local cluster, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	if body.ClusterID != "local" {
		t.Errorf("clusterId = %q; want local", body.ClusterID)
	}
	if len(body.Capabilities) != len(capabilityOperations) {
		t.Errorf("len(capabilities) = %d; want %d", len(body.Capabilities), len(capabilityOperations))
	}
}

func TestCapabilities_NoStoreHeader(t *testing.T) {
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true, hasESO: true}, nil)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q; want %q", got, "no-store")
	}
}

func TestCapabilities_ForbiddenIsNotUnsupported(t *testing.T) {
	srv := newCapabilitiesTestServer(t, discoveryFixture{}, resources.NewAlwaysDenyAccessChecker())
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	cap := findCapability(t, body, "yaml.apply")

	if !cap.PlatformSupported {
		t.Error("PlatformSupported = false; want true (RBAC denial is not the same as unsupported)")
	}
	if cap.Authorized == nil || *cap.Authorized {
		t.Fatalf("Authorized = %v; want false", cap.Authorized)
	}
	if cap.ReasonCode != ReasonForbidden {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonForbidden)
	}
}

// perIdentityClientFactory hands out a distinct fake clientset per username,
// each with its own SelfSubjectAccessReview verdict — a REAL
// AccessChecker.CanAccessGroupResource call reaches this per request (via a
// real, non-predicate, non-alwaysAllow/alwaysDeny AccessChecker), so a
// handler that (bug) cached the first identity's decision, or read
// X-Cluster-ID/username from a captured closure instead of the current
// request, would make both identities come back identical. It satisfies the
// same narrow, structurally-typed interface resources.NewAccessChecker
// accepts (interface{ ClientForUser(...) (kubernetes.Interface, error) }),
// entirely independent of *k8s.ClientFactory.
//
// NOTE: this fixture does NOT exercise the CanAccess-vs-CanAccessGroupResource
// distinction (brief A1) — CanAccess only diverges from CanAccessGroupResource
// in predicate-fake mode (access.go), and this fixture sets no predicate, so
// swapping one call for the other here would not change the outcome. A1 is
// pinned separately by TestCapabilities_PredicateDenialPinsCanAccessGroupResource,
// which uses resources.NewPredicateAccessChecker — the one fixture where the
// two methods actually disagree.
type perIdentityClientFactory struct {
	allow map[string]bool
}

func (f *perIdentityClientFactory) ClientForUser(username string, _ []string) (kubernetes.Interface, error) {
	allowed := f.allow[username]
	cs := fakekube.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Allowed: allowed},
		}, nil
	})
	return cs, nil
}

func TestCapabilities_TwoIdentitiesGetOwnPermissionView(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := &perIdentityClientFactory{allow: map[string]bool{"alice": true, "bob": false}}
	ac := resources.NewAccessChecker(factory, logger)

	srv := newCapabilitiesTestServer(t, discoveryFixture{}, ac)

	aliceToken := capabilitiesIssueToken(t, srv, "alice", false)
	bobToken := capabilitiesIssueToken(t, srv, "bob", false)

	aliceBody := decodeCapabilities(t, capabilitiesRequest(t, srv, aliceToken, "local", ""))
	bobBody := decodeCapabilities(t, capabilitiesRequest(t, srv, bobToken, "local", ""))

	aliceCap := findCapability(t, aliceBody, "yaml.apply")
	bobCap := findCapability(t, bobBody, "yaml.apply")

	if aliceCap.Authorized == nil || !*aliceCap.Authorized {
		t.Fatalf("alice authorized = %v; want true", aliceCap.Authorized)
	}
	if bobCap.Authorized == nil || *bobCap.Authorized {
		t.Fatalf("bob authorized = %v; want false", bobCap.Authorized)
	}
	if aliceCap.ReasonCode != ReasonOK {
		t.Errorf("alice reasonCode = %q; want %q", aliceCap.ReasonCode, ReasonOK)
	}
	if bobCap.ReasonCode != ReasonForbidden {
		t.Errorf("bob reasonCode = %q; want %q", bobCap.ReasonCode, ReasonForbidden)
	}
}

// TestCapabilities_PredicateDenialPinsCanAccessGroupResource makes brief
// amendment A1 (use AccessChecker.CanAccessGroupResource, never CanAccess)
// an actual falsifiable claim rather than an assertion in a comment.
//
// access.go's CanAccess checks ac.alwaysAllow, then ac.alwaysDeny, and only
// THEN reaches its predicate branch — where it short-circuits to an
// unconditional `return true, nil` without ever calling the predicate.
// CanAccessGroupResource's predicate branch actually calls the predicate.
// NewAlwaysAllowAccessChecker, NewAlwaysDenyAccessChecker, and a real
// (non-predicate) AccessChecker — every fixture used elsewhere in this file
// — never reach either method's predicate branch at all, so CanAccess and
// CanAccessGroupResource behave identically under all of them.
// resources.NewPredicateAccessChecker is the ONE fixture where the two
// methods genuinely disagree, so it is the only fixture that can pin A1: a
// handler that regressed to CanAccess would see this predicate short-circuit
// to "allowed" and report authorized: true / reasonCode: ok here, even
// though the predicate itself denies everything.
func TestCapabilities_PredicateDenialPinsCanAccessGroupResource(t *testing.T) {
	ac := resources.NewPredicateAccessChecker(func(verb, apiGroup, resource, namespace string) bool {
		return false // deny every verb/group/resource/namespace combination
	})
	srv := newCapabilitiesTestServer(t, discoveryFixture{}, ac)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	cap := findCapability(t, body, "yaml.apply")

	if cap.Authorized == nil || *cap.Authorized {
		t.Fatalf("Authorized = %v; want false — CanAccess would short-circuit this predicate-fake AccessChecker to an unconditional allow (access.go), "+
			"silently ignoring the predicate's denial; only CanAccessGroupResource actually consults it", cap.Authorized)
	}
	if cap.ReasonCode != ReasonForbidden {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonForbidden)
	}
}

func TestCapabilities_RemoteExecUnsupported(t *testing.T) {
	// Bare server: ClusterRouter and ClusterStore are both left nil. pod.exec
	// is unsupported for the remote target class regardless of whether the
	// target itself can even be resolved (buildCapability's top-priority
	// check), so this needs no ClusterRouter/ClusterStore/ResourceHandler
	// wiring at all.
	srv := testServer(t)
	token := capabilitiesIssueToken(t, srv, "admin-1", true)

	w := capabilitiesRequest(t, srv, token, "remote-42", "remote-42")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	cap := findCapability(t, body, "pod.exec")

	if cap.PlatformSupported {
		t.Error("PlatformSupported = true; want false for pod.exec on a remote cluster")
	}
	if cap.ReasonCode != ReasonUnsupportedPlatform {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonUnsupportedPlatform)
	}
}

func TestCapabilities_UnreachableIsNotUnsupported(t *testing.T) {
	now := time.Now()
	probedRecently := now.Add(-30 * time.Second) // well inside the 180s freshness window
	getter := &fakeClusterRecordGetter{rec: &store.ClusterRecord{
		ID: "remote-1", Status: "disconnected", LastProbedAt: &probedRecently,
	}}

	reach := resolveReachability(context.Background(), getter, false, "remote-1", now)
	if reach.reachable == nil || *reach.reachable {
		t.Fatalf("resolveReachability reachable = %v; want false for a disconnected cluster", reach.reachable)
	}

	// syntheticRemoteOp, not a real operation: as of task review round 1
	// (finding #3) every real row is honestly RemoteSupported: false today,
	// so no real op could reach this test's assertions without first
	// short-circuiting to unsupported_platform. See its doc comment.
	cap := buildCapability(syntheticRemoteOp, false, "", reach, nil, false, nil, nil, now)
	if !cap.PlatformSupported {
		t.Error("PlatformSupported = false; want true — a down cluster is still a supported target class")
	}
	if cap.Reachable == nil || *cap.Reachable {
		t.Fatalf("Reachable = %v; want false", cap.Reachable)
	}
	if cap.ReasonCode != ReasonUnreachable {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonUnreachable)
	}
}

func TestCapabilities_StaleProbeYieldsNullReachable(t *testing.T) {
	now := time.Now()
	staleProbe := now.Add(-5 * time.Minute) // > 3x60s = 180s staleness threshold
	getter := &fakeClusterRecordGetter{rec: &store.ClusterRecord{
		ID: "remote-1", Status: "connected", LastProbedAt: &staleProbe,
	}}

	reach := resolveReachability(context.Background(), getter, false, "remote-1", now)
	if reach.reachable != nil {
		t.Fatalf("resolveReachability reachable = %v; want nil for a 5-minute-old probe", *reach.reachable)
	}

	// syntheticRemoteOp, not a real operation — see its doc comment.
	cap := buildCapability(syntheticRemoteOp, false, "", reach, nil, false, nil, nil, now)
	if cap.Reachable != nil {
		t.Fatalf("Reachable = %v; want nil", *cap.Reachable)
	}
	if cap.ReasonCode != ReasonStaleObservation {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonStaleObservation)
	}
}

func TestCapabilities_DiscoveryPartialErrorIsNotMissing(t *testing.T) {
	// nodes (core/v1) loads fine; external-secrets.io/v1beta1 500s. Real
	// client-go discovery therefore returns a non-nil partial resource list
	// alongside a non-nil *ErrGroupDiscoveryFailed — the exact shape
	// resolveGVR (yaml/handler.go) and fetchDiscoveryLists must tolerate.
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true, hasESO: true, failESO: true}, resources.NewAlwaysAllowAccessChecker())
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	dash := findCapability(t, body, "dashboard.summary")

	if dash.DiscoveryPresent == nil || !*dash.DiscoveryPresent {
		t.Fatalf("dashboard.summary DiscoveryPresent = %v; want true — nodes loaded fine despite the external-secrets.io group failing", dash.DiscoveryPresent)
	}
	if dash.ReasonCode == ReasonDiscoveryMissing || dash.ReasonCode == ReasonDiscoveryUnavailable {
		t.Errorf("dashboard.summary ReasonCode = %q; a resource that DID load must not be reported missing/unavailable just because a DIFFERENT group's discovery failed", dash.ReasonCode)
	}
}

// TestClassifyTargetSchemaErr pins the three-way classification
// TargetSchemaFor errors are mapped through (step 2d / brief A3): a genuine
// "no such row" (pgx.ErrNoRows, as ClusterStore.Get's %w chain propagates it
// unwrapped) is cluster_unknown; ClusterRouter.requireClusterStore's "no
// cluster store" message is db_unavailable; everything else (decrypt, SSRF
// block, TLS policy failure) falls into the credentials_invalid catch-all.
func TestClassifyTargetSchemaErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ReasonCode
	}{
		{
			name: "not found propagates as cluster_unknown",
			err:  fmt.Errorf("cluster %s not found: %w", "remote-1", pgx.ErrNoRows),
			want: ReasonClusterUnknown,
		},
		{
			name: "no cluster store wired is db_unavailable",
			err:  errors.New(`non-local clusterID "remote-1" requested but ClusterRouter has no cluster store — remote routing unavailable`),
			want: ReasonDBUnavailable,
		},
		{
			name: "decrypt failure falls into the credentials_invalid catch-all",
			err:  fmt.Errorf("decrypting auth data for cluster %s: %w", "remote-1", errors.New("cipher: message authentication failed")),
			want: ReasonCredentialsInvalid,
		},
		{
			name: "SSRF block falls into the credentials_invalid catch-all",
			err:  fmt.Errorf("cluster %s URL blocked: %w", "remote-1", errors.New("URL resolves to private address")),
			want: ReasonCredentialsInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTargetSchemaErr(tt.err); got != tt.want {
				t.Errorf("classifyTargetSchemaErr(%v) = %q; want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestCapabilities_ReasonCodesAreClosed table-drives every operation against
// a representative scenario for every input combination buildCapability can
// see, and asserts every emitted reasonCode is a member of the closed set —
// and that a not-platform-supported operation always reports
// unsupported_platform regardless of what else the scenario claims about it.
// TestCapabilities_ReasonCodesAreClosed table-drives every REAL operation
// (capabilityOperations) against a set of representative scenarios and
// asserts an EXACT expected reasonCode verdict for both a Probe-carrying op
// (dashboard.summary, eso.write) and a plain one — not just "is a member of
// the valid set", which a buildCapability that always returned "ok" would
// still pass. Probe-carrying and plain ops legitimately diverge on the two
// "local discovery ..." scenarios (a plain op has no GVR to probe and so
// ignores discoveryPresent/discoveryUnavailable entirely), so each scenario
// carries two expectations, not one.
//
// Task review round 1 (finding #3) made every real row RemoteSupported:
// false, so every "remote" scenario below correctly asserts
// unsupported_platform for BOTH probe and plain real ops — that is not a
// vacuous check, it is the assertion that finding #3's fix actually landed
// on every row, probe-carrying or not. It does mean no REAL operation can
// exercise the reachable/discovery/authz priority chain on the remote
// class any more, which is why TestCapabilities_ReasonCodesAreClosed_Remote
// (below) pins that logic against the test-only synthetic ops instead.
func TestCapabilities_ReasonCodesAreClosed(t *testing.T) {
	now := time.Now()
	trueVal, falseVal := true, false

	scenarios := []struct {
		name                 string
		isLocal              bool
		globalReason         ReasonCode
		reach                reachabilityResult
		discoveryPresent     *bool
		discoveryUnavailable bool
		authorized           *bool
		authErr              error
		wantProbeOp          ReasonCode // expected reasonCode for an op with Probe != nil
		wantPlainOp          ReasonCode // expected reasonCode for an op with Probe == nil
	}{
		{
			name: "local ok", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &trueVal, authorized: &trueVal,
			wantProbeOp: ReasonOK, wantPlainOp: ReasonOK,
		},
		{
			name: "local forbidden", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &trueVal, authorized: &falseVal,
			wantProbeOp: ReasonForbidden, wantPlainOp: ReasonForbidden,
		},
		{
			name: "local authz unknown", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &trueVal, authErr: errors.New("SAR failed"),
			wantProbeOp: ReasonAuthzUnknown, wantPlainOp: ReasonAuthzUnknown,
		},
		{
			// Probe-carrying ops see discoveryPresent: false and report
			// discovery_missing BEFORE ever reaching the (still-allowed)
			// authz check; plain ops have no GVR to probe, ignore
			// discoveryPresent entirely, and fall straight through to ok.
			name: "local discovery missing", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &falseVal, authorized: &trueVal,
			wantProbeOp: ReasonDiscoveryMissing, wantPlainOp: ReasonOK,
		},
		{
			name: "local discovery unavailable", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryUnavailable: true, authorized: &trueVal,
			wantProbeOp: ReasonDiscoveryUnavailable, wantPlainOp: ReasonOK,
		},
		{
			name: "reachable but authorized never computed", isLocal: true,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &trueVal,
			wantProbeOp: ReasonAuthzUnknown, wantPlainOp: ReasonAuthzUnknown,
		},
		{
			// Every real row is RemoteSupported: false (finding #3): both
			// probe-carrying and plain ops must report unsupported_platform
			// here regardless of what reach/discovery/authorized claim.
			name: "remote — every real row is unsupported today", isLocal: false,
			reach: reachabilityResult{reachable: &trueVal, observedAt: now}, discoveryPresent: &trueVal, authorized: &trueVal,
			wantProbeOp: ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
		{
			name: "remote unreachable — still unsupported today", isLocal: false,
			reach:       reachabilityResult{reachable: &falseVal, observedAt: now},
			wantProbeOp: ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
		{
			name: "remote stale — still unsupported today", isLocal: false,
			reach:       reachabilityResult{observedAt: now},
			wantProbeOp: ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
		{
			name: "remote cluster_unknown — still unsupported today", isLocal: false,
			globalReason: ReasonClusterUnknown,
			wantProbeOp:  ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
		{
			name: "remote credentials_invalid — still unsupported today", isLocal: false,
			globalReason: ReasonCredentialsInvalid,
			wantProbeOp:  ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
		{
			name: "remote db_unavailable — still unsupported today", isLocal: false,
			globalReason: ReasonDBUnavailable,
			wantProbeOp:  ReasonUnsupportedPlatform, wantPlainOp: ReasonUnsupportedPlatform,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			for _, op := range capabilityOperations {
				cap := buildCapability(op, sc.isLocal, sc.globalReason, sc.reach,
					sc.discoveryPresent, sc.discoveryUnavailable, sc.authorized, sc.authErr, now)

				want := sc.wantPlainOp
				if op.Probe != nil {
					want = sc.wantProbeOp
				}
				if cap.ReasonCode != want {
					t.Errorf("operation %q (Probe!=nil: %v): reasonCode = %q; want %q", op.ID, op.Probe != nil, cap.ReasonCode, want)
				}
				if !validReasonCodes[cap.ReasonCode] {
					t.Errorf("operation %q scenario %q produced reasonCode %q, which is not in the valid set", op.ID, sc.name, cap.ReasonCode)
				}
			}
		})
	}
}

// TestCapabilities_ReasonCodesAreClosed_Remote pins buildCapability's
// reachable -> discovery -> authz -> ok priority chain on the REMOTE class
// with real verdicts, using the test-only syntheticRemoteOp /
// syntheticRemoteProbeOp (see their doc comment): as of finding #3, no real
// operation is RemoteSupported: true today, so none of unreachable,
// stale_observation, cluster_unknown, credentials_invalid, db_unavailable,
// forbidden, authz_unknown, discovery_missing, discovery_unavailable, or ok
// could otherwise be positively verdict-tested on the remote branch any
// more — every real op would short-circuit to unsupported_platform first.
func TestCapabilities_ReasonCodesAreClosed_Remote(t *testing.T) {
	now := time.Now()
	trueVal, falseVal := true, false

	t.Run("plain op", func(t *testing.T) {
		scenarios := []struct {
			name         string
			globalReason ReasonCode
			reach        reachabilityResult
			authorized   *bool
			authErr      error
			want         ReasonCode
		}{
			{name: "unreachable", reach: reachabilityResult{reachable: &falseVal, observedAt: now}, want: ReasonUnreachable},
			{name: "stale", reach: reachabilityResult{observedAt: now}, want: ReasonStaleObservation},
			{name: "cluster_unknown", globalReason: ReasonClusterUnknown, want: ReasonClusterUnknown},
			{name: "credentials_invalid", globalReason: ReasonCredentialsInvalid, want: ReasonCredentialsInvalid},
			{name: "db_unavailable", globalReason: ReasonDBUnavailable, want: ReasonDBUnavailable},
			{name: "forbidden", reach: reachabilityResult{reachable: &trueVal, observedAt: now}, authorized: &falseVal, want: ReasonForbidden},
			{name: "authz_unknown", reach: reachabilityResult{reachable: &trueVal, observedAt: now}, authErr: errors.New("SAR failed"), want: ReasonAuthzUnknown},
			{name: "ok", reach: reachabilityResult{reachable: &trueVal, observedAt: now}, authorized: &trueVal, want: ReasonOK},
		}
		for _, sc := range scenarios {
			t.Run(sc.name, func(t *testing.T) {
				cap := buildCapability(syntheticRemoteOp, false, sc.globalReason, sc.reach, nil, false, sc.authorized, sc.authErr, now)
				if cap.ReasonCode != sc.want {
					t.Errorf("reasonCode = %q; want %q", cap.ReasonCode, sc.want)
				}
			})
		}
	})

	t.Run("probe op", func(t *testing.T) {
		scenarios := []struct {
			name                 string
			discoveryPresent     *bool
			discoveryUnavailable bool
			authorized           *bool
			want                 ReasonCode
		}{
			{name: "discovery missing", discoveryPresent: &falseVal, authorized: &trueVal, want: ReasonDiscoveryMissing},
			{name: "discovery unavailable", discoveryUnavailable: true, authorized: &trueVal, want: ReasonDiscoveryUnavailable},
			{name: "ok", discoveryPresent: &trueVal, authorized: &trueVal, want: ReasonOK},
		}
		reach := reachabilityResult{reachable: &trueVal, observedAt: now}
		for _, sc := range scenarios {
			t.Run(sc.name, func(t *testing.T) {
				cap := buildCapability(syntheticRemoteProbeOp, false, "", reach, sc.discoveryPresent, sc.discoveryUnavailable, sc.authorized, nil, now)
				if cap.ReasonCode != sc.want {
					t.Errorf("reasonCode = %q; want %q", cap.ReasonCode, sc.want)
				}
			})
		}
	})
}
