package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/monitoring"
)

// countingBindings is a MetricsBindingReader that counts reads, so a test
// can tell a cached client from a fresh binding read.
type countingBindings struct {
	mu    sync.Mutex
	reads int
}

func (c *countingBindings) GetMetricsBinding(_ context.Context, clusterID string) (monitoring.MetricsBinding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	// Loopback: the strict transport refuses it at dial time, so a query
	// fails fast without touching the network, after the client is cached.
	return monitoring.MetricsBinding{ClusterID: clusterID, PrometheusURL: "https://127.0.0.1:1"}, nil
}

func (c *countingBindings) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func newResolverWiringServer(t *testing.T, mh *monitoring.Handler) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cs := fake.NewSimpleClientset()
	return New(Deps{
		Config:            &config.Config{ClusterID: "local"},
		Logger:            logger,
		K8sClient:         k8s.NewFakeClientFactory(cs),
		Informers:         k8s.NewInformerManager(cs, nil, logger),
		MonitoringHandler: mh,
		ReadyFn:           func() bool { return true },
	})
}

// sharedResolvers returns the resolver behind each metrics consumer New
// wires, failing when one is missing or of the wrong type.
func sharedResolvers(t *testing.T, s *Server) map[string]*monitoring.ClientResolver {
	t.Helper()
	if s.ResourceHandler == nil {
		t.Fatal("ResourceHandler not built")
	}
	util, ok := s.ResourceHandler.Utilization.(*monitoring.UtilizationAdapter)
	if !ok {
		t.Fatalf("Utilization = %T; want *monitoring.UtilizationAdapter", s.ResourceHandler.Utilization)
	}
	trends, ok := s.ResourceHandler.Trends.(*monitoring.UtilizationAdapter)
	if !ok {
		t.Fatalf("Trends = %T; want *monitoring.UtilizationAdapter", s.ResourceHandler.Trends)
	}
	cp, ok := s.ResourceHandler.ControlPlane.(*monitoring.ControlPlaneAdapter)
	if !ok {
		t.Fatalf("ControlPlane = %T; want *monitoring.ControlPlaneAdapter", s.ResourceHandler.ControlPlane)
	}
	evicter, ok := s.MetricsResolver.(*monitoring.ClientResolver)
	if !ok {
		t.Fatalf("MetricsResolver = %T; want *monitoring.ClientResolver", s.MetricsResolver)
	}
	return map[string]*monitoring.ClientResolver{
		"monitoring handler": s.MonitoringHandler.Resolver,
		"utilization":        util.Resolver,
		"trends":             trends.Resolver,
		"control plane":      cp.Resolver,
		"MetricsResolver":    evicter,
	}
}

func assertOneResolver(t *testing.T, got map[string]*monitoring.ClientResolver, want *monitoring.ClientResolver) {
	t.Helper()
	if want == nil {
		t.Fatal("no resolver to compare against")
	}
	for name, r := range got {
		if r != want {
			t.Errorf("%s resolver = %p; want the shared instance %p", name, r, want)
		}
	}
}

// With a resolver set by the caller (main.go, with a database), every
// metrics consumer New wires shares it, and an Evict through the binding
// routes' MetricsResolver is seen by the dashboard adapters.
func TestNew_MetricsConsumersShareOneResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := &countingBindings{}
	resolver := monitoring.NewClientResolver(nil, bindings, logger)
	mh := &monitoring.Handler{Resolver: resolver, Logger: logger}

	s := newResolverWiringServer(t, mh)
	assertOneResolver(t, sharedResolvers(t, s), resolver)

	util := s.ResourceHandler.Utilization.(*monitoring.UtilizationAdapter)
	ctx := t.Context()
	_, _ = util.CPUPercent(ctx, testRemoteID)
	_, _ = util.CPUPercent(ctx, testRemoteID)
	if n := bindings.count(); n != 1 {
		t.Fatalf("binding reads = %d after two adapter calls; want 1 (cached)", n)
	}
	s.MetricsResolver.Evict(testRemoteID)
	_, _ = util.CPUPercent(ctx, testRemoteID)
	if n := bindings.count(); n != 2 {
		t.Errorf("binding reads = %d after Evict; want 2 (the adapter saw the eviction)", n)
	}
}

// Without a caller-set resolver (no database), New builds one, stores it on
// the monitoring handler, and hands the same instance to the adapters and
// MetricsResolver; a remote cluster then reads as not configured.
func TestNew_BuildsAndSharesResolverWhenUnset(t *testing.T) {
	mh := &monitoring.Handler{Discoverer: &monitoring.Discoverer{}}

	s := newResolverWiringServer(t, mh)
	if mh.Resolver == nil {
		t.Fatal("New did not store the built resolver on the monitoring handler")
	}
	assertOneResolver(t, sharedResolvers(t, s), mh.Resolver)

	util := s.ResourceHandler.Utilization.(*monitoring.UtilizationAdapter)
	if _, err := util.CPUPercent(t.Context(), testRemoteID); !errors.Is(err, monitoring.ErrNoMetricsBinding) {
		t.Errorf("remote CPUPercent err = %v; want ErrNoMetricsBinding", err)
	}
}

// Without monitoring, MetricsResolver stays a true nil interface, so the
// binding routes' evict is a no-op rather than a typed-nil call.
func TestNew_NoMonitoringLeavesMetricsResolverNil(t *testing.T) {
	s := newResolverWiringServer(t, nil)
	if s.MetricsResolver != nil {
		t.Errorf("MetricsResolver = %#v; want nil without monitoring", s.MetricsResolver)
	}
	s.evictMetricsClient(testRemoteID) // must not panic
}
