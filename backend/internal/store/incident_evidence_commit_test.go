package store

import (
	"context"
	"errors"
	"testing"
	"time"
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
