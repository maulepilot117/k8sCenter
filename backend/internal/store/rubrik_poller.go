package store

// rubrik_poller.go — persistence for the Rubrik poller (plan U10, KTD10).
//
// The poller is one leased collector per mapped cluster. The lease is a
// fenced row in rubrik_poller_lease, taken and renewed on the database
// clock. Unlike the Release F collector's advisory lease, this one also
// guards the writes: CommitPollerTick locks the lease row and refuses to
// commit unless the caller still holds it with the same fence, so a
// replica that lost its lease mid-tick (a pause past the ttl, a takeover)
// writes nothing. A tick's condition states, events and delivery intents
// land in one transaction.
//
// One event per transition across replicas (R27) rests on two guards. Only
// the fenced holder commits a tick, so the hysteresis and streak state each
// tick reads is the one the previous tick wrote. And an event is unique per
// (cluster, kind, dedup_key), with a delivery intent inserted only for an
// event that was new, so re-seeing a snapshot failure series, or two
// replicas recording the same monitoring gap, adds nothing.
//
// Deliveries are claimed and marked as the Release F deliveries are:
// claiming counts as an attempt, a claimed row stays pending until marked,
// and a row whose attempts are spent is never claimed again. Sending is
// therefore at least once: a claimer that dies after sending and before
// marking leaves the row for the next claim, and nothing locks a claimed
// row once the claim commits, so another replica's drain can claim it too.
// The drain closes that window by emitting each event with ResourceUID set
// to the event id, so Notification Center's dedup drops the repeat.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Rubrik poller event kinds, as rubrik_poller_events.kind stores them.
const (
	RubrikEventSnapshotFailed     = "snapshot_failed"
	RubrikEventComplianceLost     = "compliance_lost"
	RubrikEventComplianceRestored = "compliance_restored"
	RubrikEventConnectionFailing  = "connection_failing"
	RubrikEventConnectionRestored = "connection_restored"
	RubrikEventMonitoringGap      = "monitoring_gap"
)

// Rubrik poller delivery states.
const (
	RubrikDeliveryPending   = "pending"
	RubrikDeliveryDelivered = "delivered"
	RubrikDeliveryFailed    = "failed"
)

// RubrikApplyingMinGrace is the shortest grace ExpireApplyingOperations
// accepts. A write holds its row applying while it sends the mutation and
// records the outcome, bounded by the write path's timeouts (about 65s); a
// shorter grace would move a live send to unknown and turn away its own
// settle.
const RubrikApplyingMinGrace = 2 * time.Minute

var (
	// ErrRubrikPollerLeaseHeld is returned when another replica holds the
	// cluster's poller lease live.
	ErrRubrikPollerLeaseHeld = errors.New("rubrik poller lease held by another replica")
	// ErrRubrikPollerLeaseLost is returned by CommitPollerTick when the
	// caller no longer holds the lease it ticked under. Nothing is written.
	ErrRubrikPollerLeaseLost = errors.New("rubrik poller lease lost")
	// ErrRubrikPollerMappingChanged is returned by CommitPollerTick when the
	// cluster's mapping was removed, rewritten or replaced since the tick
	// read it. Nothing is written.
	ErrRubrikPollerMappingChanged = errors.New("rubrik cluster mapping changed during the poller tick")
	// ErrRubrikPollerInvalid is returned for a tick the schema cannot store.
	ErrRubrikPollerInvalid = errors.New("invalid rubrik poller tick")
	// ErrRubrikDeliveryNotPending is returned when a delivery to mark is
	// missing or no longer pending.
	ErrRubrikDeliveryNotPending = errors.New("rubrik poller delivery is not pending")
)

// RubrikPollerLease is a cluster's rubrik_poller_lease row. Every timestamp
// is the database's, and Expired is judged on the database clock in the
// statement that read the row.
type RubrikPollerLease struct {
	ClusterID  string
	Holder     string
	Fence      int64
	AcquiredAt time.Time
	RenewedAt  time.Time
	ExpiresAt  time.Time
	Expired    bool
}

// RubrikPollerState is a cluster's poller bookkeeping (rubrik_poller_state).
// A cluster without a row has never been ticked.
type RubrikPollerState struct {
	ClusterID string
	// ActivityCursor is the start time up to which the activity series have
	// been read. Nil before the first tick.
	ActivityCursor *time.Time
	// ConnectionFailureStreak counts consecutive ticks whose RSC calls
	// failed, rate limits excepted.
	ConnectionFailureStreak int
	// ConnectionFailing is set once the streak opened the condition, and
	// cleared by the first tick that reaches RSC.
	ConnectionFailing bool
	LastTickAt        *time.Time
	UpdatedAt         time.Time
}

// RubrikPollerEvent is a notification-worthy transition
// (rubrik_poller_events). An empty Namespace makes it admin-only (KTD10).
type RubrikPollerEvent struct {
	ID          string
	ClusterID   string
	Kind        string
	RSCObjectID string
	Namespace   string
	// DedupKey identifies the transition within (cluster, kind): a
	// snapshot failure's activity series id, a gap's start, a condition's
	// transition tick. Recording a key twice records it once.
	DedupKey   string
	OccurredAt time.Time
	// Detail is the event's facts for the notification body. It is never
	// tenant error text.
	Detail    json.RawMessage
	CreatedAt time.Time
}

// RubrikPollerTick is what one tick of the cluster's poller writes.
type RubrikPollerTick struct {
	// RSCClusterID and MappingUpdatedAt identify the mapping the tick read
	// (RubrikClusterMapping's RSCClusterID and UpdatedAt). The commit
	// refuses with ErrRubrikPollerMappingChanged when the mapping now names
	// another RSC cluster or was written since, so a tick never writes one
	// target's objects under another, nor re-inserts rows a delete and
	// re-map of the same target cleared. Every mapping write moves
	// updated_at, so a refused tick costs one tick.
	RSCClusterID     string
	MappingUpdatedAt time.Time
	// State is the cluster's bookkeeping after the tick. ClusterID,
	// LastTickAt and UpdatedAt are ignored: the commit sets them.
	State RubrikPollerState
	// Compliance holds the objects' judgments, upserted. ChangedAt and
	// EvaluatedAt are ignored: the commit keeps changed_at while an
	// object's status is unchanged and stamps both on the database clock.
	Compliance []RubrikComplianceState
	// PruneCompliance deletes the cluster's states for objects not in
	// Compliance. Set it only when the tick read the cluster's full object
	// list.
	PruneCompliance bool
	// Events are the tick's transitions. ID, ClusterID and CreatedAt are
	// ignored. An event whose (kind, dedup key) is already recorded is
	// skipped, and gets no delivery.
	Events []RubrikPollerEvent
}

// RubrikPollerDelivery is one durable intent to notify one event
// (rubrik_poller_deliveries).
type RubrikPollerDelivery struct {
	ID          string
	EventID     string
	State       string
	Attempts    int
	CreatedAt   time.Time
	DeliveredAt *time.Time
	LastError   string
}

// RubrikPollerDeliveryJob is a claimed delivery with its event.
type RubrikPollerDeliveryJob struct {
	Delivery RubrikPollerDelivery
	Event    RubrikPollerEvent
}

func validRubrikEventKind(kind string) bool {
	switch kind {
	case RubrikEventSnapshotFailed, RubrikEventComplianceLost, RubrikEventComplianceRestored,
		RubrikEventConnectionFailing, RubrikEventConnectionRestored, RubrikEventMonitoringGap:
		return true
	}
	return false
}

func validRubrikComplianceStatus(status string) bool {
	switch status {
	case RubrikComplianceIn, RubrikComplianceLate, RubrikComplianceOut, RubrikComplianceUnknown:
		return true
	}
	return false
}

// pollerText refuses a required field that is empty or too long, and an
// optional one (required false) that is too long.
func pollerText(what, s string, required bool) error {
	if (required && s == "") || len(s) > rubrikOpMaxText || strings.ContainsRune(s, 0) {
		return fmt.Errorf("%w: %s must be at most %d bytes with no NUL", ErrRubrikPollerInvalid, what, rubrikOpMaxText)
	}
	return nil
}

func validatePollerTick(clusterID string, tick RubrikPollerTick) error {
	if clusterID == "" {
		return fmt.Errorf("%w: cluster id is required", ErrRubrikPollerInvalid)
	}
	if err := pollerText("rsc cluster id", tick.RSCClusterID, true); err != nil {
		return err
	}
	if tick.MappingUpdatedAt.IsZero() {
		return fmt.Errorf("%w: mapping updated-at is required", ErrRubrikPollerInvalid)
	}
	if tick.State.ConnectionFailureStreak < 0 {
		return fmt.Errorf("%w: connection failure streak is negative", ErrRubrikPollerInvalid)
	}
	seen := make(map[string]bool, len(tick.Compliance))
	for _, c := range tick.Compliance {
		if err := pollerText("rsc object id", c.RSCObjectID, true); err != nil {
			return err
		}
		if err := pollerText("namespace", c.Namespace, false); err != nil {
			return err
		}
		if !validRubrikComplianceStatus(c.Status) || c.LateStreak < 0 {
			return fmt.Errorf("%w: compliance state %q with late streak %d", ErrRubrikPollerInvalid, c.Status, c.LateStreak)
		}
		if seen[c.RSCObjectID] {
			return fmt.Errorf("%w: object %q judged twice", ErrRubrikPollerInvalid, c.RSCObjectID)
		}
		seen[c.RSCObjectID] = true
	}
	for _, e := range tick.Events {
		if !validRubrikEventKind(e.Kind) {
			return fmt.Errorf("%w: event kind %q", ErrRubrikPollerInvalid, e.Kind)
		}
		if err := pollerText("dedup key", e.DedupKey, true); err != nil {
			return err
		}
		if err := pollerText("rsc object id", e.RSCObjectID, false); err != nil {
			return err
		}
		if err := pollerText("namespace", e.Namespace, false); err != nil {
			return err
		}
		if e.OccurredAt.IsZero() {
			return fmt.Errorf("%w: event occurred-at is required", ErrRubrikPollerInvalid)
		}
		if len(e.Detail) > 0 && (len(e.Detail) > rubrikOpMaxJSONSize || !json.Valid(e.Detail)) {
			return fmt.Errorf("%w: event detail must be a JSON document of at most %d bytes", ErrRubrikPollerInvalid, rubrikOpMaxJSONSize)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lease

const rubrikPollerLeaseColumns = `cluster_id, holder, fence, acquired_at, renewed_at, expires_at, expires_at <= NOW()`

func scanRubrikPollerLease(row pgx.Row) (RubrikPollerLease, error) {
	var l RubrikPollerLease
	if err := row.Scan(&l.ClusterID, &l.Holder, &l.Fence, &l.AcquiredAt, &l.RenewedAt, &l.ExpiresAt, &l.Expired); err != nil {
		return RubrikPollerLease{}, err
	}
	return l, nil
}

// AcquireOrRenewPollerLease takes clusterID's poller lease, renews it when
// holder holds it live, or takes it over once the incumbent's lease has
// expired on the database clock. A live lease held by another holder
// returns ErrRubrikPollerLeaseHeld. A renewal keeps the fence; any
// acquisition of an expired lease, by a new holder or by the former holder
// back from a pause longer than ttl, increments it. Two replicas racing for
// an expired lease serialize on the primary key, and the fence moves by one.
func (s *RubrikStore) AcquireOrRenewPollerLease(ctx context.Context, clusterID, holder string, ttl time.Duration) (RubrikPollerLease, error) {
	if clusterID == "" || holder == "" {
		return RubrikPollerLease{}, errors.New("acquire rubrik poller lease: cluster id and holder are required")
	}
	if ttl <= 0 {
		return RubrikPollerLease{}, fmt.Errorf("acquire rubrik poller lease: ttl must be positive, got %s", ttl)
	}
	l, err := scanRubrikPollerLease(s.pool.QueryRow(ctx, `
		INSERT INTO rubrik_poller_lease AS l (cluster_id, holder, expires_at)
		VALUES ($1, $2, NOW() + make_interval(secs => $3::double precision))
		ON CONFLICT (cluster_id) DO UPDATE
		   SET holder      = EXCLUDED.holder,
		       renewed_at  = NOW(),
		       expires_at  = EXCLUDED.expires_at,
		       acquired_at = CASE WHEN l.holder = EXCLUDED.holder AND l.expires_at > NOW()
		                          THEN l.acquired_at ELSE NOW() END,
		       fence       = CASE WHEN l.holder = EXCLUDED.holder AND l.expires_at > NOW()
		                          THEN l.fence ELSE l.fence + 1 END
		 WHERE l.holder = EXCLUDED.holder OR l.expires_at <= NOW()
		RETURNING `+rubrikPollerLeaseColumns,
		clusterID, holder, ttl.Seconds()))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return RubrikPollerLease{}, ErrRubrikPollerLeaseHeld
	case err != nil:
		return RubrikPollerLease{}, fmt.Errorf("acquire rubrik poller lease: %w", err)
	}
	return l, nil
}

// ReleasePollerLease expires clusterID's lease on the database clock when
// holder still holds it live. Releasing a lease one does not hold is a
// no-op, so a shutdown after a takeover never cuts the new holder's tenure.
func (s *RubrikStore) ReleasePollerLease(ctx context.Context, clusterID, holder string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE rubrik_poller_lease
		   SET expires_at = NOW()
		 WHERE cluster_id = $1 AND holder = $2 AND expires_at > NOW()`,
		clusterID, holder); err != nil {
		return fmt.Errorf("release rubrik poller lease: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// State and tick commit

// GetPollerState reads clusterID's poller bookkeeping. found is false when
// the cluster has never been ticked: the poller's first tick records state
// and raises nothing.
func (s *RubrikStore) GetPollerState(ctx context.Context, clusterID string) (state RubrikPollerState, found bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT cluster_id, activity_cursor, connection_failure_streak, connection_failing, last_tick_at, updated_at
		  FROM rubrik_poller_state WHERE cluster_id = $1`, clusterID).Scan(
		&state.ClusterID, &state.ActivityCursor, &state.ConnectionFailureStreak, &state.ConnectionFailing,
		&state.LastTickAt, &state.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return RubrikPollerState{}, false, nil
	case err != nil:
		return RubrikPollerState{}, false, fmt.Errorf("get rubrik poller state: %w", err)
	}
	return state, true, nil
}

// CommitPollerTick writes one tick for lease's cluster in one transaction:
// the poller state, the compliance states (pruned when the tick saw every
// object), and each event not already recorded with a pending delivery for
// it. It returns the events it recorded, each with its id.
//
// It writes nothing, and returns ErrRubrikPollerLeaseLost, unless lease's
// holder still holds the lease live with the same fence. The lease row is
// locked for the transaction, so a takeover waits for the commit rather
// than racing it. It returns ErrRubrikPollerMappingChanged when the mapping
// is gone, names another RSC cluster than tick.RSCClusterID, or was written
// after tick.MappingUpdatedAt.
func (s *RubrikStore) CommitPollerTick(ctx context.Context, lease RubrikPollerLease, tick RubrikPollerTick) ([]RubrikPollerEvent, error) {
	clusterID := lease.ClusterID
	if err := validatePollerTick(clusterID, tick); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit rubrik poller tick: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fence int64
	err = tx.QueryRow(ctx, `
		SELECT fence FROM rubrik_poller_lease
		 WHERE cluster_id = $1 AND holder = $2 AND fence = $3 AND expires_at > NOW()
		   FOR UPDATE`, clusterID, lease.Holder, lease.Fence).Scan(&fence)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrRubrikPollerLeaseLost
	case err != nil:
		return nil, fmt.Errorf("commit rubrik poller tick: checking the lease: %w", err)
	}

	// FOR SHARE holds the mapping until commit: a remap or delete waits, and
	// its own cleanup of the poller tables then runs after this tick's rows.
	var mappedCluster string
	err = tx.QueryRow(ctx, `
		SELECT cluster_id FROM rubrik_cluster_mappings
		 WHERE cluster_id = $1 AND rsc_cluster_id = $2 AND updated_at = $3
		   FOR SHARE`, clusterID, tick.RSCClusterID, tick.MappingUpdatedAt).Scan(&mappedCluster)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrRubrikPollerMappingChanged
	case err != nil:
		return nil, fmt.Errorf("commit rubrik poller tick: checking the mapping: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO rubrik_poller_state AS p
		       (cluster_id, activity_cursor, connection_failure_streak, connection_failing, last_tick_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (cluster_id) DO UPDATE
		   SET activity_cursor           = EXCLUDED.activity_cursor,
		       connection_failure_streak = EXCLUDED.connection_failure_streak,
		       connection_failing        = EXCLUDED.connection_failing,
		       last_tick_at              = NOW(),
		       updated_at                = NOW()`,
		clusterID, tick.State.ActivityCursor, tick.State.ConnectionFailureStreak, tick.State.ConnectionFailing); err != nil {
		return nil, fmt.Errorf("commit rubrik poller tick: state: %w", pollerWriteError(err))
	}

	if err := upsertComplianceStates(ctx, tx, clusterID, tick.Compliance, tick.PruneCompliance); err != nil {
		return nil, err
	}

	recorded := make([]RubrikPollerEvent, 0, len(tick.Events))
	for _, e := range tick.Events {
		rec, ok, err := insertPollerEvent(ctx, tx, clusterID, e)
		if err != nil {
			return nil, err
		}
		if ok {
			recorded = append(recorded, rec)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit rubrik poller tick: %w", pollerWriteError(err))
	}
	return recorded, nil
}

// pollerWriteError reads a foreign-key violation as the mapping having gone
// since the check: the poller tables reference it.
func pollerWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		return ErrRubrikPollerMappingChanged
	}
	return err
}

func upsertComplianceStates(ctx context.Context, tx pgx.Tx, clusterID string, states []RubrikComplianceState, prune bool) error {
	ids := make([]string, len(states))
	namespaces := make([]string, len(states))
	statuses := make([]string, len(states))
	streaks := make([]int32, len(states))
	for i, c := range states {
		ids[i], namespaces[i], statuses[i], streaks[i] = c.RSCObjectID, c.Namespace, c.Status, int32(c.LateStreak)
	}
	if len(states) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rubrik_compliance_states AS c
			       (cluster_id, rsc_object_id, namespace, status, late_streak, changed_at, evaluated_at)
			SELECT $1, v.id, v.ns, v.status, v.streak, NOW(), NOW()
			  FROM unnest($2::text[], $3::text[], $4::text[], $5::int[]) AS v(id, ns, status, streak)
			ON CONFLICT (cluster_id, rsc_object_id) DO UPDATE
			   SET namespace    = EXCLUDED.namespace,
			       status       = EXCLUDED.status,
			       late_streak  = EXCLUDED.late_streak,
			       changed_at   = CASE WHEN c.status = EXCLUDED.status THEN c.changed_at ELSE NOW() END,
			       evaluated_at = NOW()`,
			clusterID, ids, namespaces, statuses, streaks); err != nil {
			return fmt.Errorf("commit rubrik poller tick: compliance states: %w", pollerWriteError(err))
		}
	}
	if prune {
		if _, err := tx.Exec(ctx, `
			DELETE FROM rubrik_compliance_states
			 WHERE cluster_id = $1 AND NOT (rsc_object_id = ANY($2::text[]))`, clusterID, ids); err != nil {
			return fmt.Errorf("commit rubrik poller tick: pruning compliance states: %w", err)
		}
	}
	return nil
}

// insertPollerEvent records e unless its (kind, dedup key) is recorded
// already, and queues its delivery. ok is false for a duplicate.
func insertPollerEvent(ctx context.Context, tx pgx.Tx, clusterID string, e RubrikPollerEvent) (RubrikPollerEvent, bool, error) {
	detail := []byte(e.Detail)
	if len(detail) == 0 {
		detail = []byte(`{}`)
	}
	id := uuid.New()
	err := tx.QueryRow(ctx, `
		INSERT INTO rubrik_poller_events (id, cluster_id, kind, rsc_object_id, namespace, dedup_key, occurred_at, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (cluster_id, kind, dedup_key) DO NOTHING
		RETURNING created_at`,
		id, clusterID, e.Kind, e.RSCObjectID, e.Namespace, e.DedupKey, e.OccurredAt, detail).Scan(&e.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return RubrikPollerEvent{}, false, nil
	case err != nil:
		return RubrikPollerEvent{}, false, fmt.Errorf("commit rubrik poller tick: event: %w", pollerWriteError(err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO rubrik_poller_deliveries (id, event_id, state)
		VALUES ($1, $2, 'pending')`, uuid.NewSHA1(id, []byte("deliver")), id); err != nil {
		return RubrikPollerEvent{}, false, fmt.Errorf("commit rubrik poller tick: delivery: %w", err)
	}
	e.ID, e.ClusterID, e.Detail = id.String(), clusterID, detail
	return e, true, nil
}

// ---------------------------------------------------------------------------
// Deliveries

const rubrikDeliveryColumns = `id, event_id, state, attempts, created_at, delivered_at, last_error`

const rubrikEventColumns = `id, cluster_id, kind, rsc_object_id, namespace, dedup_key, occurred_at, detail, created_at`

// ClaimPollerDeliveries claims up to limit of clusterID's pending
// deliveries whose attempts are below maxAttempts, oldest first, counting
// the claim as an attempt. Rows another claimer holds are skipped. A
// claimed row stays pending until MarkPollerDelivered or
// MarkPollerDeliveryFailed; a claimer that dies leaves it for the next
// claim until its attempts are spent.
func (s *RubrikStore) ClaimPollerDeliveries(ctx context.Context, clusterID string, maxAttempts, limit int) ([]RubrikPollerDeliveryJob, error) {
	if clusterID == "" {
		return nil, errors.New("claim rubrik poller deliveries: cluster id is required")
	}
	if maxAttempts <= 0 || limit <= 0 {
		return nil, fmt.Errorf("claim rubrik poller deliveries: maxAttempts (%d) and limit (%d) must be positive", maxAttempts, limit)
	}
	rows, err := s.pool.Query(ctx, `
		WITH claimed AS (
			UPDATE rubrik_poller_deliveries d
			   SET attempts = d.attempts + 1
			 WHERE d.id IN (
			       SELECT d2.id
			         FROM rubrik_poller_deliveries d2
			         JOIN rubrik_poller_events e2 ON e2.id = d2.event_id
			        WHERE d2.state = 'pending' AND d2.attempts < $1 AND e2.cluster_id = $3
			        ORDER BY d2.created_at, d2.id
			          FOR UPDATE OF d2 SKIP LOCKED
			        LIMIT $2)
			RETURNING `+qualifyColumns("d", rubrikDeliveryColumns)+`
		)
		SELECT `+qualifyColumns("c", rubrikDeliveryColumns)+`, `+qualifyColumns("e", rubrikEventColumns)+`
		  FROM claimed c
		  JOIN rubrik_poller_events e ON e.id = c.event_id
		 ORDER BY c.created_at, c.id`,
		maxAttempts, limit, clusterID)
	if err != nil {
		return nil, fmt.Errorf("claim rubrik poller deliveries: %w", err)
	}
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RubrikPollerDeliveryJob, error) {
		var (
			j                             RubrikPollerDeliveryJob
			deliveryID, eventRef, eventID uuid.UUID
		)
		if err := row.Scan(&deliveryID, &eventRef, &j.Delivery.State, &j.Delivery.Attempts, &j.Delivery.CreatedAt,
			&j.Delivery.DeliveredAt, &j.Delivery.LastError,
			&eventID, &j.Event.ClusterID, &j.Event.Kind, &j.Event.RSCObjectID, &j.Event.Namespace, &j.Event.DedupKey,
			&j.Event.OccurredAt, &j.Event.Detail, &j.Event.CreatedAt); err != nil {
			return RubrikPollerDeliveryJob{}, err
		}
		j.Delivery.ID, j.Delivery.EventID, j.Event.ID = deliveryID.String(), eventRef.String(), eventID.String()
		return j, nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim rubrik poller deliveries: %w", err)
	}
	if jobs == nil {
		jobs = []RubrikPollerDeliveryJob{}
	}
	return jobs, nil
}

// MarkPollerDelivered moves a pending delivery to delivered. A missing or
// no longer pending row returns ErrRubrikDeliveryNotPending.
func (s *RubrikStore) MarkPollerDelivered(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE rubrik_poller_deliveries
		   SET state = 'delivered', delivered_at = NOW(), last_error = ''
		 WHERE id = $1::uuid AND state = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("mark rubrik poller delivery delivered: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRubrikDeliveryNotPending
	}
	return nil
}

// MarkPollerDeliveryFailed records a failed attempt. The row stays pending
// for a later claim while its attempts are below maxAttempts, and moves to
// failed, which is final, once they reach it. errMsg is bounded and
// cleaned before it is stored; callers pass a fixed reason class, never
// tenant text. A missing or no longer pending row returns
// ErrRubrikDeliveryNotPending.
func (s *RubrikStore) MarkPollerDeliveryFailed(ctx context.Context, id, errMsg string, maxAttempts int) error {
	if maxAttempts <= 0 {
		return fmt.Errorf("mark rubrik poller delivery failed: maxAttempts (%d) must be positive", maxAttempts)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE rubrik_poller_deliveries
		   SET last_error = $2,
		       state = CASE WHEN attempts >= $3 THEN 'failed' ELSE 'pending' END
		 WHERE id = $1::uuid AND state = 'pending'`, id, boundDeliveryError(errMsg), maxAttempts)
	if err != nil {
		return fmt.Errorf("mark rubrik poller delivery failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRubrikDeliveryNotPending
	}
	return nil
}

// ---------------------------------------------------------------------------
// Operation sweep

// ExpireApplyingOperations moves clusterID's operations that have been
// applying for longer than grace to unknown, and returns how many it moved
// (KTD10). An applying row is written before its RSC mutation and settled
// by the request that sent it, so one still applying long after was cut
// off mid-send: whether RSC applied it cannot be told, and it is never
// resent. Unknown keeps blocking the object's in-flight check until a
// refresh shows the job's outcome. The update is guarded on the applying
// state, so a request that settles the row in the same instant wins. grace
// must be at least RubrikApplyingMinGrace. Nothing else writes an applying
// row's updated_at, so it is the time the write started.
func (s *RubrikStore) ExpireApplyingOperations(ctx context.Context, clusterID string, grace time.Duration) (int64, error) {
	if clusterID == "" {
		return 0, errors.New("expire applying rubrik operations: cluster id is required")
	}
	if grace < RubrikApplyingMinGrace {
		return 0, fmt.Errorf("expire applying rubrik operations: grace must be at least %s, got %s", RubrikApplyingMinGrace, grace)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE rubrik_operations
		   SET state = $3, updated_at = NOW()
		 WHERE cluster_id = $1 AND state = $4
		   AND updated_at < NOW() - make_interval(secs => $2::double precision)`,
		clusterID, grace.Seconds(), RubrikOpUnknown, RubrikOpApplying)
	if err != nil {
		return 0, fmt.Errorf("expire applying rubrik operations: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Retention

// Rubrik history retention defaults. RubrikEventRetention is well past the
// poller's 24-hour catch-up window, so an event is never pruned while the
// poller can still read the transition or job that raised it: a pruned event
// cannot come back under its dedup key. RubrikOperationRetention matches the
// audit log's 90 days, so an operation can be followed for as long as the
// write that started it is audited.
const (
	RubrikEventRetention     = 30 * 24 * time.Hour
	RubrikOperationRetention = 90 * 24 * time.Hour
	// RubrikMinRetention is the shortest retention PruneRubrikHistory
	// accepts, for the same reason: it must outlive the catch-up window.
	RubrikMinRetention = 48 * time.Hour
)

// RubrikRetention says how long PruneRubrikHistory keeps each kind of row.
type RubrikRetention struct {
	// Events is how long a poller event, with its delivery, is kept once
	// the delivery is settled.
	Events time.Duration
	// Operations is how long a finished write operation is kept.
	Operations time.Duration
	// MaxDeliveryAttempts is the delivery drain's attempt limit. A pending
	// delivery that reached it can never be claimed again, so it does not
	// keep its event.
	MaxDeliveryAttempts int
}

// RubrikPruned counts the rows one PruneRubrikHistory removed.
type RubrikPruned struct {
	Events, Operations int64
}

// PruneRubrikHistory deletes Rubrik rows past retention, across all
// clusters, and returns how many it removed of each kind:
//
//   - poller events older than r.Events whose delivery is settled, or
//     pending with its attempts spent; their deliveries go with them. An
//     event whose delivery can still be claimed is never pruned.
//   - operations that finished (succeeded, failed or partial) more than
//     r.Operations ago. An operation that is applying, running or unknown
//     is never pruned: it still blocks its object's in-flight check.
//
// Compliance states need no retention: a tick that sees every object drops
// the states of objects that are gone, and a removed mapping takes its
// cluster's states with it.
//
// Each delete stands alone; on an error the counts so far are returned with
// it. A retention below RubrikMinRetention is refused.
func (s *RubrikStore) PruneRubrikHistory(ctx context.Context, r RubrikRetention) (RubrikPruned, error) {
	var out RubrikPruned
	if r.Events < RubrikMinRetention || r.Operations < RubrikMinRetention {
		return out, fmt.Errorf("prune rubrik history: retention must be at least %s, got events %s and operations %s",
			RubrikMinRetention, r.Events, r.Operations)
	}
	if r.MaxDeliveryAttempts <= 0 {
		return out, fmt.Errorf("prune rubrik history: maxDeliveryAttempts (%d) must be positive", r.MaxDeliveryAttempts)
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rubrik_poller_events e
		 WHERE e.created_at < NOW() - make_interval(secs => $1::double precision)
		   AND NOT EXISTS (
		       SELECT 1 FROM rubrik_poller_deliveries d
		        WHERE d.event_id = e.id AND d.state = $2 AND d.attempts < $3)`,
		r.Events.Seconds(), RubrikDeliveryPending, r.MaxDeliveryAttempts)
	if err != nil {
		return out, fmt.Errorf("prune rubrik poller events: %w", err)
	}
	out.Events = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `
		DELETE FROM rubrik_operations
		 WHERE state IN ($2, $3, $4)
		   AND COALESCE(finished_at, updated_at) < NOW() - make_interval(secs => $1::double precision)`,
		r.Operations.Seconds(), RubrikOpSucceeded, RubrikOpFailed, RubrikOpPartial)
	if err != nil {
		return out, fmt.Errorf("prune rubrik operations: %w", err)
	}
	out.Operations = tag.RowsAffected()
	return out, nil
}
