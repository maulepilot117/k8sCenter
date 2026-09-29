package gateway

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

const localCluster = "local"

// localHandler builds a Handler over a fake local cluster: its dynamic client
// backs the service-account cache and its discovery backs the Discoverer,
// whose zero status makes the first Status call probe.
func localHandler(t *testing.T, lists []*metav1.APIResourceList, objs ...*unstructured.Unstructured) (*Handler, *fakeCluster) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cluster := newFakeCluster(t, lists, objs...)
	clients := &fakeClients{clusters: map[string]*fakeCluster{localCluster: cluster}}
	disc := &Discoverer{
		logger:        logger,
		discoOverride: func() discovery.DiscoveryInterface { return cluster.disc },
	}
	h := NewHandler(nil, disc, resources.NewAlwaysAllowAccessChecker(), clients, k8s.NewPresence(clients), logger)
	h.baseDynOverride = cluster.dyn
	return h, cluster
}

// A local cluster serving TLSRoute at v1 lists it there, not at v1alpha2.
func TestLocal_TLSRouteListedAtV1(t *testing.T) {
	h, _ := localHandler(t, v1Lists("v1"),
		gwObj("gateway.networking.k8s.io/v1", "TLSRoute", "edge", "tls", nil))

	rr := callOn(t, localCluster, h.HandleListRoutes, "/routes?kind=tlsroutes", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[[]RouteSummary](t, rr); len(got) != 1 || got[0].Name != "tls" {
		t.Errorf("routes = %+v, want tls", got)
	}
}

// A failed route list is dropped on the local cluster: the kind lists empty,
// the core kinds still list, and the summary carries no coverage.
func TestLocal_FailedRouteListIsDropped(t *testing.T) {
	lists := append(v1Lists(""), resourceList(APIGroup+"/v1alpha2", map[string]string{"TCPRoute": "tcproutes"}))
	h, cluster := localHandler(t, lists,
		gwObj("gateway.networking.k8s.io/v1", "Gateway", "edge", "gw", nil))
	cluster.dyn.PrependReactor("list", "tcproutes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("boom"))
	})

	rr := callOn(t, localCluster, h.HandleListRoutes, "/routes?kind=tcproutes", nil)
	if rr.Code != http.StatusOK || len(decode[[]RouteSummary](t, rr)) != 0 {
		t.Errorf("routes = %d %s, want 200 with none", rr.Code, rr.Body.String())
	}
	rr = callOn(t, localCluster, h.HandleListGateways, "/gateways", nil)
	if rr.Code != http.StatusOK || len(decode[[]GatewaySummary](t, rr)) != 1 {
		t.Errorf("gateways = %d %s, want the gateway", rr.Code, rr.Body.String())
	}
	rr = callOn(t, localCluster, h.HandleSummary, "/summary", nil)
	if sum := decode[GatewayAPISummary](t, rr); rr.Code != http.StatusOK || len(sum.Coverage) != 0 {
		t.Errorf("summary = %d %+v, want 200 without coverage", rr.Code, sum)
	}
}

func TestLocal_FailedCoreListIs500(t *testing.T) {
	h, cluster := localHandler(t, v1Lists(""))
	cluster.dyn.PrependReactor("list", "gateways", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("boom"))
	})

	if rr := callOn(t, localCluster, h.HandleListGateways, "/gateways", nil); rr.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500: %s", rr.Code, rr.Body.String())
	}
}

// Without v1 Gateway API is unavailable, and v1alpha2 is not consulted.
func TestLocal_ProbeWithoutV1SkipsV1alpha2(t *testing.T) {
	lists := []*metav1.APIResourceList{resourceList(APIGroup+"/v1alpha2", map[string]string{"TCPRoute": "tcproutes"})}
	h, cluster := localHandler(t, lists)

	if st := h.Discoverer.Status(t.Context()); st.Available {
		t.Fatalf("status = %+v, want not available", st)
	}
	// The fake records one action per group-version read.
	if n := len(cluster.disc.Actions()); n != 1 {
		t.Errorf("probe read %d group versions, want only v1", n)
	}
}
