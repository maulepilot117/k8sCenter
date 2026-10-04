package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

var deploymentsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// webRef is the live object every scenario asks about.
var webRef = ObjectRef{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "prod", Name: "web"}

var (
	adminUser = &auth.User{Username: "admin", KubernetesUsername: "admin", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}}
	devUser   = &auth.User{Username: "dev", KubernetesUsername: "dev", KubernetesGroups: []string{"devs"}}
)

// webStatusResource is web's entry in an Argo Application's status.resources.
func webStatusResource() map[string]any {
	return map[string]any{"group": "apps", "kind": "Deployment", "namespace": "prod", "name": "web", "status": "Synced"}
}

// argoAppManaging is an Argo Application whose status.resources lists rs.
func argoAppManaging(ns, name, repo, destNS string, rs ...map[string]any) *unstructured.Unstructured {
	u := argoApp(ns, name, repo)
	if destNS != "" {
		_ = unstructured.SetNestedField(u.Object, destNS, "spec", "destination", "namespace")
	}
	items := make([]any, 0, len(rs))
	for _, r := range rs {
		items = append(items, r)
	}
	_ = unstructured.SetNestedSlice(u.Object, items, "status", "resources")
	return u
}

// fluxKustomization is a Flux Kustomization whose inventory holds ids.
func fluxKustomization(ns, name, repoPath string, ids ...string) *unstructured.Unstructured {
	u := obj("Kustomization", ns, name, nil, map[string]any{"path": repoPath, "sourceRef": map[string]any{"kind": "GitRepository", "name": name}})
	entries := make([]any, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, map[string]any{"id": id, "v": "v1"})
	}
	_ = unstructured.SetNestedSlice(u.Object, entries, "status", "inventory", "entries")
	return u
}

// liveWeb is the live Deployment the refs point at.
func liveWeb(labels, annotations map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "prod", "uid": "new-uid"},
	}}
	if labels != nil {
		u.SetLabels(labels)
	}
	if annotations != nil {
		u.SetAnnotations(annotations)
	}
	return u
}

type ownershipFixture struct {
	h   *Handler
	dyn *dynfake.FakeDynamicClient // both the service-account view and the caller's client
}

// newLocalOwnership builds a Handler for the local cluster whose
// service-account cache and caller client both read one fake cluster.
func newLocalOwnership(t *testing.T, argo, flux bool, ac *resources.AccessChecker, live *unstructured.Unstructured, apps ...*unstructured.Unstructured) *ownershipFixture {
	t.Helper()
	c := newFakeCluster(t, toolLists(argo, flux), apps...)
	if live != nil {
		if err := c.dyn.Tracker().Create(deploymentsGVR, live, live.GetNamespace()); err != nil {
			t.Fatalf("seed live object: %v", err)
		}
	}
	status := GitOpsStatus{}
	if argo {
		status.ArgoCD = &ToolDetail{Available: true}
	}
	if flux {
		status.FluxCD = &ToolDetail{Available: true}
	}
	return &ownershipFixture{
		h: &Handler{
			AccessChecker:   ac,
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			Discoverer:      &GitOpsDiscoverer{status: &status},
			baseDynOverride: c.dyn,
		},
		dyn: c.dyn,
	}
}

func (f *ownershipFixture) resolveOne(t *testing.T, user *auth.User, ref ObjectRef) OwnershipResult {
	t.Helper()
	got, err := f.h.ResolveOwnership(context.Background(), user, "local", f.dyn, []ObjectRef{ref})
	if err != nil {
		t.Fatalf("ResolveOwnership: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	return got[0]
}

func wantVerdict(t *testing.T, got OwnershipResult, controller OwnershipController, confidence OwnershipConfidence, reason string) {
	t.Helper()
	if got.Controller != controller || got.Confidence != confidence || got.Reason != reason {
		t.Fatalf("verdict = %s/%s/%s, want %s/%s/%s", got.Controller, got.Confidence, got.Reason, controller, confidence, reason)
	}
	if got.WritableGitSource {
		t.Error("WritableGitSource = true; it is false on every path in Release E")
	}
	if got.IdentityBasis != "group-kind-namespace-name" || got.UIDConfirmed {
		t.Errorf("identity = %q/uidConfirmed=%v, want name-scoped and unconfirmed", got.IdentityBasis, got.UIDConfirmed)
	}
}

// wantNoLeak asserts none of the given strings appear anywhere in the
// serialized result.
func wantNoLeak(t *testing.T, got OwnershipResult, secrets ...string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Errorf("result leaks %q: %s", s, b)
		}
	}
}

func appIDs(apps []OwnedByApp) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.AppID)
	}
	return out
}

func TestResolveOwnership_ConfirmedArgo(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		argoAppManaging("argocd", "other", "https://git.example/other", "prod"),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
	if len(got.Apps) != 1 || got.Apps[0].AppID != "argo:argocd:web-app" || got.Apps[0].Source.RepoURL != "https://git.example/web" || got.Apps[0].Kind != "Application" {
		t.Fatalf("apps = %+v, want web-app with its source", got.Apps)
	}
	var confirming bool
	for _, e := range got.Evidence {
		if e.Kind == EvidenceArgoStatusResource && e.AppID == "argo:argocd:web-app" && e.Tool == ToolArgoCD {
			confirming = true
		}
	}
	if !confirming {
		t.Errorf("evidence = %+v, want the argo-status-resource entry", got.Evidence)
	}
	wantObject := webRef
	wantObject.ClusterID = "local"
	if got.Object != wantObject {
		t.Errorf("object = %+v, want the ref on the resolved cluster", got.Object)
	}
	if got.ObservedAt.IsZero() {
		t.Error("ObservedAt not set")
	}
}

func TestResolveOwnership_ConfirmedFluxKustomization(t *testing.T) {
	f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		fluxKustomization("flux-system", "apps", "./apps/prod", "prod_web_apps_Deployment", "prod_web__Service"))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
	if len(got.Apps) != 1 || got.Apps[0].AppID != "flux-ks:flux-system:apps" || got.Apps[0].Source.Path != "./apps/prod" {
		t.Fatalf("apps = %+v, want flux-ks:flux-system:apps with its path", got.Apps)
	}
	// The Service with the same name is a different object.
	svc := webRef
	svc.Group, svc.Version, svc.Resource, svc.Kind = "", "v1", "services", "Service"
	if got := f.resolveOne(t, adminUser, svc); got.Confidence != ConfidenceConfirmed {
		t.Errorf("service verdict = %s/%s, want confirmed by its own inventory entry", got.Confidence, got.Reason)
	}
}

func TestResolveOwnership_BothControllersClaim(t *testing.T) {
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()),
		fluxKustomization("flux-system", "apps", "./apps", "prod_web_apps_Deployment"))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByBoth, ConfidenceConflicting, "both-claim")
	ids := strings.Join(appIDs(got.Apps), ",")
	if ids != "argo:argocd:web-app,flux-ks:flux-system:apps" {
		t.Errorf("apps = %s, want both confirming apps", ids)
	}
}

func TestResolveOwnership_NoEvidence(t *testing.T) {
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		argoAppManaging("argocd", "other", "https://git.example/other", "prod",
			map[string]any{"group": "apps", "kind": "Deployment", "namespace": "prod", "name": "api"}),
		fluxKustomization("flux-system", "apps", "./apps", "prod_api_apps_Deployment"))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "no-evidence")
	if len(got.Apps) != 0 || len(got.Evidence) != 0 {
		t.Errorf("apps = %+v, evidence = %+v, want neither", got.Apps, got.Evidence)
	}
}

func TestResolveOwnership_ForbiddenInventory(t *testing.T) {
	t.Run("list denied for every application", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysDenyAccessChecker(), liveWeb(nil, nil),
			argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))

		got := f.resolveOne(t, devUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
		wantNoLeak(t, got, "secret-repo", "web-app", "argocd:")
		if n := countVerb(f.dyn, "get", "applications"); n != 0 {
			t.Errorf("fetched %d hidden applications, want 0", n)
		}
	})

	t.Run("list allowed but get refused", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
			argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))
		f.dyn.PrependReactor("get", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "web-app", errors.New("no get"))
		})

		got := f.resolveOne(t, devUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
		wantNoLeak(t, got, "secret-repo")
	})
}

// An access check that fails is an outage, not a refusal: reporting it as
// forbidden would tell the caller they lack a permission they may hold.
func TestResolveOwnership_AccessCheckFailureIsUnavailableNotForbidden(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewErroringAccessChecker(errors.New("SAR failed")), liveWeb(nil, nil),
		argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))

	got := f.resolveOne(t, devUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	wantNoLeak(t, got, "secret-repo", "web-app")
}

func TestResolveOwnership_StaleTrackingMetadataIsHintOnly(t *testing.T) {
	live := liveWeb(nil, map[string]string{"argocd.argoproj.io/tracking-id": "web-app:apps/Deployment:prod/web"})
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), live,
		// web-app no longer lists web: its tracking id is stale.
		argoAppManaging("argocd", "web-app", "https://git.example/web-repo", "prod",
			map[string]any{"group": "apps", "kind": "Deployment", "namespace": "prod", "name": "web-v2"}))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "hints-only")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none from a hint", got.Apps)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Kind != EvidenceArgoTrackingID || got.Evidence[0].AppID != "" ||
		got.Evidence[0].RawValue != "web-app:apps/Deployment:prod/web" {
		t.Errorf("evidence = %+v, want the tracking hint with no app id", got.Evidence)
	}
	wantNoLeak(t, got, "web-repo")
	if n := countVerb(f.dyn, "get", "applications"); n != 1 {
		t.Errorf("fetched %d applications, want the hinted one checked", n)
	}
}

// KTD10: a label anyone can write is never ownership, however familiar.
func TestResolveOwnership_InstanceLabelAloneNeverConfirms(t *testing.T) {
	live := liveWeb(map[string]string{
		"app.kubernetes.io/instance":            "web-app",
		"app.kubernetes.io/managed-by":          "argocd",
		"kustomize.toolkit.fluxcd.io/name":      "apps",
		"kustomize.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), live,
		argoAppManaging("argocd", "web-app", "https://git.example/web-repo", "prod"),
		fluxKustomization("flux-system", "apps", "./flux-path"))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "hints-only")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none", got.Apps)
	}
	kinds := map[OwnershipEvidenceKind]bool{}
	for _, e := range got.Evidence {
		kinds[e.Kind] = true
		if e.AppID != "" {
			t.Errorf("hint %+v carries an app id", e)
		}
	}
	for _, k := range []OwnershipEvidenceKind{EvidenceInstanceLabel, EvidenceManagedByLabel, EvidenceFluxOwnerLabel} {
		if !kinds[k] {
			t.Errorf("evidence missing %s: %+v", k, got.Evidence)
		}
	}
	wantNoLeak(t, got, "web-repo", "flux-path")
}

func TestResolveOwnership_FluxHelmReleaseHasNoInventory(t *testing.T) {
	live := liveWeb(map[string]string{"helm.toolkit.fluxcd.io/name": "web", "helm.toolkit.fluxcd.io/namespace": "prod"}, nil)
	f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), live,
		obj("HelmRelease", "prod", "web", nil, map[string]any{"chart": map[string]any{"spec": map[string]any{"chart": "web"}}}))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "flux-helmrelease-no-inventory")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none", got.Apps)
	}
	if n := countVerb(f.dyn, "get", "helmreleases"); n != 0 {
		t.Errorf("fetched %d helmreleases, want 0: they have no inventory to read", n)
	}
}

func TestResolveOwnership_SearchBoundExhausted(t *testing.T) {
	var apps []*unstructured.Unstructured
	for i := range maxDetailFetches + 5 {
		apps = append(apps, argoAppManaging("argocd", fmt.Sprintf("app-%02d", i), "https://git.example/r", "prod"))
	}

	t.Run("bound hit without a match is unknown, not none", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil), apps...)
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "search-bound-exhausted")
		if n := countVerb(f.dyn, "get", "applications"); n != maxDetailFetches {
			t.Errorf("fetched %d applications, want the bound %d", n, maxDetailFetches)
		}
	})

	t.Run("a hinted application is checked first", func(t *testing.T) {
		owner := argoAppManaging("argocd", "zz-owner", "https://git.example/owner", "elsewhere", webStatusResource())
		live := liveWeb(map[string]string{"app.kubernetes.io/instance": "zz-owner"}, nil)
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), live, append(apps, owner)...)
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
		if n := countVerb(f.dyn, "get", "applications"); n != 1 {
			t.Errorf("fetched %d applications, want only the hinted owner", n)
		}
	})

	t.Run("the bound is shared across one request's refs", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil), apps...)
		other := webRef
		other.Name = "api"
		if _, err := f.h.ResolveOwnership(context.Background(), adminUser, "local", f.dyn, []ObjectRef{webRef, other}); err != nil {
			t.Fatal(err)
		}
		if n := countVerb(f.dyn, "get", "applications"); n != maxDetailFetches {
			t.Errorf("fetched %d applications for two refs, want %d: details are memoized per request", n, maxDetailFetches)
		}
	})
}

// A caller who may list applications only in team-a sees nothing of the
// application in argocd that owns the object: not its id, name, namespace or
// repository, and it is never fetched.
func TestResolveOwnership_ReadOnlyUserLearnsNothing(t *testing.T) {
	ac := resources.NewPredicateAccessChecker(func(verb, _, _, ns string) bool { return verb == "list" && ns == "team-a" })
	f := newLocalOwnership(t, true, false, ac, liveWeb(nil, nil),
		argoAppManaging("argocd", "hidden-owner", "https://git.example/hidden-repo", "prod", webStatusResource()),
		argoAppManaging("team-a", "visible", "https://git.example/visible", "team-a"))

	got := f.resolveOne(t, devUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "partial-visibility")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none", got.Apps)
	}
	wantNoLeak(t, got, "hidden-owner", "hidden-repo", "argo:argocd")
	for _, a := range f.dyn.Actions() {
		if g, ok := a.(k8stesting.GetAction); ok && g.GetName() == "hidden-owner" {
			t.Error("the hidden application was fetched")
		}
	}
}

func TestResolveOwnership_RevokedPermissionMidSession(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	ac := resources.NewPredicateAccessChecker(func(string, string, string, string) bool { return allowed.Load() })
	f := newLocalOwnership(t, true, false, ac, liveWeb(nil, nil),
		argoAppManaging("argocd", "web-app", "https://git.example/web-repo", "prod", webStatusResource()))

	if got := f.resolveOne(t, devUser, webRef); got.Confidence != ConfidenceConfirmed {
		t.Fatalf("first call = %s/%s, want confirmed", got.Confidence, got.Reason)
	}
	allowed.Store(false)
	got := f.resolveOne(t, devUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
	wantNoLeak(t, got, "web-repo", "web-app")
}

// Neither controller records a UID, so a deleted and recreated object of the
// same name is still claimed — and the result says it is name-scoped.
func TestResolveOwnership_RecreatedTargetSameName(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))
	ref := webRef
	ref.UID = "old-uid" // the live object is now new-uid

	got := f.resolveOne(t, adminUser, ref)
	wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
	if got.Object.UID != "old-uid" {
		t.Errorf("object uid = %q, want the ref echoed", got.Object.UID)
	}
}

// An unavailable controller is never reported as "no controller owns it".
func TestResolveOwnership_ArgoNotInstalled(t *testing.T) {
	t.Run("no controller installed", func(t *testing.T) {
		f := newLocalOwnership(t, false, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "no-controller-installed")
	})

	t.Run("flux still confirms", func(t *testing.T) {
		f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
			fluxKustomization("flux-system", "apps", "./apps", "prod_web_apps_Deployment"))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
	})

	t.Run("an argo detail that errors is unavailable", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
			argoAppManaging("argocd", "web-app", "https://git.example/web", "prod"))
		f.dyn.PrependReactor("get", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(errors.New("boom"))
		})
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	})
}

// A remote cluster is resolved against its own applications, listed as the
// caller, and never the local cache.
func TestResolveOwnership_RemoteCluster(t *testing.T) {
	t.Run("confirmed from the remote cluster's inventory", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, false),
			argoAppManaging("argocd", "remote-web", "https://git.example/remote", "prod", webStatusResource()))
		got, err := hs.h.ResolveOwnership(context.Background(), adminUser, remoteCluster, hs.remoteDyn(), []ObjectRef{webRef})
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, got[0], OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
		if got[0].Apps[0].AppID != "argo:argocd:remote-web" || got[0].Object.ClusterID != remoteCluster {
			t.Errorf("result = %+v, want remote-web on %s", got[0], remoteCluster)
		}
		if n := hs.localActions(); n != 0 {
			t.Errorf("local cluster recorded %d actions, want 0", n)
		}
	})

	t.Run("a failing remote list is unavailable", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, true),
			fluxKustomization("flux-system", "apps", "./apps"))
		hs.remoteDyn().PrependReactor("list", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(errors.New("boom"))
		})
		got, err := hs.h.ResolveOwnership(context.Background(), adminUser, remoteCluster, hs.remoteDyn(), []ObjectRef{webRef})
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, got[0], OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	})

	t.Run("an unreachable remote cluster is unavailable", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, false))
		hs.clients.targetErr = unreachable()
		got, err := hs.h.ResolveOwnership(context.Background(), adminUser, remoteCluster, hs.remoteDyn(), []ObjectRef{webRef})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Confidence != ConfidenceUnavailable || len(got[0].Apps) != 0 {
			t.Errorf("verdict = %s/%s, want unavailable", got[0].Confidence, got[0].Reason)
		}
	})
}

func TestResolveOwnership_ContextCancelled(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), liveWeb(nil, nil),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := f.h.ResolveOwnership(ctx, adminUser, "local", f.dyn, []ObjectRef{webRef, webRef})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Errorf("results = %+v, want no partial slice", got)
	}
}

func TestOwnershipEvidenceKind_Authoritative(t *testing.T) {
	want := map[OwnershipEvidenceKind]bool{
		EvidenceArgoStatusResource: true,
		EvidenceFluxInventoryEntry: true,
		EvidenceArgoTrackingID:     false,
		EvidenceInstanceLabel:      false,
		EvidenceManagedByLabel:     false,
		EvidenceFieldManager:       false,
		EvidenceFluxOwnerLabel:     false,
		"":                         false,
		"ARGO-STATUS-RESOURCE":     false,
	}
	for k, w := range want {
		if got := k.Authoritative(); got != w {
			t.Errorf("%q.Authoritative() = %v, want %v", k, got, w)
		}
	}
}

func TestParseArgoTrackingID(t *testing.T) {
	tests := []struct {
		in                         string
		app, group, kind, ns, name string
		ok                         bool
	}{
		{"web-app:apps/Deployment:prod/web", "web-app", "apps", "Deployment", "prod", "web", true},
		{"web-app:/Service:prod/web", "web-app", "", "Service", "prod", "web", true},
		{"web-app:/Namespace:/prod", "web-app", "", "Namespace", "", "prod", true},
		{"argocd_web-app:apps/Deployment:prod/web", "argocd_web-app", "apps", "Deployment", "prod", "web", true},
		{"web-app:apps/Deployment:prod/we:b", "web-app", "apps", "Deployment", "prod", "we:b", true},
		{"", "", "", "", "", "", false},
		{"web-app", "", "", "", "", "", false},
		{":apps/Deployment:prod/web", "", "", "", "", "", false},
		{"web-app:Deployment:prod/web", "", "", "", "", "", false},
		{"web-app:apps/:prod/web", "", "", "", "", "", false},
		{"web-app:apps/Deployment:prod/", "", "", "", "", "", false},
	}
	for _, tt := range tests {
		app, group, kind, ns, name, ok := parseArgoTrackingID(tt.in)
		if ok != tt.ok || app != tt.app || group != tt.group || kind != tt.kind || ns != tt.ns || name != tt.name {
			t.Errorf("parseArgoTrackingID(%q) = %q,%q,%q,%q,%q,%v; want %q,%q,%q,%q,%q,%v",
				tt.in, app, group, kind, ns, name, ok, tt.app, tt.group, tt.kind, tt.ns, tt.name, tt.ok)
		}
	}
}

// A hint value is attacker-written: it is capped and stripped of control and
// format characters before it can reach a response.
func TestHintsFor_CapsAndStripsRawValue(t *testing.T) {
	long := strings.Repeat("a", 4096)
	live := liveWeb(map[string]string{"app.kubernetes.io/instance": "web\x1b[31m‮app"}, map[string]string{"argocd.argoproj.io/tracking-id": long})

	hints := hintsFor(live)
	if len(hints.evidence) != 2 {
		t.Fatalf("evidence = %+v, want tracking and instance hints", hints.evidence)
	}
	for _, e := range hints.evidence {
		if len(e.RawValue) > 256 {
			t.Errorf("%s raw value is %d bytes, want <= 256", e.Kind, len(e.RawValue))
		}
		if e.Kind == EvidenceInstanceLabel && e.RawValue != "web[31mapp" {
			t.Errorf("instance raw value = %q, want control and format characters stripped", e.RawValue)
		}
	}
}
