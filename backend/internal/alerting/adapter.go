package alerting

import "context"

// AlertCountAdapter implements resources.AlertCounter using the alerting Store.
type AlertCountAdapter struct {
	Store Store
}

// ActiveAlertCounts returns the number of active and critical alerts.
func (a *AlertCountAdapter) ActiveAlertCounts(ctx context.Context) (active int, critical int, err error) {
	// Equivalent to ActiveAlertCountsExcluding with no exclusions.
	return a.ActiveAlertCountsExcluding(ctx)
}

// ActiveAlertCountsExcluding returns active and critical alert counts, skipping
// any alert whose AlertName matches one of the provided excludeAlertNames.
// This is used by the cluster health signal to filter always-firing heartbeat
// alerts (e.g. Watchdog, DeadMansSwitch) so they don't depress the health score.
func (a *AlertCountAdapter) ActiveAlertCountsExcluding(ctx context.Context, excludeAlertNames ...string) (active int, critical int, err error) {
	alerts, err := a.Store.ActiveAlerts(ctx)
	if err != nil {
		return 0, 0, err
	}
	excluded := make(map[string]struct{}, len(excludeAlertNames))
	for _, name := range excludeAlertNames {
		excluded[name] = struct{}{}
	}
	for _, alert := range alerts {
		if _, skip := excluded[alert.AlertName]; skip {
			continue
		}
		active++
		if alert.Severity == "critical" {
			critical++
		}
	}
	return active, critical, nil
}
