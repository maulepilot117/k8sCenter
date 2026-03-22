package resources

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RBAC handlers — read-only for Roles/ClusterRoles, full CRUD for Bindings.

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

// --- RoleBinding CRUD ---

func (h *Handler) HandleCreateRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	if !h.checkAccess(w, r, user, "create", kindRoleBinding, ns) {
		return
	}
	var obj rbacv1.RoleBinding
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
	created, err := cs.RbacV1().RoleBindings(ns).Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "RoleBinding", ns, obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "RoleBinding", ns, obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "RoleBinding", ns, created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleUpdateRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindRoleBinding, ns) {
		return
	}
	var obj rbacv1.RoleBinding
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
	updated, err := cs.RbacV1().RoleBindings(ns).Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "RoleBinding", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "update", "RoleBinding", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "RoleBinding", ns, name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindRoleBinding, ns) {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.RbacV1().RoleBindings(ns).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "RoleBinding", ns, name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "RoleBinding", ns, name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "RoleBinding", ns, name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// --- ClusterRoleBinding CRUD ---

func (h *Handler) HandleCreateClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !h.checkAccess(w, r, user, "create", kindClusterRoleBinding, "") {
		return
	}
	var obj rbacv1.ClusterRoleBinding
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	created, err := cs.RbacV1().ClusterRoleBindings().Create(r.Context(), &obj, metav1.CreateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionCreate, "ClusterRoleBinding", "", obj.Name, audit.ResultFailure)
		mapK8sError(w, err, "create", "ClusterRoleBinding", "", obj.Name)
		return
	}
	h.auditWrite(r, user, audit.ActionCreate, "ClusterRoleBinding", "", created.Name, audit.ResultSuccess)
	writeCreated(w, created)
}

func (h *Handler) HandleUpdateClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "update", kindClusterRoleBinding, "") {
		return
	}
	var obj rbacv1.ClusterRoleBinding
	if err := decodeBody(w, r, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}
	obj.Name = name
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	updated, err := cs.RbacV1().ClusterRoleBindings().Update(r.Context(), &obj, metav1.UpdateOptions{})
	if err != nil {
		h.auditWrite(r, user, audit.ActionUpdate, "ClusterRoleBinding", "", name, audit.ResultFailure)
		mapK8sError(w, err, "update", "ClusterRoleBinding", "", name)
		return
	}
	h.auditWrite(r, user, audit.ActionUpdate, "ClusterRoleBinding", "", name, audit.ResultSuccess)
	writeData(w, updated)
}

func (h *Handler) HandleDeleteClusterRoleBinding(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !h.checkAccess(w, r, user, "delete", kindClusterRoleBinding, "") {
		return
	}
	cs, err := h.impersonatingClient(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create client", err.Error())
		return
	}
	if err := cs.RbacV1().ClusterRoleBindings().Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.auditWrite(r, user, audit.ActionDelete, "ClusterRoleBinding", "", name, audit.ResultFailure)
		mapK8sError(w, err, "delete", "ClusterRoleBinding", "", name)
		return
	}
	h.auditWrite(r, user, audit.ActionDelete, "ClusterRoleBinding", "", name, audit.ResultSuccess)
	w.WriteHeader(http.StatusNoContent)
}
