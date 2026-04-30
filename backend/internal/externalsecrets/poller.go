package externalsecrets

import (
	"context"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
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
// buckets. Refreshing folds into healthy — it's a transient state
// ("controller is working") that should not page operators.
//
// Drifted also folds into healthy here, but only as a defensive default:
// the poller reads ESes from handler.fetchAll, which does NOT resolve
// DriftStatus (that requires a per-ES impersonated `get secret` and runs
// only on the detail endpoint). So in normal operation an ES observed by
// the poller never carries StatusDrifted — the case is unreachable. Phase
// C will surface drift through the persisted history, at which point this
// bucket mapping should be revisited (e.g., a separate bucketDrifted so a
// Drifted->Stale->Drifted sequence doesn't emit a misleading recovery).
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

// isFailureBucket returns true for any bucket the operator should be paged
// for. Used to identify lateral transitions between failure modes (e.g.,
// stale -> unknown) so we emit a fresh event for the new mode rather than
// silently dropping the transition.
func isFailureBucket(b statusBucket) bool {
	return b == bucketFailed || b == bucketStale || b == bucketUnknown
}

// failureEventKind picks the EventKind for a transition INTO a failure
// bucket. Used by both healthy->failure and lateral failure->failure
// transitions so the dedupe slot matches the new state.
func failureEventKind(b statusBucket) EventKind {
	switch b {
	case bucketStale:
		return EventStale
	case bucketFailed:
		return EventSyncFailed
	default:
		return EventUnhealthy
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
	notifStore   *notifications.Store // optional; nil disables restart-recovery seeding
	historyStore *store.ESOHistoryStore
	clusterID    string // local cluster id for eso_sync_history.cluster_id
	logger       *slog.Logger

	mu            sync.Mutex
	prevBucket    map[string]statusBucket        // UID -> last-seen bucket
	seen          map[string]bool                // UIDs observed in any prior tick
	initialized   bool                           // first tick: skip lifecycle events for existing inventory
	prevAttemptAt map[string]time.Time           // UID -> last attempt_at processed (skip dup Secret fetches)
	prevKeys      map[string]map[string][32]byte // UID -> key -> sha256(value); in-memory only, lost across restart
	seedNSName    map[string]statusBucket        // (ns/name) -> bucket from recent notifications; consumed by check() on first observation
}

// NewPoller wires the poller against the platform service-account dynamic
// client (via k8s.ClientFactory). Local cluster only — multi-cluster
// dispatch is a separate concern (the platform doesn't poll remote
// clusters; that runs in each cluster's own deployment).
//
// historyStore and notifStore are optional. When historyStore is nil, the
// poller runs Phase D's dispatch-only path with no DB persistence — useful
// in tests and in deployments where the operator prefers an in-memory-only
// timeline. When notifStore is nil, restart-recovery seeding is skipped;
// a recovery transition that crosses a process restart will be silently
// suppressed instead of firing an event (the original Phase D behavior).
func NewPoller(
	cf *k8s.ClientFactory,
	disc *Discoverer,
	handler *Handler,
	notifService *notifications.NotificationService,
	notifStore *notifications.Store,
	historyStore *store.ESOHistoryStore,
	clusterID string,
	logger *slog.Logger,
) *Poller {
	return &Poller{
		k8s:           cf,
		disc:          disc,
		handler:       handler,
		notifService:  notifService,
		notifStore:    notifStore,
		historyStore:  historyStore,
		clusterID:     clusterID,
		logger:        logger,
		prevBucket:    make(map[string]statusBucket),
		seen:          make(map[string]bool),
		prevAttemptAt: make(map[string]time.Time),
		prevKeys:      make(map[string]map[string][32]byte),
	}
}

// newPollerForTest returns a minimal Poller suitable for unit tests. It has
// no k8s client, discoverer, or notification service — only the dedupe
// state and the default logger.
func newPollerForTest() *Poller {
	return &Poller{
		logger:        slog.Default(),
		clusterID:     "local",
		prevBucket:    make(map[string]statusBucket),
		seen:          make(map[string]bool),
		prevAttemptAt: make(map[string]time.Time),
		prevKeys:      make(map[string]map[string][32]byte),
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

	// Phase C: consume the (ns, name) -> bucket seed populated from
	// recently-persisted notifications. The seed exists only on the
	// first tick after a process restart and lets a recovery transition
	// that crossed the restart boundary still emit an event. Once an
	// ES UID claims its seed entry, we delete it — subsequent ticks use
	// the genuine prevBucket.
	if !hadPrev && !wasSeen && p.seedNSName != nil {
		key := es.Namespace + "/" + es.Name
		if seedBucket, ok := p.seedNSName[key]; ok {
			prev = seedBucket
			hadPrev = true
			wasSeen = true
			delete(p.seedNSName, key)
		}
	}

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

	// Healthy -> failure: emit the matching failure-class event.
	if prev == bucketHealthy && isFailureBucket(current) {
		return []emitRecord{failureRecord(es, failureEventKind(current))}
	}

	// Failure -> healthy: emit recovery if the operator opted in.
	if isFailureBucket(prev) && current == bucketHealthy {
		if alertOnRecovery(es) {
			return []emitRecord{recoveryRecord(es)}
		}
		return nil
	}

	// Lateral transitions between failure-class buckets: dedupe slots are
	// distinct per kind, so emit the new kind. Operators see "still
	// degraded but for a different reason now." Covers all six lateral
	// pairs: failed<->stale, failed<->unknown, stale<->unknown.
	if isFailureBucket(prev) && isFailureBucket(current) {
		return []emitRecord{failureRecord(es, failureEventKind(current))}
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
//
// ResourceKind is set to a static "externalsecret" string rather than
// "externalsecret.<EventKind>" so external dispatch payloads don't leak
// the operational state (sync_failed / stale / etc.) across tenants. The
// in-app feed reader can derive the EventKind from Title + Severity if
// needed; the kind suffix was redundant identity information.
func (p *Poller) emit(ctx context.Context, rec emitRecord) {
	if p.notifService == nil {
		return
	}

	p.notifService.Emit(ctx, notifications.Notification{
		Source:                 notifications.SourceExternalSecrets,
		Severity:               rec.Sev,
		Title:                  rec.Title,
		Message:                rec.Msg,
		ResourceKind:           "externalsecret",
		ResourceNS:             rec.ES.Namespace,
		ResourceName:           rec.ES.Name,
		CreatedAt:              time.Now().UTC(),
		SuppressResourceFields: true,
	})
}

// emitConcurrency caps the number of in-flight Emit() calls per tick. The
// notifications service makes synchronous DB round-trips per call; serial
// dispatch in a mass-failure scenario (e.g., 100 ESes simultaneously stale
// after an NTP correction) would block the tick goroutine for several
// seconds. Bounded concurrency keeps tick latency proportional to
// max(emit_latency) × ceil(N / emitConcurrency) instead of N × emit_latency,
// while staying conservative enough to avoid pgxpool exhaustion.
const emitConcurrency = 10

// Start runs the poller loop. Fires immediately, then on a 60s ticker.
// Blocks until ctx is cancelled. tick() panics are recovered so a single
// bad cycle doesn't kill the goroutine — silent poller death after a
// transient driver panic would leave operators unalerted indefinitely.
//
// Before the first tick, Start() seeds prevBucket from recently-persisted
// notifications so a recovery that happens across a process restart still
// fires an event. The seed is opt-in (notifStore != nil) and best-effort —
// failures are logged and execution continues.
func (p *Poller) Start(ctx context.Context) {
	p.seedFromNotifications(ctx)

	p.runTickWithRecover(ctx)

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
			p.runTickWithRecover(ctx)
		}
	}
}

// runTickWithRecover wraps tick() with a defer recover() so a panic in
// dispatch (e.g., pgx pool exhaustion edge case, slog handler defect)
// doesn't unwind the poller goroutine. The next ticker fire restarts the
// tick from a clean slate.
func (p *Poller) runTickWithRecover(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("externalsecrets poller: tick panic recovered",
				"panic", r,
			)
		}
	}()
	p.tick(ctx)
}

// tick performs one polling cycle. Lists ExternalSecrets via the handler
// cache (which already runs ApplyThresholds, so es.AlertOnRecovery /
// AlertOnLifecycle / Status are resolved), evaluates each one, dispatches
// emit records concurrently (bounded by emitConcurrency), and prunes
// deleted UIDs.
func (p *Poller) tick(ctx context.Context) {
	if p.disc != nil && !p.disc.IsAvailable(ctx) {
		return
	}

	ess, err := p.fetchExternalSecrets(ctx)
	if err != nil {
		// Don't prune on failed fetch — currentUIDs would be empty and
		// pruning everything would re-fire all ESes as "new" lifecycle
		// events on the next successful tick. The next successful fetch
		// will catch up any genuinely-deleted UIDs.
		p.logger.Error("externalsecrets poller: list failed", "error", err)
		return
	}

	currentUIDs := make(map[string]bool, len(ess))
	var records []emitRecord
	for _, es := range ess {
		currentUIDs[es.UID] = true
		records = append(records, p.check(es)...)
	}

	dispatchEmits(ctx, records, p.emit)

	p.persistAttempts(ctx, ess)

	// Prune UIDs that have vanished from the inventory. Phase D drops the
	// dedupe state silently — Phase C Unit 10 retains the prevAttemptAt
	// row for ~1 tick after deletion so a recreate-with-same-UID (rare
	// since UID is a fresh GUID per resource) doesn't re-trigger the
	// "first sync" empty-diff path. EventDeleted lifecycle dispatch on
	// pruned UIDs is deferred — needs a captured AlertOnLifecycle bool
	// per UID, which Phase D's emitRecord pipeline doesn't carry through
	// to prune time. Tracked as a Phase D follow-up.
	p.mu.Lock()
	for uid := range p.prevBucket {
		if !currentUIDs[uid] {
			delete(p.prevBucket, uid)
			delete(p.seen, uid)
			delete(p.prevAttemptAt, uid)
			delete(p.prevKeys, uid)
		}
	}
	p.mu.Unlock()
}

// dispatchEmits runs each emit through a bounded-concurrency worker pool.
// Extracted so tests can substitute a synchronous emitter without
// reimplementing the loop. ctx cancellation aborts pending dispatches.
func dispatchEmits(
	ctx context.Context,
	records []emitRecord,
	emit func(context.Context, emitRecord),
) {
	if len(records) == 0 {
		return
	}
	sem := make(chan struct{}, emitConcurrency)
	var wg sync.WaitGroup
	for _, rec := range records {
		// Priority ctx check: Go's select is non-deterministic when
		// multiple cases are ready (cancelled ctx + empty semaphore).
		// A non-blocking probe first guarantees we abort promptly when
		// ctx was cancelled, rather than racing the semaphore send.
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(r emitRecord) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				// Per-emit panic recovery — a single bad notification
				// payload shouldn't kill sibling dispatches in the
				// same tick. tick() itself has its own recover()
				// (runTickWithRecover) for everything else.
				_ = recover()
			}()
			emit(ctx, r)
		}(rec)
	}
	wg.Wait()
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
