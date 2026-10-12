package networking

// Remote Hubble flows (#608 slice 5, PR 5b). A remote cluster's Hubble Relay
// is reached by port-forward through that cluster's API server, as the
// requesting user, so no relay address or credential is stored per cluster
// (KTD8). One opener serves both the REST flows endpoint and the live
// WebSocket stream (KTD15): it checks the user may see the namespace's
// flows, takes a tunnel slot, finds the relay and its mode as the user, and
// opens the tunnel and a gRPC client over it.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// Reasons a remote flow source refuses or ends (KTD5).
const (
	ReasonRelayNotFound       = "relay_not_found"
	ReasonRelayNotReady       = "relay_not_ready"
	ReasonRelayTLSUnsupported = "relay_tls_unsupported"
	ReasonTunnelLimit         = "tunnel_limit"
	ReasonRelayRestarted      = "relay_restarted"
)

const (
	relayServiceName   = "hubble-relay"
	relayConfigMapName = "hubble-relay-config"
	relayConfigKey     = "config.yaml"
	relayPortName      = "grpc"

	// relaySetupTimeout bounds resolving the relay and opening the tunnel
	// (KTD9): the port-forward Dial is not context-aware.
	relaySetupTimeout = 15 * time.Second
	// relayProbeTimeout bounds the gRPC status call that proves the relay
	// answers, and the TLS probe that follows a failed one.
	relayProbeTimeout = 5 * time.Second
)

// Tunnel capacity (KTD9). Live streams are capped per user and per cluster,
// inside the server's global cap; REST tunnels have their own per-cluster
// limit. A request waits up to flowSlotWait for a slot before refusing,
// which absorbs FlowViewer's close-then-reopen on a filter change.
const (
	maxLiveStreamsPerUser    = 3
	maxLiveStreamsPerCluster = 20
	maxRESTTunnelsPerCluster = 10
	flowSlotWait             = 2 * time.Second
)

// PodPortForwarder opens port-forward tunnels into one remote cluster as one
// user. *k8s.StreamTransport implements it.
type PodPortForwarder interface {
	PortForward(ctx context.Context, namespace, pod string, port int32) (k8s.Tunnel, error)
}

// RemoteTunnels opens a PodPortForwarder for a remote cluster and identity.
type RemoteTunnels interface {
	PortForwarderFor(ctx context.Context, clusterID, username string, groups []string) (PodPortForwarder, error)
}

type routerTunnels struct{ router *k8s.ClusterRouter }

func (t routerTunnels) PortForwarderFor(ctx context.Context, clusterID, username string, groups []string) (PodPortForwarder, error) {
	st, err := t.router.StreamTransportFor(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// TunnelsFrom returns the RemoteTunnels backed by router, or a true nil
// interface when router is nil (docs/solutions/go-typed-nil-interface.md).
func TunnelsFrom(router *k8s.ClusterRouter) RemoteTunnels {
	if router == nil {
		return nil
	}
	return routerTunnels{router: router}
}

// NamespaceAccess answers whether a user may perform a verb on a resource in
// a namespace of a cluster. *resources.AccessChecker implements it.
type NamespaceAccess interface {
	CanAccess(ctx context.Context, clusterID, username string, groups []string, verb, resource, namespace string) (bool, error)
}

// FlowSourceKind says which capacity a flow source draws on.
type FlowSourceKind int

const (
	// FlowSourceREST is one flows request; its tunnel lives as long as
	// the request.
	FlowSourceREST FlowSourceKind = iota
	// FlowSourceLive is a live WebSocket stream.
	FlowSourceLive
)

// FlowSourceError is a remote flow source that could not open. Reason is a
// KTD5 reason and Message a fixed client message; Err, which can carry the
// remote API server's address or text, is for logs only.
type FlowSourceError struct {
	Reason    string
	Status    int
	Message   string
	Retryable bool
	Err       error
	// read marks a failed remote read, which the REST handler answers
	// through its usual remote failure writer.
	read bool
}

func (e *FlowSourceError) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason + ": " + e.Message
}

func (e *FlowSourceError) Unwrap() error { return e.Err }

func flowRefusal(reason string, status int, message string, retryable bool, err error) *FlowSourceError {
	return &FlowSourceError{Reason: reason, Status: status, Message: message, Retryable: retryable, Err: err}
}

func errRelayNotFound(err error) *FlowSourceError {
	return flowRefusal(ReasonRelayNotFound, http.StatusNotFound,
		"Hubble Relay was not found in kube-system or cilium on the selected cluster", false, err)
}

func errRelayNotReady(err error) *FlowSourceError {
	return flowRefusal(ReasonRelayNotReady, http.StatusServiceUnavailable,
		"Hubble Relay on the selected cluster is not ready", true, err)
}

func errRelayTLS() *FlowSourceError {
	return flowRefusal(ReasonRelayTLSUnsupported, http.StatusNotImplemented,
		"Hubble Relay on the selected cluster uses TLS, which k8sCenter cannot connect to yet", false, nil)
}

// flowReadFailure classifies a failed remote read for a flow source. The
// reason follows the shared remote vocabulary; the message is fixed.
func flowReadFailure(err error) *FlowSourceError {
	fe := &FlowSourceError{Err: err, read: true, Status: http.StatusBadGateway}
	var target k8s.TargetError
	var status apierrors.APIStatus
	switch {
	case errors.Is(err, errRemoteNotConfigured):
		fe.Reason, fe.Status, fe.Message = string(k8s.ReasonUnsupportedPlatform), http.StatusNotImplemented, "remote Hubble flows are not available"
	case apierrors.IsForbidden(err):
		fe.Reason, fe.Status, fe.Message = string(k8s.ReasonForbidden), http.StatusForbidden, "you do not have permission to read Hubble flows on the selected cluster"
	case errors.As(err, &target):
		fe.Reason = string(k8s.ClassifyTargetErr(target.Err))
		fe.Message = "the selected cluster could not be reached"
		if fe.Reason == string(k8s.ReasonClusterUnknown) {
			fe.Status, fe.Message = http.StatusNotFound, "the selected cluster is not registered"
		}
	case errors.Is(err, k8s.ErrDiscoveryUnavailable):
		fe.Reason, fe.Message = string(k8s.ReasonDiscoveryUnavailable), "Hubble discovery on the selected cluster failed"
	case errors.As(err, &status) && status.Status().Code == http.StatusUnauthorized:
		fe.Reason, fe.Message = string(k8s.ReasonCredentialsInvalid), "could not connect to the selected cluster with its stored credentials"
	default:
		fe.Reason, fe.Message = string(k8s.ReasonUnreachable), "the selected cluster could not be read"
	}
	return fe
}

// RemoteFlowSource reads one remote cluster's Hubble Relay through a
// port-forward tunnel. Close releases the tunnel and its slot.
type RemoteFlowSource struct {
	client         *HubbleClient
	tunnel         k8s.Tunnel
	relayNamespace string
	release        func()

	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

// GetFlows reads recent flows, as HubbleClient.GetFlows does locally.
func (s *RemoteFlowSource) GetFlows(ctx context.Context, namespace, verdict string, limit int) ([]FlowRecord, error) {
	return s.client.GetFlows(ctx, namespace, verdict, limit)
}

// StreamFlows follows flows until ctx ends or the relay stream breaks.
func (s *RemoteFlowSource) StreamFlows(ctx context.Context, namespace, verdict string, cb func(FlowRecord)) error {
	return s.client.StreamFlows(ctx, namespace, verdict, cb)
}

// Done is closed when the tunnel to the relay ends, by Close or because the
// relay or the API server went away.
func (s *RemoteFlowSource) Done() <-chan struct{} { return s.tunnel.Done() }

// CloseReason is why the source ended on its own: relay_restarted, a
// retryable end. It is empty once the caller has closed the source.
func (s *RemoteFlowSource) CloseReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ""
	}
	return ReasonRelayRestarted
}

// RelayNamespace is the namespace the relay runs in, where the user's
// pods/portforward grant authorizes the tunnel.
func (s *RemoteFlowSource) RelayNamespace() string { return s.relayNamespace }

// Close ends the gRPC client and the tunnel and frees the slot. It is safe
// to call more than once.
func (s *RemoteFlowSource) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.client.Close()
		_ = s.tunnel.Close()
		s.release()
	})
}

// OpenRemoteFlowSource opens a flow source on a remote cluster for the
// user's view of namespace. A refusal is a *FlowSourceError.
func (h *Handler) OpenRemoteFlowSource(ctx context.Context, clusterID string, user *auth.User, namespace string, kind FlowSourceKind) (*RemoteFlowSource, error) {
	if h.Clients == nil || h.Tunnels == nil || h.Access == nil {
		return nil, flowReadFailure(errRemoteNotConfigured)
	}
	allowed, err := h.Access.CanAccess(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, "list", "pods", namespace)
	if err != nil {
		return nil, flowReadFailure(err)
	}
	if !allowed {
		return nil, flowRefusal(string(k8s.ReasonForbidden), http.StatusForbidden,
			"you do not have permission to view flows in namespace "+namespace, false, nil)
	}

	release, ok := h.slots.acquire(ctx, kind, clusterID, user.KubernetesUsername)
	if !ok {
		return nil, flowRefusal(ReasonTunnelLimit, http.StatusTooManyRequests,
			"too many Hubble connections to the selected cluster; try again shortly", true, nil)
	}
	src, err := h.openRelay(ctx, clusterID, user)
	if err != nil {
		release()
		return nil, err
	}
	src.release = release
	return src, nil
}

// openRelay finds the relay as the user, detects its mode, and opens the
// tunnel and gRPC client.
func (h *Handler) openRelay(ctx context.Context, clusterID string, user *auth.User) (*RemoteFlowSource, error) {
	ctx, cancel := context.WithTimeout(ctx, relaySetupTimeout)
	defer cancel()

	// Detection reads its cluster from the context; pin it to the cluster
	// this source was opened for.
	info, err := h.remoteCNI(middleware.WithClusterID(ctx, clusterID), user, false)
	if err != nil {
		return nil, flowReadFailure(err)
	}
	if info.Name != CNICilium {
		return nil, errRelayNotFound(nil)
	}
	cs, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, flowReadFailure(k8s.TargetError{Err: err})
	}
	relay, ferr := findRelay(ctx, cs, info)
	if ferr != nil {
		return nil, ferr
	}
	mode := relayModeOf(ctx, cs, relay.namespace)
	if mode == relayModeTLS {
		return nil, errRelayTLS()
	}

	forwarder, err := h.Tunnels.PortForwarderFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, flowReadFailure(k8s.TargetError{Err: err})
	}
	tunnel, err := forwarder.PortForward(ctx, relay.namespace, relay.pod, relay.port)
	if err != nil {
		return nil, flowReadFailure(err)
	}
	client, err := newHubbleClientWithDialer(tunnelDialer(tunnel))
	if err != nil {
		_ = tunnel.Close()
		return nil, flowReadFailure(err)
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, relayProbeTimeout)
	defer cancelProbe()
	if err := client.serverStatus(probeCtx); err != nil {
		tlsRelay := mode == relayModeUnknown && relayServesTLS(ctx, tunnel)
		client.Close()
		_ = tunnel.Close()
		if tlsRelay {
			return nil, errRelayTLS()
		}
		return nil, errRelayNotReady(err)
	}
	return &RemoteFlowSource{client: client, tunnel: tunnel, relayNamespace: relay.namespace}, nil
}

// tunnelDialer is the gRPC context dialer over a tunnel. Once the tunnel
// has ended it fails at once, so gRPC does not spin on reconnects.
func tunnelDialer(tunnel k8s.Tunnel) func(ctx context.Context, _ string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		select {
		case <-tunnel.Done():
			return nil, k8s.ErrTunnelClosed
		default:
		}
		return tunnel.DialContext(ctx)
	}
}

// relayTarget is the relay pod and container port a tunnel is opened to.
// Both are derived on the server from the relay Service.
type relayTarget struct {
	namespace string
	pod       string
	port      int32
}

// findRelay locates the hubble-relay Service in the Cilium namespace and
// picks its longest-ready pod (KTD8). When detection named no namespace the
// search is kube-system then cilium, and only a NotFound moves it on.
func findRelay(ctx context.Context, cs kubernetes.Interface, info *CNIInfo) (*relayTarget, *FlowSourceError) {
	namespaces := ciliumSearchNamespaces()
	if info.Namespace != "" {
		namespaces = []string{info.Namespace}
	}
	var svc *corev1.Service
	for _, ns := range namespaces {
		s, err := cs.CoreV1().Services(ns).Get(ctx, relayServiceName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, flowReadFailure(err)
		}
		svc = s
		break
	}
	if svc == nil || len(svc.Spec.Selector) == 0 || len(svc.Spec.Ports) == 0 {
		return nil, errRelayNotFound(nil)
	}

	pods, err := cs.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return nil, flowReadFailure(err)
	}
	pod := longestReadyPod(pods.Items)
	if pod == nil {
		return nil, errRelayNotReady(nil)
	}
	port, ok := containerPortFor(pod, relayServicePort(svc))
	if !ok {
		return nil, errRelayNotFound(nil)
	}
	return &relayTarget{namespace: svc.Namespace, pod: pod.Name, port: port}, nil
}

// relayServicePort is the Service port named grpc, else its first port.
func relayServicePort(svc *corev1.Service) corev1.ServicePort {
	for _, p := range svc.Spec.Ports {
		if p.Name == relayPortName {
			return p
		}
	}
	return svc.Spec.Ports[0]
}

// containerPortFor resolves a Service port's targetPort to a port the pod
// declares. A port the pod does not declare is not resolved.
func containerPortFor(pod *corev1.Pod, sp corev1.ServicePort) (int32, bool) {
	want := sp.TargetPort
	if want.Type == intstr.Int && want.IntVal == 0 {
		want = intstr.FromInt32(sp.Port)
	}
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP {
				continue
			}
			if (want.Type == intstr.String && p.Name == want.StrVal) || (want.Type == intstr.Int && p.ContainerPort == want.IntVal) {
				return p.ContainerPort, true
			}
		}
	}
	return 0, false
}

// longestReadyPod is the Running, Ready, non-terminating pod that has been
// ready longest, or nil when there is none.
func longestReadyPod(pods []corev1.Pod) *corev1.Pod {
	type candidate struct {
		pod   *corev1.Pod
		since time.Time
	}
	var ready []candidate
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = append(ready, candidate{pod: p, since: c.LastTransitionTime.Time})
				break
			}
		}
	}
	if len(ready) == 0 {
		return nil
	}
	sort.Slice(ready, func(i, j int) bool {
		if !ready[i].since.Equal(ready[j].since) {
			return ready[i].since.Before(ready[j].since)
		}
		return ready[i].pod.Name < ready[j].pod.Name
	})
	return ready[0].pod
}

type relayMode int

const (
	relayModeUnknown relayMode = iota
	relayModePlaintext
	relayModeTLS
)

// relayModeOf reads the relay's mode from hubble-relay-config (KTD8). An
// unreadable or inconclusive config is unknown: the relay is then dialed in
// plaintext and a failure is checked for TLS. The Service port is never a
// signal, since the chart allows any servicePort.
func relayModeOf(ctx context.Context, cs kubernetes.Interface, namespace string) relayMode {
	cm, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, relayConfigMapName, metav1.GetOptions{})
	if err != nil {
		return relayModeUnknown
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(cm.Data[relayConfigKey]), &cfg); err != nil {
		return relayModeUnknown
	}
	switch v := cfg["disable-server-tls"].(type) {
	case bool:
		if v {
			return relayModePlaintext
		}
	case string:
		if strings.EqualFold(v, "true") {
			return relayModePlaintext
		}
	}
	if cert, _ := cfg["tls-relay-server-cert-file"].(string); cert != "" {
		return relayModeTLS
	}
	return relayModeUnknown
}

// relayServesTLS reports whether the relay answers a TLS client hello. The
// probe verifies against the system roots and never skips verification: a
// certificate that does not verify still proves the relay speaks TLS.
func relayServesTLS(ctx context.Context, tunnel k8s.Tunnel) bool {
	ctx, cancel := context.WithTimeout(ctx, relayProbeTimeout)
	defer cancel()
	conn, err := tunnel.DialContext(ctx)
	if err != nil {
		return false
	}
	defer conn.Close()
	err = tls.Client(conn, &tls.Config{ServerName: relayServiceName, MinVersion: tls.VersionTLS12}).HandshakeContext(ctx)
	if err == nil {
		return true
	}
	var verifyErr *tls.CertificateVerificationError
	var alert tls.AlertError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verifyErr) || errors.As(err, &alert) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid)
}

// flowSlots bounds concurrent remote flow tunnels (KTD9). The zero value is
// ready to use.
type flowSlots struct {
	mu       sync.Mutex
	freed    chan struct{} // closed and replaced whenever a slot frees
	live     map[string]int
	liveUser map[string]int
	rest     map[string]int
}

// acquire takes a slot of kind for the cluster and user, waiting up to
// flowSlotWait for one to free. The returned release frees it once.
func (s *flowSlots) acquire(ctx context.Context, kind FlowSourceKind, clusterID, username string) (release func(), ok bool) {
	timer := time.NewTimer(flowSlotWait)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.tryTake(kind, clusterID, username) {
			s.mu.Unlock()
			var once sync.Once
			return func() { once.Do(func() { s.give(kind, clusterID, username) }) }, true
		}
		if s.freed == nil {
			s.freed = make(chan struct{})
		}
		freed := s.freed
		s.mu.Unlock()
		select {
		case <-freed:
		case <-timer.C:
			return nil, false
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (s *flowSlots) tryTake(kind FlowSourceKind, clusterID, username string) bool {
	if s.live == nil {
		s.live, s.liveUser, s.rest = map[string]int{}, map[string]int{}, map[string]int{}
	}
	if kind == FlowSourceLive {
		if s.live[clusterID] >= maxLiveStreamsPerCluster || s.liveUser[username] >= maxLiveStreamsPerUser {
			return false
		}
		s.live[clusterID]++
		s.liveUser[username]++
		return true
	}
	if s.rest[clusterID] >= maxRESTTunnelsPerCluster {
		return false
	}
	s.rest[clusterID]++
	return true
}

func (s *flowSlots) give(kind FlowSourceKind, clusterID, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == FlowSourceLive {
		decrement(s.live, clusterID)
		decrement(s.liveUser, username)
	} else {
		decrement(s.rest, clusterID)
	}
	if s.freed != nil {
		close(s.freed)
		s.freed = nil
	}
}

func decrement(m map[string]int, key string) {
	if m[key] <= 1 {
		delete(m, key)
		return
	}
	m[key]--
}
