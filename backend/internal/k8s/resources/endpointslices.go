package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	discoveryv1 "k8s.io/api/discovery/v1"
)

const kindEndpointSlice = "endpointslices"

func (h *Handler) HandleListEndpointSlices(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*discoveryv1.EndpointSlice
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindEndpointSlice, params.Namespace) {
			return
		}
		all, err = h.Informers.EndpointSlices().EndpointSlices(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindEndpointSlice, "") {
			return
		}
		all, err = h.Informers.EndpointSlices().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "EndpointSlice", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetEndpointSlice(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindEndpointSlice, ns) {
		return
	}
	obj, err := h.Informers.EndpointSlices().EndpointSlices(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "EndpointSlice", ns, name)
		return
	}
	writeData(w, obj)
}
