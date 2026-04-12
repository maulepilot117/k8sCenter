package certmanager

import (
	"testing"
	"time"
)

func intPtr(i int) *int { return &i }

// TestThresholdBucket verifies thresholdBucket maps day counts to the correct bucket.
func TestThresholdBucket(t *testing.T) {
	cases := []struct {
		days int
		want threshold
	}{
		{60, thresholdNone},
		{30, thresholdWarning},
		{29, thresholdWarning},
		{8, thresholdWarning},
		{7, thresholdCritical},
		{0, thresholdCritical},
		{-1, thresholdExpired},
	}

	for _, tc := range cases {
		got := thresholdBucket(tc.days)
		if got != tc.want {
			t.Errorf("thresholdBucket(%d) = %v, want %v", tc.days, got, tc.want)
		}
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
