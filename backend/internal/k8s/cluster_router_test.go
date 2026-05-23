package k8s

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestIsLocalClusterID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"local", true},
		{"abc123", false},
		{"LOCAL", false}, // case-sensitive on purpose; middleware lowercases via WithClusterID
		{"local-something", false},
	}
	for _, tc := range cases {
		if got := isLocalClusterID(tc.in); got != tc.want {
			t.Errorf("isLocalClusterID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNormalizedClusterID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "local"},
		{"local", "local"},
		{"abc123", "abc123"},
		{"my-remote-cluster", "my-remote-cluster"},
	}
	for _, tc := range cases {
		if got := normalizedClusterID(tc.in); got != tc.want {
			t.Errorf("normalizedClusterID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRouterFor_LocalPath verifies that an empty/local clusterID dispatches
// to the localFactory and returns a populated ClientPair with IsLocal=true.
// We construct ClusterRouter with a test ClientFactory whose ClientForUser
// returns a stub clientset; no live Kubernetes API is required.
func TestRouterFor_LocalPath(t *testing.T) {
	// Use the dynamic-aware test factory so both client types stub out
	// without dialling a real cluster.
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx := context.Background()

	cases := []struct {
		name      string
		clusterID string
	}{
		{"empty clusterID", ""},
		{"explicit local", "local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pair, err := router.RouterFor(ctx, tc.clusterID, "alice", []string{"engineering"})
			if err != nil {
				t.Fatalf("RouterFor returned error: %v", err)
			}
			if pair == nil {
				t.Fatal("RouterFor returned nil ClientPair")
			}
			if !pair.IsLocal {
				t.Errorf("IsLocal = false for %q; want true", tc.clusterID)
			}
			if pair.ClusterID != "local" {
				t.Errorf("ClusterID = %q for input %q; want \"local\"", pair.ClusterID, tc.clusterID)
			}
			if pair.Typed == nil {
				t.Error("Typed client is nil")
			}
			if pair.Dynamic == nil {
				t.Error("Dynamic client is nil")
			}
		})
	}
}

// TestRouterFor_RemoteFallsThroughToLocalWhenStoreNil verifies the
// degraded-mode behavior: when clusterStore is nil (local-only deployment
// without a database), a non-local clusterID silently routes to the local
// factory. This mirrors the existing ClientForCluster contract — handlers
// that need stricter behavior (P2-5's 501 for non-local) must check
// pair.IsLocal themselves.
func TestRouterFor_RemoteFallsThroughToLocalWhenStoreNil(t *testing.T) {
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	pair, err := router.RouterFor(context.Background(), "some-remote-id", "alice", nil)
	if err != nil {
		t.Fatalf("RouterFor returned error: %v", err)
	}
	// IsLocal reflects the REQUESTED cluster, not the fallback. Handlers
	// that key behavior on IsLocal will return 501 here, which is the
	// correct safe default even when the store is misconfigured.
	if pair.IsLocal {
		t.Error("IsLocal = true for non-local input; want false (handlers must reject non-local)")
	}
	if pair.ClusterID != "some-remote-id" {
		t.Errorf("ClusterID = %q; want \"some-remote-id\"", pair.ClusterID)
	}
}

func TestLocalFactory(t *testing.T) {
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := router.LocalFactory(); got != factory {
		t.Errorf("LocalFactory() returned %v; want the original factory", got)
	}
}
