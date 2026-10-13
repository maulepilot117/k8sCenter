package policy

// The remote compliance recorder (#608, KTD3). Once at startup and every
// midnight UTC it takes one snapshot of each remote cluster that has a named
// background identity, impersonating that identity, and writes a point or a
// gap through the guarded store writes (KTD5). It is separate from the local
// ComplianceRecorder, which is unchanged (R11).

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/store"
)

// Gap reasons the recorder decides itself (KTD4 steps 1 and 2). The read's
// own reasons are in remote_record.go.
const (
	gapImpersonationRefused = "impersonation_refused"
	gapCredentialsInvalid   = string(k8s.ReasonCredentialsInvalid)
)

const (
	// remoteSnapshotTimeout bounds one cluster's snapshot, pre-check and read
	// together. A cluster that runs out of time records a gap unreachable.
	remoteSnapshotTimeout = 2 * time.Minute
	// remoteWriteTimeout bounds each store write. Writes get their own
	// context so a gap can still be written after a cluster's deadline.
	remoteWriteTimeout = 10 * time.Second
	remoteRecorderTask = "remote compliance snapshot"
)

// ComplianceIdentitySource reads the per-cluster background identities.
type ComplianceIdentitySource interface {
	List(ctx context.Context) ([]store.ComplianceIdentity, error)
	Get(ctx context.Context, clusterID string) (store.ComplianceIdentity, error)
}

// RemoteComplianceWriter writes remote points and gaps under the KTD5 rules
// and ages out old rows.
type RemoteComplianceWriter interface {
	InsertRemote(ctx context.Context, snap *store.ComplianceSnapshot) (bool, error)
	InsertGap(ctx context.Context, clusterID string, date time.Time, reason string, revision int64) (bool, error)
	Cleanup(ctx context.Context, retentionDays int) (int64, error)
}

// ImpersonationChecker proves a remote cluster's registration credential may
// impersonate an identity (KTD2). *k8s.ClusterRouter implements it.
type ImpersonationChecker interface {
	CanImpersonate(ctx context.Context, clusterID, username string, groups []string) error
}

// remoteRecordReader is the recorder-only policy read. *Handler implements it.
type remoteRecordReader interface {
	readRemoteForRecord(ctx context.Context, clusterID, username string, groups []string) (*remoteRecord, error)
}

// RemoteComplianceRecorder takes the daily compliance snapshots of remote
// clusters. Build it with NewRemoteComplianceRecorder.
type RemoteComplianceRecorder struct {
	identities ComplianceIdentitySource
	snapshots  RemoteComplianceWriter
	checker    ImpersonationChecker
	reader     remoteRecordReader
	logger     *slog.Logger
	now        func() time.Time
	timeout    time.Duration
	// inflight counts SnapshotNow goroutines, so tests can wait for them.
	inflight sync.WaitGroup
	// mu guards now: a cluster is a key while its SnapshotNow runs, and its
	// value says another save asked for a snapshot meanwhile.
	mu         sync.Mutex
	nowRunning map[string]bool
}

// NewRemoteComplianceRecorder returns a recorder over the given stores, the
// router's impersonation check and the policy handler's read, or nil when any
// of them is missing (no database, or policy not wired). Taking concrete
// pointers keeps a nil one from becoming a non-nil interface.
func NewRemoteComplianceRecorder(identities *store.ComplianceIdentityStore, snapshots *store.ComplianceStore, router *k8s.ClusterRouter, h *Handler, logger *slog.Logger) *RemoteComplianceRecorder {
	if identities == nil || snapshots == nil || router == nil || h == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RemoteComplianceRecorder{
		identities: identities,
		snapshots:  snapshots,
		checker:    router,
		reader:     h,
		logger:     logger,
		now:        time.Now,
		timeout:    remoteSnapshotTimeout,
	}
}

// Run takes a pass at startup and then every midnight UTC, until ctx ends.
func (r *RemoteComplianceRecorder) Run(ctx context.Context) {
	recoverutil.Tick(ctx, r.logger, remoteRecorderTask, r.runDaily)
	for {
		timer := time.NewTimer(durationUntilNextUTC(snapshotHourUTC))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			recoverutil.Tick(ctx, r.logger, remoteRecorderTask, r.runDaily)
		}
	}
}

// SnapshotNow takes one snapshot of clusterID in the background and returns
// at once (R3). The snapshot keeps ctx's values but not its cancellation, so
// it outlives the request that asked for it, bounded by its own deadline. A
// cluster with no identity is not read.
//
// Calls for a cluster whose snapshot is still running coalesce: however many
// arrive, one more snapshot follows the running one, and it reads the
// identity current when it starts. Repeated saves so cannot stack remote
// reads against one cluster.
func (r *RemoteComplianceRecorder) SnapshotNow(ctx context.Context, clusterID string) {
	r.mu.Lock()
	if r.nowRunning == nil {
		r.nowRunning = make(map[string]bool)
	}
	if _, running := r.nowRunning[clusterID]; running {
		r.nowRunning[clusterID] = true
		r.mu.Unlock()
		return
	}
	r.nowRunning[clusterID] = false
	r.mu.Unlock()

	bgParent := context.WithoutCancel(ctx)
	r.inflight.Add(1)
	go func() {
		defer r.inflight.Done()
		for {
			recoverutil.Safe(r.logger, remoteRecorderTask, func() { r.snapshotNowOnce(bgParent, clusterID) })
			r.mu.Lock()
			if !r.nowRunning[clusterID] {
				delete(r.nowRunning, clusterID)
				r.mu.Unlock()
				return
			}
			r.nowRunning[clusterID] = false
			r.mu.Unlock()
		}
	}()
}

// snapshotNowOnce reads clusterID's current identity and snapshots it.
func (r *RemoteComplianceRecorder) snapshotNowOnce(parent context.Context, clusterID string) {
	bg, cancel := context.WithTimeout(parent, r.timeout+remoteWriteTimeout)
	defer cancel()
	id, err := r.identities.Get(bg, clusterID)
	if errors.Is(err, store.ErrComplianceIdentityNotFound) {
		return
	}
	if err != nil {
		r.logger.Error("remote compliance snapshot: reading identity failed", "cluster", clusterID, "error", err)
		return
	}
	r.snapshotCluster(bg, id)
}

// wait blocks until every SnapshotNow started so far has finished.
func (r *RemoteComplianceRecorder) wait() { r.inflight.Wait() }

// runDaily snapshots every cluster with an identity in turn, then ages out
// rows past retention. Cleanup runs here as well as in the local recorder,
// which calls it only after a successful local snapshot, so remote retention
// does not depend on the local cluster's health (KTD7).
func (r *RemoteComplianceRecorder) runDaily(ctx context.Context) {
	ids, err := r.identities.List(ctx)
	if err != nil {
		r.logger.Error("remote compliance snapshot: listing identities failed", "error", err)
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		recoverutil.Tick(ctx, r.logger, remoteRecorderTask, func(ctx context.Context) {
			r.snapshotCluster(ctx, id)
		})
	}
	if ctx.Err() != nil {
		return
	}
	deleted, err := r.snapshots.Cleanup(ctx, snapshotRetentionDays)
	if err != nil {
		r.logger.Warn("remote compliance snapshot: cleanup failed", "error", err)
	} else if deleted > 0 {
		r.logger.Info("remote compliance snapshot: cleaned up old rows", "deleted", deleted)
	}
}

// snapshotCluster takes one cluster's snapshot as its identity and writes a
// point or a gap (KTD4). parent ending (shutdown) writes nothing.
func (r *RemoteComplianceRecorder) snapshotCluster(parent context.Context, id store.ComplianceIdentity) {
	date := r.now().UTC().Truncate(24 * time.Hour)
	log := r.logger.With("cluster", id.ClusterID, "revision", id.Revision, "date", date.Format(time.DateOnly))

	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()

	if err := r.checker.CanImpersonate(ctx, id.ClusterID, id.Username, id.Groups); err != nil {
		if errors.Is(err, k8s.ErrImpersonationDenied) {
			r.writeGap(parent, log, id, date, gapImpersonationRefused)
			return
		}
		r.failed(parent, log, id, date, err)
		return
	}

	rec, err := r.reader.readRemoteForRecord(ctx, id.ClusterID, id.Username, id.Groups)
	if err != nil {
		r.failed(parent, log, id, date, err)
		return
	}
	if rec.Outcome == recordGap {
		r.writeGap(parent, log, id, date, rec.GapReason)
		return
	}

	unreadable := make([]string, 0, len(rec.Unreadable))
	for _, e := range rec.Unreadable {
		unreadable = append(unreadable, string(e))
	}
	snap := &store.ComplianceSnapshot{
		Date:             date,
		ClusterID:        id.ClusterID,
		OverallScore:     rec.Score.Score,
		Pass:             rec.Score.Pass,
		Fail:             rec.Score.Fail,
		Warn:             rec.Score.Warn,
		Total:            rec.Score.Total,
		Username:         id.Username,
		Groups:           id.Groups,
		IdentityRevision: id.Revision,
		Partial:          rec.Outcome == recordPartial,
		Unreadable:       unreadable,
	}
	wctx, wcancel := context.WithTimeout(parent, remoteWriteTimeout)
	defer wcancel()
	stored, err := r.snapshots.InsertRemote(wctx, snap)
	switch {
	case err != nil:
		log.Error("remote compliance snapshot: writing point failed", "error", err)
	case !stored:
		log.Info("remote compliance snapshot: point not stored, the identity changed or a newer point exists")
	default:
		log.Info("remote compliance snapshot recorded", "score", snap.OverallScore, "partial", snap.Partial)
	}
}

// failed turns an error from the pre-check or the read into a gap, or into
// nothing when the error says nothing about the cluster.
func (r *RemoteComplianceRecorder) failed(parent context.Context, log *slog.Logger, id store.ComplianceIdentity, date time.Time, err error) {
	if parent.Err() != nil {
		// Shutdown (or SnapshotNow's own bound): not the cluster's failure.
		return
	}
	var reason string
	switch {
	case errors.Is(err, errRemoteBudget):
		reason = gapDiscoveryUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		// The per-cluster deadline ran out.
		reason = gapUnreachable
	default:
		switch code := k8s.ClassifyTargetErr(err); code {
		case k8s.ReasonUnreachable:
			reason = gapUnreachable
		case k8s.ReasonCredentialsInvalid:
			reason = gapCredentialsInvalid
		default:
			// cluster_unknown (deleted mid-run) or db_unavailable: there is
			// no cluster or no store to record a gap against.
			log.Warn("remote compliance snapshot skipped", "reason", string(code))
			return
		}
	}
	log.Warn("remote compliance snapshot failed", "reason", reason, "error", err)
	r.writeGap(parent, log, id, date, reason)
}

func (r *RemoteComplianceRecorder) writeGap(parent context.Context, log *slog.Logger, id store.ComplianceIdentity, date time.Time, reason string) {
	wctx, cancel := context.WithTimeout(parent, remoteWriteTimeout)
	defer cancel()
	stored, err := r.snapshots.InsertGap(wctx, id.ClusterID, date, reason, id.Revision)
	switch {
	case err != nil:
		log.Error("remote compliance snapshot: writing gap failed", "reason", reason, "error", err)
	case !stored:
		log.Info("remote compliance snapshot: gap not stored, the identity changed or a point exists", "reason", reason)
	default:
		log.Info("remote compliance snapshot gap recorded", "reason", reason)
	}
}
