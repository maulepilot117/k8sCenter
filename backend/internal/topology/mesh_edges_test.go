package topology

import (
	"testing"

	"github.com/kubecenter/kubecenter/internal/servicemesh"
)

// makeIndex builds a "Kind/Name" -> UID nameIndex for a list of services in a
// single namespace. Only Service nodes are needed for the mesh-edges tests;
// the existing graph builder populates this map for other kinds in production.
func makeIndex(services ...string) map[string]string {
	idx := make(map[string]string, len(services))
	for _, name := range services {
		idx["Service/"+name] = "uid-" + name
	}
	return idx
}

// vsRoute creates an Istio VirtualService TrafficRoute fixture. host is the
// VS's spec.hosts[0]; dests are spec.{http,tls,tcp}.route[].destination.host
// values.
func vsRoute(namespace, name, host string, dests ...string) servicemesh.TrafficRoute {
	r := servicemesh.TrafficRoute{
		Mesh:      servicemesh.MeshIstio,
		Kind:      "VirtualService",
		Name:      name,
		Namespace: namespace,
		Hosts:     []string{host},
	}
	for _, d := range dests {
		r.Destinations = append(r.Destinations, servicemesh.RouteDestination{Host: d})
	}
	return r
}

// spRoute creates a Linkerd ServiceProfile TrafficRoute fixture.
func spRoute(namespace, name, host string, dests ...string) servicemesh.TrafficRoute {
	r := servicemesh.TrafficRoute{
		Mesh:      servicemesh.MeshLinkerd,
		Kind:      "ServiceProfile",
		Name:      name,
		Namespace: namespace,
		Hosts:     []string{host},
	}
	for _, d := range dests {
		r.Destinations = append(r.Destinations, servicemesh.RouteDestination{Host: d})
	}
	return r
}

func findEdge(edges []Edge, source, target string, t EdgeType) bool {
	for _, e := range edges {
		if e.Source == source && e.Target == target && e.Type == t {
			return true
		}
	}
	return false
}

func TestBuildMeshEdges_HappyPath_IstioVS(t *testing.T) {
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{vsRoute("foo", "vs-a", "a", "b")}

	edges, stats := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if stats.Truncated {
		t.Fatalf("unexpected truncation")
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Errorf("missing mesh_vs edge a->b; got %+v", edges)
	}
}

func TestBuildMeshEdges_HappyPath_LinkerdSP(t *testing.T) {
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{spRoute("foo", "sp-a", "a", "b")}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshSP) {
		t.Errorf("missing mesh_sp edge a->b; got %+v", edges)
	}
}

func TestBuildMeshEdges_MultipleDestinations(t *testing.T) {
	idx := makeIndex("a", "b", "c", "d")
	routes := []servicemesh.TrafficRoute{vsRoute("foo", "vs-a", "a", "b", "c", "d")}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 3 {
		t.Fatalf("edges = %d, want 3", len(edges))
	}
	for _, target := range []string{"uid-b", "uid-c", "uid-d"} {
		if !findEdge(edges, "uid-a", target, EdgeMeshVS) {
			t.Errorf("missing edge uid-a -> %s", target)
		}
	}
}

func TestBuildMeshEdges_HostSuffixVariants(t *testing.T) {
	// All three forms must resolve to the same Service/b UID and dedup to
	// exactly one edge.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs-1", "a", "b"),
		vsRoute("foo", "vs-2", "a", "b.foo"),
		vsRoute("foo", "vs-3", "a", "b.foo.svc.cluster.local"),
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1 (host suffix variants must dedup)", len(edges))
	}
}

func TestBuildMeshEdges_NoMatchingDestination(t *testing.T) {
	idx := makeIndex("a") // no "b"
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs-a", "a", "external.example.com"),
		vsRoute("foo", "vs-b", "a", "b"), // b doesn't exist in idx
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 0 {
		t.Errorf("edges = %d, want 0 (unresolvable destinations must be silently dropped)", len(edges))
	}
}

func TestBuildMeshEdges_RouteInDifferentNamespace(t *testing.T) {
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{vsRoute("bar", "vs-a", "a", "b")}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 0 {
		t.Errorf("edges = %d, want 0 (route in namespace bar must not appear in foo's graph)", len(edges))
	}
}

func TestBuildMeshEdges_CrossNamespaceHost(t *testing.T) {
	// Source host "a.bar" (cross-namespace) should NOT resolve to Service/a
	// in namespace foo — that would silently merge unrelated services.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		{
			Mesh:         servicemesh.MeshIstio,
			Kind:         "VirtualService",
			Name:         "vs",
			Namespace:    "foo",
			Hosts:        []string{"a.bar"},
			Destinations: []servicemesh.RouteDestination{{Host: "b"}},
		},
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 0 {
		t.Errorf("edges = %d, want 0 (cross-namespace source host must not resolve)", len(edges))
	}
}

func TestBuildMeshEdges_EmptyAndNilInputs(t *testing.T) {
	idx := makeIndex("a", "b")

	edges, stats := buildMeshEdges(nil, "foo", idx, maxMeshEdges)
	if len(edges) != 0 || stats.Truncated {
		t.Errorf("nil routes: edges=%d truncated=%v, want 0/false", len(edges), stats.Truncated)
	}

	edges, stats = buildMeshEdges([]servicemesh.TrafficRoute{vsRoute("foo", "v", "a", "b")}, "", idx, maxMeshEdges)
	if len(edges) != 0 || stats.Truncated {
		t.Errorf("empty namespace: edges=%d truncated=%v, want 0/false", len(edges), stats.Truncated)
	}

	edges, stats = buildMeshEdges([]servicemesh.TrafficRoute{vsRoute("foo", "v", "a", "b")}, "foo", nil, maxMeshEdges)
	if len(edges) != 0 || stats.Truncated {
		t.Errorf("nil index: edges=%d truncated=%v, want 0/false", len(edges), stats.Truncated)
	}
}

func TestBuildMeshEdges_RouteWithoutHosts(t *testing.T) {
	// Hosts empty -> no source service can be resolved -> no edges, no panic.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		{
			Mesh:         servicemesh.MeshIstio,
			Kind:         "VirtualService",
			Name:         "vs",
			Namespace:    "foo",
			Destinations: []servicemesh.RouteDestination{{Host: "b"}},
		},
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 0 {
		t.Errorf("edges = %d, want 0 (route without hosts has no source)", len(edges))
	}
}

func TestBuildMeshEdges_Cap(t *testing.T) {
	idx := makeIndex("a")
	for i := range 2500 {
		// Each unique target name must exist in the index, otherwise edges
		// are dropped before hitting the cap.
		idx["Service/t"+itoa(i)] = "uid-t" + itoa(i)
	}

	dests := make([]string, 2500)
	for i := range dests {
		dests[i] = "t" + itoa(i)
	}

	routes := []servicemesh.TrafficRoute{vsRoute("foo", "fan", "a", dests...)}
	edges, stats := buildMeshEdges(routes, "foo", idx, 2000)

	if !stats.Truncated {
		t.Errorf("truncated = false, want true at cap")
	}
	if len(edges) != 2000 {
		t.Errorf("edges = %d, want 2000 (cap)", len(edges))
	}
}

func TestBuildMeshEdges_DedupAcrossRoutes(t *testing.T) {
	// Two VirtualService objects that route the same source -> target pair
	// must produce exactly one edge.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs-1", "a", "b"),
		vsRoute("foo", "vs-2", "a", "b"),
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 1 {
		t.Errorf("edges = %d, want 1 (duplicate source/target pair must dedup)", len(edges))
	}
}

func TestBuildMeshEdges_BothMeshesProduceDistinctEdges(t *testing.T) {
	// An Istio VS and a Linkerd SP routing the same a -> b pair produce
	// two edges, not one — different EdgeType means different semantics.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs", "a", "b"),
		spRoute("foo", "sp", "a", "b"),
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2 (Istio + Linkerd are distinct edge types)", len(edges))
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Error("missing mesh_vs edge")
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshSP) {
		t.Error("missing mesh_sp edge")
	}
}

func TestBuildMeshEdges_UnknownMeshOrKindSkipped(t *testing.T) {
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		// Unknown mesh
		{Mesh: servicemesh.MeshType("unknown"), Kind: "VirtualService", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		// Istio but not VirtualService — DestinationRule, Gateway are deferred from D1.
		{Mesh: servicemesh.MeshIstio, Kind: "DestinationRule", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		{Mesh: servicemesh.MeshIstio, Kind: "Gateway", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
		// Linkerd but not ServiceProfile.
		{Mesh: servicemesh.MeshLinkerd, Kind: "Server", Namespace: "foo", Hosts: []string{"a"}, Destinations: []servicemesh.RouteDestination{{Host: "b"}}},
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 0 {
		t.Errorf("edges = %d, want 0 (unknown mesh and non-routing kinds must be skipped)", len(edges))
	}
}

func TestBuildMeshEdges_SelfEdgeAllowed(t *testing.T) {
	// Istio canary patterns route a single host through DestinationRule
	// subsets of the same Service: VS hosts:[cart] route to cart-stable
	// and cart-canary, both of which are subsets of the cart Service.
	// In topology terms that's a single mesh_vs edge from cart to itself.
	// Dropping it would silently hide canary traffic from the overlay.
	idx := makeIndex("a")
	routes := []servicemesh.TrafficRoute{vsRoute("foo", "vs", "a", "a")}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if !findEdge(edges, "uid-a", "uid-a", EdgeMeshVS) {
		t.Errorf("missing self-edge uid-a -> uid-a; got %+v", edges)
	}
}

func TestBuildMeshEdges_CaseInsensitiveHostResolution(t *testing.T) {
	// VirtualService hosts are user-supplied free-form text, but the
	// underlying Service names are RFC 1123 lowercase. DNS hostnames are
	// case-insensitive, so an operator typing "MyService" in a VS spec
	// must still resolve to the lowercase "myservice" Service node.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs-1", "A", "B"),
		vsRoute("foo", "vs-2", "a", "B.Foo"),
		vsRoute("foo", "vs-3", "A.foo.svc.cluster.local", "b"),
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1 (case-insensitive lookups must dedup)", len(edges))
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Errorf("missing edge uid-a -> uid-b; got %+v", edges)
	}
}

func TestBuildMeshEdges_CustomClusterDomainResolves(t *testing.T) {
	// Clusters with --cluster-domain other than the default still produce
	// canonical "<svc>.<ns>.svc.<domain>" FQDNs in mesh routes. Splitting
	// at the first ".svc." separator (rather than literal-matching the
	// trailing domain) makes the resolver work uniformly across cluster
	// configurations without requiring an external config knob.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		vsRoute("foo", "vs-default", "a", "b.foo.svc.cluster.local"),
		vsRoute("foo", "vs-custom", "a", "b.foo.svc.k8s.example.com"),
		vsRoute("foo", "vs-shorter", "a", "b.foo.svc.cluster"),
	}

	edges, _ := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1 (cluster-domain variants must dedup to one edge)", len(edges))
	}
	if !findEdge(edges, "uid-a", "uid-b", EdgeMeshVS) {
		t.Errorf("missing edge uid-a -> uid-b across cluster-domain variants; got %+v", edges)
	}
}

func TestBuildMeshEdges_StatsCountsUnresolvedHosts(t *testing.T) {
	// One route's source host doesn't resolve (external host); two of
	// another route's destinations don't resolve. Stats should count
	// each independently so the caller can surface a "N unresolved hosts"
	// diagnostic instead of returning a silent empty graph.
	idx := makeIndex("a", "b")
	routes := []servicemesh.TrafficRoute{
		// Source unresolvable.
		vsRoute("foo", "vs-1", "external.example.com", "b"),
		// Source resolves; two of three destinations don't.
		vsRoute("foo", "vs-2", "a", "b", "external1.example.com", "external2.example.com"),
	}

	_, stats := buildMeshEdges(routes, "foo", idx, maxMeshEdges)

	if stats.Considered != 2 {
		t.Errorf("Considered = %d, want 2", stats.Considered)
	}
	if stats.UnresolvedSources != 1 {
		t.Errorf("UnresolvedSources = %d, want 1", stats.UnresolvedSources)
	}
	if stats.UnresolvedDests != 2 {
		t.Errorf("UnresolvedDests = %d, want 2", stats.UnresolvedDests)
	}
}

// itoa avoids importing strconv just for test fixtures.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
