package rubrik

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// The read endpoints (U6) serve every authenticated user. Visibility is
// computed per request after the shared cache (KTD3, KTD15): admins see every
// namespace and object of a mapped cluster, relics included; a non-admin sees
// only what U4's rules allow, and only on the local cluster (R10).

// FreshnessView says how current a part of a read answer is (R13). Error is
// the failed fetch's reason class (for example "unreachable" or
// "auth_failed") when the data are the last good answer served stale; the
// error text itself is only logged.
type FreshnessView struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Stale     bool      `json:"stale"`
	Error     string    `json:"error,omitempty"`
}

// ObjectView is one RSC object as a user sees it: its SLA assignment
// (R11), its persisted compliance, and the write controls the UI may offer.
type ObjectView struct {
	Object
	Compliance Compliance `json:"compliance"`
	ActionFlags
}

// NamespaceRow is one namespace in the namespace list and the head of the
// namespace detail. Protected and Compliance count every current object
// covering the namespace (KTD4), so a namespace is never shown unprotected
// because the object protecting it is one the user may not see; the object
// ids, action targets and last snapshot are limited to the objects the user
// sees. HiddenObjects says that more objects cover the namespace than are
// listed. The action flags allow an action when any action target allows it;
// the dialog then offers the targets (R15) and each request is checked again.
type NamespaceRow struct {
	NamespaceSummary
	HiddenObjects bool `json:"hiddenObjects,omitempty"`
	ActionFlags
}

// NamespaceList answers GET /rubrik/namespaces. NoAccess is true when the
// user holds the Rubrik read grant on no namespace of the cluster (R9); the
// list is then empty and the answer names no namespace.
type NamespaceList struct {
	NoAccess   bool           `json:"noAccess"`
	Namespaces []NamespaceRow `json:"namespaces"`
	// Objects are the objects the rows reference, each listed once.
	Objects   []ObjectView  `json:"objects"`
	Freshness FreshnessView `json:"freshness"`
}

// ObjectDetail is an object with its history, as GET
// /rubrik/namespaces/{namespace} returns it (R12). A history part whose
// fetch failed with no earlier answer to serve is empty and carries the
// failure's reason class instead of freshness.
type ObjectDetail struct {
	ObjectView
	Snapshots          []Snapshot     `json:"snapshots"`
	SnapshotsFreshness *FreshnessView `json:"snapshotsFreshness,omitempty"`
	SnapshotsError     string         `json:"snapshotsError,omitempty"`
	Jobs               []Job          `json:"jobs"`
	JobsFreshness      *FreshnessView `json:"jobsFreshness,omitempty"`
	JobsError          string         `json:"jobsError,omitempty"`
}

// NamespaceDetail answers GET /rubrik/namespaces/{namespace}.
type NamespaceDetail struct {
	Namespace NamespaceRow   `json:"namespace"`
	Objects   []ObjectDetail `json:"objects"`
	Freshness FreshnessView  `json:"freshness"`
}

// SLADomainList answers GET /rubrik/sla-domains.
type SLADomainList struct {
	NoAccess   bool          `json:"noAccess"`
	SLADomains []SLADomain   `json:"slaDomains"`
	Freshness  FreshnessView `json:"freshness"`
}

const (
	msgAccessCheck   = "Rubrik access could not be checked"
	msgRemoteAdmin   = "Rubrik data on remote clusters is available to admins only"
	msgNotSetUp      = "Rubrik is not set up for this cluster"
	msgNamespaceGone = "namespace not found"
)

// readRequest is what every read starts from: the user's view of the
// selected cluster.
type readRequest struct {
	viewer    *Viewer
	clusterID string
}

// startRead builds the viewer for the selected cluster. A non-admin asking
// about a remote cluster is refused before any store or RSC call (R10,
// AE10); the platform's cluster gate refuses first, this is the second line.
func (h *Handler) startRead(w http.ResponseWriter, r *http.Request) (readRequest, bool) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return readRequest{}, false
	}
	clusterID := k8s.NormalizedClusterID(middleware.ClusterIDFromContext(r.Context()))
	v, err := h.access.Viewer(user, clusterID)
	switch {
	case errors.Is(err, ErrRemoteAdminOnly):
		httputil.WriteErrorWithReason(w, http.StatusForbidden, msgRemoteAdmin, StateAdminOnly, nil)
		return readRequest{}, false
	case err != nil:
		h.writeAccessError(w, err)
		return readRequest{}, false
	}
	return readRequest{viewer: v, clusterID: clusterID}, true
}

// readableAnywhere reports whether a non-admin holds the read grant on any
// local namespace, which decides the noAccess answer (R9). It is checked
// before the mapping or RSC, so a user without access learns nothing about
// the Rubrik setup either.
func readableAnywhere(ctx context.Context, v *Viewer) ([]string, error) {
	all, err := v.allNamespaces()
	if err != nil {
		return nil, err
	}
	return v.VisibleNamespaces(ctx, all)
}

// resolve reads the cluster's mapping, writing the refusal when it is not
// usable. A non-admin sees one neutral state for every setup problem (R22).
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, rr readRequest) (Target, bool) {
	t, err := h.service.Resolve(r.Context(), rr.clusterID)
	if err != nil {
		h.writeReadError(w, rr, err)
		return Target{}, false
	}
	return t, true
}

// writeReadError answers a failed read. Setup problems are 409, collapsed to
// not_set_up for non-admins (R22); a store or access failure is 503; an RSC
// failure with no earlier answer to serve takes the client's class (R13).
// The error is logged, never returned: it may name an address.
func (h *Handler) writeReadError(w http.ResponseWriter, rr readRequest, err error) {
	setup := ""
	switch {
	case errors.Is(err, ErrNotConfigured):
		setup = StateNotConfigured
	case errors.Is(err, ErrNotMapped):
		setup = StateNotMapped
	case errors.Is(err, ErrMappingInvalid):
		setup = StateMappingInvalid
	case errors.Is(err, ErrTargetMissing):
		setup = "rsc_cluster_missing"
	}
	if setup != "" {
		if !rr.viewer.Admin() {
			setup = StateNotSetUp
		}
		httputil.WriteErrorWithReason(w, http.StatusConflict, msgNotSetUp, setup, nil)
		return
	}
	switch {
	case errors.Is(err, ErrStoreUnavailable):
		h.logger.Error("rubrik read: store unavailable", "cluster", rr.clusterID, "error", err)
		httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgStoreUnavailable, "store_unavailable", nil)
	case errors.Is(err, ErrAccessCheck):
		h.writeAccessError(w, err)
	default:
		status, reason, msg := classifyRSCError(err)
		h.logger.Warn("rubrik read: rsc fetch failed", "cluster", rr.clusterID, "reason", reason, "error", err)
		httputil.WriteErrorWithReason(w, status, msg, reason, nil)
	}
}

func (h *Handler) writeAccessError(w http.ResponseWriter, err error) {
	h.logger.Error("rubrik read: access check failed", "error", err)
	httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable, msgAccessCheck, "access_check_failed", nil)
}

// freshnessView renders cached data's freshness; a failed fetch's error is
// logged and reported by class only.
func (h *Handler) freshnessView(f Freshness, what string) FreshnessView {
	out := FreshnessView{FetchedAt: f.FetchedAt, Stale: f.Stale}
	if f.Err != nil {
		_, out.Error, _ = classifyRSCError(f.Err)
		h.logger.Warn("rubrik read: serving stale data", "data", what, "reason", out.Error, "error", f.Err)
	}
	return out
}

// HandleNamespaces answers GET /rubrik/namespaces: every namespace the user
// may see with its protection, SLA assignments, compliance and newest
// snapshot (R11), and the objects behind them.
func (h *Handler) HandleNamespaces(w http.ResponseWriter, r *http.Request) {
	rr, ok := h.startRead(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v := rr.viewer

	var candidates []string
	if !v.Admin() {
		visible, err := readableAnywhere(ctx, v)
		if err != nil {
			h.writeAccessError(w, err)
			return
		}
		if len(visible) == 0 {
			httputil.WriteData(w, NamespaceList{NoAccess: true, Namespaces: []NamespaceRow{}, Objects: []ObjectView{}})
			return
		}
		candidates = visible
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
	compliance, err := h.service.Compliance(ctx, t)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}

	if v.Admin() {
		// An admin sees every namespace of the cluster: the local ones, and
		// every namespace an object names, including earlier namespaces
		// that no longer exist. A remote cluster has no local informer, so
		// there the objects are the only source.
		if k8s.IsLocalClusterID(rr.clusterID) {
			all, err := v.allNamespaces()
			if err != nil {
				h.writeAccessError(w, err)
				return
			}
			candidates = append(candidates, all...)
		}
		for _, o := range view.Objects {
			candidates = append(candidates, o.Coverage.Namespaces...)
		}
		if candidates, err = v.VisibleNamespaces(ctx, candidates); err != nil {
			h.writeAccessError(w, err)
			return
		}
	}

	visible, err := h.visibleObjectViews(ctx, v, t, view.Objects, compliance)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}
	out := NamespaceList{
		Namespaces: make([]NamespaceRow, 0, len(candidates)),
		Objects:    []ObjectView{},
		Freshness:  h.freshnessView(view.Freshness, "objects"),
	}
	referenced := map[string]bool{}
	for _, ns := range candidates {
		row := namespaceRow(ns, view.Objects, visible, compliance)
		for _, id := range row.ObjectIDs {
			referenced[strings.ToLower(id)] = true
		}
		out.Namespaces = append(out.Namespaces, row)
	}
	for _, ov := range visible {
		if referenced[strings.ToLower(ov.ID)] {
			out.Objects = append(out.Objects, ov)
		}
	}
	httputil.WriteData(w, out)
}

// HandleNamespace answers GET /rubrik/namespaces/{namespace}: the objects
// covering one namespace with their snapshots and recent jobs (R12). A
// namespace a non-admin may not read is a 404, never a 403, so the answer
// does not confirm that Rubrik knows it.
func (h *Handler) HandleNamespace(w http.ResponseWriter, r *http.Request) {
	rr, ok := h.startRead(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v := rr.viewer
	ns := chi.URLParam(r, "namespace")
	if !validNamespaceName(ns) {
		httputil.WriteError(w, http.StatusNotFound, msgNamespaceGone, "")
		return
	}
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
	compliance, err := h.service.Compliance(ctx, t)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}
	visible, err := h.visibleObjectViews(ctx, v, t, view.Objects, compliance)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}
	row := namespaceRow(ns, view.Objects, visible, compliance)

	out := NamespaceDetail{Namespace: row, Objects: []ObjectDetail{}, Freshness: h.freshnessView(view.Freshness, "objects")}
	for _, ov := range visible {
		// Admins also get relics covering the namespace, with their
		// snapshots (KTD4); a non-admin never sees a relic.
		if !ov.Coverage.Covers(ns) {
			continue
		}
		d, err := h.objectDetail(ctx, rr, t, ov)
		if err != nil {
			h.writeReadError(w, rr, err)
			return
		}
		out.Objects = append(out.Objects, d)
	}
	httputil.WriteData(w, out)
}

// objectDetail fetches an object's snapshots and jobs and filters them for
// the viewer. A history fetch that failed with no earlier answer leaves that
// part empty with its reason class, so one object's failure does not hide
// the others; an access check that fails fails the request.
func (h *Handler) objectDetail(ctx context.Context, rr readRequest, t Target, ov ObjectView) (ObjectDetail, error) {
	v := rr.viewer
	d := ObjectDetail{ObjectView: ov, Snapshots: []Snapshot{}, Jobs: []Job{}}

	o, snaps, f, err := h.service.Snapshots(ctx, t, ov.ID, 0)
	switch {
	case errors.Is(err, ErrObjectNotFound):
		// The object list changed under the request; it has no history to
		// show here.
	case err != nil:
		if requestWide(err) {
			return ObjectDetail{}, err
		}
		_, d.SnapshotsError, _ = classifyRSCError(err)
		h.logger.Warn("rubrik read: snapshot fetch failed", "cluster", rr.clusterID, "reason", d.SnapshotsError, "error", err)
	default:
		if d.Snapshots, err = v.VisibleSnapshots(ctx, o, snaps); err != nil {
			return ObjectDetail{}, err
		}
		fv := h.freshnessView(f, "snapshots")
		d.SnapshotsFreshness = &fv
	}

	o, jobs, f, err := h.service.Jobs(ctx, t, ov.ID, 0)
	switch {
	case errors.Is(err, ErrObjectNotFound):
	case err != nil:
		if requestWide(err) {
			return ObjectDetail{}, err
		}
		_, d.JobsError, _ = classifyRSCError(err)
		h.logger.Warn("rubrik read: job fetch failed", "cluster", rr.clusterID, "reason", d.JobsError, "error", err)
	default:
		if d.Jobs, err = v.VisibleJobs(ctx, o, jobs); err != nil {
			return ObjectDetail{}, err
		}
		fv := h.freshnessView(f, "jobs")
		d.JobsFreshness = &fv
	}
	return d, nil
}

// requestWide reports whether a history fetch failed for a reason that
// fails the whole request rather than one object's history: the store, the
// mapping or the mapped RSC cluster.
func requestWide(err error) bool {
	return errors.Is(err, ErrStoreUnavailable) || errors.Is(err, ErrMappingInvalid) ||
		errors.Is(err, ErrNotConfigured) || errors.Is(err, ErrTargetMissing)
}

// HandleSLADomains answers GET /rubrik/sla-domains: the SLA domains the
// service account can see, which the SLA and snapshot dialogs offer. A user
// with the read grant nowhere gets noAccess and no domains.
func (h *Handler) HandleSLADomains(w http.ResponseWriter, r *http.Request) {
	rr, ok := h.startRead(w, r)
	if !ok {
		return
	}
	if !rr.viewer.Admin() {
		visible, err := readableAnywhere(r.Context(), rr.viewer)
		if err != nil {
			h.writeAccessError(w, err)
			return
		}
		if len(visible) == 0 {
			httputil.WriteData(w, SLADomainList{NoAccess: true, SLADomains: []SLADomain{}})
			return
		}
	}
	t, ok := h.resolve(w, r, rr)
	if !ok {
		return
	}
	domains, f, err := h.service.SLADomains(r.Context(), t)
	if err != nil {
		h.writeReadError(w, rr, err)
		return
	}
	// The cached slice is shared; sort a copy.
	sorted := slices.Clone(domains)
	slices.SortFunc(sorted, func(a, b SLADomain) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	httputil.WriteData(w, SLADomainList{SLADomains: sorted, Freshness: h.freshnessView(f, "sla domains")})
}

// visibleObjectViews filters the cluster's objects for the viewer, sorted by
// name and id, each with its compliance and write controls. For a non-admin
// it also drops history the viewer may not see from the object itself: the
// newest snapshot time of an object that is not namespace-scoped, or that
// predates its namespace, belongs to snapshots only admins see (R6, R7).
func (h *Handler) visibleObjectViews(ctx context.Context, v *Viewer, t Target, objects []Object, compliance func(Object) Compliance) ([]ObjectView, error) {
	visible, err := v.VisibleObjects(ctx, objects)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(visible, func(a, b Object) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	out := make([]ObjectView, 0, len(visible))
	for _, o := range visible {
		if !v.Admin() {
			if o, err = v.clampHistory(o); err != nil {
				return nil, err
			}
		}
		flags, err := v.ActionFlags(ctx, t, o)
		if err != nil {
			return nil, err
		}
		out = append(out, ObjectView{Object: o, Compliance: compliance(o), ActionFlags: flags})
	}
	return out, nil
}

// clampHistory hides an object's newest snapshot time from a non-admin when
// VisibleSnapshots would hide that snapshot.
func (v *Viewer) clampHistory(o Object) (Object, error) {
	if o.NewestSnapshot == nil {
		return o, nil
	}
	if !o.NamespaceScoped() {
		o.NewestSnapshot = nil
		return o, nil
	}
	st, err := v.namespace(o.Coverage.Namespaces[0])
	if err != nil {
		return Object{}, err
	}
	if !st.exists || o.NewestSnapshot.Before(st.created) {
		o.NewestSnapshot = nil
	}
	return o, nil
}

// namespaceRow builds ns's row. Protection and compliance come from every
// current object of the cluster (KTD4); the listed objects, action targets,
// last snapshot and action flags come from the objects the viewer sees,
// whose history visibleObjectViews has already clamped.
func namespaceRow(ns string, all []Object, visible []ObjectView, compliance func(Object) Compliance) NamespaceRow {
	full := SummarizeNamespace(ns, all, compliance)

	seen := make([]Object, 0, len(visible))
	flags := make(map[string]ActionFlags, len(visible))
	for _, ov := range visible {
		seen = append(seen, ov.Object)
		flags[strings.ToLower(ov.ID)] = ov.ActionFlags
	}
	shown := SummarizeNamespace(ns, seen, compliance)

	row := NamespaceRow{NamespaceSummary: shown, HiddenObjects: len(full.ObjectIDs) > len(shown.ObjectIDs)}
	row.Protected = full.Protected
	row.Compliance = full.Compliance
	for _, id := range shown.ActionTargets {
		f := flags[strings.ToLower(id)]
		row.CanSnapshot = row.CanSnapshot || f.CanSnapshot
		row.CanChangeSLA = row.CanChangeSLA || f.CanChangeSLA
		row.CanRestore = row.CanRestore || f.CanRestore
	}
	return row
}
