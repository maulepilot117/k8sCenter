package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/alerting"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/websocket"
)

const (
	dedupWindow    = 15 * time.Minute
	queueSize      = 1000
	semaphoreSize  = 20
	dispatchTimeout = 10 * time.Second
)

// NotificationService is the core notification center.
// It persists notifications, broadcasts via WebSocket, and dispatches to external channels.
type NotificationService struct {
	store    *Store
	hub      *websocket.Hub
	notifier *alerting.Notifier // for email digest (existing SMTP pipeline)
	queue    chan Notification
	sem      chan struct{} // dispatch semaphore
	rules    []Rule
	channels []Channel
	mu       sync.RWMutex
	logger   *slog.Logger
}

// NewService creates a notification service.
func NewService(store *Store, hub *websocket.Hub, notifier *alerting.Notifier, logger *slog.Logger) *NotificationService {
	return &NotificationService{
		store:    store,
		hub:      hub,
		notifier: notifier,
		queue:    make(chan Notification, queueSize),
		sem:      make(chan struct{}, semaphoreSize),
		logger:   logger,
	}
}

// Start loads cached rules/channels and launches the dispatch and digest goroutines.
func (s *NotificationService) Start(ctx context.Context) {
	s.refreshCache(ctx)
	go s.runDispatcher(ctx)
	go s.runDigest(ctx)
	go s.runRetention(ctx)
}

// RefreshCache reloads rules and channels from the database.
// Called on startup and after channel/rule CRUD.
func (s *NotificationService) RefreshCache(ctx context.Context) {
	s.refreshCache(ctx)
}

func (s *NotificationService) refreshCache(ctx context.Context) {
	rules, err := s.store.ListRules(ctx)
	if err != nil {
		s.logger.Error("failed to load notification rules", "error", err)
		return
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		s.logger.Error("failed to load notification channels", "error", err)
		return
	}
	s.mu.Lock()
	s.rules = rules
	s.channels = channels
	s.mu.Unlock()
}

// Emit persists a notification, broadcasts via WebSocket, and enqueues for external dispatch.
// Safe to call from any goroutine. Non-blocking.
func (s *NotificationService) Emit(ctx context.Context, n Notification) {
	// Guard: skip audit-source to prevent circular audit → Emit → audit loop
	if n.Source == SourceAudit {
		if err := s.persistAndBroadcast(ctx, n); err != nil {
			s.logger.Error("emit notification", "error", err)
		}
		return
	}

	// Dedup: suppress if same (source, kind, ns, name, title) within 15 min
	exists, err := s.store.DedupExists(ctx, n, dedupWindow)
	if err != nil {
		s.logger.Error("dedup check failed", "error", err)
		// Continue — better to duplicate than to drop
	}
	if exists {
		return
	}

	if err := s.persistAndBroadcast(ctx, n); err != nil {
		s.logger.Error("emit notification", "error", err)
		return
	}

	// Enqueue for external dispatch (non-blocking)
	select {
	case s.queue <- n:
	default:
		s.logger.Warn("notification dispatch queue full, dropping external dispatch",
			"source", n.Source, "title", n.Title)
	}
}

func (s *NotificationService) persistAndBroadcast(ctx context.Context, n Notification) error {
	id, err := s.store.InsertNotification(ctx, n)
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	n.ID = id

	// Broadcast stripped payload via WebSocket (no resource fields to prevent namespace leakage)
	stripped := map[string]any{
		"id":       n.ID,
		"source":   n.Source,
		"severity": n.Severity,
		"title":    n.Title,
	}
	strippedJSON, _ := json.Marshal(stripped)
	s.hub.HandleEvent("ADDED", "notifications", "", n.ID, json.RawMessage(strippedJSON))
	return nil
}

// --- Dispatch goroutine ---

func (s *NotificationService) runDispatcher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-s.queue:
			s.dispatchToChannels(ctx, n)
		}
	}
}

func (s *NotificationService) dispatchToChannels(ctx context.Context, n Notification) {
	s.mu.RLock()
	rules := s.rules
	channels := s.channels
	s.mu.RUnlock()

	channelMap := make(map[string]Channel, len(channels))
	for _, ch := range channels {
		channelMap[ch.ID] = ch
	}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if !ruleMatches(rule, n) {
			continue
		}
		ch, ok := channelMap[rule.ChannelID]
		if !ok {
			continue
		}
		if ch.Type == ChannelEmail {
			continue // email is digest-only, not per-notification
		}

		// Acquire semaphore, dispatch in goroutine
		s.sem <- struct{}{}
		go func(ch Channel, n Notification) {
			defer func() { <-s.sem }()
			dctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
			defer cancel()
			if err := s.dispatch(dctx, ch, n); err != nil {
				s.logger.Error("dispatch failed",
					"channel", ch.Name, "type", ch.Type, "error", err)
				_ = s.store.UpdateChannelError(dctx, ch.ID, err.Error())
			}
		}(ch, n)
	}
}

func ruleMatches(rule Rule, n Notification) bool {
	if len(rule.SourceFilter) > 0 {
		found := false
		for _, src := range rule.SourceFilter {
			if src == n.Source {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(rule.SeverityFilter) > 0 {
		found := false
		for _, sev := range rule.SeverityFilter {
			if sev == n.Severity {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// --- Channel dispatch (switch, no interface) ---

func (s *NotificationService) dispatch(ctx context.Context, ch Channel, n Notification) error {
	switch ch.Type {
	case ChannelSlack:
		return s.sendSlack(ctx, ch, n)
	case ChannelWebhook:
		return s.sendWebhook(ctx, ch, n)
	default:
		return fmt.Errorf("unsupported channel type: %s", ch.Type)
	}
}

// TestChannel sends a test notification to a channel.
func (s *NotificationService) TestChannel(ctx context.Context, ch Channel) error {
	test := Notification{
		Source:   SourceCluster,
		Severity: SeverityInfo,
		Title:    "Test notification from k8sCenter",
		Message:  "This is a test notification to verify channel connectivity.",
	}
	switch ch.Type {
	case ChannelSlack:
		return s.sendSlack(ctx, ch, test)
	case ChannelWebhook:
		return s.sendWebhook(ctx, ch, test)
	case ChannelEmail:
		return s.sendTestEmail(ch)
	default:
		return fmt.Errorf("unsupported channel type: %s", ch.Type)
	}
}

// --- Slack dispatch ---

func (s *NotificationService) sendSlack(ctx context.Context, ch Channel, n Notification) error {
	webhookURL, _ := ch.Config["webhookUrl"].(string)
	if webhookURL == "" {
		return fmt.Errorf("slack channel %q has no webhookUrl configured", ch.Name)
	}

	color := severityColor(n.Severity)
	payload := map[string]any{
		"text": fmt.Sprintf("[%s] %s", n.Severity, n.Title),
		"blocks": []map[string]any{
			{"type": "header", "text": map[string]string{"type": "plain_text", "text": n.Title}},
			{"type": "section", "text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Severity:* %s %s\n*Source:* %s\n*Resource:* %s/%s/%s",
					color, n.Severity, n.Source, n.ResourceKind, n.ResourceNS, n.ResourceName),
			}},
		},
	}
	if n.Message != "" {
		blocks := payload["blocks"].([]map[string]any)
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": n.Message},
		})
		payload["blocks"] = blocks
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("slack error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func severityColor(sev Severity) string {
	switch sev {
	case SeverityCritical:
		return ":red_circle:"
	case SeverityWarning:
		return ":large_orange_circle:"
	default:
		return ":large_blue_circle:"
	}
}

// --- Webhook dispatch ---

func (s *NotificationService) sendWebhook(ctx context.Context, ch Channel, n Notification) error {
	webhookURL, _ := ch.Config["url"].(string)
	if webhookURL == "" {
		return fmt.Errorf("webhook channel %q has no url configured", ch.Name)
	}

	payload, _ := json.Marshal(n)

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "k8sCenter-Webhook/1.0")

	// HMAC-SHA256 signature if secret is configured
	if secret, _ := ch.Config["secret"].(string); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		req.Header.Set("X-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	// Custom headers from channel config
	if headers, ok := ch.Config["headers"].(map[string]any); ok {
		for k, v := range headers {
			if sv, ok := v.(string); ok {
				req.Header.Set(k, sv)
			}
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// --- Email digest ---

func (s *NotificationService) sendTestEmail(ch Channel) error {
	if s.notifier == nil {
		return fmt.Errorf("SMTP notifier not configured")
	}
	recipients := channelRecipients(ch)
	if len(recipients) == 0 {
		return fmt.Errorf("email channel %q has no recipients", ch.Name)
	}
	return s.notifier.QueueEmail(recipients, "[k8sCenter] Test Notification", "<p>This is a test notification from k8sCenter.</p>")
}

func (s *NotificationService) runDigest(ctx context.Context) {
	for {
		next := nextDigestTime(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			s.sendDigests(ctx)
		}
	}
}

func nextDigestTime(now time.Time) time.Time {
	// Next 08:00 UTC
	t := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

func (s *NotificationService) sendDigests(ctx context.Context) {
	if s.notifier == nil {
		return
	}

	s.mu.RLock()
	channels := s.channels
	rules := s.rules
	s.mu.RUnlock()

	for _, ch := range channels {
		if ch.Type != ChannelEmail {
			continue
		}

		recipients := channelRecipients(ch)
		if len(recipients) == 0 {
			continue
		}

		since := time.Now().Add(-24 * time.Hour) // default: last 24h
		if ch.LastSentAt != nil {
			since = *ch.LastSentAt
		}

		// Collect source/severity filters from rules targeting this channel
		var sourceFilter, sevFilter []string
		for _, r := range rules {
			if r.ChannelID != ch.ID || !r.Enabled {
				continue
			}
			for _, src := range r.SourceFilter {
				sourceFilter = append(sourceFilter, string(src))
			}
			for _, sev := range r.SeverityFilter {
				sevFilter = append(sevFilter, string(sev))
			}
		}

		notifications, err := s.store.NotificationsSince(ctx, since, nil, sourceFilter, sevFilter)
		if err != nil {
			s.logger.Error("digest query failed", "channel", ch.Name, "error", err)
			continue
		}

		// Always advance last_sent_at (even on empty digest to prevent accumulation)
		if err := s.store.UpdateChannelLastSent(ctx, ch.ID); err != nil {
			s.logger.Error("update last_sent_at", "channel", ch.Name, "error", err)
		}

		if len(notifications) == 0 {
			continue
		}

		htmlBody, err := renderDigestEmail(notifications)
		if err != nil {
			s.logger.Error("render digest", "channel", ch.Name, "error", err)
			continue
		}

		subject := fmt.Sprintf("k8sCenter Notification Digest — %s — %d notifications",
			time.Now().UTC().Format("Jan 2, 2006"), len(notifications))

		if err := s.notifier.QueueEmail(recipients, subject, htmlBody); err != nil {
			s.logger.Error("queue digest email", "channel", ch.Name, "error", err)
			_ = s.store.UpdateChannelError(ctx, ch.ID, err.Error())
		}
	}
}

func channelRecipients(ch Channel) []string {
	raw, ok := ch.Config["recipients"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

var digestTemplate = template.Must(template.New("digest").Funcs(template.FuncMap{
	"severityEmoji": func(sev Severity) string {
		switch sev {
		case SeverityCritical:
			return "🔴"
		case SeverityWarning:
			return "🟡"
		default:
			return "🔵"
		}
	},
}).Parse(`<!DOCTYPE html>
<html><body>
<table width="600" style="margin:0 auto;font-family:sans-serif;border-collapse:collapse;">
  <tr><td style="background:#1a1b26;color:#c0caf5;padding:20px;">
    <h2 style="margin:0;">k8sCenter — Notification Digest</h2>
    <p style="margin:8px 0 0;">{{len .}} notifications since last digest</p>
  </td></tr>
  {{range .}}
  <tr><td style="padding:12px;border-bottom:1px solid #333;">
    <strong>{{severityEmoji .Severity}} {{.Title}}</strong><br>
    <span style="color:#888;font-size:13px;">{{.Source}} · {{.ResourceKind}}/{{.ResourceNS}}/{{.ResourceName}} · {{.CreatedAt.Format "Jan 2, 15:04 UTC"}}</span>
    {{if .Message}}<br><span style="font-size:13px;">{{.Message}}</span>{{end}}
  </td></tr>
  {{end}}
  <tr><td style="padding:16px;text-align:center;font-size:13px;color:#888;">
    All times UTC · <a href="#" style="color:#7aa2f7;">Open k8sCenter</a>
  </td></tr>
</table>
</body></html>`))

func renderDigestEmail(notifications []Notification) (string, error) {
	var buf bytes.Buffer
	if err := digestTemplate.Execute(&buf, notifications); err != nil {
		return "", fmt.Errorf("render digest template: %w", err)
	}
	return buf.String(), nil
}

// --- Retention ---

func (s *NotificationService) runRetention(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := s.store.PruneOlderThan(ctx, 90*24*time.Hour)
			if err != nil {
				s.logger.Error("prune notifications", "error", err)
			} else if count > 0 {
				s.logger.Info("pruned old notifications", "count", count)
			}
		}
	}
}

// --- SSRF validation (re-exported for handler use) ---

// ValidateChannelURL validates webhook/Slack URLs against the SSRF blocklist.
func ValidateChannelURL(url string) error {
	return k8s.ValidateRemoteURL(url)
}
