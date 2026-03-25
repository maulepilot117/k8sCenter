package resources

import (
	"encoding/base64"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const kindSecret = "secrets"

// lastAppliedConfigAnnotation is the annotation kubectl uses to store the full
// original manifest, which may contain plaintext secret values (stringData).
const lastAppliedConfigAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// maskedSecret returns a copy of a Secret with all data values replaced by "****"
// and the kubectl.kubernetes.io/last-applied-configuration annotation stripped
// to prevent leaking plaintext secret values.
func maskedSecret(s *corev1.Secret) *corev1.Secret {
	cp := s.DeepCopy()
	if cp.Data != nil {
		for k := range cp.Data {
			cp.Data[k] = []byte("****")
		}
	}
	if cp.StringData != nil {
		for k := range cp.StringData {
			cp.StringData[k] = "****"
		}
	}
	// Strip last-applied-configuration annotation which may contain plaintext
	// secret values (stringData) from the original kubectl apply manifest.
	if cp.Annotations != nil {
		delete(cp.Annotations, lastAppliedConfigAnnotation)
	}
	return cp
}

// HandleListSecrets handles GET /api/v1/resources/secrets[/:namespace]
// Secrets are NOT cached in the informer — they are fetched on-demand via
// the impersonated client. All data values are masked in list responses.
func (h *Handler) HandleListSecrets(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindSecret, params.Namespace) {
			return
		}
	} else {
		if !h.checkAccess(w, r, user, "list", kindSecret, "") {
			return
		}
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}

	opts := metav1.ListOptions{}
	if params.LabelSelector != "" {
		opts.LabelSelector = params.LabelSelector
	}
	if params.Limit > 0 {
		opts.Limit = int64(params.Limit)
	}
	if params.Continue != "" {
		opts.Continue = params.Continue
	}

	list, err := cs.CoreV1().Secrets(params.Namespace).List(r.Context(), opts)
	if err != nil {
		mapK8sError(w, err, "list", "Secret", params.Namespace, "")
		return
	}

	masked := make([]corev1.Secret, len(list.Items))
	for i := range list.Items {
		masked[i] = *maskedSecret(&list.Items[i])
	}

	writeList(w, masked, len(masked), list.Continue)
}

// HandleGetSecret handles GET /api/v1/resources/secrets/:namespace/:name
// Data values are masked.
func (h *Handler) HandleGetSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindSecret, ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	obj, err := cs.CoreV1().Secrets(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		mapK8sError(w, err, "get", "Secret", ns, name)
		return
	}
	writeData(w, maskedSecret(obj))
}

// HandleRevealSecret handles GET /api/v1/resources/secrets/:namespace/:name/reveal/:key
// Returns the plaintext value of a single secret key. Audit-logged.
func (h *Handler) HandleRevealSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	key := chi.URLParam(r, "key")
	if !h.checkAccess(w, r, user, "get", kindSecret, ns) {
		return
	}

	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	obj, err := cs.CoreV1().Secrets(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		mapK8sError(w, err, "get", "Secret", ns, name)
		return
	}

	val, exists := obj.Data[key]
	if !exists {
		writeError(w, http.StatusNotFound, "key '"+key+"' not found in secret '"+name+"'", "")
		return
	}

	h.auditWrite(r, user, audit.ActionReveal, "Secret", ns, name, audit.ResultSuccess)

	writeData(w, map[string]string{
		"key":   key,
		"value": base64.StdEncoding.EncodeToString(val),
	})
}

func (h *Handler) HandleCreateSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindSecret, ns) {
		return
	}
	var obj corev1.Secret
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.CoreV1().Secrets(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "Secret", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "Secret", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "Secret", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, maskedSecret(created))
}

func (h *Handler) HandleUpdateSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindSecret, ns) {
		return
	}
	var obj corev1.Secret
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Namespace = ns
	obj.Name = name
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	updated, err := cs.CoreV1().Secrets(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "Secret", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "Secret", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "Secret", ns, name, audit.ResultSuccess)
	writeData(w, maskedSecret(updated))
}

func (h *Handler) HandleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindSecret, ns) {
		return
	}
	cs, err := h.impersonatingClient(r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.CoreV1().Secrets(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "Secret", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "Secret", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "Secret", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
