package networking

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// agentPod is a running Cilium agent pod on node.
func agentPod(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: name, Labels: map[string]string{"k8s-app": "cilium"}},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// fakeExec is a RemoteExec whose execer answers per pod through run. It
// records each identity it opened an execer for and every exec it ran.
type fakeExec struct {
	run func(ctx context.Context, username, pod string, stdout, stderr io.Writer) error

	mu     sync.Mutex
	execs  []string // "username/pod"
	opened []string
}

type fakeExecer struct {
	f        *fakeExec
	username string
}

func (f *fakeExec) ExecerFor(_ context.Context, _ string, username string, _ []string) (PodExecer, error) {
	f.mu.Lock()
	f.opened = append(f.opened, username)
	f.mu.Unlock()
	return fakeExecer{f: f, username: username}, nil
}

func (e fakeExecer) Exec(ctx context.Context, _, pod string, cmd k8s.ExecCommand, stdout, stderr io.Writer) error {
	if cmd != k8s.ExecCiliumStatusJSON {
		return errors.New("unexpected command")
	}
	e.f.mu.Lock()
	e.f.execs = append(e.f.execs, e.username+"/"+pod)
	e.f.mu.Unlock()
	return e.f.run(ctx, e.username, pod, stdout, stderr)
}

func (f *fakeExec) execCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.execs)
}

// statusFor writes a cilium-dbg status whose single ClusterMesh cluster is
// named for the identity, so a result shows whose exec produced it.
func statusFor(username string, stdout io.Writer) error {
	return json.NewEncoder(stdout).Encode(ciliumAgentStatus{
		ClusterMesh: &agentClusterMesh{Clusters: []agentRemoteCluster{{Name: "seen-by-" + username}}},
	})
}

func newTestRemoteCollector(t *testing.T, exec *fakeExec, pods ...*corev1.Pod) (*remoteAgentCollector, *recordingAudit) {
	t.Helper()
	objs := make([]runtime.Object, 0, len(pods))
	for _, p := range pods {
		objs = append(objs, p)
	}
	rec := &recordingAudit{}
	clients := &fakeNetClients{clusters: map[string]*kfake.Clientset{netRemoteCluster: kfake.NewSimpleClientset(objs...)}}
	c := newRemoteAgentCollector(clients, exec, rec, slog.New(slog.NewTextHandler(io.Discard, nil)), 4)
	c.execTimeout = 100 * time.Millisecond
	return c, rec
}

func TestRemoteAgentCollector_ForbiddenFirstExecShortCircuits(t *testing.T) {
	exec := &fakeExec{run: func(context.Context, string, string, io.Writer, io.Writer) error {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "pods/exec"}, "cilium-a", errors.New("denied"))
	}}
	c, rec := newTestRemoteCollector(t, exec, agentPod("cilium-a", "n1"), agentPod("cilium-b", "n2"), agentPod("cilium-c", "n3"))

	_, err := c.Collect(context.Background(), netRemoteCluster, networkingTestUser, "kube-system")
	if !apierrors.IsForbidden(err) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if n := exec.execCount(); n != 1 {
		t.Errorf("ran %d execs, want 1 (no exec after the first refusal)", n)
	}
	entries := rec.all()
	if len(entries) != 1 || entries[0].Result != audit.ResultFailure || entries[0].ClusterID != netRemoteCluster || entries[0].User != "alice" {
		t.Errorf("audit = %+v, want one failure row for alice on %s", entries, netRemoteCluster)
	}
}

func TestRemoteAgentCollector_PartialTimeouts(t *testing.T) {
	slow := map[string]bool{"cilium-d": true, "cilium-e": true}
	exec := &fakeExec{run: func(ctx context.Context, username, pod string, stdout, _ io.Writer) error {
		if slow[pod] {
			<-ctx.Done()
			return ctx.Err()
		}
		return statusFor(username, stdout)
	}}
	c, rec := newTestRemoteCollector(t, exec,
		agentPod("cilium-a", "n1"), agentPod("cilium-b", "n2"), agentPod("cilium-c", "n3"),
		agentPod("cilium-d", "n4"), agentPod("cilium-e", "n5"))

	res, err := c.Collect(context.Background(), netRemoteCluster, networkingTestUser, "kube-system")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !res.partial || len(res.nodes) != 5 {
		t.Fatalf("partial=%v nodes=%d, want a partial result for 5 nodes", res.partial, len(res.nodes))
	}
	healthy, timedOut := 0, 0
	for _, n := range res.nodes {
		switch {
		case n.status != nil && n.execError == "":
			healthy++
		case n.execError == execErrTimeout && n.status == nil:
			timedOut++
		default:
			t.Errorf("node %s: status=%v execError=%q", n.nodeName, n.status, n.execError)
		}
	}
	if healthy != 3 || timedOut != 2 {
		t.Errorf("healthy=%d timedOut=%d, want 3 and 2", healthy, timedOut)
	}
	entries := rec.all()
	if len(entries) != 1 || !strings.Contains(entries[0].Detail, "5 agent pods, 2 failed") {
		t.Errorf("audit = %+v, want one row naming 5 pods and 2 failures", entries)
	}
}

func TestRemoteAgentCollector_IdentitiesNeverShareResults(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	exec := &fakeExec{run: func(_ context.Context, username, _ string, stdout, _ io.Writer) error {
		// Hold both identities' collections open together. If they share one
		// collection, only one exec arrives; give up waiting rather than hang.
		if started.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		return statusFor(username, stdout)
	}}
	c, _ := newTestRemoteCollector(t, exec, agentPod("cilium-a", "n1"))
	c.execTimeout = 5 * time.Second

	bob := &auth.User{ID: "u2", Username: "bob", KubernetesUsername: "bob"}
	results := make(map[string]*agentCollectionResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, u := range []*auth.User{networkingTestUser, bob} {
		wg.Add(1)
		go func(u *auth.User) {
			defer wg.Done()
			res, err := c.Collect(context.Background(), netRemoteCluster, u, "kube-system")
			if err != nil {
				t.Errorf("Collect(%s): %v", u.Username, err)
				return
			}
			mu.Lock()
			results[u.KubernetesUsername] = res
			mu.Unlock()
		}(u)
	}
	wg.Wait()

	for _, name := range []string{"alice", "bob"} {
		res := results[name]
		if res == nil || len(res.nodes) != 1 || res.nodes[0].status == nil {
			t.Fatalf("%s: got %+v", name, res)
		}
		if got := res.nodes[0].status.ClusterMesh.Clusters[0].Name; got != "seen-by-"+name {
			t.Errorf("%s was served %q, want their own exec result", name, got)
		}
	}
	if n := exec.execCount(); n != 2 {
		t.Errorf("ran %d execs, want one per identity", n)
	}
}

func TestRemoteAgentCollector_RawErrorTextStaysInLogs(t *testing.T) {
	exec := &fakeExec{run: func(_ context.Context, _, _ string, _, stderr io.Writer) error {
		_, _ = io.WriteString(stderr, "stderr-marker-5d2a")
		return errors.New("exec failed: error-marker-8b1c")
	}}
	c, _ := newTestRemoteCollector(t, exec, agentPod("cilium-a", "n1"), agentPod("cilium-b", "n2"))

	res, err := c.Collect(context.Background(), netRemoteCluster, networkingTestUser, "kube-system")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, n := range res.nodes {
		if n.execError != execErrFailed {
			t.Errorf("node %s: execError=%q, want failed", n.nodeName, n.execError)
		}
		if strings.Contains(n.err, "marker") {
			t.Errorf("node %s: err %q carries remote error text", n.nodeName, n.err)
		}
	}
}

func TestRemoteAgentCollector_PanickingExecIsAFailedNode(t *testing.T) {
	exec := &fakeExec{run: func(_ context.Context, username, pod string, stdout, _ io.Writer) error {
		if pod == "cilium-b" {
			panic("malformed remote stream")
		}
		return statusFor(username, stdout)
	}}
	c, _ := newTestRemoteCollector(t, exec, agentPod("cilium-a", "n1"), agentPod("cilium-b", "n2"))

	res, err := c.Collect(context.Background(), netRemoteCluster, networkingTestUser, "kube-system")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	byPod := map[string]agentNodeResult{}
	for _, n := range res.nodes {
		byPod[n.podName] = n
	}
	if b, ok := byPod["cilium-b"]; !ok || b.execError != execErrFailed || b.status != nil || b.nodeName != "n2" {
		t.Errorf("panicking pod: %+v (present %v), want a failed node on n2", b, ok)
	}
	if a, ok := byPod["cilium-a"]; !ok || a.status == nil {
		t.Errorf("healthy pod lost its result: %+v", a)
	}
}

func TestRemoteAgentCollector_PodListForbidden(t *testing.T) {
	exec := &fakeExec{run: func(context.Context, string, string, io.Writer, io.Writer) error { return nil }}
	c, rec := newTestRemoteCollector(t, exec)
	clients := c.clients.(*fakeNetClients)
	clients.clusters[netRemoteCluster].PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("denied"))
	})

	_, err := c.Collect(context.Background(), netRemoteCluster, networkingTestUser, "kube-system")
	if !apierrors.IsForbidden(err) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if n := exec.execCount(); n != 0 {
		t.Errorf("ran %d execs, want none", n)
	}
	if entries := rec.all(); len(entries) != 1 || entries[0].Result != audit.ResultFailure {
		t.Errorf("audit = %+v, want one failure row", entries)
	}
}

func TestStreamsFrom_NilRouterIsNilInterface(t *testing.T) {
	if StreamsFrom(nil) != nil {
		t.Error("StreamsFrom(nil) returned a non-nil interface wrapping a nil router")
	}
}
