package limits

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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const (
	remoteCluster = "remote-1"
	// remoteHost is the address a remote transport error names. No response
	// may carry it.
	remoteHost = "10.20.30.40"
	// internalMarker is error text a remote API server returns. No response
	// may carry it.
	internalMarker = "internal-marker-7f3a"
)

// fakeClients is a k8s.ClusterClients over one typed fake clientset per
// cluster id. targetErr makes every resolution fail. limits never uses the
// dynamic client or the schema, so those always fail.
type fakeClients struct {
	clusters  map[string]*kfake.Clientset
	targetErr error
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, _ string, _ []string) (kubernetes.Interface, error) {
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	c, ok := f.clusters[k8s.NormalizedClusterID(id)]
	if !ok {
		return nil, errors.New("no such fake cluster " + id)
	}
	return c, nil
}

func (f *fakeClients) DynamicClientForCluster(context.Context, string, string, []string) (dynamic.Interface, error) {
	return nil, errors.New("limits does not use the dynamic client")
}

func (f *fakeClients) TargetSchemaFor(context.Context, string, string, []string) (*k8s.TargetSchema, error) {
	return nil, errors.New("limits does not use the target schema")
}

// guardedInformers is the local informer source. Any read through it while
// the request targets a remote cluster is a local fallback and fails the
// test; the underlying local data is still returned so a fallback shows up
// as wrong data too.
type guardedInformers struct {
	t     *testing.T
	inner *testInformerSource
	mu    sync.Mutex
	reads int
}

func (g *guardedInformers) read() {
	g.mu.Lock()
	g.reads++
	g.mu.Unlock()
	g.t.Errorf("local informer read on a remote request")
}

func (g *guardedInformers) ResourceQuotas() corev1listers.ResourceQuotaLister {
	g.read()
	return g.inner.ResourceQuotas()
}

func (g *guardedInformers) LimitRanges() corev1listers.LimitRangeLister {
	g.read()
	return g.inner.LimitRanges()
}

// recordingChecker allows everything and records the identity and cluster
// each SAR carries.
type recordingChecker struct {
	mu        sync.Mutex
	usernames []string
	clusters  []string
}

func (c *recordingChecker) CanAccess(_ context.Context, clusterID, username string, _ []string, _, _, _ string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usernames = append(c.usernames, username)
	c.clusters = append(c.clusters, clusterID)
	return true, nil
}

func quota(ns, name, hard, used string) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(hard)},
			Used: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(used)},
		},
	}
}

func limitRange(ns, name string) *corev1.LimitRange {
	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type:    corev1.LimitTypeContainer,
			Default: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		}}},
	}
}

type remoteHarness struct {
	h       *Handler
	clients *fakeClients
	local   *guardedInformers
	checker *recordingChecker
}

// remote returns the remote cluster's fake clientset.
func (hs *remoteHarness) remote() *kfake.Clientset { return hs.clients.clusters[remoteCluster] }

// newRemoteHarness builds a Handler whose local informers hold only
// local-ns and whose remote cluster holds remote-ns (a 90% cpu quota and a
// LimitRange) and lr-only-ns (a LimitRange only).
func newRemoteHarness(t *testing.T) *remoteHarness {
	t.Helper()
	local := &guardedInformers{t: t, inner: newTestInformerSource(
		[]*corev1.ResourceQuota{quota("local-ns", "local-quota", "4", "1")},
		[]*corev1.LimitRange{limitRange("local-ns", "local-lr")},
	)}
	remote := kfake.NewSimpleClientset(
		quota("remote-ns", "remote-quota", "10", "9"),
		limitRange("remote-ns", "remote-lr"),
		limitRange("lr-only-ns", "remote-lr-2"),
	)
	clients := &fakeClients{clusters: map[string]*kfake.Clientset{remoteCluster: remote}}
	checker := &recordingChecker{}
	h := NewHandler(local, checker, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.Clients = clients
	return &remoteHarness{h: h, clients: clients, local: local, checker: checker}
}

var remoteUser = &auth.User{Username: "alice@example.com", KubernetesUsername: "alice", KubernetesGroups: []string{"devs"}}

func doLimits(t *testing.T, clusterID string, h http.HandlerFunc, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/limits", nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithUser(ctx, remoteUser)
	ctx = middleware.WithClusterID(ctx, clusterID)
	rr := httptest.NewRecorder()
	h(rr, req.WithContext(ctx))
	return rr
}

func decodeData[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return resp.Data
}

func decodeErr(t *testing.T, rr *httptest.ResponseRecorder) *api.APIError {
	t.Helper()
	var resp api.Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if resp.Error == nil {
		t.Fatalf("no error in %s", rr.Body.String())
	}
	return resp.Error
}

func countLists(c *kfake.Clientset, resource string) int {
	n := 0
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func unreachable() error {
	return &url.Error{Op: "Get", URL: "https://" + remoteHost + ":6443/api",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
}

func TestRemote_ListNamespacesSummarizesTheRemoteCluster(t *testing.T) {
	hs := newRemoteHarness(t)

	rr := doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeData[[]NamespaceSummary](t, rr)
	byNS := map[string]NamespaceSummary{}
	for _, s := range got {
		byNS[s.Namespace] = s
	}
	if len(got) != 2 {
		t.Fatalf("summaries = %+v, want remote-ns and lr-only-ns only", got)
	}
	rs := byNS["remote-ns"]
	if !rs.HasQuota || !rs.HasLimitRange || rs.QuotaCount != 1 || rs.HighestUtilization != 90 || rs.Status != ThresholdWarning {
		t.Errorf("remote-ns = %+v, want one quota at 90%% (warning) and a LimitRange", rs)
	}
	if lr := byNS["lr-only-ns"]; lr.HasQuota || !lr.HasLimitRange {
		t.Errorf("lr-only-ns = %+v, want a LimitRange and no quota", lr)
	}
	// Sorted by utilization, highest first, as the local path is.
	if got[0].Namespace != "remote-ns" {
		t.Errorf("first row = %s, want remote-ns", got[0].Namespace)
	}
	if hs.local.reads != 0 {
		t.Errorf("local informers read %d times", hs.local.reads)
	}
	for i, c := range hs.checker.clusters {
		if c != remoteCluster {
			t.Errorf("SAR %d targeted cluster %q, want %q", i, c, remoteCluster)
		}
	}
}

func TestRemote_ListNamespacesIsCachedPerClusterAndEvictable(t *testing.T) {
	hs := newRemoteHarness(t)

	for i := 0; i < 2; i++ {
		if rr := doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil); rr.Code != http.StatusOK {
			t.Fatalf("call %d: status %d: %s", i, rr.Code, rr.Body.String())
		}
	}
	if q, l := countLists(hs.remote(), "resourcequotas"), countLists(hs.remote(), "limitranges"); q != 1 || l != 1 {
		t.Fatalf("after two calls: %d quota lists, %d limitrange lists, want 1 each", q, l)
	}

	hs.h.EvictRemoteCache(remoteCluster)
	if rr := doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil); rr.Code != http.StatusOK {
		t.Fatalf("after evict: status %d: %s", rr.Code, rr.Body.String())
	}
	if q, l := countLists(hs.remote(), "resourcequotas"), countLists(hs.remote(), "limitranges"); q != 2 || l != 2 {
		t.Errorf("after evict: %d quota lists, %d limitrange lists, want 2 each", q, l)
	}
}

func TestRemote_ListNamespacesPagesAndServesATruncatedRead(t *testing.T) {
	hs := newRemoteHarness(t)
	calls := 0
	hs.remote().PrependReactor("list", "resourcequotas", func(a k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		if lim := a.(k8stesting.ListActionImpl).GetListOptions().Limit; lim != 500 {
			t.Errorf("page limit = %d, want 500", lim)
		}
		ns := "paged-" + strconv.Itoa(calls)
		return true, &corev1.ResourceQuotaList{
			ListMeta: metav1.ListMeta{Continue: "more"},
			Items:    []corev1.ResourceQuota{*quota(ns, "q", "10", "1")},
		}, nil
	})

	rr := doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if calls != 10 {
		t.Errorf("read %d pages, want the 10-page cap", calls)
	}
	quotaNS := 0
	for _, s := range decodeData[[]NamespaceSummary](t, rr) {
		if strings.HasPrefix(s.Namespace, "paged-") {
			quotaNS++
		}
	}
	if quotaNS != 10 {
		t.Errorf("served %d paged namespaces, want the 10 read before truncation", quotaNS)
	}
}

func TestRemote_GetNamespaceReadsTheRemoteCluster(t *testing.T) {
	hs := newRemoteHarness(t)

	rr := doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeData[NamespaceLimits](t, rr)
	if got.Namespace != "remote-ns" || len(got.Quotas) != 1 || got.Quotas[0].Name != "remote-quota" ||
		len(got.LimitRanges) != 1 || got.LimitRanges[0].Name != "remote-lr" {
		t.Errorf("detail = %+v, want remote-quota and remote-lr", got)
	}
	if len(got.Quotas) == 1 {
		if u := got.Quotas[0].Utilization["cpu"]; u.Percentage != 90 || u.Used != "9" || u.Hard != "10" {
			t.Errorf("cpu utilization = %+v, want 9/10 = 90%%", u)
		}
	}

	// A namespace that exists only locally has nothing on the remote.
	rr = doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "local-ns"})
	if got := decodeData[NamespaceLimits](t, rr); rr.Code != http.StatusOK || len(got.Quotas) != 0 || len(got.LimitRanges) != 0 {
		t.Errorf("local-only namespace: status %d detail %+v, want 200 and empty", rr.Code, got)
	}
	if hs.local.reads != 0 {
		t.Errorf("local informers read %d times", hs.local.reads)
	}
}

func TestRBAC_ChecksUseTheKubernetesIdentity(t *testing.T) {
	for _, cluster := range []string{remoteCluster, "local"} {
		t.Run(cluster, func(t *testing.T) {
			hs := newRemoteHarness(t)
			if cluster == "local" {
				hs.h.Informers = hs.local.inner
			}
			doLimits(t, cluster, hs.h.HandleListNamespaces, nil)
			doLimits(t, cluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"})
			if len(hs.checker.usernames) == 0 {
				t.Fatal("no SAR was made")
			}
			for _, u := range hs.checker.usernames {
				if u != remoteUser.KubernetesUsername {
					t.Errorf("SAR username = %q, want the Kubernetes username %q", u, remoteUser.KubernetesUsername)
				}
			}
		})
	}
}

func TestRemote_ForbiddenListIsAFixed403(t *testing.T) {
	for _, res := range []string{"resourcequotas", "limitranges"} {
		t.Run(res, func(t *testing.T) {
			hs := newRemoteHarness(t)
			hs.remote().PrependReactor("list", res, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(corev1.Resource(res), "", errors.New(internalMarker))
			})

			for name, rr := range map[string]*httptest.ResponseRecorder{
				"list":   doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil),
				"detail": doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"}),
			} {
				if rr.Code != http.StatusForbidden {
					t.Errorf("%s: status %d, want 403: %s", name, rr.Code, rr.Body.String())
					continue
				}
				e := decodeErr(t, rr)
				if !strings.HasPrefix(e.Message, "you do not have permission to list ") || !strings.HasSuffix(e.Message, " on the selected cluster") || e.Detail != "" {
					t.Errorf("%s: error = %+v, want the fixed permission message and no detail", name, e)
				}
				if strings.Contains(rr.Body.String(), internalMarker) {
					t.Errorf("%s: body leaks the remote error: %s", name, rr.Body.String())
				}
			}
			if hs.local.reads != 0 {
				t.Errorf("local informers read %d times", hs.local.reads)
			}
		})
	}
}

func TestRemote_OtherListFailuresDoNotLeak(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.remote().PrependReactor("list", "resourcequotas", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New(internalMarker))
	})

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil),
		"detail": doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"}),
	} {
		if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), internalMarker) {
			t.Errorf("%s: status %d body %s, want 502 without the remote error", name, rr.Code, rr.Body.String())
		}
	}
}

func TestRemote_GetNamespaceNotFoundIs404(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.remote().PrependReactor("list", "resourcequotas", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(corev1.Resource("namespaces"), internalMarker)
	})

	rr := doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "gone"})
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), internalMarker) {
		t.Errorf("status %d body %s, want 404 without the remote error", rr.Code, rr.Body.String())
	}
}

func TestRemote_UnresolvableClusterReturnsTheTargetError(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.clients.targetErr = unreachable()

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil),
		"detail": doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"}),
	} {
		body := rr.Body.String()
		if rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) {
			t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, body)
		}
		if strings.Contains(body, remoteHost) {
			t.Errorf("%s: body leaks the remote address: %s", name, body)
		}
	}
	if hs.local.reads != 0 {
		t.Errorf("local informers read %d times", hs.local.reads)
	}
}

func TestRemote_MissingClientsIsNotAvailable(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.h.Clients = nil

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list":   doLimits(t, remoteCluster, hs.h.HandleListNamespaces, nil),
		"detail": doLimits(t, remoteCluster, hs.h.HandleGetNamespace, map[string]string{"namespace": "remote-ns"}),
	} {
		if rr.Code != http.StatusNotImplemented {
			t.Errorf("%s: status %d, want 501: %s", name, rr.Code, rr.Body.String())
		}
	}
	if hs.local.reads != 0 {
		t.Errorf("local informers read %d times", hs.local.reads)
	}
	rr := doLimits(t, remoteCluster, hs.h.HandleStatus, nil)
	if got := decodeData[map[string]bool](t, rr); got["available"] {
		t.Errorf("status without Clients = %v, want unavailable", got)
	}
}

func TestStatus_RemoteIsAvailableWithClients(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.h.Informers = nil // a remote answer must not depend on local informers

	rr := doLimits(t, remoteCluster, hs.h.HandleStatus, nil)
	if got := decodeData[map[string]bool](t, rr); rr.Code != http.StatusOK || !got["available"] {
		t.Errorf("status %d data %v, want available", rr.Code, got)
	}
	if rr := doLimits(t, "local", hs.h.HandleStatus, nil); decodeData[map[string]bool](t, rr)["available"] {
		t.Errorf("local status with nil informers = %s, want unavailable", rr.Body.String())
	}
}

func TestLocal_ListAndDetailStillReadInformers(t *testing.T) {
	hs := newRemoteHarness(t)
	hs.h.Informers = hs.local.inner

	got := decodeData[[]NamespaceSummary](t, doLimits(t, "local", hs.h.HandleListNamespaces, nil))
	if len(got) != 1 || got[0].Namespace != "local-ns" {
		t.Errorf("local list = %+v, want local-ns only", got)
	}
	detail := decodeData[NamespaceLimits](t, doLimits(t, "local", hs.h.HandleGetNamespace, map[string]string{"namespace": "local-ns"}))
	if len(detail.Quotas) != 1 || detail.Quotas[0].Name != "local-quota" {
		t.Errorf("local detail = %+v, want local-quota", detail)
	}
	if n := len(hs.remote().Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions on a local request", n)
	}
}
