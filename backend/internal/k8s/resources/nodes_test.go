package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const drainNode = "node-1"

// recordingAudit captures audit entries so a test can assert which cluster a
// write was attributed to.
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

func (a *recordingAudit) snapshot() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

// drainObjects returns a node plus two pods scheduled on it.
func drainObjects() []runtime.Object {
	return []runtime.Object{
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: drainNode}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: drainNode},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-2", Namespace: "default"},
			Spec:       corev1.PodSpec{NodeName: drainNode},
		},
	}
}

// remoteDrainHandler builds a Handler whose local informer clientset holds a
// same-named node and pods, and whose remote client resolves to a separate
// fake seeded with the same shape. Any action on the local fake means the
// drain ran against the wrong cluster.
func remoteDrainHandler(t *testing.T) (*Handler, *fake.Clientset, *fake.Clientset, *recordingAudit) {
	t.Helper()
	h, local := testHandler(t, drainObjects()...)
	local.ClearActions()
	remote := fake.NewSimpleClientset(drainObjects()...)
	h.remoteClient = func(_ context.Context, clusterID string, _ *auth.User) (kubernetes.Interface, error) {
		if clusterID != remoteTestClusterID {
			t.Errorf("remote client resolved for cluster %q, want %q", clusterID, remoteTestClusterID)
		}
		return remote, nil
	}
	rec := &recordingAudit{}
	h.AuditLogger = rec
	return h, local, remote, rec
}

// drainRequest builds a drain request for drainNode targeting clusterID. The
// returned cancel func ends the request context, as net/http does once the
// handler returns.
func drainRequest(clusterID string) (*http.Request, context.CancelFunc) {
	req := requestWithUser("POST", "/api/v1/resources/nodes/"+drainNode+"/drain", `{"ignoreDaemonSets":true}`)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", drainNode)
	ctx, cancel := context.WithCancel(req.Context())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	ctx = middleware.WithClusterID(ctx, clusterID)
	return req.WithContext(ctx), cancel
}

// startDrain fires HandleDrainNode, cancels the request context the way the
// server does after the handler returns, and returns the task id.
func startDrain(t *testing.T, h *Handler, clusterID string) string {
	t.Helper()
	req, cancel := drainRequest(clusterID)
	rr := httptest.NewRecorder()
	h.HandleDrainNode(rr, req)
	cancel()
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	i := strings.Index(body, "task-")
	if i < 0 {
		t.Fatalf("no task id in response: %s", body)
	}
	return body[i : i+len("task-")+32]
}

// waitTerminal polls until the task is complete or failed.
func waitTerminal(t *testing.T, tm *TaskManager, id string) *Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := tm.Get(id)
		if !ok {
			t.Fatalf("task %s disappeared", id)
		}
		if task.Status == TaskStatusComplete || task.Status == TaskStatusFailed {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return nil
}

func countEvictions(cs *fake.Clientset) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			n++
		}
	}
	return n
}

func TestDrain_RemoteCompletesAfterRequestEnds(t *testing.T) {
	h, local, remote, rec := remoteDrainHandler(t)

	id := startDrain(t, h, remoteTestClusterID)
	task := waitTerminal(t, h.TaskManager, id)

	if task.Status != TaskStatusComplete {
		t.Fatalf("drain ended %s (%q), want complete", task.Status, task.Message)
	}
	if got := countEvictions(remote); got != 2 {
		t.Errorf("remote evictions = %d, want 2", got)
	}
	if n := len(local.Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0: %v", n, local.Actions())
	}
	if task.ClusterID != remoteTestClusterID {
		t.Errorf("task cluster = %q, want %q", task.ClusterID, remoteTestClusterID)
	}
	entries := rec.snapshot()
	if len(entries) != 1 || entries[0].ClusterID != remoteTestClusterID {
		t.Errorf("audit entries = %+v, want one entry for %q", entries, remoteTestClusterID)
	}
}

func TestDrain_RemoteClientFailureReturns500WithoutTask(t *testing.T) {
	h, local, _, _ := remoteDrainHandler(t)
	h.remoteClient = func(context.Context, string, *auth.User) (kubernetes.Interface, error) {
		return nil, context.DeadlineExceeded
	}

	req, cancel := drainRequest(remoteTestClusterID)
	defer cancel()
	rr := httptest.NewRecorder()
	h.HandleDrainNode(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if h.TaskManager.HasActiveTask("drain", remoteTestClusterID, drainNode) {
		t.Error("a failed client resolution must not leave an active task")
	}
	if n := len(local.Actions()); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestDrain_PanicMarksTaskFailed(t *testing.T) {
	h, _, remote, _ := remoteDrainHandler(t)
	remote.PrependReactor("patch", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		panic("boom")
	})

	id := startDrain(t, h, remoteTestClusterID)
	task := waitTerminal(t, h.TaskManager, id)

	if task.Status != TaskStatusFailed {
		t.Fatalf("drain ended %s, want failed", task.Status)
	}
	if countEvictions(remote) != 0 {
		t.Error("no pod may be evicted after the cordon panicked")
	}
}

func TestDrain_SameNodeNameOnDifferentClustersDoNotCollide(t *testing.T) {
	h, _, remote, _ := remoteDrainHandler(t)
	// Hold the remote drain at the pod listing so it stays active.
	release := make(chan struct{})
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		<-release
		return false, nil, nil
	})
	defer close(release)

	h.TaskManager.Create("drain", "local", drainNode, "", "admin")
	if h.TaskManager.HasActiveTask("drain", remoteTestClusterID, drainNode) {
		t.Fatal("a local drain must not count as active on the remote cluster")
	}

	startDrain(t, h, remoteTestClusterID)

	req, cancel := drainRequest(remoteTestClusterID)
	defer cancel()
	rr := httptest.NewRecorder()
	h.HandleDrainNode(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("second drain of the same node on the same cluster: got %d, want 409", rr.Code)
	}
}

func TestDrain_ClusterEvictionCancelsRunningDrain(t *testing.T) {
	h, _, remote, _ := remoteDrainHandler(t)
	listed := make(chan struct{})
	release := make(chan struct{})
	remote.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		close(listed)
		<-release
		return false, nil, nil
	})

	id := startDrain(t, h, remoteTestClusterID)
	<-listed
	h.TaskManager.CancelCluster(remoteTestClusterID)
	close(release)
	task := waitTerminal(t, h.TaskManager, id)

	if task.Status != TaskStatusFailed || !strings.Contains(task.Message, "cluster removed") {
		t.Fatalf("drain ended %s (%q), want failed with a cluster-removed message", task.Status, task.Message)
	}
	if got := countEvictions(remote); got != 0 {
		t.Errorf("remote evictions after cancellation = %d, want 0", got)
	}
}

func TestTaskManager_CancelClusterLeavesOtherClustersRunning(t *testing.T) {
	tm := NewTaskManager()
	localID := tm.Create("drain", "local", drainNode, "", "admin")
	remoteID := tm.Create("drain", remoteTestClusterID, drainNode, "", "admin")
	var localCancelled, remoteCancelled bool
	tm.SetCancel(localID, func() { localCancelled = true })
	tm.SetCancel(remoteID, func() { remoteCancelled = true })
	tm.UpdateStatus(localID, TaskStatusRunning, "draining", 10)
	tm.UpdateStatus(remoteID, TaskStatusRunning, "draining", 10)

	tm.CancelCluster(remoteTestClusterID)

	if !remoteCancelled || localCancelled {
		t.Errorf("cancelled local=%v remote=%v, want only remote", localCancelled, remoteCancelled)
	}
	if task, _ := tm.Get(localID); task.Status != TaskStatusRunning {
		t.Errorf("local task status = %s, want running", task.Status)
	}
	// A late status write from the cancelled drain must not overwrite the
	// cluster-removed outcome.
	tm.UpdateStatus(remoteID, TaskStatusFailed, "failed to list pods", 20)
	if task, _ := tm.Get(remoteID); !strings.Contains(task.Message, "cluster removed") {
		t.Errorf("remote task message = %q, want the cluster-removed outcome kept", task.Message)
	}
}
