package resources

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	kindNode             = "nodes"
	defaultDrainTimeout  = 5 * time.Minute
)

func (h *Handler) HandleListNodes(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindNode, "") {
		return
	}
	all, err := h.Informers.Nodes().List(parseSelector(params.LabelSelector))
	if err != nil {
		mapK8sError(w, err, "list", "Node", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetNode(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindNode, "") {
		return
	}
	obj, err := h.Informers.Nodes().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Node", "", name)
		return
	}
	writeData(w, obj)
}

// HandleCordonNode handles POST /api/v1/resources/nodes/:name/cordon
func (h *Handler) HandleCordonNode(w http.ResponseWriter, r *http.Request) {
	h.setNodeUnschedulable(w, r, true)
}

// HandleUncordonNode handles POST /api/v1/resources/nodes/:name/uncordon
func (h *Handler) HandleUncordonNode(w http.ResponseWriter, r *http.Request) {
	h.setNodeUnschedulable(w, r, false)
}

func (h *Handler) setNodeUnschedulable(w http.ResponseWriter, r *http.Request, unschedulable bool) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindNode, "") {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	action := "cordon"
	if !unschedulable {
		action = "uncordon"
	}

	patchData := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, unschedulable)
	result, err := cs.CoreV1().Nodes().Patch(r.Context(), name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Node", "", name, audit.ResultFailure)
		mapK8sError(w, err, action, "Node", "", name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "Node", "", name, audit.ResultSuccess)
	writeData(w, result)
}

// DrainRequest is the request body for node drain.
type DrainRequest struct {
	IgnoreDaemonSets   bool          `json:"ignoreDaemonSets"`
	DeleteEmptyDirData bool          `json:"deleteEmptyDirData"`
	Timeout            time.Duration `json:"timeout"`
}

// HandleDrainNode handles POST /api/v1/resources/nodes/:name/drain
// Returns 202 Accepted with a task ID for polling.
func (h *Handler) HandleDrainNode(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindNode, "") {
		return
	}

	var req DrainRequest
	if err := decodeBody(w, r, &req); err != nil {
		// Allow empty body with defaults
		req = DrainRequest{
			IgnoreDaemonSets:   true,
			DeleteEmptyDirData: true,
		}
	}
	if req.Timeout == 0 {
		req.Timeout = defaultDrainTimeout
	}

	taskID := h.TaskManager.Create("drain", name, "", user.Username)
	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "starting drain", 0)

	go h.executeDrain(taskID, name, req, user)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"taskID":  taskID,
		"message": "drain started",
	})
}

func (h *Handler) executeDrain(taskID, nodeName string, req DrainRequest, user *auth.User) {
	ctx, cancel := context.WithTimeout(context.Background(), req.Timeout)
	defer cancel()

	cs, err := h.K8sClient.ClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to create client: "+err.Error(), 0)
		return
	}

	// Step 1: Cordon the node
	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "cordoning node", 10)
	patchData := `{"spec":{"unschedulable":true}}`
	_, err = cs.CoreV1().Nodes().Patch(ctx, nodeName, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to cordon node: "+err.Error(), 10)
		return
	}

	// Step 2: List pods on the node
	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "listing pods on node", 20)
	podList, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})
	if err != nil {
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to list pods: "+err.Error(), 20)
		return
	}

	// Step 3: Evict pods one by one
	podsToEvict := filterPodsForDrain(podList.Items, req.IgnoreDaemonSets)
	total := len(podsToEvict)

	for i, pod := range podsToEvict {
		progress := 30 + (70 * (i + 1) / max(total, 1))
		h.TaskManager.UpdateStatus(taskID, TaskStatusRunning,
			fmt.Sprintf("evicting pod %s/%s (%d/%d)", pod.Namespace, pod.Name, i+1, total),
			progress,
		)

		if err := evictPod(ctx, cs, &pod); err != nil {
			h.TaskManager.UpdateStatus(taskID, TaskStatusFailed,
				fmt.Sprintf("failed to evict pod %s/%s: %s", pod.Namespace, pod.Name, err.Error()),
				progress,
			)
			return
		}
	}

	h.TaskManager.UpdateStatus(taskID, TaskStatusComplete,
		fmt.Sprintf("drain complete — %d pods evicted", total), 100,
	)
}

func filterPodsForDrain(pods []corev1.Pod, ignoreDaemonSets bool) []corev1.Pod {
	var result []corev1.Pod
	for _, pod := range pods {
		// Skip mirror pods (managed by kubelet directly)
		if _, isMirror := pod.Annotations["kubernetes.io/config.mirror"]; isMirror {
			continue
		}
		// Skip DaemonSet pods if requested
		if ignoreDaemonSets && isDaemonSetPod(&pod) {
			continue
		}
		result = append(result, pod)
	}
	return result
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func evictPod(ctx context.Context, cs *kubernetes.Clientset, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	return cs.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction)
}
