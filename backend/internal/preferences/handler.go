package preferences

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
	"github.com/kubecenter/kubecenter/pkg/api"
)

// Handler serves the per-user saved-view and pin endpoints.
//
// Authorization contract: the owner is ALWAYS auth.User.ID taken from the
// request context, and the cluster is ALWAYS the value the cluster-context
// middleware resolved. Neither is ever read from a request body — the request
// types in types.go cannot express them, and the decoder rejects unknown
// fields, so a client that tries gets a 400 naming the field.
//
// No RBAC re-check happens here, deliberately. A preference record holds only
// strings the user themselves typed or picked, so listing their own records
// discloses nothing they did not author. Authorization on the referenced
// object is enforced where it always was: when the saved view issues its
// resource list, or the pin opens its target, both of which impersonate the
// user. A pin whose target the user can no longer read renders as forbidden,
// which is a distinct state from deleted and from replaced.
//
// Store is nil when the deployment has no database. Every endpoint then
// answers 503 with reason "database_unavailable" rather than 404, so a client
// can tell "this server cannot persist preferences" from "no such record".
// The routes are registered either way, because chi's bare 404 for an
// unregistered path is indistinguishable from a missing record.
//
// The "no RBAC re-check" paragraph above has one exception, and AccessChecker
// is it: a dashboard layout can carry a namespace as a widget parameter, and
// that parameter is evidence of what the user could see when they saved it,
// never of what they may see now. HandleGetLayout re-authorizes each one on
// every read.
type Handler struct {
	Store       *store.PreferenceStore
	AuditLogger audit.Logger
	Logger      *slog.Logger

	// AccessChecker re-authorizes the namespaces a stored layout names. Nil
	// means no checker was wired, which is a deployment mistake rather than a
	// mode: the read path then withholds every placement it cannot
	// authorize, because "could not check" is not "allowed".
	AccessChecker *resources.AccessChecker

	// maxSavedViews, maxPins and maxLayouts override the package ceilings.
	// Zero means the package default. They are unexported so no caller can
	// widen a user's quota; the package's own tests set them so the limit path
	// can be exercised without creating a hundred records first.
	maxSavedViews int
	maxPins       int
	maxLayouts    int
}

// savedViewCeiling and pinCeiling resolve the effective per-user limits.
func (h *Handler) savedViewCeiling() int {
	if h.maxSavedViews > 0 {
		return h.maxSavedViews
	}
	return MaxSavedViewsPerUser
}

func (h *Handler) pinCeiling() int {
	if h.maxPins > 0 {
		return h.maxPins
	}
	return MaxPinsPerUser
}

// layoutCeiling resolves the per-cluster layout limit.
func (h *Handler) layoutCeiling() int {
	if h.maxLayouts > 0 {
		return h.maxLayouts
	}
	return MaxDashboardLayoutsPerUser
}

// ---------------------------------------------------------------------------
// Saved views
// ---------------------------------------------------------------------------

// HandleListViews returns every saved view the caller owns, across all
// clusters, most recently updated first.
//
// The response is deliberately unpaginated. MaxSavedViewsPerUser is enforced
// inside the store's INSERT, so the result set is bounded by construction at
// a size no client needs to page through, and each record's config is capped
// at 8 KiB by the database. The ceiling is therefore the bound; if it is ever
// raised, this endpoint needs a LIMIT and a sort tiebreaker before it can be
// paged safely, because `ORDER BY updated_at DESC` alone is not a total order
// and a cursor over it can skip or repeat rows.
func (h *Handler) HandleListViews(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, store.PreferenceKindSavedView)
}

// HandleCreateView stores a new saved view for the caller.
func (h *Handler) HandleCreateView(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}

	var req CreateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if !h.validName(w, req.Name) {
		return
	}
	_, normalized, err := ValidateSavedView(req.Config)
	if err != nil {
		h.writeValidationError(w, err)
		return
	}

	dedup := SavedViewDedupKey(req.Name)
	if err := ValidateDedupKey(dedup); err != nil {
		h.writeValidationError(w, err)
		return
	}

	rec := store.PreferenceRecord{
		OwnerID:       user.ID,
		Kind:          store.PreferenceKindSavedView,
		Name:          req.Name,
		ClusterID:     middleware.ClusterIDFromContext(r.Context()),
		DedupKey:      dedup,
		SchemaVersion: SavedViewSchemaVersion,
		Config:        normalized,
	}
	created, err := h.Store.Create(r.Context(), rec, h.savedViewCeiling())
	if err != nil {
		h.audit(r, user, audit.ActionCreate, "savedView", req.Name, audit.ResultFailure, "")
		h.mapStoreError(w, err, "duplicate_name", h.savedViewCeiling())
		return
	}

	h.audit(r, user, audit.ActionCreate, "savedView", created.Name, audit.ResultSuccess, created.ID.String())
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: created})
}

// HandleUpdateView rewrites a saved view the caller owns, subject to the
// optimistic-concurrency check on revision.
func (h *Handler) HandleUpdateView(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	id, ok := h.recordID(w, r)
	if !ok {
		return
	}
	if !h.requireKind(w, r, user.ID, id, store.PreferenceKindSavedView) {
		return
	}

	var req UpdateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if !h.validName(w, req.Name) {
		return
	}
	if req.Revision < 1 {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"revision is required", "invalid_config", nil)
		return
	}
	_, normalized, err := ValidateSavedView(req.Config)
	if err != nil {
		h.writeValidationError(w, err)
		return
	}

	dedup := SavedViewDedupKey(req.Name)
	if err := ValidateDedupKey(dedup); err != nil {
		h.writeValidationError(w, err)
		return
	}

	updated, err := h.Store.Update(r.Context(), user.ID, id, req.Revision,
		req.Name, dedup, SavedViewSchemaVersion, normalized)
	if err != nil {
		h.audit(r, user, audit.ActionUpdate, "savedView", req.Name, audit.ResultFailure, id.String())
		h.mapStoreError(w, err, "duplicate_name", h.savedViewCeiling())
		return
	}

	h.audit(r, user, audit.ActionUpdate, "savedView", updated.Name, audit.ResultSuccess, updated.ID.String())
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: updated})
}

// HandleDeleteView removes a saved view the caller owns.
func (h *Handler) HandleDeleteView(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, store.PreferenceKindSavedView, "savedView")
}

// ---------------------------------------------------------------------------
// Pins
// ---------------------------------------------------------------------------

// HandleListPins returns every pin the caller owns, across all clusters. It is
// unpaginated for the same reason as HandleListViews, bounded by MaxPinsPerUser.
func (h *Handler) HandleListPins(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, store.PreferenceKindPin)
}

// HandleCreatePin pins a resource for the caller.
//
// There is no update counterpart: a pin addresses one object, so it is created
// or removed, never edited.
func (h *Handler) HandleCreatePin(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}

	var req CreateRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if !h.validName(w, req.Name) {
		return
	}
	cfg, normalized, err := ValidatePin(req.Config)
	if err != nil {
		h.writeValidationError(w, err)
		return
	}

	dedup := PinDedupKey(cfg)
	if err := ValidateDedupKey(dedup); err != nil {
		h.writeValidationError(w, err)
		return
	}

	rec := store.PreferenceRecord{
		OwnerID:       user.ID,
		Kind:          store.PreferenceKindPin,
		Name:          req.Name,
		ClusterID:     middleware.ClusterIDFromContext(r.Context()),
		DedupKey:      dedup,
		SchemaVersion: PinSchemaVersion,
		Config:        normalized,
	}
	created, err := h.Store.Create(r.Context(), rec, h.pinCeiling())
	if err != nil {
		h.audit(r, user, audit.ActionCreate, "pin", req.Name, audit.ResultFailure, "")
		h.mapStoreError(w, err, "already_pinned", h.pinCeiling())
		return
	}

	h.audit(r, user, audit.ActionCreate, "pin", created.Name, audit.ResultSuccess, created.ID.String())
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: created})
}

// HandleDeletePin unpins a resource for the caller.
func (h *Handler) HandleDeletePin(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, store.PreferenceKindPin, "pin")
}

// ---------------------------------------------------------------------------
// Dashboard layouts
// ---------------------------------------------------------------------------

// LayoutResponse is the body both layout endpoints return: the stored record,
// plus anything the server removed from it on the way out.
//
// Withheld is not cosmetic. The read path drops placements whose namespace the
// caller can no longer see, and a client that received only the survivors
// could not tell that from a layout the user never arranged -- so it would
// render a dashboard quietly missing widgets, and the first save after that
// would make the loss permanent. Named, the client can say which widget is
// hidden and why, and can decline to write the filtered layout back.
type LayoutResponse struct {
	store.PreferenceRecord
	Withheld []string `json:"withheld,omitempty"`
}

// HandleGetLayout returns the caller's layout for one dashboard scope.
//
// A scope with no saved layout answers 204 rather than 404: "you have not
// customized this dashboard" is a normal state, and the client answers it by
// rendering its built-in default. A scope this server does not serve answers
// 400, so a mistyped scope stays distinguishable from an unsaved one -- with
// 404 for both, a client could not tell which it had.
func (h *Handler) HandleGetLayout(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	scope, ok := h.layoutScope(w, r)
	if !ok {
		return
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	rec, err := h.Store.GetByDedupKey(r.Context(), user.ID,
		store.PreferenceKindDashboardLayout, clusterID, DashboardLayoutDedupKey(scope))
	if err != nil {
		if errors.Is(err, store.ErrPreferenceNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.logger().Error("preferences: reading a dashboard layout failed",
			"scope", scope, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError,
			"failed to read the dashboard layout", "")
		return
	}

	filtered, withheld, err := h.withholdUnauthorized(r.Context(), user, clusterID, rec.Config)
	if err != nil {
		h.logger().Error("preferences: re-authorizing a dashboard layout failed",
			"scope", scope, "cluster", clusterID, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError,
			"failed to read the dashboard layout", "")
		return
	}
	rec.Config = filtered

	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: LayoutResponse{
		PreferenceRecord: *rec,
		Withheld:         withheld,
	}})
}

// HandleSaveLayout creates or replaces the caller's layout for one scope.
//
// PUT and no POST: a layout is a singleton per scope, so the client should not
// have to know whether one exists, and the scope is the address in both
// directions. revision 0 means "I believe none exists"; anything else is a
// claim about the stored revision. Both are claims, both are checked, and a
// wrong one is a conflict rather than an overwrite -- two tabs arranging the
// same dashboard must not silently discard each other's work.
func (h *Handler) HandleSaveLayout(w http.ResponseWriter, r *http.Request) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	scope, ok := h.layoutScope(w, r)
	if !ok {
		return
	}

	var req SaveLayoutRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	if req.Revision < 0 {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"revision cannot be negative", "invalid_config", nil)
		return
	}
	cfg, normalized, err := ValidateDashboardLayout(req.Config)
	if err != nil {
		h.writeValidationError(w, err)
		return
	}
	// The path names the record and the config carries a scope of its own. A
	// layout filed under one scope while claiming to be another would come
	// back as the wrong dashboard on every later read, so the two must agree
	// and the path is the one that decides.
	if cfg.Scope != scope {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			fmt.Sprintf("config is for scope %q but the path asked for %q", cfg.Scope, scope),
			"invalid_config", nil)
		return
	}

	dedup := DashboardLayoutDedupKey(scope)
	if err := ValidateDedupKey(dedup); err != nil {
		h.writeValidationError(w, err)
		return
	}

	// Which of the two writes this is depends on what is stored, not on what
	// the client believes: the belief is the thing being checked. A claim that
	// disagrees with the stored state is a conflict either way round -- "I
	// believe none exists" when one does would otherwise discard the layout
	// another tab just saved, and "I believe revision 4 exists" when none does
	// would otherwise resurrect, at a revision the client made up, a layout
	// the server has no record of.
	//
	// The read and the write that follows it are separate statements, so this
	// decides which write to attempt and never whether it may proceed. Each
	// write re-checks under the database's own guarantees: the UPDATE carries
	// the expected revision, and the INSERT counts the quota under an advisory
	// lock. A row that moves between the two loses the race, it does not slip
	// through.
	existing, err := h.Store.GetByDedupKey(r.Context(), user.ID,
		store.PreferenceKindDashboardLayout, middleware.ClusterIDFromContext(r.Context()), dedup)
	switch {
	case errors.Is(err, store.ErrPreferenceNotFound):
		if req.Revision != 0 {
			h.writeLayoutConflict(w)
			return
		}
		h.createLayout(w, r, user, scope, dedup, normalized)
	case err != nil:
		h.logger().Error("preferences: reading a dashboard layout before saving failed",
			"scope", scope, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to store preference", "")
	default:
		// Deliberately redundant, and recorded as such: handing revision 0 to
		// the UPDATE below would also answer 409, because no stored row can
		// carry it (revision >= 1 is a CHECK in migration 000018). Mutating
		// this branch away therefore changes no response, which is why no test
		// turns red for it. It stays because the rule it states is part of
		// this endpoint's contract, and a reader should find it here rather
		// than infer it from a constraint three layers down.
		if req.Revision == 0 {
			h.writeLayoutConflict(w)
			return
		}
		h.replaceLayout(w, r, user, existing.ID, scope, dedup, normalized, req.Revision)
	}
}

// createLayout stores the caller's first layout for a scope.
func (h *Handler) createLayout(w http.ResponseWriter, r *http.Request, user *auth.User,
	scope, dedup string, normalized json.RawMessage,
) {
	rec := store.PreferenceRecord{
		OwnerID:       user.ID,
		Kind:          store.PreferenceKindDashboardLayout,
		Name:          scope,
		ClusterID:     middleware.ClusterIDFromContext(r.Context()),
		DedupKey:      dedup,
		SchemaVersion: DashboardLayoutSchemaVersion,
		Config:        normalized,
	}
	// CreateInCluster, never Create: the ceiling is the number of scopes,
	// which is the right number only when it is counted inside one cluster.
	created, err := h.Store.CreateInCluster(r.Context(), rec, h.layoutCeiling())
	if err != nil {
		h.audit(r, user, audit.ActionCreate, "dashboardLayout", scope, audit.ResultFailure, "")
		// The caller was told there was no layout here a moment ago. If there
		// is one now, another save won the race, and that is a conflict
		// whichever sentinel the store reached for -- the ceiling is the
		// number of scopes, so a second save of the SAME scope exhausts the
		// quota before it ever reaches the unique index, and would otherwise
		// be reported as a limit the user has no way to act on.
		if errors.Is(err, store.ErrPreferenceLimit) && h.layoutExists(r.Context(), user.ID, rec.ClusterID, dedup) {
			h.writeLayoutConflict(w)
			return
		}
		h.mapLayoutError(w, err)
		return
	}

	h.audit(r, user, audit.ActionCreate, "dashboardLayout", created.Name,
		audit.ResultSuccess, created.ID.String())
	httputil.WriteJSON(w, http.StatusCreated, api.Response{
		Data: LayoutResponse{PreferenceRecord: *created},
	})
}

// replaceLayout rewrites the layout already stored under a scope.
//
// id came from the caller's own scope lookup, never from the request: the
// client is never told a layout's id, so it can never send somebody else's.
// The revision check stays in the store's UPDATE rather than being decided
// from that lookup, because the lookup is a separate statement and the row can
// move between the two.
func (h *Handler) replaceLayout(w http.ResponseWriter, r *http.Request, user *auth.User,
	id uuid.UUID, scope, dedup string, normalized json.RawMessage, revision int64,
) {
	updated, err := h.Store.Update(r.Context(), user.ID, id, revision,
		scope, dedup, DashboardLayoutSchemaVersion, normalized)
	if err != nil {
		h.audit(r, user, audit.ActionUpdate, "dashboardLayout", scope,
			audit.ResultFailure, id.String())
		h.mapLayoutError(w, err)
		return
	}

	h.audit(r, user, audit.ActionUpdate, "dashboardLayout", updated.Name,
		audit.ResultSuccess, updated.ID.String())
	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data: LayoutResponse{PreferenceRecord: *updated},
	})
}

// layoutScope reads and validates the {scope} path parameter.
//
// An unserved scope answers 400 so it stays distinguishable from the 204 a
// served-but-unsaved scope gives. A client that mistyped a scope learns that it
// mistyped one; a client asking about a dashboard the user has simply never
// arranged gets the answer that means "render your default".
func (h *Handler) layoutScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	scope := chi.URLParam(r, "scope")
	if _, ok := allowedDashboardScopes[scope]; !ok {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported dashboard scope %q", scope), "invalid_config", nil)
		return "", false
	}
	return scope, true
}

// mapLayoutError translates the store's sentinels for a record addressed by
// its scope rather than by an id.
//
// Three sentinels collapse onto one answer here, deliberately. The client sent
// a revision claim and the store disagreed: because the row sits at another
// revision (conflict), because a concurrent save created the row the client
// said did not exist (duplicate), or because the row the client said existed
// does not (not found). In every case the client's state is stale and the fix
// is the same one -- reload, then save. A 404 would instead say "there is no
// such dashboard", which is false: the scope is served, and the caller may
// have a layout under it a moment later.
func (h *Handler) mapLayoutError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrPreferenceConflict),
		errors.Is(err, store.ErrPreferenceDuplicate),
		errors.Is(err, store.ErrPreferenceNotFound):
		h.writeLayoutConflict(w)
	default:
		h.mapStoreError(w, err, "", h.layoutCeiling())
	}
}

// writeLayoutConflict is the one answer a stale revision claim gets, whichever
// way the claim was wrong.
func (h *Handler) writeLayoutConflict(w http.ResponseWriter) {
	httputil.WriteErrorWithReason(w, http.StatusConflict,
		"this dashboard layout changed since you loaded it; reload before saving",
		"revision_conflict", nil)
}

// layoutExists reports whether a layout is stored under this key right now. It
// is asked only on an error path, to tell a race apart from a real quota.
func (h *Handler) layoutExists(ctx context.Context, ownerID, clusterID, dedup string) bool {
	_, err := h.Store.GetByDedupKey(ctx, ownerID, store.PreferenceKindDashboardLayout, clusterID, dedup)
	return err == nil
}

// withholdUnauthorized re-authorizes the namespaces a stored layout names and
// removes the placements the caller may no longer see.
//
// A stored parameter is evidence of what the caller could see when they saved
// it, never of what they may see now. Access to a namespace can be revoked
// between the save and the read, and a layout that went on naming it would be
// the one surface in the product where a revocation did not take effect
// (spec 7).
//
// The check goes through CanAccessGroupResource and never CanAccess, which
// short-circuits to allow whenever a predicate fake is installed
// (access.go:111) -- a re-authorization written against it would pass its own
// tests while authorizing nothing. "list pods in this namespace" is the same
// question the log and topology surfaces ask of the same value.
//
// The ordinary path costs nothing: every widget shipped today is
// parameterless, so a layout naming no namespace at all is returned byte for
// byte as stored, with no re-marshal and no SelfSubjectAccessReview.
func (h *Handler) withholdUnauthorized(ctx context.Context, user *auth.User,
	clusterID string, config json.RawMessage,
) (json.RawMessage, []string, error) {
	var cfg DashboardLayoutConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		// Every row was written through ValidateDashboardLayout, so this is
		// not a shape the validator accepted. Failing is the only safe answer:
		// a config that cannot be read is one whose namespaces cannot be
		// checked, and serving it unchecked is the thing this exists to stop.
		return nil, nil, fmt.Errorf("stored layout is not a layout config: %w", err)
	}

	kept := make([]DashboardLayoutItem, 0, len(cfg.Items))
	var withheld []string
	for _, it := range cfg.Items {
		ns := it.Params[paramKeyNamespace]
		if ns == "" {
			kept = append(kept, it)
			continue
		}
		allowed, err := h.canSeeNamespace(ctx, user, clusterID, ns)
		if err != nil {
			return nil, nil, err
		}
		if !allowed {
			withheld = append(withheld, it.InstanceID)
			continue
		}
		kept = append(kept, it)
	}
	if len(withheld) == 0 {
		return config, nil, nil
	}

	cfg.Items = kept
	filtered, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("re-marshalling a filtered layout: %w", err)
	}
	return filtered, withheld, nil
}

// canSeeNamespace answers whether the caller may still list workloads in ns.
func (h *Handler) canSeeNamespace(ctx context.Context, user *auth.User,
	clusterID, namespace string,
) (bool, error) {
	if h.AccessChecker == nil {
		// No checker means no way to check, and "could not check" is not
		// "allowed". The placement is withheld and the response names it.
		return false, nil
	}
	return h.AccessChecker.CanAccessGroupResource(ctx, clusterID,
		user.KubernetesUsername, user.KubernetesGroups, "list", "", "pods", namespace)
}

// ---------------------------------------------------------------------------
// Shared internals
// ---------------------------------------------------------------------------

// begin runs the preconditions every endpoint shares: an authenticated
// caller, a configured database, and an identity that fits the column.
func (h *Handler) begin(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return nil, false
	}
	if !h.requireStore(w) {
		return nil, false
	}
	if err := ValidateOwnerID(user.ID); err != nil {
		// Server-side configuration problem, not a client mistake: the mapped
		// identity is longer than the column. Log it so an operator can see
		// which identity provider produced it.
		h.logger().Warn("preferences: authenticated identity exceeds the owner column",
			"identity_length", len(user.ID), "provider", user.Provider)
		h.writeValidationError(w, err)
		return nil, false
	}
	return user, true
}

// requireStore writes 503 and returns false when no database is configured.
func (h *Handler) requireStore(w http.ResponseWriter) bool {
	if h.Store == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
			"preferences require a database", "database_unavailable", nil)
		return false
	}
	return true
}

// list is the shared body of the two GET endpoints.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, kind store.PreferenceKind) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}

	records, err := h.Store.List(r.Context(), user.ID, kind)
	if err != nil {
		h.logger().Error("preferences: list failed", "kind", kind, "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list preferences", "")
		return
	}
	if records == nil {
		// An owner with no records gets [], never null: a client iterating the
		// response should not have to special-case the empty case.
		records = []store.PreferenceRecord{}
	}

	httputil.WriteJSON(w, http.StatusOK, api.Response{
		Data:     records,
		Metadata: &api.Metadata{Total: len(records)},
	})
}

// delete is the shared body of the two DELETE endpoints.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request,
	kind store.PreferenceKind, kindLabel string,
) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	id, ok := h.recordID(w, r)
	if !ok {
		return
	}
	if !h.requireKind(w, r, user.ID, id, kind) {
		return
	}

	if err := h.Store.Delete(r.Context(), user.ID, id); err != nil {
		h.audit(r, user, audit.ActionDelete, kindLabel, "", audit.ResultFailure, id.String())
		h.mapStoreError(w, err, "", 0)
		return
	}

	h.audit(r, user, audit.ActionDelete, kindLabel, "", audit.ResultSuccess, id.String())
	w.WriteHeader(http.StatusNoContent)
}

// recordID parses the {id} path parameter.
//
// A malformed uuid answers 404 rather than 400, so probing for records by id
// yields one indistinguishable response whether the id is nonsense, belongs to
// another user, or never existed.
func (h *Handler) recordID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "preference not found", "")
		return uuid.Nil, false
	}
	return id, true
}

// requireKind confirms the record is the kind the route serves.
//
// Saved views and pins share one id space, and the store scopes its update and
// delete by owner and id only. Without this check a caller could send a pin's
// id to a saved-view route and overwrite that pin's config while the row still
// said it was a pin, or delete it through the wrong endpoint and have the
// audit record name the wrong kind. A wrong-kind id answers exactly as a
// missing one does: the caller learns only that they have no such record.
func (h *Handler) requireKind(w http.ResponseWriter, r *http.Request,
	ownerID string, id uuid.UUID, want store.PreferenceKind,
) bool {
	rec, err := h.Store.Get(r.Context(), ownerID, id)
	if err != nil {
		h.mapStoreError(w, err, "", 0)
		return false
	}
	if rec.Kind != want {
		httputil.WriteError(w, http.StatusNotFound, "preference not found", "")
		return false
	}
	return true
}

// decodeBody caps the request body and rejects unknown fields.
//
// DisallowUnknownFields is what makes the server-derived fields unspoofable at
// this layer: a body carrying ownerId or clusterId does not silently lose them,
// it is refused with the field named, so a client built against the wrong
// contract learns immediately instead of believing it chose the cluster.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", maxBodyBytes), "")
			return false
		}
		httputil.WriteErrorWithReason(w, http.StatusBadRequest,
			"request body is not valid", "invalid_config", nil)
		return false
	}
	return true
}

// validName applies the record-label rules and writes the error when they fail.
func (h *Handler) validName(w http.ResponseWriter, name string) bool {
	if err := ValidateRecordName(name); err != nil {
		h.writeValidationError(w, err)
		return false
	}
	return true
}

// writeValidationError puts a ValidationError on the wire with its reason code.
func (h *Handler) writeValidationError(w http.ResponseWriter, err error) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		status := http.StatusBadRequest
		if ve.Reason == "identity_too_long" {
			// The caller cannot fix their own identity length; this is the
			// server's configuration being incompatible with the feature.
			status = http.StatusUnprocessableEntity
		}
		httputil.WriteErrorWithReason(w, status, ve.Message, ve.Reason, nil)
		return
	}
	httputil.WriteError(w, http.StatusBadRequest, "request is not valid", "")
}

// mapStoreError translates the store's sentinels onto the wire contract.
// dupReason distinguishes a duplicate saved-view name from an already-pinned
// resource; limit carries the ceiling into the 409 so a client can show it.
func (h *Handler) mapStoreError(w http.ResponseWriter, err error, dupReason string, limit int) {
	switch {
	case errors.Is(err, store.ErrPreferenceNotFound):
		httputil.WriteError(w, http.StatusNotFound, "preference not found", "")
	case errors.Is(err, store.ErrPreferenceConflict):
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"this record changed since you loaded it", "revision_conflict", nil)
	case errors.Is(err, store.ErrPreferenceDuplicate):
		reason := dupReason
		if reason == "" {
			reason = "duplicate_name"
		}
		message := "a saved view with this name already exists"
		if reason == "already_pinned" {
			message = "this resource is already pinned"
		}
		httputil.WriteErrorWithReason(w, http.StatusConflict, message, reason, nil)
	case errors.Is(err, store.ErrPreferenceLimit):
		httputil.WriteErrorWithReason(w, http.StatusConflict,
			"you have reached the maximum number of saved preferences", "limit_reached",
			map[string]any{"limit": limit})
	default:
		h.logger().Error("preferences: store operation failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to store preference", "")
	}
}

// audit records a preference write.
//
// detail carries the record id only. Config contents never enter the audit
// log: a saved view's search string is the user's own text and a pin names a
// resource they may not want mirrored into a second, longer-lived store.
func (h *Handler) audit(r *http.Request, user *auth.User, action audit.Action,
	kindLabel, name string, result audit.Result, recordID string,
) {
	if h.AuditLogger == nil {
		return
	}
	entry := audit.Entry{
		Timestamp:    time.Now().UTC(),
		ClusterID:    middleware.ClusterIDFromContext(r.Context()),
		User:         user.Username,
		SourceIP:     r.RemoteAddr,
		Action:       action,
		ResourceKind: kindLabel,
		ResourceName: name,
		Result:       result,
	}
	if recordID != "" {
		entry.Detail = kindLabel + " " + recordID
	}
	if err := h.AuditLogger.Log(r.Context(), entry); err != nil {
		h.logger().Warn("preferences: audit log failed", "error", err)
	}
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
