package wizard

import (
	"strings"
	"testing"
)

func TestScheduledSnapshotInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      ScheduledSnapshotInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid",
			input: ScheduledSnapshotInput{
				Name: "daily-snap", Namespace: "default", SourcePVC: "db-data",
				VolumeSnapshotClassName: "csi-snap", Schedule: "0 0 * * *", RetentionCount: 5,
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      ScheduledSnapshotInput{Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", Schedule: "0 * * * *", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      ScheduledSnapshotInput{Name: "snap", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", Schedule: "0 * * * *", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "missing sourcePVC",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", VolumeSnapshotClassName: "snap", Schedule: "0 * * * *", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"sourcePVC"},
		},
		{
			name:       "missing snapshotClassName",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", Schedule: "0 * * * *", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"volumeSnapshotClassName"},
		},
		{
			name:       "missing schedule",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"schedule"},
		},
		{
			name:       "invalid schedule format",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", Schedule: "invalid", RetentionCount: 5},
			wantErrors: 1, wantFields: []string{"schedule"},
		},
		{
			name:       "retention too low",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", Schedule: "0 * * * *", RetentionCount: 0},
			wantErrors: 1, wantFields: []string{"retentionCount"},
		},
		{
			name:       "retention too high",
			input:      ScheduledSnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "snap", Schedule: "0 * * * *", RetentionCount: 101},
			wantErrors: 1, wantFields: []string{"retentionCount"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wf := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wf {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wf, errs)
				}
			}
		})
	}
}

func TestScheduledSnapshotInputToMultiDocYAML(t *testing.T) {
	input := ScheduledSnapshotInput{
		Name: "nightly", Namespace: "prod", SourcePVC: "db-data",
		VolumeSnapshotClassName: "csi-snap", Schedule: "0 0 * * *", RetentionCount: 7,
	}
	yaml, err := input.ToMultiDocYAML()
	if err != nil {
		t.Fatalf("ToMultiDocYAML: %v", err)
	}

	// Should contain 4 documents separated by ---
	docs := strings.Split(yaml, "---\n")
	// First doc has no preceding ---, so we expect 4 parts
	if len(docs) != 4 {
		t.Errorf("expected 4 YAML documents, got %d", len(docs))
	}

	// Check each document has expected kind
	expectedKinds := []string{"ServiceAccount", "Role", "RoleBinding", "CronJob"}
	for i, kind := range expectedKinds {
		if i >= len(docs) {
			break
		}
		if !strings.Contains(docs[i], "kind: "+kind) {
			t.Errorf("document %d should contain kind: %s, got:\n%s", i, kind, docs[i][:min(200, len(docs[i]))])
		}
	}

	// Check ServiceAccount name
	if !strings.Contains(yaml, "nightly-snapshotter") {
		t.Error("expected ServiceAccount name nightly-snapshotter")
	}

	// Check CronJob schedule
	if !strings.Contains(yaml, `schedule: 0 0 * * *`) {
		t.Error("expected CronJob schedule")
	}

	// Check kubectl image is pinned
	if !strings.Contains(yaml, "bitnami/kubectl:1.31") {
		t.Error("expected pinned kubectl image")
	}

	// Check labels
	if !strings.Contains(yaml, "k8scenter.io/scheduled") {
		t.Error("expected k8scenter.io/scheduled label in script")
	}
	if !strings.Contains(yaml, "k8scenter.io/source-pvc") {
		t.Error("expected k8scenter.io/source-pvc label")
	}

	// Check retention count is in the script
	if !strings.Contains(yaml, "7") {
		t.Error("expected retention count 7 in script")
	}

	// Check concurrencyPolicy
	if !strings.Contains(yaml, "concurrencyPolicy: Forbid") {
		t.Error("expected concurrencyPolicy: Forbid")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
