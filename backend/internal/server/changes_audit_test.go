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

type fakeReceiptGetter struct {
	rec *store.ChangeReceipt
	err error
}

func (f fakeReceiptGetter) Get(context.Context, uuid.UUID) (*store.ChangeReceipt, error) {
	return f.rec, f.err
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

func verifyAuditHarness(g changesReceiptGetter, lg audit.Logger, status int, body string) http.Handler {
	r := chi.NewRouter()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
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

func TestChangesVerificationAudit(t *testing.T) {
	id := uuid.New()
	receipt := func(state store.VerificationState) fakeReceiptGetter {
		return fakeReceiptGetter{rec: &store.ChangeReceipt{ID: id, ClusterID: "c1", OwnerUsername: "owner", VerificationState: state}}
	}
	finalBody := `{"data":{"state":"verified","checks":[]}}`

	cases := []struct {
		name      string
		getter    changesReceiptGetter
		status    int
		body      string
		user      bool
		wantEntry bool
	}{
		{"transition to final is audited", receipt(store.VerifyVerifying), 200, finalBody, true, true},
		{"pending to inconclusive is audited", receipt(store.VerifyPending), 200, `{"data":{"state":"inconclusive"}}`, true, true},
		{"already-final receipt is not audited", receipt(store.VerifyVerified), 200, finalBody, true, false},
		{"still verifying is not audited", receipt(store.VerifyPending), 200, `{"data":{"state":"verifying"}}`, true, false},
		{"error response is not audited", receipt(store.VerifyPending), 503, finalBody, true, false},
		{"unparsable body is not audited", receipt(store.VerifyPending), 200, `not json`, true, false},
		{"receipt read failure passes through unaudited", fakeReceiptGetter{err: errors.New("db down")}, 200, finalBody, true, false},
		{"no user passes through unaudited", receipt(store.VerifyPending), 200, finalBody, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lg := &captureAudit{}
			rec := runVerifyAudit(verifyAuditHarness(tc.getter, lg, tc.status, tc.body), id.String(), tc.user)
			if rec.Code != tc.status || rec.Body.String() != tc.body {
				t.Fatalf("response altered: %d %q", rec.Code, rec.Body.String())
			}
			if tc.wantEntry != (len(lg.entries) == 1) || len(lg.entries) > 1 {
				t.Fatalf("audit entries = %+v; want entry=%v", lg.entries, tc.wantEntry)
			}
			if tc.wantEntry {
				e := lg.entries[0]
				if e.Action != audit.ActionChangeVerify || e.Result != audit.ResultSuccess ||
					e.User != "alice" || e.ClusterID != "c1" || e.ResourceKind != "ChangeReceipt" ||
					e.ResourceName != id.String() || !strings.Contains(e.Detail, "verdict ") {
					t.Errorf("entry = %+v", e)
				}
			}
		})
	}

	t.Run("invalid id is not audited", func(t *testing.T) {
		lg := &captureAudit{}
		runVerifyAudit(verifyAuditHarness(receipt(store.VerifyPending), lg, 400, finalBody), "not-a-uuid", true)
		if len(lg.entries) != 0 {
			t.Fatalf("entries = %+v", lg.entries)
		}
	})
	t.Run("nil dependencies are a pass-through", func(t *testing.T) {
		rec := runVerifyAudit(verifyAuditHarness(nil, nil, 200, finalBody), id.String(), true)
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}
