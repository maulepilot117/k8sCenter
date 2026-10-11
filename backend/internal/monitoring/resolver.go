package monitoring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/urlorigin"
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
// before its binding is read again. Edits through this pod's admin API evict
// immediately, and a reader that reports revisions makes a write by another
// pod visible on the next read; the TTL covers any other write.
const defaultResolverTTL = 30 * time.Second

// probeTimeout bounds ProbePrometheus end to end.
const probeTimeout = 5 * time.Second

// MetricsBinding is a remote cluster's metrics registration. Token is the
// decrypted Prometheus bearer token and AlertmanagerToken the decrypted
// Alertmanager one ("" when none is usable); neither may be logged or
// returned to a client. The reader has already checked AlertmanagerToken
// against the Alertmanager URL's origin: AlertmanagerTokenStale reports a
// stored token that no longer matches and must be entered again, in which
// case AlertmanagerToken is "".
type MetricsBinding struct {
	ClusterID              string
	PrometheusURL          string
	AlertmanagerURL        string
	Token                  string
	AlertmanagerToken      string
	AlertmanagerTokenStale bool
	// Revision changes on every write of the binding (the store's
	// updated_at). The zero value means the reader does not track revisions.
	Revision time.Time
}

// MetricsBindingReader reads a remote cluster's metrics binding. A cluster
// with no binding answers an error for which errors.Is(err,
// ErrNoMetricsBinding) holds; any other error is a failure to read.
type MetricsBindingReader interface {
	GetMetricsBinding(ctx context.Context, clusterID string) (MetricsBinding, error)
}

// MetricsBindingRevisionReader is implemented by a MetricsBindingReader that
// can report a binding's current revision without decrypting its tokens. The
// resolver checks it before reusing cached clients, so a binding written by
// another pod (which cannot call this pod's Evict) is picked up on the next
// read instead of after the TTL. Errors follow GetMetricsBinding. A reader
// without it relies on Evict and the TTL alone.
type MetricsBindingRevisionReader interface {
	MetricsBindingRevision(ctx context.Context, clusterID string) (time.Time, error)
}

// cachedClient is one remote cluster's clients, built from one binding read.
// The Alertmanager half has its own state: amErr (ErrNoAlertmanager,
// ErrAlertmanagerTokenReentry) is set when there is no Alertmanager client,
// and never affects the Prometheus client.
type cachedClient struct {
	client   *PrometheusClient
	am       *AlertmanagerClient
	amErr    error
	revision time.Time
	expires  time.Time
}

// ClientResolver answers which Prometheus and Alertmanager serve a cluster:
// the local cluster's discovered Prometheus, or a remote cluster's metrics
// binding. A remote cluster is never answered from the local Prometheus. A
// nil Bindings reader behaves as "no binding" for every remote cluster.
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

	entry, err := r.remote(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	return entry.client, nil
}

// AlertmanagerFor returns the Alertmanager client for a remote cluster. It
// answers ErrNoMetricsBinding when the cluster has no binding (or the
// resolver has no binding reader), the reader's own error when the binding
// could not be read, ErrNoAlertmanager when the binding has no Alertmanager
// URL, and ErrAlertmanagerTokenReentry when its stored token no longer
// matches the URL's origin; no request is sent in any of those cases. The
// local cluster answers ErrNoAlertmanager: its alerts come from the webhook
// alert store, not from a bound Alertmanager.
func (r *ClientResolver) AlertmanagerFor(ctx context.Context, clusterID string) (*AlertmanagerClient, error) {
	if k8s.IsLocalClusterID(clusterID) {
		return nil, ErrNoAlertmanager
	}
	entry, err := r.remote(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if entry.amErr != nil {
		return nil, entry.amErr
	}
	return entry.am, nil
}

// remote returns a remote cluster's cached clients, reading its binding
// when the entry is missing or expired, or when its revision moved since
// the entry was built (a write by another pod, which cannot Evict here).
func (r *ClientResolver) remote(ctx context.Context, clusterID string) (cachedClient, error) {
	if r.Bindings == nil {
		return cachedClient{}, ErrNoMetricsBinding
	}

	now := r.clock()
	r.mu.Lock()
	c, hit := r.cache[clusterID]
	hit = hit && now.Before(c.expires)
	epoch := r.epoch
	base := r.baseLocked()
	r.mu.Unlock()

	if hit {
		current, err := r.cachedStillCurrent(ctx, clusterID, c)
		if err != nil {
			return cachedClient{}, err
		}
		if current {
			return c, nil
		}
	}

	b, err := r.Bindings.GetMetricsBinding(ctx, clusterID)
	if err != nil {
		return cachedClient{}, err
	}
	if b.PrometheusURL == "" {
		return cachedClient{}, ErrNoMetricsBinding
	}
	pc, err := newRemotePrometheusClient(b.PrometheusURL, newBearerRoundTripper(base, b.PrometheusURL, b.Token))
	if err != nil {
		return cachedClient{}, err
	}
	entry := cachedClient{client: pc, revision: b.Revision, expires: now.Add(r.ttl())}
	switch {
	case b.AlertmanagerURL == "":
		entry.amErr = ErrNoAlertmanager
	case b.AlertmanagerTokenStale:
		// Build nothing: the stale token is never sent, and an
		// unauthenticated read would only fail as refused.
		entry.amErr = ErrAlertmanagerTokenReentry
	default:
		entry.am, entry.amErr = newAlertmanagerClient(b.AlertmanagerURL, newBearerRoundTripper(base, b.AlertmanagerURL, b.AlertmanagerToken))
	}

	r.mu.Lock()
	if r.epoch == epoch {
		if r.cache == nil {
			r.cache = map[string]cachedClient{}
		}
		r.cache[clusterID] = entry
	}
	r.mu.Unlock()
	return entry, nil
}

// cachedStillCurrent reports whether cached entry c still matches the stored
// binding. A reader without revisions trusts the entry until its TTL. When
// the binding is gone the entry is dropped and the read answers as an
// unbound cluster; a revision that cannot be read fails the read, as a cold
// binding read would, rather than serving a client the binding may no longer
// name.
func (r *ClientResolver) cachedStillCurrent(ctx context.Context, clusterID string, c cachedClient) (bool, error) {
	rr, ok := r.Bindings.(MetricsBindingRevisionReader)
	if !ok {
		return true, nil
	}
	rev, err := rr.MetricsBindingRevision(ctx, clusterID)
	if err != nil {
		if errors.Is(err, ErrNoMetricsBinding) {
			r.dropIfCached(clusterID, c)
		}
		return false, err
	}
	return rev.Equal(c.revision), nil
}

// dropIfCached removes clusterID's entry only while it is still c, so a
// fresher entry cached by a concurrent read survives.
func (r *ClientResolver) dropIfCached(clusterID string, c cachedClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.cache[clusterID]; ok && cur.client == c.client {
		delete(r.cache, clusterID)
	}
}

// Evict drops the cached clients for clusterID, so the next request reads
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

// bearerRoundTripper adds a bearer token to requests for the https origin of
// the URL it is bound to (a Prometheus or an Alertmanager; scheme, host and
// port as urlorigin.Of computes them), so neither a request elsewhere nor a
// plain-HTTP request to the same host ever carries it, and a URL bound as
// plain http never gets it at all. The caller's request is cloned, never
// modified.
type bearerRoundTripper struct {
	base   http.RoundTripper
	origin string
	token  string
}

func newBearerRoundTripper(base http.RoundTripper, boundURL, token string) *bearerRoundTripper {
	origin := urlorigin.Of(boundURL)
	if !strings.HasPrefix(origin, "https://") {
		origin = ""
	}
	return &bearerRoundTripper{base: base, origin: origin, token: token}
}

func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token == "" || t.origin == "" || urlorigin.Of(req.URL.String()) != t.origin {
		return t.base.RoundTrip(req)
	}
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(out)
}

// newRemotePrometheusClient builds a remote cluster's Prometheus client.
// Unlike the local client it never follows a redirect: a 3xx is returned
// as the response (and so fails the query), so a bound Prometheus, or a
// proxy in front of it, cannot steer the request anywhere else.
func newRemotePrometheusClient(address string, rt http.RoundTripper) (*PrometheusClient, error) {
	return newPrometheusClient(api.Config{
		Address: address,
		Client: &http.Client{
			Transport: rt,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	})
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
