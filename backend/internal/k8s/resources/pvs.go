package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

const kindPV = "persistentvolumes"

// HandleListPVs handles GET /api/v1/resources/pvs
func (h *Handler) HandleListPVs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindPV, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.PersistentVolumes().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "PersistentVolume", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

// HandleGetPV handles GET /api/v1/resources/pvs/:name
func (h *Handler) HandleGetPV(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPV, "") {
		return
	}
	obj, err := h.Informers.PersistentVolumes().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "PersistentVolume", "", name)
		return
	}
	writeData(w, obj)
}
