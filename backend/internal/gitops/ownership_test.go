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

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
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

// argoAppManaging is an in-cluster Argo Application whose status.resources
// lists rs.
func argoAppManaging(ns, name, repo, destNS string, rs ...map[string]any) *unstructured.Unstructured {
	u := argoApp(ns, name, repo)
	_ = unstructured.SetNestedField(u.Object, "https://kubernetes.default.svc", "spec", "destination", "server")
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

// withDestination replaces an Argo Application's destination cluster.
func withDestination(u *unstructured.Unstructured, server, name string) *unstructured.Unstructured {
	_ = unstructured.SetNestedField(u.Object, server, "spec", "destination", "server")
	_ = unstructured.SetNestedField(u.Object, name, "spec", "destination", "name")
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

// liveObject is a live Deployment in prod.
func liveObject(name string, labels, annotations map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": name, "namespace": "prod", "uid": "new-uid"},
	}}
	if labels != nil {
		u.SetLabels(labels)
	}
	if annotations != nil {
		u.SetAnnotations(annotations)
	}
	return u
}

// liveWeb is the live Deployment webRef points at.
func liveWeb(labels, annotations map[string]string) *unstructured.Unstructured {
	return liveObject("web", labels, annotations)
}

type ownershipFixture struct {
	h *Handler
	// sa is the service-account view the local cache lists through; caller
	// is the caller-impersonating client ResolveOwnership is handed. Both
	// serve the same objects, so a read through the wrong one still finds
	// them but is recorded on the wrong fake.
	sa, caller *dynfake.FakeDynamicClient
}

// newLocalOwnership builds a Handler for the local cluster.
func newLocalOwnership(t *testing.T, argo, flux bool, ac *resources.AccessChecker, live []*unstructured.Unstructured, apps ...*unstructured.Unstructured) *ownershipFixture {
	t.Helper()
	sa := newFakeCluster(t, toolLists(argo, flux), apps...).dyn
	copies := make([]*unstructured.Unstructured, 0, len(apps))
	for _, a := range apps {
		copies = append(copies, a.DeepCopy())
	}
	caller := newFakeCluster(t, toolLists(argo, flux), copies...).dyn
	for _, l := range live {
		if err := caller.Tracker().Create(deploymentsGVR, l, l.GetNamespace()); err != nil {
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
			baseDynOverride: sa,
		},
		sa:     sa,
		caller: caller,
	}
}

func lives(objs ...*unstructured.Unstructured) []*unstructured.Unstructured { return objs }

func (f *ownershipFixture) resolveOne(t *testing.T, user *auth.User, ref ObjectRef) OwnershipResult {
	t.Helper()
	got, err := f.h.ResolveOwnership(context.Background(), user, "local", f.caller, []ObjectRef{ref})
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

func failWith(err error) k8stesting.ReactionFunc {
	return func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, err }
}

func TestResolveOwnership_ConfirmedArgo(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
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

// Detail GETs go through the caller's client, never the service account's.
func TestResolveOwnership_DetailReadsUseTheCallerClient(t *testing.T) {
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod"),
		fluxKustomization("flux-system", "apps", "./apps"))

	f.resolveOne(t, adminUser, webRef)
	for _, res := range []string{"applications", "kustomizations", "deployments"} {
		if n := countVerb(f.sa, "get", res); n != 0 {
			t.Errorf("service account recorded %d get %s, want 0", n, res)
		}
	}
	if countVerb(f.caller, "get", "applications") != 1 || countVerb(f.caller, "get", "kustomizations") != 1 || countVerb(f.caller, "get", "deployments") != 1 {
		t.Errorf("caller actions = %v, want one get each of the app, the kustomization and the live object", f.caller.Actions())
	}
	if countVerb(f.caller, "list", "applications") != 0 {
		t.Error("the caller client listed applications; the list comes from the service-account cache")
	}
}

func TestResolveOwnership_ConfirmedFluxKustomization(t *testing.T) {
	f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
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
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
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
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
		argoAppManaging("argocd", "other", "https://git.example/other", "prod",
			map[string]any{"group": "apps", "kind": "Deployment", "namespace": "prod", "name": "api"}),
		fluxKustomization("flux-system", "apps", "./apps", "prod_api_apps_Deployment"))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "no-evidence")
	if len(got.Apps) != 0 || len(got.Evidence) != 0 {
		t.Errorf("apps = %+v, evidence = %+v, want neither", got.Apps, got.Evidence)
	}
}

// An application that deploys to another cluster describes that cluster's
// object of the same name: it may make the verdict unknown, never confirmed,
// and never no-evidence.
func TestResolveOwnership_OffClusterApplicationsCannotConfirm(t *testing.T) {
	t.Run("argo destination on another cluster", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			withDestination(argoAppManaging("argocd", "spoke-web", "https://git.example/spoke", "prod", webStatusResource()), "https://spoke.example:6443", ""))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "argo-destination-unverified")
		if len(got.Apps) != 0 {
			t.Errorf("apps = %+v, want none", got.Apps)
		}
		wantNoLeak(t, got, "spoke-web", "git.example/spoke")
	})

	t.Run("argo destination by name other than in-cluster", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			withDestination(argoAppManaging("argocd", "spoke-web", "https://git.example/spoke", "prod", webStatusResource()), "", "spoke"))
		wantVerdict(t, f.resolveOne(t, adminUser, webRef), OwnedByNone, ConfidenceUnknown, "argo-destination-unverified")
	})

	t.Run("argo destination named in-cluster confirms", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			withDestination(argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()), "", "in-cluster"))
		wantVerdict(t, f.resolveOne(t, adminUser, webRef), OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
	})

	t.Run("flux kustomization with a kubeConfig", func(t *testing.T) {
		ks := fluxKustomization("flux-system", "spoke", "./spoke", "prod_web_apps_Deployment")
		_ = unstructured.SetNestedField(ks.Object, map[string]any{"secretRef": map[string]any{"name": "spoke-kubeconfig"}}, "spec", "kubeConfig")
		f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)), ks)
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "flux-remote-kubeconfig")
		wantNoLeak(t, got, "spoke-kubeconfig")
	})

	t.Run("an in-cluster owner wins and is read first", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			withDestination(argoAppManaging("argocd", "aa-spoke", "https://git.example/spoke", "prod", webStatusResource()), "https://spoke.example:6443", ""),
			argoAppManaging("argocd", "zz-local", "https://git.example/local", "elsewhere", webStatusResource()))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
		if got.Apps[0].AppID != "argo:argocd:zz-local" || countVerb(f.caller, "get", "applications") != 1 {
			t.Errorf("apps = %v after %d gets, want zz-local read before the off-cluster app", appIDs(got.Apps), countVerb(f.caller, "get", "applications"))
		}
	})
}

func TestResolveOwnership_ForbiddenInventory(t *testing.T) {
	t.Run("list denied for every application", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysDenyAccessChecker(), lives(liveWeb(nil, nil)),
			argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))

		got := f.resolveOne(t, devUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
		wantNoLeak(t, got, "secret-repo", "web-app", "argocd:")
		if n := countVerb(f.caller, "get", "applications"); n != 0 {
			t.Errorf("fetched %d hidden applications, want 0", n)
		}
	})

	t.Run("list allowed but get refused", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))
		f.caller.PrependReactor("get", "applications",
			failWith(apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "web-app", errors.New("no get"))))

		got := f.resolveOne(t, devUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
		wantNoLeak(t, got, "secret-repo")
	})

	t.Run("flux kustomizations denied", func(t *testing.T) {
		ac := resources.NewPredicateAccessChecker(func(_, _, resource, _ string) bool { return resource != "kustomizations" })
		f := newLocalOwnership(t, false, true, ac, lives(liveWeb(nil, nil)),
			fluxKustomization("flux-system", "apps", "./secret-path", "prod_web_apps_Deployment"))
		got := f.resolveOne(t, devUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceForbidden, "flux-list-forbidden")
		wantNoLeak(t, got, "secret-path")
	})
}

// An access check that fails is an outage, not a refusal: reporting it as
// forbidden would tell the caller they lack a permission they may hold.
func TestResolveOwnership_AccessCheckFailureIsUnavailableNotForbidden(t *testing.T) {
	f := newLocalOwnership(t, true, false, resources.NewErroringAccessChecker(errors.New("SAR failed")), lives(liveWeb(nil, nil)),
		argoAppManaging("argocd", "web-app", "https://git.example/secret-repo", "prod", webStatusResource()))

	got := f.resolveOne(t, devUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	wantNoLeak(t, got, "secret-repo", "web-app")
}

// recordingFactory is the local ClientFactory seam of a real AccessChecker:
// it counts the SAR clients it hands out and allows every review.
type recordingFactory struct{ calls atomic.Int32 }

func (f *recordingFactory) ClientForUser(string, []string) (kubernetes.Interface, error) {
	f.calls.Add(1)
	cs := kfake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SelfSubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	return cs, nil
}

// The RBAC filter runs against the clusterID argument, not the request
// context's cluster: here the context names a remote cluster the checker
// has no route to, so a check sent there fails and nothing confirms.
func TestResolveOwnership_AccessChecksUseTheResolvedCluster(t *testing.T) {
	factory := &recordingFactory{}
	ac := resources.NewAccessChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f := newLocalOwnership(t, true, false, ac, lives(liveWeb(nil, nil)),
		argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))

	ctx := middleware.WithClusterID(context.Background(), "some-remote")
	got, err := f.h.ResolveOwnership(ctx, devUser, "local", f.caller, []ObjectRef{webRef})
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, got[0], OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
	if factory.calls.Load() == 0 {
		t.Error("no SAR went to the local cluster")
	}
}

func TestResolveOwnership_StaleTrackingMetadataIsHintOnly(t *testing.T) {
	live := liveWeb(nil, map[string]string{"argocd.argoproj.io/tracking-id": "web-app:apps/Deployment:prod/web"})
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(live),
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
	if n := countVerb(f.caller, "get", "applications"); n != 1 {
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
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), lives(live),
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
	f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(live),
		obj("HelmRelease", "prod", "web", nil, map[string]any{"chart": map[string]any{"spec": map[string]any{"chart": "web"}}}))

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "flux-helmrelease-no-inventory")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none", got.Apps)
	}
	if n := countVerb(f.caller, "get", "helmreleases"); n != 0 {
		t.Errorf("fetched %d helmreleases, want 0: they have no inventory to read", n)
	}
}

// unmatchedArgoApps are maxDetailFetches+5 in-cluster Applications targeting
// prod that manage nothing of interest.
func unmatchedArgoApps() []*unstructured.Unstructured {
	var apps []*unstructured.Unstructured
	for i := range maxDetailFetches + 5 {
		apps = append(apps, argoAppManaging("argocd", fmt.Sprintf("app-%02d", i), "https://git.example/r", "prod"))
	}
	return apps
}

func TestResolveOwnership_SearchBoundExhausted(t *testing.T) {
	t.Run("bound hit without a match is unknown, not none", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)), unmatchedArgoApps()...)
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "search-bound-exhausted")
		if n := countVerb(f.caller, "get", "applications"); n != maxDetailFetches {
			t.Errorf("fetched %d applications, want the bound %d", n, maxDetailFetches)
		}
	})

	t.Run("a hinted application is checked first", func(t *testing.T) {
		owner := argoAppManaging("argocd", "zz-owner", "https://git.example/owner", "elsewhere", webStatusResource())
		live := liveWeb(map[string]string{"app.kubernetes.io/instance": "zz-owner"}, nil)
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(live), append(unmatchedArgoApps(), owner)...)
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
		if n := countVerb(f.caller, "get", "applications"); n != 1 {
			t.Errorf("fetched %d applications, want only the hinted owner", n)
		}
	})

	t.Run("the bound is shared across one request's refs", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)), unmatchedArgoApps()...)
		other := webRef
		other.Name = "api"
		if _, err := f.h.ResolveOwnership(context.Background(), adminUser, "local", f.caller, []ObjectRef{webRef, other}); err != nil {
			t.Fatal(err)
		}
		if n := countVerb(f.caller, "get", "applications"); n != maxDetailFetches {
			t.Errorf("fetched %d applications for two refs, want %d: details are memoized per request", n, maxDetailFetches)
		}
	})

	// The second ref's hint points past the spent budget; its owner was
	// already read for the first ref, so it is still found.
	t.Run("past the bound, already-read applications are still checked", func(t *testing.T) {
		apps := unmatchedArgoApps()
		apiStatus := map[string]any{"group": "apps", "kind": "Deployment", "namespace": "prod", "name": "api"}
		apps[10] = argoAppManaging("argocd", "app-10", "https://git.example/api", "prod", apiStatus)
		api := liveObject("api", map[string]string{"app.kubernetes.io/instance": fmt.Sprintf("app-%02d", maxDetailFetches+4)}, nil)
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil), api), apps...)
		apiRef := webRef
		apiRef.Name = "api"

		got, err := f.h.ResolveOwnership(context.Background(), adminUser, "local", f.caller, []ObjectRef{webRef, apiRef})
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, got[0], OwnedByNone, ConfidenceUnknown, "search-bound-exhausted")
		if got[1].Confidence != ConfidenceConfirmed || got[1].Apps[0].AppID != "argo:argocd:app-10" {
			t.Errorf("api verdict = %s/%s %v, want confirmed by app-10 from the memo", got[1].Confidence, got[1].Reason, appIDs(got[1].Apps))
		}
	})
}

// The budget is spent across both controllers in one order, so many Argo
// applications cannot starve a hinted Flux owner.
func TestResolveOwnership_BudgetIsSharedHintedFirstAcrossTools(t *testing.T) {
	live := liveWeb(map[string]string{"kustomize.toolkit.fluxcd.io/name": "zz-owner", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil)
	f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), lives(live),
		append(unmatchedArgoApps(), fluxKustomization("flux-system", "zz-owner", "./owner", "prod_web_apps_Deployment"))...)

	got := f.resolveOne(t, adminUser, webRef)
	wantVerdict(t, got, OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
	if got.Apps[0].AppID != "flux-ks:flux-system:zz-owner" {
		t.Errorf("apps = %v", appIDs(got.Apps))
	}
}

// Flux candidates follow the same order under a binding bound: a
// Kustomization a hint names, then one targeting the object's namespace.
func TestResolveOwnership_FluxCandidateOrdering(t *testing.T) {
	var noise []*unstructured.Unstructured
	for i := range maxDetailFetches + 5 {
		noise = append(noise, fluxKustomization("flux-system", fmt.Sprintf("ks-%02d", i), "./noise"))
	}
	owner := func(ns string) *unstructured.Unstructured {
		ks := fluxKustomization(ns, "zz-owner", "./owner", "prod_web_apps_Deployment")
		return ks
	}

	t.Run("hint key namespace/name", func(t *testing.T) {
		live := liveWeb(map[string]string{"kustomize.toolkit.fluxcd.io/name": "zz-owner", "kustomize.toolkit.fluxcd.io/namespace": "zz-ns"}, nil)
		f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(live), append(noise, owner("zz-ns"))...)
		wantVerdict(t, f.resolveOne(t, adminUser, webRef), OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
		if n := countVerb(f.caller, "get", "kustomizations"); n != 1 {
			t.Errorf("fetched %d kustomizations, want only the hinted owner", n)
		}
	})

	t.Run("target namespace", func(t *testing.T) {
		ks := owner("zz-ns")
		_ = unstructured.SetNestedField(ks.Object, "prod", "spec", "targetNamespace")
		f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)), append(noise, ks)...)
		wantVerdict(t, f.resolveOne(t, adminUser, webRef), OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
		if n := countVerb(f.caller, "get", "kustomizations"); n != 1 {
			t.Errorf("fetched %d kustomizations, want only the one targeting prod", n)
		}
	})
}

// A caller who may list applications only in team-a sees nothing of the
// application in argocd that owns the object: not its id, name, namespace or
// repository, and it is never fetched.
func TestResolveOwnership_ReadOnlyUserLearnsNothing(t *testing.T) {
	ac := resources.NewPredicateAccessChecker(func(verb, _, _, ns string) bool { return verb == "list" && ns == "team-a" })
	f := newLocalOwnership(t, true, false, ac, lives(liveWeb(nil, nil)),
		argoAppManaging("argocd", "hidden-owner", "https://git.example/hidden-repo", "prod", webStatusResource()),
		argoAppManaging("team-a", "visible", "https://git.example/visible", "team-a"))

	got := f.resolveOne(t, devUser, webRef)
	wantVerdict(t, got, OwnedByNone, ConfidenceUnknown, "partial-visibility")
	if len(got.Apps) != 0 {
		t.Errorf("apps = %+v, want none", got.Apps)
	}
	wantNoLeak(t, got, "hidden-owner", "hidden-repo", "argo:argocd")
	for _, a := range f.caller.Actions() {
		if g, ok := a.(k8stesting.GetAction); ok && g.GetName() == "hidden-owner" {
			t.Error("the hidden application was fetched")
		}
	}
}

func TestResolveOwnership_RevokedPermissionMidSession(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	ac := resources.NewPredicateAccessChecker(func(string, string, string, string) bool { return allowed.Load() })
	f := newLocalOwnership(t, true, false, ac, lives(liveWeb(nil, nil)),
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
	f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
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
		f := newLocalOwnership(t, false, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "no-controller-installed")
	})

	t.Run("flux still confirms", func(t *testing.T) {
		f := newLocalOwnership(t, false, true, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			fluxKustomization("flux-system", "apps", "./apps", "prod_web_apps_Deployment"))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByFluxCD, ConfidenceConfirmed, "confirmed-flux-inventory")
	})

	t.Run("an argo detail that errors is unavailable", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			argoAppManaging("argocd", "web-app", "https://git.example/web", "prod"))
		f.caller.PrependReactor("get", "applications", failWith(apierrors.NewInternalError(errors.New("boom"))))
		got := f.resolveOne(t, adminUser, webRef)
		wantVerdict(t, got, OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	})
}

// A local list that fails is an outage for that controller, not an empty
// one; the list endpoint keeps showing the other controller's apps.
func TestResolveOwnership_LocalListFailureIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, resource, reason string
		argo, flux             bool
	}{
		{"argo applications", "applications", "argo-unavailable", true, true},
		{"flux kustomizations", "kustomizations", "flux-unavailable", true, true},
		{"flux helmreleases", "helmreleases", "flux-unavailable", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalOwnership(t, tc.argo, tc.flux, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
				argoAppManaging("argocd", "other", "https://git.example/other", "prod"),
				fluxKustomization("flux-system", "apps", "./apps"))
			f.sa.PrependReactor("list", tc.resource, failWith(apierrors.NewInternalError(errors.New("boom"))))
			wantVerdict(t, f.resolveOne(t, adminUser, webRef), OwnedByNone, ConfidenceUnavailable, tc.reason)
		})
	}

	t.Run("list endpoint still serves the other controller", func(t *testing.T) {
		f := newLocalOwnership(t, true, true, resources.NewAlwaysAllowAccessChecker(), nil,
			argoAppManaging("argocd", "other", "https://git.example/other", "prod"),
			fluxKustomization("flux-system", "apps", "./apps"))
		f.sa.PrependReactor("list", "applications", failWith(apierrors.NewInternalError(errors.New("boom"))))
		apps, err := f.h.fetchApps(context.Background())
		if err != nil || len(apps) != 1 || apps[0].Name != "apps" {
			t.Errorf("fetchApps = %v, %v; want only the kustomization", appNames(apps), err)
		}
	})
}

// A remote cluster is resolved against its own applications, listed as the
// caller, and never the local cache.
func TestResolveOwnership_RemoteCluster(t *testing.T) {
	resolve := func(t *testing.T, hs *harness) OwnershipResult {
		t.Helper()
		got, err := hs.h.ResolveOwnership(context.Background(), adminUser, remoteCluster, hs.remoteDyn(), []ObjectRef{webRef})
		if err != nil {
			t.Fatal(err)
		}
		return got[0]
	}
	forbidden := func(resource string) error {
		return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", errors.New("no list"))
	}

	t.Run("confirmed from the remote cluster's inventory", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, false),
			argoAppManaging("argocd", "remote-web", "https://git.example/remote", "prod", webStatusResource()))
		got := resolve(t, hs)
		wantVerdict(t, got, OwnedByArgoCD, ConfidenceConfirmed, "confirmed-argo-status")
		if got.Apps[0].AppID != "argo:argocd:remote-web" || got.Object.ClusterID != remoteCluster {
			t.Errorf("result = %+v, want remote-web on %s", got, remoteCluster)
		}
		if n := hs.localActions(); n != 0 {
			t.Errorf("local cluster recorded %d actions, want 0", n)
		}
	})

	t.Run("a failing remote list is unavailable", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, true), fluxKustomization("flux-system", "apps", "./apps"))
		hs.remoteDyn().PrependReactor("list", "applications", failWith(apierrors.NewInternalError(errors.New("boom"))))
		wantVerdict(t, resolve(t, hs), OwnedByNone, ConfidenceUnavailable, "argo-unavailable")
	})

	t.Run("a refused remote list is forbidden", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, true), fluxKustomization("flux-system", "apps", "./apps"))
		hs.remoteDyn().PrependReactor("list", "applications", failWith(forbidden("applications")))
		wantVerdict(t, resolve(t, hs), OwnedByNone, ConfidenceForbidden, "argo-list-forbidden")
	})

	// Every list failing still says whose lists failed: a Flux-only cluster
	// is not reported as an Argo problem.
	t.Run("every list refused on a flux-only cluster", func(t *testing.T) {
		hs := newHarness(t, toolLists(false, true))
		hs.remoteDyn().PrependReactor("list", "kustomizations", failWith(forbidden("kustomizations")))
		hs.remoteDyn().PrependReactor("list", "helmreleases", failWith(forbidden("helmreleases")))
		wantVerdict(t, resolve(t, hs), OwnedByNone, ConfidenceForbidden, "flux-list-forbidden")
	})

	t.Run("an unreachable remote cluster is unavailable", func(t *testing.T) {
		hs := newHarness(t, toolLists(true, false))
		hs.clients.targetErr = unreachable()
		got := resolve(t, hs)
		if got.Confidence != ConfidenceUnavailable || len(got.Apps) != 0 {
			t.Errorf("verdict = %s/%s, want unavailable", got.Confidence, got.Reason)
		}
	})
}

func TestResolveOwnership_ContextCancelled(t *testing.T) {
	t.Run("before the call", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := f.h.ResolveOwnership(ctx, adminUser, "local", f.caller, []ObjectRef{webRef, webRef})
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("got %+v, %v; want no slice and context.Canceled", got, err)
		}
	})

	t.Run("mid-search", func(t *testing.T) {
		f := newLocalOwnership(t, true, false, resources.NewAlwaysAllowAccessChecker(), lives(liveWeb(nil, nil)),
			argoAppManaging("argocd", "web-app", "https://git.example/web", "prod", webStatusResource()))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.caller.PrependReactor("get", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
			cancel()
			return false, nil, nil
		})
		other := webRef
		other.Name = "api"
		got, err := f.h.ResolveOwnership(ctx, adminUser, "local", f.caller, []ObjectRef{webRef, other})
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("got %+v, %v; want no partial slice and context.Canceled", got, err)
		}
	})
}

// TestUnconfirmedVerdict pins every row of the unconfirmed verdict and its
// precedence: each case also sets every lower-precedence condition, so a
// reordering surfaces as the wrong reason.
func TestUnconfirmedVerdict(t *testing.T) {
	all := toolState{installed: true, unavailable: true, forbidden: true, hidden: true, exhausted: true, offClusterClaim: true, helmReleaseHinted: true}
	without := func(st toolState, fields ...string) toolState {
		for _, f := range fields {
			switch f {
			case "forbidden":
				st.forbidden = false
			case "unavailable":
				st.unavailable = false
			case "offCluster":
				st.offClusterClaim = false
			case "helm":
				st.helmReleaseHinted = false
			case "exhausted":
				st.exhausted = false
			case "hidden":
				st.hidden = false
			case "installed":
				st.installed = false
			}
		}
		return st
	}
	none := toolState{installed: true}
	upTo := []string{"forbidden", "unavailable", "offCluster", "helm", "exhausted", "hidden"}

	for _, tc := range []struct {
		name       string
		hints      bool
		argo, flux toolState
		confidence OwnershipConfidence
		reason     string
	}{
		{"argo forbidden first", true, all, all, ConfidenceForbidden, "argo-list-forbidden"},
		{"then flux forbidden", true, without(all, "forbidden"), all, ConfidenceForbidden, "flux-list-forbidden"},
		{"then argo unavailable", true, without(all, upTo[:1]...), without(all, upTo[:1]...), ConfidenceUnavailable, "argo-unavailable"},
		{"then flux unavailable", true, without(all, upTo[:2]...), without(all, upTo[:1]...), ConfidenceUnavailable, "flux-unavailable"},
		{"then an argo off-cluster claim", true, without(all, upTo[:2]...), without(all, upTo[:2]...), ConfidenceUnknown, "argo-destination-unverified"},
		{"then a flux kubeConfig claim", true, without(all, upTo[:3]...), without(all, upTo[:2]...), ConfidenceUnknown, "flux-remote-kubeconfig"},
		{"then a helmrelease hint", true, without(all, upTo[:3]...), without(all, upTo[:3]...), ConfidenceUnknown, "flux-helmrelease-no-inventory"},
		{"then the bound (argo)", true, without(all, upTo[:4]...), without(all, upTo[:5]...), ConfidenceUnknown, "search-bound-exhausted"},
		{"then the bound (flux)", true, without(all, upTo[:5]...), without(all, upTo[:4]...), ConfidenceUnknown, "search-bound-exhausted"},
		{"then hidden apps (argo)", true, without(all, upTo[:5]...), without(all, upTo...), ConfidenceUnknown, "partial-visibility"},
		{"then hidden apps (flux)", true, without(all, upTo...), without(all, upTo[:5]...), ConfidenceUnknown, "partial-visibility"},
		{"then hints", true, none, none, ConfidenceUnknown, "hints-only"},
		{"hints beat no controller", true, toolState{}, toolState{}, ConfidenceUnknown, "hints-only"},
		{"then no controller installed", false, toolState{}, toolState{}, ConfidenceUnavailable, "no-controller-installed"},
		{"one installed, nothing found", false, none, toolState{}, ConfidenceUnknown, "no-evidence"},
		{"both installed, nothing found", false, none, none, ConfidenceUnknown, "no-evidence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confidence, reason := unconfirmedVerdict(tc.hints, tc.argo, tc.flux)
			if confidence != tc.confidence || reason != tc.reason {
				t.Errorf("got %s/%s, want %s/%s", confidence, reason, tc.confidence, tc.reason)
			}
		})
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
