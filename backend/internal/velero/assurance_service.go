package velero

// assurance_service.go — Release F U34b: the background backup-assurance
// collector (KTD11, R22, R23; acceptance example AE8).
//
// Once a minute the collector reads the local cluster's Velero inventory
// through the handler's existing service-account cache, runs the pure
// evaluator over the operator's policies, and reconciles the findings with
// the open exceptions in PostgreSQL: a new condition opens one exception and
// one delivery intent in a single transaction, a repeated condition is an
// observation, a condition that is no longer produced while Velero was fully
// observed is resolved. Durable intents are then drained to the Notification
// Center. It holds NO in-memory condition state: the open rows are the
// memory, so a restart is not a special case and there is nothing to seed.
//
// Panic safety (R-1). This loop runs OUTSIDE chi's recovery middleware; an
// unrecovered panic here terminates the process and silently ends backup
// monitoring. Start is the only goroutine this file ever runs on: no
// errgroup, no WaitGroup, no channels, no per-item fan-out. The tick body is
// wrapped in recoverutil.Tick and the shutdown lease release in
// recoverutil.Safe. The fan-out inside handler.fetchAll is inherited and
// already joins its recoverutil.Go workers before returning.
//
// Replica safety (R-3). The lease is advisory: it stops a second replica
// from duplicating the Kubernetes reads and racing observations, never from
// being correct. Two replicas that both believe they hold it still cannot
// open the same exception twice or enqueue the same transition twice, because
// the store's partial UNIQUE indexes decide that.
//
// Honesty (Design Decisions §7). A collection that is not ok never resolves
// anything and yields one cluster-scoped collection_unknown rather than
// per-subject alarms; notification text never claims that backups are
// missing when Velero could not be observed, and never claims that a fresh
// backup is a demonstrated recovery.
//
// Observation only. Nothing here creates a Restore, a DeleteBackupRequest or
// any other Velero object; this file does not even import a Kubernetes
// client. TestAssurance_NeverConstructsARestore pins that.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// assuranceInterval matches the cert-manager and ESO pollers; a shorter
	// cadence would only re-read the handler's 30 s cache.
	assuranceInterval = 60 * time.Second
	// assuranceLeaseTTL is three missed ticks before another replica takes
	// over. Expiry is judged on the database clock (store.AcquireOrRenewLease).
	assuranceLeaseTTL = 3 * assuranceInterval
	// assuranceDeliveryBatch bounds the serial drain per tick. At most two
	// transitions per subject can occur in one tick and the steady state is
	// zero, so the bound is a ceiling on a bad day, not a throttle.
	assuranceDeliveryBatch = 50
	// assuranceMaxDeliveryAttempts is how many ticks an intent is retried
	// before it is parked as failed and surfaced as backlog. One attempt per
	// tick means every retry lands inside the Notification Center's 15 min
	// dedup window, so a send whose MarkDelivered did not commit is
	// suppressed on retry rather than duplicated.
	assuranceMaxDeliveryAttempts = 5
	// assurancePruneInterval spaces PruneResolved; retention itself is
	// store.BackupAssuranceExceptionRetention.
	assurancePruneInterval = time.Hour
	// assuranceReleaseTimeout bounds the shutdown lease release, which runs
	// after the loop's context is already cancelled.
	assuranceReleaseTimeout = 2 * time.Second
)

// assuranceResourceKind is the ResourceKind on every assurance notification.
// It is one fixed string: encoding the condition in it ("backup.overdue")
// would hand the condition to every external channel as identity metadata,
// outside the RBAC-filtered feed (see externalsecrets.notificationFor).
const assuranceResourceKind = "backup"

// Notification titles, exported so a rename is one compiler-checked change
// (the ESO poller exports its titles for the same reason). Each opened title
// is per condition so the Notification Center's dedup identity, which keys
// on title, keeps two conditions of one subject apart.
const (
	TitleBackupOverdue             = "Backup overdue"
	TitleBackupFailed              = "Backup failed"
	TitleBackupPartiallyFailed     = "Backup partially failed"
	TitleBackupNeverRun            = "Backup schedule has never run"
	TitleBackupSchedulePaused      = "Backup schedule paused"
	TitleBackupLocationUnavailable = "Backup storage location unavailable"
	TitleBackupCollectionUnknown   = "Backup state unknown"
	// TitleBackupResolvedPrefix precedes the opened title on a resolution
	// ("Resolved: Backup overdue"), so each condition's resolution is its own
	// dedup identity too.
	TitleBackupResolvedPrefix = "Resolved: "
	// titleBackupCondition is the fallback for a condition this build does
	// not know (a newer schema than this binary): still a notification,
	// never a silent drop.
	titleBackupCondition = "Backup assurance condition"
)

// Notification messages are fixed sentences. They name neither the subject
// (identity rides in the structured, suppressible resource fields) nor any
// controller text (that stays in the privileged detail JSONB).
const (
	msgBackupOverdue             = "A backup has not completed successfully within its freshness policy."
	msgBackupFailed              = "The most recent backup run failed."
	msgBackupPartiallyFailed     = "The most recent backup run completed with errors on some items."
	msgBackupNeverRun            = "A backup schedule has produced no backups since it was created."
	msgBackupSchedulePaused      = "A backup schedule is paused; no backups will run until it is resumed."
	msgBackupLocationUnavailable = "The backup storage location is not available; new backups cannot be stored."
	msgBackupCollectionUnknown   = "Velero could not be observed, so backup state is unknown. This is not evidence that backups are missing."
	msgBackupCondition           = "A backup assurance condition holds."
	msgBackupResolved            = "The condition is no longer observed. Backup freshness is not demonstrated recoverability."
	msgBackupResolvedAbsent      = "The backup schedule is no longer present, so its open condition was closed. Backup freshness is not demonstrated recoverability."
)

// assuranceStore is the slice of *store.BackupAssuranceStore the collector
// uses. Holding it as an interface keeps the loop testable without
// PostgreSQL; the public constructor still takes the concrete store so a nil
// store disables the collector instead of becoming a non-nil interface.
type assuranceStore interface {
	AcquireOrRenewLease(ctx context.Context, clusterID, holder string, ttl time.Duration) (store.AssuranceLease, error)
	ReleaseLease(ctx context.Context, clusterID, holder string) error
	ListPolicies(ctx context.Context, clusterID string) ([]store.BackupAssurancePolicy, error)
	ListOpenExceptions(ctx context.Context, clusterID string) ([]store.BackupAssuranceException, error)
	OpenExceptionAndEnqueue(ctx context.Context, e store.BackupAssuranceException) (store.BackupAssuranceException, bool, error)
	ObserveException(ctx context.Context, id uuid.UUID, observedAt time.Time, severity string, lastSuccessAt *time.Time, detail []byte) error
	ResolveExceptionAndEnqueue(ctx context.Context, id uuid.UUID, at time.Time, reason string) (bool, error)
	ClaimPendingDeliveries(ctx context.Context, clusterID string, maxAttempts, limit int) ([]store.AssuranceDeliveryJob, error)
	MarkDelivered(ctx context.Context, id uuid.UUID) error
	MarkDeliveryFailed(ctx context.Context, id uuid.UUID, errMsg string, maxAttempts int) error
	PruneResolved(ctx context.Context, retention time.Duration) (int64, error)
}

// assuranceEmitter is the one notification operation the drain needs: the
// result-reporting emit, so an intent is marked by what actually happened.
type assuranceEmitter interface {
	EmitSync(ctx context.Context, n notifications.Notification) (notifications.EmitResult, error)
}

var (
	_ assuranceStore   = (*store.BackupAssuranceStore)(nil)
	_ assuranceEmitter = (*notifications.NotificationService)(nil)
)

// AssuranceRuntimeStatus is the collector's read model for the status
// endpoint (U35). Every field is this replica's view; lease fields are the
// row as of this replica's last successful acquisition or renewal. LastError
// is an operation label ("list policies"), never the underlying error text:
// the log carries that.
type AssuranceRuntimeStatus struct {
	// Enabled is false when the collector was built without a database or a
	// notification service; Start then returns at once.
	Enabled bool
	// Holder is this replica's lease identity.
	Holder string
	// LastTickAt is when this replica last started a tick, whether or not
	// it then held the lease.
	LastTickAt time.Time
	// LastRunAt is when this replica last completed a tick's work. Zero
	// until the first run.
	LastRunAt time.Time
	// LastCollection is the Collection of the last run that collected.
	// Empty when no run has collected yet, including when every run so far
	// found no policies and did not read Velero.
	LastCollection Collection
	// PolicyCount and FindingCount are from the last completed run.
	PolicyCount  int
	FindingCount int
	// LastError labels the operation that failed the most recent tick, and
	// LastErrorAt when. Both are cleared by the next completed run.
	LastError   string
	LastErrorAt time.Time
	// LeaseHeld reports whether this replica held the lease on its last
	// tick; the lease fields describe that lease.
	LeaseHeld      bool
	LeaseHolder    string
	LeaseFence     int64
	LeaseExpiresAt time.Time
}

// AssuranceService owns the Release F collection loop and is the read model
// behind the assurance status endpoint. Construct it with
// NewAssuranceService and run Start on its own goroutine; Snapshot is safe
// to call from any goroutine.
type AssuranceService struct {
	handler   *Handler
	disc      *Discoverer
	store     assuranceStore
	notif     assuranceEmitter
	clusterID string
	holder    string
	logger    *slog.Logger
	disabled  string // non-empty: why the collector is disabled

	// interval and now are the loop's cadence and clock; tests shorten
	// and pin them.
	interval time.Duration
	now      func() time.Time

	mu          sync.RWMutex
	status      AssuranceRuntimeStatus
	lastPruneAt time.Time
}

// DefaultAssuranceHolder is the lease identity for this process:
// "<hostname>-<pid>", stable for the process lifetime and distinct across
// replicas and restarts.
func DefaultAssuranceHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "kubecenter"
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}

// NewAssuranceService wires the collector. st and n may be nil (no
// PostgreSQL, or no notification service): the service is then disabled,
// Start logs once and returns, and Snapshot reports Enabled=false. h and d
// are the local Velero handler and discoverer; clusterID is the local
// cluster's id as the policy and exception rows carry it; an empty holder
// means DefaultAssuranceHolder().
func NewAssuranceService(
	h *Handler, d *Discoverer, st *store.BackupAssuranceStore,
	n *notifications.NotificationService, clusterID, holder string, logger *slog.Logger,
) *AssuranceService {
	// Pick the concrete nils apart here: a nil *store assigned to the
	// interface field would be non-nil and panic on first use.
	var (
		as assuranceStore
		ae assuranceEmitter
	)
	if st != nil {
		as = st
	}
	if n != nil {
		ae = n
	}
	return newAssuranceServiceWith(h, d, as, ae, clusterID, holder, logger)
}

// newAssuranceServiceWith is the composition seam behind NewAssuranceService;
// tests substitute an in-memory store and a recording emitter here.
func newAssuranceServiceWith(h *Handler, d *Discoverer, st assuranceStore, n assuranceEmitter, clusterID, holder string, logger *slog.Logger) *AssuranceService {
	if logger == nil {
		logger = slog.Default()
	}
	if holder == "" {
		holder = DefaultAssuranceHolder()
	}
	a := &AssuranceService{
		handler: h, disc: d, store: st, notif: n,
		clusterID: clusterID, holder: holder, logger: logger,
		interval: assuranceInterval, now: time.Now,
	}
	switch {
	case st == nil:
		a.disabled = "no database configured"
	case n == nil:
		a.disabled = "no notification service"
	case h == nil || d == nil:
		a.disabled = "velero handler or discoverer not wired"
	case clusterID == "":
		a.disabled = "no cluster id"
	}
	a.status = AssuranceRuntimeStatus{Enabled: a.disabled == "", Holder: holder}
	return a
}

// Start runs the collector loop: one tick at once, then one per interval,
// until ctx is cancelled, when it releases the lease and returns. A
// disabled service returns immediately. Run it as `go svc.Start(ctx)`; it
// is the only goroutine the collector uses. Each tick is wrapped in
// recoverutil.Tick so a panicking cycle is logged and the next tick starts
// from a clean slate instead of taking the process down.
func (a *AssuranceService) Start(ctx context.Context) {
	if a.disabled != "" {
		a.logger.Info("backup assurance collector disabled", "reason", a.disabled)
		return
	}
	if ctx.Err() == nil {
		a.runTickWithRecover(ctx)
	}

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.releaseLeaseOnShutdown()
			return
		case <-ticker.C:
			a.runTickWithRecover(ctx)
		}
	}
}

func (a *AssuranceService) runTickWithRecover(ctx context.Context) {
	recoverutil.Tick(ctx, a.logger, "velero assurance tick", a.tick)
}

// releaseLeaseOnShutdown hands the lease back so a replacement replica
// takes over on its next tick instead of waiting out the TTL. The loop's
// context is already cancelled, so the release gets its own bounded one.
// Best-effort: an unreleased lease expires on its own.
func (a *AssuranceService) releaseLeaseOnShutdown() {
	recoverutil.Safe(a.logger, "velero assurance lease release", func() {
		ctx, cancel := context.WithTimeout(context.Background(), assuranceReleaseTimeout)
		defer cancel()
		if err := a.store.ReleaseLease(ctx, a.clusterID, a.holder); err != nil {
			a.logger.Warn("backup assurance: lease release failed; it will expire", "error", err)
		}
	})
}

// Snapshot returns a copy of the runtime status.
func (a *AssuranceService) Snapshot() AssuranceRuntimeStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// tick is one collection cycle, entirely serial. Any step that fails on a
// non-sentinel error ends the cycle: the next tick starts over from the
// durable state, which nothing here has left half-written (every
// transition commits with its intent in one store transaction).
func (a *AssuranceService) tick(ctx context.Context) {
	now := a.now().UTC()
	a.update(func(s *AssuranceRuntimeStatus) { s.LastTickAt = now })

	// 1. Lease. A live incumbent means this replica does nothing this
	//    cycle; correctness never depended on winning.
	lease, err := a.store.AcquireOrRenewLease(ctx, a.clusterID, a.holder, assuranceLeaseTTL)
	if err != nil {
		// Held by another replica, or unknowable (store error): either way
		// this replica cannot claim tenure and does no work this cycle.
		a.update(func(s *AssuranceRuntimeStatus) {
			s.LeaseHeld, s.LeaseHolder, s.LeaseFence, s.LeaseExpiresAt = false, "", 0, time.Time{}
		})
		if !errors.Is(err, store.ErrLeaseHeldByOther) {
			a.fail(ctx, now, "acquire lease", err)
		}
		return
	}
	a.update(func(s *AssuranceRuntimeStatus) {
		s.LeaseHeld, s.LeaseHolder, s.LeaseFence, s.LeaseExpiresAt = true, lease.Holder, lease.Fence, lease.ExpiresAt
	})

	// 2. Policies. None means no evaluation and no Velero read: installing
	//    k8sCenter never implies a policy, and policy deletion cascades the
	//    exceptions and intents, so there is nothing to reconcile or drain.
	policies, err := a.store.ListPolicies(ctx, a.clusterID)
	if err != nil {
		a.fail(ctx, now, "list policies", err)
		return
	}
	if len(policies) == 0 {
		a.completeRun(now, func(s *AssuranceRuntimeStatus) { s.PolicyCount, s.FindingCount = 0, 0 })
		return
	}

	// 3. Collect, 4. evaluate (pure), 5. reconcile with the durable open set.
	obs := a.collect(ctx)
	findings := Evaluate(obs, policies, now)
	open, err := a.store.ListOpenExceptions(ctx, a.clusterID)
	if err != nil {
		a.fail(ctx, now, "list open exceptions", err)
		return
	}
	if err := a.reconcile(ctx, now, obs, findings, open); err != nil {
		a.fail(ctx, now, "reconcile exceptions", err)
		return
	}

	// 6. Drain intents, serially and bounded. 7. Retention, hourly.
	if err := a.drainDeliveries(ctx); err != nil {
		a.fail(ctx, now, "deliver notifications", err)
		return
	}
	a.pruneIfDue(ctx, now)

	a.completeRun(now, func(s *AssuranceRuntimeStatus) {
		s.LastCollection, s.PolicyCount, s.FindingCount = obs.Collection, len(policies), len(findings)
	})
}

// collect gathers one Observation through the handler's service-account
// cache (the same 30 s TTL, singleflight and recovered fan-out every Velero
// page uses) and maps failure truthfully onto Collection. fetchAll fails as
// a whole on the first list error, so the result is ok or failed; degraded
// is reachable only once doFetchAll surfaces per-list errors (plan risk R-6)
// and is not produced here.
func (a *AssuranceService) collect(ctx context.Context) Observation {
	obs := Observation{ClusterID: a.clusterID, CollectedAt: a.now().UTC(), Collection: CollectionFailed}
	obs.Status = a.disc.Status(ctx)
	if !obs.Status.Detected {
		return obs
	}
	data, err := a.handler.fetchAll(ctx)
	if err != nil {
		a.logger.Warn("backup assurance: velero read failed; collection is unknown", "error", err)
		return obs
	}
	obs.Backups, obs.Schedules, obs.Locations = data.backups, data.schedules, data.locations
	obs.Collection = CollectionOK
	return obs
}

// exceptionKey is the condition identity (Design Decisions §4): the subject
// including its UID, plus the condition. PolicyID is deliberately absent.
type exceptionKey struct {
	Subject   Subject
	Condition store.AssuranceCondition
}

func keyOfException(e store.BackupAssuranceException) exceptionKey {
	return exceptionKey{
		Subject:   Subject{Kind: e.SubjectKind, Namespace: e.SubjectNamespace, Name: e.SubjectName, UID: e.SubjectUID},
		Condition: e.Condition,
	}
}

// reconcile applies the state machine to one tick's findings against the
// open rows loaded before them:
//
//   - a finding whose identity is open is an observation;
//   - a finding whose identity is not open opens it (one transaction with
//     its "opened" intent); a lost race falls back to an observation;
//   - a held finding (paused schedule) observes an existing row but never
//     opens one;
//   - an open row no finding matched is resolved — with the "resolved"
//     intent in the same transaction — ONLY when collection was ok. A
//     failed or degraded collection leaves every open row exactly as it
//     is: this is the hard rule, enforced again at the write layer.
//
// Sentinel outcomes (policy vanished, identity contended, row already
// closed) are logged and skipped; the next tick sees a settled state. Any
// other store error aborts the tick.
func (a *AssuranceService) reconcile(ctx context.Context, now time.Time, obs Observation, findings []Finding, open []store.BackupAssuranceException) error {
	openByKey := make(map[exceptionKey]store.BackupAssuranceException, len(open))
	for _, e := range open {
		openByKey[keyOfException(e)] = e
	}
	matched := make(map[uuid.UUID]bool, len(open))

	for _, f := range findings {
		detail, err := json.Marshal(f.Detail)
		if err != nil {
			return fmt.Errorf("encode finding detail: %w", err)
		}
		if e, ok := openByKey[exceptionKey{Subject: f.Subject, Condition: f.Condition}]; ok {
			matched[e.ID] = true
			if err := a.observe(ctx, e.ID, now, f, detail); err != nil {
				return err
			}
			continue
		}
		if f.Hold {
			continue
		}
		got, opened, err := a.store.OpenExceptionAndEnqueue(ctx, store.BackupAssuranceException{
			ID:               uuid.New(),
			ClusterID:        a.clusterID,
			PolicyID:         f.PolicyID,
			SubjectKind:      f.Subject.Kind,
			SubjectNamespace: f.Subject.Namespace,
			SubjectName:      f.Subject.Name,
			SubjectUID:       f.Subject.UID,
			Condition:        f.Condition,
			Severity:         f.Severity,
			OpenedAt:         now,
			LastSuccessAt:    f.Detail.LastSuccessAt,
			Detail:           detail,
		})
		switch {
		case errors.Is(err, store.ErrAssurancePolicyNotFound):
			a.logger.Debug("backup assurance: policy removed before its exception opened", "policy", f.PolicyID)
			continue
		case errors.Is(err, store.ErrAssuranceOpenContended):
			a.logger.Debug("backup assurance: exception identity contended; retrying next tick", "condition", f.Condition)
			continue
		case errors.Is(err, store.ErrAssuranceExceptionInvalid):
			// A finding the store cannot represent is a bug in the evaluator
			// or in this mapping, not an outage: log loudly, keep going.
			a.logger.Error("backup assurance: evaluator produced an unstorable exception", "condition", f.Condition, "subjectKind", f.Subject.Kind, "error", err)
			continue
		case err != nil:
			return err
		}
		matched[got.ID] = true
		if opened {
			a.logger.Info("backup assurance: exception opened",
				"id", got.ID, "condition", got.Condition, "subjectKind", got.SubjectKind,
				"namespace", got.SubjectNamespace, "name", got.SubjectName)
			continue
		}
		// Another replica, or a tick that raced ours, opened it first.
		if err := a.observe(ctx, got.ID, now, f, detail); err != nil {
			return err
		}
	}

	if obs.Collection != CollectionOK {
		return nil
	}
	for _, e := range open {
		if matched[e.ID] {
			continue
		}
		reason := store.AssuranceResolutionConditionCleared
		if e.SubjectKind == store.ScopeSchedule && !scheduleObserved(obs, e) {
			reason = store.AssuranceResolutionSubjectAbsent
		}
		resolved, err := a.store.ResolveExceptionAndEnqueue(ctx, e.ID, now, reason)
		if err != nil {
			return err
		}
		if resolved {
			a.logger.Info("backup assurance: exception resolved",
				"id", e.ID, "condition", e.Condition, "reason", reason,
				"namespace", e.SubjectNamespace, "name", e.SubjectName)
		}
	}
	return nil
}

// observe records a repeat of an open exception. A row resolved between the
// load and this write is not an error: the condition, if still true, opens
// afresh on the next tick.
func (a *AssuranceService) observe(ctx context.Context, id uuid.UUID, now time.Time, f Finding, detail []byte) error {
	err := a.store.ObserveException(ctx, id, now, f.Severity, f.Detail.LastSuccessAt, detail)
	if errors.Is(err, store.ErrAssuranceExceptionNotOpen) {
		a.logger.Debug("backup assurance: exception closed before observation", "id", id)
		return nil
	}
	return err
}

// scheduleObserved reports whether e's schedule subject, by namespace, name
// AND uid, is in the inventory. A recreated schedule has a new uid and so
// does not keep its predecessor's exception alive (R1 / KTD3).
func scheduleObserved(obs Observation, e store.BackupAssuranceException) bool {
	for i := range obs.Schedules {
		s := &obs.Schedules[i]
		if s.Namespace == e.SubjectNamespace && s.Name == e.SubjectName && s.UID == e.SubjectUID {
			return true
		}
	}
	return false
}

// drainDeliveries claims up to a batch of pending intents and sends each
// one, marking it by what EmitSync reported: an error is a failed attempt
// (retried on later ticks, parked as failed after the last), anything the
// feed carries is delivered. Claiming already spent the attempt, so an
// intent left unmarked by a cancelled context or a crash is claimable
// again, and the Notification Center's dedup window absorbs the re-send.
func (a *AssuranceService) drainDeliveries(ctx context.Context) error {
	jobs, err := a.store.ClaimPendingDeliveries(ctx, a.clusterID, assuranceMaxDeliveryAttempts, assuranceDeliveryBatch)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, emitErr := a.notif.EmitSync(ctx, a.notificationFor(j))
		var markErr error
		switch {
		case emitErr != nil:
			a.logger.Warn("backup assurance: notification emit failed",
				"delivery", j.Delivery.ID, "attempt", j.Delivery.Attempts, "error", emitErr)
			markErr = a.store.MarkDeliveryFailed(ctx, j.Delivery.ID, deliveryFailureReason(emitErr), assuranceMaxDeliveryAttempts)
		case res.Delivered():
			markErr = a.store.MarkDelivered(ctx, j.Delivery.ID)
		default:
			// EmitSync's contract is "never EmitFailed with a nil error";
			// treat a breach as a failed attempt rather than a delivery.
			a.logger.Error("backup assurance: emit returned no error but was not delivered", "delivery", j.Delivery.ID, "result", res.String())
			markErr = a.store.MarkDeliveryFailed(ctx, j.Delivery.ID, "notification not delivered: "+res.String(), assuranceMaxDeliveryAttempts)
		}
		if errors.Is(markErr, store.ErrAssuranceDeliveryNotPending) {
			a.logger.Debug("backup assurance: delivery already finished elsewhere", "delivery", j.Delivery.ID)
			continue
		}
		if markErr != nil {
			return markErr
		}
	}
	return nil
}

// deliveryFailureReason is what the delivery row records for a failed emit.
// The raw error is logged, not stored: it can carry hostnames, SQL and
// driver detail that the status endpoint would otherwise surface (CWE-209).
func deliveryFailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "notification emit cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "notification emit timed out"
	default:
		return "notification service error (see server log)"
	}
}

// notificationFor builds the Notification Center entry for one intent. The
// sensitive-field contract mirrors externalsecrets.notificationFor: a fixed
// ResourceKind, the subject in the structured resource fields with
// SuppressResourceFields so Slack, webhook and the email digest drop them,
// and title and message that never carry controller text or the subject's
// name. ClusterID is the exception's cluster, so the feed can attribute it.
func (a *AssuranceService) notificationFor(j store.AssuranceDeliveryJob) notifications.Notification {
	e := j.Exception
	n := notifications.Notification{
		Source:                 notifications.SourceVelero,
		ResourceKind:           assuranceResourceKind,
		ResourceNS:             e.SubjectNamespace,
		ResourceName:           e.SubjectName,
		ResourceUID:            e.SubjectUID,
		ClusterID:              e.ClusterID,
		CreatedAt:              a.now().UTC(),
		SuppressResourceFields: true,
	}
	title, msg := assuranceCopy(e.Condition)
	if j.Delivery.Transition == store.AssuranceTransitionResolved {
		n.Severity = notifications.SeverityInfo
		n.Title = TitleBackupResolvedPrefix + title
		n.Message = msgBackupResolved
		if resolutionReasonOf(e.Detail) == store.AssuranceResolutionSubjectAbsent {
			n.Message = msgBackupResolvedAbsent
		}
		return n
	}
	n.Severity = notifications.Severity(e.Severity)
	if !n.Severity.Valid() {
		n.Severity = notifications.SeverityWarning
	}
	n.Title, n.Message = title, msg
	return n
}

// assuranceCopy is the opened title and message for a condition.
func assuranceCopy(c store.AssuranceCondition) (title, message string) {
	switch c {
	case store.ConditionOverdue:
		return TitleBackupOverdue, msgBackupOverdue
	case store.ConditionFailed:
		return TitleBackupFailed, msgBackupFailed
	case store.ConditionPartiallyFailed:
		return TitleBackupPartiallyFailed, msgBackupPartiallyFailed
	case store.ConditionNeverRun:
		return TitleBackupNeverRun, msgBackupNeverRun
	case store.ConditionPaused:
		return TitleBackupSchedulePaused, msgBackupSchedulePaused
	case store.ConditionLocationUnavailable:
		return TitleBackupLocationUnavailable, msgBackupLocationUnavailable
	case store.ConditionCollectionUnknown:
		return TitleBackupCollectionUnknown, msgBackupCollectionUnknown
	}
	return titleBackupCondition, msgBackupCondition
}

// resolutionReasonOf reads the reason ResolveExceptionAndEnqueue merged into
// the detail. It is the collector's own key, not controller text, and it
// only selects between two fixed sentences.
func resolutionReasonOf(detail []byte) string {
	var d struct {
		Reason string `json:"resolutionReason"`
	}
	_ = json.Unmarshal(detail, &d)
	return d.Reason
}

// pruneIfDue deletes resolved exceptions past retention at most once per
// assurancePruneInterval. A failed prune is logged, not a tick failure: it
// is housekeeping, and the next hour retries it.
func (a *AssuranceService) pruneIfDue(ctx context.Context, now time.Time) {
	a.mu.Lock()
	due := a.lastPruneAt.IsZero() || now.Sub(a.lastPruneAt) >= assurancePruneInterval
	if due {
		a.lastPruneAt = now
	}
	a.mu.Unlock()
	if !due {
		return
	}
	n, err := a.store.PruneResolved(ctx, store.BackupAssuranceExceptionRetention)
	if err != nil {
		a.logger.Warn("backup assurance: prune of resolved exceptions failed", "error", err)
		return
	}
	if n > 0 {
		a.logger.Info("backup assurance: pruned resolved exceptions", "count", n, "retention", store.BackupAssuranceExceptionRetention)
	}
}

// update mutates the status under the lock.
func (a *AssuranceService) update(fn func(*AssuranceRuntimeStatus)) {
	a.mu.Lock()
	fn(&a.status)
	a.mu.Unlock()
}

// completeRun records a finished tick and clears any earlier error.
func (a *AssuranceService) completeRun(now time.Time, fn func(*AssuranceRuntimeStatus)) {
	a.update(func(s *AssuranceRuntimeStatus) {
		s.LastRunAt, s.LastError, s.LastErrorAt = now, "", time.Time{}
		fn(s)
	})
}

// fail logs a step that ended the tick and records its label. A failure
// caused by shutdown (the context is done) is expected and is neither
// recorded nor logged above debug.
func (a *AssuranceService) fail(ctx context.Context, now time.Time, op string, err error) {
	if ctx.Err() != nil {
		a.logger.Debug("backup assurance: "+op+" abandoned on shutdown", "error", err)
		return
	}
	a.logger.Warn("backup assurance: "+op+" failed", "error", err)
	a.update(func(s *AssuranceRuntimeStatus) { s.LastError, s.LastErrorAt = op, now })
}
