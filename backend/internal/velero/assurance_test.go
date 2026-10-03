package velero

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // CRON_TZ cases must not depend on the host's zone database

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubecenter/kubecenter/internal/store"
)

// Release F U33: the backup freshness and schedule-outcome evaluator.

// ---- fixtures ----

const testCluster = "local"

var (
	// evalNow is a Thursday; the daily fixture schedule fires at 03:00 UTC.
	evalNow   = at("2026-09-10T12:00:00Z")
	policyIDs = []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		uuid.MustParse("00000000-0000-0000-0000-000000000004"),
	}
)

func policy(id int, kind store.AssuranceScopeKind, ns, name string) store.BackupAssurancePolicy {
	return store.BackupAssurancePolicy{
		ID:             policyIDs[id],
		ClusterID:      testCluster,
		ScopeKind:      kind,
		ScopeNamespace: ns,
		ScopeName:      name,
		MaxAge:         24 * time.Hour,
		Grace:          time.Hour,
		TreatPartialAs: store.AssuranceTreatPartialAsFailure,
		AlertOnPaused:  true,
		Enabled:        true,
	}
}

func dailyPolicy() store.BackupAssurancePolicy {
	return policy(0, store.ScopeSchedule, veleroNamespace, "daily")
}

func dailySchedule() Schedule {
	return Schedule{
		Name:       "daily",
		Namespace:  veleroNamespace,
		UID:        "uid-daily",
		Phase:      "Enabled",
		Schedule:   "0 3 * * *",
		LastBackup: ptr(at("2026-09-10T03:00:00Z")),
		created:    at("2026-08-01T00:00:00Z"),
	}
}

func availableDefaultBSL() BackupStorageLocation {
	return BackupStorageLocation{Name: "default", Namespace: veleroNamespace, Phase: "Available", Default: true}
}

// dailyRun is a run of the daily schedule that started at start and, when
// done is non-empty, completed then.
func dailyRun(name, phase, start, done string) Backup {
	b := Backup{Name: name, Namespace: veleroNamespace, Phase: phase, ScheduleName: "daily", UID: "uid-" + name}
	if start != "" {
		b.StartTime = ptr(at(start))
		b.created = at(start)
	}
	if done != "" {
		b.CompletionTime = ptr(at(done))
	}
	return b
}

func observe(schedules []Schedule, backups []Backup, bsls ...BackupStorageLocation) Observation {
	return Observation{
		ClusterID:   testCluster,
		CollectedAt: evalNow,
		Collection:  CollectionOK,
		Status:      VeleroStatus{Detected: true, Namespace: veleroNamespace},
		Backups:     backups,
		Schedules:   schedules,
		Locations:   &LocationsResponse{BackupStorageLocations: bsls},
	}
}

// dailyObs is the daily schedule, its runs, and an Available default BSL.
func dailyObs(runs ...Backup) Observation {
	return observe([]Schedule{dailySchedule()}, runs, availableDefaultBSL())
}

func conditionsOf(fs []Finding) []store.AssuranceCondition {
	out := make([]store.AssuranceCondition, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Condition)
	}
	return out
}

func findingFor(t *testing.T, fs []Finding, c store.AssuranceCondition) Finding {
	t.Helper()
	for _, f := range fs {
		if f.Condition == c {
			return f
		}
	}
	t.Fatalf("no %s finding in %v", c, conditionsOf(fs))
	return Finding{}
}

func assertConditions(t *testing.T, fs []Finding, want ...store.AssuranceCondition) {
	t.Helper()
	got := conditionsOf(fs)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("conditions = %v, want %v", got, want)
	}
}

func timeEq(t *testing.T, what string, got *time.Time, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %v, want nil", what, got)
		}
		return
	}
	if got == nil || !got.Equal(at(want)) {
		t.Errorf("%s = %v, want %s", what, got, want)
	}
}

// ---- FreshnessOutcomeOf ----

func TestFreshnessOutcomeOf_CompletedIsSuccess(t *testing.T) {
	if got := FreshnessOutcomeOf("Completed"); got != OutcomeSuccess {
		t.Errorf("Completed = %q, want success", got)
	}
}

func TestFreshnessOutcomeOf_PartiallyFailedIsPartial(t *testing.T) {
	if got := FreshnessOutcomeOf("PartiallyFailed"); got != OutcomePartial {
		t.Errorf("PartiallyFailed = %q, want partial, never success or plain failure", got)
	}
}

func TestFreshnessOutcomeOf_FailedAndFailedValidationAreFailure(t *testing.T) {
	for _, phase := range []string{"Failed", "FailedValidation"} {
		if got := FreshnessOutcomeOf(phase); got != OutcomeFailure {
			t.Errorf("%s = %q, want failure", phase, got)
		}
	}
}

func TestFreshnessOutcomeOf_InFlightPhasesNeverCountAsSuccessOrFailure(t *testing.T) {
	// "" is a backup Velero has not picked up yet (BackupOutcomeOf agrees).
	for _, phase := range []string{"", "New", "Queued", "ReadyToStart", "InProgress",
		"WaitingForPluginOperations", "WaitingForPluginOperationsPartiallyFailed",
		"Finalizing", "FinalizingPartiallyFailed"} {
		if got := FreshnessOutcomeOf(phase); got != OutcomeInFlight {
			t.Errorf("%q = %q, want in_flight", phase, got)
		}
	}
}

func TestFreshnessOutcomeOf_DoesNotTreatBSLOrSchedulePhasesAsSuccess(t *testing.T) {
	for _, phase := range []string{"Available", "Unavailable", "Enabled", "Deleting", "completed", "SomethingNew"} {
		if got := FreshnessOutcomeOf(phase); got != OutcomeUnknown {
			t.Errorf("%q = %q, want unknown", phase, got)
		}
	}
}

// The evaluator and the schedules page's Newest Run column (PR #547) must
// agree on every phase: each freshness outcome folds onto exactly one
// dashboard outcome. The fold is written out here, not derived from the
// production mapping, so a drift on either side fails.
func TestFreshnessOutcomeOf_AgreesWithBackupOutcomeOf(t *testing.T) {
	fold := map[FreshnessOutcome]BackupOutcome{
		OutcomeSuccess:  BackupOutcomeSucceeded,
		OutcomePartial:  BackupOutcomeFailed,
		OutcomeFailure:  BackupOutcomeFailed,
		OutcomeInFlight: BackupOutcomeInProgress,
		OutcomeUnknown:  BackupOutcomeUnknown,
	}
	for _, phase := range []string{"", "New", "Queued", "ReadyToStart", "InProgress",
		"WaitingForPluginOperations", "WaitingForPluginOperationsPartiallyFailed",
		"Finalizing", "FinalizingPartiallyFailed", "Completed", "PartiallyFailed",
		"Failed", "FailedValidation", "Deleting", "Available", "Enabled", "x"} {
		if got, want := fold[FreshnessOutcomeOf(phase)], BackupOutcomeOf(phase); got != want {
			t.Errorf("%q: freshness %q folds to %q, dashboard says %q", phase, FreshnessOutcomeOf(phase), got, want)
		}
	}
}

// ---- Evaluate: collection health (the hard rule) ----

func TestEvaluate_CollectionFailedYieldsSingleCollectionUnknown(t *testing.T) {
	// Evidence that, if believed, would open never_run, overdue, failed
	// and location_unavailable.
	never := dailySchedule()
	never.Name, never.UID, never.LastBackup = "never", "uid-never", nil
	evidence := observe([]Schedule{dailySchedule(), never},
		[]Backup{dailyRun("daily-1", "Failed", "2026-09-01T03:00:00Z", "")})
	policies := []store.BackupAssurancePolicy{
		dailyPolicy(),
		policy(1, store.ScopeSchedule, veleroNamespace, "never"),
		policy(2, store.ScopeCluster, "", ""),
	}

	cases := map[string]func(*Observation){
		"failed":       func(o *Observation) { o.Collection = CollectionFailed },
		"not detected": func(o *Observation) { o.Status.Detected = false },
		"unset":        func(o *Observation) { o.Collection = "" },
		"unrecognised": func(o *Observation) { o.Collection = "partial" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			obs := evidence
			mutate(&obs)
			got := Evaluate(obs, policies, evalNow)
			if len(got) != 1 {
				t.Fatalf("got %d findings %v, want exactly one", len(got), conditionsOf(got))
			}
			f := got[0]
			if f.Condition != store.ConditionCollectionUnknown || f.Subject != (Subject{Kind: store.ScopeCluster}) {
				t.Errorf("finding = %+v, want one cluster-scoped collection_unknown", f)
			}
			if f.PolicyID != policyIDs[2] {
				t.Errorf("policy = %v, want the cluster-scope policy", f.PolicyID)
			}
		})
	}
}

func TestEvaluate_CollectionFailedNeverClaimsNoBackupsExist(t *testing.T) {
	// A failed read looks exactly like an empty cluster: no schedules, no
	// backups. That must read as "unknown", never as "no backups".
	obs := observe(nil, nil)
	obs.Collection = CollectionFailed
	got := Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy(), policy(1, store.ScopeNamespace, "app", "")}, evalNow)
	assertConditions(t, got, store.ConditionCollectionUnknown)
	if d := got[0].Detail; d.LastOutcome != OutcomeUnknown || d.LastSuccessAt != nil {
		t.Errorf("detail = %+v, want outcome unknown with no last-success claim", d)
	}
}

func TestEvaluate_CollectionDegradedScopesUnknownToAffectedSubjectsOnly(t *testing.T) {
	staleApp := Backup{Name: "app-1", Namespace: veleroNamespace, Phase: "Completed",
		IncludedNamespaces: []string{"app"}, CompletionTime: ptr(at("2026-09-01T00:00:00Z"))}
	policies := []store.BackupAssurancePolicy{dailyPolicy(), policy(1, store.ScopeNamespace, "app", "")}
	appSubject := Subject{Kind: store.ScopeNamespace, Namespace: "app"}

	t.Run("schedules missing", func(t *testing.T) {
		obs := dailyObs(staleApp)
		obs.Collection, obs.FailedLists = CollectionDegraded, []string{"schedules"}
		got := Evaluate(obs, policies, evalNow)
		want := []Finding{
			{Subject: appSubject, PolicyID: policyIDs[1], Condition: store.ConditionOverdue},
			{Subject: Subject{Kind: store.ScopeSchedule, Namespace: veleroNamespace, Name: "daily"}, PolicyID: policyIDs[0], Condition: store.ConditionCollectionUnknown},
		}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %d findings", conditionsOf(got), len(want))
		}
		for i := range want {
			if got[i].Subject != want[i].Subject || got[i].Condition != want[i].Condition || got[i].PolicyID != want[i].PolicyID {
				t.Errorf("finding %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("locations missing", func(t *testing.T) {
		// A stale daily schedule would be overdue; without the location
		// list it is unknown instead. The namespace subject needs only
		// backups and is still evaluated.
		obs := dailyObs(staleApp, dailyRun("daily-1", "Completed", "2026-09-01T03:00:00Z", "2026-09-01T03:05:00Z"))
		obs.Collection, obs.FailedLists = CollectionDegraded, []string{"backupstoragelocations"}
		got := Evaluate(obs, policies, evalNow)
		for _, f := range got {
			if f.Subject.Kind == store.ScopeSchedule {
				if f.Condition != store.ConditionCollectionUnknown || f.Subject.UID != "uid-daily" {
					t.Errorf("schedule finding = %+v, want only collection_unknown bound to its UID", f)
				}
			}
		}
		assertConditions(t, got, store.ConditionCollectionUnknown, store.ConditionOverdue)
	})

	t.Run("unnamed failure", func(t *testing.T) {
		obs := dailyObs(staleApp)
		obs.Collection = CollectionDegraded
		got := Evaluate(obs, policies, evalNow)
		assertConditions(t, got, store.ConditionCollectionUnknown, store.ConditionCollectionUnknown)
	})

	t.Run("restores missing changes nothing", func(t *testing.T) {
		obs := dailyObs(staleApp)
		obs.Collection, obs.FailedLists = CollectionDegraded, []string{"restores", "volumesnapshotlocations"}
		got := Evaluate(obs, policies, evalNow)
		ok := Evaluate(dailyObs(staleApp), policies, evalNow)
		if !reflect.DeepEqual(got, ok) {
			t.Errorf("degraded on unused lists = %v, want the ok evaluation %v", conditionsOf(got), conditionsOf(ok))
		}
	})
}

// ---- Evaluate: freshness ----

func TestEvaluate_OverdueWhenPastMaxAgePlusGrace(t *testing.T) {
	// Last success 32h55m ago; today's 03:00 run is 9h overdue.
	got := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-09T03:00:00Z", "2026-09-09T03:05:00Z")),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	f := got[0]
	if f.Severity != store.AssuranceSeverityWarning || f.Subject.UID != "uid-daily" || f.Hold {
		t.Errorf("finding = %+v, want an unheld warning bound to the schedule UID", f)
	}
	timeEq(t, "lastSuccessAt", f.Detail.LastSuccessAt, "2026-09-09T03:05:00Z")
	timeEq(t, "expectedRunAt", f.Detail.ExpectedRunAt, "2026-09-10T03:00:00Z")
	if !f.Detail.ExpectedRunKnown || f.Detail.LastOutcome != OutcomeSuccess {
		t.Errorf("detail = %+v, want a known expected run and last outcome success", f.Detail)
	}
}

func TestEvaluate_NotOverdueInsideGrace(t *testing.T) {
	p := dailyPolicy()
	// 24h30m old: past max_age, inside max_age + grace.
	got := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-09T11:25:00Z", "2026-09-09T11:30:00Z")),
		[]store.BackupAssurancePolicy{p}, evalNow)
	assertConditions(t, got)

	// The expected run's own grace: a weekly (Monday 03:00) schedule whose
	// last success was last Monday. This Monday's run is missed, but only
	// by 30 minutes when now is 03:30, so the subject is not yet overdue
	// even though the max-age floor is long exceeded; an hour later it is.
	s := dailySchedule()
	s.Schedule = "0 3 * * 1"
	weekly := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-07T03:00:00Z", "2026-09-07T03:05:00Z")}, availableDefaultBSL())
	assertConditions(t, Evaluate(weekly, []store.BackupAssurancePolicy{p}, at("2026-09-14T03:30:00Z")))
	assertConditions(t, Evaluate(weekly, []store.BackupAssurancePolicy{p}, at("2026-09-14T04:30:00Z")), store.ConditionOverdue)
}

func TestEvaluate_LatestTickInsideGraceDoesNotMaskEarlierMissedRun(t *testing.T) {
	p := dailyPolicy()
	// Today's 03:00 run is only 30 minutes late, but yesterday's was missed
	// by 24.5h: overdue, and the detail still names the latest expected run.
	got := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")),
		[]store.BackupAssurancePolicy{p}, at("2026-09-10T03:30:00Z"))
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "expectedRunAt", got[0].Detail.ExpectedRunAt, "2026-09-10T03:00:00Z")

	// An hourly schedule under the default one-hour grace: the latest tick
	// is always under an hour old, yet a two-day-old success is overdue.
	s := dailySchedule()
	s.Schedule = "0 * * * *"
	hourly := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-08T12:00:00Z", "2026-09-08T12:05:00Z")}, availableDefaultBSL())
	got = Evaluate(hourly, []store.BackupAssurancePolicy{p}, at("2026-09-10T12:30:00Z"))
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "expectedRunAt", got[0].Detail.ExpectedRunAt, "2026-09-10T12:00:00Z")
}

func TestEvaluate_ExpectedRunSparesAScheduleWithNoRunDue(t *testing.T) {
	// A weekly (Monday 03:00) schedule under a 24h max_age: two days after
	// Monday's success no run was expected, so it is not overdue, though
	// the plain max-age rule alone would say it is.
	s := dailySchedule()
	s.Schedule = "0 3 * * 1"
	obs := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-07T03:00:00Z", "2026-09-07T03:05:00Z")}, availableDefaultBSL())
	got := Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got)

	// The following Monday's run is missed: overdue.
	got = Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, at("2026-09-14T06:00:00Z"))
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "expectedRunAt", got[0].Detail.ExpectedRunAt, "2026-09-14T03:00:00Z")
}

func TestEvaluate_InFlightRunNeitherResetsNorSuppressesOverdue(t *testing.T) {
	got := Evaluate(dailyObs(
		dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z"),
		dailyRun("daily-2", "InProgress", "2026-09-10T03:00:00Z", ""),
		dailyRun("daily-3", "FinalizingPartiallyFailed", "2026-09-10T04:00:00Z", ""),
	), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "lastSuccessAt", got[0].Detail.LastSuccessAt, "2026-09-08T03:05:00Z")
	if got[0].Detail.LastOutcome != OutcomeSuccess {
		t.Errorf("lastOutcome = %q, want the newest finished run's (success)", got[0].Detail.LastOutcome)
	}
}

func TestEvaluate_FailedOpensCriticalAndCoexistsWithOverdue(t *testing.T) {
	failed := dailyRun("daily-2", "Failed", "2026-09-10T03:00:00Z", "2026-09-10T03:01:00Z")
	got := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z"), failed),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionFailed, store.ConditionOverdue)
	if f := findingFor(t, got, store.ConditionFailed); f.Severity != store.AssuranceSeverityCritical || f.Detail.LastOutcome != OutcomeFailure {
		t.Errorf("failed finding = %+v, want critical with last outcome failure", f)
	}

	// A later success supersedes the failure and resets the clock.
	got = Evaluate(dailyObs(failed, dailyRun("daily-3", "Completed", "2026-09-10T05:00:00Z", "2026-09-10T05:10:00Z")),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got)

	// FailedValidation never starts, so it has no start time; it still
	// counts as the newest failed run.
	fv := Backup{Name: "daily-4", Namespace: veleroNamespace, Phase: "FailedValidation", ScheduleName: "daily", created: at("2026-09-10T06:00:00Z")}
	got = Evaluate(dailyObs(dailyRun("daily-3", "Completed", "2026-09-10T05:00:00Z", "2026-09-10T05:10:00Z"), fv),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionFailed)
}

func TestEvaluate_PartiallyFailedTreatedAsFailureByDefault(t *testing.T) {
	got := Evaluate(dailyObs(
		dailyRun("daily-1", "Completed", "2026-09-09T03:00:00Z", "2026-09-09T03:05:00Z"),
		dailyRun("daily-2", "PartiallyFailed", "2026-09-10T03:00:00Z", "2026-09-10T03:05:00Z"),
	), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	// The partial run does not reset the clock, so today's run counts as
	// missed as well.
	assertConditions(t, got, store.ConditionOverdue, store.ConditionPartiallyFailed)
	f := findingFor(t, got, store.ConditionPartiallyFailed)
	if f.Detail.LastOutcome != OutcomePartial {
		t.Errorf("lastOutcome = %q, want partial", f.Detail.LastOutcome)
	}
	timeEq(t, "lastSuccessAt", f.Detail.LastSuccessAt, "2026-09-09T03:05:00Z")
}

func TestEvaluate_PartiallyFailedTreatedAsSuccessStillReportsPartialOutcome(t *testing.T) {
	p := dailyPolicy()
	p.TreatPartialAs = store.AssuranceTreatPartialAsSuccess

	// A fresh partial run resets the clock: nothing is open.
	got := Evaluate(dailyObs(dailyRun("daily-2", "PartiallyFailed", "2026-09-10T03:00:00Z", "2026-09-10T03:05:00Z")),
		[]store.BackupAssurancePolicy{p}, evalNow)
	assertConditions(t, got)

	// An old partial run is the last success, and the evidence says so:
	// "last success was partial", never a bare success.
	got = Evaluate(dailyObs(dailyRun("daily-2", "PartiallyFailed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")),
		[]store.BackupAssurancePolicy{p}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	if d := got[0].Detail; d.LastOutcome != OutcomePartial {
		t.Errorf("lastOutcome = %q, want partial", d.LastOutcome)
	}
	timeEq(t, "lastSuccessAt", got[0].Detail.LastSuccessAt, "2026-09-08T03:05:00Z")
}

func TestEvaluate_CompletedWithNilCompletionTimeFallsBackToStartTime(t *testing.T) {
	// getTime leaves CompletionTime nil on a missing or unparseable
	// status.completionTimestamp; the start time stands in.
	fresh := dailyRun("daily-1", "Completed", "2026-09-10T03:00:00Z", "")
	assertConditions(t, Evaluate(dailyObs(fresh), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow))

	stale := dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "")
	got := Evaluate(dailyObs(stale), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "lastSuccessAt", got[0].Detail.LastSuccessAt, "2026-09-08T03:00:00Z")
}

func TestEvaluate_CompletedWithNoTimestampsIsUnknownNotSuccess(t *testing.T) {
	timeless := Backup{Name: "daily-x", Namespace: veleroNamespace, Phase: "Completed", ScheduleName: "daily", created: at("2026-09-10T03:00:00Z")}

	// As the only evidence: the subject's freshness is unknown.
	got := Evaluate(dailyObs(timeless), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionCollectionUnknown)
	if got[0].Subject.UID != "uid-daily" || got[0].Detail.LastSuccessAt != nil {
		t.Errorf("finding = %+v, want subject-scoped unknown with no last success", got[0])
	}

	// Beside an old success: it does not reset the clock.
	got = Evaluate(dailyObs(timeless, dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "lastSuccessAt", got[0].Detail.LastSuccessAt, "2026-09-08T03:05:00Z")
}

// ---- Evaluate: paused, never run ----

func pausedObs() Observation {
	s := dailySchedule()
	s.Paused = true
	return observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")}, availableDefaultBSL())
}

func TestEvaluate_PausedScheduleSuppressesOverdueAndEmitsPaused(t *testing.T) {
	got := Evaluate(pausedObs(), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue, store.ConditionPaused)
	if f := findingFor(t, got, store.ConditionPaused); f.Severity != store.AssuranceSeverityWarning || f.Hold {
		t.Errorf("paused finding = %+v, want an unheld warning", f)
	}
	// overdue is held: it may never open an exception for a paused
	// schedule.
	if f := findingFor(t, got, store.ConditionOverdue); !f.Hold || f.Detail.SuppressedBy != SuppressedByPaused {
		t.Errorf("overdue finding = %+v, want Hold with suppressedBy paused", f)
	}

	p := dailyPolicy()
	p.AlertOnPaused = false
	got = Evaluate(pausedObs(), []store.BackupAssurancePolicy{p}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	if !got[0].Hold {
		t.Errorf("overdue on a paused schedule must stay held with alertOnPaused off")
	}
}

func TestEvaluate_PausedScheduleDoesNotResolveExistingOverdue(t *testing.T) {
	// Overdue while running; then paused. The overdue identity must still
	// be reported (held), so the reconciler observes, not resolves, it.
	running := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	paused := Evaluate(pausedObs(), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	before := findingFor(t, running, store.ConditionOverdue)
	after := findingFor(t, paused, store.ConditionOverdue)
	if before.Subject != after.Subject || before.PolicyID != after.PolicyID {
		t.Errorf("paused overdue identity %+v differs from the running one %+v", after.Subject, before.Subject)
	}
	if before.Hold || !after.Hold {
		t.Errorf("hold: running %v, paused %v; want false then true", before.Hold, after.Hold)
	}
}

func TestEvaluate_NeverRunIsDistinctFromOverdue(t *testing.T) {
	s := dailySchedule()
	s.LastBackup = nil
	never := observe([]Schedule{s}, nil, availableDefaultBSL())
	got := Evaluate(never, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionNeverRun)
	if d := got[0].Detail; d.LastSuccessAt != nil || d.LastOutcome != OutcomeUnknown {
		t.Errorf("never_run detail = %+v, want no last success and outcome unknown", d)
	}

	// A schedule created an hour ago has not missed anything yet.
	s.created = evalNow.Add(-time.Hour)
	assertConditions(t, Evaluate(observe([]Schedule{s}, nil, availableDefaultBSL()), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow))

	// A schedule whose only run failed has run: failed and overdue, not
	// never_run.
	s.created = at("2026-08-01T00:00:00Z")
	got = Evaluate(observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Failed", "2026-09-10T03:00:00Z", "")}, availableDefaultBSL()),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionFailed, store.ConditionOverdue)

	// status.lastBackup set but no run visible (expired): overdue, not
	// never_run.
	s.LastBackup = ptr(at("2026-08-02T03:00:00Z"))
	got = Evaluate(observe([]Schedule{s}, nil, availableDefaultBSL()), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
}

func TestEvaluate_ScheduleRunsMatchVeleroTruncatedLabel(t *testing.T) {
	// Velero shortens a >63-character schedule name in its label; the
	// evaluator matches runs exactly as the schedules page does.
	long := strings.Repeat("s", 70)
	s := dailySchedule()
	s.Name = long
	run := dailyRun("r", "Completed", "2026-09-10T03:00:00Z", "2026-09-10T03:05:00Z")
	run.ScheduleName = scheduleLabelValue(long)
	got := Evaluate(observe([]Schedule{s}, []Backup{run}, availableDefaultBSL()),
		[]store.BackupAssurancePolicy{policy(0, store.ScopeSchedule, veleroNamespace, long)}, evalNow)
	assertConditions(t, got)
}

// ---- Evaluate: storage locations ----

func freshDaily() Backup {
	return dailyRun("daily-1", "Completed", "2026-09-10T03:00:00Z", "2026-09-10T03:05:00Z")
}

func TestEvaluate_MissingStorageLocationYieldsLocationUnavailable(t *testing.T) {
	s := dailySchedule()
	s.StorageLocation = "offsite"
	got := Evaluate(observe([]Schedule{s}, []Backup{freshDaily()}, availableDefaultBSL()),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionLocationUnavailable)
	if f := got[0]; f.Severity != store.AssuranceSeverityCritical || f.Detail.StorageLocation != "offsite" {
		t.Errorf("finding = %+v, want critical naming offsite in the privileged detail", f)
	}

	// A BSL of that name in another namespace is not the schedule's.
	other := BackupStorageLocation{Name: "offsite", Namespace: "elsewhere", Phase: "Available"}
	got = Evaluate(observe([]Schedule{s}, []Backup{freshDaily()}, availableDefaultBSL(), other),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionLocationUnavailable)

	// Present and Available: fine.
	ok := BackupStorageLocation{Name: "offsite", Namespace: veleroNamespace, Phase: "Available"}
	assertConditions(t, Evaluate(observe([]Schedule{s}, []Backup{freshDaily()}, ok), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow))
}

func TestEvaluate_UnavailableBSLPhaseYieldsLocationUnavailable(t *testing.T) {
	for _, phase := range []string{"Unavailable", ""} {
		bsl := availableDefaultBSL()
		bsl.Phase = phase
		bsl.Message = "access denied to bucket"
		got := Evaluate(observe([]Schedule{dailySchedule()}, []Backup{freshDaily()}, bsl),
			[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
		assertConditions(t, got, store.ConditionLocationUnavailable)
		if d := got[0].Detail; d.StorageLocation != "default" || d.BSLMessage != "access denied to bucket" {
			t.Errorf("phase %q: detail = %+v, want the location and message in the privileged fields", phase, d)
		}
	}
}

func TestEvaluate_NoDefaultBSLWithEmptyStorageLocationYieldsLocationUnavailable(t *testing.T) {
	notDefault := availableDefaultBSL()
	notDefault.Default = false
	got := Evaluate(observe([]Schedule{dailySchedule()}, []Backup{freshDaily()}, notDefault),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionLocationUnavailable)

	// An empty location list, read in full: nothing is the default.
	assertConditions(t, Evaluate(observe([]Schedule{dailySchedule()}, []Backup{freshDaily()}),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow), store.ConditionLocationUnavailable)
}

func TestEvaluate_NilLocationsIsUnreadNotEmpty(t *testing.T) {
	// A collector that hands over no location list has not read it. That
	// is unknown for the schedule, never a critical location_unavailable.
	obs := observe([]Schedule{dailySchedule()}, []Backup{freshDaily()})
	obs.Locations = nil
	got := Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionCollectionUnknown)
	if got[0].Subject.UID != "uid-daily" {
		t.Errorf("subject = %+v, want the schedule bound to its UID", got[0].Subject)
	}
}

func TestEvaluate_UntimestampedFailureStillOrdersAsNewest(t *testing.T) {
	// A PartiallyFailed run Velero recorded no times for is newer (by
	// creation time, as the schedules page orders it) than a recent
	// success. Under the default policy it is a failure, which needs no
	// timestamp: it opens partially_failed rather than vanishing.
	partial := Backup{Name: "daily-2", Namespace: veleroNamespace, Phase: "PartiallyFailed", ScheduleName: "daily", created: at("2026-09-10T06:00:00Z")}
	runs := []Backup{dailyRun("daily-1", "Completed", "2026-09-10T03:00:00Z", "2026-09-10T03:05:00Z"), partial}
	got := Evaluate(dailyObs(runs...), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionPartiallyFailed)
	if got[0].Detail.LastOutcome != OutcomePartial {
		t.Errorf("lastOutcome = %q, want partial", got[0].Detail.LastOutcome)
	}

	// Counted as a success, the same run has no time to reset the clock
	// at, so it is skipped and the timestamped success stands.
	p := dailyPolicy()
	p.TreatPartialAs = store.AssuranceTreatPartialAsSuccess
	assertConditions(t, Evaluate(dailyObs(runs...), []store.BackupAssurancePolicy{p}, evalNow))
}

func TestEvaluate_FutureTimestampIsNotFreshness(t *testing.T) {
	// A success stamped a year ahead would hold off overdue until the clock
	// caught up. It is not evidence; the old success decides.
	future := dailyRun("daily-2", "Completed", "2027-09-10T03:00:00Z", "2027-09-10T03:05:00Z")
	got := Evaluate(dailyObs(dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z"), future),
		[]store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "lastSuccessAt", got[0].Detail.LastSuccessAt, "2026-09-08T03:05:00Z")

	// Ordinary skew (a completion two minutes ahead of now) still counts.
	skewed := dailyRun("daily-2", "Completed", "2026-09-10T11:58:00Z", "2026-09-10T12:02:00Z")
	assertConditions(t, Evaluate(dailyObs(skewed), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow))
}

func TestEvaluate_AbsentScheduleProducesNoFindings(t *testing.T) {
	// The policy's schedule is not in a complete schedule list: the subject
	// is absent, and the reconciler resolves its exceptions as
	// subject_absent. Evaluate must not invent never_run or overdue for it.
	obs := observe(nil, []Backup{dailyRun("daily-1", "Failed", "2026-09-01T03:00:00Z", "")}, availableDefaultBSL())
	if got := Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow); got != nil {
		t.Errorf("got %v, want no findings for an absent schedule", conditionsOf(got))
	}
}

func TestEvaluate_RepeatedPolicyYieldsOneFindingPerIdentity(t *testing.T) {
	// The exception store admits one open row per identity, so a policy
	// listed twice (a caller bug) must not double every finding.
	once := Evaluate(pausedObs(), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	twice := Evaluate(pausedObs(), []store.BackupAssurancePolicy{dailyPolicy(), dailyPolicy()}, evalNow)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("repeated policy: got %v, want %v", conditionsOf(twice), conditionsOf(once))
	}
}

func TestEvaluate_RecreatedScheduleClockStartsAtItsCreation(t *testing.T) {
	// The schedule was deleted and recreated yesterday at 12:00; the old
	// incarnation's last success (same label) is a week old. No run of the
	// new schedule was expected before today's 03:00, which is inside
	// grace at 03:30: not overdue yet, though the old success is stale.
	s := dailySchedule()
	s.UID, s.created = "uid-new", at("2026-09-09T12:00:00Z")
	obs := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-03T03:00:00Z", "2026-09-03T03:05:00Z")}, availableDefaultBSL())
	assertConditions(t, Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, at("2026-09-10T03:30:00Z")))
	got := Evaluate(obs, []store.BackupAssurancePolicy{dailyPolicy()}, at("2026-09-10T04:30:00Z"))
	assertConditions(t, got, store.ConditionOverdue)
	if got[0].Subject.UID != "uid-new" {
		t.Errorf("subject UID = %q, want the new incarnation's", got[0].Subject.UID)
	}
}

// ---- Evaluate: namespace and cluster scope ----

func TestEvaluate_NamespaceScopeUsesBackupsCoveringTheNamespace(t *testing.T) {
	p := policy(1, store.ScopeNamespace, "app", "")
	backup := func(name, done string, incl, excl []string) Backup {
		return Backup{Name: name, Namespace: veleroNamespace, Phase: "Completed", IncludedNamespaces: incl,
			ExcludedNamespaces: excl, CompletionTime: ptr(at(done))}
	}
	cases := []struct {
		name    string
		backups []Backup
		want    []store.AssuranceCondition
	}{
		{"explicit include", []Backup{backup("b", "2026-09-10T03:00:00Z", []string{"app"}, nil)}, nil},
		{"empty include is every namespace", []Backup{backup("b", "2026-09-10T03:00:00Z", nil, nil)}, nil},
		{"star include", []Backup{backup("b", "2026-09-10T03:00:00Z", []string{"*"}, nil)}, nil},
		{"exclusion wins", []Backup{backup("b", "2026-09-10T03:00:00Z", nil, []string{"app"})}, []store.AssuranceCondition{store.ConditionOverdue}},
		{"other namespace only", []Backup{backup("b", "2026-09-10T03:00:00Z", []string{"web"}, nil)}, []store.AssuranceCondition{store.ConditionOverdue}},
		{"stale", []Backup{backup("b", "2026-09-08T03:00:00Z", []string{"app"}, nil)}, []store.AssuranceCondition{store.ConditionOverdue}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(observe(nil, tc.backups), []store.BackupAssurancePolicy{p}, evalNow)
			assertConditions(t, got, tc.want...)
			for _, f := range got {
				if f.Subject != (Subject{Kind: store.ScopeNamespace, Namespace: "app"}) || f.Detail.ExpectedRunKnown {
					t.Errorf("finding = %+v, want the namespace subject with no expected run", f)
				}
			}
		})
	}
}

func TestEvaluate_ClusterScopeOnlyCountsWholeClusterBackups(t *testing.T) {
	p := policy(2, store.ScopeCluster, "", "")
	partialScope := Backup{Name: "b", Namespace: veleroNamespace, Phase: "Completed", IncludedNamespaces: []string{"app"}, CompletionTime: ptr(at("2026-09-10T03:00:00Z"))}
	got := Evaluate(observe(nil, []Backup{partialScope}), []store.BackupAssurancePolicy{p}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	if got[0].Subject != (Subject{Kind: store.ScopeCluster}) {
		t.Errorf("subject = %+v, want the cluster", got[0].Subject)
	}

	whole := partialScope
	whole.IncludedNamespaces = nil
	assertConditions(t, Evaluate(observe(nil, []Backup{whole}), []store.BackupAssurancePolicy{p}, evalNow))
}

// ---- Evaluate: policy semantics, determinism, honesty ----

func TestEvaluate_DisabledPolicyProducesNoFindings(t *testing.T) {
	stale := dailyObs(dailyRun("daily-1", "Failed", "2026-09-01T03:00:00Z", ""))
	disabled := dailyPolicy()
	disabled.Enabled = false
	otherCluster := dailyPolicy()
	otherCluster.ClusterID = "remote-1"

	for name, policies := range map[string][]store.BackupAssurancePolicy{
		"disabled":      {disabled},
		"other cluster": {otherCluster},
		"none":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			if got := Evaluate(stale, policies, evalNow); got != nil {
				t.Errorf("got %v, want nothing", conditionsOf(got))
			}
			failed := stale
			failed.Collection = CollectionFailed
			if got := Evaluate(failed, policies, evalNow); got != nil {
				t.Errorf("failed collection: got %v, want nothing (no policy, no evaluation)", conditionsOf(got))
			}
		})
	}
}

func richObservation() (Observation, []store.BackupAssurancePolicy) {
	paused := dailySchedule()
	paused.Name, paused.UID, paused.Paused, paused.StorageLocation = "paused", "uid-paused", true, "gone"
	never := dailySchedule()
	never.Name, never.UID, never.LastBackup = "never", "uid-never", nil
	obs := observe([]Schedule{dailySchedule(), paused, never}, []Backup{
		dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z"),
		dailyRun("daily-2", "Failed", "2026-09-10T03:00:00Z", ""),
		{Name: "app-1", Namespace: veleroNamespace, Phase: "PartiallyFailed", IncludedNamespaces: []string{"app"}, CompletionTime: ptr(at("2026-09-09T00:00:00Z"))},
	}, availableDefaultBSL())
	return obs, []store.BackupAssurancePolicy{
		dailyPolicy(),
		policy(1, store.ScopeSchedule, veleroNamespace, "paused"),
		policy(2, store.ScopeSchedule, veleroNamespace, "never"),
		policy(3, store.ScopeNamespace, "app", ""),
	}
}

func TestEvaluate_IsDeterministicAcrossRepeatedCalls(t *testing.T) {
	obs, policies := richObservation()
	first := Evaluate(obs, policies, evalNow)
	if len(first) < 5 {
		t.Fatalf("fixture produced only %v; it must exercise several conditions", conditionsOf(first))
	}
	reversed := slices.Clone(policies)
	slices.Reverse(reversed)
	reorderedObs := obs
	reorderedObs.Backups = slices.Clone(obs.Backups)
	slices.Reverse(reorderedObs.Backups)
	for i := range 20 {
		ps, o := policies, obs
		if i%2 == 1 {
			ps, o = reversed, reorderedObs
		}
		if got := Evaluate(o, ps, evalNow); !reflect.DeepEqual(got, first) {
			t.Fatalf("call %d differs:\n got %+v\nwant %+v", i, got, first)
		}
	}
	if !slices.IsSortedFunc(first, func(a, b Finding) int {
		return strings.Compare(a.Subject.Name+"/"+string(a.Condition), b.Subject.Name+"/"+string(b.Condition))
	}) {
		t.Errorf("findings are not in identity order: %v", conditionsOf(first))
	}
}

func TestEvaluate_NeverInterpolatesControllerMessagesOutsidePrivilegedDetail(t *testing.T) {
	const canary = "s3://secret-bucket/arn:aws:iam::123456789012:role/velero"
	obs, policies := richObservation()
	bsl := availableDefaultBSL()
	bsl.Phase, bsl.Message, bsl.Bucket = "Unavailable", canary, canary
	obs.Locations.BackupStorageLocations = []BackupStorageLocation{bsl}

	got := Evaluate(obs, policies, evalNow)
	sawPrivileged := false
	for _, f := range got {
		if f.Detail.BSLMessage == canary {
			sawPrivileged = true
		}
		public := f
		public.Detail.StorageLocation, public.Detail.BSLMessage, public.Detail.FailureReason = "", "", ""
		b, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "secret-bucket") || strings.Contains(string(b), "123456789012") {
			t.Errorf("controller text escaped the privileged fields: %s", b)
		}
	}
	if !sawPrivileged {
		t.Errorf("the BSL message should be kept, in the privileged bslMessage field")
	}
}

func TestEvaluate_UnknownExpectedRunFallsBackToMaxAgeAndFlagsIt(t *testing.T) {
	s := dailySchedule()
	s.Schedule, s.Phase = "every day at three", "FailedValidation"
	stale := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-08T03:00:00Z", "2026-09-08T03:05:00Z")}, availableDefaultBSL())
	got := Evaluate(stale, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	d := got[0].Detail
	if d.ExpectedRunKnown || d.ExpectedRunAt != nil || d.CronParseError == "" {
		t.Errorf("detail = %+v, want expectedRunKnown false, no expected run, and the parse error", d)
	}

	// Inside max_age + grace there is nothing to report.
	fresh := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-09-09T13:00:00Z", "2026-09-09T13:05:00Z")}, availableDefaultBSL())
	assertConditions(t, Evaluate(fresh, []store.BackupAssurancePolicy{dailyPolicy()}, evalNow))

	// Walk too long to compute ("@every 1s" over the lookback): same
	// fallback, without a parse error.
	s.Schedule = "@every 1s"
	got = Evaluate(observe([]Schedule{s}, stale.Backups, availableDefaultBSL()), []store.BackupAssurancePolicy{dailyPolicy()}, evalNow)
	assertConditions(t, got, store.ConditionOverdue)
	if d := got[0].Detail; d.ExpectedRunKnown || d.CronParseError != "" {
		t.Errorf("detail = %+v, want expectedRunKnown false with no parse error", d)
	}
}

func TestEvaluate_DSTSpringForwardAbsorbedByDefaultGrace(t *testing.T) {
	// 02:30 New York does not exist on 8 March 2026. A controller that
	// still fired at the old 07:30Z instant and fires at 07:30Z on 9 March
	// is an hour behind the expected 06:30Z run. The default one-hour grace
	// absorbs it; with no grace the same evidence would read as overdue.
	s := dailySchedule()
	s.Schedule = "CRON_TZ=America/New_York 30 2 * * *"
	s.created = at("2026-01-01T00:00:00Z")
	obs := observe([]Schedule{s}, []Backup{dailyRun("daily-1", "Completed", "2026-03-08T07:30:00Z", "2026-03-08T07:35:00Z")}, availableDefaultBSL())
	p := dailyPolicy()
	p.MaxAge = store.AssuranceMinMaxAge
	at0730 := at("2026-03-09T07:30:00Z")

	assertConditions(t, Evaluate(obs, []store.BackupAssurancePolicy{p}, at0730))
	p.Grace = 0
	got := Evaluate(obs, []store.BackupAssurancePolicy{p}, at0730)
	assertConditions(t, got, store.ConditionOverdue)
	timeEq(t, "expectedRunAt", got[0].Detail.ExpectedRunAt, "2026-03-09T06:30:00Z")
}

// ---- ExpectedRunsSince ----

func expectRun(t *testing.T, expr, anchor, now, want string) {
	t.Helper()
	last, known := ExpectedRunsSince(expr, at(anchor), at(now), maxExpectedRunSteps)
	if !known {
		t.Fatalf("%q from %s to %s: known = false, want true", expr, anchor, now)
	}
	if want == "" {
		if !last.IsZero() {
			t.Errorf("%q from %s to %s = %s, want no expected run", expr, anchor, now, last)
		}
		return
	}
	if !last.Equal(at(want)) {
		t.Errorf("%q from %s to %s = %s, want %s", expr, anchor, now, last, want)
	}
}

func TestExpectedRunsSince_DailyCron(t *testing.T) {
	expectRun(t, "0 3 * * *", "2026-09-09T03:05:00Z", "2026-09-10T12:00:00Z", "2026-09-10T03:00:00Z")
	expectRun(t, "@daily", "2026-09-09T03:05:00Z", "2026-09-10T12:00:00Z", "2026-09-10T00:00:00Z")
	// A fire time exactly at now counts; one after it does not.
	expectRun(t, "0 3 * * *", "2026-09-09T03:05:00Z", "2026-09-10T03:00:00Z", "2026-09-10T03:00:00Z")
	expectRun(t, "0 3 * * *", "2026-09-10T03:05:00Z", "2026-09-10T12:00:00Z", "")
}

func TestExpectedRunsSince_WeeklyCronIsNotTreatedAsFixedInterval(t *testing.T) {
	// Mondays 03:00 (7 and 14 September 2026). Two days after a success
	// nothing is expected; a daily or "gap between two fires" reading
	// would invent one.
	expectRun(t, "0 3 * * 1", "2026-09-07T03:05:00Z", "2026-09-10T12:00:00Z", "")
	expectRun(t, "0 3 * * 1", "2026-09-07T03:05:00Z", "2026-09-14T04:00:00Z", "2026-09-14T03:00:00Z")
	// Weekdays only: Friday's run is the latest by Sunday.
	expectRun(t, "0 3 * * 1-5", "2026-09-10T03:05:00Z", "2026-09-13T12:00:00Z", "2026-09-11T03:00:00Z")
}

func TestExpectedRunsSince_MonthlyAndLeapDayExpressions(t *testing.T) {
	expectRun(t, "@monthly", "2026-08-15T00:00:00Z", "2026-09-10T00:00:00Z", "2026-09-01T00:00:00Z")
	// 31st: September has none.
	expectRun(t, "0 0 31 * *", "2026-09-01T00:00:00Z", "2026-09-30T23:00:00Z", "")
	expectRun(t, "0 0 31 * *", "2026-10-01T00:00:00Z", "2026-10-31T01:00:00Z", "2026-10-31T00:00:00Z")
	// 29 February only exists in 2028.
	expectRun(t, "0 0 29 2 *", "2028-02-01T00:00:00Z", "2028-03-01T00:00:00Z", "2028-02-29T00:00:00Z")
	expectRun(t, "0 0 29 2 *", "2026-09-01T00:00:00Z", "2026-09-30T00:00:00Z", "")
}

func TestExpectedRunsSince_DSTSpringForwardAbsorbedByDefaultGrace(t *testing.T) {
	// 02:30 New York is skipped on 8 March 2026 (robfig, like Velero's own
	// scheduler, does not fire it). Every expected run on either side is
	// within an hour of the 07:30Z instant a fixed-offset clock would use,
	// which is what the default 3600s grace is sized to absorb.
	const ny = "CRON_TZ=America/New_York 30 2 * * *"
	expectRun(t, ny, "2026-03-07T08:00:00Z", "2026-03-08T23:00:00Z", "")
	expectRun(t, ny, "2026-03-07T08:00:00Z", "2026-03-09T12:00:00Z", "2026-03-09T06:30:00Z")
	expectRun(t, ny, "2026-03-06T08:00:00Z", "2026-03-07T12:00:00Z", "2026-03-07T07:30:00Z")
	if shift := at("2026-03-07T07:30:00Z").Add(24 * time.Hour).Sub(at("2026-03-08T06:30:00Z")); shift > time.Hour {
		t.Errorf("DST shift %v exceeds the default grace", shift)
	}
}

func TestExpectedRunsSince_DSTFallBackRepeatedHourDoesNotDoubleFire(t *testing.T) {
	// 01:30 New York happens twice on 1 November 2026 (05:30Z EDT, then
	// 06:30Z EST). A daily job fires once: after the first pass, nothing
	// more is expected until the next day.
	const ny = "CRON_TZ=America/New_York 30 1 * * *"
	expectRun(t, ny, "2026-11-01T05:31:00Z", "2026-11-01T12:00:00Z", "")
	expectRun(t, ny, "2026-10-31T12:00:00Z", "2026-11-01T12:00:00Z", "2026-11-01T05:30:00Z")
	expectRun(t, ny, "2026-11-01T05:31:00Z", "2026-11-02T12:00:00Z", "2026-11-02T06:30:00Z")
}

func TestExpectedRunsSince_UnparseableExpressionReturnsKnownFalse(t *testing.T) {
	for _, expr := range []string{
		"", "not a cron", "61 * * * *", "* * * *", "0 3 * * * * *",
		"CRON_TZ=Nowhere/Zone 0 3 * * *",
		// robfig v3.0.1 panics on a zone prefix with no space after it;
		// parseCron must reject it first.
		"CRON_TZ=UTC", "TZ=America/New_York",
	} {
		if last, known := ExpectedRunsSince(expr, evalNow.Add(-24*time.Hour), evalNow, maxExpectedRunSteps); known || !last.IsZero() {
			t.Errorf("%q: (%v, %v), want (zero, false)", expr, last, known)
		}
	}
}

func TestExpectedRunsSince_ExceedingMaxStepsReturnsKnownFalse(t *testing.T) {
	anchor := evalNow.Add(-24 * time.Hour)
	// 1440 minutes in a day: inside the default cap, outside a small one.
	if _, known := ExpectedRunsSince("@every 1m", anchor, evalNow, maxExpectedRunSteps); !known {
		t.Errorf("@every 1m over a day should be computable")
	}
	if _, known := ExpectedRunsSince("@every 1m", anchor, evalNow, 100); known {
		t.Errorf("@every 1m over a day with 100 steps should not be computable")
	}
	if _, known := ExpectedRunsSince("@every 1s", evalNow.Add(-expectedRunLookback), evalNow, maxExpectedRunSteps); known {
		t.Errorf("@every 1s over the lookback should not be computable")
	}
	if _, known := ExpectedRunsSince("0 3 * * *", anchor, evalNow, 0); known {
		t.Errorf("zero steps should not be computable")
	}
	// An anchor older than the lookback cap is not computable either.
	if _, known := ExpectedRunsSince("0 3 * * *", evalNow.Add(-expectedRunLookback-time.Minute), evalNow, maxExpectedRunSteps); known {
		t.Errorf("an anchor past the lookback cap should not be computable")
	}
	// An expression that never fires (30 February).
	if _, known := ExpectedRunsSince("0 0 30 2 *", anchor, evalNow, maxExpectedRunSteps); known {
		t.Errorf("30 February should not be computable")
	}
}

func TestExpectedRunsSince_HonoursCronTZPrefix(t *testing.T) {
	// 03:00 Tokyo is 18:00Z the previous day.
	expectRun(t, "CRON_TZ=Asia/Tokyo 0 3 * * *", "2026-09-09T12:00:00Z", "2026-09-10T12:00:00Z", "2026-09-09T18:00:00Z")
	expectRun(t, "TZ=Asia/Tokyo 0 3 * * *", "2026-09-09T12:00:00Z", "2026-09-10T12:00:00Z", "2026-09-09T18:00:00Z")

	// Without a prefix the expression is UTC, whatever zone the arguments
	// (or this process) are in.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	last, known := ExpectedRunsSince("0 3 * * *", at("2026-09-09T12:00:00Z").In(tokyo), at("2026-09-10T12:00:00Z").In(tokyo), maxExpectedRunSteps)
	if !known || !last.Equal(at("2026-09-10T03:00:00Z")) {
		t.Errorf("zoneless expression = (%v, %v), want 03:00Z", last, known)
	}
}

// ---- parsing seams ----

func TestParseBackupAndScheduleCarryUID(t *testing.T) {
	b := scheduledBackupObj("b", "nightly", "Completed", at("2026-09-01T01:00:00Z"))
	b.SetUID(types.UID("uid-b"))
	if got := parseBackup(b).UID; got != "uid-b" {
		t.Errorf("backup UID = %q, want uid-b", got)
	}

	s := scheduleObj("nightly")
	s.SetUID(types.UID("uid-s"))
	s.SetCreationTimestamp(metav1.NewTime(at("2026-08-01T00:00:00Z")))
	got := parseSchedule(s)
	if got.UID != "uid-s" || !got.created.Equal(at("2026-08-01T00:00:00Z")) {
		t.Errorf("schedule UID %q created %v, want uid-s and its creationTimestamp", got.UID, got.created)
	}
	if body, _ := json.Marshal(got); !strings.Contains(string(body), `"uid":"uid-s"`) {
		t.Errorf("uid is not on the wire: %s", body)
	}
}

func TestParseScheduleZoneOnlyExpressionDoesNotPanic(t *testing.T) {
	// robfig/cron v3.0.1 panicked on "CRON_TZ=UTC" in parseSchedule's
	// next-run computation, failing the whole Velero list read.
	for _, expr := range []string{"CRON_TZ=UTC", "TZ=UTC"} {
		u := obj("Schedule", "s", map[string]any{"schedule": expr}, map[string]any{"phase": "Enabled"})
		if got := parseSchedule(u); got.NextRunTime != nil {
			t.Errorf("%q: next run %v, want none", expr, got.NextRunTime)
		}
	}
	if _, err := parseCron("CRON_TZ=UTC 0 3 * * *"); err != nil {
		t.Errorf("a zone prefix followed by a schedule must still parse: %v", err)
	}
}

// The evaluator stays pure by construction: assurance.go may not import
// anything that reaches a clock source of its own, the network, the
// filesystem or a logger.
func TestAssuranceGo_ImportsStayPure(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "assurance.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"errors": true, "slices": true, "strings": true, "time": true,
		"github.com/google/uuid":                          true,
		"github.com/robfig/cron/v3":                       true,
		"github.com/kubecenter/kubecenter/internal/store": true,
	}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !allowed[path] {
			t.Errorf("assurance.go imports %q; the evaluator must stay pure (no context, net/http, log/slog, os)", path)
		}
	}
}
