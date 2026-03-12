package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindService = "services"

func (h *Handler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*corev1.Service
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindService, params.Namespace) {
			return
		}
		all, err = h.Informers.Services().Services(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		all, err = h.Informers.Services().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "Service", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetService(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindService, ns) {
		return
	}
	obj, err := h.Informers.Services().Services(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Service", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateService(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindService, ns) {
		return
	}
	var obj corev1.Service
	if err := decodeBody(r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.CoreV1().Services(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Service", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Service", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "Service", ns, created.Name, audit.ResultSuccess)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) HandleUpdateService(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindService, ns) {
		return
	}
	var obj corev1.Service
	if err := decodeBody(r, &obj); err != nil {
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
	updated, err := cs.CoreV1().Services(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Service", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "Service", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "Service", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteService(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindService, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Services(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Service", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Service", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Service", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
