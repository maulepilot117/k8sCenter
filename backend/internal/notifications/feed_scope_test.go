package notifications

// feed_scope_test.go — the notification feed's namespace scope.
//
// The feed used to compute a non-admin's namespaces with
// RBACChecker.GetSummary(ctx, user, nil). With no namespaces to review, the
// summary was empty, and the store read an empty list as "no filter", so
// every non-admin saw every notification in the list and the unread count.
//
// The hermetic tests pin each half of the fix: the handler reviews the
// cluster's real namespaces, and the store's predicate fails closed. The
// PostgreSQL-backed test runs both against real rows through the
// KUBECENTER_TEST_DATABASE_URL gate (see store_dedup_test.go).

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// rulesClientFactory answers SelfSubjectRulesReview from a fixed
// namespace -> rules table, as RBACChecker's own tests do.
type rulesClientFactory struct {
	rules map[string][]authorizationv1.ResourceRule
}

func (f rulesClientFactory) ClientForUser(string, []string) (kubernetes.Interface, error) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "selfsubjectrulesreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectRulesReview)
			review.Status.ResourceRules = f.rules[review.Spec.Namespace]
			return true, review, nil
		})
	return cs, nil
}

func namespaceLister(t *testing.T, names ...string) corev1listers.NamespaceLister {
	t.Helper()
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, n := range names {
		if err := idx.Add(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n}}); err != nil {
			t.Fatalf("adding namespace %q: %v", n, err)
		}
	}
	return corev1listers.NewNamespaceLister(idx)
}

func scopeHandler(t *testing.T, rules map[string][]authorizationv1.ResourceRule, lister corev1listers.NamespaceLister) *Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Handler{
		RBACChecker: auth.NewRBACChecker(rulesClientFactory{rules: rules}, logger),
		Namespaces:  lister,
		Logger:      logger,
	}
}

// core is the core API group's name in a ResourceRule. The API server always
// fills APIGroups; an empty list matches no group.
var core = []string{""}

var podReader = []authorizationv1.ResourceRule{{Verbs: []string{"get", "list"}, APIGroups: core, Resources: []string{"pods"}}}

func TestAccessibleNamespaces_NonAdminGetsOnlyGrantedNamespaces(t *testing.T) {
	h := scopeHandler(t,
		map[string][]authorizationv1.ResourceRule{"team-a": podReader},
		namespaceLister(t, "team-a", "team-b"))
	user := &auth.User{Username: "alice", KubernetesUsername: "alice", Roles: []string{"user"}}

	namespaces, all, err := h.accessibleNamespaces(httptest.NewRequest("GET", "/", nil), user)
	if err != nil {
		t.Fatalf("accessibleNamespaces: %v", err)
	}
	if all {
		t.Fatal("a non-admin was given the unfiltered scope")
	}
	if !slices.Equal(namespaces, []string{"team-a"}) {
		t.Fatalf("namespaces = %v, want [team-a]", namespaces)
	}
}

func TestAccessibleNamespaces_NoGrantsIsEmptyNotUnfiltered(t *testing.T) {
	h := scopeHandler(t, nil, namespaceLister(t, "team-a", "team-b"))
	user := &auth.User{Username: "bob", KubernetesUsername: "bob", Roles: []string{"user"}}

	namespaces, all, err := h.accessibleNamespaces(httptest.NewRequest("GET", "/", nil), user)
	if err != nil {
		t.Fatalf("accessibleNamespaces: %v", err)
	}
	if all || len(namespaces) != 0 {
		t.Fatalf("got namespaces=%v all=%v, want an empty, filtered scope", namespaces, all)
	}
}

// A SelfSubjectRulesReview for a namespace returns that namespace's
// RoleBindings plus every ClusterRoleBinding, so a cluster-wide grant shows
// up in every namespace's review. withClusterRule models that: it adds
// clusterRule to each listed namespace's own rules.
func withClusterRule(own map[string][]authorizationv1.ResourceRule, clusterRule authorizationv1.ResourceRule, namespaces ...string) map[string][]authorizationv1.ResourceRule {
	rules := make(map[string][]authorizationv1.ResourceRule, len(namespaces))
	for _, ns := range namespaces {
		rules[ns] = append(slices.Clone(own[ns]), clusterRule)
	}
	return rules
}

// A namespace is in the feed scope only when the user can read a namespaced
// kind there. A cluster-wide grant on a cluster-scoped kind (the namespace
// picker's "list namespaces", "get nodes") or a write-only grant used to
// admit a namespace, and with it that namespace's notifications.
func TestAccessibleNamespaces_AdmitsOnlyNamespacedRead(t *testing.T) {
	configMapWriter := []authorizationv1.ResourceRule{{Verbs: []string{"create", "update"}, APIGroups: core, Resources: []string{"configmaps"}}}
	wildcard := []authorizationv1.ResourceRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}
	listNamespaces := authorizationv1.ResourceRule{Verbs: []string{"get", "list", "watch"}, APIGroups: core, Resources: []string{"namespaces"}}
	getNodes := []authorizationv1.ResourceRule{
		{Verbs: []string{"get", "list"}, APIGroups: core, Resources: []string{"nodes", "persistentvolumes"}},
		{Verbs: []string{"get", "list"}, APIGroups: []string{"storage.k8s.io"}, Resources: []string{"storageclasses"}},
	}
	clusterPodReader := authorizationv1.ResourceRule{Verbs: []string{"list"}, APIGroups: core, Resources: []string{"pods"}}
	// Grants on other API groups that reuse a tracked name, or that cover
	// every resource of a group with no tracked kind, read nothing tracked.
	runtimeClassReader := authorizationv1.ResourceRule{Verbs: []string{"get", "list"}, APIGroups: []string{"node.k8s.io"}, Resources: []string{"*"}}
	metricsPodReader := authorizationv1.ResourceRule{Verbs: []string{"get", "list"}, APIGroups: []string{"metrics.k8s.io"}, Resources: []string{"pods"}}
	knativeServiceReader := authorizationv1.ResourceRule{Verbs: []string{"get", "list"}, APIGroups: []string{"serving.knative.dev"}, Resources: []string{"services"}}
	appsReader := authorizationv1.ResourceRule{Verbs: []string{"list"}, APIGroups: []string{"apps"}, Resources: []string{"*"}}
	// A grant limited to named objects reads only those objects, not the kind.
	namedConfigMapReader := []authorizationv1.ResourceRule{{Verbs: []string{"get"}, APIGroups: core, Resources: []string{"configmaps"}, ResourceNames: []string{"app-config"}}}

	for _, tc := range []struct {
		name  string
		rules map[string][]authorizationv1.ResourceRule
		want  []string
	}{
		{
			name:  "cluster-wide list namespaces admits nothing extra",
			rules: withClusterRule(map[string][]authorizationv1.ResourceRule{"team-a": podReader}, listNamespaces, "team-a", "team-b"),
			want:  []string{"team-a"},
		},
		{
			name:  "cluster-wide read on cluster-scoped kinds admits nothing",
			rules: withClusterRule(nil, getNodes[0], "team-a", "team-b"),
			want:  []string{},
		},
		{
			name:  "write-only grant does not admit the namespace",
			rules: map[string][]authorizationv1.ResourceRule{"team-a": configMapWriter},
			want:  []string{},
		},
		{
			name:  "wildcard grant admits the namespace",
			rules: map[string][]authorizationv1.ResourceRule{"team-a": wildcard},
			want:  []string{"team-a"},
		},
		{
			name:  "cluster-wide read on storage classes admits nothing",
			rules: withClusterRule(nil, getNodes[1], "team-a", "team-b"),
			want:  []string{},
		},
		{
			name:  "cluster-wide read on a namespaced kind admits every namespace",
			rules: withClusterRule(nil, clusterPodReader, "team-a", "team-b"),
			want:  []string{"team-a", "team-b"},
		},
		{
			name:  "wildcard over a group with no tracked kind admits nothing",
			rules: withClusterRule(nil, runtimeClassReader, "team-a", "team-b"),
			want:  []string{},
		},
		{
			name:  "same-named kind in another group admits nothing",
			rules: withClusterRule(withClusterRule(nil, metricsPodReader, "team-a", "team-b"), knativeServiceReader, "team-a", "team-b"),
			want:  []string{},
		},
		{
			name:  "grant on one named object does not admit the namespace",
			rules: map[string][]authorizationv1.ResourceRule{"team-a": namedConfigMapReader},
			want:  []string{},
		},
		{
			name:  "wildcard over a group with tracked namespaced kinds admits",
			rules: withClusterRule(nil, appsReader, "team-a", "team-b"),
			want:  []string{"team-a", "team-b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := scopeHandler(t, tc.rules, namespaceLister(t, "team-a", "team-b"))
			user := &auth.User{Username: "dana", KubernetesUsername: "dana", Roles: []string{"user"}}

			namespaces, all, err := h.accessibleNamespaces(httptest.NewRequest("GET", "/", nil), user)
			if err != nil {
				t.Fatalf("accessibleNamespaces: %v", err)
			}
			if all {
				t.Fatal("a non-admin was given the unfiltered scope")
			}
			slices.Sort(namespaces)
			if !slices.Equal(namespaces, tc.want) {
				t.Fatalf("namespaces = %v, want %v", namespaces, tc.want)
			}
		})
	}
}

func TestAccessibleNamespaces_AdminIsUnfiltered(t *testing.T) {
	h := scopeHandler(t, nil, nil)
	admin := &auth.User{Username: "root", KubernetesUsername: "root", Roles: []string{"admin"}}

	_, all, err := h.accessibleNamespaces(httptest.NewRequest("GET", "/", nil), admin)
	if err != nil {
		t.Fatalf("accessibleNamespaces: %v", err)
	}
	if !all {
		t.Fatal("an admin was not given the unfiltered scope")
	}
}

// Without a namespace source the non-admin scope cannot be computed; that is
// an error (500), never a silent fall back to "all".
func TestAccessibleNamespaces_MissingListerFailsClosed(t *testing.T) {
	h := scopeHandler(t, nil, nil)
	user := &auth.User{Username: "carol", KubernetesUsername: "carol", Roles: []string{"user"}}

	if _, all, err := h.accessibleNamespaces(httptest.NewRequest("GET", "/", nil), user); err == nil || all {
		t.Fatalf("got all=%v err=%v, want an error", all, err)
	}
}

func TestNamespaceClause(t *testing.T) {
	if clause, _, ok := namespaceClause(true, []string{"ignored"}, 2); ok || clause != "" {
		t.Fatalf("all: got clause %q ok=%v, want no predicate", clause, ok)
	}

	for _, tc := range []struct {
		name string
		in   []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"team-a", []string{"team-a"}},
	} {
		clause, arg, ok := namespaceClause(false, tc.in, 4)
		if !ok {
			t.Errorf("%s: no predicate; a filtered scope must always add one", tc.name)
			continue
		}
		if want := "(n.resource_ns = ANY($4) OR n.resource_ns = '')"; clause != want {
			t.Errorf("%s: clause = %q, want %q", tc.name, clause, want)
		}
		if arg == nil {
			t.Errorf("%s: bound a nil slice, which pgx sends as NULL", tc.name)
		}
		if len(arg) != len(tc.in) {
			t.Errorf("%s: bound %v, want %v", tc.name, arg, tc.in)
		}
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL-backed.
// ---------------------------------------------------------------------------

func TestFeed_NamespaceScope(t *testing.T) {
	s := testNotifStore(t)
	ctx := t.Context()
	since := time.Now().Add(-time.Minute)

	// Namespaces unique to this run, so rows from other tests cannot match.
	nsA, nsB := uniqueName(t), uniqueName(t)
	insertIn := func(ns string) string {
		n := baseNotification(t)
		n.ResourceNS = ns
		return mustInsert(t, s, n)
	}
	idA, idB, idCluster := insertIn(nsA), insertIn(nsB), insertIn("")

	listed := func(namespaces []string, all bool) map[string]bool {
		t.Helper()
		got, _, err := s.ListNotifications(ctx, ListOpts{
			UserID: uniqueName(t), Namespaces: namespaces, AllNamespaces: all,
			Source: SourceDiagnostic, Since: since, Limit: 200,
		})
		if err != nil {
			t.Fatalf("ListNotifications: %v", err)
		}
		ids := map[string]bool{}
		for _, n := range got {
			ids[n.ID] = true
		}
		return ids
	}

	for _, tc := range []struct {
		name          string
		namespaces    []string
		all           bool
		wantA, wantB  bool
		wantClusterID bool
	}{
		{name: "non-admin with team-a", namespaces: []string{nsA}, wantA: true, wantClusterID: true},
		{name: "empty scope", namespaces: []string{}, wantClusterID: true},
		{name: "zero value", wantClusterID: true},
		{name: "admin", all: true, wantA: true, wantB: true, wantClusterID: true},
	} {
		ids := listed(tc.namespaces, tc.all)
		if ids[idA] != tc.wantA || ids[idB] != tc.wantB || ids[idCluster] != tc.wantClusterID {
			t.Errorf("%s: listed team-a=%v team-b=%v cluster=%v, want %v %v %v",
				tc.name, ids[idA], ids[idB], ids[idCluster], tc.wantA, tc.wantB, tc.wantClusterID)
		}
	}

	// Unread count: one fresh reader, so nothing has been read. Rows outside
	// this test move every count equally, so compare differences.
	reader := uniqueName(t)
	count := func(namespaces []string, all bool) int {
		t.Helper()
		c, err := s.UnreadCount(ctx, reader, namespaces, all)
		if err != nil {
			t.Fatalf("UnreadCount: %v", err)
		}
		return c
	}
	none, onlyA, both := count(nil, false), count([]string{nsA}, false), count([]string{nsA, nsB}, false)
	if onlyA-none != 1 {
		t.Errorf("team-a reader counted %d rows beyond cluster-scoped, want 1 (team-b must not count)", onlyA-none)
	}
	if both-onlyA != 1 {
		t.Errorf("adding team-b counted %d more rows, want 1", both-onlyA)
	}
	// The admin count used to bind a nil slice into "= ANY($2) OR = ''",
	// which counted cluster-scoped rows only.
	if admin := count(nil, true); admin < both {
		t.Errorf("admin unread count %d is below the team-a+team-b count %d", admin, both)
	}
}
