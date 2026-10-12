package networking

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
	flowpb "github.com/kubecenter/kubecenter/internal/networking/hubbleproto/flow"
	observerpb "github.com/kubecenter/kubecenter/internal/networking/hubbleproto/observer"
)

// fakeRelay is a Hubble Relay gRPC server on an in-memory listener. It
// answers ServerStatus and serves one flow per GetFlows call; a followed
// stream then stays open until the client goes away.
type fakeRelay struct {
	observerpb.UnimplementedObserverServer
	lis *bufconn.Listener
	srv *grpc.Server
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	r := &fakeRelay{lis: bufconn.Listen(1 << 20), srv: grpc.NewServer()}
	observerpb.RegisterObserverServer(r.srv, r)
	go func() { _ = r.srv.Serve(r.lis) }()
	t.Cleanup(r.srv.Stop)
	return r
}

func (r *fakeRelay) ServerStatus(context.Context, *observerpb.ServerStatusRequest) (*observerpb.ServerStatusResponse, error) {
	return &observerpb.ServerStatusResponse{}, nil
}

func (r *fakeRelay) GetFlows(req *observerpb.GetFlowsRequest, stream grpc.ServerStreamingServer[observerpb.GetFlowsResponse]) error {
	flow := &flowpb.Flow{
		Verdict: flowpb.Verdict_FORWARDED,
		Source:  &flowpb.Endpoint{Namespace: "default", PodName: "web-1"},
	}
	if err := stream.Send(&observerpb.GetFlowsResponse{ResponseTypes: &observerpb.GetFlowsResponse_Flow{Flow: flow}}); err != nil {
		return err
	}
	if req.GetFollow() {
		<-stream.Context().Done()
	}
	return nil
}

// fakeTunnel is a k8s.Tunnel whose connections come from dial.
type fakeTunnel struct {
	dial   func(ctx context.Context) (net.Conn, error)
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool
}

func newFakeTunnel(dial func(ctx context.Context) (net.Conn, error)) *fakeTunnel {
	return &fakeTunnel{dial: dial, done: make(chan struct{})}
}

func (t *fakeTunnel) DialContext(ctx context.Context) (net.Conn, error) { return t.dial(ctx) }
func (t *fakeTunnel) Done() <-chan struct{}                            { return t.done }
func (t *fakeTunnel) Close() error {
	t.closed.Store(true)
	t.once.Do(func() { close(t.done) })
	return nil
}

// fakeTunnels records each port-forward and answers with tunnel() or err.
type fakeTunnels struct {
	tunnel func() k8s.Tunnel
	err    error

	mu      sync.Mutex
	targets []relayTarget
}

func (f *fakeTunnels) PortForwarderFor(context.Context, string, string, []string) (PodPortForwarder, error) {
	return f, nil
}

func (f *fakeTunnels) PortForward(_ context.Context, namespace, pod string, port int32) (k8s.Tunnel, error) {
	f.mu.Lock()
	f.targets = append(f.targets, relayTarget{namespace: namespace, pod: pod, port: port})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.tunnel(), nil
}

func (f *fakeTunnels) opened() []relayTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]relayTarget(nil), f.targets...)
}

// fakeAccess answers every access check with allowed.
type fakeAccess struct {
	allowed bool
	calls   atomic.Int32
}

func (a *fakeAccess) CanAccess(_ context.Context, _, _ string, _ []string, verb, resource, _ string) (bool, error) {
	a.calls.Add(1)
	return a.allowed && verb == "list" && resource == "pods", nil
}

func relayService(port int32, target intstr.IntOrString, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: relayServiceName},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: relayPortName, Port: port, TargetPort: target}},
		},
	}
}

func relayPod(name string, readySince time.Time, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: name, Labels: map[string]string{"k8s-app": "hubble-relay"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "hubble-relay",
			Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 4245, Protocol: corev1.ProtocolTCP}},
		}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status, LastTransitionTime: metav1.NewTime(readySince)}},
		},
	}
}

func relayConfig(yaml string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: relayConfigMapName},
		Data:       map[string]string{relayConfigKey: yaml},
	}
}

const plaintextRelayConfig = "peer-service: unix:///var/run/cilium/hubble.sock\nlisten-address: :4245\ndisable-server-tls: true\n"

// relayCluster is a remote Cilium cluster with objs added, and a default
// relay (Service, one ready pod, plaintext config) unless objs replace it.
func relayCluster(objs ...kruntime.Object) *kfake.Clientset {
	c := remoteCiliumCluster()
	for _, o := range objs {
		if err := c.Tracker().Add(o); err != nil {
			panic(err)
		}
	}
	return c
}

func defaultRelayObjects() []kruntime.Object {
	return []kruntime.Object{
		relayService(80, intstr.FromString("grpc"), map[string]string{"k8s-app": "hubble-relay"}),
		relayPod("hubble-relay-a", time.Now().Add(-time.Hour), true),
		relayConfig(plaintextRelayConfig),
	}
}

type flowTestEnv struct {
	h       *Handler
	cluster *kfake.Clientset
	tunnels *fakeTunnels
	access  *fakeAccess
	relay   *fakeRelay
}

func newFlowTestEnv(t *testing.T, cluster *kfake.Clientset) *flowTestEnv {
	t.Helper()
	relay := newFakeRelay(t)
	tunnels := &fakeTunnels{tunnel: func() k8s.Tunnel {
		return newFakeTunnel(func(ctx context.Context) (net.Conn, error) { return relay.lis.DialContext(ctx) })
	}}
	access := &fakeAccess{allowed: true}
	h := &Handler{
		Detector: localDetectorWithFlannel(),
		Clients:  &fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: cluster}},
		Tunnels:  tunnels,
		Access:   access,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &flowTestEnv{h: h, cluster: cluster, tunnels: tunnels, access: access, relay: relay}
}

func (e *flowTestEnv) open(kind FlowSourceKind) (*RemoteFlowSource, error) {
	return e.h.OpenRemoteFlowSource(context.Background(), netRemoteCluster, networkingTestUser, "default", kind)
}

func wantFlowReason(t *testing.T, err error, reason string) *FlowSourceError {
	t.Helper()
	var fe *FlowSourceError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a FlowSourceError with reason %s", err, reason)
	}
	if fe.Reason != reason {
		t.Fatalf("reason = %q (%v), want %q", fe.Reason, fe.Err, reason)
	}
	return fe
}

func TestOpenRemoteFlowSource_PlaintextRelayServesFlows(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	src, err := env.open(FlowSourceREST)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer src.Close()

	flows, err := src.GetFlows(context.Background(), "default", "", 10)
	if err != nil {
		t.Fatalf("GetFlows: %v", err)
	}
	if len(flows) != 1 || flows[0].SrcPod != "web-1" {
		t.Errorf("flows = %+v, want the relay's one flow", flows)
	}
	// The named targetPort grpc resolves to the pod's container port.
	if got := env.tunnels.opened(); len(got) != 1 || got[0] != (relayTarget{namespace: "kube-system", pod: "hubble-relay-a", port: 4245}) {
		t.Errorf("tunnels = %+v, want one to kube-system/hubble-relay-a:4245", got)
	}
	if src.RelayNamespace() != "kube-system" {
		t.Errorf("RelayNamespace = %q, want kube-system", src.RelayNamespace())
	}
}

func TestOpenRemoteFlowSource_ServerCertConfigRefusedWithoutDial(t *testing.T) {
	objs := defaultRelayObjects()
	objs[2] = relayConfig("listen-address: :4245\ntls-relay-server-cert-file: /var/lib/hubble-relay/tls/server.crt\n")
	env := newFlowTestEnv(t, relayCluster(objs...))

	_, err := env.open(FlowSourceREST)
	fe := wantFlowReason(t, err, ReasonRelayTLSUnsupported)
	if fe.Status != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", fe.Status)
	}
	if n := len(env.tunnels.opened()); n != 0 {
		t.Errorf("opened %d tunnels, want none", n)
	}
}

func TestOpenRemoteFlowSource_ForbiddenConfigStillDialsPlaintext(t *testing.T) {
	objs := defaultRelayObjects()
	// A Service port of 443 is no TLS signal.
	objs[0] = relayService(443, intstr.FromString("grpc"), map[string]string{"k8s-app": "hubble-relay"})
	cluster := relayCluster(objs...)
	cluster.PrependReactor("get", "configmaps", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		if a.(k8stesting.GetAction).GetName() == relayConfigMapName {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, relayConfigMapName, errors.New("denied"))
		}
		return false, nil, nil
	})
	env := newFlowTestEnv(t, cluster)

	src, err := env.open(FlowSourceREST)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer src.Close()
	if flows, err := src.GetFlows(context.Background(), "default", "", 10); err != nil || len(flows) != 1 {
		t.Errorf("GetFlows = %d flows, %v; want one flow", len(flows), err)
	}
}

func TestOpenRemoteFlowSource_ServiceLookup(t *testing.T) {
	t.Run("missing service is relay_not_found", func(t *testing.T) {
		env := newFlowTestEnv(t, relayCluster())
		_, err := env.open(FlowSourceREST)
		if fe := wantFlowReason(t, err, ReasonRelayNotFound); fe.Status != http.StatusNotFound {
			t.Errorf("status = %d, want 404", fe.Status)
		}
	})
	t.Run("forbidden service get is forbidden", func(t *testing.T) {
		cluster := relayCluster(defaultRelayObjects()...)
		cluster.PrependReactor("get", "services", func(k8stesting.Action) (bool, kruntime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, relayServiceName, errors.New("denied"))
		})
		env := newFlowTestEnv(t, cluster)
		_, err := env.open(FlowSourceREST)
		if fe := wantFlowReason(t, err, "forbidden"); fe.Status != http.StatusForbidden {
			t.Errorf("status = %d, want 403", fe.Status)
		}
	})
	t.Run("empty selector is relay_not_found without a pod list", func(t *testing.T) {
		objs := defaultRelayObjects()
		objs[0] = relayService(80, intstr.FromString("grpc"), nil)
		cluster := relayCluster(objs...)
		env := newFlowTestEnv(t, cluster)
		_, err := env.open(FlowSourceREST)
		wantFlowReason(t, err, ReasonRelayNotFound)
		for _, a := range cluster.Actions() {
			if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
				t.Errorf("pods were listed for an empty selector")
			}
		}
	})
	t.Run("undeclared targetPort is relay_not_found", func(t *testing.T) {
		objs := defaultRelayObjects()
		objs[0] = relayService(80, intstr.FromInt32(9999), map[string]string{"k8s-app": "hubble-relay"})
		env := newFlowTestEnv(t, relayCluster(objs...))
		_, err := env.open(FlowSourceREST)
		wantFlowReason(t, err, ReasonRelayNotFound)
	})
}

func TestOpenRemoteFlowSource_PodSelection(t *testing.T) {
	t.Run("no ready pod is relay_not_ready", func(t *testing.T) {
		objs := defaultRelayObjects()
		objs[1] = relayPod("hubble-relay-a", time.Now(), false)
		env := newFlowTestEnv(t, relayCluster(objs...))
		_, err := env.open(FlowSourceREST)
		if fe := wantFlowReason(t, err, ReasonRelayNotReady); fe.Status != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", fe.Status)
		}
	})
	t.Run("longest-ready pod wins", func(t *testing.T) {
		objs := append(defaultRelayObjects(),
			relayPod("hubble-relay-old", time.Now().Add(-48*time.Hour), true),
			relayPod("hubble-relay-new", time.Now().Add(-time.Minute), true))
		env := newFlowTestEnv(t, relayCluster(objs...))
		src, err := env.open(FlowSourceREST)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		src.Close()
		if got := env.tunnels.opened(); len(got) != 1 || got[0].pod != "hubble-relay-old" {
			t.Errorf("tunnels = %+v, want hubble-relay-old", got)
		}
	})
}

func TestOpenRemoteFlowSource_TLSReplyIsRelayTLSUnsupported(t *testing.T) {
	tlsRelay := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(tlsRelay.Close)
	objs := defaultRelayObjects()[:2] // no relay config: the mode is unknown
	env := newFlowTestEnv(t, relayCluster(objs...))
	env.tunnels.tunnel = func() k8s.Tunnel {
		return newFakeTunnel(func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", tlsRelay.Listener.Addr().String())
		})
	}

	_, err := env.open(FlowSourceREST)
	wantFlowReason(t, err, ReasonRelayTLSUnsupported)
}

func TestOpenRemoteFlowSource_NamespaceDeniedOpensNoTunnel(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	env.access.allowed = false
	_, err := env.open(FlowSourceREST)
	if fe := wantFlowReason(t, err, "forbidden"); fe.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", fe.Status)
	}
	if n := len(env.tunnels.opened()); n != 0 {
		t.Errorf("opened %d tunnels, want none", n)
	}
}

func TestOpenRemoteFlowSource_LiveStreamCapPerUser(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	var open []*RemoteFlowSource
	for range maxLiveStreamsPerUser {
		src, err := env.open(FlowSourceLive)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		open = append(open, src)
	}

	start := time.Now()
	_, err := env.open(FlowSourceLive)
	fe := wantFlowReason(t, err, ReasonTunnelLimit)
	if !fe.Retryable || fe.Status != http.StatusTooManyRequests {
		t.Errorf("refusal = %+v, want a retryable 429", fe)
	}
	if waited := time.Since(start); waited < flowSlotWait-100*time.Millisecond {
		t.Errorf("refused after %v, want a wait of about %v", waited, flowSlotWait)
	}

	// A slot that frees during the wait lets the next stream in.
	time.AfterFunc(300*time.Millisecond, open[0].Close)
	src, err := env.open(FlowSourceLive)
	if err != nil {
		t.Fatalf("open while a slot frees: %v", err)
	}
	src.Close()
	for _, s := range open[1:] {
		s.Close()
	}
}

func TestTunnelDialer_FailsAtOnceAfterClose(t *testing.T) {
	var dials atomic.Int32
	tun := newFakeTunnel(func(context.Context) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	})
	_ = tun.Close()
	start := time.Now()
	if _, err := tunnelDialer(tun)(context.Background(), "hubble-relay"); !errors.Is(err, k8s.ErrTunnelClosed) {
		t.Fatalf("err = %v, want ErrTunnelClosed", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("dial took %v, want an immediate failure", elapsed)
	}
	if n := dials.Load(); n != 0 {
		t.Errorf("tunnel dialed %d times, want 0", n)
	}
}

func TestRemoteFlowSource_CloseReleasesEverything(t *testing.T) {
	env := newFlowTestEnv(t, relayCluster(defaultRelayObjects()...))
	var tun *fakeTunnel
	env.tunnels.tunnel = func() k8s.Tunnel {
		tun = newFakeTunnel(func(ctx context.Context) (net.Conn, error) { return env.relay.lis.DialContext(ctx) })
		return tun
	}
	baseline := runtime.NumGoroutine()

	src, err := env.open(FlowSourceLive)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan FlowRecord, 1)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- src.StreamFlows(ctx, "default", "", func(f FlowRecord) {
			select {
			case got <- f:
			default:
			}
		})
	}()
	select {
	case f := <-got:
		if f.SrcPod != "web-1" {
			t.Errorf("streamed flow = %+v, want web-1", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no flow streamed through the tunnel")
	}
	if reason := src.CloseReason(); reason != ReasonRelayRestarted {
		t.Errorf("CloseReason before Close = %q, want %q", reason, ReasonRelayRestarted)
	}
	cancel()
	<-streamDone
	src.Close()
	src.Close() // idempotent

	if !tun.closed.Load() {
		t.Error("tunnel not closed")
	}
	if reason := src.CloseReason(); reason != "" {
		t.Errorf("CloseReason after Close = %q, want empty", reason)
	}
	// The slot is free again.
	if release, ok := env.h.slots.acquire(context.Background(), FlowSourceLive, netRemoteCluster, "alice"); !ok {
		t.Error("slot not released")
	} else {
		release()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d after close, baseline %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
