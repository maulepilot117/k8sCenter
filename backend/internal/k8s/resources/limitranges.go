package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
)

const kindLimitRange = "limitranges"

func (h *Handler) HandleListLimitRanges(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.LimitRange
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindLimitRange, params.Namespace) {
			return
		}
		all, err = h.Informers.LimitRanges().LimitRanges(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindLimitRange, "") {
			return
		}
		all, err = h.Informers.LimitRanges().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "LimitRange", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetLimitRange(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindLimitRange, ns) {
		return
	}
	obj, err := h.Informers.LimitRanges().LimitRanges(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "LimitRange", ns, name)
		return
	}
	writeData(w, obj)
}
