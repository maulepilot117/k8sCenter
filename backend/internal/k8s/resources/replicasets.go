package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
)

const kindReplicaSet = "replicasets"

// HandleListReplicaSets handles GET /api/v1/resources/replicasets[/:namespace]
func (h *Handler) HandleListReplicaSets(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*appsv1.ReplicaSet
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindReplicaSet, params.Namespace) {
			return
		}
		all, err = h.Informers.ReplicaSets().ReplicaSets(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindReplicaSet, "") {
			return
		}
		all, err = h.Informers.ReplicaSets().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "ReplicaSet", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

// HandleGetReplicaSet handles GET /api/v1/resources/replicasets/:namespace/:name
func (h *Handler) HandleGetReplicaSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindReplicaSet, ns) {
		return
	}
	obj, err := h.Informers.ReplicaSets().ReplicaSets(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ReplicaSet", ns, name)
		return
	}
	writeData(w, obj)
}
