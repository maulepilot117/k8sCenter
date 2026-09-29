package velero

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const remoteCluster = "remote-1"

// remoteHost is the address a remote cluster's transport errors name. No
// response may carry it.
const remoteHost = "10.20.30.40"

// fakeCluster is one fake cluster: its discovery and dynamic client.
type fakeCluster struct {
	disc *fakediscovery.FakeDiscovery
	dyn  *dynfake.FakeDynamicClient
}

// fakeClients is a k8s.ClusterClients over one fake cluster per id. A
// target error makes every resolution fail.
type fakeClients struct {
	clusters  map[string]*fakeCluster
	targetErr error
}

func (f *fakeClients) cluster(id string) (*fakeCluster, error) {
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	if _, err := f.cluster(id); err != nil {
		return nil, err
	}
	return kfake.NewSimpleClientset(), nil
}

func (f *fakeClients) DynamicClientForCluster(_ context.Context, id, _ string, _ []string) (dynamic.Interface, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	return c.dyn, nil
}

func (f *fakeClients) TargetSchemaFor(_ context.Context, id, _ string, _ []string) (*k8s.TargetSchema, error) {
	c, err := f.cluster(id)
	if err != nil {
		return nil, err
	}
	var disc discovery.DiscoveryInterface = c.disc
	return &k8s.TargetSchema{ClusterID: id, Discovery: disc, Invalidate: func() {}}, nil
}

var veleroListKinds = map[schema.GroupVersionResource]string{
	BackupGVR:                 "BackupList",
	RestoreGVR:                "RestoreList",
	ScheduleGVR:               "ScheduleList",
	BackupStorageLocationGVR:  "BackupStorageLocationList",
	VolumeSnapshotLocationGVR: "VolumeSnapshotLocationList",
	DeleteBackupRequestGVR:    "DeleteBackupRequestList",
	DownloadRequestGVR:        "DownloadRequestList",
}

var gvrForKind = map[string]schema.GroupVersionResource{
	"Backup":                 BackupGVR,
	"Restore":                RestoreGVR,
	"Schedule":               ScheduleGVR,
	"BackupStorageLocation":  BackupStorageLocationGVR,
	"VolumeSnapshotLocation": VolumeSnapshotLocationGVR,
}

func obj(kind, name string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "velero.io/v1",
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": veleroNamespace},
	}}
	if spec != nil {
		u.Object["spec"] = spec
	}
	if status != nil {
		u.Object["status"] = status
	}
	return u
}

func backup(name string) *unstructured.Unstructured {
	return obj("Backup", name, nil, map[string]any{"phase": "Completed"})
}

func restore(name, backupName, phase string) *unstructured.Unstructured {
	var status map[string]any
	if phase != "" {
		status = map[string]any{"phase": phase}
	}
	return obj("Restore", name, map[string]any{"backupName": backupName}, status)
}

// veleroLists is discovery for a cluster that does or does not serve Velero.
func veleroLists(installed bool) []*metav1.APIResourceList {
	lists := []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}}}}
	if installed {
		l := &metav1.APIResourceList{GroupVersion: "velero.io/v1"}
		for kind, gvr := range gvrForKind {
			l.APIResources = append(l.APIResources, metav1.APIResource{Name: gvr.Resource, Kind: kind, Namespaced: true})
		}
		lists = append(lists, l)
	}
	return lists
}

func newFakeCluster(t *testing.T, installed bool, objs ...*unstructured.Unstructured) *fakeCluster {
	t.Helper()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), veleroListKinds)
	for _, o := range objs {
		if err := dyn.Tracker().Create(gvrForKind[o.GetKind()], o, o.GetNamespace()); err != nil {
			t.Fatalf("seed %s: %v", o.GetName(), err)
		}
	}
	return &fakeCluster{disc: &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: veleroLists(installed)}}, dyn: dyn}
}

type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Log(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) last(t *testing.T) audit.Entry {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 0 {
		t.Fatal("no audit entry recorded")
	}
	return a.entries[len(a.entries)-1]
}

type recordingNotifier struct {
	sent chan notifications.Notification
}

func (n *recordingNotifier) Emit(_ context.Context, note notifications.Notification) {
	select {
	case n.sent <- note:
	default:
	}
}

type harness struct {
	h       *Handler
	clients *fakeClients
	audit   *recordingAudit
	notes   *recordingNotifier
}

// newHarness builds a Handler over a remote cluster serving remoteObjs and
// a local cluster (reachable only through the same ClusterClients) seeded
// with its own backup and an in-progress restore of it, so a local read on
// the remote path shows up in its recorded actions. K8sClient and the local
// Discoverer are nil, so a service-account or local-discovery read on the
// remote path panics.
func newHarness(t *testing.T, installed bool, remoteObjs ...*unstructured.Unstructured) *harness {
	t.Helper()
	clients := &fakeClients{clusters: map[string]*fakeCluster{
		remoteCluster: newFakeCluster(t, installed, remoteObjs...),
		"local":       newFakeCluster(t, true, backup("local-backup"), restore("local-restore", "local-backup", "InProgress")),
	}}
	hs := &harness{
		clients: clients,
		audit:   &recordingAudit{},
		notes:   &recordingNotifier{sent: make(chan notifications.Notification, 8)},
	}
	hs.h = NewHandler(nil, nil, resources.NewAlwaysAllowAccessChecker(), hs.audit, hs.notes,
		clients, k8s.NewPresence(clients), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return hs
}

func (hs *harness) localActions() int { return len(hs.clients.clusters["local"].dyn.Actions()) }

func (hs *harness) remoteDyn() *dynfake.FakeDynamicClient {
	return hs.clients.clusters[remoteCluster].dyn
}

func countVerb(dyn *dynfake.FakeDynamicClient, verb, resource string) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func doCtx(t *testing.T, ctx context.Context, clusterID, method string, h http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, method, "/velero", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx = context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}})
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func do(t *testing.T, clusterID, method string, h http.HandlerFunc, params map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doCtx(t, t.Context(), clusterID, method, h, params, body)
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

func backupNames(backups []Backup) []string {
	var out []string
	for _, b := range backups {
		out = append(out, b.Name)
	}
	return out
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/apis",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

var backupParams = map[string]string{"namespace": veleroNamespace, "name": "remote-backup"}

func TestRemote_BackupListAndDetailReturnRemoteObjects(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListBackups, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rr.Code, rr.Body.String())
	}
	if got := backupNames(decode[[]Backup](t, rr)); len(got) != 1 || got[0] != "remote-backup" {
		t.Errorf("backups = %v, want only remote-backup", got)
	}

	rr = do(t, remoteCluster, http.MethodGet, hs.h.HandleGetBackup, backupParams, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", rr.Code, rr.Body.String())
	}
	if b := decode[Backup](t, rr); b.Name != "remote-backup" {
		t.Errorf("detail = %q, want remote-backup", b.Name)
	}

	rr = do(t, remoteCluster, http.MethodGet, hs.h.HandleGetBackup, map[string]string{"namespace": veleroNamespace, "name": "local-backup"}, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("detail of a local-only backup: status %d, want 404", rr.Code)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_NotInstalledListsAreEmptyAndStatusSaysMissing(t *testing.T) {
	hs := newHarness(t, false)

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListBackups, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[[]Backup](t, rr); len(got) != 0 {
		t.Errorf("backups = %v, want none", got)
	}

	st := decode[VeleroStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if st.Detected || st.Reason != string(k8s.ReasonDiscoveryMissing) {
		t.Errorf("status = %+v, want not detected with discovery_missing", st)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_StatusReportsRemoteInstallAndLocations(t *testing.T) {
	hs := newHarness(t, true, obj("BackupStorageLocation", "default", nil, nil))

	st := decode[VeleroStatus](t, do(t, remoteCluster, http.MethodGet, hs.h.HandleStatus, nil, ""))
	if !st.Detected || st.Reason != "" || st.BSLCount != 1 || st.VSLCount != 0 {
		t.Errorf("status = %+v, want detected with 1 BSL and no reason", st)
	}

	down := newHarness(t, true)
	down.clients.targetErr = unreachable()
	st = decode[VeleroStatus](t, do(t, remoteCluster, http.MethodGet, down.h.HandleStatus, nil, ""))
	if st.Detected || st.Reason != string(k8s.ReasonUnreachable) {
		t.Errorf("unreachable status = %+v, want not detected with unreachable", st)
	}
}

func TestRemote_DeleteBackupRefusedWhileRemoteRestoreInProgress(t *testing.T) {
	for _, phase := range []string{"", "New", "InProgress", "Finalizing", "WaitingForPluginOperationsPartiallyFailed"} {
		t.Run("phase="+phase, func(t *testing.T) {
			hs := newHarness(t, true, backup("remote-backup"), restore("remote-restore", "remote-backup", phase))

			rr := do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
			if rr.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409: %s", rr.Code, rr.Body.String())
			}
			if n := countVerb(hs.remoteDyn(), "create", "deletebackuprequests"); n != 0 {
				t.Errorf("created %d delete requests, want 0", n)
			}
			if n := hs.localActions(); n != 0 {
				t.Errorf("local cluster recorded %d actions, want 0", n)
			}
		})
	}
}

func TestRemote_DeleteBackupRefusedWhenRestoreListFails(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"))
	hs.remoteDyn().PrependReactor("list", "restores", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})

	rr := do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
	if rr.Code < 400 {
		t.Fatalf("status %d, want a refusal: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), remoteHost) {
		t.Errorf("body leaks the remote address: %s", rr.Body.String())
	}
	if n := countVerb(hs.remoteDyn(), "create", "deletebackuprequests"); n != 0 {
		t.Errorf("created %d delete requests, want 0", n)
	}
}

func TestRemote_DeleteBackupWithFinishedRestoreCreatesRemoteRequest(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"), restore("remote-restore", "remote-backup", "Completed"))

	rr := do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.remoteDyn(), "create", "deletebackuprequests"); n != 1 {
		t.Errorf("created %d delete requests on the remote, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want remote cluster success", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_TriggerScheduleCreatesRemoteBackupAndNextListShowsIt(t *testing.T) {
	hs := newHarness(t, true, obj("Schedule", "nightly", map[string]any{"schedule": "0 1 * * *", "template": map[string]any{"ttl": "72h0m0s"}}, nil))
	list := func() []Backup {
		t.Helper()
		rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListBackups, nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("list status %d: %s", rr.Code, rr.Body.String())
		}
		return decode[[]Backup](t, rr)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("backups before trigger = %v, want none", backupNames(got))
	}

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleTriggerSchedule, map[string]string{"namespace": veleroNamespace, "name": "nightly"}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("trigger status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countVerb(hs.remoteDyn(), "create", "backups"); n != 1 {
		t.Errorf("created %d backups on the remote, want 1", n)
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultSuccess {
		t.Errorf("audit = %+v, want remote cluster success", e)
	}
	select {
	case note := <-hs.notes.sent:
		if note.ClusterID != remoteCluster {
			t.Errorf("notification cluster = %q, want %q", note.ClusterID, remoteCluster)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification emitted after the trigger")
	}

	// The write evicted the remote cache: no 30s window where the new
	// backup is missing and invites a second trigger.
	if got := list(); len(got) != 1 || got[0].ScheduleName != "nightly" {
		t.Errorf("backups after trigger = %+v, want the triggered backup", got)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

// processDownloadRequests makes every DownloadRequest the remote returns
// look processed, with url.
func processDownloadRequests(dyn *dynfake.FakeDynamicClient, url string) {
	dyn.PrependReactor("get", "downloadrequests", func(a k8stesting.Action) (bool, runtime.Object, error) {
		name := a.(k8stesting.GetAction).GetName()
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "velero.io/v1", "kind": "DownloadRequest",
			"metadata": map[string]any{"name": name, "namespace": veleroNamespace},
			"status":   map[string]any{"phase": "Processed", "downloadURL": url},
		}}
		return true, u, nil
	})
}

func TestRemote_BackupLogsCreateDownloadRequestOnRemote(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"))
	processDownloadRequests(hs.remoteDyn(), "https://bucket.example/logs")

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleGetBackupLogs, backupParams, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if got := decode[map[string]string](t, rr)["url"]; got != "https://bucket.example/logs" {
		t.Errorf("url = %q", got)
	}
	if n := countVerb(hs.remoteDyn(), "create", "downloadrequests"); n != 1 {
		t.Errorf("created %d download requests on the remote, want 1", n)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_BackupLogsStopPollingWhenRequestEnds(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"))

	// The DownloadRequest is never processed; only the request ending can
	// stop the wait before its 30s limit.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	doCtx(t, ctx, remoteCluster, http.MethodGet, hs.h.HandleGetBackupLogs, backupParams, "")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("handler polled for %v after the request ended", elapsed)
	}
}

func TestRemote_UnreachableFailsReadsAndWritesWithoutTouchingLocal(t *testing.T) {
	hs := newHarness(t, true)
	hs.clients.targetErr = unreachable()

	create := `{"name":"b1","namespace":"velero"}`
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list backups":   do(t, remoteCluster, http.MethodGet, hs.h.HandleListBackups, nil, ""),
		"list locations": do(t, remoteCluster, http.MethodGet, hs.h.HandleListLocations, nil, ""),
		"detail":         do(t, remoteCluster, http.MethodGet, hs.h.HandleGetBackup, backupParams, ""),
		"create":         do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateBackup, nil, create),
		"delete":         do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, ""),
	} {
		if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), `"reason":"unreachable"`) {
			t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), remoteHost) {
			t.Errorf("%s: body leaks the remote address: %s", name, rr.Body.String())
		}
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_WriteFailuresDoNotLeakRemoteErrorText(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"), obj("Schedule", "nightly", map[string]any{"schedule": "0 1 * * *"}, nil))
	for _, verb := range []string{"create", "delete"} {
		hs.remoteDyn().PrependReactor(verb, "*", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, unreachable()
		})
	}

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"create backup":   do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateBackup, nil, `{"name":"b1"}`),
		"create restore":  do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateRestore, nil, `{"name":"r1","backupName":"remote-backup"}`),
		"create schedule": do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateSchedule, nil, `{"name":"s1","schedule":"0 2 * * *"}`),
		"delete backup":   do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteBackup, backupParams, ""),
		"delete schedule": do(t, remoteCluster, http.MethodDelete, hs.h.HandleDeleteSchedule, map[string]string{"namespace": veleroNamespace, "name": "nightly"}, ""),
		"trigger":         do(t, remoteCluster, http.MethodPost, hs.h.HandleTriggerSchedule, map[string]string{"namespace": veleroNamespace, "name": "nightly"}, ""),
	} {
		body := rr.Body.String()
		if rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) {
			t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, body)
		}
		if strings.Contains(body, remoteHost) || strings.Contains(body, "connection refused") {
			t.Errorf("%s: body leaks the remote error: %s", name, body)
		}
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultFailure {
		t.Errorf("audit = %+v, want remote cluster failure", e)
	}
}

func TestRemote_ForbiddenWriteIs403AndAuditedDenied(t *testing.T) {
	hs := newHarness(t, true)
	hs.remoteDyn().PrependReactor("create", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(BackupGVR.GroupResource(), "b1", errors.New("no"))
	})

	rr := do(t, remoteCluster, http.MethodPost, hs.h.HandleCreateBackup, nil, `{"name":"b1"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if e := hs.audit.last(t); e.ClusterID != remoteCluster || e.Result != audit.ResultDenied {
		t.Errorf("audit = %+v, want remote cluster denied", e)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ForbiddenSnapshotLocationListGivesPartialLocations(t *testing.T) {
	hs := newHarness(t, true, obj("BackupStorageLocation", "default", map[string]any{"provider": "aws"}, nil))
	hs.remoteDyn().PrependReactor("list", "volumesnapshotlocations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(VolumeSnapshotLocationGVR.GroupResource(), "", errors.New("no"))
	})

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListLocations, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decode[LocationsResponse](t, rr)
	if len(got.BackupStorageLocations) != 1 || got.BackupStorageLocations[0].Name != "default" {
		t.Errorf("BSLs = %+v, want the remote default", got.BackupStorageLocations)
	}
	want := k8s.SourceCoverage{Source: "volumesnapshotlocations", Status: k8s.CoverageStatusForbidden, ReasonCode: string(k8s.ReasonForbidden)}
	if len(got.Coverage) != 1 || got.Coverage[0] != want {
		t.Errorf("coverage = %+v, want %+v", got.Coverage, want)
	}
}

func TestRemote_FailedBackupListIsAnErrorNotAnEmptyList(t *testing.T) {
	hs := newHarness(t, true, backup("remote-backup"))
	hs.remoteDyn().PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachable()
	})

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListBackups, nil, "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rr.Code, rr.Body.String())
	}
	// The other lists still serve.
	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListSchedules, nil, ""); rr.Code != http.StatusOK {
		t.Errorf("schedules status %d, want 200: %s", rr.Code, rr.Body.String())
	}
}
