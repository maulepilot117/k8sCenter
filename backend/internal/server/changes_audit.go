package server

import (
	"context"
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

// statusRecorder passes the response through unchanged and remembers the status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// changesVerificationAudit records one audit entry when GET
// /changes/{id}/verification PERSISTS a final verdict.
//
// Why it exists: CLAUDE.md requires audit logging for writes, and this GET
// writes a verdict for the receipt's owner or an admin (changes plan D6:
// stateless polling, no background watcher). The handler lives in the changes
// package and carries no audit logger, so the entry is written at the route
// layer, where the audit logger and the user are both in hand.
//
// What counts as "a verdict was written" is read from the store, not inferred
// from the response: the receipt's stored verification state is read before
// the handler runs and again after a 200, and an entry is written only when it
// went from non-final to final (verified, inconclusive or verification_failed).
// Therefore none of these write an entry: a receipt already final (served from
// storage), a pending or verifying poll, a grantee's or admin-view live
// evaluation that the handler does not store, a non-200 response, and any
// request where either read fails. A failed read passes the request through
// unaudited rather than failing a verification.
//
// The entry names the caller whose request observed the transition. If two
// requests race, the one whose post-read sees the final state is audited, and
// a concurrent request that did not itself write may be the one named. The
// verdict is still written once and the entry's resource and state are exact.
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
			before, err := receipts.Get(r.Context(), id)
			if err != nil || before == nil || before.VerificationState.IsFinal() {
				next.ServeHTTP(w, r)
				return
			}

			rw := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rw, r)
			if rw.status != http.StatusOK {
				return
			}

			// Re-read on a context that survives the response: the request ctx is
			// still live here, but the audit must not depend on the client staying
			// connected after the handler has already written the verdict.
			after, err := receipts.Get(context.WithoutCancel(r.Context()), id)
			if err != nil || after == nil || !after.VerificationState.IsFinal() {
				return
			}
			if err := logger.Log(r.Context(), audit.Entry{
				Timestamp:    time.Now(),
				ClusterID:    after.ClusterID,
				User:         user.Username,
				SourceIP:     r.RemoteAddr,
				Action:       audit.ActionChangeVerify,
				ResourceKind: "ChangeReceipt",
				ResourceName: after.ID.String(),
				Result:       audit.ResultSuccess,
				Detail:       "verification verdict " + string(after.VerificationState) + " persisted (receipt owner " + after.OwnerUsername + ")",
			}); err != nil && slogger != nil {
				slogger.Warn("changes: verification audit write failed", "receiptId", after.ID, "error", err)
			}
		})
	}
}
