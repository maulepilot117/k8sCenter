package resources

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const kindDaemonSet = "daemonsets"

func (h *Handler) HandleListDaemonSets(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*appsv1.DaemonSet
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindDaemonSet, params.Namespace) {
			return
		}
		all, err = h.Informers.DaemonSets().DaemonSets(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindDaemonSet, "") {
			return
		}
		all, err = h.Informers.DaemonSets().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "DaemonSet", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetDaemonSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindDaemonSet, ns) {
		return
	}
	obj, err := h.Informers.DaemonSets().DaemonSets(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "DaemonSet", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateDaemonSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindDaemonSet, ns) {
		return
	}
	var obj appsv1.DaemonSet
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
	created, err := cs.AppsV1().DaemonSets(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "DaemonSet", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "DaemonSet", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "DaemonSet", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleUpdateDaemonSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindDaemonSet, ns) {
		return
	}
	var obj appsv1.DaemonSet
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
	updated, err := cs.AppsV1().DaemonSets(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "DaemonSet", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "DaemonSet", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "DaemonSet", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteDaemonSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindDaemonSet, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.AppsV1().DaemonSets(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "DaemonSet", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "DaemonSet", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "DaemonSet", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// HandleRestartDaemonSet handles POST /api/v1/resources/daemonsets/:namespace/:name/restart
func (h *Handler) HandleRestartDaemonSet(w http.ResponseWriter, r *http.Request) {
	h.restartWorkload(w, r, kindDaemonSet, "DaemonSet", func(cs kubernetes.Interface, ctx context.Context, ns, name string) (any, error) {
		return cs.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, restartPatch(), metav1.PatchOptions{})
	})
}
