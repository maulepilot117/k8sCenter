package notifications

import (
	"strings"
	"testing"
	"time"
)

func TestRuleMatches(t *testing.T) {
	tests := []struct {
		name     string
		rule     Rule
		notif    Notification
		expected bool
	}{
		{
			name:     "empty filters match anything",
			rule:     Rule{},
			notif:    Notification{Source: SourceAlert, Severity: SeverityCritical},
			expected: true,
		},
		{
			name: "source match",
			rule: Rule{
				SourceFilter: []Source{SourceAlert, SourcePolicy},
			},
			notif:    Notification{Source: SourceAlert, Severity: SeverityWarning},
			expected: true,
		},
		{
			name: "source no match",
			rule: Rule{
				SourceFilter: []Source{SourceAlert},
			},
			notif:    Notification{Source: SourceGitOps, Severity: SeverityWarning},
			expected: false,
		},
		{
			name: "severity match",
			rule: Rule{
				SeverityFilter: []Severity{SeverityCritical},
			},
			notif:    Notification{Source: SourceAlert, Severity: SeverityCritical},
			expected: true,
		},
		{
			name: "severity no match",
			rule: Rule{
				SeverityFilter: []Severity{SeverityCritical},
			},
			notif:    Notification{Source: SourceAlert, Severity: SeverityInfo},
			expected: false,
		},
		{
			name: "both filters match",
			rule: Rule{
				SourceFilter:   []Source{SourceAlert},
				SeverityFilter: []Severity{SeverityCritical, SeverityWarning},
			},
			notif:    Notification{Source: SourceAlert, Severity: SeverityWarning},
			expected: true,
		},
		{
			name: "source matches but severity doesn't",
			rule: Rule{
				SourceFilter:   []Source{SourceAlert},
				SeverityFilter: []Severity{SeverityCritical},
			},
			notif:    Notification{Source: SourceAlert, Severity: SeverityInfo},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ruleMatches(tt.rule, tt.notif)
			if got != tt.expected {
				t.Errorf("ruleMatches() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestSeverityColor(t *testing.T) {
	tests := []struct {
		severity Severity
		want     string
	}{
		{SeverityCritical, ":red_circle:"},
		{SeverityWarning, ":large_orange_circle:"},
		{SeverityInfo, ":large_blue_circle:"},
	}
	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			if got := severityColor(tt.severity); got != tt.want {
				t.Errorf("severityColor(%s) = %q, want %q", tt.severity, got, tt.want)
			}
		})
	}
}

func TestChannelRecipients(t *testing.T) {
	tests := []struct {
		name string
		ch   Channel
		want []string
	}{
		{
			name: "no recipients",
			ch:   Channel{Config: ChannelConfig{}},
			want: nil,
		},
		{
			name: "string slice",
			ch: Channel{
				Config: ChannelConfig{"recipients": []string{"a@x.com", "b@x.com"}},
			},
			want: []string{"a@x.com", "b@x.com"},
		},
		{
			name: "any slice (from JSON unmarshal)",
			ch: Channel{
				Config: ChannelConfig{"recipients": []any{"a@x.com", "b@x.com"}},
			},
			want: []string{"a@x.com", "b@x.com"},
		},
		{
			name: "filters empty strings from any slice",
			ch: Channel{
				Config: ChannelConfig{"recipients": []any{"a@x.com", "", "b@x.com"}},
			},
			want: []string{"a@x.com", "b@x.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := channelRecipients(tt.ch)
			if len(got) != len(tt.want) {
				t.Errorf("channelRecipients() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("channelRecipients()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNextDigestTime(t *testing.T) {
	now := time.Date(2026, 4, 10, 5, 0, 0, 0, time.UTC)
	next := nextDigestTime(now)
	if !next.After(now) {
		t.Errorf("nextDigestTime should return a time after now, got %v vs %v", next, now)
	}
	if next.Hour() != 8 || next.Minute() != 0 {
		t.Errorf("nextDigestTime should fire at 08:00 UTC, got %02d:%02d", next.Hour(), next.Minute())
	}

	// If now is past 8am, next should be tomorrow
	pastEight := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	nextDay := nextDigestTime(pastEight)
	if nextDay.Day() != 11 {
		t.Errorf("nextDigestTime past 8am should be tomorrow, got day %d", nextDay.Day())
	}
}

func TestRenderDigestEmail(t *testing.T) {
	notifications := []Notification{
		{
			Source:    SourceAlert,
			Severity:  SeverityCritical,
			Title:     "High memory usage",
			Message:   "Pod nginx exceeded 90% memory",
			CreatedAt: time.Now().UTC(),
		},
	}
	html, err := renderDigestEmail(notifications)
	if err != nil {
		t.Fatalf("renderDigestEmail failed: %v", err)
	}
	if html == "" {
		t.Error("renderDigestEmail returned empty string")
	}
	if !strings.Contains(html, "High memory usage") {
		t.Error("digest HTML should contain notification title")
	}
	if !strings.Contains(html, "Notification Digest") {
		t.Error("digest HTML should contain header")
	}
}
