package topology

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/servicemesh"
)

// recordingLister counts every list call, standing in for the local
// informers. A remote topology request must leave the count at zero.
type recordingLister struct {
	fakeLister
	calls atomic.Int64
}

func (l *recordingLister) ListPods(ctx context.Context, ns string) ([]*corev1.Pod, error) {
	l.calls.Add(1)
	return l.fakeLister.ListPods(ctx, ns)
}

func (l *recordingLister) ListServices(ctx context.Context, ns string) ([]*corev1.Service, error) {
	l.calls.Add(1)
	return l.fakeLister.ListServices(ctx, ns)
}

func (l *recordingLister) ListDeployments(ctx context.Context, ns string) ([]*appsv1.Deployment, error) {
	l.calls.Add(1)
	return l.fakeLister.ListDeployments(ctx, ns)
}

func (l *recordingLister) ListReplicaSets(ctx context.Context, ns string) ([]*appsv1.ReplicaSet, error) {
	l.calls.Add(1)
	return l.fakeLister.ListReplicaSets(ctx, ns)
}

func (l *recordingLister) ListStatefulSets(ctx context.Context, ns string) ([]*appsv1.StatefulSet, error) {
	l.calls.Add(1)
	return l.fakeLister.ListStatefulSets(ctx, ns)
}

func (l *recordingLister) ListDaemonSets(ctx context.Context, ns string) ([]*appsv1.DaemonSet, error) {
	l.calls.Add(1)
	return l.fakeLister.ListDaemonSets(ctx, ns)
}

func (l *recordingLister) ListJobs(ctx context.Context, ns string) ([]*batchv1.Job, error) {
	l.calls.Add(1)
	return l.fakeLister.ListJobs(ctx, ns)
}

func (l *recordingLister) ListCronJobs(ctx context.Context, ns string) ([]*batchv1.CronJob, error) {
	l.calls.Add(1)
	return l.fakeLister.ListCronJobs(ctx, ns)
}

func (l *recordingLister) ListIngresses(ctx context.Context, ns string) ([]*networkingv1.Ingress, error) {
	l.calls.Add(1)
	return l.fakeLister.ListIngresses(ctx, ns)
}

func (l *recordingLister) ListConfigMaps(ctx context.Context, ns string) ([]*corev1.ConfigMap, error) {
	l.calls.Add(1)
	return l.fakeLister.ListConfigMaps(ctx, ns)
}

func (l *recordingLister) ListPVCs(ctx context.Context, ns string) ([]*corev1.PersistentVolumeClaim, error) {
	l.calls.Add(1)
	return l.fakeLister.ListPVCs(ctx, ns)
}

func (l *recordingLister) ListHPAs(ctx context.Context, ns string) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	l.calls.Add(1)
	return l.fakeLister.ListHPAs(ctx, ns)
}

// recordingMeshProvider counts overlay reads, so the remote refusal is also
// proven to happen before any overlay provider is consulted.
type recordingMeshProvider struct {
	fakeMeshProvider
	calls atomic.Int64
}

func (p *recordingMeshProvider) Routes(ctx context.Context) ([]servicemesh.TrafficRoute, error) {
	p.calls.Add(1)
	return p.fakeMeshProvider.Routes(ctx)
}

func (p *recordingMeshProvider) MeshDetected(ctx context.Context) bool {
	p.calls.Add(1)
	return p.fakeMeshProvider.MeshDetected(ctx)
}

func newRecordingHandler() (*Handler, *recordingLister, *recordingMeshProvider) {
	lister := &recordingLister{fakeLister: fakeLister{services: []*corev1.Service{svc("foo", "a", "uid-a")}}}
	mesh := &recordingMeshProvider{fakeMeshProvider: fakeMeshProvider{}}
	h := &Handler{
		Builder:       NewBuilder(lister, mesh, slog.Default()),
		AccessChecker: resources.NewAlwaysAllowAccessChecker(),
		Logger:        slog.Default(),
	}
	return h, lister, mesh
}

// TestHandleNamespaceGraph_LocalUnchanged confirms the local path still builds
// the graph from the lister, for both the implicit (no header) and explicit
// local cluster id.
func TestHandleNamespaceGraph_LocalUnchanged(t *testing.T) {
	for _, clusterID := range []string{"", "local"} {
		t.Run("cluster="+clusterID, func(t *testing.T) {
			h, lister, _ := newRecordingHandler()
			w := callTopologyHandlerForCluster(t, h, clusterID, "foo", "")

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			var body struct {
				Data Graph `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if len(body.Data.Nodes) != 1 || body.Data.Nodes[0].Name != "a" {
				t.Errorf("nodes = %+v, want the single local service a", body.Data.Nodes)
			}
			if lister.calls.Load() == 0 {
				t.Error("lister was never read on a local request")
			}
		})
	}
}

// callTopologyHandlerForCluster is callTopologyHandler with a cluster id on
// the context, as middleware.ClusterContext sets it from X-Cluster-ID. An
// empty clusterID leaves the context without one (the implicit local case).
func callTopologyHandlerForCluster(t *testing.T, h *Handler, clusterID, namespace, overlay string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/topology/" + namespace
	if overlay != "" {
		url += "?overlay=" + overlay
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	ctx := auth.ContextWithUser(req.Context(), testUser())
	if clusterID != "" {
		ctx = middleware.WithClusterID(ctx, clusterID)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", namespace)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.HandleNamespaceGraph(w, req)
	return w
}
