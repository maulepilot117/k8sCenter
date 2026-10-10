package monitoring

import (
	"context"
	"errors"
	"testing"
	"time"
)

// adapterFixture is a resolver over a local fake Prometheus and a bindings
// reader that binds "remote-1" to a second fake with a bearer token: the
// same shape server.New wires into the dashboard adapters.
type adapterFixture struct {
	resolver *ClientResolver
	local    *fakePrometheus
	remote   *fakePrometheus
}

func newAdapterFixture(t *testing.T) *adapterFixture {
	t.Helper()
	local := newFakePrometheus(t, "11")
	remote := newFakePrometheus(t, "42")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: remote.srv.URL, Token: "remote-token"})
	return &adapterFixture{resolver: newTestResolver(localDiscoverer(t, local), b), local: local, remote: remote}
}

func (f *adapterFixture) assertRemoteOnly(t *testing.T) {
	t.Helper()
	if n := f.local.hits.Load(); n != 0 {
		t.Fatalf("the local Prometheus served %d request(s) for a remote cluster", n)
	}
	if f.remote.hits.Load() == 0 {
		t.Fatal("the remote Prometheus was never queried")
	}
	if got := f.remote.auth(); got != "Bearer remote-token" {
		t.Fatalf("Authorization = %q, want the binding's bearer token", got)
	}
}

func TestUtilizationAdapter_RemoteReadsOnlyTheBoundPrometheus(t *testing.T) {
	ctx := context.Background()

	t.Run("CPUPercent", func(t *testing.T) {
		f := newAdapterFixture(t)
		got, err := (&UtilizationAdapter{Resolver: f.resolver}).CPUPercent(ctx, "remote-1")
		if err != nil || got != 42 {
			t.Fatalf("CPUPercent = %v, %v; want 42 from the remote Prometheus", got, err)
		}
		f.assertRemoteOnly(t)
	})
	t.Run("MemoryPercent", func(t *testing.T) {
		f := newAdapterFixture(t)
		got, err := (&UtilizationAdapter{Resolver: f.resolver}).MemoryPercent(ctx, "remote-1")
		if err != nil || got != 42 {
			t.Fatalf("MemoryPercent = %v, %v; want 42 from the remote Prometheus", got, err)
		}
		f.assertRemoteOnly(t)
	})
	t.Run("DashboardTrends", func(t *testing.T) {
		f := newAdapterFixture(t)
		got, err := (&UtilizationAdapter{Resolver: f.resolver}).DashboardTrends(ctx, "remote-1", time.Hour, 2*time.Minute)
		if err != nil {
			t.Fatalf("DashboardTrends: %v", err)
		}
		if len(got.CPU) != 2 || got.CPU[0] != 42 {
			t.Fatalf("CPU series = %v, want the remote Prometheus's values", got.CPU)
		}
		if n := int(f.remote.hits.Load()); n != len(trendQueries) {
			t.Fatalf("remote served %d trend queries, want %d", n, len(trendQueries))
		}
		f.assertRemoteOnly(t)
	})
}

func TestUtilizationAdapter_UnboundRemoteIsNoBinding(t *testing.T) {
	ctx := context.Background()
	f := newAdapterFixture(t)
	a := &UtilizationAdapter{Resolver: f.resolver}

	if _, err := a.CPUPercent(ctx, "remote-2"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("CPUPercent: want ErrNoMetricsBinding, got %v", err)
	}
	if _, err := a.MemoryPercent(ctx, "remote-2"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("MemoryPercent: want ErrNoMetricsBinding, got %v", err)
	}
	if _, err := a.DashboardTrends(ctx, "remote-2", time.Hour, 2*time.Minute); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("DashboardTrends: want ErrNoMetricsBinding, got %v", err)
	}
	if n := f.local.hits.Load() + f.remote.hits.Load(); n != 0 {
		t.Fatalf("an unbound remote cluster reached a Prometheus (%d requests)", n)
	}
}

func TestUtilizationAdapter_LocalReadsOnlyTheLocalPrometheus(t *testing.T) {
	ctx := context.Background()
	f := newAdapterFixture(t)
	a := &UtilizationAdapter{Resolver: f.resolver}

	if got, err := a.CPUPercent(ctx, "local"); err != nil || got != 11 {
		t.Fatalf("CPUPercent = %v, %v; want 11 from the local Prometheus", got, err)
	}
	if got, err := a.MemoryPercent(ctx, ""); err != nil || got != 11 {
		t.Fatalf("MemoryPercent = %v, %v; want 11 from the local Prometheus", got, err)
	}
	if _, err := a.DashboardTrends(ctx, "local", time.Hour, 2*time.Minute); err != nil {
		t.Fatalf("DashboardTrends: %v", err)
	}
	if f.remote.hits.Load() != 0 {
		t.Fatal("the local cluster reached the remote Prometheus")
	}
	if got := f.local.auth(); got != "" {
		t.Fatalf("the local Prometheus received Authorization %q", got)
	}
	if n := int(f.local.hits.Load()); n != 2+len(trendQueries) {
		t.Fatalf("local served %d requests, want %d", n, 2+len(trendQueries))
	}
}

func TestControlPlaneAdapter_PassesClusterIDThrough(t *testing.T) {
	f := newAdapterFixture(t)
	a := &ControlPlaneAdapter{Resolver: f.resolver}

	if _, err := a.ControlPlaneStatus(context.Background(), "remote-1"); err != nil {
		t.Fatalf("ControlPlaneStatus: %v", err)
	}
	f.assertRemoteOnly(t)

	if _, err := a.ControlPlaneStatus(context.Background(), "remote-2"); !errors.Is(err, ErrNoMetricsBinding) {
		t.Fatalf("unbound remote: want ErrNoMetricsBinding, got %v", err)
	}
	if f.local.hits.Load() != 0 {
		t.Fatal("an unbound remote cluster reached the local Prometheus")
	}
}
