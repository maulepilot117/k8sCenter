package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestInsertBatchCommitRunsFreeOfCallerCancellation (PR #583 round 2):
// a caller context cancelled at the moment of COMMIT must not turn a
// committed batch into an error, so the handler can read every context
// error from InsertBatch as "nothing was recorded". The commit runs under
// context.WithoutCancel bounded by incidentCommitTimeout.
func TestInsertBatchCommitRunsFreeOfCallerCancellation(t *testing.T) {
	is, es, pool := newEvidenceStores(t)
	owner := testOwnerID(t)
	incident := mustCreateIncident(t, is, newIncident(owner, "commit-detached"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	es.beforeCommit = cancel
	rows := []IncidentEvidenceRow{snapshotRow("c1", 500), snapshotRow("c2", 500)}
	n, err := es.InsertBatch(ctx, incident, owner, rows, ceilingLimits)
	if err != nil {
		t.Fatalf("InsertBatch with the caller cancelled at commit = %v; want the committed batch", err)
	}
	if n != 2 {
		t.Fatalf("inserted = %d, want 2", n)
	}
	if errors.Is(ctx.Err(), context.Canceled) == false {
		t.Fatal("fixture: the caller context was not cancelled by the hook")
	}
	requireConsistent(t, readEvidenceTotals(t, pool, incident), 1000, 2, 1)
	// The connection returned to the pool: a fresh write succeeds at once.
	ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel2()
	es.beforeCommit = nil
	if n, err := es.InsertBatch(ctx2, incident, owner, []IncidentEvidenceRow{snapshotRow("c3", 500)}, ceilingLimits); err != nil || n != 1 {
		t.Fatalf("capture after the detached commit = (%d, %v); want (1, nil)", n, err)
	}
}

// TestErrCommitOutcomeUnknownIsDistinct pins the sentinel the handler maps
// to "may or may not have been recorded".
func TestErrCommitOutcomeUnknownIsDistinct(t *testing.T) {
	for _, other := range []error{ErrIncidentBusy, ErrIncidentNotFound, ErrNotOwner, ErrIncidentClosed, ErrIncidentInvalid, context.Canceled} {
		if errors.Is(ErrCommitOutcomeUnknown, other) || errors.Is(other, ErrCommitOutcomeUnknown) {
			t.Fatalf("ErrCommitOutcomeUnknown overlaps %v", other)
		}
	}
	if incidentCommitTimeout <= 0 || incidentCommitTimeout > incidentLockTimeout*2 {
		t.Fatalf("incidentCommitTimeout = %s; want a short positive bound", incidentCommitTimeout)
	}
}

// TestClassifyCommitError (PR #583 round 3) pins which COMMIT failures
// read as "nothing was written" and which as "outcome unknown". A server
// ERROR reply or a reported rollback means the transaction did not commit;
// a FATAL/PANIC reply, an admin-shutdown (57) or connection-exception (08)
// SQLSTATE, a transport error and the commit bound passing can all arrive
// after the server committed locally.
func TestClassifyCommitError(t *testing.T) {
	pg := func(severity, code string) error {
		return &pgconn.PgError{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: "x"}
	}
	cases := []struct {
		name    string
		err     error
		unknown bool
	}{
		{"server reported rollback", pgx.ErrTxCommitRollback, false},
		{"serialization failure", pg("ERROR", "40001"), false},
		{"unique violation", pg("ERROR", "23505"), false},
		{"wrapped ERROR reply", fmt.Errorf("commit: %w", pg("ERROR", "40P01")), false},
		{"admin shutdown FATAL", pg("FATAL", "57P01"), true},
		{"PANIC", pg("PANIC", "XX000"), true},
		{"class 57 at ERROR severity", pg("ERROR", "57014"), true},
		{"class 08 connection exception", pg("ERROR", "08006"), true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"network error", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, true},
		{"commit bound passed", context.DeadlineExceeded, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCommitError(tc.err)
			if got == nil {
				t.Fatal("classifyCommitError returned nil for a failed COMMIT")
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("classified error %v no longer wraps the cause %v", got, tc.err)
			}
			if errors.Is(got, ErrCommitOutcomeUnknown) != tc.unknown {
				t.Fatalf("classifyCommitError(%v): outcome unknown = %t, want %t", tc.err, !tc.unknown, tc.unknown)
			}
		})
	}
}
