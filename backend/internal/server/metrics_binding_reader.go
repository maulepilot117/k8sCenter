package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/store"
)

// metricsBindingGetter is the read half of *store.ClusterMetricsStore.
type metricsBindingGetter interface {
	Get(ctx context.Context, clusterID string) (store.MetricsBinding, store.MetricsTokens, error)
	Revision(ctx context.Context, clusterID string) (time.Time, error)
}

// metricsBindingReader adapts the binding store to
// monitoring.MetricsBindingReader. It lives here because neither store nor
// monitoring imports the other, and this package already imports both.
type metricsBindingReader struct {
	store metricsBindingGetter
}

// The production reader must report revisions: without them the resolver
// would serve a client built from a binding another pod has since changed
// for up to its TTL.
var _ monitoring.MetricsBindingRevisionReader = metricsBindingReader{}

// NewMetricsBindingReader returns the monitoring resolver's view of the
// binding store. A missing binding is reported as
// monitoring.ErrNoMetricsBinding; any other error (a database failure) is
// passed through unchanged so callers can classify it.
func NewMetricsBindingReader(s *store.ClusterMetricsStore) monitoring.MetricsBindingReader {
	return metricsBindingReader{store: s}
}

func (r metricsBindingReader) GetMetricsBinding(ctx context.Context, clusterID string) (monitoring.MetricsBinding, error) {
	b, tokens, err := r.store.Get(ctx, clusterID)
	if errors.Is(err, store.ErrMetricsBindingNotFound) {
		return monitoring.MetricsBinding{}, fmt.Errorf("cluster %s: %w", clusterID, monitoring.ErrNoMetricsBinding)
	}
	if err != nil {
		return monitoring.MetricsBinding{}, err
	}
	// The store returns the Alertmanager token only when its stored origin
	// is the current Alertmanager URL's origin, and flags a token that no
	// longer matches as stale; both pass through unchanged.
	return monitoring.MetricsBinding{
		ClusterID:              b.ClusterID,
		PrometheusURL:          b.PrometheusURL,
		AlertmanagerURL:        b.AlertmanagerURL,
		Token:                  tokens.Prometheus,
		AlertmanagerToken:      tokens.Alertmanager,
		AlertmanagerTokenStale: b.AlertmanagerTokenStale,
		Revision:               b.UpdatedAt,
	}, nil
}

// MetricsBindingRevision reports the binding's revision (its updated_at),
// with the same error mapping as GetMetricsBinding.
func (r metricsBindingReader) MetricsBindingRevision(ctx context.Context, clusterID string) (time.Time, error) {
	rev, err := r.store.Revision(ctx, clusterID)
	if errors.Is(err, store.ErrMetricsBindingNotFound) {
		return time.Time{}, fmt.Errorf("cluster %s: %w", clusterID, monitoring.ErrNoMetricsBinding)
	}
	if err != nil {
		return time.Time{}, err
	}
	return rev, nil
}
