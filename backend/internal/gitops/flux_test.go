package gitops

import (
	"testing"
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
