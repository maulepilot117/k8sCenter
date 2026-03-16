package resources

import (
	"bufio"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindPod = "pods"

func (h *Handler) HandleListPods(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*corev1.Pod
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindPod, params.Namespace) {
			return
		}
		all, err = h.Informers.Pods().Pods(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindPod, "") {
			return
		}
		all, err = h.Informers.Pods().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "Pod", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetPod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindPod, ns) {
		return
	}
	obj, err := h.Informers.Pods().Pods(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Pod", ns, name)
		return
	}
	writeData(w, obj)
}

// HandlePodLogs returns the last N lines of a pod's container logs.
// GET /api/v1/resources/pods/{namespace}/{name}/logs?container=X&tailLines=500&previous=false&timestamps=true
func (h *Handler) HandlePodLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	// RBAC: check get on pods/log subresource
	if !h.checkAccess(w, r, user, "get", "pods/log", ns) {
		return
	}

	q := r.URL.Query()
	container := q.Get("container")

	tailLines := int64(500)
	if tl := q.Get("tailLines"); tl != "" {
		if v, err := strconv.ParseInt(tl, 10, 64); err == nil && v > 0 {
			tailLines = v
		}
	}

	previous := q.Get("previous") == "true"
	timestamps := q.Get("timestamps") != "false" // default true

	opts := &corev1.PodLogOptions{
		Container:  container,
		TailLines:  &tailLines,
		Previous:   previous,
		Timestamps: timestamps,
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", "")
		return
	}

	stream, err := cs.CoreV1().Pods(ns).GetLogs(name, opts).Stream(r.Context())
	if err != nil {
		mapK8sError(w, err, "get", "Pod logs", ns, name)
		return
	}
	defer stream.Close()

	var lines []string
	scanner := bufio.NewScanner(stream)
	// Increase max line size to 1 MB for long log lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	writeData(w, map[string]any{
		"lines":     lines,
		"container": container,
		"pod":       name,
		"namespace": ns,
		"count":     len(lines),
	})
}

func (h *Handler) HandleDeletePod(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindPod, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Pods(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Pod", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Pod", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
