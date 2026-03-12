package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindNetworkPolicy = "networkpolicies"

func (h *Handler) HandleListNetworkPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*networkingv1.NetworkPolicy
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindNetworkPolicy, params.Namespace) {
			return
		}
		all, err = h.Informers.NetworkPolicies().NetworkPolicies(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		all, err = h.Informers.NetworkPolicies().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "NetworkPolicy", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindNetworkPolicy, ns) {
		return
	}
	obj, err := h.Informers.NetworkPolicies().NetworkPolicies(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "NetworkPolicy", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindNetworkPolicy, ns) {
		return
	}
	var obj networkingv1.NetworkPolicy
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
	created, err := cs.NetworkingV1().NetworkPolicies(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "NetworkPolicy", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "NetworkPolicy", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "NetworkPolicy", ns, created.Name, audit.ResultSuccess)
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) HandleUpdateNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindNetworkPolicy, ns) {
		return
	}
	var obj networkingv1.NetworkPolicy
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
	updated, err := cs.NetworkingV1().NetworkPolicies(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "NetworkPolicy", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "NetworkPolicy", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "NetworkPolicy", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteNetworkPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindNetworkPolicy, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.NetworkingV1().NetworkPolicies(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "NetworkPolicy", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "NetworkPolicy", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "NetworkPolicy", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
