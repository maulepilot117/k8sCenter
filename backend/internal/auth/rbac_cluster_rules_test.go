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
		clusterRulesNamespace: {{Verbs: []string{"get", "delete"}, Resources: []string{"namespaces"}}},
		"team-a": {
			{Verbs: []string{"get", "delete"}, Resources: []string{"namespaces"}},
			{Verbs: []string{"list"}, Resources: []string{"pods"}},
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
