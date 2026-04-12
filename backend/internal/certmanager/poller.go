package certmanager

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

// threshold represents the expiry bucket for a certificate.
type threshold int

const (
	thresholdNone     threshold = iota // > WarningThresholdDays
	thresholdWarning                   // <= WarningThresholdDays and > CriticalThresholdDays
	thresholdCritical                  // <= CriticalThresholdDays and >= 0
	thresholdExpired                   // < 0
)

// String returns a human-readable label for the threshold bucket.
func (t threshold) String() string {
	switch t {
	case thresholdWarning:
		return "warning"
	case thresholdCritical:
		return "critical"
	case thresholdExpired:
		return "expired"
	default:
		return "none"
	}
}

// thresholdBucket maps a days-remaining value to its threshold bucket.
func thresholdBucket(days int) threshold {
	switch {
	case days < 0:
		return thresholdExpired
	case days <= CriticalThresholdDays:
		return thresholdCritical
	case days <= WarningThresholdDays:
		return thresholdWarning
	default:
		return thresholdNone
	}
}

// emitRecord holds one notification event to be dispatched.
type emitRecord struct {
	Certificate Certificate
	Severity    string
	Threshold   threshold
}

// Poller periodically lists all cert-manager Certificates and emits notifications
// when a certificate crosses an expiry threshold. A deduplication map keyed by
// cert UID prevents repeat notifications within the same threshold bucket.
type Poller struct {
	k8s          *k8s.ClientFactory
	disc         *Discoverer
	notifService *notifications.NotificationService
	logger       *slog.Logger

	mu     sync.Mutex
	dedupe map[string]threshold // key: cert UID; value: last emitted bucket
}

// NewPoller creates a Poller wired to the cluster client, discoverer, and notification service.
func NewPoller(cf *k8s.ClientFactory, disc *Discoverer, notifService *notifications.NotificationService, logger *slog.Logger) *Poller {
	return &Poller{
		k8s:          cf,
		disc:         disc,
		notifService: notifService,
		logger:       logger,
		dedupe:       make(map[string]threshold),
	}
}

// newPollerForTest returns a minimal Poller suitable for unit tests.
// It has no k8s client, discoverer, or notification service — only an
// initialized dedupe map and the default logger.
func newPollerForTest() *Poller {
	return &Poller{
		logger: slog.Default(),
		dedupe: make(map[string]threshold),
	}
}

// check evaluates a single Certificate against the dedupe state and returns
// any emitRecord that should be dispatched. The caller is responsible for
// calling emit() on each returned record.
func (p *Poller) check(c Certificate) []emitRecord {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c.NotAfter == nil || c.DaysRemaining == nil {
		return nil
	}

	bucket := thresholdBucket(*c.DaysRemaining)

	prev, hasPrev := p.dedupe[c.UID]

	if bucket == thresholdNone {
		// Certificate is healthy. If it was previously tracked (i.e. it renewed),
		// clear the entry so a future degradation triggers a fresh emit.
		if hasPrev {
			delete(p.dedupe, c.UID)
		}
		return nil
	}

	if hasPrev && prev == bucket {
		// Already emitted for this bucket — suppress duplicate.
		return nil
	}

	// New crossing: record and emit.
	p.dedupe[c.UID] = bucket

	return []emitRecord{{
		Certificate: c,
		Severity:    bucket.String(),
		Threshold:   bucket,
	}}
}

// emit dispatches a single emitRecord to the notification service.
// It is a no-op when the notification service is nil (e.g. in tests).
func (p *Poller) emit(ctx context.Context, rec emitRecord) {
	if p.notifService == nil {
		return
	}

	var sev notifications.Severity
	var kind string

	switch rec.Threshold {
	case thresholdExpired:
		sev = notifications.SeverityCritical
		kind = "certificate.expired"
	case thresholdCritical:
		sev = notifications.SeverityCritical
		kind = "certificate.expiring"
	default:
		sev = notifications.SeverityWarning
		kind = "certificate.expiring"
	}

	c := rec.Certificate
	title := fmt.Sprintf("Certificate expiring: %s/%s", c.Namespace, c.Name)
	var msg string
	if rec.Threshold == thresholdExpired {
		title = fmt.Sprintf("Certificate expired: %s/%s", c.Namespace, c.Name)
		msg = fmt.Sprintf("Certificate %s/%s has already expired", c.Namespace, c.Name)
	} else {
		msg = fmt.Sprintf("Certificate %s/%s expires in %d day(s) (issuer: %s)",
			c.Namespace, c.Name, *c.DaysRemaining, c.IssuerRef.Name)
	}

	p.notifService.Emit(ctx, notifications.Notification{
		Source:       notifications.SourceCertManager,
		Severity:     sev,
		Title:        title,
		Message:      msg,
		ResourceKind: kind,
		ResourceNS:   c.Namespace,
		ResourceName: c.Name,
		CreatedAt:    time.Now().UTC(),
	})
}

// Start runs the poller loop. It fires immediately, then on a 60-second ticker.
// It blocks until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	p.tick(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick performs one polling cycle: lists all Certificates and processes each one.
func (p *Poller) tick(ctx context.Context) {
	if !p.disc.IsAvailable(ctx) {
		return
	}

	dyn := p.k8s.BaseDynamicClient()

	list, err := dyn.Resource(CertificateGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		p.logger.Error("certmanager poller: failed to list certificates", "error", err)
		return
	}

	for i := range list.Items {
		cert, err := normalizeCertificate(&list.Items[i])
		if err != nil {
			p.logger.Warn("certmanager poller: failed to normalize certificate",
				"name", list.Items[i].GetName(),
				"namespace", list.Items[i].GetNamespace(),
				"error", err,
			)
			continue
		}

		records := p.check(cert)
		for _, rec := range records {
			p.emit(ctx, rec)
		}
	}
}
