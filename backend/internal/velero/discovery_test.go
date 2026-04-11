package velero

import (
	"testing"
)

func TestPhaseHelpers(t *testing.T) {
	tests := []struct {
		phase      string
		isFailed   bool
		isWarning  bool
		isSuccess  bool
		isProgress bool
	}{
		{"Failed", true, false, false, false},
		{"FailedValidation", true, false, false, false},
		{"PartiallyFailed", false, true, false, false},
		{"Completed", false, false, true, false},
		{"Available", false, false, true, false},
		{"Enabled", false, false, true, false},
		{"InProgress", false, false, false, true},
		{"New", false, false, false, true},
		{"WaitingForPluginOperations", false, false, false, true},
		{"Finalizing", false, false, false, true},
		{"Queued", false, false, false, true},
		{"ReadyToStart", false, false, false, true},
		{"Unknown", false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			if got := IsFailedPhase(tt.phase); got != tt.isFailed {
				t.Errorf("IsFailedPhase(%q) = %v, want %v", tt.phase, got, tt.isFailed)
			}
			if got := IsWarningPhase(tt.phase); got != tt.isWarning {
				t.Errorf("IsWarningPhase(%q) = %v, want %v", tt.phase, got, tt.isWarning)
			}
			if got := IsSuccessPhase(tt.phase); got != tt.isSuccess {
				t.Errorf("IsSuccessPhase(%q) = %v, want %v", tt.phase, got, tt.isSuccess)
			}
			if got := IsProgressPhase(tt.phase); got != tt.isProgress {
				t.Errorf("IsProgressPhase(%q) = %v, want %v", tt.phase, got, tt.isProgress)
			}
		})
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
