package velero

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Defect #5: a schedule's last-backup phase comes from its newest Backup,
// and a backup outcome is classified from Backup phases only.

func TestBackupOutcomeOf(t *testing.T) {
	tests := []struct {
		phase string
		want  BackupOutcome
	}{
		// Success: only a backup that finished cleanly.
		{"Completed", BackupOutcomeSucceeded},
		// Failures, including a partial one.
		{"PartiallyFailed", BackupOutcomeFailed},
		{"Failed", BackupOutcomeFailed},
		{"FailedValidation", BackupOutcomeFailed},
		// Still running. A phase Velero has not set yet is a new backup.
		{"", BackupOutcomeInProgress},
		{"New", BackupOutcomeInProgress},
		{"Queued", BackupOutcomeInProgress},
		{"ReadyToStart", BackupOutcomeInProgress},
		{"InProgress", BackupOutcomeInProgress},
		{"WaitingForPluginOperations", BackupOutcomeInProgress},
		{"WaitingForPluginOperationsPartiallyFailed", BackupOutcomeInProgress},
		{"Finalizing", BackupOutcomeInProgress},
		{"FinalizingPartiallyFailed", BackupOutcomeInProgress},
		// Being deleted says nothing about how the backup ended.
		{"Deleting", BackupOutcomeUnknown},
		// BackupStorageLocation and Schedule phases are not backup outcomes.
		{"Available", BackupOutcomeUnknown},
		{"Unavailable", BackupOutcomeUnknown},
		{"Enabled", BackupOutcomeUnknown},
		// Phases are case-sensitive Velero constants.
		{"completed", BackupOutcomeUnknown},
		{"SomethingNew", BackupOutcomeUnknown},
	}
	for _, tt := range tests {
		if got := BackupOutcomeOf(tt.phase); got != tt.want {
			t.Errorf("BackupOutcomeOf(%q) = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

func TestScheduleLabelValue(t *testing.T) {
	if got := scheduleLabelValue("nightly"); got != "nightly" {
		t.Errorf("short name = %q, want it unchanged", got)
	}
	exact := strings.Repeat("a", 63)
	if got := scheduleLabelValue(exact); got != exact {
		t.Errorf("63-char name = %q, want it unchanged", got)
	}
	// Velero's label.GetValidName: the first 57 characters, then the first
	// six hex characters of the full name's SHA-256.
	long := strings.Repeat("a", 70)
	if got, want := scheduleLabelValue(long), strings.Repeat("a", 57)+"6bd5e5"; got != want {
		t.Errorf("70-char name = %q, want %q", got, want)
	}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func scheduledBackup(name, schedule, phase string, created time.Time) Backup {
	return Backup{Name: name, Namespace: veleroNamespace, Phase: phase, ScheduleName: schedule, created: created}
}

func TestWithLastBackups(t *testing.T) {
	schedules := []Schedule{
		{Name: "nightly", Namespace: veleroNamespace},
		{Name: "weekly", Namespace: veleroNamespace},
		{Name: "idle", Namespace: veleroNamespace},
		{Name: "nightly", Namespace: "elsewhere"},
	}
	backups := []Backup{
		scheduledBackup("nightly-1", "nightly", "Completed", at("2026-09-01T01:00:00Z")),
		scheduledBackup("nightly-3", "nightly", "PartiallyFailed", at("2026-09-03T01:00:00Z")),
		scheduledBackup("nightly-2", "nightly", "Completed", at("2026-09-02T01:00:00Z")),
		// Another schedule's backup is newer still and must not leak in.
		scheduledBackup("weekly-1", "weekly", "InProgress", at("2026-09-04T01:00:00Z")),
		// An ad-hoc backup carries no schedule label.
		scheduledBackup("adhoc", "", "Failed", at("2026-09-05T01:00:00Z")),
	}
	backups[4].ScheduleName = ""

	got := withLastBackups(schedules, backups)

	want := map[string][2]string{
		"velero/nightly":    {"PartiallyFailed", "failed"},
		"velero/weekly":     {"InProgress", "inProgress"},
		"velero/idle":       {"", ""},
		"elsewhere/nightly": {"", ""},
	}
	for _, s := range got {
		w := want[s.Namespace+"/"+s.Name]
		if s.LastBackupPhase != w[0] || string(s.LastBackupOutcome) != w[1] {
			t.Errorf("%s/%s: phase %q outcome %q, want %q %q", s.Namespace, s.Name, s.LastBackupPhase, s.LastBackupOutcome, w[0], w[1])
		}
	}
	if schedules[0].LastBackupPhase != "" {
		t.Errorf("withLastBackups wrote into its input, which is the shared cache")
	}
}

func TestWithLastBackupsOrdersByStartTime(t *testing.T) {
	var zero time.Time
	backups := []Backup{
		{Name: "b-old", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed", StartTime: ptr(at("2026-09-01T00:00:00Z"))},
		{Name: "b-new", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Failed", StartTime: ptr(at("2026-09-02T00:00:00Z"))},
		{Name: "b-none", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed", created: zero},
	}
	got := withLastBackups([]Schedule{{Name: "s", Namespace: veleroNamespace}}, backups)
	if got[0].LastBackupPhase != "Failed" {
		t.Errorf("phase = %q, want Failed from the backup that started last", got[0].LastBackupPhase)
	}

	// Equal start times break on the creation time.
	same := at("2026-09-01T00:00:00Z")
	backups = []Backup{
		{Name: "x", Namespace: veleroNamespace, ScheduleName: "s", Phase: "InProgress", created: at("2026-09-01T00:05:00Z"), StartTime: ptr(same)},
		{Name: "y", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed", created: at("2026-09-01T00:01:00Z"), StartTime: ptr(same)},
	}
	got = withLastBackups([]Schedule{{Name: "s", Namespace: veleroNamespace}}, backups)
	if got[0].LastBackupPhase != "InProgress" {
		t.Errorf("tie: phase = %q, want InProgress from the later creation", got[0].LastBackupPhase)
	}
}

func TestWithLastBackupsIgnoresSyncedCreationTime(t *testing.T) {
	// Velero's backup-sync controller recreates backups from the storage
	// location, so an old run can carry a newer creationTimestamp than the
	// latest run. Its startTimestamp is the original one, and wins.
	backups := []Backup{
		{Name: "s-old", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Failed",
			created: at("2026-09-10T00:00:00Z"), StartTime: ptr(at("2026-09-01T01:00:00Z"))},
		{Name: "s-latest", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed",
			created: at("2026-09-05T01:00:00Z"), StartTime: ptr(at("2026-09-05T01:00:00Z"))},
	}
	got := withLastBackups([]Schedule{{Name: "s", Namespace: veleroNamespace}}, backups)
	if got[0].LastBackupPhase != "Completed" {
		t.Errorf("phase = %q, want Completed from the run that started last, not the one synced last", got[0].LastBackupPhase)
	}
}

func TestWithLastBackupsCountsANotYetStartedRun(t *testing.T) {
	// A run Velero has not started has no startTimestamp; its creation time
	// still places it after every earlier run.
	backups := []Backup{
		{Name: "s-done", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed",
			created: at("2026-09-01T01:00:00Z"), StartTime: ptr(at("2026-09-01T01:00:01Z"))},
		{Name: "s-queued", Namespace: veleroNamespace, ScheduleName: "s", Phase: "New",
			created: at("2026-09-02T01:00:00Z")},
	}
	got := withLastBackups([]Schedule{{Name: "s", Namespace: veleroNamespace}}, backups)
	if got[0].LastBackupPhase != "New" || got[0].LastBackupOutcome != BackupOutcomeInProgress {
		t.Errorf("phase %q outcome %q, want New inProgress from the queued run", got[0].LastBackupPhase, got[0].LastBackupOutcome)
	}
}

func TestWithLastBackupsBreaksFullTieOnName(t *testing.T) {
	// Equal creation and start times: Velero names a schedule's runs
	// <schedule>-<timestamp>, so the higher name is the later run, in
	// whichever order the list returns them.
	same := at("2026-09-01T00:00:00Z")
	older := Backup{Name: "s-20260901000000", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Completed", created: same, StartTime: ptr(same)}
	newer := Backup{Name: "s-20260901000001", Namespace: veleroNamespace, ScheduleName: "s", Phase: "Failed", created: same, StartTime: ptr(same)}
	for _, backups := range [][]Backup{{older, newer}, {newer, older}} {
		got := withLastBackups([]Schedule{{Name: "s", Namespace: veleroNamespace}}, backups)
		if got[0].LastBackupPhase != "Failed" {
			t.Errorf("order %s,%s: phase = %q, want Failed from the higher name", backups[0].Name, backups[1].Name, got[0].LastBackupPhase)
		}
	}
}

func TestWithLastBackupsMatchesVeleroTruncatedLabel(t *testing.T) {
	long := strings.Repeat("n", 70)
	backups := []Backup{scheduledBackup("b", scheduleLabelValue(long), "Completed", at("2026-09-01T00:00:00Z"))}
	got := withLastBackups([]Schedule{{Name: long, Namespace: veleroNamespace}}, backups)
	if got[0].LastBackupPhase != "Completed" {
		t.Errorf("phase = %q, want Completed via Velero's truncated label value", got[0].LastBackupPhase)
	}
}

func TestParseBackupReadsCreationTimestamp(t *testing.T) {
	u := scheduledBackupObj("b", "nightly", "Completed", at("2026-09-01T01:00:00Z"))
	if got := parseBackup(u).created; !got.Equal(at("2026-09-01T01:00:00Z")) {
		t.Errorf("created = %v, want the object's creationTimestamp", got)
	}
}

// scheduledBackupObj is a Backup object labelled as a run of schedule.
func scheduledBackupObj(name, schedule, phase string, created time.Time) *unstructured.Unstructured {
	u := obj("Backup", name, nil, map[string]any{"phase": phase})
	u.SetLabels(map[string]string{"velero.io/schedule-name": schedule})
	u.SetCreationTimestamp(metav1.NewTime(created))
	return u
}

func scheduleObj(name string) *unstructured.Unstructured {
	return obj("Schedule", name, map[string]any{"schedule": "0 1 * * *"}, map[string]any{"phase": "Enabled", "lastBackup": "2026-09-03T01:00:00Z"})
}

// lastBackupFixture is two schedules, one with three runs and one with none,
// plus another schedule's newer run.
func lastBackupFixture() []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		scheduleObj("nightly"),
		scheduleObj("idle"),
		scheduledBackupObj("nightly-1", "nightly", "Completed", at("2026-09-01T01:00:00Z")),
		scheduledBackupObj("nightly-3", "nightly", "Failed", at("2026-09-03T01:00:00Z")),
		scheduledBackupObj("nightly-2", "nightly", "Completed", at("2026-09-02T01:00:00Z")),
		scheduledBackupObj("other-1", "other", "Completed", at("2026-09-04T01:00:00Z")),
	}
}

func assertLastBackups(t *testing.T, list []Schedule) {
	t.Helper()
	got := map[string]Schedule{}
	for _, s := range list {
		got[s.Name] = s
	}
	if s := got["nightly"]; s.LastBackupPhase != "Failed" || s.LastBackupOutcome != BackupOutcomeFailed {
		t.Errorf("nightly: phase %q outcome %q, want Failed failed", s.LastBackupPhase, s.LastBackupOutcome)
	}
	if s := got["nightly"]; s.LastBackup == nil || !s.LastBackup.Equal(at("2026-09-03T01:00:00Z")) {
		t.Errorf("nightly: lastBackup = %v, want Velero's status.lastBackup unchanged", s.LastBackup)
	}
	if s := got["idle"]; s.LastBackupPhase != "" || s.LastBackupOutcome != "" {
		t.Errorf("idle: phase %q outcome %q, want both empty", s.LastBackupPhase, s.LastBackupOutcome)
	}
}

func TestListSchedulesCarriesLastBackupPhase(t *testing.T) {
	for _, cluster := range []string{localCluster, remoteCluster} {
		t.Run(cluster, func(t *testing.T) {
			var hs *harness
			if cluster == localCluster {
				hs = newLocalHarness(t, lastBackupFixture()...)
			} else {
				hs = newHarness(t, true, lastBackupFixture()...)
			}
			rr := do(t, cluster, http.MethodGet, hs.h.HandleListSchedules, nil, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			assertLastBackups(t, decode[[]Schedule](t, rr))
			if strings.Contains(rr.Body.String(), `"lastBackupPhase":""`) {
				t.Errorf("an empty phase must be omitted, as mobile and web already expect: %s", rr.Body.String())
			}
		})
	}
}

func TestGetScheduleCarriesLastBackupPhase(t *testing.T) {
	for _, cluster := range []string{localCluster, remoteCluster} {
		t.Run(cluster, func(t *testing.T) {
			var hs *harness
			if cluster == localCluster {
				hs = newLocalHarness(t, lastBackupFixture()...)
			} else {
				hs = newHarness(t, true, lastBackupFixture()...)
			}
			var list []Schedule
			for _, name := range []string{"nightly", "idle"} {
				rr := do(t, cluster, http.MethodGet, hs.h.HandleGetSchedule, map[string]string{"namespace": veleroNamespace, "name": name}, "")
				if rr.Code != http.StatusOK {
					t.Fatalf("%s: status %d: %s", name, rr.Code, rr.Body.String())
				}
				list = append(list, decode[Schedule](t, rr))
			}
			assertLastBackups(t, list)
		})
	}
}

func TestListSchedulesWithoutBackupAccessOmitsLastBackupPhase(t *testing.T) {
	for _, cluster := range []string{localCluster, remoteCluster} {
		t.Run(cluster, func(t *testing.T) {
			var hs *harness
			if cluster == localCluster {
				hs = newLocalHarness(t, lastBackupFixture()...)
			} else {
				hs = newHarness(t, true, lastBackupFixture()...)
			}
			hs.h.AccessChecker = resources.NewPredicateAccessChecker(func(_, _, resource, _ string) bool {
				return resource != "backups"
			})
			rr := do(t, cluster, http.MethodGet, hs.h.HandleListSchedules, nil, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			for _, s := range decode[[]Schedule](t, rr) {
				if s.LastBackupPhase != "" || s.LastBackupOutcome != "" {
					t.Errorf("%s: phase %q outcome %q leaked to a user who cannot list backups", s.Name, s.LastBackupPhase, s.LastBackupOutcome)
				}
			}
		})
	}
}

func TestListSchedulesWithNamespacedBackupAccessCarriesLastBackupPhase(t *testing.T) {
	// The detail reads a schedule's runs with a namespaced list as the user,
	// so a user who may list backups only in the schedules' namespace sees
	// the phase on the list too.
	for _, cluster := range []string{localCluster, remoteCluster} {
		t.Run(cluster, func(t *testing.T) {
			var hs *harness
			if cluster == localCluster {
				hs = newLocalHarness(t, lastBackupFixture()...)
			} else {
				hs = newHarness(t, true, lastBackupFixture()...)
			}
			hs.h.AccessChecker = resources.NewPredicateAccessChecker(func(_, _, resource, namespace string) bool {
				return resource != "backups" || namespace == veleroNamespace
			})
			rr := do(t, cluster, http.MethodGet, hs.h.HandleListSchedules, nil, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			assertLastBackups(t, decode[[]Schedule](t, rr))
		})
	}
}

func TestCanListBackupsTreatsFailedCheckAsNo(t *testing.T) {
	hs := newLocalHarness(t)
	hs.h.AccessChecker = resources.NewErroringAccessChecker(errors.New("SAR failed"))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	user := &auth.User{KubernetesUsername: "alice"}
	for _, ns := range []string{"", veleroNamespace} {
		if hs.h.canListBackups(r, user, ns) {
			t.Errorf("canListBackups(%q) = true on a failed check, want false", ns)
		}
	}
	backups := []Backup{scheduledBackup("nightly-1", "nightly", "Completed", at("2026-09-01T01:00:00Z"))}
	if got := hs.h.listableBackups(r, user, backups); len(got) != 0 {
		t.Errorf("listableBackups kept %d backups on a failed check, want none", len(got))
	}
}

func TestRemote_FailedBackupListLeavesSchedulePhaseEmpty(t *testing.T) {
	hs := newHarness(t, true, lastBackupFixture()...)
	hs.remoteDyn().PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListSchedules, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	for _, s := range decode[[]Schedule](t, rr) {
		if s.LastBackupPhase != "" {
			t.Errorf("%s: phase %q, want empty when backups could not be read", s.Name, s.LastBackupPhase)
		}
	}
}

func TestGetScheduleServesWhenBackupListFails(t *testing.T) {
	for _, cluster := range []string{localCluster, remoteCluster} {
		t.Run(cluster, func(t *testing.T) {
			var hs *harness
			if cluster == localCluster {
				hs = newLocalHarness(t, lastBackupFixture()...)
			} else {
				hs = newHarness(t, true, lastBackupFixture()...)
			}
			dyn := hs.clients.clusters[cluster].dyn
			dyn.PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("forbidden")
			})
			rr := do(t, cluster, http.MethodGet, hs.h.HandleGetSchedule, map[string]string{"namespace": veleroNamespace, "name": "nightly"}, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			if s := decode[Schedule](t, rr); s.Name != "nightly" || s.LastBackupPhase != "" {
				t.Errorf("schedule = %+v, want nightly with an empty phase", s)
			}
		})
	}
}

func TestTriggerScheduleLabelsBackupLikeVelero(t *testing.T) {
	long := strings.Repeat("s", 70)
	hs := newLocalHarness(t, obj("Schedule", long, map[string]any{"schedule": "0 1 * * *"}, nil))

	rr := do(t, localCluster, http.MethodPost, hs.h.HandleTriggerSchedule, map[string]string{"namespace": veleroNamespace, "name": long}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[Backup](t, rr).ScheduleName; got != scheduleLabelValue(long) {
		t.Errorf("schedule label = %q, want Velero's valid label value %q", got, scheduleLabelValue(long))
	}
}
