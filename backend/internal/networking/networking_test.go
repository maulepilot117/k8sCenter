package networking

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestExtractImageVersion(t *testing.T) {
	tests := []struct {
		name          string
		containers    []corev1.Container
		containerName string
		want          string
	}{
		{
			name: "versioned image with v prefix",
			containers: []corev1.Container{
				{Name: "cilium-agent", Image: "quay.io/cilium/cilium:v1.15.3"},
			},
			containerName: "cilium-agent",
			want:          "1.15.3",
		},
		{
			name: "versioned image without v prefix",
			containers: []corev1.Container{
				{Name: "calico-node", Image: "docker.io/calico/node:3.27.0"},
			},
			containerName: "calico-node",
			want:          "3.27.0",
		},
		{
			name: "no tag",
			containers: []corev1.Container{
				{Name: "cilium-agent", Image: "quay.io/cilium/cilium"},
			},
			containerName: "cilium-agent",
			want:          "",
		},
		{
			name: "wrong container name",
			containers: []corev1.Container{
				{Name: "other", Image: "nginx:1.25"},
			},
			containerName: "cilium-agent",
			want:          "",
		},
		{
			name: "empty container name matches first",
			containers: []corev1.Container{
				{Name: "agent", Image: "cilium:v1.14.0"},
			},
			containerName: "",
			want:          "1.14.0",
		},
		{
			name:          "empty containers",
			containers:    nil,
			containerName: "cilium-agent",
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractImageVersion(tt.containers, tt.containerName)
			if got != tt.want {
				t.Errorf("extractImageVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsInSlice(t *testing.T) {
	slice := []string{"kube-system", "cilium", "calico-system"}
	if !isInSlice("kube-system", slice) {
		t.Error("expected kube-system to be in slice")
	}
	if !isInSlice("cilium", slice) {
		t.Error("expected cilium to be in slice")
	}
	if isInSlice("default", slice) {
		t.Error("expected default not to be in slice")
	}
	if isInSlice("", slice) {
		t.Error("expected empty string not to be in slice")
	}
}

func TestFormatCNIMessage(t *testing.T) {
	tests := []struct {
		name string
		info *CNIInfo
		want string
	}{
		{
			name: "nil",
			info: nil,
			want: "No CNI plugin detected",
		},
		{
			name: "unknown",
			info: &CNIInfo{Name: CNIUnknown},
			want: "No CNI plugin detected",
		},
		{
			name: "cilium with version and status",
			info: &CNIInfo{
				Name:    CNICilium,
				Version: "1.15.3",
				Status:  CNIStatus{Ready: 3, Desired: 3, Healthy: true},
			},
			want: "cilium v1.15.3 (3/3 ready)",
		},
		{
			name: "calico without version",
			info: &CNIInfo{
				Name: CNICalico,
			},
			want: "calico",
		},
		{
			name: "flannel with version no nodes",
			info: &CNIInfo{
				Name:    CNIFlannel,
				Version: "0.24.0",
			},
			want: "flannel v0.24.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCNIMessage(tt.info)
			if got != tt.want {
				t.Errorf("FormatCNIMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCachedInfo_InitiallyNil(t *testing.T) {
	d := &Detector{}
	if d.CachedInfo() != nil {
		t.Error("expected CachedInfo to be nil initially")
	}
}

func TestCachedInfo_ReturnsCopy(t *testing.T) {
	d := &Detector{}
	d.cached = &CNIInfo{Name: CNICilium, Version: "1.0"}

	info1 := d.CachedInfo()
	info2 := d.CachedInfo()

	if info1 == info2 {
		t.Error("expected CachedInfo to return different pointers (copies)")
	}
	if info1.Name != CNICilium || info1.Version != "1.0" {
		t.Error("expected cached values to match")
	}
}

func TestCNIConstants(t *testing.T) {
	if CNICilium != "cilium" {
		t.Errorf("CNICilium = %q, want cilium", CNICilium)
	}
	if CNICalico != "calico" {
		t.Errorf("CNICalico = %q, want calico", CNICalico)
	}
	if CNIFlannel != "flannel" {
		t.Errorf("CNIFlannel = %q, want flannel", CNIFlannel)
	}
	if CNIUnknown != "unknown" {
		t.Errorf("CNIUnknown = %q, want unknown", CNIUnknown)
	}
}
