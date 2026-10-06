package incidents

// retention_test.go — the retention sweep loop and the configuration resolver
// (Release D, U25a). The loop is driven through an injected ticker and a fake
// store, so no test sleeps for an hour or needs a database.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/store"
)

// fakeRetentionStore records every Cleanup call and lets a test script the
// result, a panic, or a block-until-cancel.
type fakeRetentionStore struct {
	mu          sync.Mutex
	cleanupDays []int
	impact      store.RetentionLoweringImpact
	impactErr   error
	impactPanic bool
	// the persisted applied retention
	applied      int
	appliedFound bool
	appliedErr   error
	recordErr    error
	recorded     []int
	// cleanup is called on every sweep; nil means "delete nothing".
	cleanup func(ctx context.Context, call int, days int) (int64, error)
	called  chan struct{} // receives one value per Cleanup call
}

func newFakeRetentionStore() *fakeRetentionStore {
	return &fakeRetentionStore{called: make(chan struct{}, 64)}
}

func (f *fakeRetentionStore) Cleanup(ctx context.Context, days int) (int64, error) {
	f.mu.Lock()
	f.cleanupDays = append(f.cleanupDays, days)
	call := len(f.cleanupDays)
	fn := f.cleanup
	f.mu.Unlock()
	f.called <- struct{}{}
	if fn == nil {
		return 0, nil
	}
	return fn(ctx, call, days)
}

func (f *fakeRetentionStore) RetentionLoweringImpact(context.Context, int) (store.RetentionLoweringImpact, error) {
	if f.impactPanic {
		panic("poisoned impact row")
	}
	return f.impact, f.impactErr
}

func (f *fakeRetentionStore) AppliedRetentionDays(context.Context) (int, bool, error) {
	return f.applied, f.appliedFound, f.appliedErr
}

func (f *fakeRetentionStore) RecordAppliedRetention(_ context.Context, days int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, days)
	if f.recordErr == nil {
		f.applied, f.appliedFound = days, true // what a real upsert would persist
	}
	return f.recordErr
}

func (f *fakeRetentionStore) recordedDays() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.recorded...)
}

func (f *fakeRetentionStore) calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.cleanupDays...)
}

// fakeTicker hands the loop a channel the test fires by hand.
type fakeTicker struct {
	ch      chan time.Time
	periods chan time.Duration
	stopped chan struct{}
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{ch: make(chan time.Time), periods: make(chan time.Duration, 4), stopped: make(chan struct{})}
}

func (f *fakeTicker) factory(d time.Duration) (<-chan time.Time, func()) {
	f.periods <- d
	return f.ch, func() { close(f.stopped) }
}

// logCapture collects JSON log records.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (l *logCapture) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func startLoop(t *testing.T, r *Retainer) (cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := make(chan struct{})
	go func() {
		defer close(d)
		r.RunLoop(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-d:
		case <-time.After(5 * time.Second):
			t.Error("RunLoop did not stop after cancel")
		}
	})
	return cancel, d
}

// stopLoop cancels the loop and waits for it to return, so every log record
// the loop will ever write is flushed before a test reads them.
func stopLoop(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not stop after cancel")
	}
}

func waitCall(t *testing.T, f *fakeRetentionStore) {
	t.Helper()
	select {
	case <-f.called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a Cleanup call")
	}
}

func newTestRetainer(t *testing.T, st retentionStore, days int, lc *logCapture) (*Retainer, *fakeTicker) {
	t.Helper()
	r := NewRetainer(st, days, lc.logger())
	if r == nil {
		t.Fatal("NewRetainer returned nil for a non-nil store")
	}
	ft := newFakeTicker()
	r.newTicker = ft.factory
	return r, ft
}

func TestRetainer_SweepsImmediatelyThenOnEveryTick(t *testing.T) {
	st := newFakeRetentionStore()
	r, ft := newTestRetainer(t, st, 14, &logCapture{})
	startLoop(t, r)

	waitCall(t, st) // the first sweep happens before any tick fires
	select {
	case d := <-ft.periods:
		if d != time.Hour {
			t.Errorf("sweep period = %s, want 1h", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ticker was never created")
	}
	if got := st.calls(); len(got) != 1 || got[0] != 14 {
		t.Fatalf("calls before the first tick = %v, want one call with 14 days", got)
	}

	ft.ch <- time.Now()
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	if got := st.calls(); len(got) != 3 {
		t.Errorf("calls after two ticks = %v, want 3", got)
	}
}

func TestRetainer_LogsDeletedCountOnlyWhenPositive(t *testing.T) {
	lc := &logCapture{}
	st := newFakeRetentionStore()
	st.cleanup = func(_ context.Context, call, _ int) (int64, error) {
		if call == 1 {
			return 0, nil
		}
		return 3, nil
	}
	r, ft := newTestRetainer(t, st, 30, lc)
	cancel, done := startLoop(t, r)
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	stopLoop(t, cancel, done) // the third sweep's log is written after Cleanup returns; wait for it

	var deletedInfo int
	for _, rec := range lc.records(t) {
		if rec["level"] == "INFO" && rec["deleted"] != nil {
			deletedInfo++
			if rec["deleted"].(float64) != 3 {
				t.Errorf("deleted = %v, want 3", rec["deleted"])
			}
			if rec["retentionDays"].(float64) != 30 {
				t.Errorf("retentionDays = %v, want 30", rec["retentionDays"])
			}
		}
	}
	if deletedInfo != 2 { // calls 2 and 3 deleted 3 rows; call 1 deleted none and must be silent
		t.Errorf("Info deleted-count records = %d, want 2 (a zero-delete sweep must not log)", deletedInfo)
	}
}

func TestRetainer_FailureIsObservableAndLoopContinues(t *testing.T) {
	lc := &logCapture{}
	st := newFakeRetentionStore()
	st.cleanup = func(_ context.Context, call, _ int) (int64, error) {
		if call == 1 {
			return 0, errors.New("connection refused")
		}
		return 0, nil
	}
	r, ft := newTestRetainer(t, st, 30, lc)
	startLoop(t, r)
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	if got := len(st.calls()); got != 2 {
		t.Fatalf("calls = %d, want the loop to sweep again after a failure", got)
	}
	var errorRecords int
	for _, rec := range lc.records(t) {
		if rec["level"] == "ERROR" {
			errorRecords++
			if rec["error"] == nil {
				t.Error("failure record has no error field")
			}
		}
	}
	if errorRecords != 1 {
		t.Errorf("Error records = %d, want 1", errorRecords)
	}
}

// Teeth: removing the recoverutil.Tick wrap from the sweep crashes this test.
func TestRetainer_PanicIsRecoveredAndLoopContinues(t *testing.T) {
	lc := &logCapture{}
	st := newFakeRetentionStore()
	st.cleanup = func(_ context.Context, call, _ int) (int64, error) {
		if call <= 2 { // the immediate sweep AND the first tick sweep both panic
			panic("poisoned row")
		}
		return 0, nil
	}
	r, ft := newTestRetainer(t, st, 30, lc)
	startLoop(t, r)
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	if got := len(st.calls()); got != 3 {
		t.Fatalf("calls = %d, want 3: the loop must survive a panic in the first and a later sweep", got)
	}
	var recovered int
	for _, rec := range lc.records(t) {
		if rec["task"] == "incidents-retention" && strings.Contains(rec["msg"].(string), "panic recovered") {
			recovered++
		}
	}
	if recovered != 2 {
		t.Errorf("recovered-panic records = %d, want 2", recovered)
	}
}

func TestRetainer_PartialProgressIsLoggedOnAFailedSweep(t *testing.T) {
	t.Run("deleted rows then a failure: one Info and one Error", func(t *testing.T) {
		lc := &logCapture{}
		st := newFakeRetentionStore()
		st.cleanup = func(ctx context.Context, call, _ int) (int64, error) {
			if call == 1 {
				return 7, errors.New("statement timeout")
			}
			<-ctx.Done() // the second sweep only parks, so it logs nothing
			return 0, nil
		}
		r, ft := newTestRetainer(t, st, 30, lc)
		cancel, done := startLoop(t, r)
		waitCall(t, st)
		ft.ch <- time.Now() // received only after the first sweep has returned and logged
		waitCall(t, st)
		stopLoop(t, cancel, done)
		var infos, errs int
		for _, rec := range lc.records(t) {
			switch rec["level"] {
			case "INFO":
				infos++
				if rec["deleted"].(float64) != 7 {
					t.Errorf("deleted = %v, want 7", rec["deleted"])
				}
			case "ERROR":
				errs++
			}
		}
		if infos != 1 || errs != 1 {
			t.Errorf("Info=%d Error=%d, want 1 and 1", infos, errs)
		}
	})

	t.Run("shutdown mid-sweep: progress logged, no Error", func(t *testing.T) {
		lc := &logCapture{}
		st := newFakeRetentionStore()
		st.cleanup = func(ctx context.Context, _, _ int) (int64, error) {
			<-ctx.Done()
			return 2, ctx.Err()
		}
		r, _ := newTestRetainer(t, st, 30, lc)
		cancel, done := startLoop(t, r)
		waitCall(t, st)
		stopLoop(t, cancel, done)
		var infos, errs int
		for _, rec := range lc.records(t) {
			switch rec["level"] {
			case "INFO":
				infos++
				if rec["deleted"].(float64) != 2 {
					t.Errorf("deleted = %v, want 2", rec["deleted"])
				}
			case "ERROR":
				errs++
			}
		}
		if infos != 1 || errs != 0 {
			t.Errorf("Info=%d Error=%d, want 1 and 0: a clean shutdown is not a failure", infos, errs)
		}
	})
}

func TestRetainer_StopsPromptlyOnCancel(t *testing.T) {
	t.Run("idle between ticks", func(t *testing.T) {
		st := newFakeRetentionStore()
		r, ft := newTestRetainer(t, st, 30, &logCapture{})
		cancel, done := startLoop(t, r)
		waitCall(t, st)
		<-ft.periods
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("RunLoop did not return after cancel")
		}
		select {
		case <-ft.stopped:
		default:
			t.Error("ticker was not stopped")
		}
	})

	t.Run("blocked inside a sweep", func(t *testing.T) {
		st := newFakeRetentionStore()
		st.cleanup = func(ctx context.Context, _, _ int) (int64, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		r, _ := newTestRetainer(t, st, 30, &logCapture{})
		cancel, done := startLoop(t, r)
		waitCall(t, st)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("RunLoop did not return while a sweep was blocked")
		}
	})

	t.Run("already cancelled does not sweep", func(t *testing.T) {
		st := newFakeRetentionStore()
		r, _ := newTestRetainer(t, st, 30, &logCapture{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r.RunLoop(ctx)
		if got := st.calls(); len(got) != 0 {
			t.Errorf("calls = %v, want none for a cancelled context", got)
		}
	})
}

func TestRetainer_ClampsRetentionAndRejectsNonPositive(t *testing.T) {
	for name, tc := range map[string]struct {
		in, want int
	}{
		"zero is the default, never infinite": {0, config.DefaultIncidentsRetentionDays},
		"negative is the default":             {-7, config.DefaultIncidentsRetentionDays},
		"one is kept":                         {1, 1},
		"above the maximum is clamped":        {store.IncidentMaxRetentionDays + 1, store.IncidentMaxRetentionDays},
	} {
		t.Run(name, func(t *testing.T) {
			lc := &logCapture{}
			st := newFakeRetentionStore()
			r, _ := newTestRetainer(t, st, tc.in, lc)
			if r.retentionDays != tc.want {
				t.Fatalf("retentionDays = %d, want %d", r.retentionDays, tc.want)
			}
			startLoop(t, r)
			waitCall(t, st)
			if got := st.calls(); got[0] != tc.want {
				t.Errorf("Cleanup called with %d days, want %d", got[0], tc.want)
			}
		})
	}
}

func TestRetainer_NilStoreStartsNoLoop(t *testing.T) {
	r := NewRetainer(nil, 30, slog.Default())
	if r != nil {
		t.Fatal("NewRetainer(nil store) must return nil so no loop is started")
	}
	done := make(chan struct{})
	go func() { r.RunLoop(context.Background()); close(done) }() // nil receiver, must not block or panic
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunLoop on a nil Retainer blocked")
	}
}

func TestRetainer_LogUsesOnlyBoundedStructuredFields(t *testing.T) {
	lc := &logCapture{}
	st := newFakeRetentionStore()
	st.impact = store.RetentionLoweringImpact{Count: 2, Oldest: time.Now().Add(-50 * 24 * time.Hour), Newest: time.Now().Add(-40 * 24 * time.Hour)}
	st.cleanup = func(_ context.Context, call, _ int) (int64, error) {
		switch call {
		case 1:
			return 5, nil
		case 2:
			return 0, errors.New("boom")
		}
		panic("p")
	}
	r, ft := newTestRetainer(t, st, 30, lc)
	cancel, done := startLoop(t, r)
	<-ft.periods // the first sweep is deferred: no call before the first tick
	for range 3 {
		ft.ch <- time.Now()
		waitCall(t, st)
	}
	stopLoop(t, cancel, done)

	allowed := map[string]bool{
		"time": true, "level": true, "msg": true, "error": true, "deleted": true,
		"retentionDays": true, "task": true, "panic": true, "stack": true,
		"affectedIncidents": true, "laterAffectedIncidents": true, "oldestCreatedAt": true, "newestCreatedAt": true, "firstSweepDelay": true,
	}
	for _, rec := range lc.records(t) {
		for k := range rec {
			if !allowed[k] {
				t.Errorf("log record carries unexpected field %q: %v", k, rec)
			}
		}
	}
}

func TestRetainer_LoweringDefersOnceThenRestartsAreSteadyState(t *testing.T) {
	affected := store.RetentionLoweringImpact{Count: 4, LaterCount: 6,
		Oldest: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Newest: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)}
	st := newFakeRetentionStore()
	st.impact, st.applied, st.appliedFound = affected, 60, true // lowered from 60 to 30

	// First start: the lowering is new, so the first sweep is deferred.
	lc1 := &logCapture{}
	r1, ft1 := newTestRetainer(t, st, 30, lc1)
	cancel, done := startLoop(t, r1)
	<-ft1.periods
	if got := st.calls(); len(got) != 0 {
		t.Fatalf("sweeps before the grace window = %v, want none", got)
	}
	ft1.ch <- time.Now()
	waitCall(t, st)
	stopLoop(t, cancel, done)
	if got := st.recordedDays(); len(got) != 1 || got[0] != 30 {
		t.Fatalf("recorded applied retention = %v, want [30] after the sweep", got)
	}

	// Restart with incidents still crossing the window (Count > 0): steady
	// state, so no deferral and no Warn.
	lc2 := &logCapture{}
	r2, ft2 := newTestRetainer(t, st, 30, lc2)
	cancel2, done2 := startLoop(t, r2)
	<-ft2.periods
	if got := len(st.calls()); got != 2 {
		t.Fatalf("calls after restart = %d, want 2 (an immediate sweep, no deferral)", got)
	}
	stopLoop(t, cancel2, done2)
	for _, rec := range lc2.records(t) {
		if rec["level"] == "WARN" {
			t.Errorf("restart in steady state logged a Warn: %v", rec)
		}
	}
}

func TestRetainer_PersistenceFailureIsLoggedAndTheLoopContinues(t *testing.T) {
	lc := &logCapture{}
	st := newFakeRetentionStore()
	st.recordErr = errors.New("table missing")
	r, ft := newTestRetainer(t, st, 30, lc)
	cancel, done := startLoop(t, r)
	waitCall(t, st)
	ft.ch <- time.Now()
	waitCall(t, st)
	stopLoop(t, cancel, done)
	if got := len(st.calls()); got != 2 {
		t.Fatalf("sweeps = %d, want the loop to keep sweeping after a persistence failure", got)
	}
	var warns int
	for _, rec := range lc.records(t) {
		if rec["level"] == "WARN" && strings.Contains(rec["msg"].(string), "could not record the applied retention") {
			warns++
		}
	}
	if warns != 2 {
		t.Errorf("persistence-failure Warns = %d, want 2 (one per sweep)", warns)
	}
}

func TestRetainer_FailedSweepDoesNotRecordTheAppliedRetention(t *testing.T) {
	st := newFakeRetentionStore()
	st.cleanup = func(context.Context, int, int) (int64, error) { return 1, errors.New("timeout") }
	r, ft := newTestRetainer(t, st, 30, &logCapture{})
	cancel, done := startLoop(t, r)
	waitCall(t, st)
	ft.ch <- time.Now() // the first sweep is fully handled before the tick is received
	waitCall(t, st)
	stopLoop(t, cancel, done)
	if got := st.recordedDays(); len(got) != 0 {
		t.Errorf("recorded %v after failed sweeps; a failed sweep must not claim the retention was applied", got)
	}
}

func TestRetainer_LoweringDefersTheFirstSweepOneGraceWindow(t *testing.T) {
	affected := store.RetentionLoweringImpact{Count: 4, LaterCount: 6,
		Oldest: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Newest: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)}
	for name, tc := range map[string]struct {
		impact      store.RetentionLoweringImpact
		impactErr   error
		impactPanic bool
		applied     int  // persisted applied retention (configured is 30)
		found       bool // a persisted value exists
		appliedErr  error
		confirmed   bool
		wantWarn    bool
		wantInfo    bool   // an Info notice with laterAffectedIncidents
		wantNow     bool   // sweeps before any tick
		wantMsg     string // substring of the Warn message
		wantDelay   bool   // the Warn carries firstSweepDelay
		wantPanic   bool   // a recovered-panic record is logged
	}{
		// No persisted value: the first run after upgrade. Incidents already
		// past the window mean the lowering is new.
		"no row, past-window rows: warns and defers": {impact: affected, wantWarn: true, wantMsg: "deferred", wantDelay: true},
		"confirmed lowering sweeps immediately":      {impact: affected, confirmed: true, wantWarn: true, wantNow: true, wantMsg: "confirmed by configuration"},
		"normal expiry: no warning, no delay":        {wantNow: true},
		"no row, nothing past the window: steady, Info only": {
			impact: store.RetentionLoweringImpact{LaterCount: 3}, wantInfo: true, wantNow: true},
		// A persisted value above the configured one: a new lowering.
		"lowered below the applied value: warns and defers": {impact: affected, applied: 60, found: true, wantWarn: true, wantMsg: "deferred", wantDelay: true},
		"lowered, only later-affected rows: warns, no delay": {
			impact: store.RetentionLoweringImpact{LaterCount: 3}, applied: 60, found: true, wantWarn: true, wantNow: true, wantMsg: "deleted earlier"},
		"confirmed, nothing past the window, later-affected rows: warns, sweeps now": {
			impact: store.RetentionLoweringImpact{LaterCount: 3}, applied: 60, found: true, confirmed: true, wantWarn: true, wantNow: true, wantMsg: "deleted earlier"},
		// A persisted value equal to the configured one: steady state. Rows are
		// still crossing the window, but the lowering already went through.
		"restart after the lowering was applied: no deferral, no Warn, Info": {
			impact: affected, applied: 30, found: true, wantInfo: true, wantNow: true},
		"raised retention: no deferral, no Warn": {impact: affected, applied: 10, found: true, wantInfo: true, wantNow: true},
		"raised retention, nothing affected: silent": {applied: 10, found: true, wantNow: true},
		"unreadable applied retention defers, fail safe": {
			appliedErr: errors.New("db down"), wantWarn: true, wantMsg: "deferring the first sweep", wantDelay: true},
		"unreadable impact defers, fail safe": {
			impactErr: errors.New("db down"), wantWarn: true, wantMsg: "deferring the first sweep", wantDelay: true},
		"unreadable impact but confirmed sweeps now": {
			impactErr: errors.New("db down"), confirmed: true, wantWarn: true, wantNow: true, wantMsg: "proceeds now"},
		"panicking check defers, fail safe": {impactPanic: true, wantPanic: true},
	} {
		t.Run(name, func(t *testing.T) {
			lc := &logCapture{}
			st := newFakeRetentionStore()
			st.impact, st.impactErr, st.impactPanic = tc.impact, tc.impactErr, tc.impactPanic
			st.applied, st.appliedFound, st.appliedErr = tc.applied, tc.found, tc.appliedErr
			r, ft := newTestRetainer(t, st, 30, lc)
			r.WithLoweringConfirmed(tc.confirmed)
			cancel, done := startLoop(t, r)
			select {
			case d := <-ft.periods: // created right after the (non-)deferral decision
				if d != time.Hour {
					t.Errorf("period = %s", d)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("loop never reached its ticker")
			}
			if got := len(st.calls()); (got > 0) != tc.wantNow {
				t.Fatalf("sweeps before the first tick = %d, wantNow=%v", got, tc.wantNow)
			}
			if !tc.wantNow {
				ft.ch <- time.Now() // the grace window elapses: the deferred sweep runs
				waitCall(t, st)
			}
			stopLoop(t, cancel, done)

			var warned, panicked, infoLater bool
			for _, rec := range lc.records(t) {
				if rec["task"] == retentionTask && strings.Contains(rec["msg"].(string), "panic recovered") {
					panicked = true
				}
				if rec["level"] == "INFO" && rec["laterAffectedIncidents"] != nil {
					infoLater = true
				}
				if rec["level"] != "WARN" {
					continue
				}
				warned = true
				if msg := rec["msg"].(string); !strings.Contains(msg, tc.wantMsg) {
					t.Errorf("warning %q does not contain %q", msg, tc.wantMsg)
				}
				if _, has := rec["firstSweepDelay"]; has != tc.wantDelay {
					t.Errorf("firstSweepDelay present = %v, want %v: %v", has, tc.wantDelay, rec)
				}
				if tc.wantDelay && rec["firstSweepDelay"] != "1h0m0s" {
					t.Errorf("deferral warning has delay %v, want 1h0m0s", rec["firstSweepDelay"])
				}
				if tc.impactErr == nil && tc.appliedErr == nil {
					if rec["laterAffectedIncidents"].(float64) != float64(tc.impact.LaterCount) || rec["retentionDays"].(float64) != 30 {
						t.Errorf("warning fields = %v", rec)
					}
					if tc.impact.Count > 0 && (rec["affectedIncidents"].(float64) != 4 ||
						rec["oldestCreatedAt"] != "2026-08-01T00:00:00Z" || rec["newestCreatedAt"] != "2026-08-20T00:00:00Z") {
						t.Errorf("warning fields = %v", rec)
					}
				}
			}
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v", warned, tc.wantWarn)
			}
			if panicked != tc.wantPanic {
				t.Errorf("recovered-panic record = %v, want %v", panicked, tc.wantPanic)
			}
			if infoLater != tc.wantInfo {
				t.Errorf("steady-state Info notice = %v, want %v", infoLater, tc.wantInfo)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResolveSettings
// ---------------------------------------------------------------------------

func defaultIncidentsConfig() config.IncidentsConfig {
	return config.IncidentsConfig{
		RetentionDays:    config.DefaultIncidentsRetentionDays,
		MaxItemBytes:     config.DefaultIncidentsMaxItemBytes,
		MaxIncidentBytes: config.DefaultIncidentsMaxIncidentBytes,
		MaxItems:         config.DefaultIncidentsMaxItems,
		MaxScopes:        config.DefaultIncidentsMaxScopes,
		CaptureTimeout:   config.DefaultIncidentsCaptureTimeout,
		SourceTimeout:    config.DefaultIncidentsSourceTimeout,
		MaxConcurrency:   config.DefaultIncidentsMaxConcurrency,
	}
}

func TestConfigDefaultsMatchCollectorDefaults(t *testing.T) {
	s := ResolveSettings(defaultIncidentsConfig(), slog.Default())
	if s.Limits != DefaultLimits() {
		t.Errorf("config defaults resolve to %+v, collector DefaultLimits is %+v", s.Limits, DefaultLimits())
	}
	if s.RetentionDays != DefaultRetentionDays {
		t.Errorf("config default retention %d != handler DefaultRetentionDays %d", s.RetentionDays, DefaultRetentionDays)
	}
	if err := s.Limits.Validate(); err != nil {
		t.Errorf("default limits invalid: %v", err)
	}
}

func TestResolveSettings_ClampsEveryLimit(t *testing.T) {
	type tc struct {
		name   string
		mutate func(*config.IncidentsConfig)
		field  string // the configuration field that must be named in the log
		check  func(Settings) (got, want any)
	}
	cases := []tc{
		{"retention zero", func(c *config.IncidentsConfig) { c.RetentionDays = 0 }, "retentionDays",
			func(s Settings) (any, any) { return s.RetentionDays, config.DefaultIncidentsRetentionDays }},
		{"retention negative", func(c *config.IncidentsConfig) { c.RetentionDays = -1 }, "retentionDays",
			func(s Settings) (any, any) { return s.RetentionDays, config.DefaultIncidentsRetentionDays }},
		{"retention above max", func(c *config.IncidentsConfig) { c.RetentionDays = 99999 }, "retentionDays",
			func(s Settings) (any, any) { return s.RetentionDays, store.IncidentMaxRetentionDays }},

		{"item bytes above ceiling", func(c *config.IncidentsConfig) { c.MaxItemBytes = store.EvidenceMaxItemBytesCeiling + 1 }, "maxItemBytes",
			func(s Settings) (any, any) { return s.Limits.MaxItemBytes, store.EvidenceMaxItemBytesCeiling }},
		{"item bytes below redactor minimum", func(c *config.IncidentsConfig) { c.MaxItemBytes = 1 }, "maxItemBytes",
			func(s Settings) (any, any) { return s.Limits.MaxItemBytes, MinMaxBytes }},
		{"item bytes zero", func(c *config.IncidentsConfig) { c.MaxItemBytes = 0 }, "maxItemBytes",
			func(s Settings) (any, any) { return s.Limits.MaxItemBytes, config.DefaultIncidentsMaxItemBytes }},

		{"incident bytes above ceiling", func(c *config.IncidentsConfig) { c.MaxIncidentBytes = store.EvidenceMaxIncidentBytesCeiling + 1 }, "maxIncidentBytes",
			func(s Settings) (any, any) { return s.Limits.MaxIncidentBytes, store.EvidenceMaxIncidentBytesCeiling }},
		{"incident bytes negative", func(c *config.IncidentsConfig) { c.MaxIncidentBytes = -5 }, "maxIncidentBytes",
			func(s Settings) (any, any) { return s.Limits.MaxIncidentBytes, config.DefaultIncidentsMaxIncidentBytes }},

		{"items above ceiling", func(c *config.IncidentsConfig) { c.MaxItems = store.EvidenceMaxItemsCeiling + 1 }, "maxItems",
			func(s Settings) (any, any) { return s.Limits.MaxItems, store.EvidenceMaxItemsCeiling }},
		{"items zero", func(c *config.IncidentsConfig) { c.MaxItems = 0 }, "maxItems",
			func(s Settings) (any, any) { return s.Limits.MaxItems, config.DefaultIncidentsMaxItems }},

		{"scopes above ceiling", func(c *config.IncidentsConfig) { c.MaxScopes = store.EvidenceMaxScopesCeiling + 1 }, "maxScopes",
			func(s Settings) (any, any) { return s.Limits.MaxScopes, store.EvidenceMaxScopesCeiling }},
		{"scopes zero", func(c *config.IncidentsConfig) { c.MaxScopes = 0 }, "maxScopes",
			func(s Settings) (any, any) { return s.Limits.MaxScopes, config.DefaultIncidentsMaxScopes }},

		{"capture timeout above max", func(c *config.IncidentsConfig) { c.CaptureTimeout = time.Hour }, "captureTimeout",
			func(s Settings) (any, any) { return s.Limits.CaptureTimeout, maxCaptureTimeout }},
		{"capture timeout below min", func(c *config.IncidentsConfig) { c.CaptureTimeout = time.Millisecond }, "captureTimeout",
			func(s Settings) (any, any) { return s.Limits.CaptureTimeout, minTimeout }},
		{"capture timeout zero", func(c *config.IncidentsConfig) { c.CaptureTimeout = 0 }, "captureTimeout",
			func(s Settings) (any, any) { return s.Limits.CaptureTimeout, config.DefaultIncidentsCaptureTimeout }},

		{"source timeout above max", func(c *config.IncidentsConfig) { c.CaptureTimeout = maxCaptureTimeout; c.SourceTimeout = time.Hour }, "sourceTimeout",
			func(s Settings) (any, any) { return s.Limits.SourceTimeout, maxSourceTimeout }},
		{"source timeout below min", func(c *config.IncidentsConfig) { c.SourceTimeout = time.Millisecond }, "sourceTimeout",
			func(s Settings) (any, any) { return s.Limits.SourceTimeout, minTimeout }},
		{"source timeout negative", func(c *config.IncidentsConfig) { c.SourceTimeout = -time.Second }, "sourceTimeout",
			func(s Settings) (any, any) { return s.Limits.SourceTimeout, config.DefaultIncidentsSourceTimeout }},

		{"concurrency above max", func(c *config.IncidentsConfig) { c.MaxConcurrency = 1000 }, "maxConcurrency",
			func(s Settings) (any, any) { return s.Limits.MaxConcurrency, maxConcurrencyBound }},
		{"concurrency zero", func(c *config.IncidentsConfig) { c.MaxConcurrency = 0 }, "maxConcurrency",
			func(s Settings) (any, any) { return s.Limits.MaxConcurrency, config.DefaultIncidentsMaxConcurrency }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lc := &logCapture{}
			cfg := defaultIncidentsConfig()
			c.mutate(&cfg)
			s := ResolveSettings(cfg, lc.logger())
			if got, want := c.check(s); got != want {
				t.Errorf("effective value = %v, want %v", got, want)
			}
			if err := s.Limits.Validate(); err != nil {
				t.Errorf("resolved limits are invalid: %v", err)
			}
			var logged bool
			for _, rec := range lc.records(t) {
				if rec["level"] == "WARN" && rec["field"] == c.field {
					logged = true
					if rec["configured"] == nil || rec["effective"] == nil {
						t.Errorf("correction record lacks configured/effective: %v", rec)
					}
				}
			}
			if !logged {
				t.Errorf("correction of %s was not logged: %s", c.field, lc.buf.String())
			}
		})
	}
}

func TestResolveSettings_DurationsAreLoggedAsStrings(t *testing.T) {
	lc := &logCapture{}
	cfg := defaultIncidentsConfig()
	cfg.CaptureTimeout = time.Hour
	ResolveSettings(cfg, lc.logger())
	for _, rec := range lc.records(t) {
		if rec["field"] != "captureTimeout" {
			continue
		}
		if rec["configured"] != "1h0m0s" || rec["effective"] != "5m0s" || rec["min"] != "1s" || rec["max"] != "5m0s" {
			t.Errorf("duration fields = %v, want human-readable strings, not nanoseconds", rec)
		}
		return
	}
	t.Fatal("no captureTimeout correction logged")
}

func TestResolveSettings_InRangeValuesAreKeptAndSilent(t *testing.T) {
	lc := &logCapture{}
	cfg := config.IncidentsConfig{
		RetentionDays: 365, MaxItemBytes: 4096, MaxIncidentBytes: 65536, MaxItems: 10, MaxScopes: 2,
		CaptureTimeout: 10 * time.Second, SourceTimeout: 3 * time.Second, MaxConcurrency: 8,
	}
	s := ResolveSettings(cfg, lc.logger())
	want := Limits{MaxItemBytes: 4096, MaxIncidentBytes: 65536, MaxItems: 10, MaxScopes: 2,
		CaptureTimeout: 10 * time.Second, SourceTimeout: 3 * time.Second, MaxConcurrency: 8}
	if s.Limits != want || s.RetentionDays != 365 {
		t.Errorf("Settings = %+v, want limits %+v retention 365", s, want)
	}
	if recs := lc.records(t); len(recs) != 0 {
		t.Errorf("in-range configuration logged %v", recs)
	}
	if got := s.Limits.EvidenceLimits(); got.MaxItems != 10 || got.MaxScopes != 2 {
		t.Errorf("EvidenceLimits() = %+v", got)
	}
}

func TestResolveSettings_EveryEvidenceLimitAcceptsItsExactCeiling(t *testing.T) {
	lc := &logCapture{}
	cfg := defaultIncidentsConfig()
	cfg.MaxItemBytes = store.EvidenceMaxItemBytesCeiling
	cfg.MaxIncidentBytes = store.EvidenceMaxIncidentBytesCeiling
	cfg.MaxItems = store.EvidenceMaxItemsCeiling
	cfg.MaxScopes = store.EvidenceMaxScopesCeiling
	s := ResolveSettings(cfg, lc.logger())
	want := store.EvidenceLimits{
		MaxItemBytes: store.EvidenceMaxItemBytesCeiling, MaxIncidentBytes: store.EvidenceMaxIncidentBytesCeiling,
		MaxItems: store.EvidenceMaxItemsCeiling, MaxScopes: store.EvidenceMaxScopesCeiling,
	}
	if got := s.Limits.EvidenceLimits(); got != want {
		t.Errorf("EvidenceLimits() = %+v, want the exact ceilings %+v", got, want)
	}
	if err := s.Limits.EvidenceLimits().Validate(); err != nil {
		t.Errorf("ceiling limits rejected by the store: %v", err)
	}
	if recs := lc.records(t); len(recs) != 0 {
		t.Errorf("exact ceilings logged corrections: %v", recs)
	}
	// One past each ceiling is clamped back to it.
	cfg.MaxItemBytes++
	cfg.MaxIncidentBytes++
	cfg.MaxItems++
	cfg.MaxScopes++
	if got := ResolveSettings(cfg, slog.New(slog.DiscardHandler)).Limits.EvidenceLimits(); got != want {
		t.Errorf("ceiling+1 resolved to %+v, want %+v", got, want)
	}
}

func TestResolveSettings_CrossFieldConsistency(t *testing.T) {
	t.Run("incident bytes below item bytes is raised and logged", func(t *testing.T) {
		lc := &logCapture{}
		cfg := defaultIncidentsConfig()
		cfg.MaxItemBytes = 8192
		cfg.MaxIncidentBytes = 4096
		s := ResolveSettings(cfg, lc.logger())
		if s.Limits.MaxIncidentBytes != 8192 || s.Limits.MaxItemBytes != 8192 {
			t.Errorf("limits = item %d incident %d, want both 8192", s.Limits.MaxItemBytes, s.Limits.MaxIncidentBytes)
		}
		if err := s.Limits.Validate(); err != nil {
			t.Errorf("resolved limits invalid: %v", err)
		}
		if !loggedField(lc.records(t), "maxIncidentBytes") {
			t.Errorf("correction not logged: %s", lc.buf.String())
		}
	})
	t.Run("source timeout above capture timeout is capped and logged", func(t *testing.T) {
		lc := &logCapture{}
		cfg := defaultIncidentsConfig()
		cfg.CaptureTimeout = 10 * time.Second
		cfg.SourceTimeout = 30 * time.Second
		s := ResolveSettings(cfg, lc.logger())
		if s.Limits.SourceTimeout != 10*time.Second || s.Limits.CaptureTimeout != 10*time.Second {
			t.Errorf("timeouts = source %s capture %s, want both 10s", s.Limits.SourceTimeout, s.Limits.CaptureTimeout)
		}
		if !loggedField(lc.records(t), "sourceTimeout") {
			t.Errorf("correction not logged: %s", lc.buf.String())
		}
	})
	t.Run("consistent values are untouched and silent", func(t *testing.T) {
		lc := &logCapture{}
		cfg := defaultIncidentsConfig()
		cfg.MaxItemBytes, cfg.MaxIncidentBytes = 4096, 4096
		cfg.CaptureTimeout, cfg.SourceTimeout = 7*time.Second, 7*time.Second
		s := ResolveSettings(cfg, lc.logger())
		if s.Limits.MaxIncidentBytes != 4096 || s.Limits.SourceTimeout != 7*time.Second {
			t.Errorf("equal values were altered: %+v", s.Limits)
		}
		if recs := lc.records(t); len(recs) != 0 {
			t.Errorf("logged %v", recs)
		}
	})
}

func loggedField(recs []map[string]any, field string) bool {
	for _, rec := range recs {
		if rec["level"] == "WARN" && rec["field"] == field {
			return true
		}
	}
	return false
}

func TestResolveSettings_ExactCeilingsAreAccepted(t *testing.T) {
	lc := &logCapture{}
	cfg := defaultIncidentsConfig()
	cfg.RetentionDays = store.IncidentMaxRetentionDays
	s := ResolveSettings(cfg, lc.logger())
	if s.RetentionDays != store.IncidentMaxRetentionDays || s.Limits != DefaultLimits() {
		t.Errorf("ceiling values were altered: %+v", s)
	}
	if recs := lc.records(t); len(recs) != 0 {
		t.Errorf("ceiling values logged corrections: %v", recs)
	}
}
