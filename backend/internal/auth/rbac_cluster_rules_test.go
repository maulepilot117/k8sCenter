package auth

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type rulesFactory struct {
	rules map[string][]authorizationv1.ResourceRule
	mu    sync.Mutex
	asked []string
}

func (f *rulesFactory) ClientForUser(string, []string) (kubernetes.Interface, error) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "selfsubjectrulesreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectRulesReview)
			ns := review.Spec.Namespace
			f.mu.Lock()
			f.asked = append(f.asked, ns)
			f.mu.Unlock()
			if ns == "" {
				return true, nil, errNoNamespace
			}
			review.Status.ResourceRules = f.rules[ns]
			return true, review, nil
		})
	return cs, nil
}

type noNamespaceError struct{}

func (noNamespaceError) Error() string { return "no namespace on request" }

var errNoNamespace = noNamespaceError{}

func TestRBACChecker_ClusterScopedComesFromClusterWideRules(t *testing.T) {
	factory := &rulesFactory{rules: map[string][]authorizationv1.ResourceRule{
		clusterRulesNamespace: {{Verbs: []string{"get", "delete"}, APIGroups: []string{""}, Resources: []string{"namespaces"}}},
		"team-a": {
			{Verbs: []string{"get", "delete"}, APIGroups: []string{""}, Resources: []string{"namespaces"}},
			{Verbs: []string{"list"}, APIGroups: []string{""}, Resources: []string{"pods"}},
		},
	}}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := &User{Username: "u", KubernetesUsername: "u"}

	summary, err := checker.GetSummary(context.Background(), user, []string{"team-a"})
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if got := summary.ClusterScoped["namespaces"]; !slices.Contains(got, "delete") {
		t.Errorf("ClusterScoped[namespaces] = %v, want delete", got)
	}
	if _, ok := summary.ClusterScoped["pods"]; ok {
		t.Errorf("a namespace-only grant leaked into ClusterScoped: %v", summary.ClusterScoped)
	}

	perms, err := NewRBACChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil))).
		GetNamespacePermissions(context.Background(), user, "team-a")
	if err != nil {
		t.Fatalf("GetNamespacePermissions: %v", err)
	}
	if got := perms.ClusterScoped["namespaces"]; !slices.Contains(got, "delete") {
		t.Errorf("GetNamespacePermissions ClusterScoped[namespaces] = %v, want delete", got)
	}

	if slices.Contains(factory.asked, "") {
		t.Errorf("a rules review was sent with no namespace: %q", factory.asked)
	}
}

// The probe name must never be a namespace someone could create and fill
// with RoleBindings, which would then read as cluster-wide grants.
func TestClusterRulesNamespaceCannotExist(t *testing.T) {
	if errs := validation.IsDNS1123Label(clusterRulesNamespace); len(errs) == 0 {
		t.Fatalf("%q is a valid namespace name; pick one that cannot exist", clusterRulesNamespace)
	}
}

// The summary keys permissions by resource name only, so a rule must name an
// API group that serves the tracked kind. The API server always fills
// APIGroups in a rules review.
func TestRBACChecker_RulesMatchAPIGroup(t *testing.T) {
	factory := &rulesFactory{rules: map[string][]authorizationv1.ResourceRule{
		"team-a": {
			// Same name, other group: not core pods or services.
			{Verbs: []string{"list"}, APIGroups: []string{"metrics.k8s.io"}, Resources: []string{"pods"}},
			{Verbs: []string{"get"}, APIGroups: []string{"serving.knative.dev"}, Resources: []string{"services"}},
			// A whole group with no tracked kind.
			{Verbs: []string{"list"}, APIGroups: []string{"node.k8s.io"}, Resources: []string{"*"}},
			// A whole tracked group: only that group's kinds.
			{Verbs: []string{"watch"}, APIGroups: []string{"batch"}, Resources: []string{"*"}},
			// The right group for the kind.
			{Verbs: []string{"get"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}},
			// Events are served by two groups.
			{Verbs: []string{"list"}, APIGroups: []string{"events.k8s.io"}, Resources: []string{"events"}},
			// A rule limited to named objects grants those objects only, which
			// the per-kind summary cannot express, so it grants nothing here.
			{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"db-creds"}},
			// A rule with no API groups matches nothing.
			{Verbs: []string{"get"}, Resources: []string{"configmaps"}},
		},
	}}
	checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := &User{Username: "u", KubernetesUsername: "u"}

	summary, err := checker.GetSummary(context.Background(), user, []string{"team-a"})
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	got := summary.Namespaces["team-a"]
	want := map[string][]string{
		"jobs": {"watch"}, "cronjobs": {"watch"},
		"deployments": {"get"},
		"events":      {"list"},
	}
	if len(got) != len(want) {
		t.Errorf("team-a perms = %v, want %v", got, want)
	}
	for resource, verbs := range want {
		if !slices.Equal(got[resource], verbs) {
			t.Errorf("team-a perms[%q] = %v, want %v", resource, got[resource], verbs)
		}
	}
}

// kindGroups is written out independently of trackedResources, so a typo in
// the table's group strings fails here instead of silently dropping a real
// permission from /auth/me and the notification feed.
var kindGroups = map[string]string{
	"pods": "", "services": "", "configmaps": "", "secrets": "",
	"persistentvolumeclaims": "", "endpoints": "", "events": "",
	"resourcequotas": "", "limitranges": "", "serviceaccounts": "",
	"namespaces": "", "nodes": "", "persistentvolumes": "",
	"deployments": "apps", "statefulsets": "apps", "daemonsets": "apps", "replicasets": "apps",
	"jobs": "batch", "cronjobs": "batch",
	"ingresses": "networking.k8s.io", "networkpolicies": "networking.k8s.io",
	"poddisruptionbudgets":     "policy",
	"horizontalpodautoscalers": "autoscaling",
	"storageclasses":           "storage.k8s.io",
	"roles":                    "rbac.authorization.k8s.io", "rolebindings": "rbac.authorization.k8s.io",
	"clusterroles": "rbac.authorization.k8s.io", "clusterrolebindings": "rbac.authorization.k8s.io",
	"endpointslices": "discovery.k8s.io",
}

func TestRBACChecker_EveryTrackedKindMatchesItsGroup(t *testing.T) {
	if len(kindGroups) != len(trackedResources) {
		t.Fatalf("kindGroups has %d kinds, trackedResources %d; add the new kind to both", len(kindGroups), len(trackedResources))
	}
	for resource, group := range kindGroups {
		t.Run(resource, func(t *testing.T) {
			factory := &rulesFactory{rules: map[string][]authorizationv1.ResourceRule{
				"team-a": {
					{Verbs: []string{"get"}, APIGroups: []string{group}, Resources: []string{resource}},
					{Verbs: []string{"list"}, APIGroups: []string{"example.com"}, Resources: []string{resource}},
				},
			}}
			checker := NewRBACChecker(factory, slog.New(slog.NewTextHandler(io.Discard, nil)))
			summary, err := checker.GetSummary(context.Background(), &User{Username: "u", KubernetesUsername: "u"}, []string{"team-a"})
			if err != nil {
				t.Fatalf("GetSummary: %v", err)
			}
			if got := summary.Namespaces["team-a"][resource]; !slices.Equal(got, []string{"get"}) {
				t.Errorf("perms[%q] = %v, want [get] from group %q and nothing from example.com", resource, got, group)
			}
		})
	}
}
