package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	rbacv1 "k8s.io/api/rbac/v1"
)

// RBAC Viewer — read-only handlers for Roles, ClusterRoles, and Bindings.

func (h *Handler) HandleListRoles(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)

	var all []*rbacv1.Role
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", "roles", params.Namespace) {
			return
		}
		all, err = h.Informers.Roles().Roles(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		if !h.checkAccess(w, r, user, "list", "roles", "") {
			return
		}
		all, err = h.Informers.Roles().List(parseSelector(params.LabelSelector))
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
	if !h.checkAccess(w, r, user, "get", "roles", ns) {
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
	if !h.checkAccess(w, r, user, "list", "clusterroles", "") {
		return
	}
	all, err := h.Informers.ClusterRoles().List(parseSelector(params.LabelSelector))
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
	if !h.checkAccess(w, r, user, "get", "clusterroles", "") {
		return
	}
	obj, err := h.Informers.ClusterRoles().Get(name)
	if err != nil {
		mapK8sError(w, err, "get", "ClusterRole", "", name)
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

	var all []*rbacv1.RoleBinding
	var err error
	if params.Namespace != "" {
		if !h.checkAccess(w, r, user, "list", "rolebindings", params.Namespace) {
			return
		}
		all, err = h.Informers.RoleBindings().RoleBindings(params.Namespace).List(parseSelector(params.LabelSelector))
	} else {
		if !h.checkAccess(w, r, user, "list", "rolebindings", "") {
			return
		}
		all, err = h.Informers.RoleBindings().List(parseSelector(params.LabelSelector))
	}
	if err != nil {
		mapK8sError(w, err, "list", "RoleBinding", params.Namespace, "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}

func (h *Handler) HandleListClusterRoleBindings(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	params := parseListParams(r)
	if !h.checkAccess(w, r, user, "list", "clusterrolebindings", "") {
		return
	}
	all, err := h.Informers.ClusterRoleBindings().List(parseSelector(params.LabelSelector))
	if err != nil {
		mapK8sError(w, err, "list", "ClusterRoleBinding", "", "")
		return
	}
	items, cont := paginate(all, params.Limit, params.Continue)
	writeList(w, items, len(all), cont)
}
