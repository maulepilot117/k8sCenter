package rubrik

// restore.go - same-cluster restore (U8, R16, R17, R18, KTD9).
//
// A restore runs the write sequence of writes.go and adds the R17 checks
// on top: the source is resolved on the server from the mapped cluster's
// objects and a live snapshot list, and the user's own Kubernetes identity
// must be able to create what a restore creates in every namespace it
// writes into. A refusal is audited and nothing reaches RSC.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// RSCRestorer is the part of Operations a restore sends (KTD9).
type RSCRestorer interface {
	RestoreNamespace(ctx context.Context, snapshotID, targetClusterID, targetNamespace string) (JobHandle, error)
	RestoreProtectionSetSnapshot(ctx context.Context, snapshotID string) (JobHandle, error)
}

var _ RSCRestorer = (*Operations)(nil)

// Fixed restore messages.
const (
	// msgOverwrite is the confirmation's overwrite notice (R18). RSC does
	// not document whether a restore replaces existing objects, so the
	// notice assumes it may until a tenant test settles it.
	msgOverwrite        = "Existing objects in the target namespace with the same names may be replaced."
	msgSnapshotGone     = "snapshot not found; it may have expired"
	msgSnapshotUnusable = "RSC marks this snapshot corrupted or quarantined; it cannot be restored"
	msgSetInPlace       = "a protection-set snapshot restores only into the namespaces the set protects"
	msgSetScopeUnknown  = "this protection set does not resolve to a list of namespaces; restore it from RSC"
	msgSourceUnknown    = "this Rubrik object does not name its namespace; restore it from RSC"
	msgEarlierSnapshot  = "only admins can restore a snapshot taken before the namespace was created"
	msgTargetGone       = "target namespace not found"
	msgMissingPerm      = "your own Kubernetes permissions do not cover this restore; missing: "
)

var kindRestore = writeKind{store.RubrikOpRestore, ActionRestore, audit.ActionRubrikRestore}

// restoreRequest is the body of POST /rubrik/restores. Every field is
// required: the snapshot is looked up among the named object's snapshots,
// so the server never trusts the client about where it came from.
type restoreRequest struct {
	OperationID     string `json:"operationId"`
	ObjectID        string `json:"objectId"`
	SnapshotID      string `json:"snapshotId"`
	TargetNamespace string `json:"targetNamespace"`
}

// restoreSource is the source as the server resolved it, stored on the
// operation row so the audit explains the decision (KTD9).
type restoreSource struct {
	ObjectID string `json:"objectId"`
	Family   Family `json:"family"`
	// Namespace is the namespace the snapshot was taken of; empty for a
	// set covering several.
	Namespace    string    `json:"namespace,omitempty"`
	SnapshotID   string    `json:"snapshotId"`
	SnapshotTime time.Time `json:"snapshotTime"`
	// Into lists every namespace the restore writes into: the target for a
	// per-namespace object, each namespace a protection set covers.
	Into []string `json:"into"`
}

// permission is one entry of the R17 own-permission matrix.
type permission struct {
	verb, group, resource string
}

func (p permission) String() string {
	if p.group == "" {
		return p.verb + " " + p.resource
	}
	return p.verb + " " + p.resource + "." + p.group
}

const rbacGroup = "rbac.authorization.k8s.io"

// restoreMatrix is what the user's own identity must be able to do in each
// namespace a restore writes into (R17, KTD9). Rubrik's agent restores as
// its service account, so these checks keep a restore within what the user
// could create by hand, RBAC escalation included.
var restoreMatrix = []permission{
	{"create", rbacGroup, "roles"},
	{"create", rbacGroup, "rolebindings"},
	{"create", "", "serviceaccounts"},
	{"create", "", "secrets"},
	{"create", "", "configmaps"},
	{"create", "", "services"},
	{"create", "", "persistentvolumeclaims"},
	{"create", "apps", "deployments"},
	{"create", "apps", "statefulsets"},
	{"create", "apps", "daemonsets"},
	{"bind", rbacGroup, "roles"},
	{"escalate", rbacGroup, "roles"},
	{"bind", rbacGroup, "clusterroles"},
	{"escalate", rbacGroup, "clusterroles"},
}

// sourceSecrets is what a restore into another namespace adds in the
// source namespace: the snapshot carries its Secrets (R17).
var sourceSecrets = permission{"get", "", "secrets"}

// missingPermission returns the first permission the viewer's own
// Kubernetes identity lacks for a restore, named with its namespace, or ""
// when it holds them all. Admins are checked too: the Rubrik grants are
// what admins skip, not Kubernetes RBAC. sourceNS is checked for
// sourceSecrets when it is not empty.
func (v *Viewer) missingPermission(ctx context.Context, into []string, sourceNS string) (string, error) {
	check := func(p permission, ns string) (bool, error) {
		ok, err := v.access.checker.CanAccessGroupResource(ctx, v.clusterID,
			v.user.KubernetesUsername, v.user.KubernetesGroups, p.verb, p.group, p.resource, ns)
		if err != nil {
			return false, fmt.Errorf("%w: %s in %q: %w", ErrAccessCheck, p, ns, err)
		}
		return ok, nil
	}
	for _, ns := range into {
		for _, p := range restoreMatrix {
			ok, err := check(p, ns)
			if err != nil || !ok {
				return p.String() + " in " + ns, err
			}
		}
	}
	if sourceNS != "" {
		ok, err := check(sourceSecrets, sourceNS)
		if err != nil || !ok {
			return sourceSecrets.String() + " in " + sourceNS, err
		}
	}
	return "", nil
}

// sourceNamespace is the one namespace o's snapshots were taken of, empty
// when o covers several or none resolved.
func sourceNamespace(o Object) string {
	if o.Coverage.MultiNamespace() || len(o.Coverage.Namespaces) != 1 {
		return ""
	}
	return o.Coverage.Namespaces[0]
}

// refusalSetScopeUnknown: a protection set whose namespaces do not resolve
// to a list. Its in-place restore writes into namespaces that cannot be
// named, so the own-permission matrix cannot be run and no one may restore
// it through k8sCenter.
const refusalSetScopeUnknown Refusal = "set_scope_unknown"

// refusalSourceUnknown: a per-namespace object that does not name its
// namespace. Its restore might be cross-namespace, and the source `get
// secrets` check cannot be run against a namespace that cannot be named, so
// no one may restore it through k8sCenter (fail closed).
const refusalSourceUnknown Refusal = "source_unresolved"

// setScopeUnknown reports whether o is a protection set whose namespaces do
// not resolve to a list.
func setScopeUnknown(o Object) bool {
	return o.Family == FamilyProtectionSet &&
		(o.Coverage.All || o.Coverage.Unresolved || len(o.Coverage.Namespaces) == 0)
}

// scopeRefusal is why the namespaces a restore from o reads or writes
// cannot be named, admins included: a set that does not resolve to a list,
// or a per-namespace object that names no namespace.
func scopeRefusal(o Object) Refusal {
	switch {
	case setScopeUnknown(o):
		return refusalSetScopeUnknown
	case o.Family != FamilyProtectionSet && sourceNamespace(o) == "":
		return refusalSourceUnknown
	}
	return RefusalNone
}

// restoreInto returns the namespaces a restore of o into target writes
// into. A per-namespace object restores into the target. A protection set
// restores in place only, into every namespace it covers, so the target
// must be one of them. An object whose namespaces cannot be named
// (scopeRefusal) cannot be checked and is refused. msg and reason describe
// a refusal.
func restoreInto(o Object, target string) (into []string, msg, reason string) {
	switch scopeRefusal(o) {
	case refusalSetScopeUnknown:
		return nil, msgSetScopeUnknown, string(refusalSetScopeUnknown)
	case refusalSourceUnknown:
		return nil, msgSourceUnknown, string(refusalSourceUnknown)
	}
	if o.Family != FamilyProtectionSet {
		return []string{target}, "", ""
	}
	if !slices.Contains(o.Coverage.Namespaces, target) {
		return nil, msgSetInPlace, "target_not_allowed"
	}
	into = slices.Clone(o.Coverage.Namespaces)
	slices.Sort(into)
	return slices.Compact(into), "", ""
}

// HandleRestore answers POST /rubrik/restores: restore one snapshot of an
// object of the selected cluster into a namespace of the same cluster
// (R16, R17). The checks run in order: the writes setting, the viewer, the
// operation id's earlier row, the target's read grant, the mapping, the
// object, CheckAction (the admin-only rules for multi-namespace and
// cluster-scoped sources, the source read grant, the target's restore
// grant), the protection-set in-place rule, the snapshot, the
// earlier-namespace rule, the own-permission matrix, and the in-flight
// check as the row is written.
func (h *Handler) HandleRestore(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}
	if h.ops == nil || h.writer == nil || h.restorer == nil {
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgWritesNotWired, "writes_unavailable", nil)
		return
	}
	if user.ID == "" {
		httputil.WriteError(w, http.StatusForbidden, "your session has no user id", "")
		return
	}
	var req restoreRequest
	if !decodeBody(w, r, &req) {
		return
	}
	id, ok := parseOperationID(req.OperationID)
	if !ok {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "operationId must be a UUID", "invalid_operation_id", nil)
		return
	}
	if msg := validateRestoreRequest(req); msg != "" {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, msg, "invalid_request", nil)
		return
	}
	target := req.TargetNamespace
	clusterID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(r.Context()))
	c := &writeCall{
		w: w, r: r, k: kindRestore, user: user, ns: target,
		req:  writeRequest{OperationID: id, ObjectID: req.ObjectID},
		id:   id,
		hash: restoreHash(clusterID, req),
		rr:   readRequest{clusterID: clusterID},
	}

	// c.ns is the target, so a restore into a namespace the user cannot
	// read is refused up front.
	v, t, view, ok := h.beginWrite(c)
	if !ok {
		return
	}
	ctx := r.Context()

	// The source must be an object of the mapped RSC cluster (R17): the
	// view holds only that cluster's objects. An object the user cannot
	// see is not found, so ids cannot be probed.
	c.object = req.ObjectID
	o, found := view.Object(req.ObjectID)
	if found && !v.Admin() {
		var err error
		if found, err = v.ObjectVisible(ctx, o); err != nil {
			c.accessFailed(h, err)
			return
		}
	}
	if !found {
		c.refuse(h, http.StatusNotFound, msgObjectNotFound, "object_not_found")
		return
	}
	c.object = o.ID

	refusal, err := v.CheckAction(ctx, t, ActionRestore, o, target)
	if err != nil {
		c.accessFailed(h, err)
		return
	}
	if refusal != RefusalNone {
		c.refuse(h, refusalStatus(refusal), refusalMessage(refusal), string(refusal))
		return
	}

	into, msg, reason := restoreInto(o, target)
	if reason != "" {
		c.refuse(h, http.StatusBadRequest, msg, reason)
		return
	}
	// RSC restores into existing namespaces only (R16). A remote cluster
	// has no informer to ask, so there RSC is left to refuse.
	if k8s.IsLocalClusterID(clusterID) {
		for _, ns := range into {
			st, err := v.namespace(ns)
			if err != nil {
				c.accessFailed(h, err)
				return
			}
			if !st.exists {
				c.refuse(h, http.StatusNotFound, msgTargetGone, "target_not_found")
				return
			}
		}
	}

	snap, ok := h.restoreSnapshot(c, o, req.SnapshotID)
	if !ok {
		return
	}
	if !v.Admin() {
		visible, err := v.VisibleSnapshots(ctx, o, []Snapshot{snap})
		if err != nil {
			c.accessFailed(h, err)
			return
		}
		if len(visible) == 0 {
			c.refuse(h, http.StatusForbidden, msgEarlierSnapshot, "earlier_snapshot")
			return
		}
	}

	source := sourceNamespace(o)
	secretsFrom := ""
	if o.Family != FamilyProtectionSet && source != target {
		secretsFrom = source
	}
	missing, err := v.missingPermission(ctx, into, secretsFrom)
	if err != nil {
		c.accessFailed(h, err)
		return
	}
	if missing != "" {
		c.audit(h, audit.ResultDenied, "missing_permission: "+missing)
		httputil.WriteErrorWithReason(w, http.StatusForbidden, msgMissingPerm+missing, "missing_permission",
			map[string]any{"missingPermission": missing})
		return
	}

	c.restore = &restoreSource{
		ObjectID: o.ID, Family: o.Family, Namespace: source,
		SnapshotID: snap.ID, SnapshotTime: snap.Time, Into: into,
	}
	h.send(c, t, o, opRequest{Family: o.Family, RequestedObjectID: req.ObjectID})
}

// restoreSnapshot finds snapshotID among o's snapshots in a live list, so a
// snapshot that expired since the page was loaded is not restored from a
// cached answer. A missing or expired snapshot is 404, an unusable one 409.
func (h *Handler) restoreSnapshot(c *writeCall, o Object, snapshotID string) (Snapshot, bool) {
	snaps, err := h.service.rsc.Snapshots(c.r.Context(), o.Family, o.ID, MaxSnapshotLimit)
	if err != nil {
		c.audit(h, audit.ResultFailure, "snapshots: "+errorClass(err))
		h.writeReadError(c.w, c.rr, err)
		return Snapshot{}, false
	}
	i := slices.IndexFunc(snaps, func(s Snapshot) bool { return strings.EqualFold(s.ID, snapshotID) })
	now := time.Now()
	if i < 0 || snaps[i].Expired || (snaps[i].Expires != nil && !snaps[i].Expires.After(now)) {
		c.fail(h, http.StatusNotFound, msgSnapshotGone, "snapshot_not_found", nil)
		return Snapshot{}, false
	}
	if snaps[i].Corrupted || snaps[i].Quarantined {
		c.refuse(h, http.StatusConflict, msgSnapshotUnusable, "snapshot_unusable")
		return Snapshot{}, false
	}
	return snaps[i], true
}

// sendRestore sends a recorded restore to RSC: a per-namespace object's
// snapshot into the target namespace of the mapped RSC cluster, a
// protection set's snapshot in place (KTD9).
func (h *Handler) sendRestore(ctx context.Context, t Target, o Object, c *writeCall) (JobHandle, error) {
	if o.Family == FamilyProtectionSet {
		return h.restorer.RestoreProtectionSetSnapshot(ctx, c.restore.SnapshotID)
	}
	return h.restorer.RestoreNamespace(ctx, c.restore.SnapshotID, t.Mapping.RSCClusterID, c.ns)
}

// auditDetail describes the resolved source for the restore's audit entry.
func (s *restoreSource) auditDetail() string {
	from := s.Namespace
	if from == "" {
		from = "set"
	}
	return "snapshot " + s.SnapshotID + " taken " + s.SnapshotTime.UTC().Format(time.RFC3339) +
		"; from " + from + "; into " + strings.Join(s.Into, ",")
}

// sourceJSON is the stored form of the resolved source.
func (s *restoreSource) sourceJSON() json.RawMessage {
	if s == nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return raw
}

func validateRestoreRequest(req restoreRequest) string {
	switch {
	case !validUUID(req.ObjectID):
		return "objectId must be a UUID"
	case !validUUID(req.SnapshotID):
		return "snapshotId must be a UUID"
	case !validNamespaceName(req.TargetNamespace):
		return "targetNamespace must be a namespace name"
	}
	return ""
}

// restoreHash identifies a restore request, so a retry under the same
// operation id is a replay only when it repeats it. Ids compare case-free.
func restoreHash(clusterID string, req restoreRequest) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		store.RubrikOpRestore, clusterID, req.TargetNamespace,
		strings.ToLower(req.ObjectID), strings.ToLower(req.SnapshotID),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// RestoreTargets answers the restore dialog: the namespaces a restore from
// one object may target, and the confirmation's overwrite notice (R18).
type RestoreTargets struct {
	ObjectID        string `json:"objectId"`
	SourceNamespace string `json:"sourceNamespace"`
	// InPlaceOnly is true for a protection set, which restores only into
	// the namespaces it protects (R16).
	InPlaceOnly bool `json:"inPlaceOnly"`
	// Targets are existing namespaces where the user holds the read and
	// restore grants (all existing namespaces for an admin), sorted.
	Targets []string `json:"targets"`
	// Refusal is the reason class when no restore from the object may be
	// offered at all; Targets is then empty.
	Refusal         string `json:"refusal,omitempty"`
	OverwriteNotice string `json:"overwriteNotice"`
}

// HandleRestoreTargets answers GET
// /rubrik/namespaces/{namespace}/restore-targets?objectId=: the restore
// targets for a snapshot of object objectId, which must protect namespace.
// The list is advisory; each restore request is checked again in full,
// including the own-permission matrix, which is not run here.
func (h *Handler) HandleRestoreTargets(w http.ResponseWriter, r *http.Request) {
	rr, ok := h.startRead(w, r)
	if !ok {
		return
	}
	ns := chi.URLParam(r, "namespace")
	objectID := r.URL.Query().Get("objectId")
	if !validNamespaceName(ns) {
		httputil.WriteError(w, http.StatusNotFound, msgNamespaceGone, "")
		return
	}
	if !validUUID(objectID) {
		httputil.WriteErrorWithReason(w, http.StatusBadRequest, "objectId must be a UUID", "invalid_request", nil)
		return
	}
	ctx := r.Context()
	v := rr.viewer
	if !v.Admin() {
		_, readable, err := v.readable(ctx, ns)
		if err != nil {
			h.writeAccessError(w, err)
			return
		}
		if !readable {
			httputil.WriteError(w, http.StatusNotFound, msgNamespaceGone, "")
			return
		}
	}
	t, ok := h.resolve(w, r, rr)
	if !ok {
		return
	}
	view, err := h.service.Objects(ctx, t)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}
	o, found := view.Object(objectID)
	if found && !v.Admin() {
		if found, err = v.ObjectVisible(ctx, o); err != nil {
			h.writeAccessError(w, err)
			return
		}
	}
	if !found || !o.Coverage.Covers(ns) {
		httputil.WriteError(w, http.StatusNotFound, msgObjectNotFound, "")
		return
	}

	out := RestoreTargets{
		ObjectID: o.ID, SourceNamespace: sourceNamespace(o), InPlaceOnly: o.Family == FamilyProtectionSet,
		Targets: []string{}, OverwriteNotice: msgOverwrite,
	}
	refusal, err := v.restoreRefusal(ctx, t, o)
	if err != nil {
		h.writeAccessError(w, err)
		return
	}
	if refusal != RefusalNone {
		out.Refusal = string(refusal)
		httputil.WriteData(w, out)
		return
	}
	if out.Targets, err = h.restoreCandidates(ctx, v, rr.clusterID, o, ns); err != nil {
		h.writeAccessError(w, err)
		return
	}
	httputil.WriteData(w, out)
}

// restoreCandidates lists the namespaces a restore from o may target: for
// a protection set the namespace being viewed (it restores in place), for a
// per-namespace object every local namespace. A remote cluster has no
// informer, so an admin there is offered the source namespace only. A
// non-admin is offered a namespace only with the read and restore grants.
func (h *Handler) restoreCandidates(ctx context.Context, v *Viewer, clusterID string, o Object, ns string) ([]string, error) {
	candidates := []string{ns}
	if o.Family != FamilyProtectionSet && k8s.IsLocalClusterID(clusterID) {
		all, err := v.allNamespaces()
		if err != nil {
			return nil, err
		}
		candidates = all
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if k8s.IsLocalClusterID(clusterID) {
			st, err := v.namespace(c)
			if err != nil {
				return nil, err
			}
			if !st.exists {
				continue
			}
		}
		if !v.Admin() {
			_, readable, err := v.readable(ctx, c)
			if err != nil {
				return nil, err
			}
			if !readable {
				continue
			}
			ok, err := v.can(ctx, "create", ResourceRestores, c)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, c)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
