package topology

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// fakeClients is a k8s.ClusterClients over one typed fake clientset. It
// records every client resolution; targetErr makes every resolution fail.
type fakeClients struct {
	cs        *kfake.Clientset
	targetErr error

	mu    sync.Mutex
	calls []clientCall
}

type clientCall struct {
	cluster, username string
	groups            []string
}

func (f *fakeClients) recorded() []clientCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]clientCall(nil), f.calls...)
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, username string, groups []string) (kubernetes.Interface, error) {
	f.mu.Lock()
	f.calls = append(f.calls, clientCall{cluster: id, username: username, groups: append([]string(nil), groups...)})
	f.mu.Unlock()
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	return f.cs, nil
}

func (f *fakeClients) DynamicClientForCluster(context.Context, string, string, []string) (dynamic.Interface, error) {
	return nil, errors.New("topology does not use the dynamic client")
}

func (f *fakeClients) TargetSchemaFor(context.Context, string, string, []string) (*k8s.TargetSchema, error) {
	return nil, errors.New("topology does not use the target schema")
}

// remoteTopologyObjects is the remote cluster's namespace foo: a Deployment
// owning a ReplicaSet owning a pod, and a Service selecting the pod. The
// local recording lister only has Service a.
func remoteTopologyObjects() []runtime.Object {
	return []runtime.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "foo", UID: "r-dep"}},
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "foo", UID: "r-rs",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", UID: "r-dep"}},
		}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-1", Namespace: "foo", UID: "r-pod", Labels: map[string]string{"app": "web"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-rs", UID: "r-rs"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web-svc", Namespace: "foo", UID: "r-svc"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
		},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "bar", UID: "r-other"}},
	}
}

func newRemoteTopologyHandler(objs ...runtime.Object) (*Handler, *recordingLister, *recordingMeshProvider, *fakeClients) {
	h, lister, mesh := newRecordingHandler()
	fc := &fakeClients{cs: kfake.NewSimpleClientset(objs...)}
	h.Clients = fc
	return h, lister, mesh, fc
}

func decodeGraph(t *testing.T, body []byte) Graph {
	t.Helper()
	var resp struct {
		Data Graph `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode graph: %v; body: %s", err, body)
	}
	return resp.Data
}

func TestHandleNamespaceGraph_RemoteBuildsFromRemote(t *testing.T) {
	h, lister, mesh, fc := newRemoteTopologyHandler(remoteTopologyObjects()...)

	w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	g := decodeGraph(t, w.Body.Bytes())
	nodes := map[string]bool{}
	for _, n := range g.Nodes {
		nodes[n.Kind+"/"+n.Name] = true
	}
	for _, want := range []string{"Deployment/web", "ReplicaSet/web-rs", "Pod/web-1", "Service/web-svc"} {
		if !nodes[want] {
			t.Errorf("graph lacks remote node %s; nodes = %v", want, nodes)
		}
	}
	if nodes["Service/a"] || nodes["Service/other"] {
		t.Errorf("graph has a local or other-namespace node: %v", nodes)
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.Source+">"+e.Target+":"+string(e.Type)] = true
	}
	for _, want := range []string{"r-dep>r-rs:" + string(EdgeOwner), "r-rs>r-pod:" + string(EdgeOwner), "r-svc>r-pod:" + string(EdgeSelector)} {
		if !edges[want] {
			t.Errorf("graph lacks edge %s; edges = %v", want, edges)
		}
	}
	if n := lister.calls.Load(); n != 0 {
		t.Errorf("local lister read %d times on a remote request, want 0", n)
	}
	if n := mesh.calls.Load(); n != 0 {
		t.Errorf("mesh provider read %d times on a remote request, want 0", n)
	}
	calls := fc.recorded()
	if len(calls) != 1 || calls[0].cluster != "remote-1" || calls[0].username != "u" || len(calls[0].groups) != 1 || calls[0].groups[0] != "g" {
		t.Errorf("client resolutions = %+v, want one for remote-1 as u [g]", calls)
	}
}

// A kind whose remote list hit the read cap is left out, as a forbidden kind
// is, and the graph says so.
func TestHandleNamespaceGraph_RemoteTruncatedKindIsMarked(t *testing.T) {
	h, _, _, fc := newRemoteTopologyHandler(remoteTopologyObjects()...)
	fc.cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{
			ListMeta: metav1.ListMeta{Continue: "more"},
			Items:    []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "foo"}}},
		}, nil
	})

	w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	g := decodeGraph(t, w.Body.Bytes())
	for _, n := range g.Nodes {
		if n.Kind == "Pod" {
			t.Errorf("truncated pods still in the graph: %+v", n)
		}
	}
	if !g.Truncated {
		t.Error("truncated = false, want true when a kind exceeded the remote read cap")
	}
	if msg := g.Errors["pods"]; msg == "" {
		t.Errorf("errors = %v, want a pods entry naming the read cap", g.Errors)
	}
}

func TestHandleNamespaceGraph_RemoteOverlayRefused(t *testing.T) {
	for _, overlay := range []string{"mesh", "eso-chain"} {
		t.Run(overlay, func(t *testing.T) {
			h, lister, mesh, fc := newRemoteTopologyHandler(remoteTopologyObjects()...)

			w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", overlay)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
					Reason  string `json:"reason"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Reason != reasonOverlayUnsupportedRemote || body.Error.Message != overlayUnsupportedRemoteMessage {
				t.Errorf("error = %+v, want %q %q", body.Error, reasonOverlayUnsupportedRemote, overlayUnsupportedRemoteMessage)
			}
			if lister.calls.Load() != 0 || mesh.calls.Load() != 0 || len(fc.cs.Actions()) != 0 {
				t.Error("an overlay refusal read a lister, the mesh provider or the remote cluster")
			}
		})
	}
}

func TestHandleNamespaceGraph_RemoteResolveFailure(t *testing.T) {
	h, lister, _, fc := newRemoteTopologyHandler()
	fc.targetErr = errors.New("dial tcp 10.9.8.7:6443 secret-apiserver.internal")

	w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", "")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "10.9.8.7") {
		t.Errorf("raw resolution error leaked: %s", w.Body.String())
	}
	if lister.calls.Load() != 0 {
		t.Error("local lister read after a remote resolution failure")
	}
}

func TestHandleNamespaceGraph_RemoteNilClientsIs500(t *testing.T) {
	h, lister, _, _ := newRemoteTopologyHandler()
	h.Clients = nil

	w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
	if lister.calls.Load() != 0 {
		t.Error("local lister read with no cluster clients wired")
	}
}

func TestHandleNamespaceGraph_LocalNeverResolvesClients(t *testing.T) {
	for _, clusterID := range []string{"", "local"} {
		t.Run("cluster="+clusterID, func(t *testing.T) {
			h, lister, _, fc := newRemoteTopologyHandler(remoteTopologyObjects()...)

			w := callTopologyHandlerForCluster(t, h, clusterID, "foo", "")

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if n := len(fc.recorded()); n != 0 {
				t.Errorf("Clients resolved %d times on a local request, want 0", n)
			}
			if lister.calls.Load() == 0 {
				t.Error("local lister never read on a local request")
			}
		})
	}
}

// An unknown overlay on a remote cluster gets the local 400 before the
// cluster is contacted.
func TestHandleNamespaceGraph_RemoteUnknownOverlayIs400WithoutReads(t *testing.T) {
	h, lister, _, fc := newRemoteTopologyHandler(remoteTopologyObjects()...)

	w := callTopologyHandlerForCluster(t, h, "remote-1", "foo", "bogus")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if lister.calls.Load() != 0 || len(fc.recorded()) != 0 || len(fc.cs.Actions()) != 0 {
		t.Error("an unknown overlay on a remote cluster read a lister or contacted the cluster")
	}
}
