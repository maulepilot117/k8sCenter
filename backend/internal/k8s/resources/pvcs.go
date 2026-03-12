package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindPVC = "persistentvolumeclaims"

func (h *Handler) HandleListPVCs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*corev1.PersistentVolumeClaim
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindPVC, params.Namespace) {
			return
		}
		all, err = h.Informers.PersistentVolumeClaims().PersistentVolumeClaims(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		if !h.checkAccess(w, r, user, "list", kindPVC, "") {
			return
		}
		all, err = h.Informers.PersistentVolumeClaims().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "PersistentVolumeClaim", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetPVC(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPVC, ns) {
		return
	}
	obj, err := h.Informers.PersistentVolumeClaims().PersistentVolumeClaims(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "PersistentVolumeClaim", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreatePVC(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindPVC, ns) {
		return
	}
	var obj corev1.PersistentVolumeClaim
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
	created, err := cs.CoreV1().PersistentVolumeClaims(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "PersistentVolumeClaim", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "PersistentVolumeClaim", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "PersistentVolumeClaim", ns, created.Name, audit.ResultSuccess)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) HandleDeletePVC(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindPVC, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().PersistentVolumeClaims(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "PersistentVolumeClaim", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "PersistentVolumeClaim", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "PersistentVolumeClaim", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
