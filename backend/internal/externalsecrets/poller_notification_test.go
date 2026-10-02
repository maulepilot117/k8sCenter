package externalsecrets

import (
	"testing"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// TestNotificationFor_CarriesExternalSecretUID pins the notification the ESO
// poller emits. The ExternalSecret UID is part of the notification dedup
// identity (defect #2): an ExternalSecret deleted and recreated under the
// same name is a different object, and its first failure must not be
// suppressed as a repeat of its predecessor's.
func TestNotificationFor_CarriesExternalSecretUID(t *testing.T) {
	es := ExternalSecret{Namespace: "payments", Name: "stripe-api-key", UID: "uid-es-1"}

	n := notificationFor(emitRecord{ES: es, Title: TitleSyncFailed, Msg: "sync failed", Sev: notifications.SeverityCritical})

	if n.ResourceUID != "uid-es-1" {
		t.Errorf("ResourceUID = %q, want uid-es-1", n.ResourceUID)
	}
	// The poller runs against the local cluster only; empty is the local
	// identity, and RecentBySource's restart seeding matches it.
	if n.ClusterID != "" {
		t.Errorf("ClusterID = %q, want empty (local)", n.ClusterID)
	}
	if !n.SuppressResourceFields {
		t.Error("SuppressResourceFields must stay true for every ESO event (R28 tenant scope)")
	}
	if n.Source != notifications.SourceExternalSecrets || n.ResourceKind != "externalsecret" {
		t.Errorf("source/kind = %q/%q", n.Source, n.ResourceKind)
	}
	if n.ResourceNS != "payments" || n.ResourceName != "stripe-api-key" {
		t.Errorf("resource = %q/%q", n.ResourceNS, n.ResourceName)
	}
	if n.Title != TitleSyncFailed || n.Message != "sync failed" || n.Severity != notifications.SeverityCritical {
		t.Errorf("title/message/severity = %q/%q/%q", n.Title, n.Message, n.Severity)
	}
}
