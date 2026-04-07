package gitops

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestMapArgoSyncStatus(t *testing.T) {
	tests := []struct {
		input string
		want  SyncStatus
	}{
		{"Synced", SyncSynced},
		{"OutOfSync", SyncOutOfSync},
		{"Unknown", SyncUnknown},
		{"", SyncUnknown},
		{"SomethingElse", SyncUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapArgoSyncStatus(tt.input)
			if got != tt.want {
				t.Errorf("mapArgoSyncStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapArgoHealthStatus(t *testing.T) {
	tests := []struct {
		input string
		want  HealthStatus
	}{
		{"Healthy", HealthHealthy},
		{"Degraded", HealthDegraded},
		{"Progressing", HealthProgressing},
		{"Suspended", HealthSuspended},
		{"Missing", HealthDegraded},
		{"", HealthUnknown},
		{"SomethingElse", HealthUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapArgoHealthStatus(tt.input)
			if got != tt.want {
				t.Errorf("mapArgoHealthStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func newArgoFakeDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"},
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "ApplicationList"},
		&unstructured.UnstructuredList{},
	)
	return dynamicfake.NewSimpleDynamicClient(scheme, objects...)
}

func TestSyncArgoApp_InProgress(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
			},
			"spec": map[string]interface{}{},
			"status": map[string]interface{}{
				"operationState": map[string]interface{}{
					"phase": "Running",
				},
			},
		},
	}

	client := newArgoFakeDynClient(app)
	_, err := SyncArgoApp(context.Background(), client, "argocd", "test-app", "admin")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected error containing 'already in progress', got: %v", err)
	}
}

func TestRollbackArgoApp_AutoSyncBlocks(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
			},
			"spec": map[string]interface{}{
				"syncPolicy": map[string]interface{}{
					"automated": map[string]interface{}{
						"prune": true,
					},
				},
			},
			"status": map[string]interface{}{},
		},
	}

	client := newArgoFakeDynClient(app)
	_, err := RollbackArgoApp(context.Background(), client, "argocd", "test-app", "abc123", "admin")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "auto-sync is enabled") {
		t.Errorf("expected error containing 'auto-sync is enabled', got: %v", err)
	}
}

func TestRollbackArgoApp_RevisionNotFound(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
			},
			"spec": map[string]interface{}{},
			"status": map[string]interface{}{
				"history": []interface{}{
					map[string]interface{}{"revision": "abc", "deployedAt": "2026-01-01T00:00:00Z"},
					map[string]interface{}{"revision": "def", "deployedAt": "2026-01-02T00:00:00Z"},
				},
			},
		},
	}

	client := newArgoFakeDynClient(app)
	_, err := RollbackArgoApp(context.Background(), client, "argocd", "test-app", "xyz", "admin")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found in history") {
		t.Errorf("expected error containing 'not found in history', got: %v", err)
	}
}

func TestSuspendArgoApp_StashesPolicy(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
			},
			"spec": map[string]interface{}{
				"syncPolicy": map[string]interface{}{
					"automated": map[string]interface{}{
						"prune":    true,
						"selfHeal": false,
					},
				},
			},
			"status": map[string]interface{}{},
		},
	}

	client := newArgoFakeDynClient(app)
	_, err := SuspendArgoApp(context.Background(), client, "argocd", "test-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fetch the patched object
	patched, err := client.Resource(argoApplicationGVR).Namespace("argocd").Get(context.Background(), "test-app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}

	// Check annotation exists
	annotations := patched.GetAnnotations()
	saved, ok := annotations["kubecenter.io/pre-suspend-sync-policy"]
	if !ok || saved == "" {
		t.Fatal("expected annotation kubecenter.io/pre-suspend-sync-policy to be set")
	}

	// Check automated is removed (nil in merge patch = removed)
	automated, found, _ := unstructured.NestedMap(patched.Object, "spec", "syncPolicy", "automated")
	if found && automated != nil {
		t.Errorf("expected spec.syncPolicy.automated to be nil/removed, got: %v", automated)
	}
}

func TestResumeArgoApp_RestoresPolicy(t *testing.T) {
	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
				"annotations": map[string]interface{}{
					"kubecenter.io/pre-suspend-sync-policy": `{"prune":true,"selfHeal":false}`,
				},
			},
			"spec": map[string]interface{}{
				"syncPolicy": map[string]interface{}{},
			},
			"status": map[string]interface{}{},
		},
	}

	client := newArgoFakeDynClient(app)
	_, err := ResumeArgoApp(context.Background(), client, "argocd", "test-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fetch the patched object
	patched, err := client.Resource(argoApplicationGVR).Namespace("argocd").Get(context.Background(), "test-app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}

	// Check automated sync policy is restored
	automated, found, _ := unstructured.NestedMap(patched.Object, "spec", "syncPolicy", "automated")
	if !found || automated == nil {
		t.Fatal("expected spec.syncPolicy.automated to be restored")
	}

	prune, _, _ := unstructured.NestedBool(automated, "prune")
	if !prune {
		t.Errorf("expected prune=true, got %v", prune)
	}

	selfHeal, _, _ := unstructured.NestedBool(automated, "selfHeal")
	if selfHeal {
		t.Errorf("expected selfHeal=false, got %v", selfHeal)
	}

	// Check annotation is removed (nil in merge patch)
	annotations := patched.GetAnnotations()
	if val, ok := annotations["kubecenter.io/pre-suspend-sync-policy"]; ok && val != "" {
		t.Errorf("expected annotation to be removed, got: %s", val)
	}
}
