package alerting

import "context"

// AlertCountAdapter implements resources.AlertCounter using the alerting Store.
type AlertCountAdapter struct {
	Store Store
}

// ActiveAlertCounts returns the number of active and critical alerts.
func (a *AlertCountAdapter) ActiveAlertCounts(ctx context.Context) (active int, critical int, err error) {
	alerts, err := a.Store.ActiveAlerts(ctx)
	if err != nil {
		return 0, 0, err
	}
	active = len(alerts)
	for _, alert := range alerts {
		if alert.Severity == "critical" {
			critical++
		}
	}
	return active, critical, nil
}
