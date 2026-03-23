package wizard

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSnapshotInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      SnapshotInput
		wantErrors int
		wantFields []string
	}{
		{
			name:       "valid snapshot",
			input:      SnapshotInput{Name: "my-snap", Namespace: "default", SourcePVC: "my-pvc"},
			wantErrors: 0,
		},
		{
			name:       "valid with class",
			input:      SnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "csi-snap"},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      SnapshotInput{Namespace: "default", SourcePVC: "pvc"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "invalid name",
			input:      SnapshotInput{Name: "UPPER", Namespace: "default", SourcePVC: "pvc"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      SnapshotInput{Name: "snap", SourcePVC: "pvc"},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "missing sourcePVC",
			input:      SnapshotInput{Name: "snap", Namespace: "default"},
			wantErrors: 1, wantFields: []string{"sourcePVC"},
		},
		{
			name:       "invalid sourcePVC",
			input:      SnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "UPPER_CASE"},
			wantErrors: 1, wantFields: []string{"sourcePVC"},
		},
		{
			name:       "invalid class name",
			input:      SnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc", VolumeSnapshotClassName: "BAD NAME"},
			wantErrors: 1, wantFields: []string{"volumeSnapshotClassName"},
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

func TestSnapshotInputToVolumeSnapshot(t *testing.T) {
	input := SnapshotInput{
		Name: "test-snap", Namespace: "prod", SourcePVC: "db-data",
		VolumeSnapshotClassName: "csi-snap-class",
	}
	obj := input.ToVolumeSnapshot()

	if obj.GetKind() != "VolumeSnapshot" {
		t.Errorf("expected Kind=VolumeSnapshot, got %s", obj.GetKind())
	}
	if obj.GetName() != "test-snap" || obj.GetNamespace() != "prod" {
		t.Errorf("unexpected name/namespace: %s/%s", obj.GetName(), obj.GetNamespace())
	}

	apiVersion, _, _ := unstructured.NestedString(obj.Object, "apiVersion")
	if apiVersion != "snapshot.storage.k8s.io/v1" {
		t.Errorf("expected apiVersion=snapshot.storage.k8s.io/v1, got %s", apiVersion)
	}

	pvc, _, _ := unstructured.NestedString(obj.Object, "spec", "source", "persistentVolumeClaimName")
	if pvc != "db-data" {
		t.Errorf("expected sourcePVC=db-data, got %s", pvc)
	}

	className, _, _ := unstructured.NestedString(obj.Object, "spec", "volumeSnapshotClassName")
	if className != "csi-snap-class" {
		t.Errorf("expected className=csi-snap-class, got %s", className)
	}
}

func TestSnapshotInputToVolumeSnapshotNoClass(t *testing.T) {
	input := SnapshotInput{Name: "snap", Namespace: "default", SourcePVC: "pvc"}
	obj := input.ToVolumeSnapshot()

	_, found, _ := unstructured.NestedString(obj.Object, "spec", "volumeSnapshotClassName")
	if found {
		t.Error("expected no volumeSnapshotClassName when not set")
	}
}
