package k8s

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// streamPingPeriod keeps an idle SPDY connection alive through load
// balancers in front of a remote API server. It matches the period
// client-go's spdy.RoundTripperFor sets.
const streamPingPeriod = 5 * time.Second

// streamSetupTimeout bounds an exec or port-forward call whose caller set
// no deadline. The SPDY upgrade request honors the request context, so this
// covers a dial or upgrade that never completes.
const streamSetupTimeout = 15 * time.Second

// ExecCommand names a command the stream transport may run in a remote pod.
// The set is closed on purpose: a StreamTransport carries the remote cluster's
// stored credential, so it runs only commands this package defines, in the
// container this package names, never caller-supplied argv (#608 KTD2).
type ExecCommand int

const (
	// ExecCiliumStatusJSON runs `cilium-dbg status -o json` in a Cilium
	// agent pod's cilium-agent container.
	ExecCiliumStatusJSON ExecCommand = iota + 1
)

type execSpec struct {
	container string
	argv      []string
}

var execSpecs = map[ExecCommand]execSpec{
	ExecCiliumStatusJSON: {container: "cilium-agent", argv: []string{"cilium-dbg", "status", "-o", "json"}},
}

// StreamTransport opens exec and port-forward streams to one remote cluster
// as one user.
//
// It is the only place a remote SPDY connection is built (#608 KTD1/KTD2).
// client-go's default SPDY and WebSocket round trippers ignore
// rest.Config.Dial, so building a stream from a remote rest.Config anywhere
// else would skip StrictDialContext and reopen the DNS-rebinding window
// P2-6 closed. The impersonated config never leaves this type.
type StreamTransport struct {
	cfg *rest.Config
	// dial is the upgrade connection's dialer. Production always uses
	// StrictDialContext; tests in this package override it to reach an
	// httptest server, which the strict dialer refuses as loopback.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// logger records panics recovered in the port-forward goroutines.
	logger *slog.Logger
}

// StreamTransportFor returns a stream transport for a remote cluster that
// acts as the given user. The local cluster has no stream transport: local
// exec and port-forward keep their own service-account paths.
func (cr *ClusterRouter) StreamTransportFor(ctx context.Context, clusterID, username string, groups []string) (*StreamTransport, error) {
	if isLocalClusterID(clusterID) {
		return nil, errors.New("stream transport is for remote clusters only")
	}
	if err := cr.requireClusterStore(clusterID); err != nil {
		return nil, err
	}
	cfg, err := cr.remoteConfig(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	return &StreamTransport{cfg: cfg, dial: StrictDialContext, logger: cr.logger}, nil
}

// roundTripper builds the strict SPDY round tripper and its upgrader.
func (t *StreamTransport) roundTripper() (http.RoundTripper, spdy.Upgrader, error) {
	tlsConfig, err := rest.TLSConfigFor(t.cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("building stream TLS config: %w", err)
	}
	if tlsConfig == nil {
		// The streaming round tripper treats a missing TLS config on its
		// upgrade transport as InsecureSkipVerify. Verify against the
		// system roots instead; applyClusterTLS already guarantees a remote
		// config carries a CA or an explicit AllowInsecureTLS opt-in.
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	upgrade := &http.Transport{
		DialContext:     t.dial,
		TLSClientConfig: tlsConfig,
		// Never proxy: the strict dialer must see the API server's own
		// address (see NewRemoteRESTConfig).
		Proxy: nil,
	}
	rt, err := streamspdy.NewRoundTripperWithConfig(streamspdy.RoundTripperConfig{
		UpgradeTransport: upgrade,
		PingPeriod:       streamPingPeriod,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("building stream round tripper: %w", err)
	}
	wrapped, err := rest.HTTPWrappersForConfig(t.cfg, rt)
	if err != nil {
		return nil, nil, fmt.Errorf("wrapping stream round tripper: %w", err)
	}
	return wrapped, spdy.NewUpgraderForStreaming(rt), nil
}

// subresourceURL returns the API server URL of a pod subresource, with the
// given query. Names are validated so they cannot alter the path.
func (t *StreamTransport) subresourceURL(namespace, pod, subresource string, query url.Values) (*url.URL, error) {
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return nil, fmt.Errorf("invalid namespace %q", namespace)
	}
	if errs := validation.IsDNS1123Subdomain(pod); len(errs) > 0 {
		return nil, fmt.Errorf("invalid pod name %q", pod)
	}
	base, _, err := rest.DefaultServerUrlFor(t.cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing API server URL: %w", err)
	}
	u := base.JoinPath("api", "v1", "namespaces", namespace, "pods", pod, subresource)
	u.RawQuery = query.Encode()
	return u, nil
}

// Exec runs a fixed command in a remote pod and streams its output. A
// redirect answering the upgrade request is refused rather than followed.
// When ctx has no deadline, the call is bounded by streamSetupTimeout.
func (t *StreamTransport) Exec(ctx context.Context, namespace, pod string, cmd ExecCommand, stdout, stderr io.Writer) error {
	spec, ok := execSpecs[cmd]
	if !ok {
		return fmt.Errorf("exec command %d is not allowed", cmd)
	}
	query := url.Values{
		"container": {spec.container},
		"stdout":    {"true"},
		"stderr":    {"true"},
		"command":   spec.argv,
	}
	u, err := t.subresourceURL(namespace, pod, "exec", query)
	if err != nil {
		return err
	}
	rt, upgrader, err := t.roundTripper()
	if err != nil {
		return err
	}
	executor, err := remotecommand.NewSPDYExecutorRejectRedirects(rt, upgrader, http.MethodPost, u)
	if err != nil {
		return fmt.Errorf("creating exec stream: %w", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, streamSetupTimeout)
		defer cancel()
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: stdout, Stderr: stderr})
}

// Tunnel is an open port-forward connection to one port of one remote pod.
// Each DialContext opens a new forwarded connection over it, the way each
// local connection does under kubectl port-forward.
type Tunnel interface {
	// DialContext opens a connection to the pod port. It fails at once
	// after the tunnel has ended.
	DialContext(ctx context.Context) (net.Conn, error)
	// Done is closed when the tunnel's connection to the API server ends.
	Done() <-chan struct{}
	// Close ends the tunnel and every connection opened over it.
	Close() error
}

// ErrTunnelClosed is a dial on a tunnel whose connection has ended.
var ErrTunnelClosed = errors.New("port-forward tunnel is closed")

// ErrPortForwardFailed is a forwarded connection the pod side refused or
// broke. The API server's own message, which names the pod's uid and
// network namespace, is never carried in it.
var ErrPortForwardFailed = errors.New("the forwarded connection to the pod failed")

// errorStreamWait bounds how long a forwarded connection that ended waits
// for its error stream, to tell a refusal from a plain close.
const errorStreamWait = time.Second

// PortForward opens a port-forward tunnel to a port of a remote pod. The
// caller derives the pod and port on the server; nothing here takes them
// from a client. A redirect answering the upgrade is refused rather than
// followed, and a refused upgrade keeps its HTTP status as an API error, so
// a 403 still classifies as forbidden. When ctx has no deadline, the setup
// is bounded by streamSetupTimeout.
func (t *StreamTransport) PortForward(ctx context.Context, namespace, pod string, port int32) (Tunnel, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	u, err := t.subresourceURL(namespace, pod, "portforward", nil)
	if err != nil {
		return nil, err
	}
	rt, upgrader, err := t.roundTripper()
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, streamSetupTimeout)
		defer cancel()
	}
	client := &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating port-forward request: %w", err)
	}
	req.Header.Add(httpstream.HeaderProtocolVersion, portforward.PortForwardProtocolV1Name)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("port-forward upgrade: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, upgradeRefusal(resp, pod)
	}
	conn, err := upgrader.NewConnection(resp)
	if err != nil {
		return nil, fmt.Errorf("port-forward upgrade: %w", err)
	}
	if got := resp.Header.Get(httpstream.HeaderProtocolVersion); got != portforward.PortForwardProtocolV1Name {
		_ = conn.Close()
		return nil, fmt.Errorf("port-forward upgrade negotiated protocol %q", got)
	}
	return newPortForwardTunnel(conn, port, t.logger), nil
}

// upgradeRefusal turns a refused upgrade into an API error carrying its
// status, so callers classify it like any other API answer.
func upgradeRefusal(resp *http.Response, pod string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var status metav1.Status
	if err := json.Unmarshal(body, &status); err == nil && status.Kind == "Status" && status.Code == int32(resp.StatusCode) {
		return &apierrors.StatusError{ErrStatus: status}
	}
	return apierrors.NewGenericServerResponse(resp.StatusCode, http.MethodPost,
		schema.GroupResource{Resource: "pods/portforward"}, pod, "", 0, false)
}

// portForwardTunnel implements Tunnel over one SPDY connection.
type portForwardTunnel struct {
	conn      httpstream.Connection
	port      string
	logger    *slog.Logger
	requestID atomic.Int64
	done      chan struct{}
	doneOnce  sync.Once
}

func newPortForwardTunnel(conn httpstream.Connection, port int32, logger *slog.Logger) *portForwardTunnel {
	t := &portForwardTunnel{conn: conn, port: strconv.Itoa(int(port)), logger: logger, done: make(chan struct{})}
	go func() {
		recoverutil.Safe(logger, "port-forward tunnel watch", func() { <-conn.CloseChan() })
		t.markDone()
	}()
	return t
}

func (t *portForwardTunnel) markDone() { t.doneOnce.Do(func() { close(t.done) }) }

func (t *portForwardTunnel) Done() <-chan struct{} { return t.done }

func (t *portForwardTunnel) Close() error {
	t.markDone()
	return t.conn.Close()
}

// DialContext opens one forwarded connection: the error stream first, then
// the data stream, under one request id, as client-go's port-forward does.
func (t *portForwardTunnel) DialContext(ctx context.Context) (net.Conn, error) {
	select {
	case <-t.done:
		return nil, ErrTunnelClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set(corev1.StreamType, corev1.StreamTypeError)
	headers.Set(corev1.PortHeader, t.port)
	headers.Set(corev1.PortForwardRequestIDHeader, strconv.FormatInt(t.requestID.Add(1), 10))
	errorStream, err := t.conn.CreateStream(headers)
	if err != nil {
		return nil, fmt.Errorf("creating port-forward error stream: %w", err)
	}
	// Nothing is written to the error stream.
	_ = errorStream.Close()
	errResult := make(chan []byte, 1)
	go func() {
		var msg []byte
		recoverutil.Safe(t.logger, "port-forward error stream", func() {
			msg, _ = io.ReadAll(errorStream)
		})
		errResult <- msg
	}()

	headers.Set(corev1.StreamType, corev1.StreamTypeData)
	dataStream, err := t.conn.CreateStream(headers)
	if err != nil {
		_ = errorStream.Reset()
		t.conn.RemoveStreams(errorStream)
		return nil, fmt.Errorf("creating port-forward data stream: %w", err)
	}
	return &tunnelConn{conn: t.conn, data: dataStream, errStream: errorStream, errResult: errResult}, nil
}

// tunnelConn is one forwarded connection, as a net.Conn.
type tunnelConn struct {
	conn      httpstream.Connection
	data      httpstream.Stream
	errStream httpstream.Stream
	errResult chan []byte
	closeOnce sync.Once
}

// Read reads from the pod. When the pod side ends the connection with an
// error stream message, Read reports ErrPortForwardFailed in place of the
// end of stream; the message itself is dropped.
func (c *tunnelConn) Read(b []byte) (int, error) {
	n, err := c.data.Read(b)
	if err == nil {
		return n, nil
	}
	select {
	case msg := <-c.errResult:
		// Keep it for a later Read.
		c.errResult <- msg
		if len(msg) > 0 {
			return n, ErrPortForwardFailed
		}
	case <-time.After(errorStreamWait):
	}
	return n, err
}

func (c *tunnelConn) Write(b []byte) (int, error) { return c.data.Write(b) }

// Close resets and removes both streams. It is safe to call more than once.
func (c *tunnelConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.data.Reset()
		_ = c.errStream.Reset()
		c.conn.RemoveStreams(c.data, c.errStream)
	})
	return nil
}

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "portforward" }
func (tunnelAddr) String() string  { return "portforward" }

func (c *tunnelConn) LocalAddr() net.Addr  { return tunnelAddr{} }
func (c *tunnelConn) RemoteAddr() net.Addr { return tunnelAddr{} }

// Deadlines are not supported on a SPDY stream; the tunnel's lifetime and
// the caller's context bound a forwarded connection instead.
func (c *tunnelConn) SetDeadline(time.Time) error      { return nil }
func (c *tunnelConn) SetReadDeadline(time.Time) error  { return nil }
func (c *tunnelConn) SetWriteDeadline(time.Time) error { return nil }
