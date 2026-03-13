package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	rbacv1 "k8s.io/api/rbac/v1"
)

// RBAC Viewer — read-only handlers for Roles, ClusterRoles, and Bindings.

const (
	kindRole               = "roles"
	kindClusterRole        = "clusterroles"
	kindRoleBinding        = "rolebindings"
	kindClusterRoleBinding = "clusterrolebindings"
)

func (h *Handler) HandleListRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*rbacv1.Role
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindRole, params.Namespace) {
			return
		}
		all, err = h.Informers.Roles().Roles(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindRole, "") {
			return
		}
		all, err = h.Informers.Roles().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "Role", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetRole(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindRole, ns) {
		return
	}
	obj, err := h.Informers.Roles().Roles(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "Role", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleListClusterRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindClusterRole, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.ClusterRoles().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "ClusterRole", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetClusterRole(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindClusterRole, "") {
		return
	}
	obj, err := h.Informers.ClusterRoles().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ClusterRole", "", name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleGetRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindRoleBinding, ns) {
		return
	}
	obj, err := h.Informers.RoleBindings().RoleBindings(ns).Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "RoleBinding", ns, name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleListRoleBindings(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}

	var all []*rbacv1.RoleBinding
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", kindRoleBinding, params.Namespace) {
			return
		}
		all, err = h.Informers.RoleBindings().RoleBindings(params.Namespace).List(sel)
	} else {
		if !h.checkAccess(w, r, user, "list", kindRoleBinding, "") {
			return
		}
		all, err = h.Informers.RoleBindings().List(sel)
	}
	if err != nil {
		mapK8sError(w, err, "list", "RoleBinding", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleGetClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "get", kindClusterRoleBinding, "") {
		return
	}
	obj, err := h.Informers.ClusterRoleBindings().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ClusterRoleBinding", "", name)
		return
	}
	writeData(w, obj)
}

func (h *Handler) HandleListClusterRoleBindings(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", kindClusterRoleBinding, "") {
		return
	}
	sel, ok := parseSelectorOrReject(w, params.LabelSelector)
	if !ok {
		return
	}
	all, err := h.Informers.ClusterRoleBindings().List(sel)
	if err != nil {
		mapK8sError(w, err, "list", "ClusterRoleBinding", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}
