package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindHPA = "horizontalpodautoscalers"

// HandleListHPAs handles GET /api/v1/resources/hpas[/:namespace]
func (h *Handler) HandleListHPAs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*autoscalingv2.HorizontalPodAutoscaler
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindHPA, params.Namespace) {
			return
		}
		all, err = h.Informers.HorizontalPodAutoscalers().HorizontalPodAutoscalers(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindHPA, "") {
			return
		}
		all, err = h.Informers.HorizontalPodAutoscalers().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "HorizontalPodAutoscaler", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

// HandleGetHPA handles GET /api/v1/resources/hpas/:namespace/:name
func (h *Handler) HandleGetHPA(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindHPA, ns) {
		return
	}
	obj, err := h.Informers.HorizontalPodAutoscalers().HorizontalPodAutoscalers(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "HorizontalPodAutoscaler", ns, name)
		return
	}
	writeData(w, obj)
}

// HandleCreateHPA handles POST /api/v1/resources/hpas/:namespace
func (h *Handler) HandleCreateHPA(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindHPA, ns) {
		return
	}
	var obj autoscalingv2.HorizontalPodAutoscaler
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "HorizontalPodAutoscaler", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "HorizontalPodAutoscaler", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "HorizontalPodAutoscaler", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

// HandleUpdateHPA handles PUT /api/v1/resources/hpas/:namespace/:name
func (h *Handler) HandleUpdateHPA(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindHPA, ns) {
		return
	}
	var obj autoscalingv2.HorizontalPodAutoscaler
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	obj.Name = name
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	updated, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "HorizontalPodAutoscaler", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "HorizontalPodAutoscaler", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "HorizontalPodAutoscaler", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

// HandleDeleteHPA handles DELETE /api/v1/resources/hpas/:namespace/:name
func (h *Handler) HandleDeleteHPA(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindHPA, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "HorizontalPodAutoscaler", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "HorizontalPodAutoscaler", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "HorizontalPodAutoscaler", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
