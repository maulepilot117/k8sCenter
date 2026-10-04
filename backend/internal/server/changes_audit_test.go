package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

// seqReceiptGetter returns successive Get results, then repeats the last. The
// first call is the pre-handler read, the second the post-handler read.
type seqReceiptGetter struct {
	mu    sync.Mutex
	steps []getStep
	calls int
}

type getStep struct {
	rec *store.ChangeReceipt
	err error
}

func (f *seqReceiptGetter) Get(context.Context, uuid.UUID) (*store.ChangeReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	if i >= len(f.steps) {
		i = len(f.steps) - 1
	}
	f.calls++
	return f.steps[i].rec, f.steps[i].err
}

type captureAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (c *captureAudit) Log(_ context.Context, e audit.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
	return nil
}

func verifyAuditHarness(g changesReceiptGetter, lg audit.Logger, status int) http.Handler {
	r := chi.NewRouter()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"state":"verified"}}`))
	})
	r.With(changesVerificationAudit(g, lg, nil)).Get("/changes/{id}/verification", h)
	return r
}

func runVerifyAudit(h http.Handler, id string, withUser bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/changes/"+id+"/verification", nil)
	if withUser {
		req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "u1", Username: "alice"}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The audit fires only when the STORED verification state goes from non-final
// to final across a 200: that is exactly "a verdict was written".
func TestChangesVerificationAudit(t *testing.T) {
	id := uuid.New()
	at := func(state store.VerificationState) getStep {
		return getStep{rec: &store.ChangeReceipt{ID: id, ClusterID: "c1", OwnerUsername: "owner", VerificationState: state}}
	}

	cases := []struct {
		name      string
		steps     []getStep
		status    int
		user      bool
		wantEntry bool
	}{
		{"verdict persisted (verifying to verified)", []getStep{at(store.VerifyVerifying), at(store.VerifyVerified)}, 200, true, true},
		{"verdict persisted (pending to inconclusive)", []getStep{at(store.VerifyPending), at(store.VerifyInconclusive)}, 200, true, true},
		{"verdict persisted (pending to verification_failed)", []getStep{at(store.VerifyPending), at(store.VerifyFailed)}, 200, true, true},
		{"live non-persisted evaluation (grantee/admin view): stored state unchanged", []getStep{at(store.VerifyPending), at(store.VerifyPending)}, 200, true, false},
		{"live evaluation still verifying: stored state unchanged", []getStep{at(store.VerifyVerifying), at(store.VerifyVerifying)}, 200, true, false},
		{"already-final receipt served from storage", []getStep{at(store.VerifyVerified), at(store.VerifyVerified)}, 200, true, false},
		{"error response", []getStep{at(store.VerifyPending), at(store.VerifyVerified)}, 503, true, false},
		{"pre-read failure passes through", []getStep{{err: errors.New("db down")}}, 200, true, false},
		{"post-read failure passes through", []getStep{at(store.VerifyPending), {err: errors.New("db down")}}, 200, true, false},
		{"no user passes through", []getStep{at(store.VerifyPending), at(store.VerifyVerified)}, 200, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lg := &captureAudit{}
			g := &seqReceiptGetter{steps: tc.steps}
			rec := runVerifyAudit(verifyAuditHarness(g, lg, tc.status), id.String(), tc.user)
			if rec.Code != tc.status || rec.Body.String() != `{"data":{"state":"verified"}}` {
				t.Fatalf("response altered: %d %q", rec.Code, rec.Body.String())
			}
			if tc.wantEntry != (len(lg.entries) == 1) || len(lg.entries) > 1 {
				t.Fatalf("audit entries = %+v; want entry=%v", lg.entries, tc.wantEntry)
			}
			if tc.wantEntry {
				e := lg.entries[0]
				if e.Action != audit.ActionChangeVerify || e.Result != audit.ResultSuccess ||
					e.User != "alice" || e.ClusterID != "c1" || e.ResourceKind != "ChangeReceipt" ||
					e.ResourceName != id.String() || !strings.Contains(e.Detail, "persisted") {
					t.Errorf("entry = %+v", e)
				}
			}
		})
	}

	t.Run("invalid id is not audited", func(t *testing.T) {
		lg := &captureAudit{}
		g := &seqReceiptGetter{steps: []getStep{at(store.VerifyPending), at(store.VerifyVerified)}}
		runVerifyAudit(verifyAuditHarness(g, lg, 400), "not-a-uuid", true)
		if len(lg.entries) != 0 || g.calls != 0 {
			t.Fatalf("entries = %+v, store reads = %d; want none", lg.entries, g.calls)
		}
	})
	t.Run("nil dependencies are a pass-through", func(t *testing.T) {
		rec := runVerifyAudit(verifyAuditHarness(nil, nil, 200), id.String(), true)
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}
