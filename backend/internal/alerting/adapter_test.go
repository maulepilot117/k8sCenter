package alerting

import (
	"context"
	"testing"
)

// seedStore records a firing AlertEvent into s and panics on error.
func seedStore(s *MemoryStore, events ...AlertEvent) {
	for _, e := range events {
		if err := s.Record(context.Background(), e); err != nil {
			panic(err)
		}
	}
}

func TestActiveAlertCountsExcluding(t *testing.T) {
	tests := []struct {
		name         string
		events       []AlertEvent
		exclude      []string
		wantActive   int
		wantCritical int
	}{
		{
			name: "watchdog plus one critical - watchdog excluded",
			events: []AlertEvent{
				{Fingerprint: "fp-watchdog", Status: "firing", AlertName: "Watchdog", Severity: "none"},
				{Fingerprint: "fp-crit", Status: "firing", AlertName: "NodeNotReady", Severity: "critical"},
			},
			exclude:      []string{"Watchdog"},
			wantActive:   1,
			wantCritical: 1,
		},
		{
			name: "DeadMansSwitch excluded when passed",
			events: []AlertEvent{
				{Fingerprint: "fp-dms", Status: "firing", AlertName: "DeadMansSwitch", Severity: "none"},
				{Fingerprint: "fp-warn", Status: "firing", AlertName: "HighMemory", Severity: "warning"},
			},
			exclude:      []string{"DeadMansSwitch"},
			wantActive:   1,
			wantCritical: 0,
		},
		{
			name: "no exclusions - same result as ActiveAlertCounts",
			events: []AlertEvent{
				{Fingerprint: "fp1", Status: "firing", AlertName: "Watchdog", Severity: "none"},
				{Fingerprint: "fp2", Status: "firing", AlertName: "NodeNotReady", Severity: "critical"},
				{Fingerprint: "fp3", Status: "firing", AlertName: "HighMemory", Severity: "warning"},
			},
			exclude:      nil,
			wantActive:   3,
			wantCritical: 1,
		},
		{
			name:         "empty store returns zero counts without error",
			events:       nil,
			exclude:      []string{"Watchdog", "DeadMansSwitch"},
			wantActive:   0,
			wantCritical: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			if len(tc.events) > 0 {
				seedStore(store, tc.events...)
			}
			adapter := &AlertCountAdapter{Store: store}

			gotActive, gotCritical, err := adapter.ActiveAlertCountsExcluding(context.Background(), tc.exclude...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotActive != tc.wantActive {
				t.Errorf("active: got %d, want %d", gotActive, tc.wantActive)
			}
			if gotCritical != tc.wantCritical {
				t.Errorf("critical: got %d, want %d", gotCritical, tc.wantCritical)
			}
		})
	}
}

// TestActiveAlertCountsExcluding_MatchesActiveAlertCounts verifies that passing
// no exclusions produces the same result as the unfiltered ActiveAlertCounts method.
func TestActiveAlertCountsExcluding_MatchesActiveAlertCounts(t *testing.T) {
	store := NewMemoryStore()
	seedStore(store,
		AlertEvent{Fingerprint: "fp1", Status: "firing", AlertName: "Watchdog", Severity: "none"},
		AlertEvent{Fingerprint: "fp2", Status: "firing", AlertName: "NodeNotReady", Severity: "critical"},
		AlertEvent{Fingerprint: "fp3", Status: "firing", AlertName: "HighMemory", Severity: "warning"},
	)
	adapter := &AlertCountAdapter{Store: store}

	baseActive, baseCritical, err := adapter.ActiveAlertCounts(context.Background())
	if err != nil {
		t.Fatalf("ActiveAlertCounts error: %v", err)
	}

	exclActive, exclCritical, err := adapter.ActiveAlertCountsExcluding(context.Background())
	if err != nil {
		t.Fatalf("ActiveAlertCountsExcluding error: %v", err)
	}

	if baseActive != exclActive || baseCritical != exclCritical {
		t.Errorf("mismatch: ActiveAlertCounts=(%d,%d), ActiveAlertCountsExcluding(no args)=(%d,%d)",
			baseActive, baseCritical, exclActive, exclCritical)
	}
}
