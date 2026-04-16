package resources

import (
	"context"
	"testing"
)

func TestPilotAdaptersRegistered(t *testing.T) {
	for _, kind := range []string{"configmaps", "secrets", "namespaces", "serviceaccounts", "endpoints"} {
		if a := GetAdapter(kind); a == nil {
			t.Errorf("adapter not registered for kind %q", kind)
		}
	}
}

func TestPilotAdapterMetadata(t *testing.T) {
	tests := []struct {
		kind         string
		apiResource  string
		displayName  string
		clusterScope bool
	}{
		{"configmaps", "configmaps", "ConfigMap", false},
		{"secrets", "secrets", "Secret", false},
		{"namespaces", "namespaces", "Namespace", true},
		{"serviceaccounts", "serviceaccounts", "ServiceAccount", false},
		{"endpoints", "endpoints", "Endpoints", false},
	}
	for _, tt := range tests {
		a := GetAdapter(tt.kind)
		if a == nil {
			t.Fatalf("adapter not registered for kind %q", tt.kind)
		}
		if got := a.APIResource(); got != tt.apiResource {
			t.Errorf("%s: APIResource() = %q, want %q", tt.kind, got, tt.apiResource)
		}
		if got := a.DisplayName(); got != tt.displayName {
			t.Errorf("%s: DisplayName() = %q, want %q", tt.kind, got, tt.displayName)
		}
		if got := a.ClusterScoped(); got != tt.clusterScope {
			t.Errorf("%s: ClusterScoped() = %v, want %v", tt.kind, got, tt.clusterScope)
		}
	}
}

func TestReadOnlyAdaptersRejectWrites(t *testing.T) {
	for _, kind := range []string{"serviceaccounts", "endpoints"} {
		a := GetAdapter(kind)
		if a == nil {
			t.Fatalf("adapter not registered for kind %q", kind)
		}
		ctx := context.Background()
		if _, err := a.Create(ctx, nil, "default", []byte(`{}`)); err == nil {
			t.Errorf("%s: Create should return error for read-only adapter", kind)
		}
		if _, err := a.Update(ctx, nil, "default", "test", []byte(`{}`)); err == nil {
			t.Errorf("%s: Update should return error for read-only adapter", kind)
		}
		if err := a.Delete(ctx, nil, "default", "test"); err == nil {
			t.Errorf("%s: Delete should return error for read-only adapter", kind)
		}
	}
}

func TestAllAdaptersRegistered(t *testing.T) {
	expected := []string{
		// Pilot (5)
		"configmaps", "secrets", "namespaces", "serviceaccounts", "endpoints",
		// Bulk CRUD (5)
		"services", "ingresses", "networkpolicies", "pvcs", "pvs",
		// Bulk with actions (5)
		"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs",
		// Bulk read-only/simple (5)
		"replicasets", "storageclasses", "hpas", "pdbs", "limitranges",
		// RBAC + remaining (8)
		"roles", "clusterroles", "rolebindings", "clusterrolebindings",
		"events", "endpointslices", "resourcequotas",
		"validatingwebhookconfigurations", "mutatingwebhookconfigurations",
		// Former custom-only handlers (2)
		"pods", "nodes",
	}
	for _, kind := range expected {
		if a := GetAdapter(kind); a == nil {
			t.Errorf("adapter not registered for kind %q", kind)
		}
	}
	// Reverse check: no registered adapter is missing from expected list
	expectedSet := make(map[string]bool, len(expected))
	for _, k := range expected {
		expectedSet[k] = true
	}
	for _, kind := range RegisteredKinds() {
		if !expectedSet[kind] {
			t.Errorf("registered adapter %q is not in expected list — update test", kind)
		}
	}
	if len(expected) != len(RegisteredKinds()) {
		t.Errorf("expected %d adapters, got %d registered", len(expected), len(RegisteredKinds()))
	}
}

func TestCapabilityInterfaces(t *testing.T) {
	// Scalable
	for _, kind := range []string{"deployments", "statefulsets"} {
		a := GetAdapter(kind)
		if _, ok := a.(Scalable); !ok {
			t.Errorf("%s should implement Scalable", kind)
		}
	}
	// Restartable
	for _, kind := range []string{"deployments", "statefulsets", "daemonsets"} {
		a := GetAdapter(kind)
		if _, ok := a.(Restartable); !ok {
			t.Errorf("%s should implement Restartable", kind)
		}
	}
	// Suspendable
	for _, kind := range []string{"jobs", "cronjobs"} {
		a := GetAdapter(kind)
		if _, ok := a.(Suspendable); !ok {
			t.Errorf("%s should implement Suspendable", kind)
		}
	}
	// Triggerable
	a := GetAdapter("cronjobs")
	if _, ok := a.(Triggerable); !ok {
		t.Error("cronjobs should implement Triggerable")
	}
	// Rollbackable
	a = GetAdapter("deployments")
	if _, ok := a.(Rollbackable); !ok {
		t.Error("deployments should implement Rollbackable")
	}
}

func TestNamespaceAdapterRejectsUpdate(t *testing.T) {
	a := GetAdapter("namespaces")
	if a == nil {
		t.Fatal("adapter not registered for kind namespaces")
	}
	if _, err := a.Update(context.Background(), nil, "", "test", []byte(`{}`)); err == nil {
		t.Error("namespaces: Update should return error")
	}
}
