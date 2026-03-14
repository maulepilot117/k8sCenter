package alerting

import (
	"context"
	"time"
)

// WebhookPayload is the top-level Alertmanager v4 webhook payload.
type WebhookPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []WebhookAlert    `json:"alerts"`
}

// WebhookAlert is a single alert within a webhook payload.
type WebhookAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// AlertAction describes what happened when a webhook alert was processed.
type AlertAction struct {
	Type  string       // "new", "resolved", "updated"
	Alert WebhookAlert
	Event AlertEvent
}

// ProcessWebhook processes a webhook payload and returns the actions taken.
// It updates the store and returns actions for WS broadcasting and email notification.
func ProcessWebhook(ctx context.Context, store Store, payload *WebhookPayload, clusterID string) ([]AlertAction, error) {
	var actions []AlertAction

	for _, alert := range payload.Alerts {
		alertName := alert.Labels["alertname"]
		namespace := alert.Labels["namespace"]
		severity := alert.Labels["severity"]

		switch alert.Status {
		case "firing":
			event := AlertEvent{
				ClusterID:   clusterID,
				Fingerprint: alert.Fingerprint,
				Status:      "firing",
				AlertName:   alertName,
				Namespace:   namespace,
				Severity:    severity,
				Labels:      alert.Labels,
				Annotations: alert.Annotations,
				StartsAt:    alert.StartsAt,
				EndsAt:      alert.EndsAt,
			}

			if err := store.Record(ctx, event); err != nil {
				return actions, err
			}

			actions = append(actions, AlertAction{
				Type:  "new",
				Alert: alert,
				Event: event,
			})

		case "resolved":
			if err := store.Resolve(ctx, alert.Fingerprint, alert.EndsAt); err != nil {
				return actions, err
			}

			actions = append(actions, AlertAction{
				Type:  "resolved",
				Alert: alert,
				Event: AlertEvent{
					ClusterID:   clusterID,
					Fingerprint: alert.Fingerprint,
					Status:      "resolved",
					AlertName:   alertName,
					Namespace:   namespace,
					Severity:    severity,
					Labels:      alert.Labels,
					Annotations: alert.Annotations,
					StartsAt:    alert.StartsAt,
					EndsAt:      alert.EndsAt,
					ResolvedAt:  time.Now().UTC(),
				},
			})
		}
	}

	return actions, nil
}
