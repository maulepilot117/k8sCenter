package preferences

import (
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
type Handler struct {
	Store       *store.PreferenceStore
	AuditLogger audit.Logger
	Logger      *slog.Logger
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
	created, err := h.Store.Create(r.Context(), rec, MaxSavedViewsPerUser)
	if err != nil {
		h.audit(r, user, audit.ActionCreate, "savedView", req.Name, audit.ResultFailure, "")
		h.mapStoreError(w, err, "duplicate_name", MaxSavedViewsPerUser)
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
		h.mapStoreError(w, err, "duplicate_name", MaxSavedViewsPerUser)
		return
	}

	h.audit(r, user, audit.ActionUpdate, "savedView", updated.Name, audit.ResultSuccess, updated.ID.String())
	httputil.WriteJSON(w, http.StatusOK, api.Response{Data: updated})
}

// HandleDeleteView removes a saved view the caller owns.
func (h *Handler) HandleDeleteView(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, "savedView")
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
	created, err := h.Store.Create(r.Context(), rec, MaxPinsPerUser)
	if err != nil {
		h.audit(r, user, audit.ActionCreate, "pin", req.Name, audit.ResultFailure, "")
		h.mapStoreError(w, err, "already_pinned", MaxPinsPerUser)
		return
	}

	h.audit(r, user, audit.ActionCreate, "pin", created.Name, audit.ResultSuccess, created.ID.String())
	httputil.WriteJSON(w, http.StatusCreated, api.Response{Data: created})
}

// HandleDeletePin unpins a resource for the caller.
func (h *Handler) HandleDeletePin(w http.ResponseWriter, r *http.Request) {
	h.delete(w, r, "pin")
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
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, kindLabel string) {
	user, ok := h.begin(w, r)
	if !ok {
		return
	}
	id, ok := h.recordID(w, r)
	if !ok {
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
