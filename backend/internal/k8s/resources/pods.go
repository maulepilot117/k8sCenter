package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindPod = "pods"

func (h *Handler) HandleListPods(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*corev1.Pod
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindPod, params.Namespace) {
			return
		}
		all, err = h.Informers.Pods().Pods(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		if !h.checkAccess(w, r, user, "list", kindPod, "") {
			return
		}
		all, err = h.Informers.Pods().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "Pod", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetPod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPod, ns) {
		return
	}
	obj, err := h.Informers.Pods().Pods(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Pod", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleDeletePod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindPod, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Pods(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Pod", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
