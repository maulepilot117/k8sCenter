package alerting

import (
	"testing"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// TestAlertNotification_CarriesFingerprint pins the notification an
// Alertmanager webhook alert emits. Alerts have no Kubernetes UID; the
// Alertmanager fingerprint (a hash of the full label set) is their stable
// identity and fills ResourceUID, which is part of the notification dedup
// identity (defect #2). Two alerts that share alertname and kind/namespace/
// name labels but differ in any other label (cluster, instance, ...) are
// distinct alerts and must not suppress each other.
func TestAlertNotification_CarriesFingerprint(t *testing.T) {
	action := AlertAction{
		Type: "new",
		Alert: WebhookAlert{
			Fingerprint: "fp-abc",
			Labels: map[string]string{
				"alertname": "KubePodCrashLooping",
				"severity":  "critical",
				"kind":      "Pod",
				"namespace": "team-a",
				"name":      "web",
			},
			Annotations: map[string]string{"description": "pod is crash looping"},
		},
	}

	n := alertNotification(action)

	if n.ResourceUID != "fp-abc" {
		t.Errorf("ResourceUID = %q, want fp-abc", n.ResourceUID)
	}
	// The webhook is ingested as the local cluster (ProcessWebhook records
	// alerts under this deployment's cluster); empty is the local identity.
	if n.ClusterID != "" {
		t.Errorf("ClusterID = %q, want empty (local)", n.ClusterID)
	}
	if n.Source != notifications.SourceAlert || n.Severity != notifications.SeverityCritical {
		t.Errorf("source/severity = %q/%q", n.Source, n.Severity)
	}
	if n.Title != "Alert new: KubePodCrashLooping" || n.Message != "pod is crash looping" {
		t.Errorf("title/message = %q/%q", n.Title, n.Message)
	}
	if n.ResourceKind != "Pod" || n.ResourceNS != "team-a" || n.ResourceName != "web" {
		t.Errorf("resource = %q %q/%q", n.ResourceKind, n.ResourceNS, n.ResourceName)
	}
}

func TestAlertNotification_SeverityMapping(t *testing.T) {
	cases := map[string]notifications.Severity{
		"critical": notifications.SeverityCritical,
		"warning":  notifications.SeverityWarning,
		"info":     notifications.SeverityInfo,
		"none":     notifications.SeverityInfo,
	}
	for label, want := range cases {
		a := AlertAction{Type: "new", Alert: WebhookAlert{Fingerprint: "fp", Labels: map[string]string{"alertname": "X", "severity": label}}}
		if got := alertNotification(a).Severity; got != want {
			t.Errorf("severity label %q -> %q, want %q", label, got, want)
		}
	}
	// No severity label at all keeps the historical warning default.
	a := AlertAction{Type: "new", Alert: WebhookAlert{Fingerprint: "fp", Labels: map[string]string{"alertname": "X"}}}
	if got := alertNotification(a).Severity; got != notifications.SeverityWarning {
		t.Errorf("missing severity label -> %q, want warning", got)
	}
}
