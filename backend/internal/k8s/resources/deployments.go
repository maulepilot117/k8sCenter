package resources

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

const kindDeployment = "deployments"

// HandleListDeployments handles GET /api/v1/resources/deployments[/:namespace]
func (h *Handler) HandleListDeployments(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var allDeps []*appsv1.Deployment
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindDeployment, params.Namespace) {
			return
		}
		allDeps, err = h.Informers.Deployments().Deployments(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		allDeps, err = h.Informers.Deployments().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "Deployment", params.Namespace, "")
		return
	}

	items, continueToken := paginate(allDeps, params.Limit, params.Continue)
	writeList(w, items, len(allDeps), continueToken)
}

// HandleGetDeployment handles GET /api/v1/resources/deployments/:namespace/:name
func (h *Handler) HandleGetDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.checkAccess(w, r, user, "get", kindDeployment, ns) {
		return
	}

	dep, err := h.Informers.Deployments().Deployments(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Deployment", ns, name)
		return
	}
	writeData(w, dep)
}

// HandleCreateDeployment handles POST /api/v1/resources/deployments/:namespace
func (h *Handler) HandleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindDeployment, ns) {
		return
	}

	var dep appsv1.Deployment
	if err := decodeBody(r, &dep); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	dep.Namespace = ns

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	created, err := cs.AppsV1().Deployments(ns).Create(r.Context(), &dep, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Deployment", ns, dep.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Deployment", ns, dep.Name)
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, "Deployment", ns, created.Name, audit.ResultSuccess)
	writeJSON(w, http.StatusCreated, created)
}

// HandleUpdateDeployment handles PUT /api/v1/resources/deployments/:namespace/:name
func (h *Handler) HandleUpdateDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindDeployment, ns) {
		return
	}

	var dep appsv1.Deployment
	if err := decodeBody(r, &dep); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	dep.Namespace = ns
	dep.Name = name

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	updated, err := cs.AppsV1().Deployments(ns).Update(r.Context(), &dep, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "Deployment", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

// HandleDeleteDeployment handles DELETE /api/v1/resources/deployments/:namespace/:name
func (h *Handler) HandleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindDeployment, ns) {
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	if err := cs.AppsV1().Deployments(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Deployment", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Deployment", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionDelete, "Deployment", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// HandleScaleDeployment handles POST /api/v1/resources/deployments/:namespace/:name/scale
func (h *Handler) HandleScaleDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindDeployment, ns) {
		return
	}

	var req struct {
		Replicas int32 `json:"replicas"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
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

	result, err := cs.AppsV1().Deployments(ns).UpdateScale(r.Context(), name, scale, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "scale", "Deployment", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleRollbackDeployment handles POST /api/v1/resources/deployments/:namespace/:name/rollback
func (h *Handler) HandleRollbackDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindDeployment, ns) {
		return
	}

	var req struct {
		Revision int64 `json:"revision"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	// Get the target ReplicaSet for the given revision
	rsList, err := cs.AppsV1().ReplicaSets(ns).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		mapK8sError(w, err, "list", "ReplicaSet", ns, "")
		return
	}

	var targetRS *appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if rs.Annotations["deployment.kubernetes.io/revision"] == fmt.Sprintf("%d", req.Revision) {
			// Verify this RS belongs to the deployment
			for _, ownerRef := range rs.OwnerReferences {
				if ownerRef.Kind == "Deployment" && ownerRef.Name == name {
					targetRS = rs
					break
				}
			}
		}
		if targetRS != nil {
			break
		}
	}

	if targetRS == nil {
		writeError(w, http.StatusNotFound, "revision not found", fmt.Sprintf("revision %d not found for deployment %s", req.Revision, name))
		return
	}

	// Patch the deployment with the target RS's template
	templateBytes, err := json.Marshal(targetRS.Spec.Template)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal template", err.Error())
		return
	}

	patchData := fmt.Sprintf(`{"spec":{"template":%s}}`, string(templateBytes))
	result, err := cs.AppsV1().Deployments(ns).Patch(r.Context(), name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "rollback", "Deployment", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleRestartDeployment handles POST /api/v1/resources/deployments/:namespace/:name/restart
func (h *Handler) HandleRestartDeployment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindDeployment, ns) {
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	// Rolling restart by patching the pod template annotation
	patchData := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, timeNow().Format("2006-01-02T15:04:05Z"))
	result, err := cs.AppsV1().Deployments(ns).Patch(r.Context(), name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "restart", "Deployment", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "Deployment", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// parseSelector converts a label selector string to a labels.Selector.
func parseSelector(s string) labels.Selector {
	if s == "" {
		return labels.Everything()
	}
	sel, err := labels.Parse(s)
	if err != nil {
		return labels.Everything()
	}
	return sel
}

// paginate implements simple offset-based pagination using a continue token
// that represents the starting index. Returns the page of items and the next
// continue token (empty if no more items).
func paginate[T any](items []*T, limit int, continueToken string) ([]*T, string) {
	start := 0
	if continueToken != "" {
		fmt.Sscanf(continueToken, "%d", &start)
	}

	if start >= len(items) {
		return []*T{}, ""
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	var nextToken string
	if end < len(items) {
		nextToken = fmt.Sprintf("%d", end)
	}

	return items[start:end], nextToken
}
