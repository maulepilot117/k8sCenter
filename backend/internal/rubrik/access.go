package rubrik

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// The Rubrik grants are Kubernetes RBAC on virtual resources in one API
// group (KTD2). The group and resource names are an operator-facing contract:
// installed Roles reference them, so renaming one breaks those Roles.
const (
	APIGroup = "rubrik.kubecenter.io"

	// ResourceProtections carries the read grant (verb get).
	ResourceProtections = "protections"
	// ResourceSnapshots carries the on-demand snapshot grant (verb create).
	ResourceSnapshots = "snapshots"
	// ResourceSLAAssignments carries the SLA change grant (verb update).
	ResourceSLAAssignments = "slaassignments"
	// ResourceRestores carries the restore grant (verb create).
	ResourceRestores = "restores"
)

// Action is a Rubrik write a user can ask for.
type Action string

const (
	ActionSnapshot  Action = "snapshot"
	ActionChangeSLA Action = "changeSla"
	ActionRestore   Action = "restore"
)

// grant returns the verb and resource of the action's write grant.
func (a Action) grant() (verb, resource string, ok bool) {
	switch a {
	case ActionSnapshot:
		return "create", ResourceSnapshots, true
	case ActionChangeSLA:
		return "update", ResourceSLAAssignments, true
	case ActionRestore:
		return "create", ResourceRestores, true
	}
	return "", "", false
}

var (
	// ErrRemoteAdminOnly means a non-admin asked about a remote cluster.
	// Non-admin Rubrik access exists only on the local cluster (R10); the
	// platform's cluster gate refuses first, this is the second line.
	ErrRemoteAdminOnly = errors.New("rubrik: remote clusters are admin-only")
	// ErrAccessCheck means a grant check or a namespace lookup could not be
	// made. A visibility answer fails as a whole rather than leaving the
	// namespace out, so "you may not see it" and "we could not tell" are
	// never conflated (KTD3).
	ErrAccessCheck = errors.New("rubrik: access check failed")
)

// AccessChecker runs one SelfSubjectAccessReview through impersonation on
// the named cluster. resources.AccessChecker implements it and caches the
// answers.
type AccessChecker interface {
	CanAccessGroupResource(ctx context.Context, clusterID, username string, groups []string,
		verb, apiGroup, resource, namespace string) (bool, error)
}

// Access decides what users may see and do with Rubrik data. It is safe for
// concurrent use; the Viewer it hands out is not.
type Access struct {
	checker AccessChecker
	// namespaces lists the local cluster's namespaces. Only non-admins on
	// the local cluster are filtered, so a local informer is enough.
	namespaces    corev1listers.NamespaceLister
	writesEnabled bool
}

// NewAccess returns the access rules over checker and the local namespace
// lister. writesEnabled is the rubrik.writesEnabled setting; with it off
// every write is refused, for admins too (R29).
func NewAccess(checker AccessChecker, namespaces corev1listers.NamespaceLister, writesEnabled bool) *Access {
	return &Access{checker: checker, namespaces: namespaces, writesEnabled: writesEnabled}
}

// WritesEnabled reports the rubrik.writesEnabled setting.
func (a *Access) WritesEnabled() bool { return a.writesEnabled }

// Viewer is one user's view of one cluster for the length of one request.
// It remembers grant answers and namespace lookups, so each candidate
// namespace costs one check. It is not safe for concurrent use.
type Viewer struct {
	access    *Access
	user      *auth.User
	clusterID string
	admin     bool

	grants     map[grantKey]bool
	namespaces map[string]namespaceState
	all        []string
	listedAll  bool
}

type grantKey struct {
	verb, resource, namespace string
}

// namespaceState is a local namespace as the informer sees it now.
type namespaceState struct {
	exists  bool
	created time.Time
}

// Viewer returns user's view of clusterID. Admins see every namespace of
// any mapped cluster (R5). A non-admin gets ErrRemoteAdminOnly for a remote
// cluster (R10).
func (a *Access) Viewer(user *auth.User, clusterID string) (*Viewer, error) {
	if user == nil {
		return nil, fmt.Errorf("%w: no user", ErrAccessCheck)
	}
	admin := auth.IsAdmin(user)
	if !admin && !k8s.IsLocalClusterID(clusterID) {
		return nil, ErrRemoteAdminOnly
	}
	return &Viewer{
		access:     a,
		user:       user,
		clusterID:  k8s.NormalizedClusterID(clusterID),
		admin:      admin,
		grants:     map[grantKey]bool{},
		namespaces: map[string]namespaceState{},
	}, nil
}

// Admin reports whether the viewer skips every Rubrik grant.
func (v *Viewer) Admin() bool { return v.admin }

// can reports whether the viewer holds verb on resource in namespace. The
// answer is remembered for the request.
func (v *Viewer) can(ctx context.Context, verb, resource, namespace string) (bool, error) {
	key := grantKey{verb, resource, namespace}
	if ok, seen := v.grants[key]; seen {
		return ok, nil
	}
	ok, err := v.access.checker.CanAccessGroupResource(ctx, v.clusterID,
		v.user.KubernetesUsername, v.user.KubernetesGroups, verb, APIGroup, resource, namespace)
	if err != nil {
		return false, fmt.Errorf("%w: %s %s in %q: %w", ErrAccessCheck, verb, resource, namespace, err)
	}
	v.grants[key] = ok
	return ok, nil
}

// CanRead reports whether the viewer holds the Rubrik read grant on
// namespace (R5). Admins always do.
func (v *Viewer) CanRead(ctx context.Context, namespace string) (bool, error) {
	if v.admin {
		return true, nil
	}
	if namespace == "" {
		return false, nil
	}
	return v.can(ctx, "get", ResourceProtections, namespace)
}

// namespace looks up a local namespace once per request.
func (v *Viewer) namespace(name string) (namespaceState, error) {
	if st, seen := v.namespaces[name]; seen {
		return st, nil
	}
	var st namespaceState
	ns, err := v.access.namespaces.Get(name)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return namespaceState{}, fmt.Errorf("%w: namespace %q: %w", ErrAccessCheck, name, err)
	default:
		st = namespaceState{exists: true, created: ns.CreationTimestamp.Time}
	}
	v.namespaces[name] = st
	return st, nil
}

// allNamespaces lists every local namespace once per request.
func (v *Viewer) allNamespaces() ([]string, error) {
	if v.listedAll {
		return v.all, nil
	}
	list, err := v.access.namespaces.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("%w: listing namespaces: %w", ErrAccessCheck, err)
	}
	names := make([]string, 0, len(list))
	for _, ns := range list {
		names = append(names, ns.Name)
		v.namespaces[ns.Name] = namespaceState{exists: true, created: ns.CreationTimestamp.Time}
	}
	slices.Sort(names)
	v.all, v.listedAll = names, true
	return names, nil
}

// readable reports whether a non-admin may read namespace: it exists in the
// local cluster now and the viewer holds the read grant there. A namespace
// that no longer exists belongs to an earlier owner (R6), so nothing of it
// is readable.
func (v *Viewer) readable(ctx context.Context, name string) (namespaceState, bool, error) {
	st, err := v.namespace(name)
	if err != nil || !st.exists {
		return st, false, err
	}
	ok, err := v.CanRead(ctx, name)
	return st, ok, err
}

// EarlierNamespace reports whether name is an earlier namespace: on the
// local cluster, one the informer no longer has. Every object covering it is
// earlier (KTD3), so none of them counts toward its protection, compliance
// or action targets (KTD4). A remote cluster has no informer to ask, so
// there no namespace is known to be earlier.
func (v *Viewer) EarlierNamespace(name string) (bool, error) {
	if !k8s.IsLocalClusterID(v.clusterID) {
		return false, nil
	}
	st, err := v.namespace(name)
	if err != nil {
		return false, err
	}
	return !st.exists, nil
}

// VisibleNamespaces returns the candidates the viewer may see Rubrik data
// for, sorted and without duplicates. A non-admin sees a candidate when it
// exists locally and carries the read grant. One failed check fails the
// whole answer.
func (v *Viewer) VisibleNamespaces(ctx context.Context, candidates []string) ([]string, error) {
	names := slices.Clone(candidates)
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) > 0 && names[0] == "" {
		names = names[1:]
	}
	if v.admin {
		return names, nil
	}
	out := make([]string, 0, len(names))
	for _, ns := range names {
		_, ok, err := v.readable(ctx, ns)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ns)
		}
	}
	return out, nil
}

// coveredNow returns the namespaces an object covers now. ok is false when
// its coverage is unresolved or names no namespace, which leaves it
// admin-only (R7).
func (v *Viewer) coveredNow(o Object) (namespaces []string, ok bool, err error) {
	switch {
	case o.Coverage.Unresolved:
		return nil, false, nil
	case o.Coverage.All:
		all, err := v.allNamespaces()
		if err != nil {
			return nil, false, err
		}
		return all, len(all) > 0, nil
	}
	names := make([]string, 0, len(o.Coverage.Namespaces))
	for _, ns := range o.Coverage.Namespaces {
		if ns = strings.TrimSpace(ns); ns != "" {
			names = append(names, ns)
		}
	}
	return names, len(names) > 0, nil
}

// ObjectVisible reports whether the viewer may see an object. Admins see
// every object, relics included (KTD4). A non-admin sees an object only when
// it is current (not a relic), its coverage resolves to namespaces, and every
// namespace it covers now exists locally and carries the read grant (R5,
// R6, R7). An object covering every namespace therefore needs the grant on
// every namespace of the cluster.
func (v *Viewer) ObjectVisible(ctx context.Context, o Object) (bool, error) {
	if v.admin {
		return true, nil
	}
	if o.Relic {
		return false, nil
	}
	names, ok, err := v.coveredNow(o)
	if err != nil || !ok {
		return false, err
	}
	for _, ns := range names {
		_, ok, err := v.readable(ctx, ns)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// VisibleObjects filters objects to the ones the viewer may see, keeping
// their order. One failed check fails the whole answer.
func (v *Viewer) VisibleObjects(ctx context.Context, objects []Object) ([]Object, error) {
	if v.admin {
		return slices.Clone(objects), nil
	}
	out := make([]Object, 0, len(objects))
	for _, o := range objects {
		ok, err := v.ObjectVisible(ctx, o)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// historyNamespace returns the one namespace whose history (snapshots and
// jobs) a non-admin may see for o, with its creation time. ok is false when
// o is not visible or is not namespace-scoped. A snapshot of a set covering
// several namespaces may hold namespaces that were deleted since, or that
// belonged to an earlier owner, and the model does not record which
// namespaces each snapshot covered, so that history is admin-only (R6, R7).
func (v *Viewer) historyNamespace(ctx context.Context, o Object) (namespace string, created time.Time, ok bool, err error) {
	if !o.NamespaceScoped() {
		return "", time.Time{}, false, nil
	}
	visible, err := v.ObjectVisible(ctx, o)
	if err != nil || !visible {
		return "", time.Time{}, false, err
	}
	ns := o.Coverage.Namespaces[0]
	st, err := v.namespace(ns)
	if err != nil || !st.exists {
		return "", time.Time{}, false, err
	}
	return ns, st.created, true, nil
}

// VisibleSnapshots filters the snapshots of o. Admins see all of them. A
// non-admin sees a snapshot only when o is visible and namespace-scoped, and
// the snapshot was taken at or after the namespace's creation: an earlier
// snapshot belongs to an earlier namespace of the same name (R6, AE4).
func (v *Viewer) VisibleSnapshots(ctx context.Context, o Object, snapshots []Snapshot) ([]Snapshot, error) {
	if v.admin {
		return slices.Clone(snapshots), nil
	}
	_, created, ok, err := v.historyNamespace(ctx, o)
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	if !ok {
		return out, nil
	}
	for _, s := range snapshots {
		if !s.Time.Before(created) {
			out = append(out, s)
		}
	}
	return out, nil
}

// VisibleJobs filters the job history of o. A job of another object is
// dropped for everyone, so an answer RSC scoped wrongly never shows. A
// non-admin sees a job only under the snapshot rules, and only when it
// started at or after the namespace's creation; a job without a start time
// cannot be placed and is admin-only.
func (v *Viewer) VisibleJobs(ctx context.Context, o Object, jobs []Job) ([]Job, error) {
	own := make([]Job, 0, len(jobs))
	for _, j := range jobs {
		if strings.EqualFold(j.ObjectID, o.ID) {
			own = append(own, j)
		}
	}
	if v.admin {
		return own, nil
	}
	_, created, ok, err := v.historyNamespace(ctx, o)
	if err != nil {
		return nil, err
	}
	out := []Job{}
	if !ok {
		return out, nil
	}
	for _, j := range own {
		if j.StartTime != nil && !j.StartTime.Before(created) {
			out = append(out, j)
		}
	}
	return out, nil
}

// ClampHistory clears o's newest snapshot time when VisibleSnapshots would
// hide that snapshot from the viewer, so a list row never shows history the
// detail hides. It shares historyNamespace with VisibleSnapshots and
// VisibleJobs so the R6/R7 rule has one definition. Admins see the time.
func (v *Viewer) ClampHistory(ctx context.Context, o Object) (Object, error) {
	if v.admin || o.NewestSnapshot == nil {
		return o, nil
	}
	_, created, ok, err := v.historyNamespace(ctx, o)
	if err != nil {
		return Object{}, err
	}
	if !ok || o.NewestSnapshot.Before(created) {
		o.NewestSnapshot = nil
	}
	return o, nil
}

// Refusal says why a write is refused. The empty value allows it.
type Refusal string

const (
	RefusalNone Refusal = ""
	// RefusalWritesDisabled: the rubrik.writesEnabled setting is off (R29).
	RefusalWritesDisabled Refusal = "writes_disabled"
	// RefusalMappingInvalid: the cluster has no valid mapping (R2).
	RefusalMappingInvalid Refusal = "mapping_invalid"
	// RefusalNotActionable: nothing can act on the object (a relic cannot
	// be snapshotted or given an SLA).
	RefusalNotActionable Refusal = "not_actionable"
	// RefusalAdminOnly: only admins may act on this object: it covers
	// several or unresolved namespaces (R7), its namespace no longer exists
	// (R6), or, for a restore, it may hold cluster-scoped resources (R17).
	// CheckAction sees no snapshot, so whether the chosen snapshot predates
	// the namespace (R6) is the restore path's check, through
	// VisibleSnapshots.
	RefusalAdminOnly Refusal = "admin_only"
	// RefusalNoGrant: the user lacks the read grant on the object's
	// namespace or the action's own grant (R8).
	RefusalNoGrant Refusal = "no_grant"
	// RefusalInvalidTarget: the restore target is not a namespace name.
	RefusalInvalidTarget Refusal = "invalid_target"
	// RefusalCheckFailed comes with an error: a grant check or namespace
	// lookup could not be made. It is never RefusalNone, so a caller that
	// tests the refusal before the error still refuses.
	RefusalCheckFailed Refusal = "check_failed"
)

// CheckAction decides whether the viewer may perform action on o in cluster
// target t. For a restore, targetNamespace is the namespace restored into;
// the other actions ignore it. A refusal says why. An error means a check
// could not be made; it comes with RefusalCheckFailed, never RefusalNone.
//
// Admins may act on any current object, and restore from relics and
// multi-namespace sets. A non-admin may act only on a current,
// namespace-scoped object in a namespace that exists locally, holding the
// read grant there and the action's own grant: in the object's namespace for
// a snapshot or SLA change, in the target namespace for a restore. A restore
// from an object whose snapshots may hold cluster-scoped resources is
// admin-only. The restore's own-permission matrix and source checks (R17)
// come on top of this, in the restore path.
func (v *Viewer) CheckAction(ctx context.Context, t Target, action Action, o Object, targetNamespace string) (Refusal, error) {
	verb, resource, known := action.grant()
	if !known {
		return RefusalNotActionable, nil
	}
	if r := v.writeGate(t); r != RefusalNone {
		return r, nil
	}
	if action == ActionRestore && !validNamespaceName(targetNamespace) {
		return RefusalInvalidTarget, nil
	}
	if o.Relic && action != ActionRestore {
		return RefusalNotActionable, nil
	}
	if v.admin {
		return RefusalNone, nil
	}
	source, r, err := v.nonAdminSource(ctx, action, o)
	if err != nil || r != RefusalNone {
		return r, err
	}
	grantNS := source
	if action == ActionRestore {
		grantNS = targetNamespace
	}
	ok, err := v.can(ctx, verb, resource, grantNS)
	if err != nil {
		return RefusalCheckFailed, err
	}
	if !ok {
		return RefusalNoGrant, nil
	}
	return RefusalNone, nil
}

// ActionFlags says which write controls the UI offers for an object, so it
// never offers one the server would refuse (KTD3).
type ActionFlags struct {
	CanSnapshot  bool `json:"canSnapshot"`
	CanChangeSLA bool `json:"canChangeSla"`
	// CanRestore means a restore from the object may be offered at all.
	// The target picker then lists only namespaces carrying the restore
	// grant (R18), and each request is checked again.
	CanRestore bool `json:"canRestore"`
}

// ActionFlags computes the write controls for o. CanRestore is the
// eligibility half of CheckAction: everything but the target namespace's
// grant, which depends on the target the user picks.
func (v *Viewer) ActionFlags(ctx context.Context, t Target, o Object) (ActionFlags, error) {
	var f ActionFlags
	r, err := v.CheckAction(ctx, t, ActionSnapshot, o, "")
	if err != nil {
		return ActionFlags{}, err
	}
	f.CanSnapshot = r == RefusalNone
	if r, err = v.CheckAction(ctx, t, ActionChangeSLA, o, ""); err != nil {
		return ActionFlags{}, err
	}
	f.CanChangeSLA = r == RefusalNone
	f.CanRestore, err = v.restoreEligible(ctx, t, o)
	if err != nil {
		return ActionFlags{}, err
	}
	return f, nil
}

// restoreEligible applies CheckAction's restore rules through the same
// helpers, leaving out only the target namespace's checks.
func (v *Viewer) restoreEligible(ctx context.Context, t Target, o Object) (bool, error) {
	if v.writeGate(t) != RefusalNone {
		return false, nil
	}
	if v.admin {
		return true, nil
	}
	_, r, err := v.nonAdminSource(ctx, ActionRestore, o)
	if err != nil {
		return false, err
	}
	return r == RefusalNone, nil
}

// writeGate refuses every write, for admins too, while the writes setting is
// off (R29) or t is not a valid mapping of the viewer's cluster (R2).
func (v *Viewer) writeGate(t Target) Refusal {
	if !v.access.writesEnabled {
		return RefusalWritesDisabled
	}
	if !t.valid || k8s.NormalizedClusterID(t.ClusterID) != v.clusterID {
		return RefusalMappingInvalid
	}
	return RefusalNone
}

// nonAdminSource returns the namespace a non-admin acts in for action on o:
// the object must be current and namespace-scoped, a restore source must not
// hold cluster-scoped resources, and the namespace must exist locally and
// carry the read grant. CheckAction and restoreEligible share it so the
// server's refusal and the UI's restore flag cannot drift apart (KTD3).
func (v *Viewer) nonAdminSource(ctx context.Context, action Action, o Object) (string, Refusal, error) {
	if o.Relic || !o.NamespaceScoped() || (action == ActionRestore && o.MayHoldClusterScoped) {
		return "", RefusalAdminOnly, nil
	}
	source := o.Coverage.Namespaces[0]
	st, readable, err := v.readable(ctx, source)
	if err != nil {
		return "", RefusalCheckFailed, err
	}
	if !st.exists {
		return "", RefusalAdminOnly, nil
	}
	if !readable {
		return "", RefusalNoGrant, nil
	}
	return source, RefusalNone, nil
}

// validNamespaceName reports whether s is a DNS-1123 label, the form of a
// Kubernetes namespace name.
func validNamespaceName(s string) bool {
	return len(validation.IsDNS1123Label(s)) == 0
}

// AllowedNamespaces is the visibility provider the notifications package
// uses for Rubrik feed rows (KTD11). It returns which of candidates user may
// see on clusterID, each with the namespace's creation time, so a row
// created before that time (an earlier namespace's) stays hidden (R6).
// admin is true for an admin, who sees every row, and the map is then nil.
// A non-admin on a remote cluster gets an empty map: those rows are
// admin-only (R10). One failed check fails the whole answer.
func (a *Access) AllowedNamespaces(ctx context.Context, user *auth.User, clusterID string, candidates []string) (allowed map[string]time.Time, admin bool, err error) {
	v, err := a.Viewer(user, clusterID)
	if errors.Is(err, ErrRemoteAdminOnly) {
		return map[string]time.Time{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if v.admin {
		return nil, true, nil
	}
	names, err := v.VisibleNamespaces(ctx, candidates)
	if err != nil {
		return nil, false, err
	}
	allowed = make(map[string]time.Time, len(names))
	for _, ns := range names {
		allowed[ns] = v.namespaces[ns].created
	}
	return allowed, false, nil
}
