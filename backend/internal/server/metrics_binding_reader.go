package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/store"
)

// metricsBindingGetter is the read half of *store.ClusterMetricsStore.
type metricsBindingGetter interface {
	Get(ctx context.Context, clusterID string) (store.MetricsBinding, store.MetricsTokens, error)
}

// metricsBindingReader adapts the binding store to
// monitoring.MetricsBindingReader. It lives here because neither store nor
// monitoring imports the other, and this package already imports both.
type metricsBindingReader struct {
	store metricsBindingGetter
}

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
	}, nil
}
