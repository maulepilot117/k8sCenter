package resources

import (
	"testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

// TestRemoteDashboardReasonCodeParity pins the reason codes mirrored in
// dashboard_remote.go to their canonical k8s.ReasonCode values, the closed
// set the capabilities endpoint also discloses. A rename or value change of
// any mirrored code fails here, naming the constant that drifted.
func TestRemoteDashboardReasonCodeParity(t *testing.T) {
	mirrored := []struct {
		name      string
		mirror    string
		canonical k8s.ReasonCode
	}{
		{"ReasonOK", reasonOK, k8s.ReasonOK},
		{"ReasonUnsupportedPlatform", reasonUnsupportedPlatform, k8s.ReasonUnsupportedPlatform},
		{"ReasonUnreachable", reasonUnreachable, k8s.ReasonUnreachable},
		{"ReasonForbidden", reasonForbidden, k8s.ReasonForbidden},
		{"ReasonAuthzUnknown", reasonAuthzUnknown, k8s.ReasonAuthzUnknown},
		{"ReasonAuthzNamespaceScoped", reasonAuthzNamespaced, k8s.ReasonAuthzNamespaceScoped},
	}
	for _, m := range mirrored {
		if m.mirror != string(m.canonical) {
			t.Errorf("mirrored %s = %q, but k8s.%s = %q", m.name, m.mirror, m.name, m.canonical)
		}
	}
}
