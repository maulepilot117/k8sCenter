package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	fakekube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

type infoBody struct {
	Data struct {
		ClusterID         string `json:"clusterID"`
		KubernetesVersion string `json:"kubernetesVersion"`
		Platform          string `json:"platform"`
		NodeCount         *int   `json:"nodeCount"`
	} `json:"data"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	} `json:"error"`
}

// newInfoServer builds a Server whose LOCAL cluster reports v1.1.1 / 3 nodes,
// so any local value in a remote answer is unmistakable.
func newInfoServer(t *testing.T, ac *resources.AccessChecker) *Server {
	t.Helper()
	discSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(version.Info{GitVersion: "v1.1.1", Platform: "local/arch"})
	}))
	t.Cleanup(discSrv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: discSrv.URL})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	im := k8s.NewInformerManager(fakekube.NewSimpleClientset(), nil, logger)
	idx := im.Factory().Core().V1().Nodes().Informer().GetIndexer()
	for _, n := range []string{"l1", "l2", "l3"} {
		_ = idx.Add(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n}})
	}

	srv := testServer(t)
	srv.K8sClient = k8s.NewTestClientFactoryWithDynamic(cs, nil)
	srv.Informers = im
	if ac != nil {
		srv.ResourceHandler = &resources.Handler{AccessChecker: ac}
	}
	return srv
}

func remoteFake(nodes int) *fakekube.Clientset {
	var objs []runtime.Object
	for i := 0; i < nodes; i++ {
		objs = append(objs, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "r" + string(rune('a'+i))}})
	}
	cs := fakekube.NewSimpleClientset(objs...)
	cs.Discovery().(*fake.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.99.0", Platform: "linux/amd64"}
	return cs
}

func setRemote(srv *Server, fn func(context.Context, string, *auth.User) (kubernetes.Interface, error)) {
	srv.remoteInfoClient = fn
}

func doInfo(t *testing.T, srv *Server, clusterID string, withUser bool) (*httptest.ResponseRecorder, infoBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/info", nil)
	ctx := middleware.WithClusterID(req.Context(), clusterID)
	if withUser {
		ctx = auth.ContextWithUser(ctx, &auth.User{ID: "u", Username: "u", KubernetesUsername: "u", KubernetesGroups: []string{"dev"}})
	}
	w := httptest.NewRecorder()
	srv.handleClusterInfo(w, req.WithContext(ctx))
	var b infoBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return w, b
}

func TestClusterInfo_RemoteAnswersFromRemote(t *testing.T) {
	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return remoteFake(2), nil
	})
	w, b := doInfo(t, srv, "remote-1", true)
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if b.Data.KubernetesVersion != "v1.99.0" || b.Data.Platform != "linux/amd64" || b.Data.ClusterID != "remote-1" {
		t.Errorf("not remote data: %+v", b.Data)
	}
	if b.Data.NodeCount == nil || *b.Data.NodeCount != 2 {
		t.Errorf("nodeCount = %v; want 2", b.Data.NodeCount)
	}
}

func nodeLists(cs *fakekube.Clientset) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "nodes" {
			n++
		}
	}
	return n
}

// A user not proven allowed to list nodes gets a null count, and the gate
// stops the list before it reaches the cluster: the remote here would answer
// it, so only the gate can produce the null.
func TestClusterInfo_RemoteNodesNotProvenAllowedIsNullWithoutAList(t *testing.T) {
	cases := map[string]*resources.AccessChecker{
		"sar denied": resources.NewAlwaysDenyAccessChecker(),
		"no checker": nil,
	}
	for name, ac := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newInfoServer(t, ac)
			cs := remoteFake(2)
			setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
				return cs, nil
			})
			w, b := doInfo(t, srv, "remote-1", true)
			if w.Code != 200 {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"nodeCount":null`) {
				t.Errorf("nodeCount must be JSON null: %s", w.Body.String())
			}
			if n := nodeLists(cs); n != 0 {
				t.Errorf("%d node lists reached the cluster for a user not proven allowed", n)
			}
			if b.Data.KubernetesVersion != "v1.99.0" {
				t.Errorf("version = %q", b.Data.KubernetesVersion)
			}
		})
	}
}

// The count is null whenever the whole list was not observed: the cluster
// refused it, or it was still continuing at the paging cap.
func TestClusterInfo_RemoteNodesUnobservedIsNull(t *testing.T) {
	cases := map[string]clienttesting.ReactionFunc{
		"list refused": func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("no"))
		},
		"truncated": func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, &corev1.NodeList{
				ListMeta: metav1.ListMeta{Continue: "more"},
				Items:    []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "page"}}},
			}, nil
		},
	}
	for name, reaction := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
			setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
				cs := remoteFake(2)
				cs.PrependReactor("list", "nodes", reaction)
				return cs, nil
			})
			w, b := doInfo(t, srv, "remote-1", true)
			if w.Code != 200 {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"nodeCount":null`) {
				t.Errorf("nodeCount must be JSON null: %s", w.Body.String())
			}
			if b.Data.KubernetesVersion != "v1.99.0" {
				t.Errorf("version = %q", b.Data.KubernetesVersion)
			}
		})
	}
}

func TestClusterInfo_RemoteVersionFailureIs502(t *testing.T) {
	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		cs := remoteFake(2)
		cs.Discovery().(*fake.FakeDiscovery).PrependReactor("get", "version", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("secret-internal-version")
		})
		return cs, nil
	})
	w, b := doInfo(t, srv, "remote-1", true)
	if w.Code != 502 || b.Error == nil || b.Error.Message != clusterInfoReachMsg || b.Error.Detail != "" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-internal") {
		t.Errorf("raw error leaked: %s", w.Body.String())
	}
}

// stalledClientset is a remote whose API server accepts the version request
// and never answers it. The context-free ServerVersion stalls until release
// (standing in for a TCP connection that never dies); the context-aware one
// gives up when its context ends.
type stalledClientset struct {
	*fakekube.Clientset
	disc *stalledDiscovery
}

func (c stalledClientset) Discovery() discovery.DiscoveryInterfaces { return c.disc }

type stalledDiscovery struct {
	*fake.FakeDiscovery
	release <-chan struct{}
}

func (d *stalledDiscovery) ServerVersion() (*version.Info, error) {
	<-d.release
	return nil, errors.New("stalled")
}

func (d *stalledDiscovery) ServerVersionWithContext(ctx context.Context) (*version.Info, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.release:
		return nil, errors.New("stalled")
	}
}

func TestClusterInfo_RemoteStalledVersionIsBoundedByTheBudget(t *testing.T) {
	prev := clusterInfoTimeout
	clusterInfoTimeout = 200 * time.Millisecond
	t.Cleanup(func() { clusterInfoTimeout = prev })

	release := make(chan struct{})
	timer := time.AfterFunc(5*time.Second, func() { close(release) })
	t.Cleanup(func() {
		if timer.Stop() {
			close(release)
		}
	})

	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		cs := remoteFake(2)
		return stalledClientset{Clientset: cs, disc: &stalledDiscovery{FakeDiscovery: cs.Discovery().(*fake.FakeDiscovery), release: release}}, nil
	})
	start := time.Now()
	w, b := doInfo(t, srv, "remote-1", true)
	if elapsed := time.Since(start); elapsed > clusterInfoTimeout+time.Second {
		t.Errorf("handler took %v, want it bounded by the %v budget", elapsed, clusterInfoTimeout)
	}
	if w.Code != 502 || b.Error == nil || b.Error.Message != clusterInfoReachMsg || b.Error.Detail != "" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "deadline") {
		t.Errorf("context error leaked: %s", w.Body.String())
	}
}

func TestClusterInfo_RemoteNodeListFailureIs502(t *testing.T) {
	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		cs := remoteFake(2)
		cs.PrependReactor("list", "nodes", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("secret-internal-boom")
		})
		return cs, nil
	})
	w, b := doInfo(t, srv, "remote-1", true)
	if w.Code != 502 || b.Error == nil || b.Error.Message != clusterInfoNodesMsg || b.Error.Detail != "" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-internal") {
		t.Errorf("raw error leaked: %s", w.Body.String())
	}
}

func TestClusterInfo_RemoteResolveFailureIs502NoLocalFallback(t *testing.T) {
	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, errors.New("dial tcp 10.0.0.1: secret-internal")
	})
	w, b := doInfo(t, srv, "remote-1", true)
	if w.Code != 502 || b.Error == nil || b.Error.Message != clusterInfoReachMsg || b.Error.Detail != "" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-internal") || strings.Contains(w.Body.String(), "v1.1.1") {
		t.Errorf("leak: %s", w.Body.String())
	}
}

func TestClusterInfo_Unauthenticated401(t *testing.T) {
	srv := newInfoServer(t, nil)
	w, _ := doInfo(t, srv, "local", false)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

func TestClusterInfo_LocalUnchangedAndNeverResolvesRemote(t *testing.T) {
	srv := newInfoServer(t, resources.NewAlwaysAllowAccessChecker())
	setRemote(srv, func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		t.Error("local path resolved a remote client")
		return nil, errors.New("unexpected")
	})
	for _, id := range []string{"", "local"} {
		w, b := doInfo(t, srv, id, true)
		if w.Code != 200 || b.Data.KubernetesVersion != "v1.1.1" || b.Data.NodeCount == nil || *b.Data.NodeCount != 3 || b.Data.ClusterID != "test-cluster" {
			t.Errorf("id %q: %d %s", id, w.Code, w.Body.String())
		}
	}
}
