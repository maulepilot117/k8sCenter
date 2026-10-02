package certmanager

import (
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// TestExpiryNotification_CarriesCertificateUID pins the notification the
// expiry poller emits. The certificate UID is part of the notification
// dedup identity (defect #2): a certificate deleted and reissued under the
// same name inside the 15-minute window is a different certificate, and its
// first expiry warning must not be suppressed as a repeat.
func TestExpiryNotification_CarriesCertificateUID(t *testing.T) {
	now := time.Now()
	cert := Certificate{
		UID:           "uid-cert-1",
		Name:          "wildcard-tls",
		Namespace:     "ingress",
		NotAfter:      ptrTime(now.Add(5 * 24 * time.Hour)),
		DaysRemaining: intPtr(5),
	}

	n := expiryNotification(emitRecord{Certificate: cert, Severity: "critical", Threshold: thresholdCritical})

	if n.ResourceUID != "uid-cert-1" {
		t.Errorf("ResourceUID = %q, want uid-cert-1", n.ResourceUID)
	}
	// The poller reads the local cluster only; empty is the local identity.
	if n.ClusterID != "" {
		t.Errorf("ClusterID = %q, want empty (local)", n.ClusterID)
	}
	if n.Source != notifications.SourceCertManager || n.Severity != notifications.SeverityCritical {
		t.Errorf("source/severity = %q/%q", n.Source, n.Severity)
	}
	if n.ResourceKind != "certificate.expiring" || n.ResourceNS != "ingress" || n.ResourceName != "wildcard-tls" {
		t.Errorf("resource = %q %q/%q", n.ResourceKind, n.ResourceNS, n.ResourceName)
	}
	if n.Title != "Certificate expiring (critical)" || n.Message != "A certificate expires in 5 day(s)" {
		t.Errorf("title/message = %q / %q", n.Title, n.Message)
	}
}

func TestExpiryNotification_Expired(t *testing.T) {
	cert := Certificate{UID: "uid-cert-2", Name: "old", Namespace: "default", DaysRemaining: intPtr(-1)}

	n := expiryNotification(emitRecord{Certificate: cert, Severity: "expired", Threshold: thresholdExpired})

	if n.ResourceKind != "certificate.expired" || n.Severity != notifications.SeverityCritical {
		t.Errorf("kind/severity = %q/%q", n.ResourceKind, n.Severity)
	}
	if n.Title != "Certificate expired (critical)" || n.ResourceUID != "uid-cert-2" {
		t.Errorf("title/uid = %q/%q", n.Title, n.ResourceUID)
	}
}

func TestExpiryNotification_Warning(t *testing.T) {
	cert := Certificate{UID: "uid-cert-3", Name: "api", Namespace: "default", DaysRemaining: intPtr(20)}

	n := expiryNotification(emitRecord{Certificate: cert, Severity: "warning", Threshold: thresholdWarning})

	if n.Severity != notifications.SeverityWarning || n.ResourceKind != "certificate.expiring" {
		t.Errorf("severity/kind = %q/%q", n.Severity, n.ResourceKind)
	}
}
