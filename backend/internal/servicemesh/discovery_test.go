package servicemesh

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// --- fixtures ---------------------------------------------------------------

func istioCRDList() *metav1.APIResourceList {
	return &metav1.APIResourceList{
		GroupVersion: "networking.istio.io/v1",
		APIResources: []metav1.APIResource{{Name: "virtualservices", Kind: "VirtualService"}},
	}
}

func linkerdCRDList() *metav1.APIResourceList {
	return &metav1.APIResourceList{
		GroupVersion: "policy.linkerd.io/v1beta3",
		APIResources: []metav1.APIResource{{Name: "servers", Kind: "Server"}},
	}
}

func istiodDeployment(image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "istiod",
			Namespace: istioSystemNS,
			Labels:    map[string]string{"app": "istiod"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "discovery", Image: image}},
				},
			},
		},
	}
}

func linkerdIdentityDeployment(image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "linkerd-identity",
			Namespace: linkerdControlNS,
			Labels:    map[string]string{"linkerd.io/control-plane-component": "identity"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "identity", Image: image}},
				},
			},
		},
	}
}

func ztunnelDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ztunnel",
			Namespace: istioSystemNS,
			Labels:    map[string]string{"app": "ztunnel"},
		},
	}
}

// newFakeClients returns a kubernetes.Interface / discovery.DiscoveryInterface
// pair with pre-populated objects and a controllable discovery resource list.
func newFakeClients(gvs []*metav1.APIResourceList, objects ...runtime.Object) *fake.Clientset {
	cs := fake.NewClientset(objects...)
	cs.Resources = gvs
	return cs
}

// --- tests ------------------------------------------------------------------

func TestDiscoverer_InitialStatus(t *testing.T) {
	d := NewDiscoverer(nil, slog.Default())
	if d == nil {
		t.Fatal("expected non-nil discoverer")
	}

	status := d.Status(context.Background())
	if status.Istio != nil {
		t.Error("expected Istio detail to be nil before first probe")
	}
	if status.Linkerd != nil {
		t.Error("expected Linkerd detail to be nil before first probe")
	}
	if status.Detected != MeshNone {
		t.Errorf("expected detected=%q, got %q", MeshNone, status.Detected)
	}
	if status.LastChecked.IsZero() {
		t.Error("expected LastChecked to be set on construction")
	}
}

func TestDiscoverer_StaleCacheTriggersReprobe(t *testing.T) {
	// With a nil ClientFactory, Probe() short-circuits and refreshes only
	// LastChecked. The test verifies the lazy-re-probe path fires when
	// the cache is older than staleDuration.
	d := NewDiscoverer(nil, slog.Default())

	d.mu.Lock()
	d.status.LastChecked = time.Now().Add(-2 * staleDuration).UTC()
	originalTS := d.status.LastChecked
	d.mu.Unlock()

	status := d.Status(context.Background())
	if !status.LastChecked.After(originalTS) {
		t.Errorf("expected stale cache to trigger re-probe; original=%v new=%v",
			originalTS, status.LastChecked)
	}
}

func TestDetectionFrom(t *testing.T) {
	tests := []struct {
		name    string
		istio   *MeshInfo
		linkerd *MeshInfo
		want    MeshType
	}{
		{"none", nil, nil, MeshNone},
		{"istio only", &MeshInfo{Installed: true}, nil, MeshIstio},
		{"linkerd only", nil, &MeshInfo{Installed: true}, MeshLinkerd},
		{"both", &MeshInfo{Installed: true}, &MeshInfo{Installed: true}, MeshBoth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectionFrom(tt.istio, tt.linkerd); got != tt.want {
				t.Errorf("detectionFrom(%v, %v) = %q, want %q", tt.istio, tt.linkerd, got, tt.want)
			}
		})
	}
}

func TestVersionFromDeploymentImage(t *testing.T) {
	tests := []struct {
		name       string
		containers []corev1.Container
		want       string
	}{
		{"empty", nil, ""},
		{"no tag", []corev1.Container{{Image: "docker.io/istio/pilot"}}, ""},
		{"tagged", []corev1.Container{{Image: "docker.io/istio/pilot:1.24.0"}}, "1.24.0"},
		{"registry with port + tag", []corev1.Container{{Image: "ghcr.io:5000/linkerd/proxy:v2.15.1"}}, "v2.15.1"},
		{"trailing colon", []corev1.Container{{Image: "docker.io/istio/pilot:"}}, ""},
		{"multi-container picks first", []corev1.Container{
			{Image: "docker.io/istio/pilot:1.24.0"},
			{Image: "docker.io/istio/sidecar:1.23.0"},
		}, "1.24.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionFromDeploymentImage(tt.containers); got != tt.want {
				t.Errorf("versionFromDeploymentImage(%v) = %q, want %q", tt.containers, got, tt.want)
			}
		})
	}
}

func TestDiscoverer_Probe_BothMeshesInstalled(t *testing.T) {
	cs := newFakeClients(
		[]*metav1.APIResourceList{istioCRDList(), linkerdCRDList()},
		istiodDeployment("docker.io/istio/pilot:1.24.0"),
		linkerdIdentityDeployment("cr.l5d.io/linkerd/identity:v2.15.1"),
	)
	d := newDiscovererForTest(cs, cs.Discovery(), slog.Default())

	status := d.Probe(context.Background())

	if status.Detected != MeshBoth {
		t.Errorf("Detected = %q, want %q", status.Detected, MeshBoth)
	}
	if status.Istio == nil || !status.Istio.Installed || status.Istio.Version != "1.24.0" {
		t.Errorf("Istio = %+v, want installed + version 1.24.0", status.Istio)
	}
	if status.Istio.Namespace != istioSystemNS {
		t.Errorf("Istio.Namespace = %q, want %q", status.Istio.Namespace, istioSystemNS)
	}
	if status.Istio.Mode != MeshModeSidecar {
		t.Errorf("Istio.Mode = %q, want %q (no ztunnel)", status.Istio.Mode, MeshModeSidecar)
	}
	if status.Linkerd == nil || !status.Linkerd.Installed || status.Linkerd.Version != "v2.15.1" {
		t.Errorf("Linkerd = %+v, want installed + version v2.15.1", status.Linkerd)
	}
	if status.Linkerd.Namespace != linkerdControlNS {
		t.Errorf("Linkerd.Namespace = %q, want %q", status.Linkerd.Namespace, linkerdControlNS)
	}
}

func TestDiscoverer_Probe_OnlyIstioInstalled(t *testing.T) {
	cs := newFakeClients(
		[]*metav1.APIResourceList{istioCRDList()},
		istiodDeployment("docker.io/istio/pilot:1.24.0"),
	)
	d := newDiscovererForTest(cs, cs.Discovery(), slog.Default())

	status := d.Probe(context.Background())

	if status.Detected != MeshIstio {
		t.Errorf("Detected = %q, want %q", status.Detected, MeshIstio)
	}
	if status.Istio == nil || !status.Istio.Installed {
		t.Fatalf("expected Istio installed, got %+v", status.Istio)
	}
	if status.Linkerd != nil {
		t.Errorf("expected Linkerd nil when its CRDs are absent, got %+v", status.Linkerd)
	}
}

// TestDiscoverer_Probe_CRDsWithoutControlPlane covers the plan's scenario: Istio CRDs are
// installed but istiod is missing (crashed or deleted). The mesh should still
// report Installed=true with Version="unknown" — CRD presence implies install
// intent.
func TestDiscoverer_Probe_CRDsWithoutControlPlane(t *testing.T) {
	cs := newFakeClients([]*metav1.APIResourceList{istioCRDList()})
	d := newDiscovererForTest(cs, cs.Discovery(), slog.Default())

	status := d.Probe(context.Background())

	if status.Istio == nil {
		t.Fatal("expected Istio info present when only CRDs exist")
	}
	if !status.Istio.Installed {
		t.Error("expected Installed=true when CRDs present")
	}
	if status.Istio.Version != versionUnknown {
		t.Errorf("Istio.Version = %q, want %q", status.Istio.Version, versionUnknown)
	}
	if status.Istio.Namespace != "" {
		t.Errorf("Istio.Namespace = %q, want empty (istiod absent)", status.Istio.Namespace)
	}
}

// TestDiscoverer_Probe_DiscoveryError_PreservesCache covers the plan's edge case: a
// transient discovery error (not a 404) must not flip the detected state —
// the previous cached MeshInfo is preserved and the caller sees a warning
// log instead of a failed request.
func TestDiscoverer_Probe_DiscoveryError_PreservesCache(t *testing.T) {
	cs := newFakeClients(nil)
	// Force every ServerResourcesForGroupVersion call to fail with a
	// non-NotFound error, simulating ErrGroupDiscoveryFailed.
	cs.PrependReactor("get", "resource", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom: api server unavailable")
	})

	d := newDiscovererForTest(cs, cs.Discovery(), slog.Default())

	// Seed cache as if a previous probe had detected Istio.
	seededIstio := &MeshInfo{
		Installed: true,
		Namespace: istioSystemNS,
		Version:   "1.24.0",
		Mode:      MeshModeSidecar,
	}
	d.mu.Lock()
	d.status = MeshStatus{
		Detected:    MeshIstio,
		Istio:       seededIstio,
		LastChecked: time.Now().Add(-time.Hour).UTC(),
	}
	d.mu.Unlock()

	status := d.Probe(context.Background())

	if status.Istio == nil {
		t.Fatal("expected cached Istio to be preserved through discovery error")
	}
	if status.Istio.Version != "1.24.0" {
		t.Errorf("Istio.Version = %q, want cached %q", status.Istio.Version, "1.24.0")
	}
	if status.Detected != MeshIstio {
		t.Errorf("Detected = %q, want cached %q", status.Detected, MeshIstio)
	}
	// LastChecked should still advance — the probe ran, it just preserved
	// detection state.
	if !status.LastChecked.After(time.Now().Add(-time.Minute)) {
		t.Error("expected LastChecked to advance even on preserved-cache path")
	}
}

func TestDiscoverer_Probe_AmbientModeDetection(t *testing.T) {
	cs := newFakeClients(
		[]*metav1.APIResourceList{istioCRDList()},
		istiodDeployment("docker.io/istio/pilot:1.24.0"),
		ztunnelDaemonSet(),
	)
	d := newDiscovererForTest(cs, cs.Discovery(), slog.Default())

	status := d.Probe(context.Background())

	if status.Istio == nil {
		t.Fatal("expected Istio installed")
	}
	if status.Istio.Mode != MeshModeAmbient {
		t.Errorf("Istio.Mode = %q, want %q (ztunnel DaemonSet present)",
			status.Istio.Mode, MeshModeAmbient)
	}
}
