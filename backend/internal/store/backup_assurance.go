package store

// backup_assurance.go — persistence for Release F backup assurance (KTD11):
// operator-declared freshness policies and the durable exception state
// machine, both created by migration 000022.
//
// The exception row, not the notification, is the identity of record. The
// partial UNIQUE index idx_backup_assurance_exceptions_open_unique admits at
// most one OPEN row per (cluster_id, subject_kind, subject_namespace,
// subject_name, subject_uid, condition), so two replicas — or two ticks of
// one replica — can never both open the same condition. The composite
// open/resolve-and-enqueue transitions build on WithTx and live in
// backup_assurance_delivery.go (U32b); this file holds policy CRUD and the
// non-transitioning exception reads and writes.
//
// Detail is raw JSONB at this layer and is PRIVILEGED: it may carry
// controller messages and storage locations. The store never interprets it;
// projecting it before it reaches a user is the handler's job.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BackupAssuranceExceptionRetention is how long a resolved exception is kept
// before PruneResolved may delete it. It matches the Notification Center's
// 90-day feed retention (notifications.runRetention) so an exception never
// disappears while the notifications that announced it are still listed.
// Open exceptions are never pruned. See migrations/NOTES.txt (000022).
const BackupAssuranceExceptionRetention = 90 * 24 * time.Hour

// AssuranceMinMaxAge mirrors the CHECK (max_age_seconds >= 300) floor in
// migration 000022. The database is the enforcement point; the constant
// exists so callers can explain the rule without hard-coding the number.
const AssuranceMinMaxAge = 5 * time.Minute

// AssuranceScopeKind is the policy scope_kind / exception subject_kind enum.
type AssuranceScopeKind string

const (
	// ScopeSchedule targets one Velero Schedule: namespace and name set.
	ScopeSchedule AssuranceScopeKind = "schedule"
	// ScopeNamespace targets every backup covering one namespace: namespace
	// set, name empty.
	ScopeNamespace AssuranceScopeKind = "namespace"
	// ScopeCluster targets the whole cluster: namespace and name empty.
	ScopeCluster AssuranceScopeKind = "cluster"
)

// Valid reports whether k is a known scope kind. Mirrors the CHECK on
// backup_assurance_policies.scope_kind and exceptions.subject_kind.
func (k AssuranceScopeKind) Valid() bool {
	switch k {
	case ScopeSchedule, ScopeNamespace, ScopeCluster:
		return true
	}
	return false
}

// AssuranceCondition is the exceptions.condition enum.
type AssuranceCondition string

const (
	ConditionOverdue             AssuranceCondition = "overdue"
	ConditionFailed              AssuranceCondition = "failed"
	ConditionPartiallyFailed     AssuranceCondition = "partially_failed"
	ConditionPaused              AssuranceCondition = "paused"
	ConditionNeverRun            AssuranceCondition = "never_run"
	ConditionLocationUnavailable AssuranceCondition = "location_unavailable"
	ConditionCollectionUnknown   AssuranceCondition = "collection_unknown"
)

// Valid reports whether c is a known condition. Mirrors the CHECK on
// backup_assurance_exceptions.condition.
func (c AssuranceCondition) Valid() bool {
	switch c {
	case ConditionOverdue, ConditionFailed, ConditionPartiallyFailed,
		ConditionPaused, ConditionNeverRun, ConditionLocationUnavailable,
		ConditionCollectionUnknown:
		return true
	}
	return false
}

// Exception states. resolved is terminal: a condition that recurs after
// resolution opens a NEW row, because the partial unique index covers only
// state = 'open'.
const (
	AssuranceStateOpen     = "open"
	AssuranceStateResolved = "resolved"
)

// Exception severities. Mirror the CHECK on backup_assurance_exceptions.severity.
const (
	AssuranceSeverityInfo     = "info"
	AssuranceSeverityWarning  = "warning"
	AssuranceSeverityCritical = "critical"
)

// validAssuranceSeverity reports whether s is a severity the schema admits.
func validAssuranceSeverity(s string) bool {
	switch s {
	case AssuranceSeverityInfo, AssuranceSeverityWarning, AssuranceSeverityCritical:
		return true
	}
	return false
}

// TreatPartialAs values for BackupAssurancePolicy.TreatPartialAs.
const (
	AssuranceTreatPartialAsSuccess = "success"
	AssuranceTreatPartialAsFailure = "failure"
)

// BackupAssurancePolicy is one row of backup_assurance_policies. Its identity
// for uniqueness is (ClusterID, ScopeKind, ScopeNamespace, ScopeName).
type BackupAssurancePolicy struct {
	ID             uuid.UUID
	ClusterID      string
	ScopeKind      AssuranceScopeKind
	ScopeNamespace string
	ScopeName      string
	MaxAge         time.Duration
	Grace          time.Duration
	TreatPartialAs string // "success" | "failure"
	AlertOnPaused  bool
	Enabled        bool
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedBy      string
	UpdatedAt      *time.Time
	Revision       int64
}

// BackupAssuranceException is one row of backup_assurance_exceptions.
// PolicyID is recorded but is deliberately not part of the condition
// identity: editing a policy must not orphan an open exception.
type BackupAssuranceException struct {
	ID               uuid.UUID
	ClusterID        string
	PolicyID         uuid.UUID
	SubjectKind      AssuranceScopeKind
	SubjectNamespace string
	SubjectName      string
	SubjectUID       string
	Condition        AssuranceCondition
	State            string // "open" | "resolved"
	Severity         string // "info" | "warning" | "critical"
	OpenedAt         time.Time
	LastObservedAt   time.Time
	ResolvedAt       *time.Time
	ObservationCount int64
	LastSuccessAt    *time.Time
	Detail           []byte // raw JSONB; PRIVILEGED, project before returning
}

// AssuranceExceptionQuery narrows ListExceptions.
type AssuranceExceptionQuery struct {
	// State filters by state; "" returns both open and resolved rows.
	State string
	// RestrictNamespaces, when true, limits results to rows whose
	// subject_namespace is in Namespaces. An empty Namespaces then matches
	// nothing — a caller with no visible namespaces sees no rows, rather
	// than every row. Cluster-scoped subjects have subject_namespace '' and
	// are therefore excluded unless "" is listed explicitly.
	RestrictNamespaces bool
	Namespaces         []string
	// Limit caps the page; <= 0 means AssuranceExceptionDefaultLimit, and
	// anything above AssuranceExceptionMaxLimit is clamped to it.
	Limit int
	// Offset skips rows; negative values are treated as 0.
	Offset int
}

// Page bounds for ListExceptions.
const (
	AssuranceExceptionDefaultLimit = 50
	AssuranceExceptionMaxLimit     = 500
)

// BackupAssuranceStore handles persistence for the backup assurance tables.
type BackupAssuranceStore struct {
	pool *pgxpool.Pool
}

// NewBackupAssuranceStore returns a store backed by pool, or nil when pool is
// nil (no database configured). Callers MUST nil-check the result and leave
// backup assurance disabled rather than wiring a store that would panic on
// first use.
func NewBackupAssuranceStore(pool *pgxpool.Pool) *BackupAssuranceStore {
	if pool == nil {
		return nil
	}
	return &BackupAssuranceStore{pool: pool}
}

var (
	ErrAssurancePolicyExists     = errors.New("backup assurance policy already exists for scope")
	ErrAssuranceRevisionConflict = errors.New("backup assurance policy revision conflict")
	ErrAssurancePolicyNotFound   = errors.New("backup assurance policy not found")
	// ErrAssurancePolicyInvalid wraps a policy that cannot be stored as
	// given; the wrapped message names the offending field.
	ErrAssurancePolicyInvalid = errors.New("invalid backup assurance policy")
	// ErrAssuranceExceptionNotOpen is returned by ObserveException when the
	// row does not exist or is no longer open. An observation never
	// resurrects a resolved exception.
	ErrAssuranceExceptionNotOpen = errors.New("backup assurance exception is not open")
	// ErrAssuranceObservationInvalid wraps an observation the schema would
	// reject (unknown severity, malformed detail). It is permanent: retrying
	// the same observation can never succeed.
	ErrAssuranceObservationInvalid = errors.New("invalid backup assurance observation")
)

// validatePolicy rejects what the schema cannot express or would store in an
// inconsistent shape. The numeric floors (max_age >= 300s, grace >= 0) stay
// with the database CHECKs so there is exactly one place that owns them.
func validatePolicy(p BackupAssurancePolicy) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrAssurancePolicyInvalid, fmt.Sprintf(format, args...))
	}
	if p.ID == uuid.Nil {
		return invalid("id is required")
	}
	if p.ClusterID == "" {
		return invalid("cluster id is required")
	}
	switch p.ScopeKind {
	case ScopeSchedule:
		if p.ScopeNamespace == "" || p.ScopeName == "" {
			return invalid("schedule scope requires a namespace and a name")
		}
	case ScopeNamespace:
		if p.ScopeNamespace == "" || p.ScopeName != "" {
			return invalid("namespace scope requires a namespace and no name")
		}
	case ScopeCluster:
		if p.ScopeNamespace != "" || p.ScopeName != "" {
			return invalid("cluster scope takes no namespace or name")
		}
	default:
		return invalid("unknown scope kind %q", p.ScopeKind)
	}
	switch p.TreatPartialAs {
	case AssuranceTreatPartialAsSuccess, AssuranceTreatPartialAsFailure:
	default:
		return invalid("treatPartialAs must be %q or %q", AssuranceTreatPartialAsSuccess, AssuranceTreatPartialAsFailure)
	}
	if p.MaxAge%time.Second != 0 || p.Grace%time.Second != 0 {
		return invalid("max age and grace must be whole seconds")
	}
	// Both bounds matter: an out-of-range value in either direction would
	// wrap in the int32 conversion into an unrelated value that might pass
	// the CHECKs (a huge negative grace can wrap to a positive one).
	if !fitsInt32Seconds(p.MaxAge) || !fitsInt32Seconds(p.Grace) {
		return invalid("max age and grace must fit in a 32-bit count of seconds")
	}
	return nil
}

func fitsInt32Seconds(d time.Duration) bool {
	s := d / time.Second
	return s >= math.MinInt32 && s <= math.MaxInt32
}

// durationSeconds converts a validated duration to the INTEGER column value.
// validatePolicy has already bounded it to int32 and whole seconds.
func durationSeconds(d time.Duration) int32 {
	return int32(d / time.Second)
}

// classifyPolicyWriteError maps the constraint failures a policy write can
// hit onto sentinels. A unique violation can only come from the scope index:
// the primary key is caller-generated and never reused.
func classifyPolicyWriteError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return ErrAssurancePolicyExists
		case pgCheckViolation:
			return fmt.Errorf("%w: %s violates %s", ErrAssurancePolicyInvalid, op, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s backup_assurance_policies: %w", op, err)
}

// InsertPolicy creates a policy. The caller generates the UUID so a response
// can return it without a follow-up read. A second policy for the same scope
// in the same cluster returns ErrAssurancePolicyExists; a value below a
// schema floor returns ErrAssurancePolicyInvalid.
func (s *BackupAssuranceStore) InsertPolicy(ctx context.Context, p BackupAssurancePolicy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO backup_assurance_policies (
			id, cluster_id, scope_kind, scope_namespace, scope_name,
			max_age_seconds, grace_seconds, treat_partial_as,
			alert_on_paused, enabled, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		p.ID, p.ClusterID, string(p.ScopeKind), p.ScopeNamespace, p.ScopeName,
		durationSeconds(p.MaxAge), durationSeconds(p.Grace), p.TreatPartialAs,
		p.AlertOnPaused, p.Enabled, p.CreatedBy)
	if err != nil {
		return classifyPolicyWriteError("insert", err)
	}
	return nil
}

// UpdatePolicy rewrites a policy's thresholds and switches if it still
// carries p.Revision, bumping revision and stamping updated_at/updated_by.
//
// The scope (kind, namespace, name) and created_* fields are immutable: a
// different scope is a different policy, and must be created as one. They
// are validated (an update describing an invalid policy is still refused)
// but never written.
//
// Zero rows updated is ambiguous, so it is disambiguated inside the same
// transaction by a cluster-scoped existence probe: ErrAssuranceRevisionConflict
// when the policy exists at another revision, ErrAssurancePolicyNotFound when
// it does not exist in this cluster.
func (s *BackupAssuranceStore) UpdatePolicy(ctx context.Context, p BackupAssurancePolicy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE backup_assurance_policies
			SET max_age_seconds = $4, grace_seconds = $5, treat_partial_as = $6,
			    alert_on_paused = $7, enabled = $8, updated_by = $9,
			    revision = revision + 1, updated_at = NOW()
			WHERE id = $1 AND cluster_id = $2 AND revision = $3`,
			p.ID, p.ClusterID, p.Revision,
			durationSeconds(p.MaxAge), durationSeconds(p.Grace), p.TreatPartialAs,
			p.AlertOnPaused, p.Enabled, p.UpdatedBy)
		if err != nil {
			return classifyPolicyWriteError("update", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var exists int
		err = tx.QueryRow(ctx,
			`SELECT 1 FROM backup_assurance_policies WHERE id = $1 AND cluster_id = $2`,
			p.ID, p.ClusterID).Scan(&exists)
		switch {
		case err == nil:
			return ErrAssuranceRevisionConflict
		case errors.Is(err, pgx.ErrNoRows):
			return ErrAssurancePolicyNotFound
		default:
			return fmt.Errorf("probe backup_assurance_policies: %w", err)
		}
	})
}

// DeletePolicy removes a policy. Its exceptions, and their deliveries,
// cascade with it (migration 000022; see NOTES.txt). A policy that does not
// exist in clusterID returns ErrAssurancePolicyNotFound.
func (s *BackupAssuranceStore) DeletePolicy(ctx context.Context, clusterID string, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM backup_assurance_policies WHERE id = $1 AND cluster_id = $2`,
		id, clusterID)
	if err != nil {
		return fmt.Errorf("delete backup_assurance_policies: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAssurancePolicyNotFound
	}
	return nil
}

const assurancePolicyColumns = `
	id, cluster_id, scope_kind, scope_namespace, scope_name,
	max_age_seconds, grace_seconds, treat_partial_as,
	alert_on_paused, enabled, created_by, created_at,
	updated_by, updated_at, revision`

func scanAssurancePolicy(row pgx.Row) (BackupAssurancePolicy, error) {
	var (
		p                   BackupAssurancePolicy
		scopeKind           string
		maxAgeSec, graceSec int32
	)
	err := row.Scan(
		&p.ID, &p.ClusterID, &scopeKind, &p.ScopeNamespace, &p.ScopeName,
		&maxAgeSec, &graceSec, &p.TreatPartialAs,
		&p.AlertOnPaused, &p.Enabled, &p.CreatedBy, &p.CreatedAt,
		&p.UpdatedBy, &p.UpdatedAt, &p.Revision)
	if err != nil {
		return BackupAssurancePolicy{}, err
	}
	p.ScopeKind = AssuranceScopeKind(scopeKind)
	p.MaxAge = time.Duration(maxAgeSec) * time.Second
	p.Grace = time.Duration(graceSec) * time.Second
	return p, nil
}

// GetPolicy returns one policy, or ErrAssurancePolicyNotFound when it does
// not exist in clusterID.
func (s *BackupAssuranceStore) GetPolicy(ctx context.Context, clusterID string, id uuid.UUID) (*BackupAssurancePolicy, error) {
	p, err := scanAssurancePolicy(s.pool.QueryRow(ctx,
		`SELECT `+assurancePolicyColumns+`
		 FROM backup_assurance_policies
		 WHERE id = $1 AND cluster_id = $2`, id, clusterID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssurancePolicyNotFound
		}
		return nil, fmt.Errorf("get backup_assurance_policies: %w", err)
	}
	return &p, nil
}

// ListPolicies returns every policy for clusterID in a stable order (scope
// kind, namespace, name). No policies is an empty slice and a nil error:
// install creates none, and no policy means no evaluation.
func (s *BackupAssuranceStore) ListPolicies(ctx context.Context, clusterID string) ([]BackupAssurancePolicy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+assurancePolicyColumns+`
		 FROM backup_assurance_policies
		 WHERE cluster_id = $1
		 ORDER BY scope_kind, scope_namespace, scope_name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list backup_assurance_policies: %w", err)
	}
	policies, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BackupAssurancePolicy, error) {
		return scanAssurancePolicy(r)
	})
	if err != nil {
		return nil, fmt.Errorf("scan backup_assurance_policies: %w", err)
	}
	if policies == nil {
		policies = []BackupAssurancePolicy{}
	}
	return policies, nil
}

const assuranceExceptionColumns = `
	id, cluster_id, policy_id, subject_kind, subject_namespace, subject_name,
	subject_uid, condition, state, severity, opened_at, last_observed_at,
	resolved_at, observation_count, last_success_at, detail`

// assuranceExceptionScan is the single owner of the destination list that
// matches assuranceExceptionColumns. Every scanner of an exception row (here
// and the delivery-job join in backup_assurance_delivery.go) goes through it,
// so a column added to the projection is added to exactly one Scan list.
type assuranceExceptionScan struct {
	e                      BackupAssuranceException
	subjectKind, condition string
}

// dests returns the Scan destinations in assuranceExceptionColumns order.
func (s *assuranceExceptionScan) dests() []any {
	return []any{
		&s.e.ID, &s.e.ClusterID, &s.e.PolicyID, &s.subjectKind, &s.e.SubjectNamespace, &s.e.SubjectName,
		&s.e.SubjectUID, &s.condition, &s.e.State, &s.e.Severity, &s.e.OpenedAt, &s.e.LastObservedAt,
		&s.e.ResolvedAt, &s.e.ObservationCount, &s.e.LastSuccessAt, &s.e.Detail,
	}
}

// result converts the scanned enum strings and returns the exception.
func (s *assuranceExceptionScan) result() BackupAssuranceException {
	s.e.SubjectKind = AssuranceScopeKind(s.subjectKind)
	s.e.Condition = AssuranceCondition(s.condition)
	return s.e
}

func scanAssuranceException(row pgx.Row) (BackupAssuranceException, error) {
	var s assuranceExceptionScan
	if err := row.Scan(s.dests()...); err != nil {
		return BackupAssuranceException{}, err
	}
	return s.result(), nil
}

func collectAssuranceExceptions(rows pgx.Rows) ([]BackupAssuranceException, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BackupAssuranceException, error) {
		return scanAssuranceException(r)
	})
	if err != nil {
		return nil, fmt.Errorf("scan backup_assurance_exceptions: %w", err)
	}
	if out == nil {
		out = []BackupAssuranceException{}
	}
	return out, nil
}

// ListOpenExceptions returns every open exception for clusterID. It is the
// collector's whole memory: on every tick, including the first after a
// restart, it reconciles this list against the evaluator's findings.
func (s *BackupAssuranceStore) ListOpenExceptions(ctx context.Context, clusterID string) ([]BackupAssuranceException, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+assuranceExceptionColumns+`
		 FROM backup_assurance_exceptions
		 WHERE cluster_id = $1 AND state = 'open'
		 ORDER BY opened_at DESC, id DESC`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list open backup_assurance_exceptions: %w", err)
	}
	return collectAssuranceExceptions(rows)
}

// ListExceptions returns one page of exceptions for clusterID, newest first,
// and the total number of rows matching q before paging. RBAC beyond the
// namespace restriction is the caller's job; a caller that filters further
// must recompute any count it reports.
func (s *BackupAssuranceStore) ListExceptions(ctx context.Context, clusterID string, q AssuranceExceptionQuery) ([]BackupAssuranceException, int, error) {
	switch q.State {
	case "", AssuranceStateOpen, AssuranceStateResolved:
	default:
		return nil, 0, fmt.Errorf("list backup_assurance_exceptions: unknown state %q", q.State)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = AssuranceExceptionDefaultLimit
	}
	limit = min(limit, AssuranceExceptionMaxLimit)
	offset := max(q.Offset, 0)
	namespaces := q.Namespaces
	if namespaces == nil {
		namespaces = []string{}
	}

	const where = `
		WHERE cluster_id = $1
		  AND ($2 = '' OR state = $2)
		  AND (NOT $3::boolean OR subject_namespace = ANY($4::text[]))`

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM backup_assurance_exceptions`+where,
		clusterID, q.State, q.RestrictNamespaces, namespaces).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count backup_assurance_exceptions: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+assuranceExceptionColumns+`
		 FROM backup_assurance_exceptions`+where+`
		 ORDER BY opened_at DESC, id DESC
		 LIMIT $5 OFFSET $6`,
		clusterID, q.State, q.RestrictNamespaces, namespaces, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list backup_assurance_exceptions: %w", err)
	}
	out, err := collectAssuranceExceptions(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ObserveException records a repeat of an open exception (open → open):
// it bumps observation_count and refreshes last_observed_at, severity,
// last_success_at and detail. It never reopens a resolved row; observing a
// row that is missing or resolved returns ErrAssuranceExceptionNotOpen.
// An empty detail is stored as '{}'; a non-empty detail must be a JSON
// object, because resolution merges resolutionReason into it. A nil
// lastSuccessAt keeps the stored value: "no success seen in this
// observation" never erases a success an earlier observation recorded.
//
// Observations are monotonic in observedAt. Two replicas can observe the
// same open exception concurrently; an observation older than the one
// already stored is dropped (nil error, nothing written) so a delayed write
// can never replace newer severity, last_success_at or detail with stale
// values. An observation at the same instant as the stored one is applied.
//
// An unknown severity or malformed detail returns
// ErrAssuranceObservationInvalid before anything is sent to the database,
// so a permanent input error is distinguishable from a transient one.
func (s *BackupAssuranceStore) ObserveException(
	ctx context.Context, id uuid.UUID, observedAt time.Time,
	severity string, lastSuccessAt *time.Time, detail []byte,
) error {
	if !validAssuranceSeverity(severity) {
		return fmt.Errorf("%w: unknown severity %q", ErrAssuranceObservationInvalid, severity)
	}
	if len(detail) == 0 {
		detail = []byte(`{}`)
	} else if !isJSONObject(detail) {
		return fmt.Errorf("%w: detail must be a JSON object", ErrAssuranceObservationInvalid)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE backup_assurance_exceptions
		SET last_observed_at = $2, observation_count = observation_count + 1,
		    severity = $3, last_success_at = COALESCE($4, last_success_at), detail = $5
		WHERE id = $1 AND state = 'open' AND last_observed_at <= $2`,
		id, observedAt, severity, lastSuccessAt, detail)
	if err != nil {
		return fmt.Errorf("observe backup_assurance_exceptions: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// Zero rows: missing, resolved, or a stale observation of an open row.
	var state string
	err = s.pool.QueryRow(ctx,
		`SELECT state FROM backup_assurance_exceptions WHERE id = $1`, id).Scan(&state)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrAssuranceExceptionNotOpen
	case err != nil:
		return fmt.Errorf("probe backup_assurance_exceptions: %w", err)
	case state != AssuranceStateOpen:
		return ErrAssuranceExceptionNotOpen
	default:
		return nil // stale observation of a still-open row: dropped
	}
}

// isJSONObject reports whether b is valid JSON whose top-level value is an
// object. Exception detail must be an object so that resolution can merge
// resolutionReason into it; an array or scalar would be stored fine and then
// make every later jsonb merge fail.
func isJSONObject(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}

// PruneResolved deletes resolved exceptions whose resolved_at is older than
// retention, across all clusters, and returns how many it removed. Open
// exceptions are never pruned. A non-positive retention is refused rather
// than read as "delete every resolved exception".
func (s *BackupAssuranceStore) PruneResolved(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, fmt.Errorf("prune backup_assurance_exceptions: retention must be positive, got %s", retention)
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM backup_assurance_exceptions
		WHERE state = 'resolved'
		  AND resolved_at < NOW() - make_interval(secs => $1::double precision)`,
		retention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("prune backup_assurance_exceptions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// assuranceRollbackTimeout bounds WithTx's deferred rollback.
const assuranceRollbackTimeout = 5 * time.Second

// WithTx runs fn inside a transaction, committing when fn returns nil and
// rolling back otherwise (including when fn panics, via the deferred
// Rollback). U32b's OpenExceptionAndEnqueue / ResolveExceptionAndEnqueue
// build on it so a state transition and its delivery intent commit together.
func (s *BackupAssuranceStore) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin backup assurance tx: %w", err)
	}
	// Rollback after a successful Commit is a no-op returning ErrTxClosed.
	// It runs on a context detached from ctx's cancellation (a cancelled
	// caller must still release the transaction) but with its own bound, so
	// a wedged connection cannot hold the calling goroutine indefinitely.
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), assuranceRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit backup assurance tx: %w", err)
	}
	return nil
}
