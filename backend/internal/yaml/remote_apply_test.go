package yaml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/restmapper"
	clienttesting "k8s.io/client-go/testing"
)

// Apply shares remote_test.go's fixtures: LOCAL is a real ClusterRouter over
// a counting discovery server, REMOTE is injected through clusterTargeter.
// Every test that must not mutate asserts on the fake dynamic clients' action
// logs, never on an error string.

type applyBody struct {
	Results []struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Action    string `json:"action"`
		Error     string `json:"error"`
	} `json:"results"`
	Summary struct {
		Total      int `json:"total"`
		Created    int `json:"created"`
		Configured int `json:"configured"`
		Unchanged  int `json:"unchanged"`
		Failed     int `json:"failed"`
	} `json:"summary"`
}

type errorBody struct {
	Error struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Reason  string         `json:"reason"`
		Extra   map[string]any `json:"extra"`
	} `json:"error"`
}

// recordingAuditLogger captures every entry so a test can assert on what
// reached the audit trail.
type recordingAuditLogger struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (l *recordingAuditLogger) Log(_ context.Context, e audit.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	return nil
}

func (l *recordingAuditLogger) snapshot() []audit.Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]audit.Entry(nil), l.entries...)
}

// applyURL builds /yaml/apply with the given query parameters.
func applyURL(params map[string]string) string {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	if len(q) == 0 {
		return "/yaml/apply"
	}
	return "/yaml/apply?" + q.Encode()
}

func (fx *fixture) apply(clusterID string, params map[string]string, body string) *httptest.ResponseRecorder {
	return serve(fx.handler.HandleApply, newRequest(http.MethodPost, applyURL(params), clusterID, body))
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("decoding error response %q: %v", w.Body.String(), err)
	}
	return e
}

// remoteSchemaFrom rebuilds the remote TargetSchema over disc, for a test
// that extends what the remote cluster advertises.
func remoteSchemaFrom(t *testing.T, disc *fakediscovery.FakeDiscovery) *k8s.TargetSchema {
	t.Helper()
	groups, err := restmapper.GetAPIGroupResources(disc)
	if err != nil {
		t.Fatalf("GetAPIGroupResources: %v", err)
	}
	return &k8s.TargetSchema{
		ClusterID:  remoteClusterID,
		Generation: remoteGeneration,
		Discovery:  disc,
		Mapper:     restmapper.NewDiscoveryRESTMapper(groups),
		Invalidate: func() {},
	}
}

// assertNoActions fails when dyn recorded anything at all: a refused apply
// must not even read from the target.
func assertNoActions(t *testing.T, name string, dyn *dynfake.FakeDynamicClient) {
	t.Helper()
	if n := len(dyn.Actions()); n != 0 {
		t.Errorf("%s dynamic client recorded %d actions %v; want 0", name, n, dyn.Actions())
	}
}

func patchedNames(dyn *dynfake.FakeDynamicClient) []string {
	var names []string
	for _, a := range dyn.Actions() {
		if p, ok := a.(clienttesting.PatchAction); ok {
			names = append(names, p.GetResource().Resource+"/"+p.GetName())
		}
	}
	return names
}

// --- Target routing ----------------------------------------------------------

func TestHandleApply_RemoteUsesTargetMapper(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := fx.apply(remoteClusterID, nil, widgetYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Total != 1 || body.Summary.Failed != 0 || body.Results[0].Action != "created" {
		t.Fatalf("apply = %+v; want the remote-only Widget created on the remote target", body)
	}
	if got := patchedNames(fx.remoteDyn); len(got) != 1 || got[0] != "widgets/gizmo" {
		t.Errorf("remote patches = %v; want exactly widgets/gizmo", got)
	}
	fx.local.assertUntouched(t)
}

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

const gadgetCRDYAML = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
spec:
  group: example.com
  names:
    kind: Gadget
    plural: gadgets
  scope: Namespaced
`

// A bundle that installs a CRD and then uses it must apply in one request:
// the CR's first mapping misses (the CRD was not in discovery when the cache
// was built), the target schema is refreshed, and the retry resolves it.
func TestHandleApply_CRDAndCRInOneBundle(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, disc, invalidations := cachedRemoteSchema(t)
	disc.Resources = append(disc.Resources, &metav1.APIResourceList{
		GroupVersion: "apiextensions.k8s.io/v1",
		APIResources: []metav1.APIResource{{
			Name: "customresourcedefinitions", SingularName: "customresourcedefinition", Namespaced: false,
			Kind: "CustomResourceDefinition", Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"},
		}},
	})
	fx.targeter.schema = ts

	// Applying the CRD "installs" it: the remote API server starts serving
	// gadgets from that point on.
	fx.remoteDyn.PrependReactor("patch", "customresourcedefinitions", func(clienttesting.Action) (bool, runtime.Object, error) {
		installGadgetsKeeping(disc)
		return false, nil, nil
	})

	w := fx.apply(remoteClusterID, nil, gadgetCRDYAML+"---\n"+gadgetYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Total != 2 || body.Summary.Failed != 0 {
		t.Fatalf("apply = %+v; want both the CRD and its CR applied", body)
	}
	if n := invalidations.Load(); n != 1 {
		t.Errorf("Invalidate called %d times; want exactly 1 (the CR's first miss)", n)
	}
	fx.local.assertUntouched(t)
}

// installGadgetsKeeping adds gadgets to the example.com/v1 list while keeping
// every other advertised group, replacing the list rather than editing it in
// place (see installGadgets).
func installGadgetsKeeping(disc *fakediscovery.FakeDiscovery) {
	resources := make([]*metav1.APIResourceList, 0, len(disc.Resources))
	for _, l := range disc.Resources {
		if l.GroupVersion == "example.com/v1" {
			l = &metav1.APIResourceList{
				GroupVersion: l.GroupVersion,
				APIResources: append(append([]metav1.APIResource{}, l.APIResources...), gadgetResource),
			}
		}
		resources = append(resources, l)
	}
	disc.Resources = resources
}

// TestHandleApply_CRDDiscoveryLagResolvesOnRetry mirrors a real remote API
// server, where discovery lags the CRD patch: the CR's first RESTMapping
// miss fires an invalidation immediately, before applyOne's retry loop has
// waited at all, so that first refresh still repopulates the cache with the
// stale, gadget-less set. Only a LATER invalidation — after a real backoff
// wait — observes the CRD once discovery has caught up. This is unlike
// TestHandleApply_CRDAndCRInOneBundle, whose reactor installs the kind
// synchronously during the CRD patch itself.
func TestHandleApply_CRDDiscoveryLagResolvesOnRetry(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, disc, invalidations := cachedRemoteSchema(t)
	disc.Resources = append(disc.Resources, &metav1.APIResourceList{
		GroupVersion: "apiextensions.k8s.io/v1",
		APIResources: []metav1.APIResource{{
			Name: "customresourcedefinitions", SingularName: "customresourcedefinition", Namespaced: false,
			Kind: "CustomResourceDefinition", Verbs: metav1.Verbs{"get", "list", "create", "update", "patch", "delete"},
		}},
	})

	// Gadgets are installed only on the SECOND invalidation, not synchronously
	// in the CRD patch reactor: the first invalidation (fired on the CR's
	// initial miss) sees the pre-patch discovery set, just as it would
	// against a real API server whose discovery has not caught up yet.
	baseInvalidate := ts.Invalidate
	ts.Invalidate = func() {
		before := invalidations.Load()
		baseInvalidate()
		if before == 1 {
			installGadgetsKeeping(disc)
		}
	}
	fx.targeter.schema = ts

	// The CRD patch succeeds but does NOT make the remote server start
	// serving gadgets right away, unlike TestHandleApply_CRDAndCRInOneBundle.
	fx.remoteDyn.PrependReactor("patch", "customresourcedefinitions", func(clienttesting.Action) (bool, runtime.Object, error) {
		return false, nil, nil
	})

	w := fx.apply(remoteClusterID, nil, gadgetCRDYAML+"---\n"+gadgetYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Total != 2 || body.Summary.Failed != 0 {
		t.Fatalf("apply = %+v; want both the CRD and its CR applied once discovery catches up on retry, results=%+v", body.Summary, body.Results)
	}
	if n := invalidations.Load(); n < 2 {
		t.Errorf("Invalidate called %d times; want at least 2 (the CR needed a later retry to resolve)", n)
	}
	fx.local.assertUntouched(t)
}

// TestHandleApply_NeverServedKindInvalidatesBoundedTimes guards the other
// side of the same budget: a bundle referencing a kind the remote cluster
// never serves must not invalidate the schema unboundedly while applyOne
// retries. The budget is capped per GroupKind at applyRESTMappingAttempts (3)
// — one chance per applyOne attempt, not one chance per document or per
// retry-within-a-retry.
func TestHandleApply_NeverServedKindInvalidatesBoundedTimes(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, _, invalidations := cachedRemoteSchema(t)
	fx.targeter.schema = ts

	// gadgets are never installed on the remote cluster's discovery.
	w := fx.apply(remoteClusterID, nil, gadgetYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Failed != 1 {
		t.Fatalf("apply = %+v; want the never-served Gadget to fail", body)
	}
	if n := invalidations.Load(); n != applyRESTMappingAttempts {
		t.Errorf("Invalidate called %d times; want exactly %d (bounded per GroupKind)", n, applyRESTMappingAttempts)
	}
	fx.local.assertUntouched(t)
}

// TestHandleApply_RemoteUnreachableDoesNotTouchLocal is D6 mechanism 4: when
// the remote target cannot be resolved the apply fails, and the LOCAL
// cluster's side of the wire proves nothing ran there instead.
func TestHandleApply_RemoteUnreachableDoesNotTouchLocal(t *testing.T) {
	cases := []struct {
		name     string
		targeter func(l *localCluster) clusterTargeter
	}{
		{"real router without cluster store", func(l *localCluster) clusterTargeter { return l.router }},
		{"remote resolution error", func(l *localCluster) clusterTargeter {
			return &fakeTargeter{local: l.router, err: errors.New("dial tcp 203.0.113.9:6443: i/o timeout")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := newLocalCluster(t, configMap("settings", "team-a"))
			h := newTestHandler(tc.targeter(local))

			w := serve(h.HandleApply, newRequest(http.MethodPost, "/yaml/apply", remoteClusterID, configMapYAML))
			if w.Code < 500 {
				t.Errorf("status = %d; want 5xx when the remote target cannot be resolved, body=%s", w.Code, w.Body.String())
			}
			local.assertUntouched(t)
		})
	}
}

// --- Target pinning (AE2) ----------------------------------------------------

func TestHandleApply_PinMismatchAbortsBeforeAnyMutation(t *testing.T) {
	cases := []struct {
		name, header, pin string
	}{
		{"pinned remote, header local", "", remoteClusterID},
		{"pinned local, header remote", remoteClusterID, k8s.LocalClusterID},
		{"pinned remote, header other remote", "remote-b", remoteClusterID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)

			w := fx.apply(tc.header, map[string]string{"targetCluster": tc.pin}, configMapYAML+"---\n"+widgetYAML)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
			}
			e := decodeError(t, w)
			if e.Error.Reason != "cluster_pin_mismatch" {
				t.Errorf("reason = %q; want cluster_pin_mismatch", e.Error.Reason)
			}
			if e.Error.Extra["pinnedClusterId"] != k8s.NormalizedClusterID(tc.pin) ||
				e.Error.Extra["requestClusterId"] != k8s.NormalizedClusterID(tc.header) {
				t.Errorf("extra = %v; want pinnedClusterId=%q requestClusterId=%q",
					e.Error.Extra, k8s.NormalizedClusterID(tc.pin), k8s.NormalizedClusterID(tc.header))
			}
			assertNoActions(t, "remote", fx.remoteDyn)
			fx.local.assertUntouched(t)
			if n := fx.targeter.calls.Load(); n != 0 {
				t.Errorf("targeter called %d times; a cluster mismatch must be refused before any routing", n)
			}
		})
	}
}

func TestHandleApply_GenerationMismatchAborts(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]string
	}{
		{"cluster and stale generation", map[string]string{"targetCluster": remoteClusterID, "targetGeneration": "2025-12-31T00:00:00Z"}},
		{"stale generation alone", map[string]string{"targetGeneration": "2025-12-31T00:00:00Z"}},
		{"local generation against a remote", map[string]string{"targetCluster": remoteClusterID, "targetGeneration": "local"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)

			w := fx.apply(remoteClusterID, tc.params, widgetYAML)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
			}
			e := decodeError(t, w)
			if e.Error.Reason != "cluster_generation_mismatch" {
				t.Errorf("reason = %q; want cluster_generation_mismatch", e.Error.Reason)
			}
			if e.Error.Extra["pinnedGeneration"] != tc.params["targetGeneration"] ||
				e.Error.Extra["targetGeneration"] != remoteGeneration {
				t.Errorf("extra = %v; want pinnedGeneration=%q targetGeneration=%q",
					e.Error.Extra, tc.params["targetGeneration"], remoteGeneration)
			}
			assertNoActions(t, "remote", fx.remoteDyn)
			fx.local.assertUntouched(t)
		})
	}
}

func TestHandleApply_MatchingPinApplies(t *testing.T) {
	cases := []struct {
		name, header, body string
		params             map[string]string
		dyn                func(fx *fixture) *dynfake.FakeDynamicClient
	}{
		{"remote", remoteClusterID, widgetYAML,
			map[string]string{"targetCluster": remoteClusterID, "targetGeneration": remoteGeneration},
			func(fx *fixture) *dynfake.FakeDynamicClient { return fx.remoteDyn }},
		// The header's "" and the pin's "local" name the same cluster.
		{"local", "", configMapYAML,
			map[string]string{"targetCluster": k8s.LocalClusterID, "targetGeneration": "local"},
			func(fx *fixture) *dynfake.FakeDynamicClient { return fx.local.dyn }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)

			w := fx.apply(tc.header, tc.params, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
			}
			if body := decodeData[applyBody](t, w); body.Summary.Failed != 0 || body.Summary.Created != 1 {
				t.Errorf("apply = %+v; want one document created", body)
			}
			if len(patchedNames(tc.dyn(fx))) != 1 {
				t.Errorf("pinned target recorded patches %v; want exactly one", patchedNames(tc.dyn(fx)))
			}
		})
	}
}

// R4: mobile and existing scripts send no pin. They must keep applying on the
// header's cluster exactly as before.
func TestHandleApply_UnpinnedRequestStillWorks(t *testing.T) {
	fx := newFixture(t, nil, nil)

	w := fx.apply("", nil, configMapYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Total != 1 || body.Summary.Created != 1 || body.Results[0].Name != "settings" {
		t.Fatalf("apply = %+v; want the ConfigMap created on the local cluster", body)
	}
	if got := patchedNames(fx.local.dyn); len(got) != 1 || got[0] != "configmaps/settings" {
		t.Errorf("local patches = %v; want exactly configmaps/settings", got)
	}
	assertNoActions(t, "remote", fx.remoteDyn)
}

func TestHandleApply_PinMismatchIsAudited(t *testing.T) {
	cases := []struct {
		name, header, reason string
		params               map[string]string
	}{
		{"cluster", "", "cluster_pin_mismatch", map[string]string{"targetCluster": remoteClusterID}},
		{"generation", remoteClusterID, "cluster_generation_mismatch", map[string]string{"targetGeneration": "stale"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)
			rec := &recordingAuditLogger{}
			fx.handler.AuditLogger = rec

			if w := fx.apply(tc.header, tc.params, configMapYAML); w.Code != http.StatusConflict {
				t.Fatalf("status = %d; want 409, body=%s", w.Code, w.Body.String())
			}
			entries := rec.snapshot()
			if len(entries) != 1 {
				t.Fatalf("audit entries = %+v; want exactly one for the refused apply", entries)
			}
			e := entries[0]
			if e.Result != audit.ResultFailure || e.Detail != tc.reason || e.Action != audit.ActionApply {
				t.Errorf("audit entry = %+v; want action=apply result=failure detail=%s", e, tc.reason)
			}
			if e.ClusterID != k8s.NormalizedClusterID(tc.header) || e.User != "alice" {
				t.Errorf("audit entry cluster/user = %q/%q; want %q/alice", e.ClusterID, e.User, k8s.NormalizedClusterID(tc.header))
			}
		})
	}
}

// --- Per-document outcomes on a remote target -------------------------------

// rejectPatch makes the remote API server refuse a patch to one named object.
func rejectPatch(dyn *dynfake.FakeDynamicClient, name string, err func(clienttesting.PatchAction) error) {
	dyn.PrependReactor("patch", "*", func(action clienttesting.Action) (bool, runtime.Object, error) {
		p := action.(clienttesting.PatchAction)
		if p.GetName() != name {
			return false, nil, nil
		}
		if e := err(p); e != nil {
			return true, nil, e
		}
		return false, nil, nil
	})
}

func widgetDoc(name string) string {
	return strings.Replace(widgetYAML, "name: gizmo", "name: "+name, 1)
}

func TestHandleApply_AdmissionDenialIsPerDocument(t *testing.T) {
	fx := newFixture(t, nil, nil)
	rejectPatch(fx.remoteDyn, "denied", func(clienttesting.PatchAction) error {
		return apierrors.NewInvalid(schema.GroupKind{Group: "example.com", Kind: "Widget"}, "denied",
			field.ErrorList{field.Forbidden(field.NewPath("spec", "size"), "denied by policy require-small-widgets")})
	})

	w := fx.apply(remoteClusterID, nil, widgetDoc("first")+"---\n"+widgetDoc("denied")+"---\n"+widgetDoc("last"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Failed != 1 || body.Summary.Created != 2 {
		t.Fatalf("summary = %+v; want 1 failed and 2 created", body.Summary)
	}
	if r := body.Results[1]; r.Action != "failed" || !strings.Contains(r.Error, "require-small-widgets") {
		t.Errorf("denied result = %+v; want failed with the admission message", r)
	}
	fx.local.assertUntouched(t)
}

func TestHandleApply_FieldConflictWithoutForce(t *testing.T) {
	conflictUnlessForced := func(p clienttesting.PatchAction) error {
		opts := p.(clienttesting.PatchActionImpl).GetPatchOptions()
		if opts.Force != nil && *opts.Force {
			return nil
		}
		return apierrors.NewConflict(widgetGVR.GroupResource(), p.GetName(), errors.New(`Apply failed with 1 conflict: conflict with "kubectl" using example.com/v1: .spec.size`))
	}

	fx := newFixture(t, nil, nil)
	rejectPatch(fx.remoteDyn, "gizmo", conflictUnlessForced)

	w := fx.apply(remoteClusterID, nil, widgetYAML)
	body := decodeData[applyBody](t, w)
	if r := body.Results[0]; r.Action != "failed" || !strings.Contains(r.Error, "Use force to override") {
		t.Fatalf("result = %+v; want a failed conflict with the force hint", r)
	}

	w = fx.apply(remoteClusterID, map[string]string{"force": "true"}, widgetYAML)
	body = decodeData[applyBody](t, w)
	if r := body.Results[0]; r.Action == "failed" {
		t.Fatalf("forced result = %+v; want the apply to succeed with force=true", r)
	}
	fx.local.assertUntouched(t)
}

func TestHandleApply_PartialApplyReportsAllDocuments(t *testing.T) {
	fx := newFixture(t, nil, nil)
	rejectPatch(fx.remoteDyn, "broken", func(clienttesting.PatchAction) error {
		return apierrors.NewInternalError(errors.New("etcdserver: request timed out"))
	})

	w := fx.apply(remoteClusterID, nil, widgetDoc("alpha")+"---\n"+widgetDoc("broken")+"---\n"+widgetDoc("omega"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Total != 3 || len(body.Results) != 3 {
		t.Fatalf("apply = %+v; want all three documents reported", body)
	}
	for i, want := range []string{"alpha", "broken", "omega"} {
		if body.Results[i].Name != want {
			t.Errorf("results[%d].name = %q; want %q", i, body.Results[i].Name, want)
		}
	}
	if body.Results[1].Action != "failed" || body.Summary.Failed != 1 {
		t.Errorf("apply = %+v; want only the middle document failed", body)
	}
}

// Apply is deliberately NOT Secret-refusing, unlike diff and export: it must
// stay able to create Secrets. This guards against "harmonising" the three.
func TestHandleApply_SecretIsAppliedNotRefused(t *testing.T) {
	fx := newFixture(t, nil, nil)
	disc := fx.targeter.schema.Discovery.(*fakediscovery.FakeDiscovery)
	disc.Resources = append(disc.Resources, &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "secrets", SingularName: "secret", Namespaced: true, Kind: "Secret"}},
	})
	fx.targeter.schema = remoteSchemaFrom(t, disc)

	w := fx.apply(remoteClusterID, nil, secretYAML)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if body := decodeData[applyBody](t, w); body.Summary.Created != 1 {
		t.Fatalf("apply = %+v; want the Secret created", body)
	}
	if got := patchedNames(fx.remoteDyn); len(got) != 1 || got[0] != "secrets/creds" {
		t.Errorf("remote patches = %v; want exactly secrets/creds", got)
	}
	fx.local.assertUntouched(t)
}

// --- Request-wide invalidation budget ---------------------------------------

// TestHandleApply_InvalidationsCappedPerRequest guards the request-wide
// ceiling on top of the per-GroupKind cap: without it, a bundle naming many
// distinct never-served kinds invalidates applyRESTMappingAttempts times PER
// KIND, unbounded in kind count. Kept at exactly 4 kinds: applyOne's
// 500ms+1s backoff sleeps unconditionally between attempts regardless of
// whether invalidateOnMiss actually refreshed, so each never-served document
// costs ~1.5s and the count is chosen to stay well under ~8s.
func TestHandleApply_InvalidationsCappedPerRequest(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, _, invalidations := cachedRemoteSchema(t)
	fx.targeter.schema = ts

	kinds := []string{"Gadget", "Doohickey", "Widgetoid", "Thingamajig"}
	docs := make([]string, len(kinds))
	for i, k := range kinds {
		docs[i] = strings.ReplaceAll(gadgetYAML, "Gadget", k)
	}

	w := fx.apply(remoteClusterID, nil, strings.Join(docs, "---\n"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	body := decodeData[applyBody](t, w)
	if body.Summary.Failed != len(kinds) {
		t.Fatalf("apply = %+v; want all %d never-served kinds to fail", body.Summary, len(kinds))
	}
	for _, r := range body.Results {
		if r.Action != "failed" || !strings.Contains(r.Error, "unknown resource type") {
			t.Errorf("result = %+v; want failed with unknown resource type", r)
		}
	}
	if n := invalidations.Load(); n != applyMaxInvalidations {
		t.Errorf("Invalidate called %d times; want exactly %d (the request-wide cap)", n, applyMaxInvalidations)
	}
	fx.local.assertUntouched(t)
}

// TestHandleApply_SameKindTwiceSharesPerKindBudget guards that the
// per-GroupKind budget is keyed by GroupKind, not by document: two documents
// naming the same never-served kind must not each earn their own
// applyRESTMappingAttempts invalidations.
func TestHandleApply_SameKindTwiceSharesPerKindBudget(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, _, invalidations := cachedRemoteSchema(t)
	fx.targeter.schema = ts

	doc2 := strings.Replace(gadgetYAML, "sprocket", "cog", 1)
	w := fx.apply(remoteClusterID, nil, gadgetYAML+"---\n"+doc2)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if body := decodeData[applyBody](t, w); body.Summary.Failed != 2 {
		t.Fatalf("apply = %+v; want both never-served Gadgets to fail", body.Summary)
	}
	if n := invalidations.Load(); n != applyRESTMappingAttempts {
		t.Errorf("Invalidate called %d times; want exactly %d (budget shared across documents of the same kind)", n, applyRESTMappingAttempts)
	}
	fx.local.assertUntouched(t)
}

// TestHandleApply_DistinctKindsEachGetOwnPerKindBudget guards that the
// per-GroupKind budget is keyed independently per kind: two distinct
// never-served kinds must each exhaust their own applyRESTMappingAttempts,
// not share a single counter, while staying within the request-wide cap.
func TestHandleApply_DistinctKindsEachGetOwnPerKindBudget(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, _, invalidations := cachedRemoteSchema(t)
	fx.targeter.schema = ts

	doc2 := strings.ReplaceAll(gadgetYAML, "Gadget", "Doohickey")
	w := fx.apply(remoteClusterID, nil, gadgetYAML+"---\n"+doc2)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
	}
	if body := decodeData[applyBody](t, w); body.Summary.Failed != 2 {
		t.Fatalf("apply = %+v; want both never-served kinds to fail", body.Summary)
	}
	if n := invalidations.Load(); n != 2*applyRESTMappingAttempts {
		t.Errorf("Invalidate called %d times; want %d (%d per distinct kind, within the request-wide cap)",
			n, 2*applyRESTMappingAttempts, applyRESTMappingAttempts)
	}
	fx.local.assertUntouched(t)
}

// --- Pin edge cases ----------------------------------------------------------

// TestHandleApply_ClusterOnlyPinApplies guards a pin naming only the cluster
// half — parseTargetPin leaves TargetGeneration empty, which HandleApply's
// generation check treats as unpinned — still applies normally, on both a
// remote target and the local cluster's "" / "local" spellings.
func TestHandleApply_ClusterOnlyPinApplies(t *testing.T) {
	cases := []struct {
		name, header, pin, body string
		dyn                     func(fx *fixture) *dynfake.FakeDynamicClient
	}{
		{"remote", remoteClusterID, remoteClusterID, widgetYAML,
			func(fx *fixture) *dynfake.FakeDynamicClient { return fx.remoteDyn }},
		{"local, header empty pin local", "", k8s.LocalClusterID, configMapYAML,
			func(fx *fixture) *dynfake.FakeDynamicClient { return fx.local.dyn }},
		{"local, header local pin empty", k8s.LocalClusterID, "", configMapYAML,
			func(fx *fixture) *dynfake.FakeDynamicClient { return fx.local.dyn }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, nil, nil)

			w := fx.apply(tc.header, map[string]string{"targetCluster": tc.pin}, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200, body=%s", w.Code, w.Body.String())
			}
			if body := decodeData[applyBody](t, w); body.Summary.Failed != 0 || body.Summary.Created != 1 {
				t.Errorf("apply = %+v; want one document created", body)
			}
			if got := patchedNames(tc.dyn(fx)); len(got) != 1 {
				t.Errorf("patches = %v; want exactly one", got)
			}
		})
	}
}

// TestHandleApply_PinMatchesButRoutingFailsIsRoutingError guards that a pin
// matching the header cluster does not shortcut past a genuine routing
// failure: TargetFor's error must surface as the routing 5xx it already is,
// not get reinterpreted as a 409 pin refusal, and the local cluster must stay
// untouched.
func TestHandleApply_PinMatchesButRoutingFailsIsRoutingError(t *testing.T) {
	local := newLocalCluster(t, configMap("settings", "team-a"))
	targeter := &fakeTargeter{local: local.router, err: errors.New("dial tcp 203.0.113.9:6443: i/o timeout")}
	h := newTestHandler(targeter)

	w := serve(h.HandleApply, newRequest(http.MethodPost,
		applyURL(map[string]string{"targetCluster": remoteClusterID}), remoteClusterID, widgetYAML))
	if w.Code < 500 {
		t.Fatalf("status = %d; want 5xx from the routing failure, body=%s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w); e.Error.Reason == "cluster_pin_mismatch" || e.Error.Reason == "cluster_generation_mismatch" {
		t.Errorf("reason = %q; want a routing failure, not a pin refusal", e.Error.Reason)
	}
	local.assertUntouched(t)
}

// TestHandleApply_PinRoundTripFromRealResponseJSON guards the JSON tag and
// the query key from drifting apart: it decodes targetCluster/targetGeneration
// from HandleValidate's and HandleDiff's own response JSON — not a fixture
// literal — and feeds them back as HandleApply's query params.
func TestHandleApply_PinRoundTripFromRealResponseJSON(t *testing.T) {
	for _, verb := range []string{"validate", "diff"} {
		t.Run(verb, func(t *testing.T) {
			fx := newFixture(t, nil, nil)

			var w *httptest.ResponseRecorder
			if verb == "validate" {
				w = serve(fx.handler.HandleValidate, newRequest(http.MethodPost, "/yaml/validate", remoteClusterID, widgetYAML))
			} else {
				w = serve(fx.handler.HandleDiff, newRequest(http.MethodPost, "/yaml/diff", remoteClusterID, widgetYAML))
			}
			if w.Code != http.StatusOK {
				t.Fatalf("%s status = %d; want 200, body=%s", verb, w.Code, w.Body.String())
			}

			var env struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("decoding %s envelope: %v", verb, err)
			}
			var pin struct {
				TargetCluster    string `json:"targetCluster"`
				TargetGeneration string `json:"targetGeneration"`
			}
			if err := json.Unmarshal(env.Data, &pin); err != nil {
				t.Fatalf("decoding %s pin: %v", verb, err)
			}
			if pin.TargetCluster == "" || pin.TargetGeneration == "" {
				t.Fatalf("%s pin = %+v; want both fields populated", verb, pin)
			}

			aw := fx.apply(remoteClusterID, map[string]string{
				"targetCluster":    pin.TargetCluster,
				"targetGeneration": pin.TargetGeneration,
			}, widgetYAML)
			if aw.Code != http.StatusOK {
				t.Fatalf("apply status = %d; want 200, body=%s", aw.Code, aw.Body.String())
			}
			if body := decodeData[applyBody](t, aw); body.Summary.Failed != 0 || body.Summary.Created != 1 {
				t.Errorf("apply = %+v; want one document created", body)
			}
		})
	}
}

// --- Concurrency --------------------------------------------------------------

// TestHandleApply_ConcurrentAppliesShareTargetSchemaSafely guards concurrent
// HandleApply calls against one shared remote TargetSchema — each call gets
// its own schemaRefresh, but Invalidate resets the same underlying mapper —
// where some calls need a refresh to resolve a CRD installed after the
// mapper cache warmed. No timing assertions: only that every goroutine's
// apply succeeds, proving no race corrupts the shared mapper or the shared
// fake dynamic client.
func TestHandleApply_ConcurrentAppliesShareTargetSchemaSafely(t *testing.T) {
	fx := newFixture(t, nil, nil)
	ts, disc, _ := cachedRemoteSchema(t)
	fx.targeter.schema = ts

	// Warm the mapper cache with gadgets absent, then install them — mirrors
	// TestHandleValidate_RemoteCRDInstalledAfterCacheWarmResolves, so half the
	// goroutines below each need exactly one refresh to resolve.
	if w := fx.apply(remoteClusterID, nil, widgetYAML); w.Code != http.StatusOK {
		t.Fatalf("warm-up status = %d; body=%s", w.Code, w.Body.String())
	}
	installGadgets(disc)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var body string
			if i%2 == 0 {
				body = widgetDoc(fmt.Sprintf("gizmo-%d", i))
			} else {
				body = strings.Replace(gadgetYAML, "sprocket", fmt.Sprintf("sprocket-%d", i), 1)
			}
			codes[i] = fx.apply(remoteClusterID, nil, body).Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("goroutine %d status = %d; want 200", i, code)
		}
	}
}
