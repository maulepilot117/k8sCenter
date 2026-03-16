package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
)

const kindResourceQuota = "resourcequotas"

func (h *Handler) HandleListResourceQuotas(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.ResourceQuota
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindResourceQuota, params.Namespace) {
			return
		}
		all, err = h.Informers.ResourceQuotas().ResourceQuotas(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindResourceQuota, "") {
			return
		}
		all, err = h.Informers.ResourceQuotas().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "ResourceQuota", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetResourceQuota(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindResourceQuota, ns) {
		return
	}
	obj, err := h.Informers.ResourceQuotas().ResourceQuotas(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ResourceQuota", ns, name)
		return
	}
	writeData(w, obj)
}
