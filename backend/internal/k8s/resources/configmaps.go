package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindConfigMap = "configmaps"

func (h *Handler) HandleListConfigMaps(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.ConfigMap
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindConfigMap, params.Namespace) {
			return
		}
		all, err = h.Informers.ConfigMaps().ConfigMaps(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindConfigMap, "") {
			return
		}
		all, err = h.Informers.ConfigMaps().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "ConfigMap", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetConfigMap(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindConfigMap, ns) {
		return
	}
	obj, err := h.Informers.ConfigMaps().ConfigMaps(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ConfigMap", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateConfigMap(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindConfigMap, ns) {
		return
	}
	var obj corev1.ConfigMap
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.CoreV1().ConfigMaps(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "ConfigMap", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "ConfigMap", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "ConfigMap", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleUpdateConfigMap(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindConfigMap, ns) {
		return
	}
	var obj corev1.ConfigMap
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	obj.Name = name
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	updated, err := cs.CoreV1().ConfigMaps(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "ConfigMap", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "ConfigMap", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "ConfigMap", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteConfigMap(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindConfigMap, ns) {
		return
	}
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().ConfigMaps(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "ConfigMap", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "ConfigMap", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "ConfigMap", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
