package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindPDB = "poddisruptionbudgets"

func (h *Handler) HandleListPDBs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*policyv1.PodDisruptionBudget
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindPDB, params.Namespace) {
			return
		}
		all, err = h.Informers.PodDisruptionBudgets().PodDisruptionBudgets(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindPDB, "") {
			return
		}
		all, err = h.Informers.PodDisruptionBudgets().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "PodDisruptionBudget", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetPDB(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPDB, ns) {
		return
	}
	obj, err := h.Informers.PodDisruptionBudgets().PodDisruptionBudgets(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "PodDisruptionBudget", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreatePDB(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindPDB, ns) {
		return
	}
	var obj policyv1.PodDisruptionBudget
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
	created, err := cs.PolicyV1().PodDisruptionBudgets(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "PodDisruptionBudget", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "PodDisruptionBudget", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "PodDisruptionBudget", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleDeletePDB(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindPDB, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.PolicyV1().PodDisruptionBudgets(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "PodDisruptionBudget", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "PodDisruptionBudget", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "PodDisruptionBudget", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
