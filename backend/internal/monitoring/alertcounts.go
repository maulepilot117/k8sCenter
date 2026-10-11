package monitoring

import "context"

// AlertCountsAdapter implements resources.RemoteAlertCounter over the
// Alertmanager the Resolver picks for the requested cluster. Errors keep the
// Alertmanager error classes (ErrNoAlertmanager, ErrNoMetricsBinding,
// ErrAlertmanagerTokenReentry, ErrAlertmanagerRefused, ...), so the dashboard
// chooses a fixed client message per class. Nothing is cached: every call
// reads the Alertmanager.
type AlertCountsAdapter struct {
	Resolver *ClientResolver
}

// RemoteAlertCounts returns the active and critical alert counts of the
// Alertmanager bound to clusterID. A nil Resolver behaves as "no binding".
func (a *AlertCountsAdapter) RemoteAlertCounts(ctx context.Context, clusterID string) (active int, critical int, err error) {
	if a.Resolver == nil {
		return 0, 0, ErrNoMetricsBinding
	}
	am, err := a.Resolver.AlertmanagerFor(ctx, clusterID)
	if err != nil {
		return 0, 0, err
	}
	counts, err := am.Counts(ctx)
	if err != nil {
		return 0, 0, err
	}
	return counts.Active, counts.Critical, nil
}
