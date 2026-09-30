package velero

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// Local-cluster tests. The local path reads a service-account cache filled
// through baseDyn and the local Discoverer; both point at the harness's
// "local" fake, which also serves the user's impersonated writes.

const localCluster = "local"

// newLocalHarness builds a Handler whose local cluster serves objs and
// reports Velero installed.
func newLocalHarness(t *testing.T, objs ...*unstructured.Unstructured) *harness {
	t.Helper()
	hs := newHarness(t, true)
	local := newFakeCluster(t, true, objs...)
	hs.clients.clusters[localCluster] = local
	hs.h.baseDynOverride = local.dyn
	hs.h.Discoverer = &Discoverer{logger: hs.h.Logger, status: VeleroStatus{Detected: true, LastChecked: time.Now().UTC()}}
	return hs
}

func (hs *harness) localDyn() *dynfake.FakeDynamicClient {
	return hs.clients.clusters[localCluster].dyn
}

func inNamespace(u *unstructured.Unstructured, ns string) *unstructured.Unstructured {
	u.SetNamespace(ns)
	return u
}

func TestLocal_DeleteBackupRefusedWhileRestoreInProgress(t *testing.T) {
	for _, phase := range []string{"", "New", "InProgress", "Finalizing", "FinalizingPartiallyFailed", "WaitingForPluginOperationsPartiallyFailed"} {
		t.Run("phase="+phase, func(t *testing.T) {
			hs := newLocalHarness(t, backup("remote-backup"), restore("r1", "remote-backup", phase))

			rr := do(t, localCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
			if rr.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409: %s", rr.Code, rr.Body.String())
			}
			if n := countVerb(hs.localDyn(), "create", "deletebackuprequests"); n != 0 {
				t.Errorf("created %d delete requests, want 0", n)
			}
		})
	}
}

func TestLocal_DeleteBackupIgnoresUnrelatedAndFinishedRestores(t *testing.T) {
	hs := newLocalHarness(t,
		backup("remote-backup"),
		restore("done", "remote-backup", "Completed"),
		restore("other-backup", "another-backup", "InProgress"),
		inNamespace(restore("other-ns", "remote-backup", "InProgress"), "elsewhere"))

	rr := do(t, localCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.localDyn(), "create", "deletebackuprequests"); n != 1 {
		t.Errorf("created %d delete requests, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != localCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want local success", e)
	}
}

func TestLocal_DeleteBackupRefusedWhenRestoresCannotBeRead(t *testing.T) {
	hs := newLocalHarness(t, backup("remote-backup"))
	hs.localDyn().PrependReactor("list", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver down")
	})

	rr := do(t, localCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
	if rr.Code < 400 {
		t.Fatalf("status %d, want a refusal: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.localDyn(), "create", "deletebackuprequests"); n != 0 {
		t.Errorf("created %d delete requests, want 0", n)
	}
	if e := hs.audit.last(t); e.Action != audit.ActionVeleroBackupDelete || e.Result != audit.ResultFailure {
		t.Errorf("audit = %+v, want a failed backup delete", e)
	}
}

func TestLocal_ListsSortACopyOfTheCache(t *testing.T) {
	hs := newLocalHarness(t)
	older, newer := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hs.h.cachedData = &cachedVeleroData{
		backups:   []Backup{{Name: "old", StartTime: &older}, {Name: "new", StartTime: &newer}},
		restores:  []Restore{{Name: "old", StartTime: &older}, {Name: "new", StartTime: &newer}},
		schedules: []Schedule{{Name: "zeta"}, {Name: "alpha"}},
		locations: &LocationsResponse{},
		fetchedAt: time.Now(),
	}

	if got := backupNames(decode[[]Backup](t, do(t, localCluster, http.MethodGet, hs.h.HandleListBackups, nil, ""))); !slices.Equal(got, []string{"new", "old"}) {
		t.Errorf("backups = %v, want newest first", got)
	}
	do(t, localCluster, http.MethodGet, hs.h.HandleListRestores, nil, "")
	do(t, localCluster, http.MethodGet, hs.h.HandleListSchedules, nil, "")

	d := hs.h.cachedData
	if d.backups[0].Name != "old" || d.restores[0].Name != "old" || d.schedules[0].Name != "zeta" {
		t.Errorf("a list reordered the shared cache: backups %v, restores[0] %q, schedules[0] %q",
			backupNames(d.backups), d.restores[0].Name, d.schedules[0].Name)
	}
}

func TestLocal_DeniedLocationListDoesNotBlankTheCache(t *testing.T) {
	hs := newLocalHarness(t)
	hs.h.cachedData = &cachedVeleroData{
		locations: &LocationsResponse{
			BackupStorageLocations:  []BackupStorageLocation{{Name: "default"}},
			VolumeSnapshotLocations: []VolumeSnapshotLocation{{Name: "default"}},
		},
		fetchedAt: time.Now(),
	}
	hs.h.AccessChecker = resources.NewPredicateAccessChecker(func(_, _, resource, _ string) bool {
		return resource != "backupstoragelocations"
	})

	got := decode[LocationsResponse](t, do(t, localCluster, http.MethodGet, hs.h.HandleListLocations, nil, ""))
	if len(got.BackupStorageLocations) != 0 || len(got.VolumeSnapshotLocations) != 1 {
		t.Errorf("locations = %+v, want only the permitted snapshot location", got)
	}
	if n := len(hs.h.cachedData.locations.BackupStorageLocations); n != 1 {
		t.Errorf("the denied request blanked the shared cache: %d BSLs left, want 1", n)
	}
}

func TestLocal_CreateBackupKeepsLabels(t *testing.T) {
	hs := newLocalHarness(t)

	rr := do(t, localCluster, http.MethodPost, hs.h.HandleCreateBackup, nil, `{"name":"b1","labels":{"team":"payments"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[Backup](t, rr).Labels["team"]; got != "payments" {
		t.Errorf("response label team = %q, want payments", got)
	}
	stored, err := hs.localDyn().Tracker().Get(BackupGVR, veleroNamespace, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.(*unstructured.Unstructured).GetLabels()["team"]; got != "payments" {
		t.Errorf("stored label team = %q, want payments", got)
	}
	if e := hs.audit.last(t); e.ClusterID != localCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want local success", e)
	}
}

func TestLocal_RefusedWriteIsAuditedDeniedWithTheLocalMessage(t *testing.T) {
	hs := newLocalHarness(t)
	hs.localDyn().PrependReactor("create", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(BackupGVR.GroupResource(), "b1", errors.New("no"))
	})

	rr := do(t, localCluster, http.MethodPost, hs.h.HandleCreateBackup, nil, `{"name":"b1"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want the local cluster's historical 500", rr.Code)
	}
	if e := hs.audit.last(t); e.ClusterID != localCluster || e.Result != audit.ResultDenied {
		t.Errorf("audit = %+v, want local denied", e)
	}
}

func TestLocal_FailedAccessCheckStaysADenial(t *testing.T) {
	hs := newLocalHarness(t, backup("remote-backup"))
	hs.h.AccessChecker = resources.NewErroringAccessChecker(errors.New("SAR failed"))

	rr := do(t, localCluster, http.MethodGet, hs.h.HandleListBackups, nil, "")
	if rr.Code != http.StatusOK || len(decode[[]Backup](t, rr)) != 0 {
		t.Errorf("list: status %d body %s, want 200 []", rr.Code, rr.Body.String())
	}
	if rr := do(t, localCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, ""); rr.Code != http.StatusForbidden {
		t.Errorf("delete: status %d, want 403", rr.Code)
	}
}
