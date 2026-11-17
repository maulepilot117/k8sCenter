package store

// rubrik_operations.go - durable records of Rubrik writes (KTD7).
//
// The client's operation id is the row key. A row is written as applying
// before the RSC mutation is sent, then moved on by guarded updates, so a
// retry under the same id is answered from the row instead of reaching RSC a
// second time (docs/solutions/tracked-apply-durable-intent.md).
//
// Each RSC object allows one in-flight SLA change or restore, and a snapshot
// is refused while a restore is in flight. An in-flight restore also refuses
// every write into a namespace it restores into. An unknown outcome keeps
// blocking until a refresh settles it, because the mutation may have been
// applied.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Rubrik operation kinds, mirroring the rubrik_operations kind CHECK.
const (
	RubrikOpSnapshot  = "snapshot"
	RubrikOpSLAAssign = "sla_assign"
	RubrikOpRestore   = "restore"
)

// Rubrik operation states, mirroring the rubrik_operations state CHECK.
//
// applying: the row exists and the mutation may be on its way to RSC.
// running: RSC accepted the mutation and started the job named by the handle.
// succeeded, failed, partial: the job finished (partial is RSC's
// PARTIAL_SUCCESS, never reported as success).
// unknown: the mutation's outcome could not be told (a timeout, an
// unreadable answer). It is never resent, and it blocks the object like an
// open operation until a refresh settles it.
const (
	RubrikOpApplying  = "applying"
	RubrikOpRunning   = "running"
	RubrikOpSucceeded = "succeeded"
	RubrikOpFailed    = "failed"
	RubrikOpPartial   = "partial"
	RubrikOpUnknown   = "unknown"
)

// Rubrik job families: how a started job is followed. A per-namespace
// mutation answers a taskchain; a protection-set mutation an async request.
const (
	RubrikJobTaskchain    = "taskchain"
	RubrikJobAsyncRequest = "async_request"
)

// Size bounds on the stored text, so a caller cannot store an unbounded
// tenant message or request body.
const (
	rubrikOpMaxText     = 512
	rubrikOpMaxFailure  = 4096
	rubrikOpMaxJSONSize = 16 << 10
)

var (
	// ErrRubrikOperationNotFound is returned when no operation has the id.
	ErrRubrikOperationNotFound = errors.New("rubrik operation not found")
	// ErrRubrikOperationExists is returned by InsertOperation when the id is
	// already taken. Nothing is written; the caller reads the stored row to
	// tell a replay from a reused id.
	ErrRubrikOperationExists = errors.New("rubrik operation id already used")
	// ErrRubrikOperationInFlight is returned by InsertOperation when another
	// open operation on the same RSC object, or an open restore into the same
	// namespace, blocks this one. Nothing is written.
	ErrRubrikOperationInFlight = errors.New("another rubrik operation on the object is in flight")
	// ErrRubrikOperationStateChanged is returned by UpdateOperation when the
	// row is no longer in a state the caller allowed: another request or the
	// poller moved it first. Nothing is written, and the current row is
	// returned with the error.
	ErrRubrikOperationStateChanged = errors.New("rubrik operation state changed concurrently")
	// ErrRubrikOperationInvalid is returned for input the store refuses
	// before any SQL runs.
	ErrRubrikOperationInvalid = errors.New("invalid rubrik operation")
)

// RubrikOperation is one durable Rubrik write.
type RubrikOperation struct {
	// ID is the client's operation id.
	ID string
	// Owner is the k8sCenter user id of the requester.
	Owner     string
	ClusterID string
	Kind      string
	// Namespace is the namespace acted on: the object's namespace for a
	// snapshot or SLA change, the target namespace for a restore.
	Namespace   string
	RSCObjectID string
	// RequestHash identifies the request a retry must repeat to be a replay.
	RequestHash string
	// Request is the request as the server resolved it, and Source the
	// resolved restore source. Neither holds secrets.
	Request json.RawMessage
	Source  json.RawMessage
	State   string
	// JobFamily and JobHandle name the RSC job, once one is known.
	JobFamily string
	JobHandle string
	// FailureMessage says why a failed, partial or unknown operation ended
	// as it did.
	FailureMessage string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// FinishedAt is set when the operation reaches a final state.
	FinishedAt *time.Time
}

// RubrikOperationUpdate is one UpdateOperation. An empty JobFamily or
// JobHandle keeps the stored value, so a refresh that learns nothing new
// about the job cannot erase its handle.
type RubrikOperationUpdate struct {
	State          string
	JobFamily      string
	JobHandle      string
	FailureMessage string
}

var (
	rubrikOpKinds  = []string{RubrikOpSnapshot, RubrikOpSLAAssign, RubrikOpRestore}
	rubrikOpStates = []string{RubrikOpApplying, RubrikOpRunning, RubrikOpSucceeded, RubrikOpFailed, RubrikOpPartial, RubrikOpUnknown}
	rubrikOpFinal  = []string{RubrikOpSucceeded, RubrikOpFailed, RubrikOpPartial}
	// rubrikOpOpen are the states that block an object. It must match the
	// state list of idx_rubrik_operations_in_flight in migration 000031.
	rubrikOpOpen        = []string{RubrikOpApplying, RubrikOpRunning, RubrikOpUnknown}
	rubrikOpJobFamilies = []string{"", RubrikJobTaskchain, RubrikJobAsyncRequest}
)

// RubrikOperationFinal reports whether state is final: the job is over and
// the operation no longer blocks its object. unknown is not final.
func RubrikOperationFinal(state string) bool {
	return slices.Contains(rubrikOpFinal, state)
}

const rubrikOperationColumns = `id, owner, cluster_id, kind, namespace, rsc_object_id, request_hash,
	request, source, state, job_family, job_handle, failure_message, created_at, updated_at, finished_at`

func scanRubrikOperation(row pgx.Row) (RubrikOperation, error) {
	var (
		op      RubrikOperation
		id      uuid.UUID
		request []byte
		source  []byte
	)
	err := row.Scan(&id, &op.Owner, &op.ClusterID, &op.Kind, &op.Namespace, &op.RSCObjectID, &op.RequestHash,
		&request, &source, &op.State, &op.JobFamily, &op.JobHandle, &op.FailureMessage,
		&op.CreatedAt, &op.UpdatedAt, &op.FinishedAt)
	if err != nil {
		return RubrikOperation{}, err
	}
	op.ID = id.String()
	op.Request = json.RawMessage(request)
	op.Source = json.RawMessage(source)
	return op, nil
}

// parseRubrikOperationID parses an operation id. The store takes any UUID
// spelling PostgreSQL would; the handler decides which ids clients may mint.
func parseRubrikOperationID(id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: id is not a uuid", ErrRubrikOperationInvalid)
	}
	return u, nil
}

// validText refuses an empty or oversized required field.
func validText(what, s string) error {
	if s == "" || len(s) > rubrikOpMaxText {
		return fmt.Errorf("%w: %s must be 1 to %d bytes", ErrRubrikOperationInvalid, what, rubrikOpMaxText)
	}
	return nil
}

// jsonOrEmpty returns raw as a JSON object document, '{}' when it is empty.
func jsonOrEmpty(what string, raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	if len(raw) > rubrikOpMaxJSONSize || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: %s must be a JSON document of at most %d bytes", ErrRubrikOperationInvalid, what, rubrikOpMaxJSONSize)
	}
	return raw, nil
}

// InsertOperation records op as applying. op.State, CreatedAt, UpdatedAt,
// FinishedAt and the job fields are ignored: a new row has no job yet.
//
// It refuses, writing nothing:
//   - ErrRubrikOperationExists when op.ID is taken, whatever the stored row
//     holds;
//   - ErrRubrikOperationInFlight when an open operation (applying, running or
//     unknown) on the same cluster blocks it: on the same RSC object, an SLA
//     change or restore blocks another SLA change or restore, and a restore
//     also blocks a snapshot; and an open restore blocks every write into a
//     namespace it restores into, whichever object that write acts on.
//
// The namespaces an operation writes into are its Namespace and, for a
// restore, the namespaces its Source lists under "into" (a protection-set
// restore writes into every namespace the set covers).
//
// The SLA-change and restore exclusion on one object holds across replicas
// through the idx_rubrik_operations_in_flight partial unique index. The
// snapshot rule and the namespace rule rest on the NOT EXISTS below alone,
// so two writes inserted in the same instant can both be recorded. That
// window is accepted: it never lets two restores or two SLA changes of one
// object through, and RSC still runs the jobs one after the other.
func (s *RubrikStore) InsertOperation(ctx context.Context, op RubrikOperation) (RubrikOperation, error) {
	id, err := parseRubrikOperationID(op.ID)
	if err != nil {
		return RubrikOperation{}, err
	}
	if !slices.Contains(rubrikOpKinds, op.Kind) {
		return RubrikOperation{}, fmt.Errorf("%w: unknown kind", ErrRubrikOperationInvalid)
	}
	for _, f := range []struct{ what, v string }{
		{"owner", op.Owner}, {"cluster id", op.ClusterID}, {"namespace", op.Namespace},
		{"rsc object id", op.RSCObjectID}, {"request hash", op.RequestHash},
	} {
		if err := validText(f.what, f.v); err != nil {
			return RubrikOperation{}, err
		}
	}
	request, err := jsonOrEmpty("request", op.Request)
	if err != nil {
		return RubrikOperation{}, err
	}
	source, err := jsonOrEmpty("source", op.Source)
	if err != nil {
		return RubrikOperation{}, err
	}

	// The blocking matrix, for an open operation already on the object:
	//
	//	new \ open    snapshot  sla_assign  restore
	//	snapshot      -         -           blocked
	//	sla_assign    -         blocked     blocked
	//	restore       -         blocked     blocked
	//
	// The sla_assign and restore cells are also idx_rubrik_operations_in_flight,
	// which must keep the same kinds and states ($10). Across objects, an
	// open restore blocks every new operation that writes into one of its
	// namespaces ($11).
	out, err := scanRubrikOperation(s.pool.QueryRow(ctx, `
		INSERT INTO rubrik_operations (id, owner, cluster_id, kind, namespace, rsc_object_id,
			request_hash, request, source, state)
		SELECT $1::uuid, $2::text, $3::text, $4::text, $5::text, $6::text, $7::text, $8::jsonb, $9::jsonb, 'applying'
		WHERE NOT EXISTS (
			SELECT 1 FROM rubrik_operations
			WHERE cluster_id = $3::text
			  AND state = ANY($10::text[])
			  AND (
			        (rsc_object_id = $6::text
			         AND (kind = 'restore' OR ($4::text <> 'snapshot' AND kind = 'sla_assign')))
			     OR (kind = 'restore'
			         AND (namespace = ANY($11::text[]) OR source -> 'into' ?| $11::text[]))
			  )
		)
		RETURNING `+rubrikOperationColumns,
		id, op.Owner, op.ClusterID, op.Kind, op.Namespace, op.RSCObjectID, op.RequestHash, request, source,
		rubrikOpOpen, rubrikOpNamespaces(op)))
	if err == nil {
		return out, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return RubrikOperation{}, s.blockedInsert(ctx, op.ID)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		if pgErr.ConstraintName == "idx_rubrik_operations_in_flight" {
			// A concurrent retry of this very id can trip this index
			// before the primary key, so it is not proof of another
			// operation.
			return RubrikOperation{}, s.blockedInsert(ctx, op.ID)
		}
		return RubrikOperation{}, ErrRubrikOperationExists
	}
	return RubrikOperation{}, fmt.Errorf("inserting rubrik operation: %w", err)
}

// rubrikOpNamespaces lists the namespaces op writes into: its Namespace and,
// for a restore, those its Source lists under "into". An unreadable Source
// adds nothing; jsonOrEmpty has already refused one that is not JSON.
func rubrikOpNamespaces(op RubrikOperation) []string {
	out := []string{op.Namespace}
	if op.Kind == RubrikOpRestore && len(op.Source) > 0 {
		var src struct {
			Into []string `json:"into"`
		}
		if json.Unmarshal(op.Source, &src) == nil {
			out = append(out, src.Into...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// blockedInsert names why an insert of id was refused by the in-flight
// rules: when a row with id exists, the caller is retrying and is told
// ErrRubrikOperationExists, never that it is blocked, so a retry is always
// answered from its own row.
func (s *RubrikStore) blockedInsert(ctx context.Context, id string) error {
	_, err := s.GetOperation(ctx, id)
	switch {
	case err == nil:
		return ErrRubrikOperationExists
	case errors.Is(err, ErrRubrikOperationNotFound):
		return ErrRubrikOperationInFlight
	}
	return err
}

// GetOperation returns the operation with id. An id that is not a UUID is
// ErrRubrikOperationNotFound, so ids are not distinguishable by spelling.
func (s *RubrikStore) GetOperation(ctx context.Context, id string) (RubrikOperation, error) {
	u, err := parseRubrikOperationID(id)
	if err != nil {
		return RubrikOperation{}, ErrRubrikOperationNotFound
	}
	op, err := scanRubrikOperation(s.pool.QueryRow(ctx,
		`SELECT `+rubrikOperationColumns+` FROM rubrik_operations WHERE id = $1`, u))
	if errors.Is(err, pgx.ErrNoRows) {
		return RubrikOperation{}, ErrRubrikOperationNotFound
	}
	if err != nil {
		return RubrikOperation{}, fmt.Errorf("getting rubrik operation: %w", err)
	}
	return op, nil
}

// UpdateOperation moves operation id to u.State, but only while it is in
// one of from. A final state stamps finished_at once; any other state
// clears it. Moving back to applying is refused: applying means the
// mutation may not have been sent yet, which is never true after it was.
//
// When the row is not in a state of from, nothing is written and the
// current row comes back with ErrRubrikOperationStateChanged. A missing row
// is ErrRubrikOperationNotFound.
func (s *RubrikStore) UpdateOperation(ctx context.Context, id string, from []string, u RubrikOperationUpdate) (RubrikOperation, error) {
	uid, err := parseRubrikOperationID(id)
	if err != nil {
		return RubrikOperation{}, err
	}
	if len(from) == 0 {
		return RubrikOperation{}, fmt.Errorf("%w: no state to move from", ErrRubrikOperationInvalid)
	}
	for _, st := range from {
		if !slices.Contains(rubrikOpStates, st) {
			return RubrikOperation{}, fmt.Errorf("%w: unknown state to move from", ErrRubrikOperationInvalid)
		}
	}
	if u.State == RubrikOpApplying || !slices.Contains(rubrikOpStates, u.State) {
		return RubrikOperation{}, fmt.Errorf("%w: cannot move to that state", ErrRubrikOperationInvalid)
	}
	if !slices.Contains(rubrikOpJobFamilies, u.JobFamily) {
		return RubrikOperation{}, fmt.Errorf("%w: unknown job family", ErrRubrikOperationInvalid)
	}
	if len(u.JobHandle) > rubrikOpMaxText {
		return RubrikOperation{}, fmt.Errorf("%w: job handle over %d bytes", ErrRubrikOperationInvalid, rubrikOpMaxText)
	}
	if len(u.FailureMessage) > rubrikOpMaxFailure {
		return RubrikOperation{}, fmt.Errorf("%w: failure message over %d bytes", ErrRubrikOperationInvalid, rubrikOpMaxFailure)
	}

	op, err := scanRubrikOperation(s.pool.QueryRow(ctx, `
		UPDATE rubrik_operations SET
			state           = $3::text,
			job_family      = CASE WHEN $4::text = '' THEN job_family ELSE $4::text END,
			job_handle      = CASE WHEN $5::text = '' THEN job_handle ELSE $5::text END,
			failure_message = $6::text,
			updated_at      = now(),
			finished_at     = CASE WHEN $7::boolean THEN COALESCE(finished_at, now()) ELSE NULL END
		WHERE id = $1::uuid AND state = ANY($2::text[])
		RETURNING `+rubrikOperationColumns,
		uid, from, u.State, u.JobFamily, u.JobHandle, u.FailureMessage, RubrikOperationFinal(u.State)))
	if err == nil {
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RubrikOperation{}, fmt.Errorf("updating rubrik operation: %w", err)
	}
	current, getErr := s.GetOperation(ctx, id)
	if getErr != nil {
		return RubrikOperation{}, getErr
	}
	return current, ErrRubrikOperationStateChanged
}
