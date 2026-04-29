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

// thresholdBucket maps a days-remaining value to its threshold bucket
// using the cert's resolved per-cert thresholds. If ApplyThresholds
// hasn't been run on this cert (zero values), defense-in-depth falls
// back to the package defaults so a forgotten resolution call still
// emits sensible (global-default) notifications instead of treating
// warn=0 as "never warn".
func thresholdBucket(cert Certificate) threshold {
	if cert.DaysRemaining == nil {
		return thresholdNone
	}
	days := *cert.DaysRemaining
	warn := cert.WarningThresholdDays
	if warn <= 0 {
		warn = WarningThresholdDays
	}
	crit := cert.CriticalThresholdDays
	if crit <= 0 {
		crit = CriticalThresholdDays
	}
	switch {
	case days < 0:
		return thresholdExpired
	case days <= crit:
		return thresholdCritical
	case days <= warn:
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
	handler      *Handler
	notifService *notifications.NotificationService
	logger       *slog.Logger

	mu     sync.Mutex
	dedupe map[string]threshold // key: cert UID; value: last emitted bucket
}

// NewPoller creates a Poller wired to the cluster client, discoverer, handler cache, and notification service.
func NewPoller(cf *k8s.ClientFactory, disc *Discoverer, handler *Handler, notifService *notifications.NotificationService, logger *slog.Logger) *Poller {
	return &Poller{
		k8s:          cf,
		disc:         disc,
		handler:      handler,
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

	bucket := thresholdBucket(c)

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
	var title, msg string
	if rec.Threshold == thresholdExpired {
		title = "Certificate expired (critical)"
		msg = "A certificate has already expired"
	} else {
		title = fmt.Sprintf("Certificate expiring (%s)", rec.Severity)
		msg = fmt.Sprintf("A certificate expires in %d day(s)", *c.DaysRemaining)
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

	certs, err := p.fetchCertificates(ctx)
	if err != nil {
		p.logger.Error("certmanager poller: failed to list certificates", "error", err)
		return
	}

	seen := make(map[string]bool, len(certs))
	for _, cert := range certs {
		seen[cert.UID] = true

		records := p.check(cert)
		for _, rec := range records {
			p.emit(ctx, rec)
		}
	}

	// Prune stale UIDs from dedupe map
	p.mu.Lock()
	for uid := range p.dedupe {
		if !seen[uid] {
			delete(p.dedupe, uid)
		}
	}
	p.mu.Unlock()
}

// fetchCertificates returns certificates from the handler cache if
// available, otherwise falls back to direct API listing. The cache
// path returns thresholds-resolved certs (handler.fetchAll runs
// ApplyThresholds before storing). The fallback path also lists
// Issuers + ClusterIssuers and runs ApplyThresholds so it produces
// the same threshold-resolved view — otherwise tests and degraded
// startup would skip per-cert overrides entirely.
func (p *Poller) fetchCertificates(ctx context.Context) ([]Certificate, error) {
	if p.handler != nil {
		return p.handler.CachedCertificates(ctx)
	}

	// Fallback: direct list (used in tests or when handler is nil)
	dyn := p.k8s.BaseDynamicClient()
	list, err := dyn.Resource(CertificateGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	certs := make([]Certificate, 0, len(list.Items))
	for i := range list.Items {
		cert, cerr := normalizeCertificate(&list.Items[i])
		if cerr != nil {
			p.logger.Warn("certmanager poller: failed to normalize certificate",
				"name", list.Items[i].GetName(),
				"namespace", list.Items[i].GetNamespace(),
				"error", cerr,
			)
			continue
		}
		certs = append(certs, cert)
	}

	// Best-effort issuer fetches — failures fall through to defaults
	// rather than erroring the whole poll cycle. The poller already
	// degrades gracefully when fields are missing (thresholdBucket has
	// belt-and-suspenders defaults), so this just enriches when
	// possible.
	var issuers, clusterIssuers []Issuer
	if iList, ierr := dyn.Resource(IssuerGVR).Namespace("").List(ctx, metav1.ListOptions{}); ierr == nil {
		for i := range iList.Items {
			issuers = append(issuers, normalizeIssuer(&iList.Items[i], "Namespaced"))
		}
	}
	if ciList, cerr := dyn.Resource(ClusterIssuerGVR).Namespace("").List(ctx, metav1.ListOptions{}); cerr == nil {
		for i := range ciList.Items {
			clusterIssuers = append(clusterIssuers, normalizeIssuer(&ciList.Items[i], "Cluster"))
		}
	}

	return ApplyThresholds(certs, issuers, clusterIssuers, p.logger), nil
}
