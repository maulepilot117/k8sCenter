package wizard

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestPVCInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      PVCInput
		wantErrors int
		wantFields []string
	}{
		{
			name: "valid PVC",
			input: PVCInput{
				Name: "my-pvc", Namespace: "default",
				StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce",
			},
			wantErrors: 0,
		},
		{
			name: "valid with DataSource",
			input: PVCInput{
				Name: "restored", Namespace: "default",
				StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce",
				DataSource: &PVCDataSource{Name: "snap-1", Kind: "VolumeSnapshot", APIGroup: "snapshot.storage.k8s.io"},
			},
			wantErrors: 0,
		},
		{
			name: "valid ReadWriteOncePod",
			input: PVCInput{
				Name: "db-data", Namespace: "default",
				StorageClassName: "fast", Size: "50Gi", AccessMode: "ReadWriteOncePod",
			},
			wantErrors: 0,
		},
		{
			name:       "missing name",
			input:      PVCInput{Namespace: "default", StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "invalid name",
			input:      PVCInput{Name: "UPPER", Namespace: "default", StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"name"},
		},
		{
			name:       "missing namespace",
			input:      PVCInput{Name: "pvc", StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"namespace"},
		},
		{
			name:       "missing storageClassName",
			input:      PVCInput{Name: "pvc", Namespace: "default", Size: "10Gi", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"storageClassName"},
		},
		{
			name:       "missing size",
			input:      PVCInput{Name: "pvc", Namespace: "default", StorageClassName: "standard", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"size"},
		},
		{
			name:       "invalid size",
			input:      PVCInput{Name: "pvc", Namespace: "default", StorageClassName: "standard", Size: "notasize", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"size"},
		},
		{
			name:       "zero size",
			input:      PVCInput{Name: "pvc", Namespace: "default", StorageClassName: "standard", Size: "0Gi", AccessMode: "ReadWriteOnce"},
			wantErrors: 1, wantFields: []string{"size"},
		},
		{
			name:       "invalid access mode",
			input:      PVCInput{Name: "pvc", Namespace: "default", StorageClassName: "standard", Size: "10Gi", AccessMode: "Invalid"},
			wantErrors: 1, wantFields: []string{"accessMode"},
		},
		{
			name: "incomplete DataSource",
			input: PVCInput{
				Name: "pvc", Namespace: "default", StorageClassName: "standard",
				Size: "10Gi", AccessMode: "ReadWriteOnce",
				DataSource: &PVCDataSource{Name: "", Kind: "", APIGroup: ""},
			},
			wantErrors: 3, wantFields: []string{"dataSource.name", "dataSource.kind", "dataSource.apiGroup"},
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

func TestPVCInputToPersistentVolumeClaim(t *testing.T) {
	input := PVCInput{
		Name: "my-pvc", Namespace: "prod",
		StorageClassName: "fast-ssd", Size: "100Gi", AccessMode: "ReadWriteOnce",
	}
	pvc := input.ToPersistentVolumeClaim()

	if pvc.Kind != "PersistentVolumeClaim" {
		t.Errorf("expected Kind=PersistentVolumeClaim, got %s", pvc.Kind)
	}
	if pvc.Name != "my-pvc" || pvc.Namespace != "prod" {
		t.Errorf("unexpected name/namespace: %s/%s", pvc.Name, pvc.Namespace)
	}
	if *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Errorf("expected storageClassName=fast-ssd, got %s", *pvc.Spec.StorageClassName)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("unexpected access modes: %v", pvc.Spec.AccessModes)
	}
	storage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storage.String() != "100Gi" {
		t.Errorf("expected 100Gi, got %s", storage.String())
	}
	if pvc.Spec.DataSource != nil {
		t.Error("expected nil DataSource")
	}
}

func TestPVCInputToPersistentVolumeClaimWithDataSource(t *testing.T) {
	input := PVCInput{
		Name: "restored", Namespace: "default",
		StorageClassName: "standard", Size: "10Gi", AccessMode: "ReadWriteOnce",
		DataSource: &PVCDataSource{
			Name: "my-snapshot", Kind: "VolumeSnapshot", APIGroup: "snapshot.storage.k8s.io",
		},
	}
	pvc := input.ToPersistentVolumeClaim()

	if pvc.Spec.DataSource == nil {
		t.Fatal("expected DataSource to be set")
	}
	if pvc.Spec.DataSource.Name != "my-snapshot" {
		t.Errorf("expected dataSource.name=my-snapshot, got %s", pvc.Spec.DataSource.Name)
	}
	if pvc.Spec.DataSource.Kind != "VolumeSnapshot" {
		t.Errorf("expected dataSource.kind=VolumeSnapshot, got %s", pvc.Spec.DataSource.Kind)
	}
	if *pvc.Spec.DataSource.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("expected dataSource.apiGroup=snapshot.storage.k8s.io, got %s", *pvc.Spec.DataSource.APIGroup)
	}
}
