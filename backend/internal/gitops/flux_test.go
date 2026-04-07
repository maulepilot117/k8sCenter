package gitops

import (
	"context"
	"strconv"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestMapFluxConditions_ReadyTrue(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "True", "message": "Applied revision: main@sha1:abc123"},
	}

	sync, health, msg := mapFluxConditions(conditions)

	if sync != SyncSynced {
		t.Errorf("expected sync=%s, got %s", SyncSynced, sync)
	}
	if health != HealthHealthy {
		t.Errorf("expected health=%s, got %s", HealthHealthy, health)
	}
	if msg != "Applied revision: main@sha1:abc123" {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestMapFluxConditions_ReadyFalse_Failed(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "False", "reason": "ReconciliationFailed", "message": "kustomize build failed"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncFailed {
		t.Errorf("expected sync=%s, got %s", SyncFailed, sync)
	}
	if health != HealthDegraded {
		t.Errorf("expected health=%s, got %s", HealthDegraded, health)
	}
}

func TestMapFluxConditions_ReadyFalse_OutOfSync(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "False", "reason": "ProgressingWithRetry", "message": "retrying"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncOutOfSync {
		t.Errorf("expected sync=%s, got %s", SyncOutOfSync, sync)
	}
	if health != HealthDegraded {
		t.Errorf("expected health=%s, got %s", HealthDegraded, health)
	}
}

func TestMapFluxConditions_Reconciling(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "Unknown", "message": "reconciliation in progress"},
		{"type": "Reconciling", "status": "True"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncProgressing {
		t.Errorf("expected sync=%s, got %s", SyncProgressing, sync)
	}
	if health != HealthProgressing {
		t.Errorf("expected health=%s, got %s", HealthProgressing, health)
	}
}

func TestMapFluxConditions_Stalled(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "False", "reason": "ReconciliationFailed", "message": "dependency not ready"},
		{"type": "Stalled", "status": "True"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncStalled {
		t.Errorf("expected sync=%s, got %s", SyncStalled, sync)
	}
	if health != HealthDegraded {
		t.Errorf("expected health=%s, got %s", HealthDegraded, health)
	}
}

func TestMapFluxConditions_HealthCheckFailed(t *testing.T) {
	conditions := []map[string]string{
		{"type": "Ready", "status": "True", "message": "Applied revision"},
		{"type": "HealthCheckFailed", "status": "True"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncSynced {
		t.Errorf("expected sync=%s, got %s", SyncSynced, sync)
	}
	if health != HealthDegraded {
		t.Errorf("expected health=%s, got %s", HealthDegraded, health)
	}
}

func TestMapFluxConditions_Empty(t *testing.T) {
	sync, health, msg := mapFluxConditions(nil)

	if sync != SyncUnknown {
		t.Errorf("expected sync=%s, got %s", SyncUnknown, sync)
	}
	if health != HealthUnknown {
		t.Errorf("expected health=%s, got %s", HealthUnknown, health)
	}
	if msg != "" {
		t.Errorf("expected empty message, got %s", msg)
	}
}

func TestMapFluxConditions_StalledOverridesReconciling(t *testing.T) {
	// Both Stalled and Reconciling are true; Stalled should win.
	conditions := []map[string]string{
		{"type": "Ready", "status": "False", "reason": "SomeReason", "message": "stuck"},
		{"type": "Reconciling", "status": "True"},
		{"type": "Stalled", "status": "True"},
	}

	sync, health, _ := mapFluxConditions(conditions)

	if sync != SyncStalled {
		t.Errorf("expected sync=%s, got %s", SyncStalled, sync)
	}
	if health != HealthDegraded {
		t.Errorf("expected health=%s, got %s", HealthDegraded, health)
	}
}

func newFluxFakeDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"},
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "KustomizationList"},
		&unstructured.UnstructuredList{},
	)
	return dynamicfake.NewSimpleDynamicClient(scheme, objects...)
}

func TestReconcileFluxResource_Suspended(t *testing.T) {
	ks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
			"kind":       "Kustomization",
			"metadata": map[string]interface{}{
				"name":      "test-ks",
				"namespace": "flux-system",
			},
			"spec": map[string]interface{}{
				"suspend": true,
			},
		},
	}

	client := newFluxFakeDynClient(ks)
	_, err := ReconcileFluxResource(context.Background(), client, fluxKustomizationGVR, "flux-system", "test-ks")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "is suspended") {
		t.Errorf("expected error containing 'is suspended', got: %v", err)
	}
}

func TestReconcileFluxResource_SetsAnnotation(t *testing.T) {
	ks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
			"kind":       "Kustomization",
			"metadata": map[string]interface{}{
				"name":      "test-ks",
				"namespace": "flux-system",
			},
			"spec": map[string]interface{}{
				"suspend": false,
			},
		},
	}

	client := newFluxFakeDynClient(ks)
	_, err := ReconcileFluxResource(context.Background(), client, fluxKustomizationGVR, "flux-system", "test-ks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fetch the patched object
	patched, err := client.Resource(fluxKustomizationGVR).Namespace("flux-system").Get(context.Background(), "test-ks", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}

	annotations := patched.GetAnnotations()
	val, ok := annotations["reconcile.fluxcd.io/requestedAt"]
	if !ok || val == "" {
		t.Fatal("expected annotation reconcile.fluxcd.io/requestedAt to be set")
	}

	// Verify it's a numeric string (Unix timestamp)
	if _, err := strconv.ParseInt(val, 10, 64); err != nil {
		t.Errorf("expected numeric string for requestedAt annotation, got: %s", val)
	}
}

func TestSuspendFluxResource(t *testing.T) {
	ks := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
			"kind":       "Kustomization",
			"metadata": map[string]interface{}{
				"name":      "test-ks",
				"namespace": "flux-system",
			},
			"spec": map[string]interface{}{
				"suspend": false,
			},
		},
	}

	client := newFluxFakeDynClient(ks)
	_, err := SuspendFluxResource(context.Background(), client, fluxKustomizationGVR, "flux-system", "test-ks", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fetch the patched object
	patched, err := client.Resource(fluxKustomizationGVR).Namespace("flux-system").Get(context.Background(), "test-ks", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get patched object: %v", err)
	}

	suspended, found, _ := unstructured.NestedBool(patched.Object, "spec", "suspend")
	if !found {
		t.Fatal("expected spec.suspend to be present")
	}
	if !suspended {
		t.Errorf("expected spec.suspend=true, got %v", suspended)
	}
}
