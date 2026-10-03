package store

// backup_assurance_delivery.go — the two composite state transitions of the
// Release F exception machine (KTD11), the durable delivery intents they
// enqueue, and the collector lease. Migration 000022 owns the schema; the
// policy CRUD and non-transitioning exception accessors live in
// backup_assurance.go (U32).
//
// Correctness lives in two UNIQUE indexes, not in this file and not in the
// lease:
//
//   - idx_backup_assurance_exceptions_open_unique admits one OPEN row per
//     condition identity, so concurrent openers (two replicas, or two ticks)
//     can never both open the same condition. The loser's INSERT ... ON
//     CONFLICT DO NOTHING affects zero rows and it is handed the winner's row
//     with opened = false.
//   - idx_backup_assurance_deliveries_once admits one delivery intent per
//     (exception, transition), so a transition is notifiable exactly once
//     across restarts. The intent commits in the SAME transaction as the
//     transition; sending it is a separate, later step (U34b's drain).
//
// The lease is ADVISORY. It suppresses duplicate Kubernetes reads and racing
// observations when more than one replica runs; if two replicas both believe
// they hold it, the indexes above still make a duplicate exception or a
// duplicate notification impossible. Nothing gates a write on the lease or
// its fence. Expiry is judged on the DATABASE clock (NOW()) only, so replica
// clock skew cannot cause a premature takeover or an immortal lease.
//
// No method here reads the process clock or uses context.Background(): the
// caller supplies every application timestamp and every context, and the
// database supplies every lease timestamp. A hermetic test pins both rules.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Delivery transitions. Mirror the CHECK on backup_assurance_deliveries.transition.
const (
	AssuranceTransitionOpened   = "opened"
	AssuranceTransitionResolved = "resolved"
)

// Delivery states. Mirror the CHECK on backup_assurance_deliveries.state.
// failed is terminal: a failed intent is never retried and never regenerates
// its condition.
const (
	AssuranceDeliveryPending   = "pending"
	AssuranceDeliveryDelivered = "delivered"
	AssuranceDeliveryFailed    = "failed"
)

// Resolution reasons recorded under detail.resolutionReason by
// ResolveExceptionAndEnqueue. Callers may pass other non-empty reasons; these
// two are the ones the state machine in the plan names.
const (
	// AssuranceResolutionConditionCleared: the evaluator stopped producing
	// the condition for a subject that is still observable.
	AssuranceResolutionConditionCleared = "condition_cleared"
	// AssuranceResolutionSubjectAbsent: the subject left the inventory (the
	// schedule was deleted, or its UID changed).
	AssuranceResolutionSubjectAbsent = "subject_absent"
)

// assuranceDeliveryErrorMaxLen bounds last_error so a pathological error
// string cannot grow the row without limit. The head of the message is kept:
// that is where the sentinel and the operation name live.
const assuranceDeliveryErrorMaxLen = 1024

// assuranceOpenMaxAttempts bounds the retry inside OpenExceptionAndEnqueue
// for the window in which another replica opened AND resolved the identity
// between this transaction's INSERT and its re-SELECT. Each retry is a fresh
// transaction; three is far beyond what a 60 s tick can race against.
const assuranceOpenMaxAttempts = 3

// assuranceDeliveryExhaustedError is written to last_error when a pending
// intent is found with its attempts already spent (a process died between
// claiming and marking it, on its last attempt).
const assuranceDeliveryExhaustedError = "delivery attempts exhausted"

var (
	// ErrAssuranceExceptionInvalid wraps an exception OpenExceptionAndEnqueue
	// refuses before touching the database; the wrapped message names the
	// offending field. It is permanent: retrying the same input cannot succeed.
	ErrAssuranceExceptionInvalid = errors.New("invalid backup assurance exception")
	// ErrAssuranceResolutionInvalid wraps a resolution with no timestamp or
	// no reason. A resolved row always carries resolved_at and a reason; the
	// schema does not enforce either, so this method does.
	ErrAssuranceResolutionInvalid = errors.New("invalid backup assurance resolution")
	// ErrAssuranceDeliveryNotPending is returned by MarkDelivered and
	// MarkDeliveryFailed when the intent is missing or no longer pending
	// (already delivered, already failed, or cascaded away with its policy).
	ErrAssuranceDeliveryNotPending = errors.New("backup assurance delivery is not pending")
	// ErrLeaseHeldByOther is returned by AcquireOrRenewLease when a different
	// holder owns a lease that has not expired on the database clock.
	ErrLeaseHeldByOther = errors.New("backup assurance collector lease held by another replica")
	// ErrLeaseNotFound is returned by GetLease when no replica has ever held
	// the lease for the cluster.
	ErrLeaseNotFound = errors.New("backup assurance collector lease not found")
)

// AssuranceDelivery is one row of backup_assurance_deliveries: the durable
// intent to notify one transition of one exception.
type AssuranceDelivery struct {
	ID          uuid.UUID
	ExceptionID uuid.UUID
	Transition  string // "opened" | "resolved"
	State       string // "pending" | "delivered" | "failed"
	Attempts    int
	CreatedAt   time.Time
	DeliveredAt *time.Time
	LastError   string
}

// AssuranceDeliveryJob is a claimed delivery joined to its exception, so the
// caller can build a notification without a second round trip. Detail on the
// embedded exception is PRIVILEGED (see backup_assurance.go).
type AssuranceDeliveryJob struct {
	Delivery  AssuranceDelivery
	Exception BackupAssuranceException
}

// AssuranceLease is the backup_assurance_collector_lease row for one cluster.
// Every timestamp is the database's; Expired is evaluated against the
// database clock in the same statement that read the row.
type AssuranceLease struct {
	ClusterID  string
	Holder     string
	Fence      int64
	AcquiredAt time.Time
	RenewedAt  time.Time
	ExpiresAt  time.Time
	Expired    bool
}

// deliveryIDFor derives the intent's primary key from its identity. The id is
// a pure function of (exception, transition), so it can be recomputed by
// anyone holding the exception: the UNIQUE index remains the guard, the
// derived key simply makes the "exactly one per transition" rule visible in
// the row itself.
func deliveryIDFor(exceptionID uuid.UUID, transition string) uuid.UUID {
	return uuid.NewSHA1(exceptionID, []byte(transition))
}

// validateOpenException rejects what the schema cannot express or would
// store in an inconsistent shape. The subject shape rules mirror
// validatePolicy's scope rules, plus one: a schedule subject needs its UID,
// because the UID is what lets a deleted-and-recreated schedule open a new
// exception instead of inheriting the old one (R1 / KTD3).
func validateOpenException(e BackupAssuranceException) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrAssuranceExceptionInvalid, fmt.Sprintf(format, args...))
	}
	if e.ID == uuid.Nil {
		return invalid("id is required")
	}
	if e.ClusterID == "" {
		return invalid("cluster id is required")
	}
	if e.PolicyID == uuid.Nil {
		return invalid("policy id is required")
	}
	switch e.SubjectKind {
	case ScopeSchedule:
		if e.SubjectNamespace == "" || e.SubjectName == "" || e.SubjectUID == "" {
			return invalid("schedule subject requires a namespace, a name and a uid")
		}
	case ScopeNamespace:
		if e.SubjectNamespace == "" || e.SubjectName != "" || e.SubjectUID != "" {
			return invalid("namespace subject requires a namespace and no name or uid")
		}
	case ScopeCluster:
		if e.SubjectNamespace != "" || e.SubjectName != "" || e.SubjectUID != "" {
			return invalid("cluster subject takes no namespace, name or uid")
		}
	default:
		return invalid("unknown subject kind %q", e.SubjectKind)
	}
	if !e.Condition.Valid() {
		return invalid("unknown condition %q", e.Condition)
	}
	if !validAssuranceSeverity(e.Severity) {
		return invalid("unknown severity %q", e.Severity)
	}
	if e.OpenedAt.IsZero() {
		return invalid("openedAt is required")
	}
	if len(e.Detail) > 0 && !json.Valid(e.Detail) {
		return invalid("detail is not valid JSON")
	}
	return nil
}

// OpenExceptionAndEnqueue inserts an open exception and its "opened" delivery
// intent in ONE transaction.
//
// opened = true: no open row existed for e's condition identity; the returned
// exception is the row as stored (observation_count 1, opened_at and
// last_observed_at = e.OpenedAt) and exactly one pending "opened" intent was
// created for it.
//
// opened = false: an open row for this identity already existed — this
// replica lost the race, or an earlier tick opened it. The returned exception
// is that existing row and NO delivery is created; the caller should record
// an observation (ObserveException) instead. The existing row's PolicyID may
// differ from e.PolicyID: policy identity is deliberately not part of the
// condition identity.
//
// The INSERT selects its cluster_id and policy_id FROM the policy row itself,
// restricted to e.ClusterID, so an exception can never cite a policy from
// another cluster and a policy deleted between the caller's ListPolicies and
// this call cannot be referenced: both surface as ErrAssurancePolicyNotFound
// with nothing written. e.State, e.ObservationCount and e.ResolvedAt are
// ignored; an empty Detail is stored as '{}'.
func (s *BackupAssuranceStore) OpenExceptionAndEnqueue(
	ctx context.Context, e BackupAssuranceException,
) (BackupAssuranceException, bool, error) {
	if err := validateOpenException(e); err != nil {
		return BackupAssuranceException{}, false, err
	}
	detail := e.Detail
	if len(detail) == 0 {
		detail = []byte(`{}`)
	}

	for range assuranceOpenMaxAttempts {
		var (
			got    BackupAssuranceException
			opened bool
			raced  bool
		)
		err := s.WithTx(ctx, func(tx pgx.Tx) error {
			// ON CONFLICT DO NOTHING without a named arbiter: the arbiter is
			// a partial index, which cannot be named as a constraint target.
			// A concurrent opener blocks here until the first committer is
			// decided, then sees the conflict and returns zero rows.
			inserted, err := scanAssuranceException(tx.QueryRow(ctx, `
				INSERT INTO backup_assurance_exceptions (
					id, cluster_id, policy_id, subject_kind, subject_namespace, subject_name,
					subject_uid, condition, state, severity, opened_at, last_observed_at,
					observation_count, last_success_at, detail
				)
				SELECT $1::uuid, p.cluster_id, p.id, $4::text, $5::text, $6::text,
				       $7::text, $8::text, 'open', $9::text, $10::timestamptz, $10::timestamptz,
				       1, $11::timestamptz, $12::jsonb
				  FROM backup_assurance_policies p
				 WHERE p.id = $3 AND p.cluster_id = $2
				ON CONFLICT DO NOTHING
				RETURNING `+assuranceExceptionColumns,
				e.ID, e.ClusterID, e.PolicyID, string(e.SubjectKind), e.SubjectNamespace, e.SubjectName,
				e.SubjectUID, string(e.Condition), e.Severity, e.OpenedAt, e.LastSuccessAt, detail))
			switch {
			case err == nil:
				got, opened = inserted, true
				if _, err := tx.Exec(ctx, `
					INSERT INTO backup_assurance_deliveries (id, exception_id, transition, state)
					VALUES ($1, $2, $3, 'pending')`,
					deliveryIDFor(inserted.ID, AssuranceTransitionOpened), inserted.ID, AssuranceTransitionOpened); err != nil {
					return fmt.Errorf("enqueue opened delivery: %w", err)
				}
				return nil
			case !errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("open backup_assurance_exceptions: %w", err)
			}

			// Zero rows: an open row holds this identity, or the policy is
			// not in this cluster. A new statement sees the winner's commit.
			existing, err := scanAssuranceException(tx.QueryRow(ctx, `
				SELECT `+assuranceExceptionColumns+`
				  FROM backup_assurance_exceptions
				 WHERE cluster_id = $1 AND subject_kind = $2 AND subject_namespace = $3
				   AND subject_name = $4 AND subject_uid = $5 AND condition = $6
				   AND state = 'open'`,
				e.ClusterID, string(e.SubjectKind), e.SubjectNamespace, e.SubjectName, e.SubjectUID, string(e.Condition)))
			switch {
			case err == nil:
				got = existing
				return nil
			case !errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("find open backup_assurance_exceptions: %w", err)
			}

			var one int
			err = tx.QueryRow(ctx,
				`SELECT 1 FROM backup_assurance_policies WHERE id = $1 AND cluster_id = $2`,
				e.PolicyID, e.ClusterID).Scan(&one)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return ErrAssurancePolicyNotFound
			case err != nil:
				return fmt.Errorf("probe backup_assurance_policies: %w", err)
			}
			// The policy is fine, so the conflict was a concurrent open that
			// has already been resolved again. Retry in a fresh transaction.
			raced = true
			return nil
		})
		if err != nil {
			return BackupAssuranceException{}, false, err
		}
		if !raced {
			return got, opened, nil
		}
	}
	return BackupAssuranceException{}, false, fmt.Errorf(
		"open backup_assurance_exceptions: identity was opened and resolved concurrently %d times in a row",
		assuranceOpenMaxAttempts)
}

// ResolveExceptionAndEnqueue flips one open exception to resolved and inserts
// its "resolved" delivery intent in ONE transaction. at becomes resolved_at
// and reason is recorded as detail.resolutionReason (merged, never replacing
// the rest of detail).
//
// resolved = true: this call performed the transition and enqueued exactly
// one pending "resolved" intent. resolved = false, nil error: the row was
// already resolved (another replica won) or no longer exists (its policy was
// deleted and it cascaded away); nothing was written and nothing will be
// notified. Only the transaction whose UPDATE returns the row inserts the
// intent, so the UNIQUE (exception_id, transition) index is never contended.
//
// A zero at or an empty reason returns ErrAssuranceResolutionInvalid: the
// schema allows a resolved row with a NULL resolved_at, this method does not.
func (s *BackupAssuranceStore) ResolveExceptionAndEnqueue(
	ctx context.Context, id uuid.UUID, at time.Time, reason string,
) (bool, error) {
	if at.IsZero() {
		return false, fmt.Errorf("%w: resolvedAt is required", ErrAssuranceResolutionInvalid)
	}
	if reason == "" {
		return false, fmt.Errorf("%w: reason is required", ErrAssuranceResolutionInvalid)
	}
	resolved := false
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var resolvedID uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE backup_assurance_exceptions
			   SET state = 'resolved',
			       resolved_at = $2,
			       detail = jsonb_set(detail, '{resolutionReason}', to_jsonb($3::text), true)
			 WHERE id = $1 AND state = 'open'
			RETURNING id`, id, at, reason).Scan(&resolvedID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("resolve backup_assurance_exceptions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO backup_assurance_deliveries (id, exception_id, transition, state)
			VALUES ($1, $2, $3, 'pending')`,
			deliveryIDFor(id, AssuranceTransitionResolved), id, AssuranceTransitionResolved); err != nil {
			return fmt.Errorf("enqueue resolved delivery: %w", err)
		}
		resolved = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return resolved, nil
}

// assuranceDeliveryColumns is the deliveries projection, unqualified.
const assuranceDeliveryColumns = `
	id, exception_id, transition, state, attempts, created_at, delivered_at, last_error`

// qualifyColumns prefixes every column in a comma-separated projection with
// alias so two projections can share one SELECT without ambiguity.
func qualifyColumns(alias, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

// ClaimPendingDeliveries atomically claims up to limit pending intents for
// clusterID whose attempts are below maxAttempts, oldest first, incrementing
// attempts on each, and returns them joined to their exceptions. Concurrent
// claimers never receive the same row: the selection is FOR UPDATE SKIP
// LOCKED, so a row another transaction is claiming is skipped, not waited on.
//
// Claiming counts as an attempt. A caller that claims and then neither marks
// the row delivered nor failed (crash, cancelled context) leaves it pending
// with attempts incremented; the next claim picks it up again until the
// attempts are spent. A pending row found with attempts already at or above
// maxAttempts is one such abandoned final attempt: it is moved to failed
// (last_error set if empty) before the claim so the backlog cannot carry an
// unclaimable pending row forever.
//
// The claim is scoped to clusterID through the exception join, so one
// cluster's drain never spends another cluster's attempts.
func (s *BackupAssuranceStore) ClaimPendingDeliveries(
	ctx context.Context, clusterID string, maxAttempts, limit int,
) ([]AssuranceDeliveryJob, error) {
	if clusterID == "" {
		return nil, errors.New("claim backup_assurance_deliveries: cluster id is required")
	}
	if maxAttempts <= 0 || limit <= 0 {
		return nil, fmt.Errorf("claim backup_assurance_deliveries: maxAttempts (%d) and limit (%d) must be positive", maxAttempts, limit)
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE backup_assurance_deliveries d
		   SET state = 'failed',
		       last_error = CASE WHEN d.last_error = '' THEN $3::text ELSE d.last_error END
		  FROM backup_assurance_exceptions e
		 WHERE e.id = d.exception_id AND e.cluster_id = $2
		   AND d.state = 'pending' AND d.attempts >= $1`,
		maxAttempts, clusterID, assuranceDeliveryExhaustedError); err != nil {
		return nil, fmt.Errorf("fail exhausted backup_assurance_deliveries: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		WITH claimed AS (
			UPDATE backup_assurance_deliveries d
			   SET attempts = d.attempts + 1
			 WHERE d.id IN (
			       SELECT d2.id
			         FROM backup_assurance_deliveries d2
			         JOIN backup_assurance_exceptions e2 ON e2.id = d2.exception_id
			        WHERE d2.state = 'pending' AND d2.attempts < $1 AND e2.cluster_id = $3
			        ORDER BY d2.created_at, d2.id
			          FOR UPDATE OF d2 SKIP LOCKED
			        LIMIT $2)
			RETURNING `+qualifyColumns("d", assuranceDeliveryColumns)+`
		)
		SELECT `+qualifyColumns("c", assuranceDeliveryColumns)+`,
		       `+qualifyColumns("e", assuranceExceptionColumns)+`
		  FROM claimed c
		  JOIN backup_assurance_exceptions e ON e.id = c.exception_id
		 ORDER BY c.created_at, c.id`,
		maxAttempts, limit, clusterID)
	if err != nil {
		return nil, fmt.Errorf("claim backup_assurance_deliveries: %w", err)
	}
	jobs, err := pgx.CollectRows(rows, scanAssuranceDeliveryJob)
	if err != nil {
		return nil, fmt.Errorf("scan backup_assurance_deliveries: %w", err)
	}
	if jobs == nil {
		jobs = []AssuranceDeliveryJob{}
	}
	return jobs, nil
}

func scanAssuranceDeliveryJob(row pgx.CollectableRow) (AssuranceDeliveryJob, error) {
	var (
		j                      AssuranceDeliveryJob
		subjectKind, condition string
	)
	d, e := &j.Delivery, &j.Exception
	err := row.Scan(
		&d.ID, &d.ExceptionID, &d.Transition, &d.State, &d.Attempts, &d.CreatedAt, &d.DeliveredAt, &d.LastError,
		&e.ID, &e.ClusterID, &e.PolicyID, &subjectKind, &e.SubjectNamespace, &e.SubjectName,
		&e.SubjectUID, &condition, &e.State, &e.Severity, &e.OpenedAt, &e.LastObservedAt,
		&e.ResolvedAt, &e.ObservationCount, &e.LastSuccessAt, &e.Detail)
	if err != nil {
		return AssuranceDeliveryJob{}, err
	}
	e.SubjectKind = AssuranceScopeKind(subjectKind)
	e.Condition = AssuranceCondition(condition)
	return j, nil
}

// MarkDelivered moves a pending intent to delivered, stamping delivered_at on
// the database clock. A row that is missing or no longer pending returns
// ErrAssuranceDeliveryNotPending and is left as it is.
func (s *BackupAssuranceStore) MarkDelivered(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE backup_assurance_deliveries
		   SET state = 'delivered', delivered_at = NOW(), last_error = ''
		 WHERE id = $1 AND state = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("mark backup_assurance_deliveries delivered: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAssuranceDeliveryNotPending
	}
	return nil
}

// MarkDeliveryFailed records a failed attempt on a pending intent. While
// attempts < maxAttempts the row stays pending with last_error updated and
// is retried by a later claim; once attempts >= maxAttempts it moves to
// failed, which is terminal. A row that is missing or no longer pending
// returns ErrAssuranceDeliveryNotPending.
func (s *BackupAssuranceStore) MarkDeliveryFailed(ctx context.Context, id uuid.UUID, errMsg string, maxAttempts int) error {
	if maxAttempts <= 0 {
		return fmt.Errorf("mark backup_assurance_deliveries failed: maxAttempts (%d) must be positive", maxAttempts)
	}
	if len(errMsg) > assuranceDeliveryErrorMaxLen {
		errMsg = errMsg[:assuranceDeliveryErrorMaxLen]
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE backup_assurance_deliveries
		   SET last_error = $2,
		       state = CASE WHEN attempts >= $3 THEN 'failed' ELSE 'pending' END
		 WHERE id = $1 AND state = 'pending'`, id, errMsg, maxAttempts)
	if err != nil {
		return fmt.Errorf("mark backup_assurance_deliveries failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAssuranceDeliveryNotPending
	}
	return nil
}

// CountPendingDeliveries returns how many intents for clusterID are still
// pending and how many have terminally failed. The status endpoint reports
// both as the delivery backlog.
func (s *BackupAssuranceStore) CountPendingDeliveries(ctx context.Context, clusterID string) (pending, failed int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE d.state = 'pending'),
		       count(*) FILTER (WHERE d.state = 'failed')
		  FROM backup_assurance_deliveries d
		  JOIN backup_assurance_exceptions e ON e.id = d.exception_id
		 WHERE e.cluster_id = $1`, clusterID).Scan(&pending, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("count backup_assurance_deliveries: %w", err)
	}
	return pending, failed, nil
}

const assuranceLeaseColumns = `cluster_id, holder, fence, acquired_at, renewed_at, expires_at, expires_at <= NOW()`

func scanAssuranceLease(row pgx.Row) (AssuranceLease, error) {
	var l AssuranceLease
	if err := row.Scan(&l.ClusterID, &l.Holder, &l.Fence, &l.AcquiredAt, &l.RenewedAt, &l.ExpiresAt, &l.Expired); err != nil {
		return AssuranceLease{}, err
	}
	return l, nil
}

// AcquireOrRenewLease takes the collector lease for clusterID, renews it when
// holder already holds it live, or takes it over when the incumbent's lease
// has expired on the DATABASE clock. A live lease held by another holder
// returns ErrLeaseHeldByOther. The lease is advisory (see the file comment):
// losing it means skipping work, never losing correctness.
//
// Semantics, in the order a reviewer can check them against the statement:
//
//   - expires_at is always NOW() + ttl on the database clock, so replica clock
//     skew is irrelevant to both renewal and takeover.
//   - A live renewal by the incumbent keeps acquired_at and fence.
//   - Any acquisition of an expired lease — by a new holder or by the former
//     holder returning after a pause longer than ttl — resets acquired_at and
//     increments fence, so fence is a monotonic epoch of continuous tenure.
//   - Two replicas racing for an expired lease serialize on the primary key:
//     the second re-evaluates the WHERE against the first's committed row,
//     finds it live and foreign, and gets ErrLeaseHeldByOther. Fence moves
//     by exactly one.
//
// A released lease is an expired lease (ReleaseLease sets expires_at = NOW()),
// so the row persists and the fence keeps counting across releases.
func (s *BackupAssuranceStore) AcquireOrRenewLease(
	ctx context.Context, clusterID, holder string, ttl time.Duration,
) (AssuranceLease, error) {
	if clusterID == "" || holder == "" {
		return AssuranceLease{}, errors.New("acquire backup assurance lease: cluster id and holder are required")
	}
	if ttl <= 0 {
		return AssuranceLease{}, fmt.Errorf("acquire backup assurance lease: ttl must be positive, got %s", ttl)
	}
	l, err := scanAssuranceLease(s.pool.QueryRow(ctx, `
		INSERT INTO backup_assurance_collector_lease AS l (cluster_id, holder, expires_at)
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
		RETURNING `+assuranceLeaseColumns,
		clusterID, holder, ttl.Seconds()))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return AssuranceLease{}, ErrLeaseHeldByOther
	case err != nil:
		return AssuranceLease{}, fmt.Errorf("acquire backup assurance lease: %w", err)
	}
	return l, nil
}

// ReleaseLease gives the lease up early by expiring it on the database clock,
// but only when holder still holds it: a replica that already lost the lease
// cannot expire the new incumbent's tenure. Releasing a lease one does not
// hold is a no-op with a nil error, so a shutdown path never logs a spurious
// failure after a takeover. Best-effort by contract: an unreleased lease
// simply expires after its ttl.
func (s *BackupAssuranceStore) ReleaseLease(ctx context.Context, clusterID, holder string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE backup_assurance_collector_lease
		   SET expires_at = NOW()
		 WHERE cluster_id = $1 AND holder = $2 AND expires_at > NOW()`,
		clusterID, holder); err != nil {
		return fmt.Errorf("release backup assurance lease: %w", err)
	}
	return nil
}

// GetLease reads the lease row for clusterID, with Expired judged on the
// database clock in the same statement. ErrLeaseNotFound means no replica
// has ever held it.
func (s *BackupAssuranceStore) GetLease(ctx context.Context, clusterID string) (AssuranceLease, error) {
	l, err := scanAssuranceLease(s.pool.QueryRow(ctx,
		`SELECT `+assuranceLeaseColumns+` FROM backup_assurance_collector_lease WHERE cluster_id = $1`,
		clusterID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return AssuranceLease{}, ErrLeaseNotFound
	case err != nil:
		return AssuranceLease{}, fmt.Errorf("get backup assurance lease: %w", err)
	}
	return l, nil
}
