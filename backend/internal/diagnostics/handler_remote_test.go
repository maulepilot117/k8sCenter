package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/topology"
)

// rawRemoteText is planted in every failure a fake remote cluster returns. It
// must never reach a response body.
const rawRemoteText = "dial tcp 10.9.8.7:6443 secret-apiserver.internal"

// fakeClients is a k8s.ClusterClients over one typed fake clientset. It
// records every client resolution so a test can assert the identity a read
// impersonated; targetErr makes every resolution fail.
type fakeClients struct {
	cs        *kfake.Clientset
	targetErr error

	mu    sync.Mutex
	calls []clientCall
}

type clientCall struct {
	cluster, username string
	groups            []string
}

func (f *fakeClients) recorded() []clientCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]clientCall(nil), f.calls...)
}

func (f *fakeClients) ClientForCluster(_ context.Context, id, username string, groups []string) (kubernetes.Interface, error) {
	f.mu.Lock()
	f.calls = append(f.calls, clientCall{cluster: id, username: username, groups: append([]string(nil), groups...)})
	f.mu.Unlock()
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	return f.cs, nil
}

func (f *fakeClients) DynamicClientForCluster(context.Context, string, string, []string) (dynamic.Interface, error) {
	return nil, errors.New("diagnostics does not use the dynamic client")
}

func (f *fakeClients) TargetSchemaFor(context.Context, string, string, []string) (*k8s.TargetSchema, error) {
	return nil, errors.New("diagnostics does not use the target schema")
}

// remoteObjects is the remote cluster's team-a namespace: Deployment api
// owning ReplicaSet api-rs owning the crash-looping pod api-1, plus a healthy
// unowned pod remote-ok. None of these names exist on the local lister.
func remoteObjects() []runtime.Object {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "team-a", UID: "uid-dep"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
	}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "api-rs", Namespace: "team-a", UID: "uid-rs",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: "uid-dep"}},
	}}
	crashing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-1", Namespace: "team-a", UID: "uid-api-1",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-rs", UID: "uid-rs"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", RestartCount: 7,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}},
		},
	}
	ok := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-ok", Namespace: "team-a", UID: "uid-ok"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "team-b", UID: "uid-else"}}
	return []runtime.Object{dep, rs, crashing, ok, other}
}

// newRemoteDiagHandler wires a handler whose local sources are a counting
// lister holding the local-only pod "web", and whose Clients resolve the
// remote fake.
func newRemoteDiagHandler(objs ...runtime.Object) (*Handler, *countingLister, *fakeClients) {
	lister := &countingLister{pods: []*corev1.Pod{testPod(true)}}
	h := newDiagHandler(lister, false)
	fc := &fakeClients{cs: kfake.NewSimpleClientset(objs...)}
	h.Clients = fc
	return h, lister, fc
}

type errorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

func decodeError(t *testing.T, body []byte) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body: %v; body: %s", err, body)
	}
	return e
}

func assertNoLocalRead(t *testing.T, lister *countingLister) {
	t.Helper()
	if n := lister.calls.Load(); n != 0 {
		t.Errorf("local lister read %d times on a remote request, want 0", n)
	}
}

func assertImpersonated(t *testing.T, fc *fakeClients) {
	t.Helper()
	calls := fc.recorded()
	if len(calls) == 0 {
		t.Fatal("remote client was never resolved")
	}
	for _, c := range calls {
		if c.cluster != "remote-1" || c.username != "alice" || len(c.groups) != 1 || c.groups[0] != "ops" {
			t.Errorf("client resolved as %+v, want cluster remote-1 as alice [ops]", c)
		}
	}
}

func TestDiagnosticsRemote_SummaryListsRemotePods(t *testing.T) {
	h, lister, fc := newRemoteDiagHandler(remoteObjects()...)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/summary")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data namespaceSummaryResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.Total != 2 {
		t.Errorf("total = %d, want the 2 remote team-a pods", body.Data.Total)
	}
	if len(body.Data.Failing) != 1 || body.Data.Failing[0].Name != "api-1" || body.Data.Failing[0].Reason != "CrashLoopBackOff" {
		t.Errorf("failing = %+v, want only remote pod api-1 CrashLoopBackOff", body.Data.Failing)
	}
	assertNoLocalRead(t, lister)
	assertImpersonated(t, fc)
}

func TestDiagnosticsRemote_SARDeniedReadsNothing(t *testing.T) {
	for name, path := range map[string]string{
		"summary":  "/diagnostics/team-a/summary",
		"resource": "/diagnostics/team-a/Deployment/api",
	} {
		t.Run(name, func(t *testing.T) {
			h, lister, fc := newRemoteDiagHandler(remoteObjects()...)
			h.AccessChecker = resources.NewAlwaysDenyAccessChecker()

			w := callDiag(t, h, "remote-1", path)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
			}
			if n := len(fc.cs.Actions()); n != 0 {
				t.Errorf("remote cluster saw %d actions after a denied SAR, want 0", n)
			}
			assertNoLocalRead(t, lister)
		})
	}
}

func TestDiagnosticsRemote_ResolveFailureIsATargetError(t *testing.T) {
	for name, path := range map[string]string{
		"summary":  "/diagnostics/team-a/summary",
		"resource": "/diagnostics/team-a/Deployment/api",
	} {
		t.Run(name, func(t *testing.T) {
			h, lister, fc := newRemoteDiagHandler()
			fc.targetErr = errors.New(rawRemoteText)

			w := callDiag(t, h, "remote-1", path)

			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "10.9.8.7") || strings.Contains(w.Body.String(), "secret-apiserver") {
				t.Errorf("raw resolution error leaked: %s", w.Body.String())
			}
			if e := decodeError(t, w.Body.Bytes()); e.Error.Reason != string(k8s.ReasonCredentialsInvalid) {
				t.Errorf("reason = %q, want %q", e.Error.Reason, k8s.ReasonCredentialsInvalid)
			}
			assertNoLocalRead(t, lister)
		})
	}
}

func TestDiagnosticsRemote_NilClientsIs500(t *testing.T) {
	h, lister, _ := newRemoteDiagHandler()
	h.Clients = nil
	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/summary")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", w.Code, w.Body.String())
	}
	assertNoLocalRead(t, lister)
}

// endlessPods makes every pod list page carry a continue token, so the
// remote read hits its page cap.
func endlessPods(cs *kfake.Clientset) {
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{
			ListMeta: metav1.ListMeta{Continue: "more"},
			Items:    []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "team-a"}}},
		}, nil
	})
}

func failPods(cs *kfake.Clientset, err error) {
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// The summary has no slot to say it is partial, so a pod list that hit the
// remote read cap is refused rather than undercounted.
func TestDiagnosticsRemote_SummaryTruncatedIs502(t *testing.T) {
	h, lister, fc := newRemoteDiagHandler()
	endlessPods(fc.cs)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/summary")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w.Body.Bytes()); e.Error.Message != summaryTruncatedMessage {
		t.Errorf("message = %q, want %q", e.Error.Message, summaryTruncatedMessage)
	}
	assertNoLocalRead(t, lister)
}

func TestDiagnosticsRemote_SummaryListErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{
			name:    "forbidden",
			err:     apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New(rawRemoteText)),
			status:  http.StatusForbidden,
			message: "you do not have permission to list pods in namespace team-a on the selected cluster",
		},
		{
			name:    "transport",
			err:     errors.New(rawRemoteText),
			status:  http.StatusBadGateway,
			message: "failed to list pods on the selected cluster",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lister, fc := newRemoteDiagHandler()
			failPods(fc.cs, tc.err)

			w := callDiag(t, h, "remote-1", "/diagnostics/team-a/summary")

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tc.status, w.Body.String())
			}
			e := decodeError(t, w.Body.Bytes())
			if e.Error.Message != tc.message || e.Error.Detail != "" {
				t.Errorf("error = %+v, want message %q and no detail", e.Error, tc.message)
			}
			if strings.Contains(w.Body.String(), "10.9.8.7") {
				t.Errorf("raw remote error leaked: %s", w.Body.String())
			}
			assertNoLocalRead(t, lister)
		})
	}
}

func TestDiagnosticsRemote_DetailResolvesRemoteTarget(t *testing.T) {
	h, lister, fc := newRemoteDiagHandler(remoteObjects()...)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/Deployment/api")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Results     []Result    `json:"results"`
			BlastRadius BlastResult `json:"blastRadius"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var crash *Result
	for i := range body.Data.Results {
		if body.Data.Results[i].RuleName == "CrashLoopBackOff" {
			crash = &body.Data.Results[i]
		}
	}
	if crash == nil || crash.Status != "fail" {
		t.Fatalf("CrashLoopBackOff = %+v, want fail from the remote pod", crash)
	}
	linked := false
	for _, l := range crash.Links {
		if l.Kind == "Pod" && l.Name == "api-1" {
			linked = true
		}
	}
	if !linked {
		t.Errorf("CrashLoopBackOff links = %+v, want remote pod api-1", crash.Links)
	}

	affected := map[string]bool{}
	for _, a := range body.Data.BlastRadius.DirectlyAffected {
		affected[a.Kind+"/"+a.Name] = true
	}
	if !affected["ReplicaSet/api-rs"] || !affected["Pod/api-1"] {
		t.Errorf("blast radius directly affected = %+v, want the remote ReplicaSet and pod", body.Data.BlastRadius.DirectlyAffected)
	}
	assertNoLocalRead(t, lister)
	assertImpersonated(t, fc)
}

func TestDiagnosticsRemote_DetailMissingIs404(t *testing.T) {
	h, lister, _ := newRemoteDiagHandler(remoteObjects()...)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/Pod/web")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (web exists only locally); body: %s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w.Body.Bytes()); e.Error.Message != `Pod "web" not found in namespace "team-a"` {
		t.Errorf("message = %q, want the historical not-found text", e.Error.Message)
	}
	assertNoLocalRead(t, lister)
}

// A target kind whose list hit the read cap without containing the target
// cannot be called absent: it is a 502, never a 404.
func TestDiagnosticsRemote_DetailTargetBeyondCapIs502(t *testing.T) {
	h, lister, fc := newRemoteDiagHandler()
	endlessPods(fc.cs)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/Pod/api-1")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w.Body.Bytes()); e.Error.Message != "too many pods in namespace team-a on the selected cluster to find Pod api-1" {
		t.Errorf("message = %q", e.Error.Message)
	}
	assertNoLocalRead(t, lister)
}

// Related pods that hit the read cap are a truncated limitation, not
// "unavailable", and the request still answers.
func TestDiagnosticsRemote_DetailTruncatedRelatedPods(t *testing.T) {
	objs := remoteObjects()
	h, lister, fc := newRemoteDiagHandler(objs...)
	endlessPods(fc.cs)

	w := callDiag(t, h, "remote-1", "/diagnostics/team-a/Deployment/api")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	assertNoLocalRead(t, lister)

	cs := kfake.NewSimpleClientset(objs...)
	endlessPods(cs)
	target, err := Resolve(context.Background(), topology.NewRemoteLister(cs, h.Logger), "team-a", "Deployment", "api", &RelatedRBAC{Pods: true, ReplicaSets: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := Limitation{Kind: limitPods, Reason: ReasonTruncated}
	if len(target.Limitations) != 1 || target.Limitations[0] != want {
		t.Errorf("limitations = %+v, want [%+v]", target.Limitations, want)
	}
}

func TestDiagnosticsLocal_NeverResolvesClients(t *testing.T) {
	for _, clusterID := range []string{"", "local"} {
		for name, path := range diagPaths {
			t.Run(name+"/cluster="+clusterID, func(t *testing.T) {
				h, lister, fc := newRemoteDiagHandler(remoteObjects()...)
				lister.pods = []*corev1.Pod{testPod(false)}

				w := callDiag(t, h, clusterID, path)

				if w.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
				}
				if n := len(fc.recorded()); n != 0 {
					t.Errorf("Clients resolved %d times on a local request, want 0", n)
				}
				if lister.calls.Load() == 0 {
					t.Error("local lister was never read on a local request")
				}
			})
		}
	}
}
