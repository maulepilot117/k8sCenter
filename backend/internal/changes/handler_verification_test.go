package changes

// Verification endpoint tests: who may persist a verdict, the receipt's
// cluster versus the header's, budgets, cancellation and redaction of checks.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

func TestHandleVerification_PersistsAndFreezesAfterWindow(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, verificationWindow+time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, testUser, rec.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Reason != ReasonWindowExpired || v.RetryAfterSeconds != 0 {
		t.Fatalf("view = %+v", v)
	}
	if w.Header().Get("Retry-After") != "" {
		t.Fatal("a frozen verdict carries no Retry-After")
	}
	state, checks := persistedState(t, hs.reader.fakeStore, rec.ID)
	if state != store.VerifyInconclusive || len(checks) != 1 || checks[0].Reason != ReasonWindowExpired {
		t.Fatalf("persisted = %s %+v", state, checks)
	}
	if hs.reader.row(t, rec.ID).VerifiedAt == nil {
		t.Fatal("a final verdict stamps verifiedAt")
	}

	// A second poll returns the frozen verdict without touching the cluster.
	before := len(hs.targeter.calls())
	w = hs.verify(t, testUser, rec.ID)
	if w.Code != http.StatusOK || len(hs.targeter.calls()) != before {
		t.Fatalf("frozen verdict re-read the cluster: %d %v", w.Code, hs.targeter.calls())
	}
}

func TestHandleVerification_VerifyingCarriesRetryAfter(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyVerifying || v.RetryAfterSeconds != verifyRetryAfterSeconds || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("view = %+v, Retry-After = %q", v, w.Header().Get("Retry-After"))
	}
	if v.Checks[0].Reason != ReasonRolloutInProgress || v.Checks[0].Source == nil || v.Checks[0].Source.Name != "web" {
		t.Fatalf("check = %+v", v.Checks[0])
	}
}

func TestHandleVerification_ForbiddenObjectIsInconclusive(t *testing.T) {
	hs := newHarness(t)
	dyn, _ := fakeDyn()
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac"))
	})
	hs.targeter.dyn = dyn
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	// Live read forbidden, SAR still allowed: the check is visible and says so.
	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Redacted ||
		v.Checks[0].Status != CheckInconclusive || v.Checks[0].Reason != ReasonReadForbidden {
		t.Fatalf("view = %+v", v)
	}

	// SAR denied as well: the same check is reduced to the stub.
	hs.access.allow = func(string, string, string) bool { return false }
	rec2 := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	w = hs.verify(t, testUser, rec2.ID)
	v = decodeView(t, w)
	if v.RedactedChecks != 1 || !v.Checks[0].Redacted || v.Checks[0].RedactionReason != RedactionForbidden ||
		v.Checks[0].Source != nil || v.Checks[0].Message != "" {
		t.Fatalf("redacted check = %+v", v.Checks[0])
	}
	if strings.Contains(w.Body.String(), "web") {
		t.Fatalf("redacted check leaked the object: %s", w.Body.String())
	}
}

func TestHandleVerification_RequestCancelled(t *testing.T) {
	hs := newHarness(t)
	var cancel context.CancelFunc
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	// The client hangs up while the live read is in flight.
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		cancel()
		return false, nil, nil
	})
	hs.targeter.dyn = dyn
	hs.reader.honorCtx = true
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	// Owner (persisting) and grantee (live) hit the same mapping: a cancelled
	// client is written nothing, and nothing is persisted.
	hs.reader.grants[rec.ID] = []string{otherUser.ID}
	for _, user := range []*auth.User{testUser, otherUser} {
		var ctx context.Context
		ctx, cancel = context.WithCancel(t.Context())
		defer cancel()
		w := hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: user, id: rec.ID.String(), ctx: ctx})
		if w.Body.Len() != 0 {
			t.Fatalf("%s: cancelled verification wrote a response: %d %s", user.Username, w.Code, w.Body.String())
		}
		if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
			t.Fatal("a cancelled verification must persist nothing")
		}
	}
}

func TestHandleVerification_StoreErrorWithLiveRequestIsNotSilent(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	// The store's own cancellation, while THIS request is still alive: the
	// error text says "canceled" but the client is waiting for an answer.
	hs.reader.failSetVerification = context.Canceled
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, testUser, rec.ID)
	expectError(t, w, http.StatusServiceUnavailable, "change receipt store unavailable")
	if decodeEnvelope(t, w).Error.Reason != ReasonReceiptStoreUnavailable {
		t.Fatalf("reason = %s", w.Body.String())
	}
}

func TestHandleVerification_StalledAccessCheckRedactsWithinBudget(t *testing.T) {
	hs := newHarness(t)
	hs.h.clusterTimeout = 20 * time.Millisecond
	hs.access.delay = 5 * time.Second // a blackholed cluster
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second,
		recordedDeployment(0, "web", "uid-web"), recordedObj(1, "", "v1", "configmaps", "ConfigMap", "prod", "cfg", "uid-cfg"))

	start := time.Now()
	w := hs.verify(t, testUser, rec.ID)
	if time.Since(start) > 2*time.Second {
		t.Fatal("the post-pass SARs were not bounded by the budget")
	}
	v := decodeView(t, w)
	// Two objects: a ready Deployment (pass) and a ConfigMap (kind not
	// supported), so the aggregate is inconclusive. Both checks are stubs.
	if v.State != store.VerifyInconclusive || len(v.Checks) != 2 || v.RedactedChecks != 2 {
		t.Fatalf("a stalled SAR must fail closed: %+v", v)
	}
	if len(hs.access.calls) != 1 {
		t.Fatalf("after the deadline the memo must stop dialing: %v", hs.access.calls)
	}
}

func TestHandleVerification_BudgetExceededIs504ForEveryReader(t *testing.T) {
	hs := newHarness(t)
	hs.h.clusterTimeout = 20 * time.Millisecond
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		time.Sleep(50 * time.Millisecond) // a stalled cluster
		return false, nil, nil
	})
	hs.targeter.dyn = dyn
	hs.reader.honorCtx = true // the real store refuses a write on an expired context
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	for _, user := range []*auth.User{testUser, otherUser, adminUser} {
		w := hs.verify(t, user, rec.ID)
		expectError(t, w, http.StatusGatewayTimeout, "verification timed out")
	}
	if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
		t.Fatal("a verdict built from aborted reads must not be persisted")
	}
}

func TestHandleVerification_AdminEvaluatesLiveButNeverPersists(t *testing.T) {
	hs := newHarness(t)
	// The admin's own Kubernetes identity may not read the workload.
	dyn, _ := fakeDyn()
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac"))
	})
	hs.targeter.dyn = dyn
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	w := hs.verify(t, adminUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Reason != ReasonReadForbidden {
		t.Fatalf("admin live view = %+v", v)
	}
	row := hs.reader.row(t, rec.ID)
	if row.VerificationState != store.VerifyPending || row.Verification != nil {
		t.Fatalf("an admin's read_forbidden froze the owner's receipt: %s", row.VerificationState)
	}

	// The owner, who can read it, records the verdict; the admin then reads
	// the owner's stored verdict without a cluster call.
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	if v := decodeView(t, hs.verify(t, testUser, rec.ID)); v.State != store.VerifyVerified {
		t.Fatalf("owner verdict = %+v", v)
	}
	before := len(hs.targeter.calls())
	if v := decodeView(t, hs.verify(t, adminUser, rec.ID)); v.State != store.VerifyVerified || len(hs.targeter.calls()) != before {
		t.Fatalf("admin must read the stored verdict: %+v (target calls %v)", v, hs.targeter.calls())
	}
}

func TestMayPersistVerdict_OwnerOnly(t *testing.T) {
	for d, want := range map[ReadDecision]bool{ReadDenied: false, ReadAsOwner: true, ReadAsGrantee: false, ReadAsAdmin: false} {
		if got := mayPersistVerdict(d); got != want {
			t.Fatalf("mayPersistVerdict(%v) = %v, want %v", d, got, want)
		}
	}
}

func TestHandleVerification_NonAdminRemoteFinalVerdictIsServedRedacted(t *testing.T) {
	hs := newHarness(t)
	web := recordedDeployment(0, "web", "uid-web")
	rec := hs.seed(testUser, "remote-1", web) // alice is no longer admin
	withVerification(hs, rec, store.VerifyVerified,
		storedCheck(rec, CheckPass, ReasonRolloutComplete, web, map[string]string{"Deployment/web": "uid-web"}))

	w := hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: testUser, cluster: "remote-1", id: rec.ID.String()})
	v := decodeView(t, w)
	if v.State != store.VerifyVerified || v.RedactedChecks != 1 || !v.Checks[0].Redacted || v.Checks[0].Source != nil {
		t.Fatalf("final verdict for a non-admin remote owner = %+v", v)
	}
	if len(hs.targeter.calls()) != 0 || len(hs.access.calls) != 0 {
		t.Fatalf("no remote cluster call may be made: target=%v access=%v", hs.targeter.calls(), hs.access.calls)
	}
	if strings.Contains(w.Body.String(), "uid-web") {
		t.Fatalf("remote object leaked to a non-admin: %s", w.Body.String())
	}
}

func TestHandleVerification_GranteeEvaluatesLiveButNeverPersists(t *testing.T) {
	hs := newHarness(t)
	// The grantee's identity is forbidden on the live read.
	dyn, _ := fakeDyn()
	reads := 0
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		reads++
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac"))
	})
	hs.targeter.dyn = dyn
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	w := hs.verify(t, otherUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || len(v.Checks) != 1 || v.Checks[0].Reason != ReasonReadForbidden || reads != 1 {
		t.Fatalf("grantee must get a live evaluation: %+v (reads %d)", v, reads)
	}
	row := hs.reader.row(t, rec.ID)
	if row.VerificationState != store.VerifyPending || row.Verification != nil || row.VerifiedAt != nil {
		t.Fatalf("a grantee's verdict must never be stored: %s %s", row.VerificationState, row.Verification)
	}
	for _, c := range hs.reader.calls {
		if c == "setVerification" {
			t.Fatal("grantee verification wrote to the store")
		}
	}

	// The owner, who can read the object, then records the real verdict.
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	w = hs.verify(t, testUser, rec.ID)
	v = decodeView(t, w)
	if v.State != store.VerifyVerified || hs.reader.row(t, rec.ID).VerificationState != store.VerifyVerified {
		t.Fatalf("owner verdict = %+v, stored %s", v, hs.reader.row(t, rec.ID).VerificationState)
	}

	// Once final, the grantee reads the stored verdict, no cluster call.
	before := len(hs.targeter.calls())
	w = hs.verify(t, otherUser, rec.ID)
	v = decodeView(t, w)
	if v.State != store.VerifyVerified || len(hs.targeter.calls()) != before {
		t.Fatalf("frozen verdict for grantee = %+v, target calls %v", v, hs.targeter.calls())
	}
}

func TestHandleVerification_GranteeWindowExpiryIsNotFrozenForOwner(t *testing.T) {
	hs := newHarness(t)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, verificationWindow+time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	w := hs.verify(t, otherUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyInconclusive || v.Checks[0].Reason != ReasonWindowExpired {
		t.Fatalf("grantee sees the same rules: %+v", v)
	}
	if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
		t.Fatal("grantee evaluation froze the owner's receipt")
	}
}

func TestHandleVerification_PendingReceiptDoesNotRead(t *testing.T) {
	hs := newHarness(t)
	dyn, gets := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	hs.targeter.dyn = dyn
	rec := hs.seed(testUser, "local", recordedDeployment(0, "web", "uid-web"))
	rec.State, rec.CompletedAt = store.ReceiptApplying, nil
	hs.reader.put(*rec)

	w := hs.verify(t, testUser, rec.ID)
	v := decodeView(t, w)
	if v.State != store.VerifyPending || len(v.Checks) != 0 || gets.Load() != 0 {
		t.Fatalf("pending view = %+v, gets = %d", v, gets.Load())
	}
}

func TestHandleVerification_UsesReceiptClusterNotHeader(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	rec.ClusterID, rec.OwnerID = "remote-1", adminUser.ID
	hs.reader.put(*rec)

	// Header: local. Receipt: remote-1. Admin owner.
	w := hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: adminUser, cluster: "local", id: rec.ID.String()})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if calls := hs.targeter.calls(); len(calls) != 1 || calls[0] != "remote-1" {
		t.Fatalf("verification client built for %v, want the receipt's remote-1", calls)
	}
	if len(hs.access.calls) != 1 || hs.access.calls[0].cluster != "remote-1" {
		t.Fatalf("check redaction SAR went to %v", hs.access.calls)
	}
	v := decodeView(t, w)
	if v.State != store.VerifyVerified || v.Checks[0].Source == nil || v.Checks[0].Source.ClusterID != "remote-1" {
		t.Fatalf("view = %+v", v)
	}

	// The same receipt, owned by a non-admin: no remote cluster access.
	rec.OwnerID = testUser.ID
	rec.VerificationState, rec.Verification, rec.VerifiedAt = store.VerifyPending, nil, nil
	hs.reader.put(*rec)
	before := len(hs.targeter.calls())
	w = hs.do(t, hs.h.HandleVerification, http.MethodGet, request{user: testUser, cluster: "remote-1", id: rec.ID.String()})
	expectError(t, w, http.StatusForbidden, "admin role required for remote cluster access")
	if len(hs.targeter.calls()) != before {
		t.Fatal("a non-admin must not reach a remote cluster through a receipt")
	}
}

func TestHandleVerification_TargetFailure(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.err = errors.New("cannot build client")
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	w := hs.verify(t, testUser, rec.ID)
	expectError(t, w, http.StatusInternalServerError, "failed to create kubernetes client")
	if strings.Contains(w.Body.String(), "cannot build client") {
		t.Fatal("5xx detail leaked")
	}

	hs.h.clusters = nil
	w = hs.verify(t, testUser, rec.ID)
	expectError(t, w, http.StatusServiceUnavailable, "cluster routing is not configured")
}
