package storage

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	remoteCluster = "remote-1"
	localCluster  = "local"
	// remoteHost is the address a remote cluster's transport errors name. No
	// response may carry it.
	remoteHost = "10.20.30.40"
)

// fakeCluster is one fake cluster: its discovery and dynamic client.
type fakeCluster struct {
	disc *fakediscovery.FakeDiscovery
	dyn  *dynfake.FakeDynamicClient
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	if _, err := f.cluster(id); err != nil {
		return nil, err
	}
	return kfake.NewSimpleClientset(), nil
}

func (f *fakeClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.dyn, nil
}

func (f *fakeClients) TargetSchemaFor(_ context.Context, id, _ string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return &k8s.TargetSchema{ClusterID: id, Discovery: c.disc, Invalidate: func() {}}, nil
}

var snapshotListKinds = map[schema.GroupVersionResource]string{
	volumeSnapshotGVR:      "VolumeSnapshotList",
	volumeSnapshotClassGVR: "VolumeSnapshotClassList",
}

func snapshot(ns, name, pvc string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"source": map[string]any{"persistentVolumeClaimName": pvc}},
	}}
}

func snapshotClass(name, driver string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion":     "snapshot.storage.k8s.io/v1",
		"kind":           "VolumeSnapshotClass",
		"metadata":       map[string]any{"name": name},
		"driver":         driver,
		"deletionPolicy": "Delete",
	}}
}

// discoveryLists is discovery for a cluster that does or does not serve the
// snapshot CRDs.
func discoveryLists(installed bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if installed {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: "snapshot.storage.k8s.io/v1", APIResources: []metav1.APIResource{
			{Name: "volumesnapshots", Kind: "VolumeSnapshot", Namespaced: true},
			{Name: "volumesnapshotclasses", Kind: "VolumeSnapshotClass"},
		}})
	}
	return lists
}

func newFakeCluster(t *testing.T, installed bool, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), snapshotListKinds)
	for _, o := range objs {
		gvr := volumeSnapshotGVR
		if o.GetKind() == "VolumeSnapshotClass" {
			gvr = volumeSnapshotClassGVR
		}
		if err := dyn.Tracker().Create(gvr, o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: discoveryLists(installed)}}, dyn: dyn}
}

type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) last(t *testing.T) audit.Entry {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 0 {
		t.Fatal("no audit entry recorded")
	}
	return a.entries[len(a.entries)-1]
}

type harness struct {
	h       *Handler
	clients *fakeClients
	audit   *recordingAudit
}

// newHarness builds a Handler over a remote cluster and a local one (both
// reachable only through the same ClusterClients), each seeded with its own
// objects, so a local read on the remote path shows up in the local fake's
// recorded actions. K8sClient and Informers are nil, so a service-account,
// local-discovery or informer read on the remote path panics.
func newHarness(t *testing.T, remoteInstalled bool, remoteObjs ...*unstructured.Unstructured) *harness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, remoteInstalled, remoteObjs...),
		localCluster:  newFakeCluster(t, true, snapshot("apps", "local-snap", "data"), snapshotClass("local-only-class", "local.csi")),
	}}
	hs := &harness{clients: clients, audit: &recordingAudit{}}
	hs.h = &Handler{
		AuditLogger: hs.audit,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clients:     clients,
		Presence:    k8s.NewPresence(clients),
	}
	return hs
}

func (hs *harness) localActions() int { return len(hs.clients.clusters[localCluster].dyn.Actions()) }

func (hs *harness) dyn(id string) *dynfake.FakeDynamicClient { return hs.clients.clusters[id].dyn }

func countVerb(dyn *dynfake.FakeDynamicClient, verb, resource string) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func do(t *testing.T, clusterID, method string, h http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/storage", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}})
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

// listResponse is the {data, metadata} shape of the snapshot list routes.
type listResponse[T any] struct {
	Data     []T `json:"data"`
	Metadata struct {
		Total     int    `json:"total"`
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	} `json:"metadata"`
}

func decodeList[T any](t *testing.T, rr *httptest.ResponseRecorder) listResponse[T] {
	t.Helper()
	var resp listResponse[T]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

var snapParams = map[string]string{"namespace": "apps", "name": "remote-snap"}

func TestRemote_ListSnapshotsReturnsRemoteSnapshots(t *testing.T) {
	hs := newHarness(t, true, snapshot("apps", "remote-snap", "data"))

	for _, params := range []map[string]string{nil, {"namespace": "apps"}} {
		rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListSnapshots, params, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		got := decodeList[SnapshotInfo](t, rr)
		if len(got.Data) != 1 || got.Data[0].Name != "remote-snap" || !got.Metadata.Available {
			t.Errorf("params %v: list = %+v, want only remote-snap", params, got)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_CreateSnapshotReachesRemoteAndAuditsRemoteID(t *testing.T) {
	hs := newHarness(t, true)

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"},
		`{"name":"snap-1","sourcePVC":"data","volumeSnapshotClassName":"remote-class"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.dyn(remoteCluster), "create", "volumesnapshots"); n != 1 {
		t.Errorf("created %d snapshots on the remote, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want remote cluster success", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
	// The next list shows it: remote reads are direct, with no stale window.
	if got := decodeList[SnapshotInfo](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleListSnapshots, nil, "")); len(got.Data) != 1 {
		t.Errorf("list after create = %+v, want the new snapshot", got.Data)
	}
}

func TestRemote_GetAndDeleteSnapshotActOnRemote(t *testing.T) {
	hs := newHarness(t, true, snapshot("apps", "remote-snap", "data"))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleGetSnapshot, snapParams, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleGetSnapshot, map[string]string{"namespace": "apps", "name": "local-snap"}, ""); rr.Code != http.StatusNotFound {
		t.Errorf("get of a local-only snapshot: status %d, want 404", rr.Code)
	}

	rr = do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteSnapshot, snapParams, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.dyn(remoteCluster), "delete", "volumesnapshots"); n != 1 {
		t.Errorf("deleted %d snapshots on the remote, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Action != audit.ActionDelete || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want remote cluster delete success", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_SnapshotClassesListTheRemoteOnly(t *testing.T) {
	hs := newHarness(t, true, snapshotClass("remote-class", "remote.csi"))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListSnapshotClasses, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeList[map[string]any](t, rr)
	if len(got.Data) != 1 || got.Data[0]["name"] != "remote-class" || got.Data[0]["driver"] != "remote.csi" || !got.Metadata.Available {
		t.Errorf("classes = %+v, want only remote-class", got)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_NotInstalledOnRemoteEvenWhenLocalHasTheCRDs(t *testing.T) {
	hs := newHarness(t, false)

	for name, h := range map[string]http.HandlerFunc{"classes": hs.h.HandleListSnapshotClasses, "snapshots": hs.h.HandleListSnapshots} {
		rr := do(t, remoteCluster, http.MethodGet, h, nil, "")
		got := decodeList[map[string]any](t, rr)
		if rr.Code != http.StatusOK || got.Metadata.Available || got.Metadata.Reason != string(k8s.ReasonDiscoveryMissing) || len(got.Data) != 0 {
			t.Errorf("%s: status %d body %s, want 200 unavailable with discovery_missing", name, rr.Code, rr.Body.String())
		}
	}
	if rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"}, `{"name":"s","sourcePVC":"data"}`); rr.Code != http.StatusNotFound {
		t.Errorf("create: status %d, want 404 not installed", rr.Code)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_UnreachableReturnsTheClusterError(t *testing.T) {
	hs := newHarness(t, true)
	hs.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":    do(t, remoteCluster, http.MethodGet, hs.h.HandleListSnapshots, nil, ""),
		"classes": do(t, remoteCluster, http.MethodGet, hs.h.HandleListSnapshotClasses, nil, ""),
		"get":     do(t, remoteCluster, http.MethodGet, hs.h.HandleGetSnapshot, snapParams, ""),
		"create":  do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"}, `{"name":"s","sourcePVC":"data"}`),
		"delete":  do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteSnapshot, snapParams, ""),
	} {
		body := rr.Body.String()
		if rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) {
			t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, body)
		}
		if strings.Contains(body, remoteHost) {
			t.Errorf("%s: body leaks the remote address: %s", name, body)
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_FailedWritesAreClassifiedAndAudited(t *testing.T) {
	hs := newHarness(t, true, snapshot("apps", "remote-snap", "data"))
	hs.dyn(remoteCluster).PrependReactor("create", "volumesnapshots", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})
	hs.dyn(remoteCluster).PrependReactor("delete", "volumesnapshots", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(volumeSnapshotGVR.GroupResource(), "remote-snap", errors.New("no"))
	})

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"}, `{"name":"s","sourcePVC":"data"}`)
	if body := rr.Body.String(); rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) || strings.Contains(body, remoteHost) {
		t.Errorf("create: status %d body %s, want 502 unreachable without the address", rr.Code, body)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultFailure {
		t.Errorf("create audit = %+v, want remote failure", e)
	}

	if rr := do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteSnapshot, snapParams, ""); rr.Code != http.StatusForbidden {
		t.Errorf("delete: status %d, want 403", rr.Code)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultDenied {
		t.Errorf("delete audit = %+v, want remote denied", e)
	}
}

func TestLocal_CreateSnapshotAuditsTheRequestCluster(t *testing.T) {
	hs := newHarness(t, true)
	// The local answer comes from the local discovery check; mark it fresh.
	hs.h.snapshotAvail, hs.h.snapshotCheckedAt = true, time.Now()

	rr := do(t, localCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"}, `{"name":"snap-1","sourcePVC":"data"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.dyn(localCluster), "create", "volumesnapshots"); n != 1 {
		t.Errorf("created %d snapshots locally, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != localCluster {
		t.Errorf("audit cluster = %q, want %q", e.ClusterID, localCluster)
	}
}

// crdGone answers a collection-level NotFound, as an API server does once a
// CRD is removed. Discovery is flipped to "not installed" at the same time,
// so a presence re-check sees the removal the cached verdict missed.
func crdGone(c *fakeCluster, gvr schema.GroupVersionResource, verbs ...string) {
	for _, verb := range verbs {
		c.dyn.PrependReactor(verb, gvr.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			c.disc.Resources = discoveryLists(false)
			return true, nil, apierrors.NewNotFound(gvr.GroupResource(), "")
		})
	}
}

func TestRemote_CRDRemovedAfterDiscoveryReportsNotInstalled(t *testing.T) {
	for name, tc := range map[string]struct {
		h   func(*Handler) http.HandlerFunc
		gvr schema.GroupVersionResource
	}{
		"snapshots": {func(h *Handler) http.HandlerFunc { return h.HandleListSnapshots }, volumeSnapshotGVR},
		"classes":   {func(h *Handler) http.HandlerFunc { return h.HandleListSnapshotClasses }, volumeSnapshotClassGVR},
	} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, true)
			crdGone(hs.clients.clusters[remoteCluster], tc.gvr, "list")

			rr := do(t, remoteCluster, http.MethodGet, tc.h(hs.h), nil, "")
			got := decodeList[map[string]any](t, rr)
			if rr.Code != http.StatusOK || got.Metadata.Available || got.Metadata.Reason != string(k8s.ReasonDiscoveryMissing) {
				t.Errorf("status %d body %s, want 200 unavailable with discovery_missing", rr.Code, rr.Body.String())
			}
		})
	}

	hs := newHarness(t, true)
	crdGone(hs.clients.clusters[remoteCluster], volumeSnapshotGVR, "create")
	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSnapshot, map[string]string{"namespace": "apps"}, `{"name":"s","sourcePVC":"data"}`)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "not installed") {
		t.Errorf("create after removal: status %d body %s, want 404 not installed", rr.Code, rr.Body.String())
	}
}
