package incidents

// Collaborator grant handlers (Release D, U23b; Q1 P2, P3). All three are
// owner-only: a collaborator can see the incident and gets 403, anyone else
// the 404 a missing id gets. A grant conveys standing to ask and nothing
// else (P2): every evidence read still re-authorizes the grantee's own
// Kubernetes access per item.
//
// The grantee id is an auth.User.ID. It is validated for length and charset
// (store.ValidateGranteeID) but NOT resolved against local_users: OIDC and
// LDAP identities have no row there. Revocation takes effect on the
// grantee's next request, because visibility is computed per request from
// the grants table; there is no session to invalidate.

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// GrantView is a grant as the API returns it.
type GrantView struct {
	IncidentID  string    `json:"incidentId"`
	GranteeID   string    `json:"granteeId"`
	GrantedBy   string    `json:"grantedBy"`
	CanAnnotate bool      `json:"canAnnotate"`
	CreatedAt   time.Time `json:"createdAt"`
}

// grantRequest is the POST /incidents/{id}/grants body. Decoded strictly:
// there is no field that could widen a grant beyond the collaborator
// ceiling, and none is accepted.
type grantRequest struct {
	GranteeID   string `json:"granteeId"`
	CanAnnotate bool   `json:"canAnnotate"`
}

func grantView(g store.IncidentGrantRow) GrantView {
	return GrantView{IncidentID: g.IncidentID.String(), GranteeID: g.GranteeID, GrantedBy: g.GrantedBy,
		CanAnnotate: g.CanAnnotate, CreatedAt: g.CreatedAt}
}

// requireOwner is the P3 gate after visibility: the caller already knows
// the incident exists, so a non-owner is told 403.
func requireOwner(w http.ResponseWriter, c *caller, what string) bool {
	if c.role != RoleOwner {
		httputil.WriteError(w, http.StatusForbidden, "only the incident owner may "+what, "")
		return false
	}
	return true
}

// HandleListGrants returns the incident's grants, oldest first (at most
// store.IncidentMaxGrants, so there is no paging).
// GET /api/v1/incidents/{incidentID}/grants
func (h *Handler) HandleListGrants(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok || !requireOwner(w, c, "list its grants") {
		return
	}
	grants, err := h.grants.ListGrants(r.Context(), c.row.ID)
	if err != nil {
		h.writeStoreFailure(w, "list incident grants", err)
		return
	}
	items := make([]GrantView, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantView(g))
	}
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: items, Metadata: &api.Metadata{Total: len(items)}})
}

// HandleAddGrant gives granteeId a grant, or updates can_annotate on the
// one it holds (201 with the grant either way). Granting to oneself is a
// 204 no-op: the owner already holds every right. A full incident is 409
// grant_limit_reached.
// POST /api/v1/incidents/{incidentID}/grants
func (h *Handler) HandleAddGrant(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok || !requireOwner(w, c, "share it") {
		return
	}
	var req grantRequest
	if !decodeStrictBody(w, r, &req) {
		return
	}
	if err := store.ValidateGranteeID(req.GranteeID); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid grantee id", err.Error())
		return
	}
	if req.GranteeID == user.ID {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	id := c.row.ID
	detail := "incident " + id.String() + " grantee " + req.GranteeID
	ctx, cancel := context.WithTimeout(r.Context(), storeWriteTimeout)
	defer cancel()
	if err := h.grants.AddGrant(ctx, id, user.ID, req.GranteeID, req.CanAnnotate); err != nil {
		h.auditLog(r, user, ActionIncidentGrantAdd, audit.ResultFailure, c.row.ClusterID, "incidentGrant", detail)
		h.writeStoreFailure(w, "add incident grant", err)
		return
	}
	h.auditLog(r, user, ActionIncidentGrantAdd, audit.ResultSuccess, c.row.ClusterID, "incidentGrant", detail)

	g, err := h.grants.GetGrant(ctx, id, req.GranteeID)
	if err != nil || g == nil {
		// The write committed and was audited; answer from the request.
		h.logger.Warn("incident grant added but could not be read back; answering from the request", "incidentId", id, "error", err)
		g = &store.IncidentGrantRow{IncidentID: id, GranteeID: req.GranteeID, GrantedBy: user.ID, CanAnnotate: req.CanAnnotate, CreatedAt: time.Now().UTC()}
	}
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: grantView(*g)})
}

// HandleRemoveGrant revokes granteeID's grant. A grantee without one is 404
// (not an idempotent 204): the owner already knows the incident exists, so
// nothing leaks, and a revoke that matched nothing (a mistyped id) is
// something the owner needs to hear about rather than a silent success.
// The path segment is percent-decoded, so an id containing "/" or "%" is
// still addressable.
// DELETE /api/v1/incidents/{incidentID}/grants/{granteeID}
func (h *Handler) HandleRemoveGrant(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	c, ok := h.loadVisible(w, r, user)
	if !ok || !requireOwner(w, c, "revoke its grants") {
		return
	}
	granteeID, err := url.PathUnescape(chi.URLParam(r, "granteeID"))
	if err == nil {
		err = store.ValidateGranteeID(granteeID)
	}
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid grantee id", err.Error())
		return
	}
	id := c.row.ID
	detail := "incident " + id.String() + " grantee " + granteeID
	ctx, cancel := context.WithTimeout(r.Context(), storeWriteTimeout)
	defer cancel()
	if err := h.grants.RemoveGrant(ctx, id, user.ID, granteeID); err != nil {
		h.auditLog(r, user, ActionIncidentGrantRemove, audit.ResultFailure, c.row.ClusterID, "incidentGrant", detail)
		h.writeStoreFailure(w, "remove incident grant", err)
		return
	}
	h.auditLog(r, user, ActionIncidentGrantRemove, audit.ResultSuccess, c.row.ClusterID, "incidentGrant", detail)
	w.WriteHeader(http.StatusNoContent)
}
