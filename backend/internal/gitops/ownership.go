package gitops

// Read-only GitOps ownership evidence for a live object (Release E U26,
// R18, KTD10). The answer to "which controller manages this object?" comes
// only from a controller's own reconciliation record — Argo CD's
// Application status.resources[] or a Flux Kustomization's inventory. Labels,
// annotations and field managers on the object are hints: anyone who can
// write the object can write them, so they only decide which applications
// are checked first and never establish ownership or a Git source.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// maxDetailFetches bounds per-request Application/Kustomization detail GETs.
// Exhausting it yields ConfidenceUnknown/"search-bound-exhausted", never
// OwnedByNone/"no-evidence" -- absence of evidence we did not look for is not
// evidence.
const maxDetailFetches = 25

// maxHintValueBytes caps an untrusted hint value before it reaches a response.
const maxHintValueBytes = 256

// identityBasisNameScoped is the only identity basis Release E can offer:
// neither controller records the UID of what it manages.
const identityBasisNameScoped = "group-kind-namespace-name"

// Reason codes carried in OwnershipResult.Reason. Stable wire values.
const (
	reasonConfirmedArgo   = "confirmed-argo-status"
	reasonConfirmedFlux   = "confirmed-flux-inventory"
	reasonBothClaim       = "both-claim"
	reasonArgoForbidden   = "argo-list-forbidden"
	reasonFluxForbidden   = "flux-list-forbidden"
	reasonArgoUnavailable = "argo-unavailable"
	reasonFluxUnavailable = "flux-unavailable"
	reasonHelmRelease     = "flux-helmrelease-no-inventory"
	reasonBoundExhausted  = "search-bound-exhausted"
	reasonPartial         = "partial-visibility"
	reasonHintsOnly       = "hints-only"
	reasonNoController    = "no-controller-installed"
	reasonNoEvidence      = "no-evidence"
)

// Object metadata the hints are read from.
const (
	argoTrackingIDAnnotation = "argocd.argoproj.io/tracking-id"
	instanceLabel            = "app.kubernetes.io/instance"
	managedByLabel           = "app.kubernetes.io/managed-by"
	fluxKSNameLabel          = "kustomize.toolkit.fluxcd.io/name"
	fluxKSNamespaceLabel     = "kustomize.toolkit.fluxcd.io/namespace"
	fluxHRNameLabel          = "helm.toolkit.fluxcd.io/name"
	fluxHRNamespaceLabel     = "helm.toolkit.fluxcd.io/namespace"
)

// toolState is what one controller's search established for one object,
// short of a confirmation. decide turns it into a verdict.
type toolState struct {
	installed bool
	// unavailable: a list, a detail GET or the access check guarding the
	// list failed, so part of the search space was never seen.
	unavailable bool
	// forbidden: the caller may list none of the controller's applications,
	// or was refused a GET on one it can list.
	forbidden bool
	// hidden: the caller can list some applications but not all of them.
	hidden bool
	// exhausted: maxDetailFetches ran out before every candidate was read.
	exhausted bool
	// helmReleaseHinted: the object carries Flux HelmRelease labels, and a
	// HelmRelease keeps no inventory to confirm them against (flux only).
	helmReleaseHinted bool
}

// objectHints is everything non-authoritative read off one live object.
type objectHints struct {
	evidence []OwnershipEvidence
	// argoApps are Application names the object points at, either "name" or
	// Argo's apps-in-any-namespace "namespace_name".
	argoApps map[string]bool
	// fluxKustomizations are "namespace/name" Kustomizations it points at.
	fluxKustomizations map[string]bool
	helmRelease        bool
}

// ResolveOwnership answers, for each ref, which GitOps controller demonstrably
// manages it on clusterID. Applications come from the same source the list
// endpoint uses for that cluster — the service-account cache for the local
// cluster, a per-identity read for a remote one — and are RBAC-filtered
// against the caller before any matching, so an application the caller
// cannot list contributes no evidence and leaks no repository coordinates.
//
// dynClient must impersonate the caller on clusterID: it reads each candidate
// application's detail and the live object's hints, so a caller who can list
// but not get an application is answered forbidden rather than shown another
// tenant's inventory. Hints are read only for refs that carry Version and
// Resource. The detail-fetch bound and its memo span the whole call.
//
// Never returns a writable Git source (Q4 unresolved). It errors only on a
// cancelled context (with no partial slice) or a missing user or client;
// every cluster-side failure is a per-ref verdict.
func (h *Handler) ResolveOwnership(
	ctx context.Context,
	user *auth.User,
	clusterID string,
	dynClient dynamic.Interface,
	refs []ObjectRef,
) ([]OwnershipResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if user == nil || dynClient == nil {
		return nil, errors.New("gitops: ResolveOwnership needs a user and a dynamic client")
	}
	if len(refs) == 0 {
		return []OwnershipResult{}, nil
	}

	run := &ownershipRun{
		dyn:     dynClient,
		view:    h.ownershipView(ctx, clusterID, user),
		details: map[string]appDetail{},
	}
	observedAt := time.Now().UTC()
	out := make([]OwnershipResult, 0, len(refs))
	for _, ref := range refs {
		res, err := run.resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		res.Object.ClusterID = clusterID
		res.ObservedAt = observedAt
		out = append(out, res)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// clusterView is one cluster's applications as the caller may see them.
type clusterView struct {
	argo, flux toolState
	argoApps   []NormalizedApp
	fluxKS     []NormalizedApp
}

// ownershipView reads clusterID's applications the way the list endpoint
// does for that cluster and applies the caller's RBAC filter.
func (h *Handler) ownershipView(ctx context.Context, clusterID string, user *auth.User) clusterView {
	var (
		v      clusterView
		status GitOpsStatus
		apps   []NormalizedApp
		failed map[string]error
	)
	if k8s.IsLocalClusterID(clusterID) {
		if h.Discoverer == nil {
			return unreadableView(nil)
		}
		status = h.Discoverer.Status()
		var err error
		if apps, err = h.fetchApps(ctx); err != nil {
			return unreadableView(err)
		}
	} else {
		snap, err := h.remoteCache().Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*snapshot, error) {
			return h.fetchRemote(ctx, clusterID, user)
		})
		if err != nil {
			return unreadableView(err)
		}
		status, apps, failed = snap.status, snap.apps, snap.failed
	}

	v.argo.installed = status.ArgoCD != nil && status.ArgoCD.Available
	v.flux.installed = status.FluxCD != nil && status.FluxCD.Available
	markReadFailure(&v.argo, failed[ArgoApplicationGVR.Resource])
	markReadFailure(&v.flux, failed[FluxKustomizationGVR.Resource])

	vis := h.visibleApps(ctx, clusterID, user, apps)
	for _, app := range vis.apps {
		switch toolPrefixForApp(app) {
		case "argo":
			v.argoApps = append(v.argoApps, app)
		case "flux-ks":
			v.fluxKS = append(v.fluxKS, app)
		}
	}
	// HelmReleases cannot confirm anything, so only the prefixes that can
	// shape a tool's visibility.
	applyVisibility(&v.argo, vis, "argo", len(v.argoApps))
	applyVisibility(&v.flux, vis, "flux-ks", len(v.fluxKS))
	return v
}

// unreadableView is a cluster whose applications could not be read at all:
// both controllers count as installed and unseen.
func unreadableView(err error) clusterView {
	v := clusterView{argo: toolState{installed: true}, flux: toolState{installed: true}}
	if err == nil {
		err = errors.New("no discoverer")
	}
	markReadFailure(&v.argo, err)
	markReadFailure(&v.flux, err)
	return v
}

// markReadFailure records a failed read: a refusal is forbidden, anything
// else unavailable.
func markReadFailure(st *toolState, err error) {
	switch {
	case err == nil:
	case apierrors.IsForbidden(err):
		st.forbidden = true
	default:
		st.unavailable = true
	}
}

// applyVisibility folds the RBAC filter's answer for one tool prefix into st.
func applyVisibility(st *toolState, vis appVisibility, prefix string, visible int) {
	if vis.failed[prefix] > 0 {
		st.unavailable = true
	}
	if vis.denied[prefix] > 0 {
		if visible == 0 {
			st.forbidden = true
		} else {
			st.hidden = true
		}
	}
}

// appDetail is one application's managed resources, or why they are unknown.
type appDetail struct {
	resources []ManagedResource
	forbidden bool
	failed    bool
}

// ownershipRun is one ResolveOwnership call: the cluster view, the caller's
// client, and the detail memo and budget shared across its refs.
type ownershipRun struct {
	dyn     dynamic.Interface
	view    clusterView
	details map[string]appDetail
	fetches int
}

func (r *ownershipRun) resolve(ctx context.Context, ref ObjectRef) (OwnershipResult, error) {
	hints, err := r.liveHints(ctx, ref)
	if err != nil {
		return OwnershipResult{}, err
	}
	argo, flux := r.view.argo, r.view.flux
	flux.helmReleaseHinted = hints.helmRelease

	ev := hints.evidence
	var apps []OwnedByApp
	searches := []struct {
		apps   []NormalizedApp
		st     *toolState
		kind   OwnershipEvidenceKind
		hinted func(NormalizedApp) bool
	}{
		{r.view.argoApps, &argo, EvidenceArgoStatusResource, func(a NormalizedApp) bool {
			return hints.argoApps[a.Name] || hints.argoApps[a.Namespace+"_"+a.Name]
		}},
		{r.view.fluxKS, &flux, EvidenceFluxInventoryEntry, func(a NormalizedApp) bool {
			return hints.fluxKustomizations[a.Namespace+"/"+a.Name]
		}},
	}
	for _, s := range searches {
		owner, err := r.search(ctx, ref, s.apps, s.hinted, s.st)
		if err != nil {
			return OwnershipResult{}, err
		}
		if owner == nil {
			continue
		}
		ev = append(ev, OwnershipEvidence{Kind: s.kind, Tool: owner.Tool, AppID: owner.ID})
		apps = append(apps, OwnedByApp{
			AppID:     owner.ID,
			Tool:      owner.Tool,
			Kind:      owner.Kind,
			Namespace: owner.Namespace,
			Name:      owner.Name,
			Source:    owner.Source,
			Suspended: owner.Suspended,
		})
	}
	return decide(ref, ev, apps, argo, flux), nil
}

// liveHints reads the live object as the caller and extracts its hints. An
// object that is missing, unreadable or unaddressable simply has none.
func (r *ownershipRun) liveHints(ctx context.Context, ref ObjectRef) (objectHints, error) {
	if ref.Version == "" || ref.Resource == "" || ref.Name == "" {
		return objectHints{}, nil
	}
	gvr := schema.GroupVersionResource{Group: ref.Group, Version: ref.Version, Resource: ref.Resource}
	var ri dynamic.ResourceInterface = r.dyn.Resource(gvr)
	if ref.Namespace != "" {
		ri = r.dyn.Resource(gvr).Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return objectHints{}, ctx.Err()
	}
	return hintsFor(obj), nil
}

// search walks one controller's visible applications — hinted ones first,
// then those targeting the object's namespace, then the rest — and returns
// the first whose own record names ref. Every way the walk falls short of
// the whole space is recorded on st.
func (r *ownershipRun) search(ctx context.Context, ref ObjectRef, apps []NormalizedApp, hinted func(NormalizedApp) bool, st *toolState) (*NormalizedApp, error) {
	for _, app := range orderCandidates(apps, ref, hinted) {
		d, ok, err := r.detail(ctx, app)
		if err != nil {
			return nil, err
		}
		if !ok {
			st.exhausted = true
			return nil, nil
		}
		switch {
		case d.forbidden:
			st.forbidden = true
		case d.failed:
			st.unavailable = true
		}
		for _, mr := range d.resources {
			if matchesRef(mr, ref) {
				return &app, nil
			}
		}
	}
	return nil, nil
}

// orderCandidates returns apps in search order without reordering the input.
func orderCandidates(apps []NormalizedApp, ref ObjectRef, hinted func(NormalizedApp) bool) []NormalizedApp {
	rank := func(a NormalizedApp) int {
		switch {
		case hinted(a):
			return 0
		case ref.Namespace != "" && (a.DestinationNamespace == ref.Namespace || a.Namespace == ref.Namespace):
			return 1
		default:
			return 2
		}
	}
	out := append([]NormalizedApp(nil), apps...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// detail returns an Argo Application's or Flux Kustomization's managed
// resources, fetching them as the caller at most once per run. ok is false
// when the fetch budget is spent.
func (r *ownershipRun) detail(ctx context.Context, app NormalizedApp) (appDetail, bool, error) {
	if d, seen := r.details[app.ID]; seen {
		return d, true, nil
	}
	if r.fetches >= maxDetailFetches {
		return appDetail{}, false, nil
	}
	r.fetches++

	var (
		detail *AppDetail
		err    error
	)
	if app.Tool == ToolArgoCD {
		detail, err = GetArgoAppDetail(ctx, r.dyn, app.Namespace, app.Name)
	} else {
		detail, err = GetFluxAppDetail(ctx, r.dyn, "Kustomization", app.Namespace, app.Name)
	}

	var d appDetail
	switch {
	case err == nil:
		d.resources = detail.Resources
	case ctx.Err() != nil:
		return appDetail{}, true, ctx.Err()
	case apierrors.IsNotFound(err):
		// Deleted since the list was cached: it manages nothing now.
	case apierrors.IsForbidden(err):
		d.forbidden = true
	default:
		d.failed = true
	}
	r.details[app.ID] = d
	return d, true, nil
}

// matchesRef reports whether a ManagedResource names exactly this ref, on
// (group, kind, namespace, name). Case-sensitive on kind, per the API. A ref
// without a kind or name names nothing.
func matchesRef(mr ManagedResource, ref ObjectRef) bool {
	if ref.Kind == "" || ref.Name == "" {
		return false
	}
	return mr.Group == ref.Group && mr.Kind == ref.Kind && mr.Namespace == ref.Namespace && mr.Name == ref.Name
}

// decide applies the KTD10 rule and is its single chokepoint: an application
// is listed, and a controller confirmed, only when evidence whose
// Kind.Authoritative() is true names that application for that tool. Hints
// can only move an unconfirmed verdict between unknown reasons.
func decide(ref ObjectRef, ev []OwnershipEvidence, apps []OwnedByApp, argo, flux toolState) OwnershipResult {
	res := OwnershipResult{
		Object:            ref,
		Controller:        OwnedByNone,
		Evidence:          ev,
		IdentityBasis:     identityBasisNameScoped,
		UIDConfirmed:      false,
		WritableGitSource: false,
	}

	type claim struct {
		appID string
		tool  Tool
	}
	confirmed := map[claim]bool{}
	hasHints := false
	for _, e := range ev {
		if e.Kind.Authoritative() {
			if e.AppID != "" {
				confirmed[claim{e.AppID, e.Tool}] = true
			}
		} else {
			hasHints = true
		}
	}
	var argoConfirmed, fluxConfirmed bool
	for _, a := range apps {
		if !confirmed[claim{a.AppID, a.Tool}] {
			continue
		}
		switch a.Tool {
		case ToolArgoCD:
			argoConfirmed = true
		case ToolFluxCD:
			fluxConfirmed = true
		default:
			continue
		}
		res.Apps = append(res.Apps, a)
	}

	switch {
	case argoConfirmed && fluxConfirmed:
		res.Controller, res.Confidence, res.Reason = OwnedByBoth, ConfidenceConflicting, reasonBothClaim
	case argoConfirmed:
		res.Controller, res.Confidence, res.Reason = OwnedByArgoCD, ConfidenceConfirmed, reasonConfirmedArgo
	case fluxConfirmed:
		res.Controller, res.Confidence, res.Reason = OwnedByFluxCD, ConfidenceConfirmed, reasonConfirmedFlux
	default:
		res.Confidence, res.Reason = unconfirmedVerdict(hasHints, argo, flux)
	}
	return res
}

// unconfirmedVerdict explains why nothing was confirmed, most limiting
// first: a search the caller could not see beats one that failed, which
// beats one that saw everything it was allowed to and found only hints.
func unconfirmedVerdict(hasHints bool, argo, flux toolState) (OwnershipConfidence, string) {
	switch {
	case argo.forbidden:
		return ConfidenceForbidden, reasonArgoForbidden
	case flux.forbidden:
		return ConfidenceForbidden, reasonFluxForbidden
	case argo.unavailable:
		return ConfidenceUnavailable, reasonArgoUnavailable
	case flux.unavailable:
		return ConfidenceUnavailable, reasonFluxUnavailable
	case flux.helmReleaseHinted:
		return ConfidenceUnknown, reasonHelmRelease
	case argo.exhausted || flux.exhausted:
		return ConfidenceUnknown, reasonBoundExhausted
	case argo.hidden || flux.hidden:
		return ConfidenceUnknown, reasonPartial
	case hasHints:
		return ConfidenceUnknown, reasonHintsOnly
	case !argo.installed && !flux.installed:
		return ConfidenceUnavailable, reasonNoController
	default:
		return ConfidenceUnknown, reasonNoEvidence
	}
}

// hintsFor extracts every non-authoritative signal from a live object. Every
// value is untrusted: it is sanitized before it is placed in evidence and is
// used otherwise only as a lookup key against applications the caller may
// already list.
func hintsFor(obj *unstructured.Unstructured) objectHints {
	hints := objectHints{argoApps: map[string]bool{}, fluxKustomizations: map[string]bool{}}
	if obj == nil {
		return hints
	}
	add := func(kind OwnershipEvidenceKind, tool Tool, raw, note string) {
		hints.evidence = append(hints.evidence, OwnershipEvidence{Kind: kind, Tool: tool, RawValue: sanitizeHint(raw), Note: note})
	}
	labels, annotations := obj.GetLabels(), obj.GetAnnotations()

	if v := annotations[argoTrackingIDAnnotation]; v != "" {
		note := "unparseable tracking id"
		if app, group, kind, ns, name, ok := parseArgoTrackingID(v); ok {
			hints.argoApps[app] = true
			note = ""
			gvk := obj.GroupVersionKind()
			if group != gvk.Group || kind != gvk.Kind || ns != obj.GetNamespace() || name != obj.GetName() {
				note = "tracking id names a different object"
			}
		}
		add(EvidenceArgoTrackingID, ToolArgoCD, v, note)
	}
	if v := labels[instanceLabel]; v != "" {
		hints.argoApps[v] = true
		add(EvidenceInstanceLabel, ToolArgoCD, v, "argo cd label tracking; also set by helm and others")
	}
	if v := labels[managedByLabel]; v != "" {
		if tool := toolNamedBy(v); tool != ToolNone {
			add(EvidenceManagedByLabel, tool, v, "")
		}
	}
	if name := labels[fluxKSNameLabel]; name != "" {
		ns := labels[fluxKSNamespaceLabel]
		hints.fluxKustomizations[ns+"/"+name] = true
		add(EvidenceFluxOwnerLabel, ToolFluxCD, ns+"/"+name, "kustomization")
	}
	if name := labels[fluxHRNameLabel]; name != "" {
		hints.helmRelease = true
		add(EvidenceFluxOwnerLabel, ToolFluxCD, labels[fluxHRNamespaceLabel]+"/"+name, "helmrelease")
	}
	for _, manager := range fieldManagers(obj) {
		if tool := toolNamedBy(manager); tool != ToolNone {
			add(EvidenceFieldManager, tool, manager, "")
		}
	}
	return hints
}

// fieldManagers lists the distinct managers in metadata.managedFields,
// tolerating any shape: the field is read without a deep copy, which would
// panic on non-JSON values.
func fieldManagers(obj *unstructured.Unstructured) []string {
	raw, found, err := unstructured.NestedFieldNoCopy(obj.Object, "metadata", "managedFields")
	entries, ok := raw.([]any)
	if !found || err != nil || !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		manager, ok := m["manager"].(string)
		if !ok || manager == "" || seen[manager] {
			continue
		}
		seen[manager] = true
		out = append(out, manager)
	}
	return out
}

// toolNamedBy maps a managed-by value or field manager to the controller it
// names, if any.
func toolNamedBy(v string) Tool {
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(lower, "argocd"), strings.HasPrefix(lower, "argo-cd"):
		return ToolArgoCD
	case strings.HasPrefix(lower, "flux"), lower == "kustomize-controller", lower == "helm-controller":
		return ToolFluxCD
	default:
		return ToolNone
	}
}

// parseArgoTrackingID parses "app:group/Kind:namespace/name" from the
// argocd.argoproj.io/tracking-id annotation. UNTRUSTED input: the value is
// written by whoever can write the object. Returns a HINT only.
func parseArgoTrackingID(v string) (appName, group, kind, namespace, name string, ok bool) {
	parts := strings.SplitN(v, ":", 3)
	if len(parts) != 3 || parts[0] == "" {
		return "", "", "", "", "", false
	}
	g, k, found := strings.Cut(parts[1], "/")
	if !found || k == "" {
		return "", "", "", "", "", false
	}
	ns, n, found := strings.Cut(parts[2], "/")
	if !found || n == "" {
		return "", "", "", "", "", false
	}
	return parts[0], g, k, ns, n, true
}

// sanitizeHint makes an untrusted value safe to echo: valid UTF-8, no
// control or format (bidi override) characters, at most maxHintValueBytes.
func sanitizeHint(v string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(v, "") {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > maxHintValueBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
