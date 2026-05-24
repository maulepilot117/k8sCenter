package k8s

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
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

// TestIsLocalClusterIDExported verifies the exported IsLocalClusterID function
// produces identical results to the unexported alias. This ensures callers
// outside the k8s package (e.g. resources, server) see consistent behavior.
func TestIsLocalClusterIDExported(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"local", true},
		{"abc123", false},
		{"remote-cluster-1", false},
	}
	for _, tc := range cases {
		if got := IsLocalClusterID(tc.in); got != tc.want {
			t.Errorf("IsLocalClusterID(%q) = %v, want %v", tc.in, got, tc.want)
		}
		// Must agree with the unexported alias
		if IsLocalClusterID(tc.in) != isLocalClusterID(tc.in) {
			t.Errorf("IsLocalClusterID(%q) disagrees with isLocalClusterID(%q)", tc.in, tc.in)
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

// TestRouterFor_RemoteFailsClosedWhenStoreNil verifies F#18: when
// clusterStore is nil (local-only deployment without a database), a
// non-local clusterID hard-errors instead of silently routing to the
// local factory. The previous behavior was a silent downgrade — handlers
// that didn't double-check pair.IsLocal would execute "remote" requests
// against the local cluster. AccessChecker already failed closed in the
// same scenario; both layers now agree.
func TestRouterFor_RemoteFailsClosedWhenStoreNil(t *testing.T) {
	stubClient := &kubernetes.Clientset{}
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(stubClient, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := router.RouterFor(context.Background(), "some-remote-id", "alice", nil)
	if err == nil {
		t.Fatal("RouterFor returned nil error for non-local clusterID with nil clusterStore; want hard-error (F#18 fail-closed)")
	}
	if !strings.Contains(err.Error(), "no cluster store") {
		t.Errorf("error = %q; want substring 'no cluster store'", err.Error())
	}
}

// TestApplyClusterTLS_FailsClosedWithoutCAData is the F#5 regression test:
// when no CA data is stored and AllowInsecureTLS is false, building the
// remote config must error rather than silently disable TLS verification.
func TestApplyClusterTLS_FailsClosedWithoutCAData(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("no CA + no opt-in → error", func(t *testing.T) {
		cfg := &rest.Config{}
		err := applyClusterTLS(cfg, "my-cluster", nil, false, logger)
		if err == nil {
			t.Fatal("expected error; got nil")
		}
		want := "AllowInsecureTLS is false"
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q; want substring %q", err.Error(), want)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false (fail-closed must NOT mutate config)")
		}
	})

	t.Run("no CA + admin opt-in → insecure set", func(t *testing.T) {
		cfg := &rest.Config{}
		if err := applyClusterTLS(cfg, "homelab", nil, true, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = false; want true (AllowInsecureTLS opt-in must disable verification)")
		}
	})

	t.Run("CA data present → no change", func(t *testing.T) {
		cfg := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: []byte("-----BEGIN CERTIFICATE-----...")}}
		if err := applyClusterTLS(cfg, "prod", cfg.TLSClientConfig.CAData, false, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false (CA data must NOT trigger insecure path)")
		}
	})

	t.Run("CA data present + opt-in → no change (CA wins)", func(t *testing.T) {
		// AllowInsecureTLS is only consulted when CA data is missing.
		// Operators with both set keep the verified path.
		cfg := &rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}
		if err := applyClusterTLS(cfg, "weird", cfg.TLSClientConfig.CAData, true, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TLSClientConfig.Insecure {
			t.Error("Insecure = true; want false — CA data must beat the AllowInsecureTLS flag")
		}
	})
}

func TestLocalFactory(t *testing.T) {
	stubDyn := fake.NewSimpleDynamicClient(scheme.Scheme)
	factory := NewTestClientFactoryWithDynamic(&kubernetes.Clientset{}, stubDyn)
	router := NewClusterRouter(factory, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := router.LocalFactory(); got != factory {
		t.Errorf("LocalFactory() returned %v; want the original factory", got)
	}
}
