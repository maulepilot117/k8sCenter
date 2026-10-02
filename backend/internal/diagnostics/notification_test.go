package diagnostics

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// TestFindingNotification_CarriesClusterAndUID pins the notification a failed
// diagnostic finding emits. Cluster and UID are both part of the
// notification dedup identity (defect #2): the same pod name failing on
// another cluster, or a pod recreated under the same name, is a separate
// event that must not be suppressed as a repeat.
func TestFindingNotification_CarriesClusterAndUID(t *testing.T) {
	target := &DiagnosticTarget{
		Kind:      "Pod",
		Name:      "web",
		Namespace: "team-a",
		Object:    &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-web"}},
	}
	result := Result{RuleName: "CrashLoopBackOff", Status: "fail", Severity: SeverityCritical, Message: "restarting"}

	n := findingNotification("local", target, result)

	if n.ResourceUID != "uid-web" {
		t.Errorf("ResourceUID = %q, want uid-web", n.ResourceUID)
	}
	if n.ClusterID != "local" {
		t.Errorf("ClusterID = %q, want local (the request's cluster)", n.ClusterID)
	}
	if n.Source != notifications.SourceDiagnostic || n.Severity != notifications.SeverityCritical {
		t.Errorf("source/severity = %q/%q", n.Source, n.Severity)
	}
	if n.Title != "CrashLoopBackOff: web" || n.Message != "restarting" {
		t.Errorf("title/message = %q/%q", n.Title, n.Message)
	}
	if n.ResourceKind != "Pod" || n.ResourceNS != "team-a" || n.ResourceName != "web" {
		t.Errorf("resource = %q %q/%q", n.ResourceKind, n.ResourceNS, n.ResourceName)
	}
}

func TestFindingNotification_WarningAndNoObject(t *testing.T) {
	// A target whose object is missing (or not a metav1.Object) still
	// notifies; it just has no UID to contribute.
	target := &DiagnosticTarget{Kind: "Service", Name: "api", Namespace: "team-a"}
	result := Result{RuleName: "NoEndpoints", Status: "fail", Severity: SeverityWarning, Message: "no ready endpoints"}

	n := findingNotification("", target, result)

	if n.ResourceUID != "" || n.ClusterID != "" {
		t.Errorf("uid/cluster = %q/%q, want both empty", n.ResourceUID, n.ClusterID)
	}
	if n.Severity != notifications.SeverityWarning {
		t.Errorf("severity = %q, want warning", n.Severity)
	}
}
