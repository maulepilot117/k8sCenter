package externalsecrets

import (
	"context"
	"crypto/sha256"
	"sort"
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
// fed into prevBucket. 24h matches the operator-paging cadence — beyond
// that, the operator has either acknowledged or moved on.
const recoverySeedWindow = 24 * time.Hour

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
// We seed by (ns, name) rather than UID because nc_notifications has no
// UID column — the notification was emitted with ResourceNS/ResourceName
// as the public identity. The first tick's check() does the (ns,name)
// -> UID translation when it observes the live ES.
func (p *Poller) seedFromNotifications(ctx context.Context) {
	if p.notifStore == nil {
		return
	}
	since := time.Now().Add(-recoverySeedWindow)
	notifs, err := p.notifStore.RecentBySource(ctx, notifications.SourceExternalSecrets, since)
	if err != nil {
		p.logger.Warn("externalsecrets poller: prev-bucket seed failed (continuing without)", "error", err)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seedNSName == nil {
		p.seedNSName = make(map[string]statusBucket, len(notifs))
	}
	for _, n := range notifs {
		key := n.ResourceNS + "/" + n.ResourceName
		p.seedNSName[key] = bucketFromNotification(n)
	}
	p.logger.Info("externalsecrets poller: seeded prev-bucket from recent notifications",
		"count", len(p.seedNSName),
		"window", recoverySeedWindow)
}

// bucketFromNotification reconstructs the operational bucket from a
// persisted notification. The schema has no `kind` column, so we decode
// from severity + title — the title strings are stable and set by
// failureRecord / lifecycleRecord / recoveryRecord.
func bucketFromNotification(n notifications.Notification) statusBucket {
	switch n.Title {
	case "ExternalSecret sync failed":
		return bucketFailed
	case "ExternalSecret stale":
		return bucketStale
	case "ExternalSecret unhealthy":
		return bucketUnknown
	case "ExternalSecret recovered", "ExternalSecret first synced":
		return bucketHealthy
	}
	// Fallback by severity for forward compatibility — a future title
	// rename shouldn't silently break the seed.
	switch n.Severity {
	case notifications.SeverityCritical:
		return bucketFailed
	case notifications.SeverityWarning:
		return bucketStale
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
// ON CONFLICT DO NOTHING in the underlying SQL means re-observed
// attempts (same UID, same lastRefreshTime) are dropped at the DB layer.
// The in-process prevAttemptAt map skips the Secret fetch when the
// attempt hasn't advanced — saves ~1 GET per ES per minute in steady
// state.
//
// Errors are logged per-ES and swallowed: a failed Secret fetch (RBAC,
// transient) degrades to "outcome only, no diff" rather than dropping
// the timeline entry. A failed INSERT logs but doesn't retry — the next
// tick's INSERT covers it (or skips if attempt_at is unchanged).
func (p *Poller) persistAttempts(ctx context.Context, ess []ExternalSecret) {
	if p.historyStore == nil {
		return
	}
	for _, es := range ess {
		entry, ok := p.buildHistoryEntry(ctx, es)
		if !ok {
			continue
		}
		if err := p.historyStore.Insert(ctx, entry); err != nil {
			p.logger.Warn("externalsecrets poller: history insert failed",
				"uid", es.UID,
				"namespace", es.Namespace,
				"name", es.Name,
				"error", err)
		}
	}
}

// buildHistoryEntry decides whether to record an attempt for this ES and
// constructs the row. Returns ok=false when the ES is in a non-recordable
// state (Refreshing / Stale / Unknown — these don't represent a NEW
// attempt) or when the attempt_at hasn't advanced since the last
// persisted row.
func (p *Poller) buildHistoryEntry(ctx context.Context, es ExternalSecret) (store.ESOSyncHistoryEntry, bool) {
	outcome, recordable := outcomeFor(es.Status)
	if !recordable {
		return store.ESOSyncHistoryEntry{}, false
	}

	attemptAt := attemptTimeFor(es)

	p.mu.Lock()
	prevAt, hadPrev := p.prevAttemptAt[es.UID]
	p.mu.Unlock()

	if hadPrev && prevAt.Equal(attemptAt) {
		return store.ESOSyncHistoryEntry{}, false
	}

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
		DiffKeysAdded:         added,
		DiffKeysRemoved:       removed,
		DiffKeysChanged:       changed,
		SyncedResourceVersion: es.SyncedResourceVersion,
	}

	p.mu.Lock()
	p.prevAttemptAt[es.UID] = attemptAt
	p.mu.Unlock()

	return entry, true
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

// attemptTimeFor resolves the attempt timestamp. Prefers the controller's
// LastSyncTime when populated (matches operator intuition: "this is when
// ESO last tried"), falls back to wall-clock now() truncated to second
// granularity (matches the unique index's effective resolution).
func attemptTimeFor(es ExternalSecret) time.Time {
	if es.LastSyncTime != nil && !es.LastSyncTime.IsZero() {
		return es.LastSyncTime.UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
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

	sec, err := cs.CoreV1().Secrets(es.Namespace).Get(ctx, es.TargetSecretName, metav1.GetOptions{})
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
func (p *Poller) RunRetention(ctx context.Context) {
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
