package alerting

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"math/rand/v2"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
)

//go:embed templates/*.html
var templateFS embed.FS

var emailTemplates = template.Must(
	template.New("").Funcs(template.FuncMap{
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "N/A"
			}
			return t.Format("2006-01-02 15:04:05 UTC")
		},
		"duration": func(start, end time.Time) string {
			if end.IsZero() || start.IsZero() {
				return "ongoing"
			}
			d := end.Sub(start)
			if d < time.Minute {
				return fmt.Sprintf("%ds", int(d.Seconds()))
			}
			if d < time.Hour {
				return fmt.Sprintf("%dm", int(d.Minutes()))
			}
			return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
		},
	}).ParseFS(templateFS, "templates/*.html"),
)

// EmailMessage represents an email to be sent.
type EmailMessage struct {
	Subject    string
	Body       string
	Priority   string   // "high" for critical alerts
	Recipients []string // optional override; nil uses configured recipients
}

// Notifier sends alert email notifications via SMTP.
type Notifier struct {
	configMu   sync.RWMutex
	config     config.SMTPConfig
	from       string
	recipients []string

	queue  chan *EmailMessage
	logger *slog.Logger

	// Rate limiting
	rateMu           sync.Mutex
	hourlyCount      int
	hourlyReset      time.Time
	hourlyLimit      int
	alertCooldown    map[string]time.Time // fingerprint → last notified time
	cooldownDuration time.Duration
}

// NewNotifier creates a new email notifier.
func NewNotifier(cfg config.SMTPConfig, from string, recipients []string, rateLimit int, logger *slog.Logger) *Notifier {
	return &Notifier{
		config:           cfg,
		from:             from,
		recipients:       recipients,
		queue:            make(chan *EmailMessage, 100),
		logger:           logger,
		hourlyLimit:      rateLimit,
		hourlyReset:      time.Now(),
		alertCooldown:    make(map[string]time.Time),
		cooldownDuration: 15 * time.Minute,
	}
}

// UpdateConfig updates the SMTP configuration at runtime.
func (n *Notifier) UpdateConfig(cfg config.SMTPConfig, from string, recipients []string) {
	n.configMu.Lock()
	defer n.configMu.Unlock()
	n.config = cfg
	if from != "" {
		n.from = from
	}
	if len(recipients) > 0 {
		n.recipients = recipients
	}
}

// SMTPConfigured returns true if SMTP is configured with at least a host.
func (n *Notifier) SMTPConfigured() bool {
	n.configMu.RLock()
	defer n.configMu.RUnlock()
	return n.config.Host != ""
}

// QueueAlert queues an email notification for an alert action.
// Returns true if the email was queued, false if rate-limited or skipped.
func (n *Notifier) QueueAlert(action AlertAction) bool {
	if !n.SMTPConfigured() {
		return false
	}

	// Resolved alerts bypass per-alert cooldown
	if action.Type != "resolved" {
		if !n.checkCooldown(action.Alert.Fingerprint) {
			return false
		}
	}

	// Check global rate limit
	if !n.checkGlobalRate() {
		n.logger.Warn("email rate limit exceeded, dropping notification",
			"alertName", action.Alert.Labels["alertname"],
			"fingerprint", action.Alert.Fingerprint,
		)
		return false
	}

	msg, err := n.renderAlert(action)
	if err != nil {
		n.logger.Error("failed to render alert email", "error", err)
		return false
	}

	select {
	case n.queue <- msg:
		return true
	default:
		n.logger.Warn("email queue full, dropping notification")
		return false
	}
}

// QueueTestEmail queues a test email to verify SMTP configuration.
func (n *Notifier) QueueTestEmail() error {
	if !n.SMTPConfigured() {
		return fmt.Errorf("SMTP is not configured")
	}

	var buf bytes.Buffer
	if err := emailTemplates.ExecuteTemplate(&buf, "alert_test.html", map[string]string{
		"Time": time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
	}); err != nil {
		return fmt.Errorf("rendering test email: %w", err)
	}

	msg := &EmailMessage{
		Subject: "[KubeCenter] Test Email",
		Body:    buf.String(),
	}

	select {
	case n.queue <- msg:
		return nil
	default:
		return fmt.Errorf("email queue is full")
	}
}

// QueueEmail queues an arbitrary HTML email for delivery.
// Used by the notification center for digest emails with custom recipients.
func (n *Notifier) QueueEmail(recipients []string, subject, htmlBody string) error {
	if !n.SMTPConfigured() {
		return fmt.Errorf("SMTP is not configured")
	}
	msg := &EmailMessage{
		Subject:    subject,
		Body:       htmlBody,
		Recipients: recipients,
	}
	select {
	case n.queue <- msg:
		return nil
	default:
		return fmt.Errorf("email queue is full")
	}
}

// Run processes the email queue and periodically cleans up expired cooldowns.
// Call as `go notifier.Run(ctx)`.
func (n *Notifier) Run(ctx context.Context) {
	cleanupTicker := time.NewTicker(15 * time.Minute)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-n.queue:
			if err := n.sendWithRetry(ctx, msg, 3); err != nil {
				n.logger.Error("failed to send email after retries",
					"error", err, "subject", msg.Subject)
			}
		case <-cleanupTicker.C:
			n.cleanupCooldowns()
		}
	}
}

// cleanupCooldowns removes expired entries from the alertCooldown map.
func (n *Notifier) cleanupCooldowns() {
	n.rateMu.Lock()
	defer n.rateMu.Unlock()

	now := time.Now()
	for fp, lastNotify := range n.alertCooldown {
		if now.Sub(lastNotify) >= n.cooldownDuration {
			delete(n.alertCooldown, fp)
		}
	}
}

func (n *Notifier) checkCooldown(fingerprint string) bool {
	n.rateMu.Lock()
	defer n.rateMu.Unlock()

	if last, ok := n.alertCooldown[fingerprint]; ok {
		if time.Since(last) < n.cooldownDuration {
			return false
		}
	}
	n.alertCooldown[fingerprint] = time.Now()
	return true
}

func (n *Notifier) checkGlobalRate() bool {
	n.rateMu.Lock()
	defer n.rateMu.Unlock()

	now := time.Now()
	if now.Sub(n.hourlyReset) >= time.Hour {
		n.hourlyCount = 0
		n.hourlyReset = now
	}

	if n.hourlyCount >= n.hourlyLimit {
		return false
	}
	n.hourlyCount++
	return true
}

func (n *Notifier) renderAlert(action AlertAction) (*EmailMessage, error) {
	var (
		tmplName string
		subject  string
	)

	data := map[string]any{
		"AlertName":    action.Alert.Labels["alertname"],
		"Severity":     action.Alert.Labels["severity"],
		"Namespace":    action.Alert.Labels["namespace"],
		"Status":       action.Alert.Status,
		"Labels":       action.Alert.Labels,
		"Annotations":  action.Alert.Annotations,
		"StartsAt":     action.Alert.StartsAt,
		"EndsAt":       action.Alert.EndsAt,
		"GeneratorURL": action.Alert.GeneratorURL,
	}

	switch action.Type {
	case "resolved":
		tmplName = "alert_resolved.html"
		subject = fmt.Sprintf("[KubeCenter] [RESOLVED] %s", action.Alert.Labels["alertname"])
	default:
		tmplName = "alert_firing.html"
		severity := action.Alert.Labels["severity"]
		if severity == "" {
			severity = "warning"
		}
		subject = fmt.Sprintf("[KubeCenter] [%s] %s", severity, action.Alert.Labels["alertname"])
	}

	var buf bytes.Buffer
	if err := emailTemplates.ExecuteTemplate(&buf, tmplName, data); err != nil {
		return nil, fmt.Errorf("rendering template %s: %w", tmplName, err)
	}

	// Sanitize CRLF to prevent SMTP header injection via alert labels
	subject = strings.ReplaceAll(subject, "\r", "")
	subject = strings.ReplaceAll(subject, "\n", "")

	return &EmailMessage{
		Subject: subject,
		Body:    buf.String(),
	}, nil
}

func (n *Notifier) sendWithRetry(ctx context.Context, msg *EmailMessage, maxAttempts int) error {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			jitter := time.Duration(rand.Int64N(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		if err := n.send(msg); err != nil {
			lastErr = err
			n.logger.Warn("SMTP send attempt failed",
				"attempt", attempt+1, "error", err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all %d SMTP attempts failed: %w", maxAttempts, lastErr)
}

func (n *Notifier) send(msg *EmailMessage) error {
	n.configMu.RLock()
	cfg := n.config
	from := n.from
	recipients := n.recipients
	n.configMu.RUnlock()

	if cfg.Host == "" {
		return fmt.Errorf("SMTP host not configured")
	}

	// Use per-message recipients if set, otherwise configured list, otherwise from address
	rcpts := recipients
	if len(msg.Recipients) > 0 {
		rcpts = msg.Recipients
	}
	if len(rcpts) == 0 {
		rcpts = []string{from}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	// Build RFC 2822 message
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\n", from)
	fmt.Fprintf(&body, "To: %s\r\n", strings.Join(rcpts, ", "))
	fmt.Fprintf(&body, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&body, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&body, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&body, "\r\n")
	body.WriteString(msg.Body)

	// Port 465 uses implicit TLS
	if cfg.Port == 465 {
		return n.sendImplicitTLS(cfg, from, rcpts, addr, body.Bytes())
	}

	// Port 587 (or other) uses STARTTLS
	return n.sendSTARTTLS(cfg, from, rcpts, addr, body.Bytes())
}

func (n *Notifier) sendSTARTTLS(cfg config.SMTPConfig, from string, rcpts []string, addr string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello("kubecenter"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         cfg.Host,
			InsecureSkipVerify: cfg.TLSInsecure,
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range rcpts {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return c.Quit()
}

func (n *Notifier) sendImplicitTLS(cfg config.SMTPConfig, from string, rcpts []string, addr string, msg []byte) error {
	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.TLSInsecure,
	}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer c.Close()

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range rcpts {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return c.Quit()
}
