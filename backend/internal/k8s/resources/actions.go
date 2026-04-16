package resources

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
)

// ---------------------------------------------------------------------------
// Generic action handlers — dispatch to capability interfaces on adapters.
// ---------------------------------------------------------------------------

// scaleRequest is the JSON body for HandleScaleResource.
type scaleRequest struct {
	Replicas int32 `json:"replicas"`
}

// HandleScaleResource handles POST /api/v1/resources/{kind}/{namespace}/{name}/scale
func (h *Handler) HandleScaleResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	scalable, ok := adapter.(Scalable)
	if !ok {
		writeError(w, http.StatusBadRequest, adapter.DisplayName()+" does not support scaling", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	var req scaleRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid scale request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := scalable.Scale(cs, ns, name, req.Replicas)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "scale", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleRestartResource handles POST /api/v1/resources/{kind}/{namespace}/{name}/restart
func (h *Handler) HandleRestartResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	restartable, ok := adapter.(Restartable)
	if !ok {
		writeError(w, http.StatusBadRequest, adapter.DisplayName()+" does not support restart", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := restartable.Restart(cs, ns, name)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "restart", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// suspendRequest is the JSON body for HandleSuspendResource.
type suspendRequest struct {
	Suspend bool `json:"suspend"`
}

// HandleSuspendResource handles POST /api/v1/resources/{kind}/{namespace}/{name}/suspend
func (h *Handler) HandleSuspendResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	suspendable, ok := adapter.(Suspendable)
	if !ok {
		writeError(w, http.StatusBadRequest, adapter.DisplayName()+" does not support suspend", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	var req suspendRequest
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid suspend request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := suspendable.Suspend(cs, ns, name, req.Suspend)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "suspend", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleTriggerResource handles POST /api/v1/resources/{kind}/{namespace}/{name}/trigger
func (h *Handler) HandleTriggerResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	triggerable, ok := adapter.(Triggerable)
	if !ok {
		writeError(w, http.StatusBadRequest, adapter.DisplayName()+" does not support triggering", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "create", adapter.APIResource(), ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := triggerable.Trigger(cs, ns, name)
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "trigger", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeCreated(w, result)
}

// rollbackRequest is the JSON body for HandleRollbackResource.
type rollbackRequest struct {
	Revision int64 `json:"revision"`
}

// HandleRollbackResource handles POST /api/v1/resources/{kind}/{namespace}/{name}/rollback
func (h *Handler) HandleRollbackResource(w http.ResponseWriter, r *http.Request) {
	adapter, ok := resolveAdapter(w, r)
	if !ok {
		return
	}

	rollbackable, ok := adapter.(Rollbackable)
	if !ok {
		writeError(w, http.StatusBadRequest, adapter.DisplayName()+" does not support rollback", "")
		return
	}

	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "update", adapter.APIResource(), ns) {
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		return
	}
	var req rollbackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid rollback request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	result, err := rollbackable.Rollback(cs, ns, name, req.Revision)
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultFailure)
		mapK8sError(w, err, "rollback", adapter.DisplayName(), ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, adapter.DisplayName(), ns, name, audit.ResultSuccess)
	writeData(w, result)
}
