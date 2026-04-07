package k8s

import (
	"log/slog"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestCiliumNetworkPolicies_NilWhenNoWatch(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	mgr := NewInformerManager(fakeCS, nil, testLogger())
	if mgr.CiliumNetworkPolicies() != nil {
		t.Error("expected nil lister when no CRD watch started")
	}
}
