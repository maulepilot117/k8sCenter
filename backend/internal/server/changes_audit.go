package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/changes"
	"github.com/kubecenter/kubecenter/internal/store"
)

// ChangesVerificationAudit builds the hook that audits persisted change-receipt
// verification verdicts, for changes.Handler.SetVerificationAudit.
//
// GET /changes/{id}/verification writes a verdict for the receipt's owner (plan
// D6: stateless polling, no background watcher), and CLAUDE.md requires audit
// logging for writes. The handler decides when a write happened: it calls the
// hook only for a verdict THIS request's store write landed
// (VerificationResult.Persisted), so there is no store re-read here and nothing
// on the response path that can wait on the database. A live evaluation by a
// grantee or admin, a stored verdict read back, a write that lost a race to a
// concurrent final verdict, and the orphan reconciler's verdict (no HTTP
// request at all) never reach it.
//
// One change_verify entry is written per persisted verdict, naming the
// persisting caller and the receipt's cluster. A failed audit write is logged
// and does not fail the verification, which is already committed.
func ChangesVerificationAudit(logger audit.Logger, slogger *slog.Logger) changes.VerificationAuditFunc {
	if logger == nil {
		return nil
	}
	return func(r *http.Request, user *auth.User, rec *store.ChangeReceipt, state store.VerificationState) {
		if err := logger.Log(r.Context(), audit.Entry{
			Timestamp:    time.Now(),
			ClusterID:    rec.ClusterID,
			User:         user.Username,
			SourceIP:     r.RemoteAddr,
			Action:       audit.ActionChangeVerify,
			ResourceKind: "ChangeReceipt",
			ResourceName: rec.ID.String(),
			Result:       audit.ResultSuccess,
			Detail:       "verification verdict " + string(state) + " persisted (receipt owner " + rec.OwnerUsername + ")",
		}); err != nil && slogger != nil {
			slogger.Warn("changes: verification audit write failed", "receiptId", rec.ID, "error", err)
		}
	}
}
