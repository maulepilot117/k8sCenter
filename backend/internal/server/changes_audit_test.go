package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

type captureAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (c *captureAudit) Log(_ context.Context, e audit.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
	return c.err
}

// The hook writes exactly one change_verify entry naming the caller, the
// receipt and its cluster. When the handler calls it is covered by the
// changes package tests (persisted-only).
func TestChangesVerificationAudit_WritesOneEntry(t *testing.T) {
	lg := &captureAudit{}
	hook := ChangesVerificationAudit(lg, nil)
	if hook == nil {
		t.Fatal("hook is nil with a logger")
	}
	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/changes/"+id.String()+"/verification", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	rec := &store.ChangeReceipt{ID: id, ClusterID: "c1", OwnerUsername: "alice"}

	hook(req, &auth.User{ID: "u1", Username: "alice"}, rec, store.VerifyVerified)

	if len(lg.entries) != 1 {
		t.Fatalf("entries = %+v; want exactly one", lg.entries)
	}
	e := lg.entries[0]
	if e.Action != audit.ActionChangeVerify || e.Result != audit.ResultSuccess || e.User != "alice" ||
		e.ClusterID != "c1" || e.ResourceKind != "ChangeReceipt" || e.ResourceName != id.String() ||
		e.SourceIP != "10.1.2.3:4567" || !strings.Contains(e.Detail, "verified persisted") {
		t.Errorf("entry = %+v", e)
	}
}

// A failed audit write must not panic or propagate: the verdict is committed.
func TestChangesVerificationAudit_WriteFailureIsSwallowed(t *testing.T) {
	hook := ChangesVerificationAudit(&captureAudit{err: errors.New("audit down")}, nil)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	hook(req, &auth.User{Username: "alice"}, &store.ChangeReceipt{ID: uuid.New()}, store.VerifyInconclusive)
}

// No audit logger means no hook (the handler then audits nothing).
func TestChangesVerificationAudit_NilLoggerYieldsNilHook(t *testing.T) {
	if ChangesVerificationAudit(nil, nil) != nil {
		t.Fatal("want a nil hook without a logger")
	}
}
