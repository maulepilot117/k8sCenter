package resources

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
)

// TestAccessChecker_RemoteClusterIDRequiresRouter is the F#9 regression:
// when no clusterRouter is wired and clusterID is non-local, CanAccess and
// CanAccessGroupResource must error rather than silently fall through to
// the local cluster's clientFactory — which would re-introduce the very
// vulnerability F#9 was filed to fix.
//
// The bug pre-fix: an admin on cluster A whose RBAC scopes them to
// namespace X-only on cluster B could have a SAR for cluster B answered by
// cluster A's local RBAC, silently leaking access.
func TestAccessChecker_RemoteClusterIDRequiresRouter(t *testing.T) {
	factory := &countingFactory{}
	ac := NewAccessChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := ac.CanAccess(context.Background(), "remote-cluster-1", "alice", []string{"dev"}, "list", "pods", "default")
	if err == nil {
		t.Fatal("expected error for non-local clusterID without a ClusterRouter; got nil")
	}
	if !strings.Contains(err.Error(), "ClusterRouter") {
		t.Errorf("error = %q; want substring \"ClusterRouter\" so operators know what's wired wrong", err.Error())
	}
	if factory.callCount != 0 {
		t.Errorf("local ClientForUser called %d times for a non-local SAR; want 0 (F#9 regression — silent local fallback)", factory.callCount)
	}

	// CanAccessGroupResource (CRD path) must behave identically.
	_, err = ac.CanAccessGroupResource(context.Background(), "remote-cluster-1", "alice", []string{"dev"}, "list", "argoproj.io", "applications", "default")
	if err == nil {
		t.Fatal("expected error from CanAccessGroupResource for non-local cluster without router; got nil")
	}
	if factory.callCount != 0 {
		t.Errorf("local ClientForUser called %d times via CRD path; want 0", factory.callCount)
	}
}

// TestAccessChecker_LocalClusterShortCircuits verifies the fast paths
// (alwaysAllow / alwaysDeny / predicate) all accept the new clusterID
// argument without ever calling clientForCluster. This is how every existing
// test fixture in the codebase (NewAlwaysAllowAccessChecker etc.) keeps
// working post-F#9 — the routing logic is bypassed before it can fire.
func TestAccessChecker_LocalClusterShortCircuits(t *testing.T) {
	t.Run("alwaysAllow", func(t *testing.T) {
		ac := NewAlwaysAllowAccessChecker()
		allowed, err := ac.CanAccess(context.Background(), "any-cluster", "alice", nil, "list", "pods", "ns")
		if err != nil || !allowed {
			t.Errorf("alwaysAllow returned (%v, %v); want (true, nil)", allowed, err)
		}
		allowed, err = ac.CanAccessGroupResource(context.Background(), "any-cluster", "alice", nil, "list", "g", "r", "ns")
		if err != nil || !allowed {
			t.Errorf("alwaysAllow group returned (%v, %v); want (true, nil)", allowed, err)
		}
	})
	t.Run("alwaysDeny", func(t *testing.T) {
		ac := NewAlwaysDenyAccessChecker()
		allowed, err := ac.CanAccess(context.Background(), "remote-X", "alice", nil, "list", "pods", "ns")
		if err != nil || allowed {
			t.Errorf("alwaysDeny returned (%v, %v); want (false, nil)", allowed, err)
		}
	})
}

// TestAccessCacheKey_SeparatesClusters is a unit test on the cache key
// structure itself: two keys with identical fields except clusterID must
// not collide. The accessCacheKey is a struct so this is enforced by Go's
// struct equality — but the test pins the contract so the next refactor
// can't drop clusterID from the key.
func TestAccessCacheKey_SeparatesClusters(t *testing.T) {
	a := accessCacheKey{clusterID: "local", username: "alice", verb: "list", resource: "pods", namespace: "default"}
	b := accessCacheKey{clusterID: "remote-X", username: "alice", verb: "list", resource: "pods", namespace: "default"}
	if a == b {
		t.Fatal("accessCacheKey equality ignored clusterID — local 'allow' decisions would leak into remote checks (F#9 regression)")
	}
	c := accessCacheKey{clusterID: "local", username: "alice", verb: "list", resource: "pods", namespace: "default"}
	if a != c {
		t.Error("accessCacheKey equality with identical fields disagreed; cache lookups would always miss")
	}
}

// TestApiGroupForResource_PodDisruptionBudgets verifies that apiGroupForResource
// maps "poddisruptionbudgets" to "policy" so SubjectAccessReviews for PDB RBAC
// checks carry the correct API group (T7 — FIX 1 regression guard).
func TestApiGroupForResource_PodDisruptionBudgets(t *testing.T) {
	got := apiGroupForResource("poddisruptionbudgets")
	if got != "policy" {
		t.Errorf("apiGroupForResource(%q) = %q, want %q", "poddisruptionbudgets", got, "policy")
	}
}

// TestApiGroupForResource_KnownMappings spot-checks that other well-known
// entries still resolve correctly after the PDB addition.
func TestApiGroupForResource_KnownMappings(t *testing.T) {
	cases := []struct {
		resource string
		want     string
	}{
		{"deployments", "apps"},
		{"statefulsets", "apps"},
		{"jobs", "batch"},
		{"cronjobs", "batch"},
		{"ingresses", "networking.k8s.io"},
		{"networkpolicies", "networking.k8s.io"},
		{"poddisruptionbudgets", "policy"},
		{"roles", "rbac.authorization.k8s.io"},
		{"pods", ""},
		{"nodes", ""},
		{"services", ""},
		{"persistentvolumeclaims", ""},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			got := apiGroupForResource(tc.resource)
			if got != tc.want {
				t.Errorf("apiGroupForResource(%q) = %q, want %q", tc.resource, got, tc.want)
			}
		})
	}
}

// --- helpers --------------------------------------------------------------

// countingFactory implements AccessChecker.clientFactory and records call
// count. Returns nil clientset — the routing tests above never reach the
// SAR call, they only assert which side of the local/remote split the
// AccessChecker took.
type countingFactory struct {
	callCount int
}

func (f *countingFactory) ClientForUser(username string, groups []string) (kubernetes.Interface, error) {
	f.callCount++
	return nil, nil
}
