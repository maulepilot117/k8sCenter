package k8s

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
)

func crdTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestCRDDiscovery creates a CRDDiscovery with pre-populated CRDs for testing.
// It bypasses the real informer factory since we don't have a real API server.
func newTestCRDDiscovery(crds map[string]*CRDInfo) *CRDDiscovery {
	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClient(scheme)

	return &CRDDiscovery{
		dynClient:  dynClient,
		logger:     crdTestLogger(),
		crds:       crds,
		countCache: make(map[string]int),
	}
}

func TestResolveGVR_DeniesCoreAPIGroups(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{})

	deniedGroups := []string{
		"",                             // core
		"apps",                         // deployments, etc.
		"batch",                        // jobs, cronjobs
		"rbac.authorization.k8s.io",    // roles, bindings
		"networking.k8s.io",            // ingresses, netpol
		"storage.k8s.io",               // storageclasses
		"apiextensions.k8s.io",         // CRDs themselves
		"admissionregistration.k8s.io", // webhooks
		"autoscaling",                  // HPA
		"policy",                       // PDB
		"coordination.k8s.io",          // leases
		"discovery.k8s.io",             // endpointslices
		"events.k8s.io",                // events
		"scheduling.k8s.io",            // priority classes
		"certificates.k8s.io",          // CSR
		"authentication.k8s.io",        // token reviews
		"authorization.k8s.io",         // SAR
	}

	for _, group := range deniedGroups {
		_, ok := d.ResolveGVR(group, "anything")
		if ok {
			t.Errorf("expected group %q to be denied, but ResolveGVR returned true", group)
		}
	}
}

func TestResolveGVR_AllowsCRDGroups(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"cert-manager.io/certificates": {
			Group:    "cert-manager.io",
			Version:  "v1",
			Resource: "certificates",
			Kind:     "Certificate",
			Scope:    "Namespaced",
			Served:   true,
		},
		"cilium.io/ciliumnetworkpolicies": {
			Group:    "cilium.io",
			Version:  "v2",
			Resource: "ciliumnetworkpolicies",
			Kind:     "CiliumNetworkPolicy",
			Scope:    "Namespaced",
			Served:   true,
		},
		"monitoring.coreos.com/prometheusrules": {
			Group:    "monitoring.coreos.com",
			Version:  "v1",
			Resource: "prometheusrules",
			Kind:     "PrometheusRule",
			Scope:    "Namespaced",
			Served:   true,
		},
	})

	tests := []struct {
		group    string
		resource string
		wantGVR  schema.GroupVersionResource
	}{
		{
			group:    "cert-manager.io",
			resource: "certificates",
			wantGVR:  schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		},
		{
			group:    "cilium.io",
			resource: "ciliumnetworkpolicies",
			wantGVR:  schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies"},
		},
		{
			group:    "monitoring.coreos.com",
			resource: "prometheusrules",
			wantGVR:  schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"},
		},
	}

	for _, tt := range tests {
		gvr, ok := d.ResolveGVR(tt.group, tt.resource)
		if !ok {
			t.Errorf("ResolveGVR(%q, %q) returned false, want true", tt.group, tt.resource)
			continue
		}
		if gvr != tt.wantGVR {
			t.Errorf("ResolveGVR(%q, %q) = %v, want %v", tt.group, tt.resource, gvr, tt.wantGVR)
		}
	}
}

func TestResolveGVR_NotFound(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{})

	_, ok := d.ResolveGVR("nonexistent.example.com", "widgets")
	if ok {
		t.Error("expected ResolveGVR to return false for unknown CRD")
	}
}

func TestIsNamespaced(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"example.com/widgets": {
			Group:    "example.com",
			Version:  "v1",
			Resource: "widgets",
			Kind:     "Widget",
			Scope:    "Namespaced",
			Served:   true,
		},
		"example.com/clusterwidgets": {
			Group:    "example.com",
			Version:  "v1",
			Resource: "clusterwidgets",
			Kind:     "ClusterWidget",
			Scope:    "Cluster",
			Served:   true,
		},
	})

	tests := []struct {
		group    string
		resource string
		want     bool
	}{
		{"example.com", "widgets", true},
		{"example.com", "clusterwidgets", false},
		{"example.com", "nonexistent", false},      // not found
		{"apps", "deployments", false},              // denied group
	}

	for _, tt := range tests {
		got := d.IsNamespaced(tt.group, tt.resource)
		if got != tt.want {
			t.Errorf("IsNamespaced(%q, %q) = %v, want %v", tt.group, tt.resource, got, tt.want)
		}
	}
}

func TestGetCRDInfo(t *testing.T) {
	info := &CRDInfo{
		Group:    "example.com",
		Version:  "v1alpha1",
		Resource: "foos",
		Kind:     "Foo",
		Scope:    "Namespaced",
		Served:   true,
	}
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"example.com/foos": info,
	})

	got := d.GetCRDInfo("example.com", "foos")
	if got == nil {
		t.Fatal("expected non-nil CRDInfo")
	}
	if got.Kind != "Foo" {
		t.Errorf("Kind = %q, want %q", got.Kind, "Foo")
	}

	// Denied group returns nil.
	if d.GetCRDInfo("apps", "deployments") != nil {
		t.Error("expected nil for denied group")
	}

	// Unknown CRD returns nil.
	if d.GetCRDInfo("example.com", "bars") != nil {
		t.Error("expected nil for unknown CRD")
	}
}

func TestListCRDs_GroupsByAPIGroup(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"cert-manager.io/certificates": {
			Group: "cert-manager.io", Version: "v1", Resource: "certificates",
			Kind: "Certificate", Scope: "Namespaced", Served: true,
		},
		"cert-manager.io/issuers": {
			Group: "cert-manager.io", Version: "v1", Resource: "issuers",
			Kind: "Issuer", Scope: "Namespaced", Served: true,
		},
		"monitoring.coreos.com/prometheusrules": {
			Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules",
			Kind: "PrometheusRule", Scope: "Namespaced", Served: true,
		},
	})

	grouped := d.ListCRDs()

	if len(grouped) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(grouped))
	}

	certManagerCRDs := grouped["cert-manager.io"]
	if len(certManagerCRDs) != 2 {
		t.Errorf("cert-manager.io: expected 2 CRDs, got %d", len(certManagerCRDs))
	}

	monitoringCRDs := grouped["monitoring.coreos.com"]
	if len(monitoringCRDs) != 1 {
		t.Errorf("monitoring.coreos.com: expected 1 CRD, got %d", len(monitoringCRDs))
	}
}

func TestOnCRDAdd(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{})

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "certificates.cert-manager.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cert-manager.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "certificates",
				Kind:   "Certificate",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true},
				{Name: "v1beta1", Served: true, Storage: false},
			},
		},
	}

	d.onCRDAdd(crd)

	info := d.GetCRDInfo("cert-manager.io", "certificates")
	if info == nil {
		t.Fatal("expected CRD to be added")
	}
	if info.Version != "v1" {
		t.Errorf("Version = %q, want %q (should pick storage version)", info.Version, "v1")
	}
	if info.Scope != "Namespaced" {
		t.Errorf("Scope = %q, want %q", info.Scope, "Namespaced")
	}
}

func TestOnCRDAdd_DeniedGroup(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{})

	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "apps", // denied
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "deployments",
				Kind:   "Deployment",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true},
			},
		},
	}

	d.onCRDAdd(crd)

	if len(d.crds) != 0 {
		t.Error("denied group CRD should not be added")
	}
}

func TestOnCRDDelete(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"example.com/widgets": {
			Group: "example.com", Version: "v1", Resource: "widgets",
			Kind: "Widget", Scope: "Namespaced", Served: true,
		},
	})
	d.countCache["example.com/widgets"] = 42

	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "widgets"},
		},
	}

	d.onCRDDelete(crd)

	if len(d.crds) != 0 {
		t.Error("CRD should be removed after delete")
	}
	if _, ok := d.countCache["example.com/widgets"]; ok {
		t.Error("count cache entry should be removed after CRD delete")
	}
}

func TestOnCRDDelete_Tombstone(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{
		"example.com/widgets": {
			Group: "example.com", Version: "v1", Resource: "widgets",
			Kind: "Widget", Scope: "Namespaced", Served: true,
		},
	})

	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "widgets"},
		},
	}

	// Wrap in tombstone to simulate missed delete event.
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "widgets.example.com",
		Obj: crd,
	}

	d.onCRDDelete(tombstone)

	if len(d.crds) != 0 {
		t.Error("CRD should be removed even from tombstone")
	}
}

func TestCRDInfoFromSpec_ClusterScoped(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "clusterwidgets",
				Kind:   "ClusterWidget",
			},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true},
			},
		},
	}

	info := crdInfoFromSpec(crd)
	if info == nil {
		t.Fatal("expected non-nil CRDInfo")
	}
	if info.Scope != "Cluster" {
		t.Errorf("Scope = %q, want %q", info.Scope, "Cluster")
	}
}

func TestCRDInfoFromSpec_NoServedVersion(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "noop", Kind: "Noop"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: false, Storage: true},
			},
		},
	}

	info := crdInfoFromSpec(crd)
	if info != nil {
		t.Error("expected nil for CRD with no served versions")
	}
}

func TestCRDInfoFromSpec_AdditionalPrinterColumns(t *testing.T) {
	cols := []apiextensionsv1.CustomResourceColumnDefinition{
		{Name: "Status", Type: "string", JSONPath: ".status.phase"},
		{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"},
	}

	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "things", Kind: "Thing"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true, AdditionalPrinterColumns: cols},
			},
		},
	}

	info := crdInfoFromSpec(crd)
	if info == nil {
		t.Fatal("expected non-nil CRDInfo")
	}
	if len(info.AdditionalPrinterColumns) != 2 {
		t.Errorf("AdditionalPrinterColumns: got %d, want 2", len(info.AdditionalPrinterColumns))
	}
}

func TestGetCounts(t *testing.T) {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

	// Pre-populate the fake dynamic client with 3 widget objects.
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "WidgetList"}, &unstructured.UnstructuredList{})
	dynClient := fakedynamic.NewSimpleDynamicClient(scheme,
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]interface{}{"name": "w1", "namespace": "default"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]interface{}{"name": "w2", "namespace": "default"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]interface{}{"name": "w3", "namespace": "kube-system"},
		}},
	)

	d := &CRDDiscovery{
		dynClient: dynClient,
		logger:    crdTestLogger(),
		crds: map[string]*CRDInfo{
			"example.com/widgets": {
				Group: "example.com", Version: "v1", Resource: "widgets",
				Kind: "Widget", Scope: "Namespaced", Served: true,
			},
		},
		countCache: make(map[string]int),
	}

	// The fake client doesn't support Limit, so it returns all items.
	// This exercises the count path.
	_ = dynClient.Tracker().Add(&unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "example.com/v1", "kind": "Widget",
		"metadata": map[string]interface{}{"name": "w1", "namespace": "default"},
	}})

	counts := d.GetCounts(context.Background())

	// Verify that the GVR key has a count.
	count, ok := counts["example.com/widgets"]
	if !ok {
		t.Fatal("expected count for example.com/widgets")
	}
	_ = gvr
	// The fake client should return at least the objects we added.
	if count < 1 {
		t.Errorf("count = %d, expected at least 1", count)
	}

	// Second call should use cache (verify no panic and same result).
	counts2 := d.GetCounts(context.Background())
	if counts2["example.com/widgets"] != count {
		t.Errorf("cached count = %d, expected %d", counts2["example.com/widgets"], count)
	}
}

func TestGetCounts_CacheExpiry(t *testing.T) {
	d := newTestCRDDiscovery(map[string]*CRDInfo{})

	// Manually set stale cache.
	d.countCache["example.com/things"] = 99
	d.countUpdated = time.Now().Add(-2 * countCacheTTL)

	// No CRDs registered, so refresh produces empty map.
	counts := d.GetCounts(context.Background())
	if len(counts) != 0 {
		t.Errorf("expected empty counts after cache expiry with no CRDs, got %v", counts)
	}
}

func TestCoreAPIDenylist_Comprehensive(t *testing.T) {
	// Verify all expected groups are in the denylist.
	expectedDenied := []string{
		"", "apps", "batch", "autoscaling", "networking.k8s.io",
		"rbac.authorization.k8s.io", "storage.k8s.io", "apiextensions.k8s.io",
		"admissionregistration.k8s.io", "coordination.k8s.io", "discovery.k8s.io",
		"events.k8s.io", "flowcontrol.apiserver.k8s.io", "node.k8s.io",
		"policy", "scheduling.k8s.io", "certificates.k8s.io",
		"authentication.k8s.io", "authorization.k8s.io", "apiregistration.k8s.io",
		"resource.k8s.io", "internal.apiserver.k8s.io", "storagemigration.k8s.io",
	}

	for _, group := range expectedDenied {
		if !coreAPIDenylist[group] {
			t.Errorf("group %q should be in denylist but is not", group)
		}
	}

	// Verify known CRD groups are NOT in the denylist.
	allowedGroups := []string{
		"cert-manager.io", "cilium.io", "monitoring.coreos.com",
		"argoproj.io", "istio.io", "kustomize.toolkit.fluxcd.io",
	}
	for _, group := range allowedGroups {
		if coreAPIDenylist[group] {
			t.Errorf("group %q should NOT be in denylist but is", group)
		}
	}
}
