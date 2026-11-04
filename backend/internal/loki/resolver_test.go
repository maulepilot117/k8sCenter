package loki

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/store"
)

const remoteID = "remote-1"

// --- fakes ---

// spyLocal is the local Loki; it counts every time it is asked.
type spyLocal struct {
	client *Client
	calls  atomic.Int32
}

func (s *spyLocal) Client() *Client {
	s.calls.Add(1)
	return s.client
}

type fakeBindings struct {
	binding store.LokiBinding
	token   string
	err     error
	calls   atomic.Int32
}

func (f *fakeBindings) Get(context.Context, string) (store.LokiBinding, string, error) {
	f.calls.Add(1)
	return f.binding, f.token, f.err
}

// fakeClients hands out one fake clientset and records who asked.
type fakeClients struct {
	cs    *kfake.Clientset
	err   error
	calls atomic.Int32
}

func (f *fakeClients) ClientForCluster(context.Context, string, string, []string) (kubernetes.Interface, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.cs, nil
}

// fakeTunnel dials target (an httptest server) for every connection.
type fakeTunnel struct {
	target   string
	done     chan struct{}
	doneOnce sync.Once
	closed   atomic.Bool
}

func newFakeTunnel(target string) *fakeTunnel {
	return &fakeTunnel{target: target, done: make(chan struct{})}
}

func (t *fakeTunnel) DialContext(ctx context.Context) (net.Conn, error) {
	select {
	case <-t.done:
		return nil, k8s.ErrTunnelClosed
	default:
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", t.target)
}

func (t *fakeTunnel) Done() <-chan struct{} { return t.done }

func (t *fakeTunnel) Close() error {
	t.closed.Store(true)
	t.doneOnce.Do(func() { close(t.done) })
	return nil
}

type forwardCall struct {
	namespace, pod string
	port           int32
}

type fakeForwarder struct {
	tunnel *fakeTunnel
	err    error

	mu    sync.Mutex
	calls []forwardCall
}

func (f *fakeForwarder) PortForward(_ context.Context, _, _ string, _ []string, namespace, pod string, port int32) (k8s.Tunnel, error) {
	f.mu.Lock()
	f.calls = append(f.calls, forwardCall{namespace, pod, port})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.tunnel, nil
}

func (f *fakeForwarder) called() []forwardCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]forwardCall(nil), f.calls...)
}

// --- fixtures ---

// lokiSvc is a Service whose pods carry selector, serving http-metrics on
// port, targeting the pods' http-metrics container port.
func lokiSvc(namespace, name string, labels, selector map[string]string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{Name: "grpc", Port: 9095, TargetPort: intstr.FromString("grpc")},
				{Name: "http-metrics", Port: port, TargetPort: intstr.FromString("http-metrics")},
			},
		},
	}
}

// readyPod is a ready pod in namespace with labels, declaring http-metrics
// on containerPort.
func readyPod(namespace, name string, labels map[string]string, containerPort int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "loki",
			Ports: []corev1.ContainerPort{
				{Name: "grpc", ContainerPort: 9095, Protocol: corev1.ProtocolTCP},
				{Name: "http-metrics", ContainerPort: containerPort, Protocol: corev1.ProtocolTCP},
			},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour)),
			}},
		},
	}
}

var (
	gatewayLabels = map[string]string{"app.kubernetes.io/name": "loki", "app.kubernetes.io/component": "gateway"}
	realSelector  = map[string]string{"app": "real-gateway"}
	decoySelector = map[string]string{"app": "decoy-gateway"}
)

// realLoki is a well-known loki-gateway in monitoring and its pod.
func realLoki() []kruntime.Object {
	return []kruntime.Object{
		lokiSvc("monitoring", "loki-gateway", nil, realSelector, 80),
		readyPod("monitoring", "loki-gateway-0", realSelector, 8080),
	}
}

// decoyLoki is a gateway-labelled Service, in a namespace that sorts first,
// and its pod.
func decoyLoki() []kruntime.Object {
	return []kruntime.Object{
		lokiSvc("aaa-tenant", "evil-gateway", gatewayLabels, decoySelector, 80),
		readyPod("aaa-tenant", "evil-gateway-0", decoySelector, 8080),
	}
}

// remoteResolver builds a resolver whose remote cluster serves objs and
// whose tunnels reach lokiAddr.
func remoteResolver(t *testing.T, lokiAddr string, objs ...kruntime.Object) (*Resolver, *fakeClients, *fakeForwarder, *spyLocal) {
	t.Helper()
	local := &spyLocal{client: NewClient("http://local-loki.monitoring:3100", "")}
	clients := &fakeClients{cs: kfake.NewClientset(objs...)}
	fwd := &fakeForwarder{tunnel: newFakeTunnel(lokiAddr)}
	r := &Resolver{
		local:      local,
		clients:    clients,
		forwarder:  fwd,
		discovery:  remotecache.New[*lokiService](time.Minute, 0, nil),
		strict:     http.DefaultTransport,
		strictDial: (&net.Dialer{}).DialContext,
	}
	return r, clients, fwd, local
}

// labelsServer answers /loki/api/v1/labels with one label, recording each
// request it gets.
type labelsServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newLabelsServer(t *testing.T, tlsServer bool) *labelsServer {
	t.Helper()
	ls := &labelsServer{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.reqs = append(ls.reqs, r.Clone(context.Background()))
		ls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["namespace"]}`))
	})
	if tlsServer {
		ls.Server = httptest.NewTLSServer(h)
	} else {
		ls.Server = httptest.NewServer(h)
	}
	t.Cleanup(ls.Close)
	return ls
}

func (ls *labelsServer) requests() []*http.Request {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return append([]*http.Request(nil), ls.reqs...)
}

func asUnavailable(t *testing.T, err error) *UnavailableError {
	t.Helper()
	var ue *UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v (%T), want *UnavailableError", err, err)
	}
	return ue
}

func listedServices(cs *kfake.Clientset) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetResource().Resource == "services" {
			n++
		}
	}
	return n
}

// --- local vs remote ---

func TestResolve_LocalUsesDiscoverer(t *testing.T) {
	r, clients, fwd, local := remoteResolver(t, "127.0.0.1:1")
	got, err := r.Resolve(context.Background(), "local", "admin", nil)
	if err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	defer got.Close()
	if got.Client != local.client || got.Source != SourceLocal {
		t.Fatalf("local resolved to %+v, want the discoverer's client", got.Location)
	}
	if clients.calls.Load() != 0 || len(fwd.called()) != 0 {
		t.Fatal("a local request reached the remote path")
	}
}

func TestResolve_LocalWithoutLokiIsNotFound(t *testing.T) {
	r, _, _, local := remoteResolver(t, "127.0.0.1:1")
	local.client = nil
	_, err := r.Resolve(context.Background(), "", "admin", nil)
	if ue := asUnavailable(t, err); ue.Reason != ReasonLokiNotFound {
		t.Fatalf("reason = %q, want %q", ue.Reason, ReasonLokiNotFound)
	}
}

// Whatever the remote path answers, it never asks the local Loki.
func TestResolve_RemoteNeverUsesLocalLoki(t *testing.T) {
	lokiSrv := newLabelsServer(t, false)
	cases := map[string][]kruntime.Object{
		"found":     realLoki(),
		"not found": nil,
	}
	for name, objs := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, _, local := remoteResolver(t, lokiSrv.Listener.Addr().String(), objs...)
			got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
			if err == nil {
				got.Close()
			}
			if _, err := r.Locate(context.Background(), remoteID, "admin", nil); err != nil && name == "found" {
				t.Fatalf("Locate: %v", err)
			}
			if n := local.calls.Load(); n != 0 {
				t.Fatalf("remote request asked the local Loki %d times", n)
			}
		})
	}
}

// --- binding (AE3) ---

func TestResolve_BindingUsesConfiguredURLWithoutDiscovery(t *testing.T) {
	lokiSrv := newLabelsServer(t, true)
	r, clients, fwd, _ := remoteResolver(t, "127.0.0.1:1", realLoki()...)
	r.strict = lokiSrv.Client().Transport
	r.bindings = &fakeBindings{
		binding: store.LokiBinding{ClusterID: remoteID, URL: lokiSrv.URL + "/", TenantID: "tenant-a", HasToken: true},
		token:   "s3cret",
	}

	got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer got.Close()
	if got.Source != SourceBinding || got.URL != lokiSrv.URL+"/" {
		t.Fatalf("location = %+v, want the binding", got.Location)
	}
	if _, err := got.Client.Labels(context.Background(), time.Time{}, time.Time{}); err != nil {
		t.Fatalf("Labels: %v", err)
	}
	reqs := lokiSrv.requests()
	if len(reqs) != 1 {
		t.Fatalf("configured Loki got %d requests, want 1", len(reqs))
	}
	if h := reqs[0].Header.Get("Authorization"); h != "Bearer s3cret" {
		t.Fatalf("Authorization = %q, want the bound token", h)
	}
	if h := reqs[0].Header.Get("X-Scope-OrgID"); h != "tenant-a" {
		t.Fatalf("X-Scope-OrgID = %q, want the bound tenant", h)
	}
	if clients.calls.Load() != 0 || len(fwd.called()) != 0 {
		t.Fatal("a bound cluster ran automatic discovery")
	}

	loc, err := r.Locate(context.Background(), remoteID, "admin", nil)
	if err != nil || loc.Source != SourceBinding {
		t.Fatalf("Locate = %+v, %v; want the binding", loc, err)
	}
	if clients.calls.Load() != 0 {
		t.Fatal("Locate on a bound cluster ran automatic discovery")
	}
}

func TestResolve_BindingRefusesRedirect(t *testing.T) {
	var elsewhereHits atomic.Int32
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer elsewhere.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	r, _, _, _ := remoteResolver(t, "127.0.0.1:1")
	r.strict = redirector.Client().Transport
	r.bindings = &fakeBindings{binding: store.LokiBinding{URL: redirector.URL, HasToken: true}, token: "s3cret"}

	got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, err = got.Client.Labels(context.Background(), time.Time{}, time.Time{})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusFound {
		t.Fatalf("Labels error = %v, want the 302 itself", err)
	}
	if elsewhereHits.Load() != 0 {
		t.Fatal("the redirect was followed")
	}
	if ue := QueryFailure(err); ue == nil || ue.Reason != ReasonLokiUnreachable {
		t.Fatalf("QueryFailure = %+v, want loki_unreachable", ue)
	}
}

// A token is sent only when the store reports it usable, and only over
// https to the bound origin.
func TestResolve_BindingTokenOnlyToItsOrigin(t *testing.T) {
	t.Run("stale token refuses the binding", func(t *testing.T) {
		lokiSrv := newLabelsServer(t, true)
		r, clients, _, _ := remoteResolver(t, "127.0.0.1:1", realLoki()...)
		r.strict = lokiSrv.Client().Transport
		r.bindings = &fakeBindings{binding: store.LokiBinding{URL: lokiSrv.URL, TokenStale: true}}
		_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
		if ue := asUnavailable(t, err); ue.Reason != string(k8s.ReasonCredentialsInvalid) {
			t.Fatalf("reason = %q, want credentials_invalid", ue.Reason)
		}
		if len(lokiSrv.requests()) != 0 || clients.calls.Load() != 0 {
			t.Fatal("a stale binding was queried or fell back to discovery")
		}
	})
	t.Run("token the store did not mark usable is dropped", func(t *testing.T) {
		lokiSrv := newLabelsServer(t, true)
		r, _, _, _ := remoteResolver(t, "127.0.0.1:1")
		r.strict = lokiSrv.Client().Transport
		r.bindings = &fakeBindings{binding: store.LokiBinding{URL: lokiSrv.URL, HasToken: false}, token: "s3cret"}
		got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if _, err := got.Client.Labels(context.Background(), time.Time{}, time.Time{}); err != nil {
			t.Fatalf("Labels: %v", err)
		}
		if h := lokiSrv.requests()[0].Header.Get("Authorization"); h != "" {
			t.Fatalf("Authorization = %q, want none", h)
		}
	})
	t.Run("plain http never carries the token", func(t *testing.T) {
		lokiSrv := newLabelsServer(t, false)
		r, _, _, _ := remoteResolver(t, "127.0.0.1:1")
		r.bindings = &fakeBindings{binding: store.LokiBinding{URL: lokiSrv.URL, HasToken: true}, token: "s3cret"}
		got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if _, err := got.Client.Labels(context.Background(), time.Time{}, time.Time{}); err != nil {
			t.Fatalf("Labels: %v", err)
		}
		if h := lokiSrv.requests()[0].Header.Get("Authorization"); h != "" {
			t.Fatalf("Authorization = %q over http, want none", h)
		}
	})
}

func TestResolve_BindingReadFailures(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason string
	}{
		{"unregistered cluster", fmt.Errorf("getting loki binding: %w", pgx.ErrNoRows), string(k8s.ReasonClusterUnknown)},
		{"database down", errors.New("connection refused"), string(k8s.ReasonDBUnavailable)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, clients, _, local := remoteResolver(t, "127.0.0.1:1", realLoki()...)
			r.bindings = &fakeBindings{err: tc.err}
			_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
			if ue := asUnavailable(t, err); ue.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", ue.Reason, tc.reason)
			}
			if clients.calls.Load() != 0 || local.calls.Load() != 0 {
				t.Fatal("a failed binding read fell back to discovery or the local Loki")
			}
		})
	}
}

// --- automatic path ---

func TestResolve_PortForwardReachesChosenPod(t *testing.T) {
	lokiSrv := newLabelsServer(t, false)
	r, _, fwd, _ := remoteResolver(t, lokiSrv.Listener.Addr().String(), realLoki()...)

	got, err := r.Resolve(context.Background(), remoteID, "admin", []string{"admins"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != SourcePortForward || got.Namespace != "monitoring" || got.Service != "loki-gateway" {
		t.Fatalf("location = %+v, want monitoring/loki-gateway over port-forward", got.Location)
	}
	labels, err := got.Client.Labels(context.Background(), time.Time{}, time.Time{})
	if err != nil || len(labels) != 1 {
		t.Fatalf("Labels = %v, %v; want the remote Loki's answer", labels, err)
	}
	calls := fwd.called()
	if len(calls) != 1 || calls[0] != (forwardCall{"monitoring", "loki-gateway-0", 8080}) {
		t.Fatalf("port-forward calls = %+v, want monitoring/loki-gateway-0:8080", calls)
	}
	if host := lokiSrv.requests()[0].Host; host != "loki-gateway.monitoring.svc:80" {
		t.Fatalf("Host = %q, want the Service's in-cluster name", host)
	}

	got.Close()
	got.Close()
	if !fwd.tunnel.closed.Load() {
		t.Fatal("Close did not close the tunnel")
	}
	select {
	case <-got.Done():
	default:
		t.Fatal("Done is not closed after Close")
	}
	if _, err := got.Client.Labels(context.Background(), time.Time{}, time.Time{}); QueryFailure(err) == nil {
		t.Fatalf("a query after Close = %v, want Loki unreachable", err)
	}
}

// A Service anyone can label never outranks a well-known name, and label
// matches are picked by namespace, then name.
func TestResolve_DiscoveryOrder(t *testing.T) {
	t.Run("well-known name beats a gateway decoy", func(t *testing.T) {
		objs := append(realLoki(), decoyLoki()...)
		r, _, fwd, _ := remoteResolver(t, "127.0.0.1:1", objs...)
		loc, err := r.Locate(context.Background(), remoteID, "admin", nil)
		if err != nil {
			t.Fatalf("Locate: %v", err)
		}
		if loc.Namespace != "monitoring" || loc.Service != "loki-gateway" {
			t.Fatalf("chose %s/%s, want monitoring/loki-gateway", loc.Namespace, loc.Service)
		}
		got, err := r.Resolve(context.Background(), remoteID, "admin", nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		got.Close()
		if calls := fwd.called(); calls[0].namespace != "monitoring" {
			t.Fatalf("tunnel opened to %+v, want the monitoring pod", calls[0])
		}
	})
	t.Run("label matches tie-break by namespace then name", func(t *testing.T) {
		objs := []kruntime.Object{
			lokiSvc("zeta", "loki-gw", gatewayLabels, decoySelector, 80),
			lokiSvc("beta", "loki-gw-b", gatewayLabels, decoySelector, 80),
			lokiSvc("beta", "loki-gw-a", gatewayLabels, decoySelector, 80),
			// Any Loki component ranks below every gateway.
			lokiSvc("alpha", "loki-read", map[string]string{"app.kubernetes.io/name": "loki"}, decoySelector, 3100),
		}
		r, clients, _, _ := remoteResolver(t, "127.0.0.1:1", objs...)
		// The API server's list order is not a ranking: answer in the
		// order above, which the fake would otherwise sort. The fake still
		// applies the label selector to what the reactor returns.
		clients.cs.PrependReactor("list", "services", func(k8stesting.Action) (bool, kruntime.Object, error) {
			list := &corev1.ServiceList{}
			for _, o := range objs {
				list.Items = append(list.Items, *o.(*corev1.Service))
			}
			return true, list, nil
		})
		loc, err := r.Locate(context.Background(), remoteID, "admin", nil)
		if err != nil {
			t.Fatalf("Locate: %v", err)
		}
		if loc.Namespace != "beta" || loc.Service != "loki-gw-a" {
			t.Fatalf("chose %s/%s, want beta/loki-gw-a", loc.Namespace, loc.Service)
		}
	})
	t.Run("a refused well-known lookup fails rather than falling through", func(t *testing.T) {
		objs := append(realLoki(), decoyLoki()...)
		r, clients, _, _ := remoteResolver(t, "127.0.0.1:1", objs...)
		clients.cs.PrependReactor("get", "services", func(k8stesting.Action) (bool, kruntime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "loki-gateway", errors.New("denied"))
		})
		_, err := r.Locate(context.Background(), remoteID, "admin", nil)
		if ue := asUnavailable(t, err); ue.Reason != string(k8s.ReasonForbidden) {
			t.Fatalf("reason = %q, want forbidden", ue.Reason)
		}
		for _, a := range clients.cs.Actions() {
			if a.GetVerb() == "list" {
				t.Fatal("the label search ran after a refused well-known lookup")
			}
		}
	})
}

func TestResolve_NoLokiServiceIsNotFound(t *testing.T) {
	r, _, fwd, _ := remoteResolver(t, "127.0.0.1:1")
	_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
	if ue := asUnavailable(t, err); ue.Reason != ReasonLokiNotFound || ue.Status != http.StatusNotFound {
		t.Fatalf("got %+v, want loki_not_found 404", ue)
	}
	if len(fwd.called()) != 0 {
		t.Fatal("a tunnel was opened with no Loki Service")
	}
}

// AE2: an admin who may not port-forward in Loki's namespace is told so.
func TestResolve_PortForwardRefusedIsForbidden(t *testing.T) {
	r, _, fwd, _ := remoteResolver(t, "127.0.0.1:1", realLoki()...)
	fwd.err = apierrors.NewForbidden(schema.GroupResource{Resource: "pods/portforward"}, "loki-gateway-0", errors.New("denied"))
	_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
	ue := asUnavailable(t, err)
	if ue.Reason != string(k8s.ReasonForbidden) || ue.Status != http.StatusForbidden {
		t.Fatalf("got %+v, want forbidden 403", ue)
	}
	if !strings.Contains(ue.Message, "namespace monitoring") {
		t.Fatalf("message %q does not name Loki's namespace", ue.Message)
	}
}

func TestResolve_RemoteFailures(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*fakeClients, *fakeForwarder)
		objs   []kruntime.Object
		reason string
	}{
		{
			name:   "unregistered cluster",
			setup:  func(c *fakeClients, _ *fakeForwarder) { c.err = fmt.Errorf("cluster lookup: %w", pgx.ErrNoRows) },
			reason: string(k8s.ReasonClusterUnknown),
		},
		{
			name:   "no ready Loki pod",
			objs:   []kruntime.Object{lokiSvc("monitoring", "loki-gateway", nil, realSelector, 80)},
			reason: ReasonLokiUnreachable,
		},
		{
			name: "tunnel cluster unreachable",
			objs: realLoki(),
			setup: func(_ *fakeClients, f *fakeForwarder) {
				f.err = k8s.TargetError{Err: fmt.Errorf("lookup: %w", pgx.ErrNoRows)}
			},
			reason: string(k8s.ReasonClusterUnknown),
		},
		{
			name:   "tunnel refused by the pod",
			objs:   realLoki(),
			setup:  func(_ *fakeClients, f *fakeForwarder) { f.err = k8s.ErrPortForwardFailed },
			reason: ReasonLokiUnreachable,
		},
		{
			name: "stored credentials rejected",
			objs: realLoki(),
			setup: func(c *fakeClients, _ *fakeForwarder) {
				c.cs.PrependReactor("get", "services", func(k8stesting.Action) (bool, kruntime.Object, error) {
					return true, nil, apierrors.NewUnauthorized("bad token")
				})
			},
			reason: string(k8s.ReasonCredentialsInvalid),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, clients, fwd, local := remoteResolver(t, "127.0.0.1:1", tc.objs...)
			if tc.setup != nil {
				tc.setup(clients, fwd)
			}
			_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
			if ue := asUnavailable(t, err); ue.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q (%v)", ue.Reason, tc.reason, ue.Err)
			}
			if local.calls.Load() != 0 {
				t.Fatal("a remote failure fell back to the local Loki")
			}
		})
	}
}

func TestResolve_NoRouterIsUnsupported(t *testing.T) {
	r := NewResolver(nil, nil, nil, nil)
	if r.local != nil || r.bindings != nil || r.clients != nil || r.forwarder != nil {
		t.Fatal("a nil dependency became a non-nil interface")
	}
	_, err := r.Resolve(context.Background(), remoteID, "admin", nil)
	if ue := asUnavailable(t, err); ue.Reason != string(k8s.ReasonUnsupportedPlatform) {
		t.Fatalf("reason = %q, want unsupported_platform", ue.Reason)
	}
	if _, err := r.Resolve(context.Background(), "local", "admin", nil); asUnavailable(t, err).Reason != ReasonLokiNotFound {
		t.Fatalf("local with no discoverer = %v, want loki_not_found", err)
	}
}

// The found Service is cached per identity, and Evict drops it.
func TestResolve_DiscoveryCachedPerIdentity(t *testing.T) {
	r, clients, _, _ := remoteResolver(t, "127.0.0.1:1", realLoki()...)
	locate := func(user string) {
		t.Helper()
		if _, err := r.Locate(context.Background(), remoteID, user, nil); err != nil {
			t.Fatalf("Locate(%s): %v", user, err)
		}
	}
	locate("alice")
	first := listedServices(clients.cs)
	locate("alice")
	if n := listedServices(clients.cs); n != first {
		t.Fatalf("a second lookup for the same user read services again (%d -> %d)", first, n)
	}
	locate("bob")
	afterBob := listedServices(clients.cs)
	if afterBob == first {
		t.Fatal("another identity was served the first identity's discovery")
	}
	r.Evict(remoteID)
	locate("alice")
	if n := listedServices(clients.cs); n == afterBob {
		t.Fatal("Evict did not drop the cached discovery")
	}
}

// --- client ---

func TestQueryFailure(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason string // "" means not an unavailability
	}{
		{"bad query", &StatusError{Code: http.StatusBadRequest}, ""},
		{"unauthorized", &StatusError{Code: http.StatusUnauthorized}, string(k8s.ReasonCredentialsInvalid)},
		{"forbidden", &StatusError{Code: http.StatusForbidden}, string(k8s.ReasonCredentialsInvalid)},
		{"redirect", &StatusError{Code: http.StatusMovedPermanently}, ReasonLokiUnreachable},
		{"server error", fmt.Errorf("wrapped: %w", &StatusError{Code: http.StatusBadGateway}), ReasonLokiUnreachable},
		{"transport", fmt.Errorf("%w: dial refused", errLokiTransport), ReasonLokiUnreachable},
		{"tunnel closed", k8s.ErrTunnelClosed, ReasonLokiUnreachable},
		{"canceled", fmt.Errorf("%w: %w", errLokiTransport, context.Canceled), ""},
		{"decode", errors.New("decoding response: unexpected EOF"), ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := QueryFailure(tc.err)
			if tc.reason == "" {
				if got != nil {
					t.Fatalf("QueryFailure = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Reason != tc.reason {
				t.Fatalf("QueryFailure = %+v, want %q", got, tc.reason)
			}
		})
	}
}

// Loki's error body never reaches the returned error.
func TestClient_ErrorBodyNotReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":"parse error near <script>secret-host:3100</script>"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "").Labels(context.Background(), time.Time{}, time.Time{})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusBadRequest {
		t.Fatalf("error = %v, want a 400 StatusError", err)
	}
	if strings.Contains(err.Error(), "secret-host") || strings.Contains(err.Error(), "parse error") {
		t.Fatalf("error %q carries Loki's body", err)
	}
}

func TestClient_DialTailHeaders(t *testing.T) {
	type seen struct{ auth, tenant string }
	got := make(chan seen, 1)
	upgrader := websocket.Upgrader{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Header.Get("Authorization"), r.Header.Get("X-Scope-OrgID")}
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = c.Close()
		}
	})

	t.Run("wss carries token and tenant", func(t *testing.T) {
		srv := httptest.NewTLSServer(h)
		defer srv.Close()
		c := newRemoteClient(srv.URL, "tenant-a", "s3cret", srv.Client().Transport, (&net.Dialer{}).DialContext)
		c.tailTLS = srv.Client().Transport.(*http.Transport).TLSClientConfig
		conn, err := c.DialTail(context.Background(), `{app="x"}`, 0, 10)
		if err != nil {
			t.Fatalf("DialTail: %v", err)
		}
		_ = conn.Close()
		if s := <-got; s.auth != "Bearer s3cret" || s.tenant != "tenant-a" {
			t.Fatalf("headers = %+v, want token and tenant", s)
		}
	})
	t.Run("ws never carries the token", func(t *testing.T) {
		srv := httptest.NewServer(h)
		defer srv.Close()
		c := newRemoteClient(srv.URL, "", "s3cret", http.DefaultTransport, (&net.Dialer{}).DialContext)
		conn, err := c.DialTail(context.Background(), `{app="x"}`, 0, 10)
		if err != nil {
			t.Fatalf("DialTail: %v", err)
		}
		_ = conn.Close()
		if s := <-got; s.auth != "" {
			t.Fatalf("Authorization = %q over ws, want none", s.auth)
		}
	})
	t.Run("refused handshake is a status error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		c := newRemoteClient(srv.URL, "", "", http.DefaultTransport, (&net.Dialer{}).DialContext)
		_, err := c.DialTail(context.Background(), `{app="x"}`, 0, 10)
		if ue := QueryFailure(err); ue == nil || ue.Reason != string(k8s.ReasonCredentialsInvalid) {
			t.Fatalf("DialTail error %v classified as %+v, want credentials_invalid", err, ue)
		}
	})
}

// Local discovery shares the order but skips a step it cannot read.
func TestSearchLokiService_LocalSkipsUnreadableSteps(t *testing.T) {
	cs := kfake.NewClientset(decoyLoki()...)
	cs.PrependReactor("get", "services", func(k8stesting.Action) (bool, kruntime.Object, error) {
		return true, nil, errors.New("transient")
	})
	found, err := searchLokiService(context.Background(), cs, false)
	if err != nil || found == nil || found.svc.Name != "evil-gateway" || found.via != viaServiceLabel {
		t.Fatalf("lenient search = %+v, %v; want the label match", found, err)
	}
	if found.port.Name != "http-metrics" {
		t.Fatalf("port = %q, want http-metrics over the first-declared grpc", found.port.Name)
	}
	if _, err := searchLokiService(context.Background(), cs, true); err == nil {
		t.Fatal("strict search skipped an unreadable step")
	}
}
