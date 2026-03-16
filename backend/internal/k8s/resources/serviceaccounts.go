package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
)

const kindServiceAccount = "serviceaccounts"

func (h *Handler) HandleListServiceAccounts(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.ServiceAccount
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindServiceAccount, params.Namespace) {
			return
		}
		all, err = h.Informers.ServiceAccounts().ServiceAccounts(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindServiceAccount, "") {
			return
		}
		all, err = h.Informers.ServiceAccounts().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "ServiceAccount", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetServiceAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindServiceAccount, ns) {
		return
	}
	obj, err := h.Informers.ServiceAccounts().ServiceAccounts(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ServiceAccount", ns, name)
		return
	}
	writeData(w, obj)
}
