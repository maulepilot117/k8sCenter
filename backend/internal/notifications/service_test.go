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

func TestRenderDigestEmailEscapesHTML(t *testing.T) {
	// Verify html/template escapes malicious input (XSS protection)
	notifications := []Notification{
		{
			Source:    SourceAlert,
			Severity:  SeverityCritical,
			Title:     "<script>alert('xss')</script>",
			Message:   "<img src=x onerror=alert(1)>",
			CreatedAt: time.Now().UTC(),
		},
	}
	html, err := renderDigestEmail(notifications)
	if err != nil {
		t.Fatalf("renderDigestEmail failed: %v", err)
	}
	// html/template should escape the angle brackets so the browser sees them
	// as text, not HTML elements. We don't strip "onerror=" — we just ensure
	// the < > characters are escaped so the tag never executes.
	if strings.Contains(html, "<script>") {
		t.Error("digest HTML should escape <script> tags")
	}
	if strings.Contains(html, "<img src=x") {
		t.Error("digest HTML should escape <img> tags")
	}
	// Should contain escaped versions
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("digest HTML should contain HTML-escaped script tags")
	}
}

func TestBlockedHeaders(t *testing.T) {
	// Verify all security-sensitive headers are blocked
	required := []string{
		"host",
		"authorization",
		"cookie",
		"x-signature-256",
		"x-forwarded-for",
		"x-forwarded-host",
		"x-forwarded-proto",
		"content-type",
		"user-agent",
	}
	for _, h := range required {
		if !blockedHeaders[h] {
			t.Errorf("blockedHeaders should contain %q", h)
		}
	}
}

func TestSeverityColorFallback(t *testing.T) {
	// Unknown severity should fall back to info color
	if got := severityColor(Severity("unknown")); got != ":large_blue_circle:" {
		t.Errorf("severityColor for unknown should fall back to info color, got %q", got)
	}
	if got := severityColor(""); got != ":large_blue_circle:" {
		t.Errorf("severityColor for empty should fall back to info color, got %q", got)
	}
}

func TestNextDigestTimeBoundary(t *testing.T) {
	// Exactly at 08:00:00 — should advance to tomorrow (uses !t.After(now))
	exactly := time.Date(2026, 4, 10, 8, 0, 0, 0, time.UTC)
	next := nextDigestTime(exactly)
	if next.Day() != 11 {
		t.Errorf("nextDigestTime at exactly 08:00 should advance to tomorrow, got day %d", next.Day())
	}
}
