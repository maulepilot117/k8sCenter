package k8s

import "testing"

// TestClusterStatusValues pins the cluster-health status vocabulary's wire
// values. store.ClusterRecord.Status is a plain string column, and
// cross-package consumers (server.handle_clusters.go,
// server.handle_capabilities.go) compare against these constants rather than
// bare literals — but nothing stops someone from renaming a constant's
// *value* here without noticing the string itself is a cross-package
// contract with the database and with those consumers. This test fails loudly
// if that ever happens, instead of every remote cluster silently reporting
// unreachable.
func TestClusterStatusValues(t *testing.T) {
	cases := []struct {
		name  string
		got   ClusterStatus
		wantS string
	}{
		{"connected", StatusConnected, "connected"},
		{"disconnected", StatusDisconnected, "disconnected"},
		{"blocked", StatusBlocked, "blocked"},
		{"error", StatusError, "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.got) != tc.wantS {
				t.Errorf("ClusterStatus %s = %q, want %q", tc.name, string(tc.got), tc.wantS)
			}
			if tc.got.String() != tc.wantS {
				t.Errorf("ClusterStatus %s .String() = %q, want %q", tc.name, tc.got.String(), tc.wantS)
			}
		})
	}
}
