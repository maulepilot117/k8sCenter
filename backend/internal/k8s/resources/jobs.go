package resources

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	kindJob     = "jobs"
	kindCronJob = "cronjobs"
)

// Jobs

func (h *Handler) HandleListJobs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*batchv1.Job
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindJob, params.Namespace) {
			return
		}
		all, err = h.Informers.Jobs().Jobs(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindJob, "") {
			return
		}
		all, err = h.Informers.Jobs().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "Job", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindJob, ns) {
		return
	}
	obj, err := h.Informers.Jobs().Jobs(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Job", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindJob, ns) {
		return
	}
	var obj batchv1.Job
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
	created, err := cs.BatchV1().Jobs(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Job", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Job", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "Job", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleDeleteJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindJob, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	propagation := metav1.DeletePropagationBackground
	if err := cs.BatchV1().Jobs(ns).Delete(r.Context(), name, metav1.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Job", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Job", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Job", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// CronJobs

func (h *Handler) HandleListCronJobs(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*batchv1.CronJob
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindCronJob, params.Namespace) {
			return
		}
		all, err = h.Informers.CronJobs().CronJobs(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindCronJob, "") {
			return
		}
		all, err = h.Informers.CronJobs().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "CronJob", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetCronJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindCronJob, ns) {
		return
	}
	obj, err := h.Informers.CronJobs().CronJobs(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "CronJob", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleCreateCronJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindCronJob, ns) {
		return
	}
	var obj batchv1.CronJob
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
	created, err := cs.BatchV1().CronJobs(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "CronJob", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "CronJob", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "CronJob", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleDeleteCronJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindCronJob, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.BatchV1().CronJobs(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "CronJob", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "CronJob", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "CronJob", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// HandleSuspendJob handles POST /api/v1/resources/jobs/:namespace/:name/suspend
func (h *Handler) HandleSuspendJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindJob, ns) {
		return
	}

	var req struct {
		Suspend bool `json:"suspend"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Guard: suspending a completed job is a no-op that confuses users
	job, err := h.Informers.Jobs().Jobs(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Job", ns, name)
		return
	}
	if job.Status.CompletionTime != nil {
		writeError(w, http.StatusUnprocessableEntity, "job has already completed", "")
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	patchData := fmt.Sprintf(`{"spec":{"suspend":%v}}`, req.Suspend)
	result, err := cs.BatchV1().Jobs(ns).Patch(r.Context(), name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Job", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "suspend", "Job", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "Job", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleSuspendCronJob handles POST /api/v1/resources/cronjobs/:namespace/:name/suspend
func (h *Handler) HandleSuspendCronJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindCronJob, ns) {
		return
	}

	var req struct {
		Suspend bool `json:"suspend"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	patchData := fmt.Sprintf(`{"spec":{"suspend":%v}}`, req.Suspend)
	result, err := cs.BatchV1().CronJobs(ns).Patch(r.Context(), name, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "CronJob", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "suspend", "CronJob", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionUpdate, "CronJob", ns, name, audit.ResultSuccess)
	writeData(w, result)
}

// HandleTriggerCronJob handles POST /api/v1/resources/cronjobs/:namespace/:name/trigger
func (h *Handler) HandleTriggerCronJob(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	// Triggering reads the CronJob template and creates a Job
	if !h.checkAccess(w, r, user, "get", kindCronJob, ns) {
		return
	}
	if !h.checkAccess(w, r, user, "create", kindJob, ns) {
		return
	}

	// Fetch the CronJob to get its job template
	cronJob, err := h.Informers.CronJobs().CronJobs(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "CronJob", ns, name)
		return
	}

	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	// Build Job from CronJob template using generateName for uniqueness
	prefix := name
	if len(prefix) > 43 {
		prefix = prefix[:43]
	}

	isController := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-manual-",
			Namespace:    ns,
			Labels:       cronJob.Spec.JobTemplate.Labels,
			Annotations: map[string]string{
				"cronjob.kubernetes.io/instantiate": "manual",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "CronJob",
					Name:       cronJob.Name,
					UID:        cronJob.UID,
					Controller: &isController,
				},
			},
		},
		Spec: cronJob.Spec.JobTemplate.Spec,
	}

	created, err := cs.BatchV1().Jobs(ns).Create(r.Context(), job, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Job", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "trigger", "CronJob", ns, name)
		return
	}

	h.auditWrite(r, user, audit.ActionCreate, "Job", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}
