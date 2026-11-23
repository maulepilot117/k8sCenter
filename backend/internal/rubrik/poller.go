package rubrik

// poller.go — the Rubrik poller's tick (plan U10, KTD5, KTD10, R24, R27).
//
// One poller runs per replica. Every PollInterval it lists the connection's
// mapped clusters and ticks each one that this replica can lease: it reads
// the cluster's objects, SLA domains and recent failed snapshot jobs from
// RSC, turns them into condition states and events with planTick, and hands
// the result to store.CommitPollerTick, which writes it in one transaction
// only while this replica still holds the cluster's lease with the same
// fence. Every decision a tick makes depends on the persisted state it read
// after taking the lease, so the hysteresis and the connection streak carry
// over when another replica takes the lease, and two replicas never commit
// the same transition (R27).
//
// The poller only reads RSC. Its RSC calls go through the Service's cached
// reads and the read-only RSCReader; it holds no Operations, Client or
// writer, and poller_test.go pins that.
//
// What a tick decides:
//   - The cluster's first successful tick records state and raises nothing:
//     no activity is read, and every object starts closed (the plan's
//     Baseline --> Closed), so one already out of compliance is recorded as
//     late and opens on the next tick like any other.
//   - Compliance (KTD5): an object's instantaneous judgment comes from
//     EvaluateCompliance. A first out-of-compliance judgment is recorded as
//     late; a second consecutive one records out and raises
//     compliance_lost; an in-compliance judgment after out raises
//     compliance_restored. An unknown judgment (no SLA frequency) changes
//     nothing. Only protecting objects are judged; the states of all other
//     objects are pruned and read as unknown.
//   - Snapshot failures: a failed Backup activity series raises one
//     snapshot_failed event, keyed by its series id, so seeing it again
//     raises nothing.
//   - Catch-up (R24): failures are read from the activity cursor, but never
//     from more than 24 hours back. A cursor older than that raises one
//     monitoring_gap event keyed by the cursor, and the earlier failures
//     are left to the operator.
//   - Connection (KTD10): a tick whose RSC reads fail counts toward the
//     streak and changes nothing else; the third consecutive failure raises
//     connection_failing, and the next tick that reads RSC raises
//     connection_restored. Reads that run past the cluster deadline count
//     too. Rate limits, store failures and answers that are not about the
//     connection (the mapped RSC cluster is missing) neither count nor
//     reset the streak: the tick commits nothing.
//
// An event names a namespace only when it concerns exactly one current
// namespace. Events for relics, multi-namespace or unresolved sets, objects
// RSC no longer lists, the connection and the gap carry no namespace, which
// makes them admin-only in the feed (KTD10, KTD11).
//
// Delivering the events, sweeping operations and the main.go wiring come in
// the next PR.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

const (
	// pollerLeaseTTL keeps a cluster's lease across one missed tick, so a
	// healthy holder keeps it and a dead one hands over within two ticks.
	pollerLeaseTTL = 2 * PollInterval
	// pollerClusterTimeout bounds one cluster's tick. A tick whose RSC reads
	// run past it commits a failed tick within pollerCommitTimeout; both
	// together stay well inside the lease TTL, so that commit still runs
	// under its own lease or not at all.
	pollerClusterTimeout = 3 * time.Minute
	pollerCommitTimeout  = 10 * time.Second
	// pollerReleaseTimeout bounds the shutdown lease release, which runs
	// after the loop's context is cancelled.
	pollerReleaseTimeout = 2 * time.Second
	// pollerCatchUp is the furthest back a tick reads failures (R24).
	pollerCatchUp = 24 * time.Hour
	// pollerJobLookback is how long before the failure window a job may have
	// started and still be read: the activity filter selects by start time,
	// and a snapshot can run for hours before it fails.
	pollerJobLookback = 24 * time.Hour
	// pollerFailureSlack widens the failure window back past the cursor, for
	// a job RSC stamps a moment before the tick that read it. Seeing a
	// failure twice raises it once.
	pollerFailureSlack = time.Minute
	// pollerConnectionThreshold is how many consecutive failed ticks open
	// the connection-failing condition (KTD10).
	pollerConnectionThreshold = 3
	// pollerComplianceThreshold is how many consecutive out-of-compliance
	// judgments open the out-of-compliance condition (KTD5).
	pollerComplianceThreshold = 2
	// pollerObjectBatch caps how many object ids one activity query names.
	pollerObjectBatch = 100
	// activityBackup is RSC's activity type for a snapshot job.
	activityBackup = "Backup"
)

// pollerFailureStatuses are the activity statuses a failed job reports.
var pollerFailureStatuses = []string{"FAILURE", "TASK_FAILURE"}

// errPollerClusterDeadline is the cause a cluster tick's context carries when
// pollerClusterTimeout expires, so the tick can tell a slow tenant (a failed
// tick) from process shutdown (nothing to commit).
var errPollerClusterDeadline = errors.New("rubrik poller cluster tick exceeded its deadline")

// PollerStore is the part of store.RubrikStore the poller uses.
type PollerStore interface {
	ListMappings(ctx context.Context, connectionID string) ([]store.RubrikClusterMapping, error)
	AcquireOrRenewPollerLease(ctx context.Context, clusterID, holder string, ttl time.Duration) (store.RubrikPollerLease, error)
	ReleasePollerLease(ctx context.Context, clusterID, holder string) error
	GetPollerState(ctx context.Context, clusterID string) (store.RubrikPollerState, bool, error)
	ListComplianceStates(ctx context.Context, clusterID string) ([]store.RubrikComplianceState, error)
	CommitPollerTick(ctx context.Context, lease store.RubrikPollerLease, tick store.RubrikPollerTick) ([]store.RubrikPollerEvent, error)
}

// Poller runs the Rubrik poller loop. Build it with NewPoller and run Start
// in its own goroutine.
type Poller struct {
	svc      *Service
	store    PollerStore
	holder   string
	logger   *slog.Logger
	now      func() time.Time
	interval time.Duration

	mu sync.Mutex
	// held are the clusters whose lease this replica took, released on
	// shutdown.
	held map[string]bool
}

// DefaultPollerHolder returns a lease holder id unique to this process: the
// host name (the pod name in a cluster) and a random suffix, so two
// processes never share an id even on one host.
func DefaultPollerHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "kubecenter"
	}
	return host + "-" + uuid.NewString()[:8]
}

// NewPoller returns a poller over the service's reads and the Rubrik store.
// An empty holder means DefaultPollerHolder().
func NewPoller(svc *Service, st PollerStore, holder string) *Poller {
	if holder == "" {
		holder = DefaultPollerHolder()
	}
	return &Poller{
		svc:      svc,
		store:    st,
		holder:   holder,
		logger:   svc.logger,
		now:      time.Now,
		interval: PollInterval,
		held:     map[string]bool{},
	}
}

func (p *Poller) clock() time.Time { return p.now().UTC() }

// Start ticks at once and then every interval until ctx is cancelled, when
// it releases the leases it holds and returns. Each tick runs under
// recoverutil.Tick, so a panic is logged and the next tick starts clean.
func (p *Poller) Start(ctx context.Context) {
	if ctx.Err() == nil {
		recoverutil.Tick(ctx, p.logger, "rubrik poller tick", p.tick)
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.releaseLeases()
			return
		case <-ticker.C:
			recoverutil.Tick(ctx, p.logger, "rubrik poller tick", p.tick)
		}
	}
}

// releaseLeases hands back every lease this replica took, so another
// replica takes over on its next tick instead of waiting out the TTL.
// Best-effort: an unreleased lease expires on its own.
func (p *Poller) releaseLeases() {
	p.mu.Lock()
	clusters := make([]string, 0, len(p.held))
	for id := range p.held {
		clusters = append(clusters, id)
	}
	clear(p.held)
	p.mu.Unlock()
	for _, id := range clusters {
		recoverutil.Safe(p.logger, "rubrik poller lease release", func() {
			ctx, cancel := context.WithTimeout(context.Background(), pollerReleaseTimeout)
			defer cancel()
			if err := p.store.ReleasePollerLease(ctx, id, p.holder); err != nil {
				p.logger.Warn("rubrik poller: lease release failed; it will expire", "cluster", id, "error", err)
			}
		})
	}
}

// tick ticks every mapped cluster of the connection, one after another,
// each under its own deadline and panic recovery.
func (p *Poller) tick(ctx context.Context) {
	mappings, err := p.store.ListMappings(ctx, p.svc.connectionID)
	if err != nil {
		p.logger.Error("rubrik poller: listing mappings failed", "error", err)
		return
	}
	for _, m := range mappings {
		if ctx.Err() != nil {
			return
		}
		clusterCtx, cancel := context.WithTimeoutCause(ctx, pollerClusterTimeout, errPollerClusterDeadline)
		recoverutil.Tick(clusterCtx, p.logger, "rubrik poller cluster tick", func(ctx context.Context) {
			if err := p.tickCluster(ctx, m.ClusterID); err != nil {
				p.logger.Warn("rubrik poller: cluster tick failed", "cluster", m.ClusterID, "reason", errorClass(err), "error", err)
			}
		})
		cancel()
	}
}

// tickCluster runs one cluster's tick. A lease another replica holds, a
// mapping that is no longer valid, and a refused commit end the tick
// quietly; only a failure to read or write the store is returned.
func (p *Poller) tickCluster(ctx context.Context, clusterID string) error {
	lease, err := p.store.AcquireOrRenewPollerLease(ctx, clusterID, p.holder, pollerLeaseTTL)
	if errors.Is(err, store.ErrRubrikPollerLeaseHeld) {
		return nil
	}
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.held[clusterID] = true
	p.mu.Unlock()

	// The mapping is read before the state. The commit is pinned to this
	// read, so a re-map that lands after it is refused at commit, and one
	// that landed before it already cleared the state read next. Reading
	// the state first would let a re-map in between write the previous
	// target's cursor and conditions back under the new one.
	t, err := p.svc.Resolve(ctx, clusterID)
	switch {
	case errors.Is(err, ErrNotConfigured), errors.Is(err, ErrNotMapped), errors.Is(err, ErrMappingInvalid):
		return nil
	case err != nil:
		return err
	}
	// The state is read under the lease, so it is the one the previous
	// holder's last commit wrote.
	prev, found, err := p.store.GetPollerState(ctx, clusterID)
	if err != nil {
		return err
	}
	// One instant decides the window, the gap and the judgments.
	in := tickInput{Now: p.clock(), Mapping: t.Mapping, Prev: prev, HasPrev: found}
	if in.first() {
		in.Obs, err = p.observe(ctx, t, nil)
	} else {
		in.Obs, err = p.observe(ctx, t, in.failureWindow())
	}
	if ctx.Err() != nil {
		if !errors.Is(context.Cause(ctx), errPollerClusterDeadline) {
			return nil // shutting down: commit nothing
		}
		// RSC was too slow to read inside the deadline. That counts against
		// the connection like any timeout, so a tenant that is always this
		// slow opens connection_failing instead of going quiet. The lease
		// outlives the deadline; the commit gets a short context of its own.
		err = fmt.Errorf("%w: %w", ErrUnreachable, context.Cause(ctx))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), pollerCommitTimeout)
		defer cancel()
	}
	var tick store.RubrikPollerTick
	if err != nil {
		if !countsAsConnectionFailure(err) {
			p.logger.Info("rubrik poller: tick skipped", "cluster", clusterID, "reason", errorClass(err), "error", err)
			return nil
		}
		p.logger.Warn("rubrik poller: rsc read failed", "cluster", clusterID, "reason", errorClass(err), "error", err)
		tick = planFailedTick(in)
	} else {
		states, err := p.store.ListComplianceStates(ctx, clusterID)
		if err != nil {
			return err
		}
		in.PrevStates = states
		tick = planTick(in)
	}
	recorded, err := p.store.CommitPollerTick(ctx, lease, tick)
	switch {
	case errors.Is(err, store.ErrRubrikPollerLeaseLost), errors.Is(err, store.ErrRubrikPollerMappingChanged):
		p.logger.Info("rubrik poller: tick discarded", "cluster", clusterID, "error", err)
		return nil
	case err != nil:
		return err
	}
	if len(recorded) > 0 {
		p.logger.Info("rubrik poller: recorded events", "cluster", clusterID, "events", len(recorded))
	}
	return nil
}

// countsAsConnectionFailure reports whether a failed read says the RSC
// connection is failing: the tenant could not be reached, refused the
// credentials, or answered with errors or nothing usable. A rate limit is
// RSC working as intended, and store, configuration and mapping failures
// say nothing about the connection.
func countsAsConnectionFailure(err error) bool {
	switch {
	case errors.Is(err, ErrRateLimited):
		return false
	case errors.Is(err, ErrUnreachable), errors.Is(err, ErrAuthFailed), errors.Is(err, ErrInvalidTenantURL),
		errors.Is(err, ErrGraphQL), errors.Is(err, ErrUnreadable), errors.Is(err, context.DeadlineExceeded):
		return true
	}
	return false
}

// observation is what a tick read from RSC.
type observation struct {
	Objects []Object
	// Frequencies maps a lower-cased SLA domain id to its most frequent
	// schedule interval.
	Frequencies map[string]time.Duration
	// Failures are the failed snapshot jobs of the cluster's objects that
	// failed inside the window. Nil on a first tick.
	Failures []Job
}

// window is the span a tick reads failures from: jobs started after
// StartedAfter that failed after FailedAfter.
type window struct {
	StartedAfter time.Time
	FailedAfter  time.Time
}

// observe reads the cluster's objects and SLA domains, and with a window
// its failed snapshot jobs. A cached answer whose latest fetch failed is a
// failure here: the poller judges only what RSC said this tick.
func (p *Poller) observe(ctx context.Context, t Target, w *window) (*observation, error) {
	view, err := p.svc.Objects(ctx, t)
	if err != nil {
		return nil, err
	}
	if view.Err != nil {
		return nil, view.Err
	}
	slas, f, err := p.svc.SLADomains(ctx, t)
	if err != nil {
		return nil, err
	}
	if f.Err != nil {
		return nil, f.Err
	}
	obs := &observation{Objects: view.Objects, Frequencies: make(map[string]time.Duration, len(slas))}
	for _, d := range slas {
		obs.Frequencies[strings.ToLower(d.ID)] = d.Frequency
	}
	if w == nil {
		return obs, nil
	}
	ids := make([]string, 0, len(view.Objects))
	own := make(map[string]bool, len(view.Objects))
	for _, o := range view.Objects {
		key := strings.ToLower(o.ID)
		if own[key] || !validUUID(o.ID) {
			continue // the activity filter takes uuids only
		}
		own[key] = true
		ids = append(ids, o.ID)
	}
	startedAfter := w.StartedAfter
	seen := map[string]bool{}
	for len(ids) > 0 {
		batch := ids[:min(pollerObjectBatch, len(ids))]
		ids = ids[len(batch):]
		jobs, err := p.svc.rsc.Jobs(ctx, ActivityFilter{
			ObjectIDs: batch, Statuses: pollerFailureStatuses, StartedAfter: &startedAfter, Limit: MaxSnapshotLimit,
		})
		if err != nil {
			return nil, err
		}
		if len(jobs) >= MaxSnapshotLimit {
			p.logger.Warn("rubrik poller: failed-job list hit its cap; older failures in the window are not raised",
				"cluster", t.ClusterID, "cap", MaxSnapshotLimit)
		}
		asked := make(map[string]bool, len(batch))
		for _, id := range batch {
			asked[strings.ToLower(id)] = true
		}
		for _, j := range jobs {
			// Only jobs of the objects this query named, each series once.
			key := strings.ToLower(j.ID)
			if asked[strings.ToLower(j.ObjectID)] && !seen[key] && j.Status == JobFailed &&
				strings.EqualFold(j.Type, activityBackup) && failedAt(j).After(w.FailedAfter) {
				seen[key] = true
				obs.Failures = append(obs.Failures, j)
			}
		}
	}
	return obs, nil
}

// failedAt is when a job failed: its last update, else its start. A job
// with neither reads as failing at the zero time, outside every window.
func failedAt(j Job) time.Time {
	switch {
	case j.LastUpdated != nil:
		return *j.LastUpdated
	case j.StartTime != nil:
		return *j.StartTime
	}
	return time.Time{}
}

// tickInput is everything planTick and planFailedTick decide from.
type tickInput struct {
	Now     time.Time
	Mapping store.RubrikClusterMapping
	// Prev is the cluster's state as the previous commit left it; HasPrev
	// is false before the first commit.
	Prev    store.RubrikPollerState
	HasPrev bool
	// PrevStates are the persisted compliance states; read only for a
	// successful tick.
	PrevStates []store.RubrikComplianceState
	// Obs is what the tick read; nil for a failed tick.
	Obs *observation
}

// first reports whether no tick has read RSC successfully yet.
func (in tickInput) first() bool {
	return !in.HasPrev || in.Prev.ActivityCursor == nil
}

// gapStart returns the cursor when it is older than the catch-up window.
func (in tickInput) gapStart() (time.Time, bool) {
	if in.first() {
		return time.Time{}, false
	}
	cursor := in.Prev.ActivityCursor.UTC()
	return cursor, in.Now.Sub(cursor) > pollerCatchUp
}

// failureWindow returns the span a non-first tick reads failures from.
func (in tickInput) failureWindow() *window {
	from := in.Prev.ActivityCursor.UTC()
	if _, ok := in.gapStart(); ok {
		from = in.Now.Add(-pollerCatchUp)
	}
	failedAfter := from.Add(-pollerFailureSlack)
	return &window{StartedAfter: failedAfter.Add(-pollerJobLookback), FailedAfter: failedAfter}
}

// transitionKey identifies the tick a transition happened in, for the
// dedup key of a condition event. It is the previous commit's update time,
// which every replica reads the same from the state it ticks from, and
// which no later tick shares.
func (in tickInput) transitionKey() string {
	if !in.HasPrev {
		return "first"
	}
	return in.Prev.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// baseTick is the tick's identity and the state it starts from.
func (in tickInput) baseTick() store.RubrikPollerTick {
	state := in.Prev
	state.ClusterID, state.LastTickAt = "", nil
	return store.RubrikPollerTick{
		RSCClusterID:     in.Mapping.RSCClusterID,
		MappingUpdatedAt: in.Mapping.UpdatedAt,
		State:            state,
	}
}

// planFailedTick is a tick whose RSC reads failed in a way that counts
// against the connection: the streak grows, the condition opens at the
// threshold, and nothing else changes.
func planFailedTick(in tickInput) store.RubrikPollerTick {
	tick := in.baseTick()
	tick.State.ConnectionFailureStreak++
	if tick.State.ConnectionFailureStreak >= pollerConnectionThreshold && !tick.State.ConnectionFailing {
		tick.State.ConnectionFailing = true
		tick.Events = append(tick.Events, store.RubrikPollerEvent{
			Kind: store.RubrikEventConnectionFailing, DedupKey: in.transitionKey(), OccurredAt: in.Now,
			Detail: detail(map[string]any{"consecutiveFailures": tick.State.ConnectionFailureStreak}),
		})
	}
	return tick
}

// planTick is a tick whose RSC reads succeeded.
func planTick(in tickInput) store.RubrikPollerTick {
	tick := in.baseTick()
	key := in.transitionKey()
	if tick.State.ConnectionFailing {
		tick.Events = append(tick.Events, store.RubrikPollerEvent{
			Kind: store.RubrikEventConnectionRestored, DedupKey: key, OccurredAt: in.Now,
		})
	}
	tick.State.ConnectionFailureStreak, tick.State.ConnectionFailing = 0, false
	first := in.first()
	now := in.Now.UTC()
	tick.State.ActivityCursor = &now

	byID := make(map[string]Object, len(in.Obs.Objects))
	for _, o := range in.Obs.Objects {
		if k := strings.ToLower(o.ID); byID[k].ID == "" {
			byID[k] = o
		}
	}

	if gap, ok := in.gapStart(); ok {
		end := in.Now.Add(-pollerCatchUp).UTC()
		tick.Events = append(tick.Events, store.RubrikPollerEvent{
			Kind: store.RubrikEventMonitoringGap, DedupKey: gap.Format(time.RFC3339Nano), OccurredAt: in.Now,
			Detail: detail(map[string]any{"gapStart": gap, "gapEnd": end}),
		})
	}
	if !first {
		for _, j := range in.Obs.Failures {
			o := byID[strings.ToLower(j.ObjectID)]
			d := map[string]any{"jobId": j.ID, "objectId": j.ObjectID, "failedAt": failedAt(j).UTC()}
			if j.StartTime != nil {
				d["startedAt"] = j.StartTime.UTC()
			}
			tick.Events = append(tick.Events, store.RubrikPollerEvent{
				Kind: store.RubrikEventSnapshotFailed, RSCObjectID: j.ObjectID, Namespace: eventNamespace(o),
				DedupKey: strings.ToLower(j.ID), OccurredAt: failedAt(j), Detail: detail(d),
			})
		}
	}

	prevByID := make(map[string]store.RubrikComplianceState, len(in.PrevStates))
	for _, s := range in.PrevStates {
		prevByID[strings.ToLower(s.RSCObjectID)] = s
	}
	judged := make(map[string]bool, len(in.Obs.Objects))
	for _, o := range in.Obs.Objects {
		k := strings.ToLower(o.ID)
		if judged[k] || !o.Protecting() {
			continue
		}
		judged[k] = true
		prev, hasPrev := prevByID[k]
		judgment := EvaluateCompliance(o.NewestSnapshot, in.Obs.Frequencies[strings.ToLower(o.EffectiveSLA.ID)], in.Now)
		next, kind := stepCompliance(prev, hasPrev, judgment)
		next.RSCObjectID, next.Namespace = o.ID, eventNamespace(o)
		tick.Compliance = append(tick.Compliance, next)
		if kind != "" {
			d := map[string]any{"objectId": o.ID}
			if o.NewestSnapshot != nil {
				d["newestSnapshot"] = o.NewestSnapshot.UTC()
			}
			tick.Events = append(tick.Events, store.RubrikPollerEvent{
				Kind: kind, RSCObjectID: o.ID, Namespace: next.Namespace,
				DedupKey: k + "|" + key, OccurredAt: in.Now, Detail: detail(d),
			})
		}
	}
	tick.PruneCompliance = true
	return tick
}

// stepCompliance applies one judgment to an object's persisted state and
// returns the next state and the event kind it raises, if any. LateStreak
// counts consecutive out-of-compliance judgments, saturating at the
// threshold; reaching it opens the condition. Every object starts closed,
// the cluster's first tick included (the plan's Baseline --> Closed), so an
// object already out when monitoring starts is late after one tick and opens
// on the next, and its later recovery closes a condition that was raised.
func stepCompliance(prev store.RubrikComplianceState, hasPrev bool, j Compliance) (store.RubrikComplianceState, string) {
	switch j {
	case ComplianceInCompliance:
		next := store.RubrikComplianceState{Status: store.RubrikComplianceIn}
		if hasPrev && prev.Status == store.RubrikComplianceOut {
			return next, store.RubrikEventComplianceRestored
		}
		return next, ""
	case ComplianceOutOfCompliance:
		if hasPrev && prev.Status == store.RubrikComplianceOut {
			return store.RubrikComplianceState{Status: store.RubrikComplianceOut, LateStreak: pollerComplianceThreshold}, ""
		}
		streak := 1
		if hasPrev && prev.Status == store.RubrikComplianceLate {
			streak = min(prev.LateStreak+1, pollerComplianceThreshold)
		}
		if streak >= pollerComplianceThreshold {
			return store.RubrikComplianceState{Status: store.RubrikComplianceOut, LateStreak: streak}, store.RubrikEventComplianceLost
		}
		return store.RubrikComplianceState{Status: store.RubrikComplianceLate, LateStreak: streak}, ""
	}
	// Unknown: the judgment cannot be made, so the persisted state stands.
	if hasPrev {
		return store.RubrikComplianceState{Status: prev.Status, LateStreak: prev.LateStreak}, ""
	}
	return store.RubrikComplianceState{Status: store.RubrikComplianceUnknown}, ""
}

// eventNamespace is the namespace an event or state for o names: its one
// namespace when o is a current, namespace-scoped object, and none (which
// is admin-only) for a relic, a multi-namespace or unresolved set, or an
// object RSC no longer lists (the zero Object).
func eventNamespace(o Object) string {
	if o.ID == "" || o.Relic || !o.NamespaceScoped() {
		return ""
	}
	return o.Coverage.Namespaces[0]
}

// detail encodes an event's facts. Every value is one the poller computed
// or an RSC id or time; none is tenant text.
func detail(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
