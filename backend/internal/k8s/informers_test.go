package k8s

import (
	"log/slog"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestProbeCRDs_NoCilium(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	result := probeCRDsAndCreateFactory(nil, fakeCS.Discovery(), time.Minute, testLogger())
	if result != nil {
		t.Error("expected nil when Cilium CRD is not installed")
	}
}

func TestProbeCRDs_WithCilium(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	fakeCS.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "cilium.io/v2",
			APIResources: []metav1.APIResource{
				{Name: "ciliumnetworkpolicies", Kind: "CiliumNetworkPolicy", Namespaced: true},
			},
		},
	}

	fakeDyn := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme())
	result := probeCRDsAndCreateFactory(fakeDyn, fakeCS.Discovery(), time.Minute, testLogger())
	if result == nil {
		t.Fatal("expected non-nil when Cilium CRD is installed")
	}
}

func TestCiliumNetworkPolicies_NilWhenNoDynFactory(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	mgr := NewInformerManager(fakeCS, nil, fakeCS.Discovery(), testLogger())
	if mgr.CiliumNetworkPolicies() != nil {
		t.Error("expected nil lister when dynClient is nil")
	}
}
