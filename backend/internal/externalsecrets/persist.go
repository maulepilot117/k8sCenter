package externalsecrets

import (
	"context"
	"crypto/sha256"
	"sort"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/store"
)

// historyRetentionDays is the maximum age of rows kept in eso_sync_history.
// Matches the audit-log and notifications retention policy (90 days).
const historyRetentionDays = 90

// recoverySeedWindow is how far back the restart-recovery seed looks. A
// failure event older than this is presumed already resolved or noticed,
// and would generate a misleading recovery dispatch on the next tick if
// fed into prevBucket.
//
// Narrowed from 24h (initial Phase C ship) to 2h after the adversarial
// review surfaced a phantom-recovery scenario: an ES deleted and
// recreated (same ns/name, new UID) within the seed window inherits the
// old UID's bucket via the (ns,name) seed key. 2h still covers the
// realistic restart-recovery case (process restart while a real failure
// is open) without crossing typical operator turnaround on a delete and
// recreate.
const recoverySeedWindow = 2 * time.Hour

// secretFetchTimeout caps each impersonated/SA Secret GET inside
// resolveDiffKeys. A wedged API server should not stall the entire
// persist pass. Matches handler.fetchTimeout precedent (10s) but
// halved because we issue one Get per ES rather than 5 concurrent CRD
// lists.
const secretFetchTimeout = 5 * time.Second

// persistConcurrency caps in-flight Secret GETs inside persistAttempts.
// Mirrors emitConcurrency (10) — at 1000 ESes per cluster, this turns a
// 50-second worst-case serial pass into a 5-second parallel one without
// blowing past pgxpool or the API server.
const persistConcurrency = 10

// seedEntry carries the bucket and the source-notification timestamp.
// The timestamp lets check() distinguish a seed for the SAME ES (still
// existing across restart) from a seed for a DELETED ES whose name was
// reused: the live ES's metadata.creationTimestamp must be earlier than
// the seed's NotifTime, otherwise the live ES is fresh and shouldn't
// inherit the deleted ES's bucket state.
type seedEntry struct {
	Bucket    statusBucket
	NotifTime time.Time
}

// seedFromNotifications populates seedNSName from recently-persisted
// notifications. Called once before the first tick. The first
// observation of each ES in tick() consumes its matching seed entry and
// uses it as the "previous bucket" — without this, a recovery that
// crossed a process restart would observe Healthy with no prior state
// and silently swallow the recovery dispatch.
//
// Safe no-op when notifStore is nil. Errors are logged and swallowed:
// a partial seed is preferable to refusing to start.
//
// Filters by p.clusterID so a multi-cluster deployment doesn't seed one
// cluster's prev-bucket with another cluster's notifications (an ES at
// the same ns/name in a different cluster is unrelated).
//
// We seed by (ns, name) rather than UID because nc_notifications has no
// UID column — the notification was emitted with ResourceNS/ResourceName
// as the public identity. The first tick's check() does the (ns,name)
// -> UID translation when it observes the live ES, validating the seed
// against the ES's CreationTimestamp before applying it.
func (p *Poller) seedFromNotifications(ctx context.Context) {
	if p.notifStore == nil {
		return
	}
	since := time.Now().Add(-recoverySeedWindow)
	notifs, err := p.notifStore.RecentBySource(ctx, notifications.SourceExternalSecrets, p.clusterID, since)
	if err != nil {
		p.logger.Warn("externalsecrets poller: prev-bucket seed failed (continuing without)", "error", err)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seedNSName == nil {
		p.seedNSName = make(map[string]seedEntry, len(notifs))
	}
	for _, n := range notifs {
		key := n.ResourceNS + "/" + n.ResourceName
		p.seedNSName[key] = seedEntry{
			Bucket:    bucketFromNotification(n),
			NotifTime: n.CreatedAt,
		}
	}
	p.logger.Info("externalsecrets poller: seeded prev-bucket from recent notifications",
		"count", len(p.seedNSName),
		"window", recoverySeedWindow)
}

// bucketFromNotification reconstructs the operational bucket from a
// persisted notification. The schema has no `kind` column, so we decode
// from severity + title — the title strings are constants in poller.go
// (Title*) so this decoder stays in sync if a title is renamed.
//
// The severity fallback exists for forward compatibility: a future
// notification kind not yet covered by the title switch falls back to a
// safe-by-default bucket. The fallback is intentionally conservative —
// it picks bucketUnknown for SeverityWarning rather than guessing
// between Stale and Unhealthy, since the consequence of a wrong guess
// is a phantom dispatch.
func bucketFromNotification(n notifications.Notification) statusBucket {
	switch n.Title {
	case TitleSyncFailed:
		return bucketFailed
	case TitleStale:
		return bucketStale
	case TitleUnhealthy:
		return bucketUnknown
	case TitleRecovered, TitleFirstSynced, TitleCreated:
		return bucketHealthy
	case TitleDeleted:
		// A deleted-lifecycle notification doesn't tell us about the
		// successor's state. Treat as unknown so the first observation
		// of a same-named successor establishes its own baseline
		// rather than inheriting deleted-state semantics.
		return bucketUnknown
	}
	// Fallback by severity for forward compatibility — a future title
	// rename or new event kind shouldn't silently break the seed.
	switch n.Severity {
	case notifications.SeverityCritical:
		return bucketFailed
	case notifications.SeverityWarning:
		// Warning could be Stale or Unhealthy; collapse to Unknown
		// rather than guess. isFailureBucket(bucketUnknown)=true so
		// recovery detection still fires; a phantom Stale dedupe slot
		// is avoided.
		return bucketUnknown
	case notifications.SeverityInfo:
		return bucketHealthy
	}
	return bucketUnknown
}

// persistAttempts is the Phase C extension to tick(). For each ES whose
// status maps to a recordable outcome (success / failure), compute an
// attempt_at, look up the synced k8s Secret keys (when present), compute
// a diff against the in-memory prev key-set, and INSERT a row.
//
// Concurrency: Secret fetches run with a bounded worker pool
// (persistConcurrency) mirroring dispatchEmits. Without this, a serial
// pass over 1000 ESes at 50ms/Get takes 50s — exceeding the 60s tick
// cadence and stacking ticks indefinitely.
//
// State update ordering: prevAttemptAt[uid] is committed only after the
// Insert succeeds (inside the worker goroutine, under p.mu). A failed
// INSERT leaves prevAttemptAt unchanged so the next tick re-attempts
// the same (uid, attempt_at). Without this ordering, a transient INSERT
// failure permanently skips that attempt.
//
// Errors are logged per-ES and swallowed: a failed Secret fetch (RBAC,
// transient) degrades to "outcome only, no diff" rather than dropping
// the timeline entry. A failed INSERT logs but doesn't retry — the next
// tick's INSERT covers it.
func (p *Poller) persistAttempts(ctx context.Context, ess []ExternalSecret) {
	if p.historyStore == nil {
		return
	}

	// Pre-filter: skip non-recordable states + dedup against prevAttemptAt
	// before spawning any goroutines. This avoids paying the goroutine /
	// semaphore overhead for the vast majority of ticks (steady-state
	// ESes whose lastRefreshTime hasn't advanced).
	type attemptCandidate struct {
		es        ExternalSecret
		outcome   string
		attemptAt time.Time
	}
	candidates := make([]attemptCandidate, 0, len(ess))
	p.mu.Lock()
	for _, es := range ess {
		outcome, recordable := outcomeFor(es.Status)
		if !recordable {
			continue
		}
		attemptAt, ok := attemptTimeFor(es)
		if !ok {
			// LastSyncTime not populated by the controller. The poller
			// has no controller-reported attempt to record; using
			// time.Now() would produce a fresh attempt_at on every
			// tick (defeating ON CONFLICT dedup) and cause unbounded
			// row growth for ESes with providers that never set
			// status.refreshTime. Skip until ESO populates it.
			continue
		}
		if prevAt, hadPrev := p.prevAttemptAt[es.UID]; hadPrev && prevAt.Equal(attemptAt) {
			continue
		}
		candidates = append(candidates, attemptCandidate{es: es, outcome: outcome, attemptAt: attemptAt})
	}
	p.mu.Unlock()

	if len(candidates) == 0 {
		return
	}

	sem := make(chan struct{}, persistConcurrency)
	var wg sync.WaitGroup
	for _, c := range candidates {
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
		go func(c attemptCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { _ = recover() }()
			p.persistOne(ctx, c.es, c.outcome, c.attemptAt)
		}(c)
	}
	wg.Wait()
}

// persistOne runs the Secret fetch + diff + INSERT for a single ES.
// Updates prevAttemptAt[uid] only after Insert succeeds — a failed
// INSERT leaves the in-memory map untouched so the next tick retries.
func (p *Poller) persistOne(ctx context.Context, es ExternalSecret, outcome string, attemptAt time.Time) {
	added, removed, changed := p.resolveDiffKeys(ctx, es, outcome)

	entry := store.ESOSyncHistoryEntry{
		ClusterID:             p.clusterID,
		UID:                   es.UID,
		Namespace:             es.Namespace,
		Name:                  es.Name,
		AttemptAt:             attemptAt,
		Outcome:               outcome,
		Reason:                es.ReadyReason,
		Message:               es.ReadyMessage,
		DiffKeysAdded:         emptyIfNil(added),
		DiffKeysRemoved:       emptyIfNil(removed),
		DiffKeysChanged:       emptyIfNil(changed),
		SyncedResourceVersion: es.SyncedResourceVersion,
	}

	if err := p.historyStore.Insert(ctx, entry); err != nil {
		p.logger.Warn("externalsecrets poller: history insert failed",
			"uid", es.UID,
			"namespace", es.Namespace,
			"name", es.Name,
			"error", err)
		return
	}

	p.mu.Lock()
	p.prevAttemptAt[es.UID] = attemptAt
	p.mu.Unlock()
}

// emptyIfNil normalizes a nil []string to an empty (non-nil) slice. pgx
// encodes nil slices as SQL NULL; the diff_keys_* columns are
// TEXT[] NOT NULL DEFAULT '{}' so a nil bind would violate the
// constraint. Empty slices encode as PG empty arrays cleanly.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// outcomeFor maps the derived Status to the eso_sync_history.outcome
// CHECK enum. Only transitions that represent a controller attempt are
// recordable; transient states (Refreshing) and threshold overlays
// (Stale) are skipped to keep the timeline focused on real reconcile
// outcomes.
//
// Drifted folds into success because the underlying ESO reconcile DID
// succeed — drift is a post-reconcile local-edit overlay that the next
// successful reconcile resolves.
//
// Partial outcome detection requires inspecting per-key sync results
// against the source-store path, which v1 does not implement (would
// need source-store credentials we don't hold per the scope boundary).
// The CHECK constraint accepts 'partial' for future enhancement.
func outcomeFor(s Status) (string, bool) {
	switch s {
	case StatusSynced, StatusDrifted:
		return "success", true
	case StatusSyncFailed:
		return "failure", true
	}
	return "", false
}

// attemptTimeFor resolves the attempt timestamp from the controller's
// LastSyncTime. Returns ok=false when the controller hasn't populated
// LastSyncTime — in that case there's no controller-reported attempt
// to record, and using wall-clock now() would defeat the
// ON CONFLICT dedup (every tick produces a new second-truncated
// timestamp), causing unbounded row growth for ESes whose provider
// never sets status.refreshTime.
func attemptTimeFor(es ExternalSecret) (time.Time, bool) {
	if es.LastSyncTime != nil && !es.LastSyncTime.IsZero() {
		return es.LastSyncTime.UTC().Truncate(time.Second), true
	}
	return time.Time{}, false
}

// resolveDiffKeys returns the (added, removed, changed) key-sets for
// this attempt. Returns nil slices in four cases:
//   - failure outcome (no Secret to compare against; failure wins).
//   - missing TargetSecretName (no target to fetch).
//   - first observation in this process lifetime (no prev; can't diff).
//   - Secret fetch failure (RBAC, deleted, transient — degrades).
//
// The fetched Secret data is hashed key-by-key and stored in
// p.prevKeys[uid] for next-attempt comparison. The hash is sha256 of the
// raw value bytes — values are NEVER persisted, only the hash, and only
// in-process.
//
// The Get call is wrapped with secretFetchTimeout so a wedged API
// server can't stall the entire persist pass.
func (p *Poller) resolveDiffKeys(ctx context.Context, es ExternalSecret, outcome string) ([]string, []string, []string) {
	if outcome != "success" || es.TargetSecretName == "" {
		return nil, nil, nil
	}
	if p.k8s == nil {
		return nil, nil, nil
	}

	cs := p.k8s.BaseClientset()
	if cs == nil {
		return nil, nil, nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, secretFetchTimeout)
	defer cancel()
	sec, err := cs.CoreV1().Secrets(es.Namespace).Get(fetchCtx, es.TargetSecretName, metav1.GetOptions{})
	if err != nil {
		// IsNotFound and IsForbidden are expected operational states
		// (Secret was deleted, or operator disabled the core/secrets
		// grant). Other errors are transient driver failures worth
		// logging. Record a drift hint of Unknown so the list view's
		// dashboard doesn't keep showing a stale Drifted state when
		// the poller can no longer reach the Secret.
		if p.handler != nil {
			p.handler.RecordDrift(es.UID, DriftUnknown)
		}
		if !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
			p.logger.Warn("externalsecrets poller: target secret fetch failed",
				"uid", es.UID,
				"namespace", es.Namespace,
				"target", es.TargetSecretName,
				"error", err)
		}
		return nil, nil, nil
	}

	// Drift hint: compare ES's controller-reported syncedResourceVersion
	// against the live Secret's resourceVersion. This is the same
	// computation the detail endpoint runs via the impersonated client.
	// Stash for the list endpoint to surface.
	if p.handler != nil {
		p.handler.RecordDrift(es.UID, computeDriftStatus(es.SyncedResourceVersion, sec.ResourceVersion))
	}

	current := hashSecretData(sec.Data)

	p.mu.Lock()
	prev, hadPrev := p.prevKeys[es.UID]
	p.prevKeys[es.UID] = current
	p.mu.Unlock()

	if !hadPrev {
		// First observation in this process lifetime. Per plan §470
		// the first row carries empty diff arrays — we have no
		// baseline to compare against.
		return nil, nil, nil
	}

	return diffKeySets(prev, current)
}

// hashSecretData returns a per-key sha256 of the raw value bytes. Used
// only for in-process comparison; never logged or persisted.
func hashSecretData(data map[string][]byte) map[string][32]byte {
	out := make(map[string][32]byte, len(data))
	for k, v := range data {
		out[k] = sha256.Sum256(v)
	}
	return out
}

// diffKeySets computes (added, removed, changed) by comparing per-key
// hashes. Output slices are sorted for stable assertions and
// reproducible storage.
func diffKeySets(prev, current map[string][32]byte) ([]string, []string, []string) {
	var added, removed, changed []string
	for k, currentHash := range current {
		prevHash, inPrev := prev[k]
		if !inPrev {
			added = append(added, k)
			continue
		}
		if prevHash != currentHash {
			changed = append(changed, k)
		}
	}
	for k := range prev {
		if _, inCurrent := current[k]; !inCurrent {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

// RunRetention deletes eso_sync_history rows older than
// historyRetentionDays. Caller schedules on a 1h tick from main.go. Safe
// no-op when historyStore is nil. Logs deleted-row count for operator
// visibility.
//
// Internal panic recovery: a pgx driver fault should not kill the
// retention goroutine permanently. The caller's goroutine wrapper in
// main.go also has defer recover() — this is belt-and-suspenders.
func (p *Poller) RunRetention(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("externalsecrets poller: retention panic recovered", "panic", r)
		}
	}()
	if p.historyStore == nil {
		return
	}
	deleted, err := p.historyStore.Cleanup(ctx, historyRetentionDays)
	if err != nil {
		p.logger.Error("externalsecrets poller: retention cleanup failed", "error", err)
		return
	}
	if deleted > 0 {
		p.logger.Info("externalsecrets poller: retention cleanup deleted rows",
			"deleted", deleted,
			"retention_days", historyRetentionDays)
	}
}
