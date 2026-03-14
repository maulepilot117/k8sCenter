package wizard

import (
	"strings"
	"testing"
)

// --- StorageClassInput validation tests ---

func TestStorageClassValidate_Valid(t *testing.T) {
	s := StorageClassInput{
		Name:        "fast-storage",
		Provisioner: "ebs.csi.aws.com",
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestStorageClassValidate_ValidFull(t *testing.T) {
	s := StorageClassInput{
		Name:                 "longhorn-replicated",
		Provisioner:          "driver.longhorn.io",
		ReclaimPolicy:        "Retain",
		VolumeBindingMode:    "WaitForFirstConsumer",
		AllowVolumeExpansion: true,
		IsDefault:            true,
		Parameters:           map[string]string{"numberOfReplicas": "3"},
		MountOptions:         []string{"debug"},
	}
	if errs := s.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestStorageClassValidate_MissingRequired(t *testing.T) {
	s := StorageClassInput{}
	errs := s.Validate()
	fields := map[string]bool{}
	for _, e := range errs {
		fields[e.Field] = true
	}
	if !fields["name"] {
		t.Error("expected name validation error")
	}
	if !fields["provisioner"] {
		t.Error("expected provisioner validation error")
	}
}

func TestStorageClassValidate_InvalidName(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"MyClass"},                      // uppercase
		{"-start-dash"},                  // starts with dash
		{"has spaces"},                   // spaces
		{strings.Repeat("a", 254)},       // too long
	}
	for _, tt := range tests {
		s := StorageClassInput{Name: tt.name, Provisioner: "test.csi"}
		errs := s.Validate()
		found := false
		for _, e := range errs {
			if e.Field == "name" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected name validation error for %q, got none", tt.name)
		}
	}
}

func TestStorageClassValidate_InvalidReclaimPolicy(t *testing.T) {
	s := StorageClassInput{
		Name:          "test",
		Provisioner:   "test.csi",
		ReclaimPolicy: "Recycle",
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "reclaimPolicy" {
			found = true
		}
	}
	if !found {
		t.Error("expected reclaimPolicy validation error for Recycle")
	}
}

func TestStorageClassValidate_InvalidBindingMode(t *testing.T) {
	s := StorageClassInput{
		Name:              "test",
		Provisioner:       "test.csi",
		VolumeBindingMode: "Invalid",
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "volumeBindingMode" {
			found = true
		}
	}
	if !found {
		t.Error("expected volumeBindingMode validation error")
	}
}

func TestStorageClassValidate_TooManyParameters(t *testing.T) {
	params := make(map[string]string, 51)
	for i := range 51 {
		params[strings.Repeat("k", i+1)] = "v"
	}
	s := StorageClassInput{
		Name:        "test",
		Provisioner: "test.csi",
		Parameters:  params,
	}
	errs := s.Validate()
	found := false
	for _, e := range errs {
		if e.Field == "parameters" {
			found = true
		}
	}
	if !found {
		t.Error("expected parameters validation error for >50 entries")
	}
}

func TestStorageClassToStorageClass_Basic(t *testing.T) {
	s := StorageClassInput{
		Name:        "fast",
		Provisioner: "ebs.csi.aws.com",
	}
	sc := s.ToStorageClass()
	if sc.Name != "fast" {
		t.Errorf("expected name fast, got %s", sc.Name)
	}
	if sc.Provisioner != "ebs.csi.aws.com" {
		t.Errorf("expected provisioner ebs.csi.aws.com, got %s", sc.Provisioner)
	}
	if sc.TypeMeta.Kind != "StorageClass" {
		t.Errorf("expected kind StorageClass, got %s", sc.TypeMeta.Kind)
	}
	if sc.AllowVolumeExpansion == nil || *sc.AllowVolumeExpansion != false {
		t.Error("expected allowVolumeExpansion to be false")
	}
}

func TestStorageClassToStorageClass_Full(t *testing.T) {
	s := StorageClassInput{
		Name:                 "replicated",
		Provisioner:          "driver.longhorn.io",
		ReclaimPolicy:        "Retain",
		VolumeBindingMode:    "WaitForFirstConsumer",
		AllowVolumeExpansion: true,
		IsDefault:            true,
		Parameters:           map[string]string{"numberOfReplicas": "3"},
		MountOptions:         []string{"debug", "noatime"},
	}
	sc := s.ToStorageClass()

	if sc.ReclaimPolicy == nil || string(*sc.ReclaimPolicy) != "Retain" {
		t.Error("expected reclaimPolicy Retain")
	}
	if sc.VolumeBindingMode == nil || string(*sc.VolumeBindingMode) != "WaitForFirstConsumer" {
		t.Error("expected volumeBindingMode WaitForFirstConsumer")
	}
	if sc.AllowVolumeExpansion == nil || *sc.AllowVolumeExpansion != true {
		t.Error("expected allowVolumeExpansion true")
	}
	if sc.Annotations["storageclass.kubernetes.io/is-default-class"] != "true" {
		t.Error("expected default class annotation")
	}
	if sc.Parameters["numberOfReplicas"] != "3" {
		t.Error("expected parameter numberOfReplicas=3")
	}
	if len(sc.MountOptions) != 2 {
		t.Errorf("expected 2 mount options, got %d", len(sc.MountOptions))
	}
}

func TestStorageClassToStorageClass_NoDefault(t *testing.T) {
	s := StorageClassInput{
		Name:        "test",
		Provisioner: "test.csi",
		IsDefault:   false,
	}
	sc := s.ToStorageClass()
	if sc.Annotations != nil {
		t.Error("expected no annotations when IsDefault is false")
	}
}
