package k8s

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
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

// StreamTransport opens exec streams to one remote cluster as one user.
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
	return &StreamTransport{cfg: cfg, dial: StrictDialContext}, nil
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
