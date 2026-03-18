package resources

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const kindStatefulSet = "statefulsets"

func (h *Handler) HandleListStatefulSets(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*appsv1.StatefulSet
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindStatefulSet, params.Namespace) {
			return
		}
		all, err = h.Informers.StatefulSets().StatefulSets(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindStatefulSet, "") {
			return
		}
		all, err = h.Informers.StatefulSets().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "StatefulSet", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetStatefulSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindStatefulSet, ns) {
		return
	}
	obj, err := h.Informers.StatefulSets().StatefulSets(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "StatefulSet", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateStatefulSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindStatefulSet, ns) {
		return
	}
	var obj appsv1.StatefulSet
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
	created, err := cs.AppsV1().StatefulSets(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "StatefulSet", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "StatefulSet", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "StatefulSet", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleUpdateStatefulSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindStatefulSet, ns) {
		return
	}
	var obj appsv1.StatefulSet
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
	updated, err := cs.AppsV1().StatefulSets(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "StatefulSet", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "StatefulSet", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "StatefulSet", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteStatefulSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindStatefulSet, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.AppsV1().StatefulSets(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "StatefulSet", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "StatefulSet", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "StatefulSet", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleScaleStatefulSet(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindStatefulSet, ns) {
		return
	}
	var req struct {
		Replicas int32 `json:"replicas"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Replicas < 0 || req.Replicas > 1000 {
		writeError(w, http.StatusBadRequest, "replicas must be between 0 and 1000", "")
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	scale := &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       autoscalingv1.ScaleSpec{Replicas: req.Replicas},
	}
	result, err := cs.AppsV1().StatefulSets(ns).UpdateScale(r.Context(), name, scale, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "StatefulSet", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "scale", "StatefulSet", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "StatefulSet", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleRestartStatefulSet handles POST /api/v1/resources/statefulsets/:namespace/:name/restart
func (h *Handler) HandleRestartStatefulSet(w http.ResponseWriter, r *http.Request) {
	h.restartWorkload(w, r, kindStatefulSet, "StatefulSet", func(cs kubernetes.Interface, ctx context.Context, ns, name string) (any, error) {
		return cs.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, restartPatch(), metav1.PatchOptions{})
	})
}
