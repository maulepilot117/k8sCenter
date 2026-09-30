package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// remoteHost is the address a remote cluster's transport errors name. No
// response may carry it.
const remoteHost = "10.20.30.40"

// remoteFake is one fake cluster: its discovery and its clients.
type remoteFake struct {
	disc *fakediscovery.FakeDiscovery
	dyn  *dynamicfake.FakeDynamicClient
	kube *kubefake.Clientset
}

// remoteFakes is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every resolution fail.
type remoteFakes struct {
	clusters  map[string]*remoteFake
	targetErr error
}

func (f *remoteFakes) cluster(id string) (*remoteFake, error) {
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *remoteFakes) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.kube, nil
}

func (f *remoteFakes) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.dyn, nil
}

func (f *remoteFakes) TargetSchemaFor(_ context.Context, id, _ string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	var disc discovery.DiscoveryInterface = c.disc
	return &k8s.TargetSchema{ClusterID: id, Discovery: disc, Invalidate: func() {}}, nil
}

var esoKinds = map[schema.GroupVersionResource]string{
	ExternalSecretGVR:        "ExternalSecret",
	ClusterExternalSecretGVR: "ClusterExternalSecret",
	SecretStoreGVR:           "SecretStore",
	ClusterSecretStoreGVR:    "ClusterSecretStore",
	PushSecretGVR:            "PushSecret",
}

// esoDiscovery is discovery for a cluster serving ESO's kinds at version, or
// not serving ESO at all when version is empty.
func esoDiscovery(version string) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "secrets", Kind: "Secret", Namespaced: true}}}}
	if version != "" {
		l := &metav1.APIResourceList{GroupVersion: GroupName + "/" + version}
		for gvr, kind := range esoKinds {
			l.APIResources = append(l.APIResources, metav1.APIResource{Name: gvr.Resource, Kind: kind})
		}
		lists = append(lists, l)
	}
	return lists
}

func newRemoteFake(version string, objs []runtime.Object, secrets ...runtime.Object) *remoteFake {
	return &remoteFake{
		disc: &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: esoDiscovery(version)}},
		dyn:  newEsoFakeDynClient(objs...),
		kube: kubefake.NewClientset(secrets...),
	}
}

type remoteHarness struct {
	h       *Handler
	clients *remoteFakes
}

// newRemoteHarness builds a Handler over a remote cluster and a local
// cluster, both reachable only through the same ClusterClients. The local
// one is seeded with its own objects so a local read on the remote path
// shows in its recorded actions. K8sClient and the local Discoverer are
// nil, so a service-account or local-discovery read on the remote path
// panics.
func newRemoteHarness(remote *remoteFake) *remoteHarness {
	clients := &remoteFakes{clusters: map[string]*remoteFake{
		remoteCluster: remote,
		k8s.LocalClusterID: newRemoteFake("v1",
			[]runtime.Object{makeES("apps", "local-es", "uid-local"), makeStore("apps", "local-store", "uid-ls")},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "local-secret", Namespace: "src"}}),
	}}
	h := NewHandler(nil, clients, k8s.NewPresence(clients), nil, resources.NewAlwaysAllowAccessChecker(),
		nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &remoteHarness{h: h, clients: clients}
}

// localActions is every call the local cluster's clients received.
func (rh *remoteHarness) localActions() int {
	local := rh.clients.clusters[k8s.LocalClusterID]
	return len(local.dyn.Actions()) + len(local.kube.Actions())
}

func (rh *remoteHarness) remote() *remoteFake { return rh.clients.clusters[remoteCluster] }

// doRemote serves one request on the remote cluster as an admin.
func doRemote(t *testing.T, h http.HandlerFunc, params map[string]string, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/externalsecrets"+query, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}})
	ctx = middleware.WithClusterID(ctx, remoteCluster)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func decodeData[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

func decodeErrorReason(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Error.Reason
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, name(it))
	}
	slices.Sort(out)
	return out
}

func unreachableErr() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

// remoteInventory is one of each ESO kind, named for the remote cluster.
func remoteInventory() []runtime.Object {
	return []runtime.Object{
		makeES("apps", "remote-es", "uid-res"),
		makeCES("remote-ces", "uid-rces"),
		makeStore("apps", "vault", "uid-rs"),
		makeClusterStore("remote-cstore", "uid-rcs"),
		makePushSecret("apps", "remote-push", "uid-rps"),
	}
}

func TestRemote_ListsReturnRemoteObjects(t *testing.T) {
	rh := newRemoteHarness(newRemoteFake("v1", remoteInventory()))
	// A drift observation from the local poller under the remote row's UID
	// must not reach the remote list.
	rh.h.RecordDrift("uid-res", DriftInSync)

	rr := doRemote(t, rh.h.HandleListExternalSecrets, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ES list status %d: %s", rr.Code, rr.Body.String())
	}
	ess := decodeData[[]ExternalSecret](t, rr)
	if got := names(ess, func(e ExternalSecret) string { return e.Name }); !slices.Equal(got, []string{"remote-es"}) {
		t.Errorf("ExternalSecrets = %v, want [remote-es]", got)
	}
	for _, es := range ess {
		if es.LastObservedDriftStatus != DriftUnknown || es.DriftStatus != "" {
			t.Errorf("%s drift = (last observed %q, live %q), want (Unknown, absent)", es.Name, es.LastObservedDriftStatus, es.DriftStatus)
		}
	}

	checks := []struct {
		handler http.HandlerFunc
		want    string
		got     func(*httptest.ResponseRecorder) []string
	}{
		{rh.h.HandleListClusterExternalSecrets, "remote-ces", func(rr *httptest.ResponseRecorder) []string {
			return names(decodeData[[]ClusterExternalSecret](t, rr), func(c ClusterExternalSecret) string { return c.Name })
		}},
		{rh.h.HandleListStores, "vault", func(rr *httptest.ResponseRecorder) []string {
			return names(decodeData[[]SecretStore](t, rr), func(s SecretStore) string { return s.Name })
		}},
		{rh.h.HandleListClusterStores, "remote-cstore", func(rr *httptest.ResponseRecorder) []string {
			return names(decodeData[[]SecretStore](t, rr), func(s SecretStore) string { return s.Name })
		}},
		{rh.h.HandleListPushSecrets, "remote-push", func(rr *httptest.ResponseRecorder) []string {
			return names(decodeData[[]PushSecret](t, rr), func(p PushSecret) string { return p.Name })
		}},
	}
	for _, c := range checks {
		rr := doRemote(t, c.handler, nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("list %s: status %d: %s", c.want, rr.Code, rr.Body.String())
		}
		if got := c.got(rr); !slices.Equal(got, []string{c.want}) {
			t.Errorf("list = %v, want [%s]", got, c.want)
		}
	}
	if n := rh.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_DetailReadsRemoteObjectStoresAndSecret(t *testing.T) {
	es := makeES("apps", "remote-es", "uid-res")
	es.Object["status"].(map[string]any)["syncedResourceVersion"] = "42"
	store := makeStore("apps", "vault", "uid-rs")
	store.SetAnnotations(map[string]string{AnnotationStaleAfterMinutes: "90"})
	remote := newRemoteFake("v1", []runtime.Object{es, store},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "remote-es-secret", Namespace: "apps", ResourceVersion: "42"}})
	rh := newRemoteHarness(remote)

	rr := doRemote(t, rh.h.HandleGetExternalSecret, map[string]string{"namespace": "apps", "name": "remote-es"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeData[ExternalSecret](t, rr)
	if got.DriftStatus != DriftInSync {
		t.Errorf("drift = %q (%s), want InSync from the remote Secret", got.DriftStatus, got.DriftUnknownReason)
	}
	if got.StaleAfterMinutes == nil || *got.StaleAfterMinutes != 90 || got.StaleAfterMinutesSource != ThresholdSourceSecretStore {
		t.Errorf("threshold = %v from %q, want 90 from the remote store", got.StaleAfterMinutes, got.StaleAfterMinutesSource)
	}

	rr = doRemote(t, rh.h.HandleGetExternalSecret, map[string]string{"namespace": "apps", "name": "local-es"}, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("detail of a local-only ES: status %d, want 404", rr.Code)
	}
	if n := rh.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_NotInstalled(t *testing.T) {
	for _, tc := range []struct{ name, version string }{
		{"no ESO", ""},
		{"only an older API version", "v1beta1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rh := newRemoteHarness(newRemoteFake(tc.version, nil))

			rr := doRemote(t, rh.h.HandleListExternalSecrets, nil, "")
			if rr.Code != http.StatusOK || len(decodeData[[]ExternalSecret](t, rr)) != 0 {
				t.Errorf("list = %d %s, want 200 []", rr.Code, rr.Body.String())
			}
			if st := decodeData[ESOStatus](t, doRemote(t, rh.h.HandleStatus, nil, "")); st.Detected || st.Reason != string(k8s.ReasonDiscoveryMissing) {
				t.Errorf("status = %+v, want not detected with discovery_missing", st)
			}
			rr = doRemote(t, rh.h.HandleGetStore, map[string]string{"namespace": "apps", "name": "vault"}, "")
			if rr.Code != http.StatusServiceUnavailable || decodeErrorReason(t, rr) != string(k8s.ReasonDiscoveryMissing) {
				t.Errorf("detail = %d %s, want 503 discovery_missing", rr.Code, rr.Body.String())
			}
			if n := len(rh.remote().dyn.Actions()); n != 0 {
				t.Errorf("remote cluster recorded %d list/get calls, want 0", n)
			}
		})
	}
}

func TestRemote_InstalledStatus(t *testing.T) {
	rh := newRemoteHarness(newRemoteFake("v1", nil))
	if st := decodeData[ESOStatus](t, doRemote(t, rh.h.HandleStatus, nil, "")); !st.Detected || st.Reason != "" {
		t.Errorf("status = %+v, want detected with no reason", st)
	}
}

func TestRemote_UnreachableNeverLeaksOrFallsBack(t *testing.T) {
	rh := newRemoteHarness(newRemoteFake("v1", remoteInventory()))
	rh.clients.targetErr = unreachableErr()

	for name, handler := range map[string]http.HandlerFunc{
		"list":   rh.h.HandleListExternalSecrets,
		"cslist": rh.h.HandleListClusterStores,
		"detail": rh.h.HandleGetExternalSecret,
		"paths":  rh.h.HandleListPaths,
	} {
		rr := doRemote(t, handler, map[string]string{"namespace": "apps", "name": "remote-es"}, "")
		if rr.Code != http.StatusBadGateway || decodeErrorReason(t, rr) != string(k8s.ReasonUnreachable) {
			t.Errorf("%s = %d %s, want 502 unreachable", name, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), remoteHost) {
			t.Errorf("%s response leaks the remote host: %s", name, rr.Body.String())
		}
	}
	if st := decodeData[ESOStatus](t, doRemote(t, rh.h.HandleStatus, nil, "")); st.Detected || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("status = %+v, want not detected with unreachable", st)
	}
	if n := rh.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_OneFailedListDoesNotHideTheOthers(t *testing.T) {
	remote := newRemoteFake("v1", remoteInventory())
	remote.dyn.PrependReactor("list", "secretstores", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(SecretStoreGVR.GroupResource(), "", errors.New("denied at "+remoteHost))
	})
	rh := newRemoteHarness(remote)

	rr := doRemote(t, rh.h.HandleListExternalSecrets, nil, "")
	if rr.Code != http.StatusOK || len(decodeData[[]ExternalSecret](t, rr)) != 1 {
		t.Errorf("ES list = %d %s, want 200 with the remote ES", rr.Code, rr.Body.String())
	}
	rr = doRemote(t, rh.h.HandleListStores, nil, "")
	if rr.Code != http.StatusForbidden || strings.Contains(rr.Body.String(), remoteHost) {
		t.Errorf("store list = %d %s, want 403 without the remote host", rr.Code, rr.Body.String())
	}
}

func TestRemote_PathDiscoveryReadsRemoteStoreAndSecrets(t *testing.T) {
	remote := newRemoteFake("v1",
		[]runtime.Object{makeKubernetesProviderStore("apps", "k8s-store", "src")},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "remote-secret", Namespace: "src"}})
	rh := newRemoteHarness(remote)

	rr := doRemote(t, rh.h.HandleListPaths, map[string]string{"namespace": "apps", "name": "k8s-store"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("paths status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decodeData[pathDiscoveryResponse](t, rr); !got.Supported || !slices.Equal(got.Paths, []string{"remote-secret"}) {
		t.Errorf("paths = %+v, want the remote cluster's [remote-secret]", got)
	}
	if n := rh.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_EvictRemoteCacheShowsNewObjects(t *testing.T) {
	rh := newRemoteHarness(newRemoteFake("v1", remoteInventory()))
	count := func() int {
		return len(decodeData[[]ExternalSecret](t, doRemote(t, rh.h.HandleListExternalSecrets, nil, "")))
	}
	if n := count(); n != 1 {
		t.Fatalf("first list = %d ESes, want 1", n)
	}
	added := makeES("apps", "added-es", "uid-add")
	if err := rh.remote().dyn.Tracker().Create(ExternalSecretGVR, added, "apps"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n := count(); n != 1 {
		t.Errorf("cached list = %d ESes, want 1 until the cache is evicted", n)
	}
	rh.h.EvictRemoteCache(remoteCluster)
	if n := count(); n != 2 {
		t.Errorf("list after evict = %d ESes, want 2", n)
	}
}

// Writes stay refused on a remote cluster (R12): force-sync answers 501 and
// sends nothing to either cluster.
func TestRemote_ForceSyncStillRefused(t *testing.T) {
	rh := newRemoteHarness(newRemoteFake("v1", remoteInventory()))
	rr := doRemote(t, rh.h.HandleForceSyncExternalSecret, map[string]string{"namespace": "apps", "name": "remote-es"}, "")
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("force-sync = %d %s, want 501", rr.Code, rr.Body.String())
	}
	if n := len(rh.remote().dyn.Actions()) + rh.localActions(); n != 0 {
		t.Errorf("clusters recorded %d actions, want 0", n)
	}
}
