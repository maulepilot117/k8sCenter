package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	version2 "k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/kubecenter/kubecenter/internal/store"
)

// TestClusterStatusValues pins the cluster-health status vocabulary's wire
// values. store.ClusterRecord.Status is a plain string column, and
// cross-package consumers (server.handle_clusters.go,
// server.handle_capabilities.go) compare against these constants rather than
// bare literals — but nothing stops someone from renaming a constant's
// *value* here without noticing the string itself is a cross-package
// contract with the database and with those consumers. This test fails loudly
// if that ever happens, instead of every remote cluster silently reporting
// unreachable.
func TestClusterStatusValues(t *testing.T) {
	cases := []struct {
		name  string
		got   ClusterStatus
		wantS string
	}{
		{"connected", StatusConnected, "connected"},
		{"disconnected", StatusDisconnected, "disconnected"},
		{"blocked", StatusBlocked, "blocked"},
		{"error", StatusError, "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.got) != tc.wantS {
				t.Errorf("ClusterStatus %s = %q, want %q", tc.name, string(tc.got), tc.wantS)
			}
			if tc.got.String() != tc.wantS {
				t.Errorf("ClusterStatus %s .String() = %q, want %q", tc.name, tc.got.String(), tc.wantS)
			}
		})
	}
}

// fakeProbeStore is an in-memory ClusterStatusStore.
type fakeProbeStore struct {
	records map[string]*store.ClusterRecord
	updates []string
}

func (f *fakeProbeStore) List(_ context.Context) ([]store.ClusterRecord, error) {
	out := make([]store.ClusterRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeProbeStore) Get(_ context.Context, id string) (*store.ClusterRecord, error) {
	r, ok := f.records[id]
	if !ok {
		return nil, fmt.Errorf("cluster %s not found", id)
	}
	c := *r
	return &c, nil
}

func (f *fakeProbeStore) UpdateStatus(_ context.Context, id, status, message, k8sVersion string, nodeCount int) error {
	r, ok := f.records[id]
	if !ok {
		return fmt.Errorf("cluster %s not found", id)
	}
	r.Status, r.StatusMessage, r.K8sVersion, r.NodeCount = status, message, k8sVersion, nodeCount
	f.updates = append(f.updates, id)
	return nil
}

func newLocalFake(version string, nodes ...string) *kubefake.Clientset {
	cs := kubefake.NewSimpleClientset()
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version2.Info{GitVersion: version}
	for _, n := range nodes {
		_, _ = cs.CoreV1().Nodes().Create(context.Background(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n}}, metav1.CreateOptions{})
	}
	return cs
}

func newTestProber(fs *fakeProbeStore, local kubernetes.Interface) *ClusterProber {
	p := &ClusterProber{clusterStore: fs, logger: slog.Default()}
	if local != nil {
		p.SetLocalClient(local)
	}
	return p
}

func TestProbeOne_LocalClusterGetsNodeCountAndVersion(t *testing.T) {
	fs := &fakeProbeStore{records: map[string]*store.ClusterRecord{
		"local": {ID: "local", IsLocal: true, Status: "connected"},
	}}
	p := newTestProber(fs, newLocalFake("v1.31.2", "n1", "n2", "n3"))

	rec, err := p.ProbeOne(context.Background(), "local")
	if err != nil {
		t.Fatalf("ProbeOne(local) error: %v", err)
	}
	if rec.NodeCount != 3 || rec.K8sVersion != "v1.31.2" || rec.Status != StatusConnected.String() {
		t.Errorf("got status=%q version=%q nodes=%d, want connected v1.31.2 3", rec.Status, rec.K8sVersion, rec.NodeCount)
	}
}

func TestProbeAll_ProbesLocalCluster(t *testing.T) {
	fs := &fakeProbeStore{records: map[string]*store.ClusterRecord{
		"local": {ID: "local", IsLocal: true},
	}}
	newTestProber(fs, newLocalFake("v1.30.0", "a")).probeAll(context.Background())
	if r := fs.records["local"]; r.NodeCount != 1 || r.K8sVersion != "v1.30.0" {
		t.Errorf("local record not refreshed: %+v", r)
	}
}

func TestProbeOne_LocalClusterWithoutClientDoesNotTouchRemotePath(t *testing.T) {
	fs := &fakeProbeStore{records: map[string]*store.ClusterRecord{
		"local": {ID: "local", IsLocal: true, Status: "connected", NodeCount: 2, K8sVersion: "v1"},
	}}
	p := newTestProber(fs, nil)
	if _, err := p.ProbeOne(context.Background(), "local"); err == nil {
		t.Fatal("expected error when no local client is configured")
	}
	if len(fs.updates) != 0 {
		t.Errorf("status must not be overwritten with remote-path errors, got updates %v", fs.updates)
	}
}

func TestProbeAll_RemoteClusterStillProbedViaRemotePath(t *testing.T) {
	// A remote record with undecryptable credentials must go through the
	// remote path (credential error), proving remote behaviour is unchanged
	// and that the local client is never used for it.
	fs := &fakeProbeStore{records: map[string]*store.ClusterRecord{
		"r1": {ID: "r1", APIServerURL: "https://203.0.113.10:6443", AuthData: []byte("garbage")},
	}}
	local := newLocalFake("v9.9.9", "n1")
	p := newTestProber(fs, local)
	p.encKey = "key"
	p.probeAll(context.Background())
	if r := fs.records["r1"]; r.K8sVersion == "v9.9.9" || r.NodeCount != 0 {
		t.Errorf("remote record picked up local data: %+v", r)
	}
}
