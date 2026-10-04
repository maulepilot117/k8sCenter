package changes

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/diagnostics"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Live-object fixtures. Every field a D6 predicate reads is a parameter so a
// table row can flip exactly one of them.
// ---------------------------------------------------------------------------

type deploymentSpec struct {
	name, ns, uid                string
	generation, observedGen      int64
	replicas, updated, available int64
	availableCond                string // "True", "False", "" (absent)
}

func deploymentObj(d deploymentSpec) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name": d.name, "namespace": d.ns, "uid": d.uid, "generation": d.generation,
		},
		"spec": map[string]any{"replicas": d.replicas},
		"status": map[string]any{
			"observedGeneration": d.observedGen,
			"updatedReplicas":    d.updated,
			"availableReplicas":  d.available,
		},
	}}
	if d.availableCond != "" {
		u.Object["status"].(map[string]any)["conditions"] = []any{
			map[string]any{"type": "Progressing", "status": "True"},
			map[string]any{"type": "Available", "status": d.availableCond},
		}
	}
	return u
}

func readyDeployment(name, uid string) deploymentSpec {
	return deploymentSpec{name: name, ns: "prod", uid: uid, generation: 4, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"}
}

type statefulSetSpec struct {
	name, ns, uid            string
	generation, observedGen  int64
	replicas, updated, ready int64
	currentRev, updateRev    string
}

func statefulSetObj(s statefulSetSpec) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata": map[string]any{
			"name": s.name, "namespace": s.ns, "uid": s.uid, "generation": s.generation,
		},
		"spec": map[string]any{"replicas": s.replicas},
		"status": map[string]any{
			"observedGeneration": s.observedGen,
			"updatedReplicas":    s.updated,
			"readyReplicas":      s.ready,
			"currentRevision":    s.currentRev,
			"updateRevision":     s.updateRev,
		},
	}}
}

func readyStatefulSet(name, uid string) statefulSetSpec {
	return statefulSetSpec{name: name, ns: "prod", uid: uid, generation: 2, observedGen: 2,
		replicas: 3, updated: 3, ready: 3, currentRev: "db-7f8", updateRev: "db-7f8"}
}

type daemonSetSpec struct {
	name, ns, uid                        string
	generation, observedGen              int64
	desired, updated, ready, unavailable int64
}

func daemonSetObj(d daemonSetSpec) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "DaemonSet",
		"metadata": map[string]any{
			"name": d.name, "namespace": d.ns, "uid": d.uid, "generation": d.generation,
		},
		"status": map[string]any{
			"observedGeneration":     d.observedGen,
			"desiredNumberScheduled": d.desired,
			"updatedNumberScheduled": d.updated,
			"numberReady":            d.ready,
			"numberUnavailable":      d.unavailable,
		},
	}}
}

func readyDaemonSet(name, uid string) daemonSetSpec {
	return daemonSetSpec{name: name, ns: "kube-system", uid: uid, generation: 1, observedGen: 1,
		desired: 5, updated: 5, ready: 5, unavailable: 0}
}

func configMapObj(name, uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": "prod", "uid": uid},
		"data":     map[string]any{"key": "value"},
	}}
}

// recordedObj is a receipt object for a verifiable (non-failed) apply.
func recordedObj(index int, group, version, resource, kind, ns, name, uid string) store.ReceiptObject {
	return store.ReceiptObject{Index: index, Group: group, Version: version, Resource: resource,
		Kind: kind, Namespace: ns, Name: name, UID: uid, Action: ActionConfigured}
}

func recordedDeployment(index int, name, uid string) store.ReceiptObject {
	return recordedObj(index, "apps", "v1", "deployments", "Deployment", "prod", name, uid)
}

// completedReceipt seeds a terminal, as-yet-unverified receipt into the fake
// store and returns it. completedAgo is how long before fixedNow it finished.
func completedReceipt(fs *fakeStore, state store.ReceiptState, completedAgo time.Duration, objs ...store.ReceiptObject) *store.ChangeReceipt {
	done := fixedNow.Add(-completedAgo)
	started := done.Add(-time.Second)
	r := store.ChangeReceipt{
		ID: uuid.New(), OwnerID: testUser.ID, OwnerUsername: testUser.Username, ClusterID: "local",
		ClusterGeneration: "local", ContentDigest: computeDigest([]byte("raw")), DocumentCount: len(objs),
		State: state, Objects: objs, VerificationState: store.VerifyPending,
		CreatedAt: started, MutationStartedAt: &started, CompletedAt: &done,
	}
	fs.put(r)
	return &r
}

// fakeDyn builds a fake dynamic client over the given live objects and counts
// GETs so a test can prove when no read happened.
func fakeDyn(objs ...k8sruntime.Object) (*dynfake.FakeDynamicClient, *atomic.Int32) {
	dyn := dynfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), objs...)
	var gets atomic.Int32
	dyn.PrependReactor("get", "*", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		gets.Add(1)
		return false, nil, nil
	})
	return dyn, &gets
}

func singleCheck(t *testing.T, res *VerificationResult) CheckResult {
	t.Helper()
	if len(res.Checks) != 1 {
		t.Fatalf("checks = %d, want 1: %+v", len(res.Checks), res.Checks)
	}
	return res.Checks[0]
}

func expectCheck(t *testing.T, c CheckResult, status CheckStatus, reason string) {
	t.Helper()
	if c.Status != status || c.Reason != reason {
		t.Fatalf("check = %s/%s, want %s/%s (%s)", c.Status, c.Reason, status, reason, c.Message)
	}
	if c.CheckID != CheckIDRolloutComplete {
		t.Fatalf("checkId = %q", c.CheckID)
	}
	if c.ObservedAt.IsZero() {
		t.Fatal("observedAt not set")
	}
}

// persistedState reads back what VerifyOnce stored.
func persistedState(t *testing.T, fs *fakeStore, id uuid.UUID) (store.VerificationState, []CheckResult) {
	t.Helper()
	row := fs.row(t, id)
	var checks []CheckResult
	if len(row.Verification) > 0 {
		if err := json.Unmarshal(row.Verification, &checks); err != nil {
			t.Fatalf("persisted verification is not a []CheckResult: %v", err)
		}
	}
	return row.VerificationState, checks
}

// ---------------------------------------------------------------------------
// VerifyOnce
// ---------------------------------------------------------------------------

func TestVerifyOnce_DeploymentReady_Verified(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerified || res.RetryAfterSeconds != 0 {
		t.Fatalf("result = %+v", res)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckPass, ReasonRolloutComplete)
	if c.Source.UID != "uid-web" || c.Source.Resource != "deployments" || c.Source.ClusterID != "local" {
		t.Fatalf("source = %+v", c.Source)
	}
	if c.Evidence["Deployment/web"] != "uid-web" {
		t.Fatalf("evidence = %v", c.Evidence)
	}
	if gets.Load() != 1 {
		t.Fatalf("GETs = %d, want exactly one per object", gets.Load())
	}
	st, checks := persistedState(t, fs, r.ID)
	if st != store.VerifyVerified || len(checks) != 1 || checks[0].Status != CheckPass {
		t.Fatalf("persisted = %s / %+v", st, checks)
	}
	if fs.row(t, r.ID).VerifiedAt == nil {
		t.Fatal("verified_at not stamped on a final verdict")
	}
	// Verified never upgrades the apply state.
	if fs.row(t, r.ID).State != store.ReceiptApplied {
		t.Fatal("apply state changed")
	}
}

func TestVerifyOnce_DeploymentStaleObservedGeneration_Verifying(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	// Replica counts look complete, but the controller has not observed the
	// new generation yet: the counts describe the OLD spec. This is the gap
	// checkReplicaMismatch has today.
	d := readyDeployment("web", "uid-web")
	d.observedGen = d.generation - 1
	dyn, _ := fakeDyn(deploymentObj(d))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerifying || res.RetryAfterSeconds != verifyRetryAfterSeconds {
		t.Fatalf("result = %+v", res)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckWarn, ReasonRolloutInProgress)
	if !strings.Contains(c.Detail, "observedGeneration 3 >= generation 4") {
		t.Fatalf("detail must name the failing predicate: %q", c.Detail)
	}
	st, _ := persistedState(t, fs, r.ID)
	if st != store.VerifyVerifying {
		t.Fatalf("persisted state = %s", st)
	}
	if fs.row(t, r.ID).VerifiedAt != nil {
		t.Fatal("verifying is not final; verified_at must stay unset")
	}
}

func TestVerifyOnce_StatefulSetRevisionMismatch_Verifying(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedObj(0, "apps", "v1", "statefulsets", "StatefulSet", "prod", "db", "uid-db"))
	s := readyStatefulSet("db", "uid-db")
	s.updateRev = "db-9a1"
	dyn, _ := fakeDyn(statefulSetObj(s))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerifying {
		t.Fatalf("state = %s", res.State)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckWarn, ReasonRolloutInProgress)
	if !strings.Contains(c.Detail, "currentRevision db-7f8 == updateRevision db-9a1") {
		t.Fatalf("detail = %q", c.Detail)
	}
}

func TestVerifyOnce_DaemonSetUnavailable_Verifying(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedObj(0, "apps", "v1", "daemonsets", "DaemonSet", "kube-system", "node-agent", "uid-na"))
	d := readyDaemonSet("node-agent", "uid-na")
	d.unavailable = 1
	d.ready = 4
	dyn, _ := fakeDyn(daemonSetObj(d))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerifying {
		t.Fatalf("state = %s", res.State)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckWarn, ReasonRolloutInProgress)
	if !strings.Contains(c.Detail, "numberUnavailable 1 == 0") || !strings.Contains(c.Detail, "numberReady 4 >= desiredNumberScheduled 5") {
		t.Fatalf("detail = %q", c.Detail)
	}
}

func TestVerifyOnce_ConfigMapIsInconclusiveNotPass(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedObj(0, "", "v1", "configmaps", "ConfigMap", "prod", "web-config", "uid-cm"))
	dyn, gets := fakeDyn(configMapObj("web-config", "uid-cm"))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckInconclusive, ReasonKindNotSupported)
	if res.State != store.VerifyInconclusive {
		t.Fatalf("state = %s, want inconclusive (an unsupported kind is never verified)", res.State)
	}
	if res.RetryAfterSeconds != 0 {
		t.Fatal("an unsupported kind cannot become supported by polling")
	}
	if gets.Load() != 0 {
		t.Fatalf("an unsupported kind must not be read: %d GETs", gets.Load())
	}
	if c.Source.Kind != "ConfigMap" || c.Source.UID != "uid-cm" {
		t.Fatalf("source = %+v", c.Source)
	}
}

func TestVerifyOnce_MixedSupportedAndUnsupported_NeverVerified(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedDeployment(0, "web", "uid-web"),
		recordedObj(1, "", "v1", "configmaps", "ConfigMap", "prod", "web-config", "uid-cm"))
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")), configMapObj("web-config", "uid-cm"))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyInconclusive {
		t.Fatalf("one pass plus one unverifiable object must not read as verified: %s", res.State)
	}
	if len(res.Checks) != 2 || res.Checks[0].Status != CheckPass || res.Checks[1].Status != CheckInconclusive {
		t.Fatalf("checks = %+v", res.Checks)
	}
}

func TestVerifyOnce_TargetDeleted_Fails(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	dyn, _ := fakeDyn() // nothing live

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckFail, ReasonNotFound)
	if res.State != store.VerifyFailed || res.RetryAfterSeconds != 0 {
		t.Fatalf("result = %+v", res)
	}
	st, _ := persistedState(t, fs, r.ID)
	if st != store.VerifyFailed {
		t.Fatalf("persisted = %s", st)
	}
	// verification_failed never downgrades the apply state.
	if fs.row(t, r.ID).State != store.ReceiptApplied {
		t.Fatal("apply state changed")
	}
}

func TestVerifyOnce_TargetRecreatedNewUID_Inconclusive(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-old"))
	// A perfectly healthy Deployment with the same name but a different UID.
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-new")))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckInconclusive, ReasonTargetRecreated)
	if res.State != store.VerifyInconclusive || res.RetryAfterSeconds != 0 {
		t.Fatalf("a healthy impostor must not verify the receipt: %+v", res)
	}
	if c.Source.UID != "uid-old" {
		t.Fatalf("source keeps the recorded identity: %+v", c.Source)
	}
	if c.Evidence["Deployment/web"] != "uid-new" {
		t.Fatalf("evidence names the live uid: %v", c.Evidence)
	}
}

func TestVerifyOnce_Forbidden_Inconclusive(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac denied"))
	})

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckInconclusive, ReasonReadForbidden)
	if res.State != store.VerifyInconclusive {
		t.Fatalf("a permission failure must never read as a pass: %s", res.State)
	}
	if res.RetryAfterSeconds != 0 {
		t.Fatal("revoked permission is not retryable")
	}
}

func TestVerifyOnce_TransientReadError_RetriesWithinWindow(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("etcd leader changed")
	})

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	expectCheck(t, singleCheck(t, res), CheckInconclusive, ReasonReadFailed)
	if res.State != store.VerifyVerifying || res.RetryAfterSeconds != verifyRetryAfterSeconds {
		t.Fatalf("a transient read failure inside the window should be retried: %+v", res)
	}
}

func TestVerifyOnce_WindowExpired_Inconclusive(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, verificationWindow+time.Second,
		recordedDeployment(0, "web", "uid-web"),
		recordedObj(1, "apps", "v1", "deployments", "Deployment", "prod", "api", "uid-api"))
	stuck := readyDeployment("web", "uid-web")
	stuck.available, stuck.availableCond = 1, "False"
	dyn, _ := fakeDyn(deploymentObj(stuck), deploymentObj(readyDeployment("api", "uid-api")))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyInconclusive || res.RetryAfterSeconds != 0 {
		t.Fatalf("past the window the verdict is frozen: %+v", res)
	}
	if len(res.Checks) != 2 {
		t.Fatalf("checks = %+v", res.Checks)
	}
	expectCheck(t, res.Checks[0], CheckInconclusive, ReasonWindowExpired)
	if !strings.Contains(res.Checks[0].Detail, "availableReplicas 1 >= replicas 3") {
		t.Fatalf("the last observation must be kept: %q", res.Checks[0].Detail)
	}
	expectCheck(t, res.Checks[1], CheckPass, ReasonRolloutComplete)
	st, _ := persistedState(t, fs, r.ID)
	if st != store.VerifyInconclusive || fs.row(t, r.ID).VerifiedAt == nil {
		t.Fatalf("expiry must persist as a final verdict: %s", st)
	}

	// Frozen: a later poll returns the stored verdict without reading.
	dyn2, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")), deploymentObj(readyDeployment("api", "uid-api")))
	again, err := svc.VerifyOnce(context.Background(), fs.row(t, r.ID), dyn2)
	if err != nil {
		t.Fatal(err)
	}
	if again.State != store.VerifyInconclusive || gets.Load() != 0 || again.Checks[0].Reason != ReasonWindowExpired {
		t.Fatalf("frozen verdict was recomputed: %+v (gets=%d)", again, gets.Load())
	}
}

func TestVerifyOnce_WindowBoundaryIsInclusive(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, verificationWindow, recordedDeployment(0, "web", "uid-web"))
	stuck := readyDeployment("web", "uid-web")
	stuck.updated = 2
	dyn, _ := fakeDyn(deploymentObj(stuck))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerifying {
		t.Fatalf("at exactly the window the check is still open: %s", res.State)
	}
}

func TestVerifyOnce_ControllerOverwroteFields_StillReportsObserved(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	// The user applied replicas: 3; an HPA (or Argo) has since set replicas: 5
	// and the rollout to 5 is still converging. The verifier has no manifest
	// to compare against and does not pretend to: it reports the live
	// postcondition against the live spec.
	d := readyDeployment("web", "uid-web")
	d.generation, d.observedGen = 5, 5
	d.replicas, d.updated, d.available = 5, 5, 4
	dyn, _ := fakeDyn(deploymentObj(d))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	c := singleCheck(t, res)
	expectCheck(t, c, CheckWarn, ReasonRolloutInProgress)
	if !strings.Contains(c.Detail, "availableReplicas 4 >= replicas 5") {
		t.Fatalf("detail must describe what was observed, not what was submitted: %q", c.Detail)
	}
	if c.Evidence["Deployment/web"] != "uid-web" {
		t.Fatalf("evidence = %v", c.Evidence)
	}
	for k, v := range c.Evidence {
		if strings.Contains(v, "replicas") || strings.Contains(k, "spec") {
			t.Fatalf("evidence must hold identities, never content: %v", c.Evidence)
		}
	}
}

func TestVerifyOnce_FailedObjectsAreNotVerified(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	failed := recordedDeployment(1, "api", "")
	failed.Action, failed.Error = ActionFailed, "forbidden"
	r := completedReceipt(fs, store.ReceiptPartial, 10*time.Second, recordedDeployment(0, "web", "uid-web"), failed)
	// Both exist live and are healthy; "api" was NOT applied by us.
	dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")), deploymentObj(readyDeployment("api", "uid-api")))

	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Checks) != 1 || res.Checks[0].Source.Name != "web" {
		t.Fatalf("the failed object must be skipped: %+v", res.Checks)
	}
	if gets.Load() != 1 {
		t.Fatalf("GETs = %d; the failed object must not even be read", gets.Load())
	}
	if res.State != store.VerifyVerified {
		t.Fatalf("state = %s", res.State)
	}
	// Verified does not upgrade partial.
	if fs.row(t, r.ID).State != store.ReceiptPartial {
		t.Fatal("apply state changed")
	}
}

func TestVerifyOnce_NothingVerifiable_Inconclusive(t *testing.T) {
	for _, state := range []store.ReceiptState{store.ReceiptFailed, store.ReceiptUnknown} {
		t.Run(string(state), func(t *testing.T) {
			fs := newFakeStore()
			svc := newTestService(fs)
			failed := recordedDeployment(0, "web", "")
			failed.Action = ActionFailed
			r := completedReceipt(fs, state, 10*time.Second, failed)
			dyn, gets := fakeDyn()

			res, err := svc.VerifyOnce(context.Background(), r, dyn)
			if err != nil {
				t.Fatal(err)
			}
			if res.State != store.VerifyInconclusive || len(res.Checks) != 0 || gets.Load() != 0 {
				t.Fatalf("result = %+v gets=%d", res, gets.Load())
			}
			if res.Checks == nil {
				t.Fatal("checks must be an empty array on the wire, not null")
			}
			st, _ := persistedState(t, fs, r.ID)
			if st != store.VerifyInconclusive {
				t.Fatalf("persisted = %s", st)
			}
		})
	}
}

func TestVerifyOnce_InFlightReceiptIsPendingWithoutReading(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	started := fixedNow.Add(-time.Second)
	r := store.ChangeReceipt{ID: uuid.New(), OwnerID: testUser.ID, ClusterID: "local", ContentDigest: "sha256:x",
		State: store.ReceiptApplying, MutationStartedAt: &started, VerificationState: store.VerifyPending,
		Objects: []store.ReceiptObject{recordedDeployment(0, "web", "uid-web")}}
	fs.put(r)
	dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))

	res, err := svc.VerifyOnce(context.Background(), &r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyPending || gets.Load() != 0 {
		t.Fatalf("an in-flight receipt has nothing to verify yet: %+v gets=%d", res, gets.Load())
	}
	if fs.row(t, r.ID).VerificationState != store.VerifyPending || len(fs.calls) != 0 {
		t.Fatalf("nothing may be persisted for an in-flight receipt: %v", fs.calls)
	}
}

func TestVerifyOnce_FinalVerdictIsFrozen(t *testing.T) {
	t.Run("already final on entry: stored verdict, no read, no write", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
		stored := []CheckResult{{CheckID: CheckIDRolloutComplete, Status: CheckFail, Reason: ReasonNotFound, Source: SourceRef{Kind: "Deployment", Name: "web"}}}
		payload, _ := json.Marshal(stored)
		r.VerificationState, r.Verification = store.VerifyFailed, payload
		fs.put(*r)
		dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web"))) // it came back; the verdict stands

		res, err := svc.VerifyOnce(context.Background(), r, dyn)
		if err != nil {
			t.Fatal(err)
		}
		if res.State != store.VerifyFailed || len(res.Checks) != 1 || res.Checks[0].Reason != ReasonNotFound {
			t.Fatalf("result = %+v", res)
		}
		if gets.Load() != 0 || len(fs.calls) != 0 {
			t.Fatalf("frozen verdict must cause no read and no write: gets=%d calls=%v", gets.Load(), fs.calls)
		}
	})
	t.Run("reconciled under us: SetVerification refuses, stored verdict is returned", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
		stale := *r // what the handler read a moment ago: still pending
		// Reconciliation (or a concurrent poll) finalized the row meanwhile.
		stored := []CheckResult{{CheckID: CheckIDRolloutComplete, Status: CheckInconclusive, Reason: ReasonWindowExpired}}
		payload, _ := json.Marshal(stored)
		r.VerificationState, r.Verification = store.VerifyInconclusive, payload
		fs.put(*r)
		stuck := readyDeployment("web", "uid-web")
		stuck.updated = 1
		dyn, _ := fakeDyn(deploymentObj(stuck)) // would compute "verifying"

		res, err := svc.VerifyOnce(context.Background(), &stale, dyn)
		if err != nil {
			t.Fatalf("a frozen verdict is not an error: %v", err)
		}
		if res.State != store.VerifyInconclusive || res.Checks[0].Reason != ReasonWindowExpired || res.RetryAfterSeconds != 0 {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("store write fails: unavailable", func(t *testing.T) {
		fs := newFakeStore()
		svc := newTestService(fs)
		r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
		fs.failSetVerification = errors.New("write timeout")
		dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))

		_, err := svc.VerifyOnce(context.Background(), r, dyn)
		mustUnavailable(t, err, "verification")
	})
}

func TestVerifyOnce_InvalidInput(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	dyn, _ := fakeDyn()
	if _, err := svc.VerifyOnce(context.Background(), nil, dyn); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil receipt: %v", err)
	}
	r := completedReceipt(fs, store.ReceiptApplied, time.Second)
	if _, err := svc.VerifyOnce(context.Background(), r, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil dyn: %v", err)
	}
	var nilStore *store.ChangeReceiptStore
	if _, err := NewService(nilStore, nil).VerifyOnce(context.Background(), r, dyn); err == nil {
		t.Fatal("no store must fail closed")
	}
}

func TestVerifyOnce_ClusterScopedObjectReadWithoutNamespace(t *testing.T) {
	// A cluster-scoped apps/v1 kind does not exist, but the read path must
	// still route a namespace-less object to the cluster-scoped client. Prove
	// it with a reactor that inspects the action's namespace.
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedObj(0, "apps", "v1", "deployments", "Deployment", "", "global", "uid-g"))
	dyn, _ := fakeDyn()
	var sawNS string
	dyn.PrependReactor("get", "deployments", func(a k8stesting.Action) (bool, k8sruntime.Object, error) {
		sawNS = a.GetNamespace()
		d := readyDeployment("global", "uid-g")
		d.ns = ""
		return true, deploymentObj(d), nil
	})
	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if sawNS != "" || res.State != store.VerifyVerified {
		t.Fatalf("ns=%q state=%s", sawNS, res.State)
	}
}

func TestVerifyOnce_StartsNoBackgroundGoroutine(t *testing.T) {
	fs := newFakeStore()
	svc := newTestService(fs)
	r := completedReceipt(fs, store.ReceiptApplied, 10*time.Second,
		recordedDeployment(0, "web", "uid-web"),
		recordedObj(1, "apps", "v1", "statefulsets", "StatefulSet", "prod", "db", "uid-db"),
		recordedObj(2, "apps", "v1", "daemonsets", "DaemonSet", "kube-system", "agent", "uid-ag"))
	stuck := readyDeployment("web", "uid-web")
	stuck.updated = 1 // leaves the receipt in "verifying", the state a watcher would want to poll
	dyn, _ := fakeDyn(deploymentObj(stuck), statefulSetObj(readyStatefulSet("db", "uid-db")), daemonSetObj(readyDaemonSet("agent", "uid-ag")))

	runtime.GC()
	before := runtime.NumGoroutine()
	res, err := svc.VerifyOnce(context.Background(), r, dyn)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != store.VerifyVerifying {
		t.Fatalf("state = %s", res.State)
	}
	// Give any detached goroutine a chance to show up before we count.
	for i := 0; i < 10; i++ {
		runtime.Gosched()
	}
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("VerifyOnce left %d goroutine(s) running; verification must be stateless polling", after-before)
	}
}

// ---------------------------------------------------------------------------
// CheckRollout: every D6 predicate, both polarities, pure.
// ---------------------------------------------------------------------------

func TestCheckRollout_TableDriven(t *testing.T) {
	src := SourceRef{ClusterID: "local"}
	type row struct {
		name      string
		obj       *unstructured.Unstructured
		status    CheckStatus
		reason    string
		detailHas string
	}
	mut := func(d deploymentSpec, f func(*deploymentSpec)) *unstructured.Unstructured {
		f(&d)
		return deploymentObj(d)
	}
	smut := func(s statefulSetSpec, f func(*statefulSetSpec)) *unstructured.Unstructured {
		f(&s)
		return statefulSetObj(s)
	}
	dmut := func(d daemonSetSpec, f func(*daemonSetSpec)) *unstructured.Unstructured {
		f(&d)
		return daemonSetObj(d)
	}
	dep := readyDeployment("web", "u")
	sts := readyStatefulSet("db", "u")
	ds := readyDaemonSet("agent", "u")

	rows := []row{
		// Deployment
		{"deployment ready", deploymentObj(dep), CheckPass, ReasonRolloutComplete, "condition Available=True"},
		{"deployment observedGeneration behind", mut(dep, func(d *deploymentSpec) { d.observedGen = 3 }), CheckWarn, ReasonRolloutInProgress, "observedGeneration 3 >= generation 4"},
		{"deployment observedGeneration ahead is fine", mut(dep, func(d *deploymentSpec) { d.observedGen = 9 }), CheckPass, ReasonRolloutComplete, ""},
		{"deployment updatedReplicas short", mut(dep, func(d *deploymentSpec) { d.updated = 2 }), CheckWarn, ReasonRolloutInProgress, "updatedReplicas 2 == replicas 3"},
		{"deployment updatedReplicas over (surge) is not complete", mut(dep, func(d *deploymentSpec) { d.updated = 4 }), CheckWarn, ReasonRolloutInProgress, "updatedReplicas 4 == replicas 3"},
		{"deployment availableReplicas short", mut(dep, func(d *deploymentSpec) { d.available = 2 }), CheckWarn, ReasonRolloutInProgress, "availableReplicas 2 >= replicas 3"},
		{"deployment availableReplicas over is fine", mut(dep, func(d *deploymentSpec) { d.available = 4 }), CheckPass, ReasonRolloutComplete, ""},
		{"deployment Available=False", mut(dep, func(d *deploymentSpec) { d.availableCond = "False" }), CheckWarn, ReasonRolloutInProgress, "condition Available=False"},
		{"deployment Available absent", mut(dep, func(d *deploymentSpec) { d.availableCond = "" }), CheckWarn, ReasonRolloutInProgress, "condition Available=<unset>"},
		{"deployment replicas unset defaults to 1", func() *unstructured.Unstructured {
			u := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "u", generation: 1, observedGen: 1, updated: 1, available: 1, availableCond: "True"})
			unstructured.RemoveNestedField(u.Object, "spec", "replicas")
			return u
		}(), CheckPass, ReasonRolloutComplete, "updatedReplicas 1 == replicas 1"},
		{"deployment scaled to zero", mut(dep, func(d *deploymentSpec) { d.replicas, d.updated, d.available = 0, 0, 0 }), CheckPass, ReasonRolloutComplete, "replicas 0"},
		{"deployment status empty (never observed)", func() *unstructured.Unstructured {
			u := deploymentObj(dep)
			delete(u.Object, "status")
			return u
		}(), CheckWarn, ReasonRolloutInProgress, "observedGeneration 0 >= generation 4"},
		// StatefulSet
		{"statefulset ready", statefulSetObj(sts), CheckPass, ReasonRolloutComplete, "currentRevision db-7f8 == updateRevision db-7f8"},
		{"statefulset observedGeneration behind", smut(sts, func(s *statefulSetSpec) { s.observedGen = 1 }), CheckWarn, ReasonRolloutInProgress, "observedGeneration 1 >= generation 2"},
		{"statefulset updatedReplicas short", smut(sts, func(s *statefulSetSpec) { s.updated = 1 }), CheckWarn, ReasonRolloutInProgress, "updatedReplicas 1 == replicas 3"},
		{"statefulset readyReplicas short", smut(sts, func(s *statefulSetSpec) { s.ready = 2 }), CheckWarn, ReasonRolloutInProgress, "readyReplicas 2 >= replicas 3"},
		{"statefulset readyReplicas over is fine", smut(sts, func(s *statefulSetSpec) { s.ready = 4 }), CheckPass, ReasonRolloutComplete, ""},
		{"statefulset revision mismatch", smut(sts, func(s *statefulSetSpec) { s.updateRev = "db-9" }), CheckWarn, ReasonRolloutInProgress, "currentRevision db-7f8 == updateRevision db-9"},
		{"statefulset revisions unset", smut(sts, func(s *statefulSetSpec) { s.currentRev, s.updateRev = "", "" }), CheckPass, ReasonRolloutComplete, "<unset> == updateRevision <unset>"},
		// DaemonSet
		{"daemonset ready", daemonSetObj(ds), CheckPass, ReasonRolloutComplete, "numberUnavailable 0 == 0"},
		{"daemonset observedGeneration behind", dmut(ds, func(d *daemonSetSpec) { d.observedGen = 0 }), CheckWarn, ReasonRolloutInProgress, "observedGeneration 0 >= generation 1"},
		{"daemonset updatedNumberScheduled short", dmut(ds, func(d *daemonSetSpec) { d.updated = 4 }), CheckWarn, ReasonRolloutInProgress, "updatedNumberScheduled 4 == desiredNumberScheduled 5"},
		{"daemonset numberReady short", dmut(ds, func(d *daemonSetSpec) { d.ready = 4 }), CheckWarn, ReasonRolloutInProgress, "numberReady 4 >= desiredNumberScheduled 5"},
		{"daemonset numberUnavailable nonzero", dmut(ds, func(d *daemonSetSpec) { d.unavailable = 2 }), CheckWarn, ReasonRolloutInProgress, "numberUnavailable 2 == 0"},
		{"daemonset no nodes desired", dmut(ds, func(d *daemonSetSpec) { d.desired, d.updated, d.ready = 0, 0, 0 }), CheckPass, ReasonRolloutComplete, "desiredNumberScheduled 0"},
		// Unsupported
		{"configmap", configMapObj("c", "u"), CheckInconclusive, ReasonKindNotSupported, ""},
		{"deployment in a foreign group", func() *unstructured.Unstructured {
			u := deploymentObj(dep)
			u.SetAPIVersion("acme.example.com/v1")
			return u
		}(), CheckInconclusive, ReasonKindNotSupported, ""},
		{"nil object", nil, CheckInconclusive, ReasonKindNotSupported, ""},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckRollout(tc.obj, src, fixedNow)
			if got.Status != tc.status || got.Reason != tc.reason {
				t.Fatalf("got %s/%s, want %s/%s; detail: %s", got.Status, got.Reason, tc.status, tc.reason, got.Detail)
			}
			if tc.detailHas != "" && !strings.Contains(got.Detail, tc.detailHas) {
				t.Fatalf("detail %q lacks %q", got.Detail, tc.detailHas)
			}
			if got.CheckID != CheckIDRolloutComplete || !got.ObservedAt.Equal(fixedNow) || got.Source.ClusterID != "local" {
				t.Fatalf("envelope = %+v", got)
			}
			if got.Status == CheckPass && got.Reason != ReasonRolloutComplete {
				t.Fatalf("a pass must carry the ok reason")
			}
			if tc.obj != nil && tc.status != CheckInconclusive {
				if got.Source.UID != "u" || got.Source.Name != tc.obj.GetName() {
					t.Fatalf("source not filled from the object: %+v", got.Source)
				}
				if got.Evidence[tc.obj.GetKind()+"/"+tc.obj.GetName()] != "u" {
					t.Fatalf("evidence = %v", got.Evidence)
				}
			}
			// Severity follows status.
			switch got.Status {
			case CheckPass:
				if got.Severity != diagnostics.SeverityInfo {
					t.Fatalf("severity = %s", got.Severity)
				}
			case CheckWarn:
				if got.Severity != diagnostics.SeverityWarning {
					t.Fatalf("severity = %s", got.Severity)
				}
			}
		})
	}
}

func TestCheckRollout_NeverPassesForUnknownKind(t *testing.T) {
	// Every kind diagnostics can resolve except the three workloads, plus a
	// few arbitrary ones: none may pass, whatever their status looks like.
	for _, kind := range []string{"Pod", "Service", "PersistentVolumeClaim", "ConfigMap", "Secret", "Job", "CronJob", "ReplicaSet", "Ingress", "Namespace"} {
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": kind,
			"metadata": map[string]any{"name": "x", "uid": "u", "generation": int64(1)},
			"status":   map[string]any{"observedGeneration": int64(1), "replicas": int64(1), "readyReplicas": int64(1), "availableReplicas": int64(1), "updatedReplicas": int64(1)},
		}}
		got := CheckRollout(u, SourceRef{}, fixedNow)
		if got.Status != CheckInconclusive || got.Reason != ReasonKindNotSupported {
			t.Fatalf("%s: %s/%s", kind, got.Status, got.Reason)
		}
	}
}

func TestCheckRollout_JSONShapeMatchesU20Contract(t *testing.T) {
	got := CheckRollout(deploymentObj(readyDeployment("web", "u")), SourceRef{ClusterID: "local", Resource: "deployments"}, fixedNow)
	buf, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"checkId", "status", "severity", "reason", "message", "detail", "source", "observedAt", "evidence"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("persisted check lacks %q: %s", k, buf)
		}
	}
	src := m["source"].(map[string]any)
	for _, k := range []string{"clusterId", "group", "version", "resource", "kind", "namespace", "name", "uid"} {
		if _, ok := src[k]; !ok {
			t.Fatalf("source lacks %q: %s", k, buf)
		}
	}
}

func TestAggregateVerification(t *testing.T) {
	pass := CheckResult{Status: CheckPass, Reason: ReasonRolloutComplete}
	inProgress := CheckResult{Status: CheckWarn, Reason: ReasonRolloutInProgress}
	readFailed := CheckResult{Status: CheckInconclusive, Reason: ReasonReadFailed}
	notFound := CheckResult{Status: CheckFail, Reason: ReasonNotFound}
	unsupported := CheckResult{Status: CheckInconclusive, Reason: ReasonKindNotSupported}
	forbidden := CheckResult{Status: CheckInconclusive, Reason: ReasonReadForbidden}
	recreated := CheckResult{Status: CheckInconclusive, Reason: ReasonTargetRecreated}
	expired := CheckResult{Status: CheckInconclusive, Reason: ReasonWindowExpired}

	cases := []struct {
		name   string
		checks []CheckResult
		want   store.VerificationState
	}{
		{"none", nil, store.VerifyInconclusive},
		{"all pass", []CheckResult{pass, pass}, store.VerifyVerified},
		{"any fail wins", []CheckResult{pass, inProgress, notFound}, store.VerifyFailed},
		{"in progress", []CheckResult{pass, inProgress}, store.VerifyVerifying},
		{"transient read failure", []CheckResult{pass, readFailed}, store.VerifyVerifying},
		{"in progress beats terminal inconclusive", []CheckResult{unsupported, inProgress}, store.VerifyVerifying},
		{"unsupported", []CheckResult{pass, unsupported}, store.VerifyInconclusive},
		{"forbidden", []CheckResult{pass, forbidden}, store.VerifyInconclusive},
		{"recreated", []CheckResult{pass, recreated}, store.VerifyInconclusive},
		{"expired", []CheckResult{pass, expired}, store.VerifyInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregateVerification(tc.checks); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
