package gitops

import "testing"

func TestMapArgoSyncStatus(t *testing.T) {
	tests := []struct {
		input string
		want  SyncStatus
	}{
		{"Synced", SyncSynced},
		{"OutOfSync", SyncOutOfSync},
		{"Unknown", SyncUnknown},
		{"", SyncUnknown},
		{"SomethingElse", SyncUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapArgoSyncStatus(tt.input)
			if got != tt.want {
				t.Errorf("mapArgoSyncStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapArgoHealthStatus(t *testing.T) {
	tests := []struct {
		input string
		want  HealthStatus
	}{
		{"Healthy", HealthHealthy},
		{"Degraded", HealthDegraded},
		{"Progressing", HealthProgressing},
		{"Suspended", HealthSuspended},
		{"Missing", HealthDegraded},
		{"", HealthUnknown},
		{"SomethingElse", HealthUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapArgoHealthStatus(tt.input)
			if got != tt.want {
				t.Errorf("mapArgoHealthStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
