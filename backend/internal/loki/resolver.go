package loki

// Per-cluster Loki resolution (remote logs and shells U11, KTD6, KTD7).
// Every Loki read names a cluster, and the resolver answers which Loki
// serves it:
//   - the local cluster: the Loki the Discoverer found;
//   - a remote cluster with a Loki binding: the configured URL, over the
//     strict transport, with redirects refused and the bearer token pinned
//     to the URL's origin;
//   - a remote cluster without one: the Loki Service found as the
//     requesting user, reached by a port-forward tunnel to its longest-ready
//     pod, opened as the user.
//
// A remote cluster is never answered from the local Loki, whatever fails.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/store"
)

// Reasons a cluster's Loki is unavailable. The other reasons the resolver
// reports are the shared remote ones: forbidden, credentials_invalid,
// cluster_unknown, unreachable, db_unavailable and unsupported_platform.
const (
	ReasonLokiNotFound    = "loki_not_found"
	ReasonLokiUnreachable = "loki_unreachable"
)

// resolveSetupTimeout bounds finding a remote cluster's Loki and opening
// its tunnel. The tunnel itself outlives it.
const resolveSetupTimeout = 15 * time.Second

// Source says how a cluster's Loki is reached.
type Source string

const (
	// SourceLocal is the local cluster's discovered Loki.
	SourceLocal Source = "local"
	// SourceBinding is a remote cluster's configured Loki URL.
	SourceBinding Source = "binding"
	// SourcePortForward is a remote cluster's in-cluster Loki, reached by
	// a port-forward tunnel.
	SourcePortForward Source = "port-forward"
)

// UnavailableError says why a cluster's Loki cannot be used. Reason is a
// stable code, Status the HTTP status to answer with, and Message a fixed
// client message. Err, which can carry a remote API server's or Loki's
// address or text, is for logs only.
type UnavailableError struct {
	Reason    string
	Status    int
	Message   string
	Retryable bool
	Err       error
}

func (e *UnavailableError) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason + ": " + e.Message
}

func (e *UnavailableError) Unwrap() error { return e.Err }

func unavailable(reason string, status int, message string, retryable bool, err error) *UnavailableError {
	return &UnavailableError{Reason: reason, Status: status, Message: message, Retryable: retryable, Err: err}
}

// Location is where a cluster's Loki is, found without connecting to it.
type Location struct {
	Source Source
	// URL is the local discovered URL or the binding's URL. It is empty
	// for the port-forward path, which has no address of its own.
	URL string
	// Namespace and Service name the Service the port-forward path chose.
	Namespace string
	Service   string
}

// Resolved is one request's Loki. Close releases what Resolve opened for it
// (the port-forward path's tunnel); call it when the request ends.
type Resolved struct {
	Location
	Client *Client

	tunnel    k8s.Tunnel
	closeOnce sync.Once
}

// Done is closed when the tunnel under the client ends, by Close or because
// the pod or the API server went away. It is nil, and so never ready, for a
// client with no tunnel.
func (r *Resolved) Done() <-chan struct{} {
	if r.tunnel == nil {
		return nil
	}
	return r.tunnel.Done()
}

// Close ends the tunnel and every connection over it. It does nothing for a
// client with no tunnel, and is safe to call more than once.
func (r *Resolved) Close() {
	if r == nil || r.tunnel == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.Client.closeIdle()
		_ = r.tunnel.Close()
	})
}

// LocalLoki is the local cluster's discovered Loki. *Discoverer implements
// it.
type LocalLoki interface {
	Client() *Client
}

// BindingStore reads remote clusters' Loki bindings.
// *store.ClusterLogsStore implements it.
type BindingStore interface {
	Get(ctx context.Context, clusterID string) (store.LokiBinding, string, error)
}

// RemoteClients returns a remote cluster's typed client as a user.
// *k8s.ClusterRouter implements it.
type RemoteClients interface {
	ClientForCluster(ctx context.Context, clusterID, username string, groups []string) (kubernetes.Interface, error)
}

// PortForwarder opens a port-forward tunnel to a remote cluster's pod as a
// user. A failure to reach the cluster at all is a k8s.TargetError.
type PortForwarder interface {
	PortForward(ctx context.Context, clusterID, username string, groups []string, namespace, pod string, port int32) (k8s.Tunnel, error)
}

type routerPortForwarder struct{ router *k8s.ClusterRouter }

func (f routerPortForwarder) PortForward(ctx context.Context, clusterID, username string, groups []string, namespace, pod string, port int32) (k8s.Tunnel, error) {
	st, err := f.router.StreamTransportFor(ctx, clusterID, username, groups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	return st.PortForward(ctx, namespace, pod, port)
}

// Resolver answers which Loki serves a cluster. Bindings are read on every
// request, so a binding written by any pod is used at once. The Service the
// port-forward path finds is cached per (cluster, identity) for 30s.
type Resolver struct {
	local     LocalLoki
	bindings  BindingStore
	clients   RemoteClients
	forwarder PortForwarder
	discovery *remotecache.Cache[*lokiService]

	// strict and strictDial are what a binding's client connects through:
	// public addresses only, like the remote API server. tailTLS is the
	// tail's TLS configuration (nil: system roots). Tests substitute all
	// three to reach httptest servers.
	strict     http.RoundTripper
	strictDial DialFunc
	tailTLS    *tls.Config
}

// NewResolver returns a resolver over the local Discoverer, the binding
// store and the cluster router. Any of them may be nil: no local Loki, no
// remote binding, or no remote cluster reachable.
func NewResolver(disc *Discoverer, bindings *store.ClusterLogsStore, router *k8s.ClusterRouter, logger *slog.Logger) *Resolver {
	r := &Resolver{
		discovery:  remotecache.New[*lokiService](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, logger),
		strict:     k8s.StrictHTTPTransport(),
		strictDial: k8s.StrictDialContext,
	}
	// Each dependency is set only when present, so a missing one is a true
	// nil interface, never a typed nil (docs/solutions/go-typed-nil-interface.md).
	if disc != nil {
		r.local = disc
	}
	if bindings != nil {
		r.bindings = bindings
	}
	if router != nil {
		r.clients = router
		r.forwarder = routerPortForwarder{router: router}
	}
	return r
}

// Evict drops what the resolver holds for clusterID, so the next request
// looks for its Loki again. Call it when the cluster is deregistered and
// after its binding is written or deleted.
func (r *Resolver) Evict(clusterID string) {
	r.discovery.EvictCluster(clusterID)
}

// Locate reports where clusterID's Loki is, for the given user, without
// connecting to it. A failure is an *UnavailableError.
func (r *Resolver) Locate(ctx context.Context, clusterID, username string, groups []string) (Location, error) {
	if k8s.IsLocalClusterID(clusterID) {
		c, err := r.localClient()
		if err != nil {
			return Location{}, err
		}
		return Location{Source: SourceLocal, URL: c.BaseURL()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveSetupTimeout)
	defer cancel()

	b, _, found, err := r.binding(ctx, clusterID)
	if err != nil {
		return Location{}, err
	}
	if found {
		return Location{Source: SourceBinding, URL: b.URL}, nil
	}
	_, svc, err := r.findService(ctx, clusterID, username, groups)
	if err != nil {
		return Location{}, err
	}
	return portForwardLocation(svc), nil
}

// Resolve returns a client for clusterID's Loki, for the given user. The
// caller must Close the result. A failure is an *UnavailableError.
func (r *Resolver) Resolve(ctx context.Context, clusterID, username string, groups []string) (*Resolved, error) {
	if k8s.IsLocalClusterID(clusterID) {
		c, err := r.localClient()
		if err != nil {
			return nil, err
		}
		return &Resolved{Location: Location{Source: SourceLocal, URL: c.BaseURL()}, Client: c}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveSetupTimeout)
	defer cancel()

	b, token, found, err := r.binding(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if found {
		c := newRemoteClient(b.URL, b.TenantID, token, r.strict, r.strictDial)
		c.tailTLS = r.tailTLS
		return &Resolved{Location: Location{Source: SourceBinding, URL: b.URL}, Client: c}, nil
	}
	return r.portForward(ctx, clusterID, username, groups)
}

func (r *Resolver) localClient() (*Client, error) {
	if r.local != nil {
		if c := r.local.Client(); c != nil {
			return c, nil
		}
	}
	return nil, unavailable(ReasonLokiNotFound, http.StatusServiceUnavailable,
		"Loki has not been detected. Deploy Loki or configure KUBECENTER_LOKI_URL.", false, nil)
}

// binding reads clusterID's Loki binding. found is false when the cluster
// has none, or when no binding store is wired. The token is returned only
// when the store reports it usable for the binding's URL; a stored token
// entered for another URL refuses the binding rather than querying it
// without one.
func (r *Resolver) binding(ctx context.Context, clusterID string) (b store.LokiBinding, token string, found bool, err error) {
	if r.bindings == nil {
		return store.LokiBinding{}, "", false, nil
	}
	b, token, err = r.bindings.Get(ctx, clusterID)
	switch {
	case errors.Is(err, store.ErrLokiBindingNotFound):
		return store.LokiBinding{}, "", false, nil
	case errors.Is(err, pgx.ErrNoRows):
		return store.LokiBinding{}, "", false, unavailable(string(k8s.ReasonClusterUnknown), http.StatusNotFound,
			"the selected cluster is not registered", false, err)
	case err != nil:
		return store.LokiBinding{}, "", false, unavailable(string(k8s.ReasonDBUnavailable), http.StatusServiceUnavailable,
			"the selected cluster's Loki settings could not be read", true, err)
	}
	if b.TokenStale {
		return store.LokiBinding{}, "", false, unavailable(string(k8s.ReasonCredentialsInvalid), http.StatusBadGateway,
			"the Loki token for the selected cluster was entered for a different URL; enter it again", false, nil)
	}
	if !b.HasToken {
		token = ""
	}
	return b, token, true, nil
}

// findService finds the Loki Service on a remote cluster as the user, in
// the shared search order, through the per-identity cache. It also returns
// the user's client, which the caller selects the Service's pod with.
func (r *Resolver) findService(ctx context.Context, clusterID, username string, groups []string) (kubernetes.Interface, *lokiService, error) {
	if r.clients == nil || r.forwarder == nil {
		return nil, nil, unavailable(string(k8s.ReasonUnsupportedPlatform), http.StatusNotImplemented,
			"log search is not available for remote clusters", false, nil)
	}
	cs, err := r.clients.ClientForCluster(ctx, clusterID, username, groups)
	if err != nil {
		return nil, nil, targetFailure(err)
	}
	svc, err := r.discovery.Get(ctx, clusterID, username, groups, func(ctx context.Context) (*lokiService, error) {
		// Fail closed: a refused read must not let a lower-ranked Service win.
		return searchLokiService(ctx, cs, true)
	})
	if err != nil {
		return nil, nil, apiFailure(err)
	}
	if svc == nil || !svc.hasPort {
		return nil, nil, errLokiNotFound(nil)
	}
	return cs, svc, nil
}

// portForward opens a tunnel to the found Service's longest-ready pod and
// builds a client that dials over it.
func (r *Resolver) portForward(ctx context.Context, clusterID, username string, groups []string) (*Resolved, error) {
	cs, svc, err := r.findService(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	backend, err := k8s.SelectServiceBackend(ctx, cs, svc.svc, svc.port)
	switch {
	case errors.Is(err, k8s.ErrServiceNoSelector), errors.Is(err, k8s.ErrBackendPortUndeclared):
		return nil, errLokiNotFound(err)
	case errors.Is(err, k8s.ErrNoReadyBackend):
		return nil, unavailable(ReasonLokiUnreachable, http.StatusServiceUnavailable,
			"Loki on the selected cluster has no ready pod", true, err)
	case err != nil:
		return nil, apiFailure(err)
	}

	tunnel, err := r.forwarder.PortForward(ctx, clusterID, username, groups, backend.Namespace, backend.Pod, backend.Port)
	if err != nil {
		return nil, portForwardFailure(err, backend.Namespace)
	}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return tunnel.DialContext(ctx)
	}
	// Every connection goes over the tunnel whatever its address, and no
	// proxy is consulted. The base URL names the Service, which is the Host
	// a gateway in front of Loki expects.
	transport := &http.Transport{
		DialContext:           dial,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	base := fmt.Sprintf("http://%s.%s.svc:%d", svc.svc.Name, svc.svc.Namespace, svc.port.Port)
	return &Resolved{
		Location: portForwardLocation(svc),
		Client:   newRemoteClient(base, "", "", transport, dial),
		tunnel:   tunnel,
	}, nil
}

func portForwardLocation(svc *lokiService) Location {
	return Location{Source: SourcePortForward, Namespace: svc.svc.Namespace, Service: svc.svc.Name}
}

func errLokiNotFound(err error) *UnavailableError {
	return unavailable(ReasonLokiNotFound, http.StatusNotFound,
		"Loki was not found on the selected cluster", false, err)
}

// targetFailure classifies a failure to reach a remote cluster at all.
func targetFailure(err error) *UnavailableError {
	switch k8s.ClassifyTargetErr(err) {
	case k8s.ReasonClusterUnknown:
		return unavailable(string(k8s.ReasonClusterUnknown), http.StatusNotFound,
			"the selected cluster is not registered", false, err)
	case k8s.ReasonDBUnavailable:
		return unavailable(string(k8s.ReasonDBUnavailable), http.StatusServiceUnavailable,
			"the cluster registry could not be read", true, err)
	case k8s.ReasonCredentialsInvalid:
		return errClusterCredentials(err)
	default:
		return unavailable(string(k8s.ReasonUnreachable), http.StatusBadGateway,
			"the selected cluster could not be reached", true, err)
	}
}

// apiFailure classifies a failed read from a remote cluster's API server.
func apiFailure(err error) *UnavailableError {
	var target k8s.TargetError
	switch {
	case errors.As(err, &target):
		return targetFailure(target.Err)
	case apierrors.IsForbidden(err):
		return unavailable(string(k8s.ReasonForbidden), http.StatusForbidden,
			"you do not have permission to look for Loki on the selected cluster", false, err)
	case apierrors.IsUnauthorized(err):
		return errClusterCredentials(err)
	default:
		return unavailable(string(k8s.ReasonUnreachable), http.StatusBadGateway,
			"the selected cluster could not be read", true, err)
	}
}

// portForwardFailure classifies a tunnel that could not be opened to Loki's
// pod in namespace.
func portForwardFailure(err error, namespace string) *UnavailableError {
	var target k8s.TargetError
	switch {
	case errors.As(err, &target):
		return targetFailure(target.Err)
	case apierrors.IsForbidden(err):
		return unavailable(string(k8s.ReasonForbidden), http.StatusForbidden,
			"you do not have permission to reach Loki in namespace "+namespace+" on the selected cluster", false, err)
	case apierrors.IsUnauthorized(err):
		return errClusterCredentials(err)
	default:
		return errLokiUnreachable(err)
	}
}

func errClusterCredentials(err error) *UnavailableError {
	return unavailable(string(k8s.ReasonCredentialsInvalid), http.StatusBadGateway,
		"could not connect to the selected cluster with its stored credentials", false, err)
}

func errLokiUnreachable(err error) *UnavailableError {
	return unavailable(ReasonLokiUnreachable, http.StatusBadGateway,
		"Loki on the selected cluster could not be reached", true, err)
}

// QueryFailure reports whether a failed Loki request means Loki is
// unavailable, and why. It returns nil for Loki's own answer to the query
// (a 400 for invalid LogQL, say) and for the caller's cancellation, which
// the caller reports as a failed query.
func QueryFailure(err error) *UnavailableError {
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	var status *StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden:
			return unavailable(string(k8s.ReasonCredentialsInvalid), http.StatusBadGateway,
				"Loki on the selected cluster refused k8sCenter's credentials", false, err)
		case status.Code >= 300 && status.Code < 400:
			return unavailable(ReasonLokiUnreachable, http.StatusBadGateway,
				"Loki on the selected cluster answered with a redirect, which k8sCenter does not follow", false, err)
		case status.Code >= 500:
			return errLokiUnreachable(err)
		}
		return nil
	}
	if errors.Is(err, errLokiTransport) || errors.Is(err, k8s.ErrTunnelClosed) || errors.Is(err, context.DeadlineExceeded) {
		return errLokiUnreachable(err)
	}
	return nil
}
