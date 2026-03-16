package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Webhook configuration handlers — read-only (List, Get) for
// ValidatingWebhookConfigurations and MutatingWebhookConfigurations.
// Both are cluster-scoped resources in admissionregistration.k8s.io/v1.

const (
	kindValidatingWebhookConfiguration = "validatingwebhookconfigurations"
	kindMutatingWebhookConfiguration   = "mutatingwebhookconfigurations"
)

// --- ValidatingWebhookConfigurations ---

func (h *Handler) HandleListValidatingWebhookConfigurations(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindValidatingWebhookConfiguration, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.ValidatingWebhookConfigurations().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "ValidatingWebhookConfiguration", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetValidatingWebhookConfiguration(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindValidatingWebhookConfiguration, "") {
		return
	}
	obj, err := h.Informers.ValidatingWebhookConfigurations().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ValidatingWebhookConfiguration", "", name)
		return
	}
	writeData(w, obj)
}

// --- MutatingWebhookConfigurations ---

func (h *Handler) HandleListMutatingWebhookConfigurations(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindMutatingWebhookConfiguration, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	all, err := h.Informers.MutatingWebhookConfigurations().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "MutatingWebhookConfiguration", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetMutatingWebhookConfiguration(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindMutatingWebhookConfiguration, "") {
		return
	}
	obj, err := h.Informers.MutatingWebhookConfigurations().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "MutatingWebhookConfiguration", "", name)
		return
	}
	writeData(w, obj)
}
