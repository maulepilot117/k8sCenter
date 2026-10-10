package monitoring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// ErrNoMetricsBinding means the selected remote cluster has no metrics
// binding. A MetricsBindingReader signals "no row" with it (wrapped with %w
// is fine; callers test with errors.Is). It is the resources sentinel, so
// the dashboard providers' callers can recognise it without importing this
// package.
var ErrNoMetricsBinding = resources.ErrNoMetricsBinding

// ErrPrometheusUnavailable means the local cluster's Prometheus has not been
// discovered (or no Discoverer is wired).
var ErrPrometheusUnavailable = errors.New("prometheus not available")

// defaultResolverTTL bounds how long a remote cluster's client is reused
// before its binding is read again. Edits through the admin API evict
// immediately; the TTL covers any write that bypassed Evict.
const defaultResolverTTL = 30 * time.Second

// probeTimeout bounds ProbePrometheus end to end.
const probeTimeout = 5 * time.Second

// MetricsBinding is a remote cluster's Prometheus registration. Token is
// the decrypted bearer token ("" when none is stored); it must never be
// logged or returned to a client.
type MetricsBinding struct {
	ClusterID       string
	PrometheusURL   string
	AlertmanagerURL string
	Token           string
}

// MetricsBindingReader reads a remote cluster's metrics binding. A cluster
// with no binding answers an error for which errors.Is(err,
// ErrNoMetricsBinding) holds; any other error is a failure to read.
type MetricsBindingReader interface {
	GetMetricsBinding(ctx context.Context, clusterID string) (MetricsBinding, error)
}

type cachedClient struct {
	client  *PrometheusClient
	expires time.Time
}

// ClientResolver answers which Prometheus serves a cluster: the local
// cluster's discovered Prometheus, or a remote cluster's metrics binding.
// A remote cluster is never answered from the local Prometheus. A nil
// Bindings reader behaves as "no binding" for every remote cluster.
type ClientResolver struct {
	Discoverer *Discoverer
	Bindings   MetricsBindingReader
	Logger     *slog.Logger
	TTL        time.Duration

	// base is the transport remote clients dial through. NewClientResolver
	// sets the strict transport (public addresses only, like the remote API
	// server); tests substitute one that can reach httptest servers.
	base http.RoundTripper
	// now is the clock for cache expiry; nil means time.Now.
	now func() time.Time

	mu    sync.Mutex
	cache map[string]cachedClient
	// epoch advances on every Evict. A binding read that began before an
	// Evict does not cache its result, so an in-flight read cannot reinstate
	// a binding that was just replaced or deleted.
	epoch uint64
}

// NewClientResolver returns a resolver over the local Discoverer and the
// remote bindings reader (nil: no remote cluster has a binding).
func NewClientResolver(disc *Discoverer, bindings MetricsBindingReader, logger *slog.Logger) *ClientResolver {
	return &ClientResolver{
		Discoverer: disc,
		Bindings:   bindings,
		Logger:     logger,
		TTL:        defaultResolverTTL,
		base:       k8s.StrictHTTPTransport(),
		cache:      map[string]cachedClient{},
	}
}

// PrometheusFor returns the Prometheus client for clusterID. The local
// cluster answers ErrPrometheusUnavailable when nothing was discovered; a
// remote cluster answers ErrNoMetricsBinding when it has no binding, and
// the reader's own error when the binding could not be read.
func (r *ClientResolver) PrometheusFor(ctx context.Context, clusterID string) (*PrometheusClient, error) {
	if k8s.IsLocalClusterID(clusterID) {
		if r.Discoverer == nil {
			return nil, ErrPrometheusUnavailable
		}
		pc := r.Discoverer.PrometheusClient()
		if pc == nil {
			return nil, ErrPrometheusUnavailable
		}
		return pc, nil
	}

	if r.Bindings == nil {
		return nil, ErrNoMetricsBinding
	}

	now := r.clock()
	r.mu.Lock()
	if c, ok := r.cache[clusterID]; ok && now.Before(c.expires) {
		r.mu.Unlock()
		return c.client, nil
	}
	epoch := r.epoch
	base := r.baseLocked()
	r.mu.Unlock()

	b, err := r.Bindings.GetMetricsBinding(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if b.PrometheusURL == "" {
		return nil, ErrNoMetricsBinding
	}
	pc, err := NewPrometheusClientWithTransport(b.PrometheusURL, newBearerRoundTripper(base, b.PrometheusURL, b.Token))
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.epoch == epoch {
		if r.cache == nil {
			r.cache = map[string]cachedClient{}
		}
		r.cache[clusterID] = cachedClient{client: pc, expires: now.Add(r.ttl())}
	}
	r.mu.Unlock()
	return pc, nil
}

// Evict drops the cached client for clusterID, so the next request reads
// the binding again. Call it after the binding is written or deleted, and
// when the cluster is deregistered.
func (r *ClientResolver) Evict(clusterID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, clusterID)
	r.epoch++
}

func (r *ClientResolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *ClientResolver) ttl() time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return defaultResolverTTL
}

// baseLocked returns the remote base transport, creating the strict one for
// a resolver built without NewClientResolver. Caller holds r.mu.
func (r *ClientResolver) baseLocked() http.RoundTripper {
	if r.base == nil {
		r.base = k8s.StrictHTTPTransport()
	}
	return r.base
}

// bearerRoundTripper adds the binding's bearer token to requests for the
// bound Prometheus host only, so a redirect elsewhere never carries it. The
// caller's request is cloned, never modified.
type bearerRoundTripper struct {
	base  http.RoundTripper
	host  string
	token string
}

func newBearerRoundTripper(base http.RoundTripper, prometheusURL, token string) *bearerRoundTripper {
	host := ""
	if u, err := url.Parse(prometheusURL); err == nil {
		host = u.Host
	}
	return &bearerRoundTripper{base: base, host: host, token: token}
}

func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token == "" || t.host == "" || req.URL.Host != t.host {
		return t.base.RoundTrip(req)
	}
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(out)
}

// ProbePrometheus checks that prometheusURL answers the Prometheus HTTP API:
// GET <url>/api/v1/status/buildinfo through the strict transport with the
// bearer token, within 5s, without following redirects. A transport error
// or a non-2xx status is an error. The error may name the address; callers
// answer clients with a fixed message and only log it.
func ProbePrometheus(ctx context.Context, prometheusURL, token string) error {
	t := k8s.StrictHTTPTransport()
	defer t.CloseIdleConnections()
	return probePrometheus(ctx, t, prometheusURL, token)
}

func probePrometheus(ctx context.Context, base http.RoundTripper, prometheusURL, token string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	target := strings.TrimRight(prometheusURL, "/") + "/api/v1/status/buildinfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("building prometheus probe: %w", err)
	}
	client := &http.Client{
		Transport: newBearerRoundTripper(base, prometheusURL, token),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus probe: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("prometheus probe: status %d", resp.StatusCode)
	}
	return nil
}
