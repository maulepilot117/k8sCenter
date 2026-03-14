package storage

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildClassInfo_Basic(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "fast",
			CreationTimestamp: metav1.Now(),
		},
		Provisioner: "ebs.csi.aws.com",
	}
	info := buildClassInfo(sc)
	if info.Name != "fast" {
		t.Errorf("expected name fast, got %s", info.Name)
	}
	if info.Provisioner != "ebs.csi.aws.com" {
		t.Errorf("expected provisioner ebs.csi.aws.com, got %s", info.Provisioner)
	}
	if info.IsDefault {
		t.Error("expected IsDefault to be false")
	}
}

func TestBuildClassInfo_Default(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-class",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
			CreationTimestamp: metav1.Now(),
		},
		Provisioner: "test.csi",
	}
	info := buildClassInfo(sc)
	if !info.IsDefault {
		t.Error("expected IsDefault to be true")
	}
}

func TestBuildClassInfo_WithPolicies(t *testing.T) {
	reclaimPolicy := corev1.PersistentVolumeReclaimRetain
	bindingMode := storagev1.VolumeBindingWaitForFirstConsumer
	allowExpansion := true
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "full",
			CreationTimestamp: metav1.Now(),
		},
		Provisioner:          "test.csi",
		ReclaimPolicy:        &reclaimPolicy,
		VolumeBindingMode:    &bindingMode,
		AllowVolumeExpansion: &allowExpansion,
		Parameters:           map[string]string{"type": "gp3"},
	}
	info := buildClassInfo(sc)
	if info.ReclaimPolicy != "Retain" {
		t.Errorf("expected reclaimPolicy Retain, got %s", info.ReclaimPolicy)
	}
	if info.VolumeBindingMode != "WaitForFirstConsumer" {
		t.Errorf("expected volumeBindingMode WaitForFirstConsumer, got %s", info.VolumeBindingMode)
	}
	if !info.AllowVolumeExpansion {
		t.Error("expected allowVolumeExpansion to be true")
	}
	if info.Parameters["type"] != "gp3" {
		t.Error("expected parameter type=gp3")
	}
}

func TestBuildDriverInfo_Basic(t *testing.T) {
	attachReq := true
	podInfo := false
	storageCap := true
	d := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: "ebs.csi.aws.com"},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired:  &attachReq,
			PodInfoOnMount:  &podInfo,
			StorageCapacity: &storageCap,
		},
	}
	info := buildDriverInfo(d, nil, nil)
	if info.Name != "ebs.csi.aws.com" {
		t.Errorf("expected name ebs.csi.aws.com, got %s", info.Name)
	}
	if !info.AttachRequired {
		t.Error("expected AttachRequired true")
	}
	if info.PodInfoOnMount {
		t.Error("expected PodInfoOnMount false")
	}
	if !info.StorageCapacity {
		t.Error("expected StorageCapacity true")
	}
}

func TestBuildDriverInfo_WithExpansion(t *testing.T) {
	allowExpansion := true
	d := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: "driver.longhorn.io"},
	}
	classes := []*storagev1.StorageClass{
		{
			Provisioner:          "driver.longhorn.io",
			AllowVolumeExpansion: &allowExpansion,
		},
		{
			Provisioner: "other.csi",
		},
	}
	info := buildDriverInfo(d, classes, nil)
	if !info.Capabilities.VolumeExpansion {
		t.Error("expected VolumeExpansion capability from matching StorageClass")
	}
}

func TestBuildDriverInfo_WithSnapshot(t *testing.T) {
	d := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: "ebs.csi.aws.com"},
	}
	snapshotDrivers := map[string]bool{
		"ebs.csi.aws.com": true,
	}
	info := buildDriverInfo(d, nil, snapshotDrivers)
	if !info.Capabilities.Snapshot {
		t.Error("expected Snapshot capability")
	}
	if !info.Capabilities.Clone {
		t.Error("expected Clone capability (correlated with snapshot)")
	}
}

func TestBuildSnapshotInfo(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name":              "snap-1",
			"namespace":         "default",
			"creationTimestamp": "2025-01-15T10:00:00Z",
		},
		"spec": map[string]any{
			"volumeSnapshotClassName": "csi-snap-class",
			"source": map[string]any{
				"persistentVolumeClaimName": "my-pvc",
			},
		},
		"status": map[string]any{
			"readyToUse":  true,
			"restoreSize": "10Gi",
		},
	}
	info := buildSnapshotInfo(obj)
	if info.Name != "snap-1" {
		t.Errorf("expected name snap-1, got %s", info.Name)
	}
	if info.Namespace != "default" {
		t.Errorf("expected namespace default, got %s", info.Namespace)
	}
	if info.VolumeSnapshotClassName != "csi-snap-class" {
		t.Errorf("expected VolumeSnapshotClassName csi-snap-class, got %s", info.VolumeSnapshotClassName)
	}
	if info.SourcePVC != "my-pvc" {
		t.Errorf("expected SourcePVC my-pvc, got %s", info.SourcePVC)
	}
	if !info.ReadyToUse {
		t.Error("expected ReadyToUse true")
	}
	if info.RestoreSize != "10Gi" {
		t.Errorf("expected RestoreSize 10Gi, got %s", info.RestoreSize)
	}
}

func TestBuildSnapshotInfo_MissingFields(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name": "snap-2",
		},
	}
	info := buildSnapshotInfo(obj)
	if info.Name != "snap-2" {
		t.Errorf("expected name snap-2, got %s", info.Name)
	}
	if info.Namespace != "" {
		t.Errorf("expected empty namespace, got %s", info.Namespace)
	}
	if info.ReadyToUse {
		t.Error("expected ReadyToUse false for missing status")
	}
}

func TestDriverPresets(t *testing.T) {
	// Verify known presets exist
	knownDrivers := []string{"driver.longhorn.io", "nfs.csi.k8s.io", "ebs.csi.aws.com", "rook-ceph.rbd.csi.ceph.com"}
	for _, d := range knownDrivers {
		preset, ok := DriverPresets[d]
		if !ok {
			t.Errorf("expected preset for driver %s", d)
			continue
		}
		if preset.DisplayName == "" {
			t.Errorf("expected display name for driver %s", d)
		}
		if len(preset.Parameters) == 0 {
			t.Errorf("expected parameters for driver %s", d)
		}
	}
}
