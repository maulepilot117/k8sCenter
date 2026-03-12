package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindIngress = "ingresses"

func (h *Handler) HandleListIngresses(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*networkingv1.Ingress
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindIngress, params.Namespace) {
			return
		}
		all, err = h.Informers.Ingresses().Ingresses(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		if !h.checkAccess(w, r, user, "list", kindIngress, "") {
			return
		}
		all, err = h.Informers.Ingresses().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "Ingress", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetIngress(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindIngress, ns) {
		return
	}
	obj, err := h.Informers.Ingresses().Ingresses(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Ingress", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateIngress(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindIngress, ns) {
		return
	}
	var obj networkingv1.Ingress
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
	created, err := cs.NetworkingV1().Ingresses(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Ingress", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Ingress", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "Ingress", ns, created.Name, audit.ResultSuccess)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) HandleUpdateIngress(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindIngress, ns) {
		return
	}
	var obj networkingv1.Ingress
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
	updated, err := cs.NetworkingV1().Ingresses(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Ingress", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "Ingress", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "Ingress", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteIngress(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindIngress, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.NetworkingV1().Ingresses(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Ingress", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Ingress", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Ingress", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
