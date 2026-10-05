package changes

// Verification audit hook tests: it fires exactly once, and only for a verdict
// the owner's own request persisted.

import (
	"net/http"
	"testing"
	"time"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

type auditCall struct {
	user  *auth.User
	rec   *store.ChangeReceipt
	state store.VerificationState
}

// withAuditRecorder installs a recording hook and returns the call list.
func withAuditRecorder(hs *harness) *[]auditCall {
	var calls []auditCall
	hs.h.SetVerificationAudit(func(_ *http.Request, u *auth.User, rec *store.ChangeReceipt, st store.VerificationState) {
		calls = append(calls, auditCall{u, rec, st})
	})
	return &calls
}

func TestHandleVerification_AuditsOwnersPersistedVerdictOnce(t *testing.T) {
	hs := newHarness(t)
	calls := withAuditRecorder(hs)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	if w := hs.verify(t, testUser, rec.ID); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0].user.ID != testUser.ID || (*calls)[0].rec.ID != rec.ID ||
		(*calls)[0].state != store.VerifyVerified {
		t.Fatalf("audit calls = %+v; want exactly one for the owner's verified verdict", *calls)
	}

	// A second owner poll (another tab) reads the frozen verdict: no entry.
	if w := hs.verify(t, testUser, rec.ID); w.Code != http.StatusOK {
		t.Fatalf("second poll status %d", w.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("a stored verdict read back was audited again: %+v", *calls)
	}
}

func TestHandleVerification_NonFinalVerdictIsNotAudited(t *testing.T) {
	hs := newHarness(t)
	calls := withAuditRecorder(hs)
	stale := deploymentObj(deploymentSpec{name: "web", ns: "prod", uid: "uid-web", generation: 5, observedGen: 4,
		replicas: 3, updated: 3, available: 3, availableCond: "True"})
	hs.targeter.dyn, _ = fakeDyn(stale)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))

	v := decodeView(t, hs.verify(t, testUser, rec.ID))
	if v.State != store.VerifyVerifying {
		t.Fatalf("state = %s; want verifying", v.State)
	}
	if len(*calls) != 0 {
		t.Fatalf("progress (verifying) was audited: %+v", *calls)
	}
}

func TestHandleVerification_LiveEvaluationByGranteeOrAdminIsNotAudited(t *testing.T) {
	hs := newHarness(t)
	calls := withAuditRecorder(hs)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.grants[rec.ID] = []string{otherUser.ID}

	for _, u := range []*auth.User{otherUser, adminUser} {
		v := decodeView(t, hs.verify(t, u, rec.ID))
		if v.State != store.VerifyVerified {
			t.Fatalf("%s live view = %+v", u.ID, v)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a live, non-persisted evaluation was audited: %+v", *calls)
	}
	if hs.reader.row(t, rec.ID).VerificationState != store.VerifyPending {
		t.Fatal("a grantee/admin read persisted a verdict")
	}
}

// The owner's write loses a race to a concurrent final verdict: the store
// refuses it (ErrReceiptAlreadyFinal), the stored verdict is served, and this
// request wrote nothing, so it is not audited.
func TestHandleVerification_LostRaceIsNotAudited(t *testing.T) {
	hs := newHarness(t)
	calls := withAuditRecorder(hs)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	dyn, _ := fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	// While this request's live read is in flight, another request freezes the
	// receipt, so this request's SetVerification is refused.
	dyn.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		hs.reader.mu.Lock()
		hs.reader.rows[rec.ID].VerificationState = store.VerifyInconclusive
		hs.reader.mu.Unlock()
		return false, nil, nil
	})
	hs.targeter.dyn = dyn

	if w := hs.verify(t, testUser, rec.ID); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused (lost-race) write was audited: %+v", *calls)
	}
}

// A verdict the orphan reconciler wrote (no HTTP request, so no hook) reaches
// the handler only as an already-final stored verdict: no entry.
func TestHandleVerification_ReconciledVerdictIsNotAudited(t *testing.T) {
	hs := newHarness(t)
	calls := withAuditRecorder(hs)
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptUnknown, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	hs.reader.mu.Lock()
	hs.reader.rows[rec.ID].VerificationState = store.VerifyInconclusive
	hs.reader.mu.Unlock()

	if w := hs.verify(t, testUser, rec.ID); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("a reconciler-written verdict was audited as a user action: %+v", *calls)
	}
}

func TestHandleVerification_NoHookInstalledStillVerifies(t *testing.T) {
	hs := newHarness(t)
	hs.targeter.dyn, _ = fakeDyn(deploymentObj(readyDeployment("web", "uid-web")))
	rec := completedReceipt(hs.reader.fakeStore, store.ReceiptApplied, 10*time.Second, recordedDeployment(0, "web", "uid-web"))
	if v := decodeView(t, hs.verify(t, testUser, rec.ID)); v.State != store.VerifyVerified {
		t.Fatalf("view = %+v", v)
	}
}
