package certmanager

import (
	"testing"
	"time"
)

func intPtr(i int) *int { return &i }

// TestThresholdBucket verifies thresholdBucket maps day counts to the
// correct bucket using the cert's resolved per-cert thresholds.
func TestThresholdBucket(t *testing.T) {
	cases := []struct {
		name string
		cert Certificate
		want threshold
	}{
		// Default thresholds (warn=30, crit=7) — cert has zero values,
		// thresholdBucket falls back to the package defaults.
		{"default-runway-60d", Certificate{DaysRemaining: intPtr(60)}, thresholdNone},
		{"default-warn-30d", Certificate{DaysRemaining: intPtr(30)}, thresholdWarning},
		{"default-warn-8d", Certificate{DaysRemaining: intPtr(8)}, thresholdWarning},
		{"default-crit-7d", Certificate{DaysRemaining: intPtr(7)}, thresholdCritical},
		{"default-crit-0d", Certificate{DaysRemaining: intPtr(0)}, thresholdCritical},
		{"default-expired", Certificate{DaysRemaining: intPtr(-1)}, thresholdExpired},
		// Per-cert overrides — ACME-style tight warn means 20d is still
		// in the safe zone.
		{"cert-warn-14d-20d-remaining", Certificate{DaysRemaining: intPtr(20), WarningThresholdDays: 14, CriticalThresholdDays: 3}, thresholdNone},
		{"cert-warn-14d-13d-remaining", Certificate{DaysRemaining: intPtr(13), WarningThresholdDays: 14, CriticalThresholdDays: 3}, thresholdWarning},
		{"cert-crit-3d-3d-remaining", Certificate{DaysRemaining: intPtr(3), WarningThresholdDays: 14, CriticalThresholdDays: 3}, thresholdCritical},
		// Per-cert override — long-runway internal CA wants 60d warning.
		{"cert-warn-60d-50d-remaining", Certificate{DaysRemaining: intPtr(50), WarningThresholdDays: 60, CriticalThresholdDays: 14}, thresholdWarning},
		// Nil DaysRemaining — bucket is None (treated as healthy /
		// indeterminate).
		{"nil-days-remaining", Certificate{DaysRemaining: nil, WarningThresholdDays: 30, CriticalThresholdDays: 7}, thresholdNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := thresholdBucket(tc.cert)
			if got != tc.want {
				t.Errorf("thresholdBucket(%+v) = %v, want %v", tc.cert, got, tc.want)
			}
		})
	}
}

// TestDedupeEmitOncePerCrossing tests the full state machine for deduplication.
func TestDedupeEmitOncePerCrossing(t *testing.T) {
	p := newPollerForTest()

	now := time.Now()
	cert := Certificate{
		UID:           "test-uid-1",
		Name:          "my-cert",
		Namespace:     "default",
		NotAfter:      ptrTime(now.Add(25 * 24 * time.Hour)),
		DaysRemaining: intPtr(25),
	}

	// Step 1: Fresh poller, cert at 25d (warning) → emits 1 record with severity "warning"
	records := p.check(cert)
	if len(records) != 1 {
		t.Fatalf("step1: expected 1 record, got %d", len(records))
	}
	if records[0].Severity != "warning" {
		t.Errorf("step1: expected severity 'warning', got %q", records[0].Severity)
	}

	// Step 2: Same cert, same bucket next tick → emits 0 records
	records = p.check(cert)
	if len(records) != 0 {
		t.Fatalf("step2: expected 0 records, got %d", len(records))
	}

	// Step 3: Cert crosses to 5d (critical) → emits 1 record with severity "critical"
	cert.DaysRemaining = intPtr(5)
	cert.NotAfter = ptrTime(now.Add(5 * 24 * time.Hour))
	records = p.check(cert)
	if len(records) != 1 {
		t.Fatalf("step3: expected 1 record, got %d", len(records))
	}
	if records[0].Severity != "critical" {
		t.Errorf("step3: expected severity 'critical', got %q", records[0].Severity)
	}

	// Step 4: Cert renews, advances to 60d (none) → emits 0, entry cleared
	cert.DaysRemaining = intPtr(60)
	cert.NotAfter = ptrTime(now.Add(60 * 24 * time.Hour))
	records = p.check(cert)
	if len(records) != 0 {
		t.Fatalf("step4: expected 0 records after renewal, got %d", len(records))
	}
	// Confirm entry was cleared from dedupe map
	p.mu.Lock()
	_, exists := p.dedupe[cert.UID]
	p.mu.Unlock()
	if exists {
		t.Error("step4: expected dedupe entry to be cleared after renewal to thresholdNone")
	}

	// Step 5: Cert re-degrades to 20d (warning) → emits 1 (re-emit because entry was cleared)
	cert.DaysRemaining = intPtr(20)
	cert.NotAfter = ptrTime(now.Add(20 * 24 * time.Hour))
	records = p.check(cert)
	if len(records) != 1 {
		t.Fatalf("step5: expected 1 record after re-degradation, got %d", len(records))
	}
	if records[0].Severity != "warning" {
		t.Errorf("step5: expected severity 'warning', got %q", records[0].Severity)
	}
}

// TestDedupeRestartsFromEmpty pins restart behavior: fresh poller always emits for degraded certs.
func TestDedupeRestartsFromEmpty(t *testing.T) {
	p := newPollerForTest()

	now := time.Now()
	cert := Certificate{
		UID:           "restart-uid-1",
		Name:          "tls-cert",
		Namespace:     "prod",
		NotAfter:      ptrTime(now.Add(5 * 24 * time.Hour)),
		DaysRemaining: intPtr(5),
	}

	// Fresh Poller (empty map), cert at 5d critical → emits 1 critical
	records := p.check(cert)
	if len(records) != 1 {
		t.Fatalf("restart test: expected 1 record, got %d", len(records))
	}
	if records[0].Severity != "critical" {
		t.Errorf("restart test: expected severity 'critical', got %q", records[0].Severity)
	}
	if records[0].Threshold != thresholdCritical {
		t.Errorf("restart test: expected threshold %v, got %v", thresholdCritical, records[0].Threshold)
	}
}
