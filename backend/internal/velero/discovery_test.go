package velero

import (
	"testing"
)

func TestIsProgressPhase(t *testing.T) {
	tests := []struct {
		phase string
		want  bool
	}{
		{"New", true},
		{"InProgress", true},
		{"WaitingForPluginOperations", true},
		{"WaitingForPluginOperationsPartiallyFailed", true},
		{"Finalizing", true},
		{"FinalizingPartiallyFailed", true},
		{"Queued", true},
		{"ReadyToStart", true},
		{"Completed", false},
		{"PartiallyFailed", false},
		{"Failed", false},
		{"FailedValidation", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsProgressPhase(tt.phase); got != tt.want {
			t.Errorf("IsProgressPhase(%q) = %v, want %v", tt.phase, got, tt.want)
		}
	}
}

func TestGVRs(t *testing.T) {
	// Verify GVR constants are correctly defined
	if BackupGVR.Group != "velero.io" {
		t.Errorf("BackupGVR.Group = %q, want %q", BackupGVR.Group, "velero.io")
	}
	if BackupGVR.Version != "v1" {
		t.Errorf("BackupGVR.Version = %q, want %q", BackupGVR.Version, "v1")
	}
	if BackupGVR.Resource != "backups" {
		t.Errorf("BackupGVR.Resource = %q, want %q", BackupGVR.Resource, "backups")
	}

	if RestoreGVR.Resource != "restores" {
		t.Errorf("RestoreGVR.Resource = %q, want %q", RestoreGVR.Resource, "restores")
	}

	if ScheduleGVR.Resource != "schedules" {
		t.Errorf("ScheduleGVR.Resource = %q, want %q", ScheduleGVR.Resource, "schedules")
	}

	if BackupStorageLocationGVR.Resource != "backupstoragelocations" {
		t.Errorf("BackupStorageLocationGVR.Resource = %q, want %q", BackupStorageLocationGVR.Resource, "backupstoragelocations")
	}

	if VolumeSnapshotLocationGVR.Resource != "volumesnapshotlocations" {
		t.Errorf("VolumeSnapshotLocationGVR.Resource = %q, want %q", VolumeSnapshotLocationGVR.Resource, "volumesnapshotlocations")
	}
}
