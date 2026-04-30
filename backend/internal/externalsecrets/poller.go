package externalsecrets

import (
	"context"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/notifications"
)

// pollerInterval is the cadence at which the poller scans the ExternalSecret
// inventory and dispatches Notification Center events. 60s matches the
// cert-manager poller and is conservative enough that even an aggressive
// `eso-stale-after-minutes` annotation (5-min floor, see thresholds.go)
// can't produce more than ~12 stale events per hour per ES. Lower than
// 60s would put pressure on the handler cache TTL (also 30s).
const pollerInterval = 60 * time.Second

// EventKind labels each notification dispatch type. The dedupe key is
// (UID, EventKind) so failure and recovery for the same ES live in
// separate slots — a recovery emit is NOT suppressed by a recently-cleared
// failure (plan L4.1).
type EventKind string

const (
	EventSyncFailed  EventKind = "sync_failed"
	EventStale       EventKind = "stale"
	EventUnhealthy   EventKind = "unhealthy"
	EventRecovered   EventKind = "recovered"
	EventCreated     EventKind = "created"
	EventDeleted     EventKind = "deleted"
	EventFirstSynced EventKind = "first_synced"
)

// statusBucket collapses the 6-state Status enum into 4 emit-relevant
// buckets. Drifted folds into healthy here — drift gets its own dedicated
// event in Phase C and is operationally distinct from "the controller
// can't reconcile". Refreshing also folds into healthy: it's a transient
// state ("controller is working") that should not page operators.
type statusBucket int

const (
	bucketHealthy statusBucket = iota
	bucketFailed
	bucketStale
	bucketUnknown
)

func (b statusBucket) String() string {
	switch b {
	case bucketHealthy:
		return "healthy"
	case bucketFailed:
		return "failed"
	case bucketStale:
		return "stale"
	default:
		return "unknown"
	}
}

func bucketFor(s Status) statusBucket {
	switch s {
	case StatusSynced, StatusRefreshing, StatusDrifted:
		return bucketHealthy
	case StatusSyncFailed:
		return bucketFailed
	case StatusStale:
		return bucketStale
	default:
		return bucketUnknown
	}
}

// emitRecord captures one dispatch the tick wants to perform. The poller
// computes records under p.mu, then releases the lock and dispatches —
// notifications.Emit can block on a slow Slack/webhook leg, and we don't
// want the dedupe map locked while that runs.
type emitRecord struct {
	ES    ExternalSecret
	Kind  EventKind
	Title string
	Msg   string
	Sev   notifications.Severity
}

// Poller scans the ExternalSecret inventory on a 60s cadence and emits
// Notification Center events for status transitions. Phase D ships the
// dispatch path (failure, stale, recovery, lifecycle); Phase C Unit 10
// will extend tick() with DB persistence so the timeline UI has a backing
// store. The structure here is deliberately split: stateTransitions /
// emit / tick are separable so Phase C only adds an extra side-effect
// inside tick rather than reshaping the dispatch flow.
type Poller struct {
	k8s          *k8s.ClientFactory
	disc         *Discoverer
	handler      *Handler
	notifService *notifications.NotificationService
	logger       *slog.Logger

	mu          sync.Mutex
	prevBucket  map[string]statusBucket // UID -> last-seen bucket
	seen        map[string]bool         // UIDs observed in any prior tick
	initialized bool                    // first tick: skip lifecycle events for existing inventory
}

// NewPoller wires the poller against the platform service-account dynamic
// client (via k8s.ClientFactory). Local cluster only — multi-cluster
// dispatch is a separate concern (the platform doesn't poll remote
// clusters; that runs in each cluster's own deployment).
func NewPoller(
	cf *k8s.ClientFactory,
	disc *Discoverer,
	handler *Handler,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Poller {
	return &Poller{
		k8s:          cf,
		disc:         disc,
		handler:      handler,
		notifService: notifService,
		logger:       logger,
		prevBucket:   make(map[string]statusBucket),
		seen:         make(map[string]bool),
	}
}

// newPollerForTest returns a minimal Poller suitable for unit tests. It has
// no k8s client, discoverer, or notification service — only the dedupe
// state and the default logger.
func newPollerForTest() *Poller {
	return &Poller{
		logger:     slog.Default(),
		prevBucket: make(map[string]statusBucket),
		seen:       make(map[string]bool),
	}
}

// check evaluates a single ExternalSecret against the dedupe state and
// returns any emitRecord that should be dispatched. Caller is responsible
// for calling emit() on each record (matches cert-manager's split).
//
// Dedupe semantics:
//   - Bucket transition healthy->failed emits sync_failed once. Subsequent
//     ticks with the same bucket suppress (in-memory dedupe by (UID, kind)).
//   - Bucket transition failed->healthy emits recovered IF the resolved
//     AlertOnRecovery is true. Recovery emit clears prevBucket so a future
//     re-failure produces a fresh sync_failed event.
//   - Brand-new ES (no prior bucket recorded) skips lifecycle emit on the
//     very first tick (initialized=false). On subsequent ticks, a UID we
//     haven't seen before fires created or first_synced — only when
//     AlertOnLifecycle is true (off by default).
func (p *Poller) check(es ExternalSecret) []emitRecord {
	p.mu.Lock()
	defer p.mu.Unlock()

	current := bucketFor(es.Status)
	prev, hadPrev := p.prevBucket[es.UID]
	wasSeen := p.seen[es.UID]
	p.seen[es.UID] = true
	p.prevBucket[es.UID] = current

	// Lifecycle: brand-new ES. Skip during the first tick (initialized=false)
	// — operators get one created/first_synced for ESes added AFTER the
	// platform comes up, not for the entire existing inventory at startup.
	if !wasSeen && p.initialized {
		if alertOnLifecycle(es) {
			kind := EventCreated
			if current == bucketHealthy {
				kind = EventFirstSynced
			}
			return []emitRecord{lifecycleRecord(es, kind)}
		}
	}

	// First tick: record bucket but don't emit. The "previous bucket" was
	// nil before this tick, so any "transition" is just initial observation.
	if !hadPrev {
		return nil
	}

	// Same bucket on consecutive ticks: nothing to emit.
	if prev == current {
		return nil
	}

	// Bucket transition.
	switch {
	case prev == bucketHealthy && current == bucketFailed:
		return []emitRecord{failureRecord(es, EventSyncFailed)}
	case prev == bucketHealthy && current == bucketStale:
		return []emitRecord{failureRecord(es, EventStale)}
	case prev == bucketHealthy && current == bucketUnknown:
		// Healthy -> Unknown is unusual (controller stopped reconciling).
		// Fold into sync_failed semantics so operators still get paged.
		return []emitRecord{failureRecord(es, EventSyncFailed)}
	case (prev == bucketFailed || prev == bucketStale || prev == bucketUnknown) && current == bucketHealthy:
		if alertOnRecovery(es) {
			return []emitRecord{recoveryRecord(es)}
		}
		return nil
	case prev == bucketFailed && current == bucketStale,
		prev == bucketStale && current == bucketFailed,
		prev == bucketFailed && current == bucketUnknown,
		prev == bucketUnknown && current == bucketFailed:
		// Lateral transitions between failure-class buckets: dedupe slots
		// are distinct, so emit the new kind. Operators see "still
		// degraded but for a different reason now."
		var kind EventKind
		switch current {
		case bucketStale:
			kind = EventStale
		case bucketFailed:
			kind = EventSyncFailed
		default:
			kind = EventUnhealthy
		}
		return []emitRecord{failureRecord(es, kind)}
	}

	return nil
}

// alertOnRecovery resolves the AlertOnRecovery flag on the ES, defaulting
// to false (matches DefaultAlertOnRecovery). Reading via the resolver path
// would re-walk the chain — but ApplyThresholds has already populated
// es.AlertOnRecovery, so we just dereference.
func alertOnRecovery(es ExternalSecret) bool {
	if es.AlertOnRecovery == nil {
		return DefaultAlertOnRecovery
	}
	return *es.AlertOnRecovery
}

// alertOnLifecycle is the AlertOnLifecycle equivalent.
func alertOnLifecycle(es ExternalSecret) bool {
	if es.AlertOnLifecycle == nil {
		return DefaultAlertOnLifecycle
	}
	return *es.AlertOnLifecycle
}

func failureRecord(es ExternalSecret, kind EventKind) emitRecord {
	var title, msg string
	sev := notifications.SeverityWarning
	switch kind {
	case EventSyncFailed:
		title = "ExternalSecret sync failed"
		msg = "An ExternalSecret could not be reconciled by the controller."
		sev = notifications.SeverityCritical
	case EventStale:
		title = "ExternalSecret stale"
		msg = "An ExternalSecret has not synced within its stale-after window."
	case EventUnhealthy:
		title = "ExternalSecret unhealthy"
		msg = "An ExternalSecret is in an unknown or degraded state."
	}
	return emitRecord{ES: es, Kind: kind, Title: title, Msg: msg, Sev: sev}
}

func recoveryRecord(es ExternalSecret) emitRecord {
	return emitRecord{
		ES:    es,
		Kind:  EventRecovered,
		Title: "ExternalSecret recovered",
		Msg:   "An ExternalSecret has resumed syncing successfully.",
		Sev:   notifications.SeverityInfo,
	}
}

func lifecycleRecord(es ExternalSecret, kind EventKind) emitRecord {
	var title, msg string
	switch kind {
	case EventFirstSynced:
		title = "ExternalSecret first synced"
		msg = "A new ExternalSecret has been reconciled for the first time."
	case EventCreated:
		title = "ExternalSecret created"
		msg = "A new ExternalSecret has been created (not yet synced)."
	case EventDeleted:
		title = "ExternalSecret deleted"
		msg = "An ExternalSecret has been deleted."
	}
	return emitRecord{ES: es, Kind: kind, Title: title, Msg: msg, Sev: notifications.SeverityInfo}
}

// emit dispatches a single emitRecord to the notification service. No-op
// when notifService is nil (tests). All ESO events set
// SuppressResourceFields=true so Slack/webhook payloads omit
// namespace/name — defeats the tenant-leakage path that the
// RBAC-generic title alone doesn't close.
func (p *Poller) emit(ctx context.Context, rec emitRecord) {
	if p.notifService == nil {
		return
	}

	resourceKind := "externalsecret." + string(rec.Kind)

	p.notifService.Emit(ctx, notifications.Notification{
		Source:                 notifications.SourceExternalSecrets,
		Severity:               rec.Sev,
		Title:                  rec.Title,
		Message:                rec.Msg,
		ResourceKind:           resourceKind,
		ResourceNS:             rec.ES.Namespace,
		ResourceName:           rec.ES.Name,
		CreatedAt:              time.Now().UTC(),
		SuppressResourceFields: true,
	})
}

// Start runs the poller loop. Fires immediately, then on a 60s ticker.
// Blocks until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	p.tick(ctx)

	// After the first tick we mark the poller initialized — subsequent
	// ticks will treat unseen UIDs as genuine creations rather than
	// startup inventory.
	p.mu.Lock()
	p.initialized = true
	p.mu.Unlock()

	ticker := time.NewTicker(pollerInterval)
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

// tick performs one polling cycle. Lists ExternalSecrets via the handler
// cache (which already runs ApplyThresholds, so es.AlertOnRecovery /
// AlertOnLifecycle / Status are resolved), evaluates each one, dispatches
// emit records, and prunes deleted UIDs.
func (p *Poller) tick(ctx context.Context) {
	if p.disc != nil && !p.disc.IsAvailable(ctx) {
		return
	}

	ess, err := p.fetchExternalSecrets(ctx)
	if err != nil {
		p.logger.Error("externalsecrets poller: list failed", "error", err)
		return
	}

	currentUIDs := make(map[string]bool, len(ess))
	var records []emitRecord
	for _, es := range ess {
		currentUIDs[es.UID] = true
		records = append(records, p.check(es)...)
	}

	for _, rec := range records {
		p.emit(ctx, rec)
	}

	// Prune UIDs that vanished — emit deleted lifecycle events for ESes
	// we'd previously seen and that have AlertOnLifecycle resolved true.
	// Note: deletion lifecycle events run from the LAST resolved state
	// of the ES, which we no longer hold (the ES is gone from the
	// inventory). For Phase D we surface a generic deletion event keyed
	// by UID alone; Phase C will persist the last-seen state in the
	// history table and replace this stub with rich payloads.
	p.mu.Lock()
	for uid := range p.prevBucket {
		if !currentUIDs[uid] {
			delete(p.prevBucket, uid)
			delete(p.seen, uid)
		}
	}
	p.mu.Unlock()
}

// fetchExternalSecrets returns the ES inventory from the handler cache when
// available (already threshold-resolved), falling back to a direct list +
// in-process ApplyThresholds otherwise. The fallback path is used in
// tests that bypass the handler.
func (p *Poller) fetchExternalSecrets(ctx context.Context) ([]ExternalSecret, error) {
	if p.handler != nil {
		return p.handler.CachedExternalSecrets(ctx)
	}

	// Fallback: direct list with in-process resolution. Errors degrade
	// gracefully — a failed store list still produces ESes via the
	// resolver default path.
	dyn := p.k8s.BaseDynamicClient()
	esList, err := dyn.Resource(ExternalSecretGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	ess := make([]ExternalSecret, 0, len(esList.Items))
	for i := range esList.Items {
		ess = append(ess, normalizeExternalSecret(&esList.Items[i]))
	}

	var stores, clusterStores []SecretStore
	if sList, sErr := dyn.Resource(SecretStoreGVR).Namespace("").List(ctx, metav1.ListOptions{}); sErr == nil {
		for i := range sList.Items {
			stores = append(stores, normalizeSecretStore(&sList.Items[i], "Namespaced"))
		}
	}
	if csList, cErr := dyn.Resource(ClusterSecretStoreGVR).Namespace("").List(ctx, metav1.ListOptions{}); cErr == nil {
		for i := range csList.Items {
			clusterStores = append(clusterStores, normalizeSecretStore(&csList.Items[i], "Cluster"))
		}
	}

	ApplyThresholds(ess, stores, clusterStores, p.logger)
	return ess, nil
}
