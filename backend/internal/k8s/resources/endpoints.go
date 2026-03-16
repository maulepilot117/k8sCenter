package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
)

const kindEndpoints = "endpoints"

// HandleListEndpoints handles GET /api/v1/resources/endpoints[/:namespace]
func (h *Handler) HandleListEndpoints(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.Endpoints
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindEndpoints, params.Namespace) {
			return
		}
		all, err = h.Informers.Endpoints().Endpoints(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindEndpoints, "") {
			return
		}
		all, err = h.Informers.Endpoints().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "Endpoints", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

// HandleGetEndpoints handles GET /api/v1/resources/endpoints/:namespace/:name
func (h *Handler) HandleGetEndpoints(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindEndpoints, ns) {
		return
	}
	obj, err := h.Informers.Endpoints().Endpoints(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Endpoints", ns, name)
		return
	}
	writeData(w, obj)
}
