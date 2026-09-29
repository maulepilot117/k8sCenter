package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/pkg/api"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	kindNode            = "nodes"
	defaultDrainTimeout = 5 * time.Minute
)

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
	cs, err := h.impersonatingClient(r, user)
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
	if req.Timeout <= 0 {
		req.Timeout = defaultDrainTimeout
	}

	if req.Timeout > 30*time.Minute {
		req.Timeout = 30 * time.Minute
	}

	clusterID := middleware.ClusterIDFromContext(r.Context())
	taskID, created := h.TaskManager.CreateIfNoActive("drain", clusterID, name, "", user.Username)
	if !created {
		writeError(w, http.StatusConflict, "drain already in progress for node "+name, "")
		return
	}

	// The drain outlives the request: net/http cancels r.Context() as soon as
	// the 202 is written. It keeps the request's values but not its
	// cancellation, and stops at the drain timeout or when the TaskManager
	// cancels it (task finished, or its cluster was evicted). The cancel is
	// registered before client resolution so an eviction that lands while the
	// client is being built still stops the drain.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), req.Timeout)
	h.TaskManager.SetCancel(taskID, cancel)

	// Resolve the client on the request path, while the request's cluster
	// context is still authoritative, so a resolution failure is a 500 here
	// rather than a task that fails later.
	cs, err := h.clientForCluster(r.Context(), clusterID, user)
	if err != nil {
		h.Logger.Error("drain: failed to create client", "node", name, "clusterID", clusterID, "error", err)
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to create client", 0)
		writeError(w, http.StatusInternalServerError, "failed to create client", "")
		return
	}

	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "starting drain", 0)
	h.auditWrite(r, user, audit.ActionUpdate, "Node", "", name, audit.ResultSuccess)

	go func() {
		recoverutil.Safe(h.Logger, "resources node drain", func() {
			h.executeDrain(ctx, taskID, name, req, cs)
		})
		// Safety net, outside the recovered closure: any exit that left the
		// task non-terminal (a panic) is marked failed. UpdateStatus is a
		// no-op for a task that already finished.
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "drain stopped unexpectedly", 0)
	}()

	writeJSON(w, http.StatusAccepted, api.Response{
		Data: map[string]string{
			"taskID":  taskID,
			"message": "drain started",
		},
	})
}

// clientForCluster resolves the impersonating clientset for the cluster the
// request targets. Both branches end in ClusterRouter.ClientForCluster; the
// split only keeps the remoteClient test override (see remoteClientFor) off
// the local path.
func (h *Handler) clientForCluster(ctx context.Context, clusterID string, user *auth.User) (kubernetes.Interface, error) {
	if k8s.IsLocalClusterID(clusterID) {
		return h.ClusterRouter.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	}
	return h.remoteClientFor(ctx, clusterID, user)
}

// executeDrain cordons nodeName and evicts its pods through cs, recording
// progress on the task. It stops before the next eviction once ctx ends.
func (h *Handler) executeDrain(ctx context.Context, taskID, nodeName string, req DrainRequest, cs kubernetes.Interface) {
	if h.drainStopped(ctx, taskID, 0) {
		return
	}

	// Step 1: Cordon the node
	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "cordoning node", 10)
	patchData := `{"spec":{"unschedulable":true}}`
	_, err := cs.CoreV1().Nodes().Patch(ctx, nodeName, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.Logger.Error("drain: failed to cordon node", "node", nodeName, "error", err)
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to cordon node", 10)
		return
	}

	// Step 2: List pods on the node
	h.TaskManager.UpdateStatus(taskID, TaskStatusRunning, "listing pods on node", 20)
	podList, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})
	if err != nil {
		h.Logger.Error("drain: failed to list pods", "node", nodeName, "error", err)
		h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, "failed to list pods", 20)
		return
	}

	// Step 3: Evict pods one by one
	podsToEvict := filterPodsForDrain(podList.Items, req.IgnoreDaemonSets)
	total := len(podsToEvict)

	for i, pod := range podsToEvict {
		progress := 30 + (70 * (i + 1) / max(total, 1))
		if h.drainStopped(ctx, taskID, progress) {
			return
		}
		h.TaskManager.UpdateStatus(taskID, TaskStatusRunning,
			fmt.Sprintf("evicting pod %s/%s (%d/%d)", pod.Namespace, pod.Name, i+1, total),
			progress,
		)

		if err := evictPod(ctx, cs, &pod); err != nil {
			h.Logger.Error("drain: failed to evict pod", "node", nodeName, "pod", pod.Namespace+"/"+pod.Name, "error", err)
			h.TaskManager.UpdateStatus(taskID, TaskStatusFailed,
				fmt.Sprintf("failed to evict pod %s/%s", pod.Namespace, pod.Name),
				progress,
			)
			return
		}
	}

	h.TaskManager.UpdateStatus(taskID, TaskStatusComplete,
		fmt.Sprintf("drain complete — %d pods evicted", total), 100,
	)
}

// drainStopped reports whether ctx has ended, marking the task failed if so.
// A task its cluster eviction already failed keeps that outcome.
func (h *Handler) drainStopped(ctx context.Context, taskID string, progress int) bool {
	if ctx.Err() == nil {
		return false
	}
	msg := "drain cancelled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg = "drain timed out"
	}
	h.TaskManager.UpdateStatus(taskID, TaskStatusFailed, msg, progress)
	return true
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

func evictPod(ctx context.Context, cs kubernetes.Interface, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	return cs.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction)
}
