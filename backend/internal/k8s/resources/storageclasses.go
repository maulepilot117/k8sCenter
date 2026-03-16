package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

const kindStorageClass = "storageclasses"

// HandleListStorageClasses handles GET /api/v1/resources/storageclasses
func (h *Handler) HandleListStorageClasses(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindStorageClass, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.StorageClasses().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "StorageClass", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

// HandleGetStorageClass handles GET /api/v1/resources/storageclasses/:name
func (h *Handler) HandleGetStorageClass(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindStorageClass, "") {
		return
	}
	obj, err := h.Informers.StorageClasses().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "StorageClass", "", name)
		return
	}
	writeData(w, obj)
}
