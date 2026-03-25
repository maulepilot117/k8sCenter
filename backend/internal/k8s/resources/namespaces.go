package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindNamespace = "namespaces"

func (h *Handler) HandleListNamespaces(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindNamespace, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.Namespaces().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "Namespace", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetNamespace(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindNamespace, "") {
		return
	}
	obj, err := h.Informers.Namespaces().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Namespace", "", name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateNamespace(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.checkAccess(w, r, user, "create", kindNamespace, "") {
		return
	}
	var obj corev1.Namespace
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.CoreV1().Namespaces().Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Namespace", "", obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Namespace", "", obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "Namespace", "", created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleDeleteNamespace(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindNamespace, "") {
		return
	}
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Namespaces().Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Namespace", "", name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Namespace", "", name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Namespace", "", name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
