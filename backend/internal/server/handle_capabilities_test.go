package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// Every operation's LocalSupported is true (task review, whole-branch
	// pass, Minor #10) — this is the production branch that would break if
	// a future edit accidentally flipped one, since nothing else in this
	// file asserts platformSupported across the whole table for local.
	for _, c := range body.Capabilities {
		if !c.PlatformSupported {
			t.Errorf("operation %q: PlatformSupported = false; want true for the local cluster", c.Operation)
		}
	}
}

// TestCapabilities_RemoteRequiresAdmin pins the ONE thing standing between a
// non-admin user and a remote cluster's capability disclosure: routes.go
// mounts this endpoint outside the admin-only /clusters group specifically
// because middleware.ClusterContext already admin-gates any non-local
// X-Cluster-ID (middleware/cluster.go) before the handler ever runs. That
// claim was previously prose only — every other test in this file uses
// either "local" or an admin token. Without this test, a future refactor
// that moved this route out of the ClusterContext-wrapped group would leave
// every other test green while silently opening remote capability
// disclosure to any authenticated viewer (task review, whole-branch pass,
// Important #1).
func TestCapabilities_RemoteRequiresAdmin(t *testing.T) {
	srv := testServer(t)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false) // non-admin

	w := capabilitiesRequest(t, srv, token, "remote-1", "remote-1")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 for a non-admin user asking about a remote cluster, body=%s", w.Code, w.Body.String())
	}
}

// TestCapabilities_NoAccessCheckerYieldsAuthzUnknown exercises the
// errNoAccessChecker branch through the real router (task review,
// whole-branch pass, Minor #10): a bare testServer(t) never sets
// ResourceHandler, so a local request must still succeed (200) with every
// operation's authorized dimension null and reasonCode: authz_unknown,
// rather than panicking on a nil AccessChecker.
func TestCapabilities_NoAccessCheckerYieldsAuthzUnknown(t *testing.T) {
	srv := testServer(t) // bare: no ResourceHandler, no ClusterRouter, no ClusterStore
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)
	cap := findCapability(t, body, "yaml.validate")

	if cap.Authorized != nil {
		t.Fatalf("Authorized = %v; want nil — no ResourceHandler/AccessChecker is wired", cap.Authorized)
	}
	if cap.ReasonCode != ReasonAuthzUnknown {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonAuthzUnknown)
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

// TestCapabilities_ForbiddenIsNotUnsupported pins the D3 separation an RBAC
// denial must keep from unsupported_platform, on BOTH scopes.
//
// This test used to assert yaml.apply (a NAMESPACED op) came back
// forbidden/false under an always-deny checker. That assertion encoded the
// pre-fix behavior review finding #2 identified: the SAR is issued
// cluster-wide, so a negative on a namespaced resource is not proof of
// denial and is now reported authz_unknown/null. The test's actual subject —
// "a denial is not 'unsupported'" — is unchanged and is now carried by
// dashboard.summary, whose nodes probe is genuinely cluster-scoped, with
// yaml.apply kept as the namespaced counterpart.
func TestCapabilities_ForbiddenIsNotUnsupported(t *testing.T) {
	// hasNodes so dashboard.summary's GVR probe succeeds — discovery_missing
	// outranks the authz dimension and would mask the verdict under test.
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, resources.NewAlwaysDenyAccessChecker())
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)

	clusterScoped := findCapability(t, body, "dashboard.summary")
	if !clusterScoped.PlatformSupported {
		t.Error("dashboard.summary PlatformSupported = false; want true (RBAC denial is not the same as unsupported)")
	}
	if clusterScoped.Authorized == nil || *clusterScoped.Authorized {
		t.Fatalf("dashboard.summary Authorized = %v; want false — nodes is cluster-scoped, so the cluster-wide SAR asked an exact question", clusterScoped.Authorized)
	}
	if clusterScoped.ReasonCode != ReasonForbidden {
		t.Errorf("dashboard.summary ReasonCode = %q; want %q", clusterScoped.ReasonCode, ReasonForbidden)
	}

	namespaced := findCapability(t, body, "yaml.apply")
	if !namespaced.PlatformSupported {
		t.Error("yaml.apply PlatformSupported = false; want true")
	}
	if namespaced.Authorized != nil {
		t.Fatalf("yaml.apply Authorized = %v; want null — configmaps is namespaced and the probe was cluster-wide", *namespaced.Authorized)
	}
	if namespaced.ReasonCode != ReasonAuthzUnknown {
		t.Errorf("yaml.apply ReasonCode = %q; want %q", namespaced.ReasonCode, ReasonAuthzUnknown)
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

	// hasNodes so dashboard.summary reaches its authz dimension (see
	// TestCapabilities_ForbiddenIsNotUnsupported).
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, ac)

	aliceToken := capabilitiesIssueToken(t, srv, "alice", false)
	bobToken := capabilitiesIssueToken(t, srv, "bob", false)

	aliceBody := decodeCapabilities(t, capabilitiesRequest(t, srv, aliceToken, "local", ""))
	bobBody := decodeCapabilities(t, capabilitiesRequest(t, srv, bobToken, "local", ""))

	aliceCap := findCapability(t, aliceBody, "yaml.apply")
	bobCap := findCapability(t, bobBody, "yaml.apply")

	if aliceCap.Authorized == nil || !*aliceCap.Authorized {
		t.Fatalf("alice authorized = %v; want true", aliceCap.Authorized)
	}
	if aliceCap.ReasonCode != ReasonOK {
		t.Errorf("alice reasonCode = %q; want %q", aliceCap.ReasonCode, ReasonOK)
	}
	// bob's denial on a NAMESPACED op is reported unknown, not forbidden —
	// the SAR was cluster-wide. This assertion previously read
	// forbidden/false; see TestCapabilities_ForbiddenIsNotUnsupported's
	// comment for why it changed. The test's subject (two identities get
	// genuinely different views) is unaffected: alice's row is ok/true.
	if bobCap.Authorized != nil {
		t.Fatalf("bob authorized = %v; want null for a cluster-wide denial on a namespaced op", *bobCap.Authorized)
	}
	if bobCap.ReasonCode != ReasonAuthzUnknown {
		t.Errorf("bob reasonCode = %q; want %q", bobCap.ReasonCode, ReasonAuthzUnknown)
	}

	// The cluster-scoped row still carries a definite per-identity verdict,
	// which is what keeps this test a real "own permission view" check
	// rather than one where the denied identity says only "unknown".
	aliceNodes := findCapability(t, aliceBody, "dashboard.summary")
	bobNodes := findCapability(t, bobBody, "dashboard.summary")
	if aliceNodes.Authorized == nil || !*aliceNodes.Authorized || aliceNodes.ReasonCode != ReasonOK {
		t.Errorf("alice dashboard.summary = (authorized %v, reason %q); want (true, %q)", aliceNodes.Authorized, aliceNodes.ReasonCode, ReasonOK)
	}
	if bobNodes.Authorized == nil || *bobNodes.Authorized || bobNodes.ReasonCode != ReasonForbidden {
		t.Errorf("bob dashboard.summary = (authorized %v, reason %q); want (false, %q)", bobNodes.Authorized, bobNodes.ReasonCode, ReasonForbidden)
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
	// hasNodes so dashboard.summary — the cluster-scoped row that still
	// produces a definite forbidden verdict — reaches its authz dimension.
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, ac)
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)

	clusterScoped := findCapability(t, body, "dashboard.summary")
	if clusterScoped.Authorized == nil || *clusterScoped.Authorized {
		t.Fatalf("dashboard.summary Authorized = %v; want false — CanAccess would short-circuit this predicate-fake AccessChecker to an unconditional allow (access.go), "+
			"silently ignoring the predicate's denial; only CanAccessGroupResource actually consults it", clusterScoped.Authorized)
	}
	if clusterScoped.ReasonCode != ReasonForbidden {
		t.Errorf("dashboard.summary ReasonCode = %q; want %q", clusterScoped.ReasonCode, ReasonForbidden)
	}

	// yaml.apply is namespaced, so the same predicate denial is reported as
	// unknown rather than forbidden. It still falsifies the CanAccess
	// regression — that path would report ok/true here — which is why the
	// assertion was updated rather than dropped when finding #2 was fixed.
	namespaced := findCapability(t, body, "yaml.apply")
	if namespaced.Authorized != nil {
		t.Fatalf("yaml.apply Authorized = %v; want null (a regression to CanAccess would report true/ok here)", *namespaced.Authorized)
	}
	if namespaced.ReasonCode != ReasonAuthzUnknown {
		t.Errorf("yaml.apply ReasonCode = %q; want %q", namespaced.ReasonCode, ReasonAuthzUnknown)
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

	// The other half of the same partial result, and the half review finding
	// #12 was about: eso.write probes external-secrets.io — precisely the
	// group that failed. Tolerating the partial list is right for nodes and
	// wrong here, because "not in the list" and "we never got the list" are
	// indistinguishable for the failed group. The row must report
	// discovery_unavailable (unknown) with a null discoveryPresent, never
	// discovery_missing (a definite claim the CRD is not installed) — the
	// exact collapse discovery_unavailable exists to prevent.
	eso := findCapability(t, body, "eso.write")
	if eso.DiscoveryPresent != nil {
		t.Fatalf("eso.write DiscoveryPresent = %v; want null — external-secrets.io is the group whose discovery failed, so its absence from the partial list is no evidence", *eso.DiscoveryPresent)
	}
	if eso.ReasonCode != ReasonDiscoveryUnavailable {
		t.Errorf("eso.write ReasonCode = %q; want %q", eso.ReasonCode, ReasonDiscoveryUnavailable)
	}
}

// TestCapabilities_DiscoveryMissingStillReportsMissing is the negative
// control for the test above: when discovery loads cleanly and the CRD
// genuinely is not installed, eso.write must still report the DEFINITE
// discovery_missing. Without this, finding #12's fix could have been
// "report unavailable whenever the probe finds nothing", which would have
// destroyed the distinction from the other side.
func TestCapabilities_DiscoveryMissingStillReportsMissing(t *testing.T) {
	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true, hasESO: false}, resources.NewAlwaysAllowAccessChecker())
	token := capabilitiesIssueToken(t, srv, "viewer-1", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	eso := findCapability(t, decodeCapabilities(t, w), "eso.write")

	if eso.DiscoveryPresent == nil || *eso.DiscoveryPresent {
		t.Fatalf("eso.write DiscoveryPresent = %v; want false — discovery answered fully and the CRD is absent", eso.DiscoveryPresent)
	}
	if eso.ReasonCode != ReasonDiscoveryMissing {
		t.Errorf("eso.write ReasonCode = %q; want %q", eso.ReasonCode, ReasonDiscoveryMissing)
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
		// Review finding #7 — three shapes that used to hit the
		// credentials_invalid catch-all and tell the operator their stored
		// credentials were bad when nothing was wrong with them.
		{
			// ValidateRemoteURLContext fails closed on a lookup failure and
			// wraps the resolver's *net.DNSError verbatim.
			name: "DNS failure is unreachable, not credentials_invalid",
			err: fmt.Errorf("cluster %s URL blocked: %w", "remote-1",
				fmt.Errorf("DNS resolution failed for %s: %w", "api.example.invalid",
					&net.DNSError{Err: "no such host", Name: "api.example.invalid", IsNotFound: true})),
			want: ReasonUnreachable,
		},
		{
			// The 30s ceiling TargetSchemaFor puts on its cold-miss path
			// (and remoteConfig's own) expires while dialing the API server.
			name: "deadline exceeded is unreachable, not credentials_invalid",
			err:  fmt.Errorf("creating discovery client for cluster %s: %w", "remote-1", context.DeadlineExceeded),
			want: ReasonUnreachable,
		},
		{
			name: "cancelled context is unreachable, not credentials_invalid",
			err:  fmt.Errorf("creating discovery client for cluster %s: %w", "remote-1", context.Canceled),
			want: ReasonUnreachable,
		},
		{
			name: "transport failure is unreachable, not credentials_invalid",
			err: fmt.Errorf("probing cluster %s: %w", "remote-1",
				&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}),
			want: ReasonUnreachable,
		},
		{
			// The cold-miss path's cluster-record read hits a live database
			// error that is NOT pgx.ErrNoRows. The row may well exist; the
			// registry is what's broken.
			name: "postgres server error is db_unavailable, not credentials_invalid",
			err: fmt.Errorf("cluster %s not found: %w", "remote-1",
				fmt.Errorf("getting cluster %s: %w", "remote-1", &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"})),
			want: ReasonDBUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTargetSchemaErr(tt.err); got != tt.want {
				t.Errorf("classifyTargetSchemaErr(%v) = %q; want %q", tt.err, got, tt.want)
			}
		})
	}

	// A Postgres CONNECT failure is the case that pins the ORDER of the
	// checks, not just their existence: *pgconn.ConnectError wraps a
	// net.OpError, so a classifier that asked "is this a network error?"
	// before "is this a database error?" would report the CLUSTER
	// unreachable when it is the registry that is unreachable.
	//
	// It cannot be constructed by hand — ConnectError's wrapped error is
	// unexported and its Error() method dereferences it — so this drives a
	// real refused connection against a loopback port nothing listens on.
	t.Run("postgres connect failure is db_unavailable, not unreachable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, connErr := pgconn.Connect(ctx, "postgres://kubecenter:kubecenter@127.0.0.1:1/kubecenter?sslmode=disable")
		var typed *pgconn.ConnectError
		if connErr == nil || !errors.As(connErr, &typed) {
			t.Skipf("environment did not produce a *pgconn.ConnectError for a refused loopback connection (got %v); "+
				"the ordering claim is unverified here", connErr)
		}

		wrapped := fmt.Errorf("cluster %s not found: %w", "remote-1", connErr)
		if got := classifyTargetSchemaErr(wrapped); got != ReasonDBUnavailable {
			t.Errorf("classifyTargetSchemaErr(%v) = %q; want %q — the database is what failed, not the cluster", wrapped, got, ReasonDBUnavailable)
		}
	})
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

// TestCapabilities_RegistryReadFailureIsDBUnavailable pins review finding
// #1: resolveReachability used to discard the error from the cluster-record
// read, returning a bare reachabilityResult that buildCapability rendered as
// stale_observation. A Postgres outage therefore looked like a lagging
// prober — the operator waits for a probe cycle that will never come instead
// of investigating the database. db_unavailable exists for exactly this.
func TestCapabilities_RegistryReadFailureIsDBUnavailable(t *testing.T) {
	now := time.Now()

	t.Run("read error", func(t *testing.T) {
		getter := &fakeClusterRecordGetter{err: errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")}

		reach := resolveReachability(context.Background(), getter, false, "remote-1", now)
		if reach.reachable != nil {
			t.Fatalf("reachable = %v; want nil — a failed registry read is not a reachability verdict", *reach.reachable)
		}
		if reach.reason != ReasonDBUnavailable {
			t.Fatalf("reason = %q; want %q", reach.reason, ReasonDBUnavailable)
		}

		cap := buildCapability(syntheticRemoteOp, false, "", reach, nil, false, nil, nil, now)
		if cap.ReasonCode != ReasonDBUnavailable {
			t.Errorf("ReasonCode = %q; want %q — stale_observation would send the operator to wait out an outage", cap.ReasonCode, ReasonDBUnavailable)
		}
	})

	t.Run("nil getter", func(t *testing.T) {
		reach := resolveReachability(context.Background(), nil, false, "remote-1", now)
		if reach.reachable != nil || reach.reason != ReasonDBUnavailable {
			t.Fatalf("reach = (reachable %v, reason %q); want (nil, %q)", reach.reachable, reach.reason, ReasonDBUnavailable)
		}
	})

	// The genuine stale case must keep reporting stale_observation — the
	// fix must not turn every null reachable into db_unavailable.
	t.Run("stale probe still reports stale_observation", func(t *testing.T) {
		staleProbe := now.Add(-5 * time.Minute)
		getter := &fakeClusterRecordGetter{rec: &store.ClusterRecord{
			ID: "remote-1", Status: k8s.StatusConnected.String(), LastProbedAt: &staleProbe,
		}}

		reach := resolveReachability(context.Background(), getter, false, "remote-1", now)
		if reach.reason != "" {
			t.Fatalf("reason = %q; want empty for an ordinary stale probe", reach.reason)
		}
		cap := buildCapability(syntheticRemoteOp, false, "", reach, nil, false, nil, nil, now)
		if cap.ReasonCode != ReasonStaleObservation {
			t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonStaleObservation)
		}
	})
}

// namespaceAwareClientFactory models the identity review findings #2/#6/#10
// are about: one that holds an ordinary namespaced Role (edit in
// allowNamespace) and nothing cluster-wide. Unlike perIdentityClientFactory
// it INSPECTS the created SelfSubjectAccessReview's ResourceAttributes, so
// the namespace dimension of the probe is actually exercised — the flat
// per-username verdict of the older fixture is precisely why a cluster-wide
// probe against namespaced resources was invisible to this suite.
type namespaceAwareClientFactory struct {
	allowNamespace string

	mu       sync.Mutex
	observed []authorizationv1.ResourceAttributes
}

func (f *namespaceAwareClientFactory) ClientForUser(string, []string) (kubernetes.Interface, error) {
	cs := fakekube.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(clienttesting.CreateAction)
		if !ok {
			return false, nil, fmt.Errorf("unexpected action %T for selfsubjectaccessreviews", action)
		}
		sar, ok := create.GetObject().(*authorizationv1.SelfSubjectAccessReview)
		if !ok {
			return false, nil, fmt.Errorf("unexpected object %T in SAR create", create.GetObject())
		}
		attrs := sar.Spec.ResourceAttributes
		if attrs == nil {
			return false, nil, errors.New("SAR carried no ResourceAttributes")
		}
		f.mu.Lock()
		f.observed = append(f.observed, *attrs)
		f.mu.Unlock()

		// The whole point: this identity is allowed in exactly one
		// namespace, so an empty (cluster-wide) namespace is denied.
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Allowed: attrs.Namespace == f.allowNamespace},
		}, nil
	})
	return cs, nil
}

func (f *namespaceAwareClientFactory) namespacesProbed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.observed))
	for _, a := range f.observed {
		out = append(out, a.Namespace)
	}
	return out
}

// TestCapabilities_NamespacedDenialIsUnknownNotForbidden is the regression
// test for review findings #2, #6 and #10. An identity whose RBAC is an
// ordinary namespaced Role is denied by the CLUSTER-WIDE SAR this endpoint
// issues (empty namespace on a namespaced resource means "in all
// namespaces"), yet can perform the operation perfectly well in its own
// namespace. Reporting that denial as authorized: false / forbidden told
// every namespace-scoped user — including the app-admin of finding #6, who
// can run log search — that they could not do things they could.
//
// The cluster-scoped row is the negative control: nodes is not namespaced,
// so the same denial there IS real and must stay forbidden. Without that
// half, "report everything as unknown" would pass.
func TestCapabilities_NamespacedDenialIsUnknownNotForbidden(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	factory := &namespaceAwareClientFactory{allowNamespace: "team-a"}
	ac := resources.NewAccessChecker(factory, logger)

	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true, hasESO: true}, ac)
	token := capabilitiesIssueToken(t, srv, "app-admin", false)

	w := capabilitiesRequest(t, srv, token, "local", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeCapabilities(t, w)

	// Every namespaced row — the yaml.* configmaps rows, the pods rows, the
	// pods/exec + pods/log subresource rows (finding #10's wildcard shape)
	// and externalsecrets — reports unknown, not a denial.
	for _, op := range []string{
		"yaml.validate", "yaml.diff", "yaml.export", "yaml.apply",
		"resources.counts", "pod.exec", "logs.stream", "logs.search",
		"flows.stream", "eso.write",
	} {
		cap := findCapability(t, body, op)
		if cap.Authorized != nil {
			t.Errorf("%s Authorized = %v; want null — a cluster-wide denial on a namespaced resource is not proof this identity cannot act", op, *cap.Authorized)
		}
		if cap.ReasonCode != ReasonAuthzUnknown {
			t.Errorf("%s ReasonCode = %q; want %q", op, cap.ReasonCode, ReasonAuthzUnknown)
		}
	}

	nodes := findCapability(t, body, "dashboard.summary")
	if nodes.Authorized == nil || *nodes.Authorized {
		t.Fatalf("dashboard.summary Authorized = %v; want false — nodes is cluster-scoped, so this denial is real", nodes.Authorized)
	}
	if nodes.ReasonCode != ReasonForbidden {
		t.Errorf("dashboard.summary ReasonCode = %q; want %q", nodes.ReasonCode, ReasonForbidden)
	}

	// Pin what was actually asked: every probe carried the empty
	// (cluster-wide) namespace. If a future change starts passing a real
	// namespace, this fixture's verdicts flip and the assertions above
	// become meaningless without anyone noticing.
	probed := factory.namespacesProbed()
	if len(probed) == 0 {
		t.Fatal("no SelfSubjectAccessReview reached the fixture; the namespace dimension was never exercised")
	}
	for _, ns := range probed {
		if ns != "" {
			t.Errorf("SAR namespace = %q; want the empty cluster-wide namespace — update this test alongside any ?namespace= support", ns)
		}
	}
}

// TestAuthorizedFromClusterWideSAR pins the scope-dependent reading of a
// cluster-wide SAR verdict in isolation from the handler.
func TestAuthorizedFromClusterWideSAR(t *testing.T) {
	namespaced := capabilityOp{ID: "ns", AuthResource: "configmaps"}
	clusterScoped := capabilityOp{ID: "cs", AuthResource: "nodes", ClusterScoped: true}

	tests := []struct {
		name    string
		op      capabilityOp
		allowed bool
		want    *bool
	}{
		{"namespaced allow implies every namespace", namespaced, true, boolPtr(true)},
		{"namespaced denial is unknown", namespaced, false, nil},
		{"cluster-scoped allow", clusterScoped, true, boolPtr(true)},
		{"cluster-scoped denial is real", clusterScoped, false, boolPtr(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authorizedFromClusterWideSAR(tt.op, tt.allowed)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("authorized = %v; want nil", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("authorized = nil; want %v", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Fatalf("authorized = %v; want %v", *got, *tt.want)
			}
		})
	}
}

// TestCapabilityOperations_ScopePinned forces every operation row to have
// made a conscious namespaced-vs-cluster-scoped choice. capabilityOp's zero
// value means namespaced (the safe direction), so a new row that forgets the
// field compiles and behaves plausibly; this test is what makes the omission
// visible. Three later units (U9a, U9b, U10) are scheduled to edit this
// table.
func TestCapabilityOperations_ScopePinned(t *testing.T) {
	// nodes is the only cluster-scoped resource probed today. configmaps,
	// pods, pods/exec, pods/log and externalsecrets are all namespaced.
	wantClusterScoped := map[string]bool{
		"dashboard.summary": true,
	}

	seen := map[string]bool{}
	for _, op := range capabilityOperations {
		seen[op.ID] = true
		if got := op.ClusterScoped; got != wantClusterScoped[op.ID] {
			t.Errorf("operation %q (AuthResource %q): ClusterScoped = %v; want %v",
				op.ID, op.AuthResource, got, wantClusterScoped[op.ID])
		}
	}
	for id := range wantClusterScoped {
		if !seen[id] {
			t.Errorf("expected cluster-scoped operation %q is no longer in capabilityOperations; update this test", id)
		}
	}
}

// withRemoteSupported flips RemoteSupported to true on the named PRODUCTION
// operation rows for the duration of one test, restoring the table via
// t.Cleanup so no mutated global leaks into a sibling
// (TestCapabilityContractParity and TestCapabilities_ReasonCodesAreClosed
// both read this table).
//
// It replaces the slice with a copy rather than mutating rows in place, so
// even a failed restore cannot leave a half-edited row behind.
func withRemoteSupported(t *testing.T, ids ...string) {
	t.Helper()

	original := capabilityOperations
	swapped := make([]capabilityOp, len(original))
	copy(swapped, original)

	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	flipped := 0
	for i := range swapped {
		if wanted[swapped[i].ID] {
			swapped[i].RemoteSupported = true
			flipped++
		}
	}
	if flipped != len(ids) {
		t.Fatalf("withRemoteSupported: flipped %d of %d requested operations %v; a row was renamed or removed", flipped, len(ids), ids)
	}

	capabilityOperations = swapped
	t.Cleanup(func() { capabilityOperations = original })
}

// withCapabilityClusterGetter substitutes the reachability getter for the
// duration of one test. See capabilityClusterGetter's doc comment for why
// the seam exists.
func withCapabilityClusterGetter(t *testing.T, getter clusterRecordGetter) {
	t.Helper()
	original := capabilityClusterGetter
	capabilityClusterGetter = func(*Server) clusterRecordGetter { return getter }
	t.Cleanup(func() { capabilityClusterGetter = original })
}

// TestCapabilities_RemoteChainEndToEnd closes review finding #3: because
// every production row is RemoteSupported: false today, anySupported is
// false for any remote target and the entire remote chain — target
// resolution, reachability, discovery, the impersonated SAR — had never
// executed end to end. cluster_unknown, credentials_invalid, db_unavailable,
// unreachable and stale_observation were pinned only by direct calls to
// buildCapability against test-only synthetic rows, which cannot catch a
// handler that wires those inputs together wrongly.
//
// This drives REAL production rows (one plain, one GVR-probing) through the
// real HTTP handler with RemoteSupported temporarily flipped, exactly as
// U9a/U9b/U10 will flip them for good.
func TestCapabilities_RemoteChainEndToEnd(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-30 * time.Second) // inside the 180s freshness window
	stale := now.Add(-10 * time.Minute) // outside it

	tests := []struct {
		name           string
		getter         clusterRecordGetter
		checker        *resources.AccessChecker
		wantPlain      ReasonCode // yaml.validate — no GVR probe
		wantProbe      ReasonCode // dashboard.summary — probes core/v1 nodes
		wantAuthorized bool       // whether yaml.validate's authorized must be non-null
	}{
		{
			name:    "registry read failure",
			getter:  &fakeClusterRecordGetter{err: errors.New("connect: connection refused")},
			checker: resources.NewAlwaysAllowAccessChecker(),
			// Finding #1 end to end: a broken registry must not masquerade
			// as a lagging prober in the HTTP response either.
			wantPlain: ReasonDBUnavailable, wantProbe: ReasonDBUnavailable,
		},
		{
			name: "stale probe",
			getter: &fakeClusterRecordGetter{rec: &store.ClusterRecord{
				ID: "remote-1", Status: k8s.StatusConnected.String(), LastProbedAt: &stale,
			}},
			checker:   resources.NewAlwaysAllowAccessChecker(),
			wantPlain: ReasonStaleObservation, wantProbe: ReasonStaleObservation,
		},
		{
			name: "disconnected cluster",
			getter: &fakeClusterRecordGetter{rec: &store.ClusterRecord{
				ID: "remote-1", Status: k8s.StatusDisconnected.String(), LastProbedAt: &fresh,
			}},
			checker:   resources.NewAlwaysAllowAccessChecker(),
			wantPlain: ReasonUnreachable, wantProbe: ReasonUnreachable,
		},
		{
			name: "reachable, authorized",
			getter: &fakeClusterRecordGetter{rec: &store.ClusterRecord{
				ID: "remote-1", Status: k8s.StatusConnected.String(), LastProbedAt: &fresh,
			}},
			checker:   resources.NewAlwaysAllowAccessChecker(),
			wantPlain: ReasonOK,
			// No ClusterRouter is wired (A4's narrow case), so there is no
			// target schema to run discovery against: unknown, not missing.
			wantProbe:      ReasonDiscoveryUnavailable,
			wantAuthorized: true,
		},
		{
			name: "reachable, denied cluster-wide on a namespaced op",
			getter: &fakeClusterRecordGetter{rec: &store.ClusterRecord{
				ID: "remote-1", Status: k8s.StatusConnected.String(), LastProbedAt: &fresh,
			}},
			checker: resources.NewAlwaysDenyAccessChecker(),
			// yaml.validate probes configmaps (namespaced) — finding #2's
			// semantics apply on the remote class too.
			wantPlain: ReasonAuthzUnknown,
			wantProbe: ReasonDiscoveryUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withRemoteSupported(t, "yaml.validate", "dashboard.summary")
			withCapabilityClusterGetter(t, tt.getter)

			srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, tt.checker)
			// A non-nil ClusterStore keeps the A4 "no registry wired at all"
			// branch from firing; reachability reads through the substituted
			// getter, so the nil pool is never touched. ClusterRouter is
			// cleared to exercise A4's other, narrower branch.
			srv.ClusterStore = store.NewClusterStore(nil, "test-encryption-key")
			srv.ClusterRouter = nil

			token := capabilitiesIssueToken(t, srv, "admin-1", true)
			w := capabilitiesRequest(t, srv, token, "remote-1", "remote-1")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
			}
			body := decodeCapabilities(t, w)

			plain := findCapability(t, body, "yaml.validate")
			if !plain.PlatformSupported {
				t.Fatal("yaml.validate PlatformSupported = false; withRemoteSupported did not take effect")
			}
			if plain.ReasonCode != tt.wantPlain {
				t.Errorf("yaml.validate ReasonCode = %q; want %q", plain.ReasonCode, tt.wantPlain)
			}
			if tt.wantAuthorized && (plain.Authorized == nil || !*plain.Authorized) {
				t.Errorf("yaml.validate Authorized = %v; want true — the impersonated SAR must actually run on the remote branch", plain.Authorized)
			}

			probe := findCapability(t, body, "dashboard.summary")
			if probe.ReasonCode != tt.wantProbe {
				t.Errorf("dashboard.summary ReasonCode = %q; want %q", probe.ReasonCode, tt.wantProbe)
			}

			// Rows that were NOT flipped must still report the honest
			// unsupported_platform — the flip is per-row, not a global
			// "remote works now" switch.
			untouched := findCapability(t, body, "pod.exec")
			if untouched.PlatformSupported || untouched.ReasonCode != ReasonUnsupportedPlatform {
				t.Errorf("pod.exec = (platformSupported %v, reason %q); want (false, %q)",
					untouched.PlatformSupported, untouched.ReasonCode, ReasonUnsupportedPlatform)
			}

			for _, c := range body.Capabilities {
				if !validReasonCodes[c.ReasonCode] {
					t.Errorf("operation %q emitted reasonCode %q, which is not in the valid set", c.Operation, c.ReasonCode)
				}
			}
		})
	}
}

// TestCapabilities_RemoteNoClusterStoreIsDBUnavailable drives the other A4
// branch — no cluster registry wired at all — through the real handler with
// a production row flipped to RemoteSupported, rather than by handing
// buildCapability a globalReason directly.
func TestCapabilities_RemoteNoClusterStoreIsDBUnavailable(t *testing.T) {
	withRemoteSupported(t, "yaml.validate")

	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, resources.NewAlwaysAllowAccessChecker())
	srv.ClusterStore = nil

	token := capabilitiesIssueToken(t, srv, "admin-1", true)
	w := capabilitiesRequest(t, srv, token, "remote-1", "remote-1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}

	cap := findCapability(t, decodeCapabilities(t, w), "yaml.validate")
	if !cap.PlatformSupported {
		t.Fatal("PlatformSupported = false; withRemoteSupported did not take effect")
	}
	if cap.ReasonCode != ReasonDBUnavailable {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonDBUnavailable)
	}
}

// TestCapabilities_RemoteTargetResolutionFailureClassified drives the real
// ClusterRouter.TargetSchemaFor error path through the handler: the router
// has no cluster store, so resolving a remote target fails with
// requireClusterStore's load-bearing message and classifyTargetSchemaErr
// must map it to db_unavailable. Before this, classifyTargetSchemaErr was
// only ever called from a unit test with a hand-built error.
func TestCapabilities_RemoteTargetResolutionFailureClassified(t *testing.T) {
	withRemoteSupported(t, "yaml.validate")

	srv := newCapabilitiesTestServer(t, discoveryFixture{hasNodes: true}, resources.NewAlwaysAllowAccessChecker())
	// Non-nil so the A4 "no registry" branch does not pre-empt the router
	// call; the router itself was built with a nil store, so TargetSchemaFor
	// fails closed. Reachability is never reached (globalReason wins), so
	// the nil pool is never touched.
	srv.ClusterStore = store.NewClusterStore(nil, "test-encryption-key")

	token := capabilitiesIssueToken(t, srv, "admin-1", true)
	w := capabilitiesRequest(t, srv, token, "remote-1", "remote-1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}

	cap := findCapability(t, decodeCapabilities(t, w), "yaml.validate")
	if cap.ReasonCode != ReasonDBUnavailable {
		t.Errorf("ReasonCode = %q; want %q", cap.ReasonCode, ReasonDBUnavailable)
	}
	if cap.Reachable != nil {
		t.Errorf("Reachable = %v; want null — nothing is knowable once the target cannot be resolved", *cap.Reachable)
	}
}
