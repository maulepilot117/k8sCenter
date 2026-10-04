package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/store"
)

// changesReceiptGetter is the one receipt-store method the verification audit
// needs. *store.ChangeReceiptStore satisfies it.
type changesReceiptGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*store.ChangeReceipt, error)
}

// maxAuditedBody caps how much of the verification response is buffered to read
// its state. The body is a state plus per-object checks, normally a few KB.
const maxAuditedBody = 1 << 20

// auditRecorder passes the response through unchanged while keeping the status
// and a bounded copy of the body.
type auditRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (a *auditRecorder) WriteHeader(code int) {
	if a.status == 0 {
		a.status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *auditRecorder) Write(p []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	if room := maxAuditedBody - a.body.Len(); room > 0 {
		a.body.Write(p[:min(len(p), room)])
	}
	return a.ResponseWriter.Write(p)
}

func (a *auditRecorder) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// changesVerificationAudit records one audit entry when GET
// /changes/{id}/verification turns a receipt whose stored verification was not
// final into a final verdict.
//
// Why it exists: CLAUDE.md requires audit logging for writes, and this GET
// persists a verdict for the receipt's owner or an admin (changes plan D6:
// stateless polling, no background watcher). The handler lives in the changes
// package and carries no audit logger, so the entry is written at the route
// layer, where the audit logger and the user are both in hand.
//
// Rules, chosen so polling never floods the audit table:
//   - the receipt's stored verification must be non-final BEFORE the handler
//     runs (one indexed read); a receipt already final is served from storage
//     and writes nothing, so re-viewing a finished receipt is not audited;
//   - the response must be a 200 whose state is final (verified, inconclusive or
//     verification_failed); pending/verifying polls write nothing.
//
// An entry is therefore written for the transition, once per final verdict
// reached by a request. It records the verdict returned to the caller: for a
// grantee (whose live evaluation is never stored) the detail still names the
// state they were shown, which is the event worth auditing. A failure to read
// the receipt, an unparsable id, or a nil dependency passes the request through
// unaudited rather than failing a verification.
func changesVerificationAudit(receipts changesReceiptGetter, logger audit.Logger, slogger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if receipts == nil || logger == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, hasUser := auth.UserFromContext(r.Context())
			id, err := uuid.Parse(chi.URLParam(r, "id"))
			if !hasUser || user == nil || err != nil {
				next.ServeHTTP(w, r)
				return
			}
			rec, err := receipts.Get(r.Context(), id)
			if err != nil || rec == nil || rec.VerificationState.IsFinal() {
				next.ServeHTTP(w, r)
				return
			}

			rw := &auditRecorder{ResponseWriter: w}
			next.ServeHTTP(rw, r)

			if rw.status != http.StatusOK {
				return
			}
			var resp struct {
				Data struct {
					State store.VerificationState `json:"state"`
				} `json:"data"`
			}
			if json.Unmarshal(rw.body.Bytes(), &resp) != nil || !resp.Data.State.IsFinal() {
				return
			}
			if err := logger.Log(r.Context(), audit.Entry{
				Timestamp:    time.Now(),
				ClusterID:    rec.ClusterID,
				User:         user.Username,
				SourceIP:     r.RemoteAddr,
				Action:       audit.ActionChangeVerify,
				ResourceKind: "ChangeReceipt",
				ResourceName: rec.ID.String(),
				Result:       audit.ResultSuccess,
				Detail:       "verification verdict " + string(resp.Data.State) + " (receipt owner " + rec.OwnerUsername + ")",
			}); err != nil && slogger != nil {
				slogger.Warn("changes: verification audit write failed", "receiptId", rec.ID, "error", err)
			}
		})
	}
}
