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

