package policy

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// A failed access review is not a denial.
//
// These filters used to fold the error branch into `allowed = false`, so a
// namespace whose SelfSubjectAccessReview failed was dropped exactly as if
// the user had been refused it. The route then answered 200 with a SHORTER
// list and nothing to say why, and the cards built on it — policy violations
// and the compliance score — rendered that as a clean, compliant cluster. A
// security card reporting good news because its own permission check broke is
// the failure mode this release exists to prevent, so the distinction is
// pinned here in both directions.
func erroringHandler() *Handler {
	return &Handler{
		AccessChecker: resources.NewErroringAccessChecker(errors.New("apiserver unavailable")),
		Logger:        slog.Default(),
	}
}

func denyingHandler() *Handler {
	return &Handler{
		AccessChecker: resources.NewAlwaysDenyAccessChecker(),
		Logger:        slog.Default(),
	}
}

func testUser() *auth.User {
	return &auth.User{KubernetesUsername: "dev", KubernetesGroups: []string{"devs"}}
}

func TestFilterViolationsByRBAC_ErrorIsNotADenial(t *testing.T) {
	violations := []NormalizedViolation{
		{Namespace: "prod", Policy: "require-limits"},
		{Namespace: "staging", Policy: "require-limits"},
	}

	// Denied: a real answer. The list is filtered and the request succeeds.
	got, err := denyingHandler().filterViolationsByRBAC(context.Background(), testUser(), violations)
	if err != nil {
		t.Fatalf("a denial must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("denied namespaces must be filtered out, got %d", len(got))
	}

	// Errored: no answer. The caller must be told, not handed a short list
	// that looks exactly like the clean one above.
	got, err = erroringHandler().filterViolationsByRBAC(context.Background(), testUser(), violations)
	if err == nil {
		t.Fatal("a failed access review must surface as an error, not an empty list")
	}
	if got != nil {
		t.Fatalf("no partial list may be returned alongside the error, got %d items", len(got))
	}
}

func TestFilterPoliciesByRBAC_ErrorIsNotADenial(t *testing.T) {
	policies := []NormalizedPolicy{
		{Namespace: "prod", Name: "require-limits"},
	}

	got, err := denyingHandler().filterPoliciesByRBAC(context.Background(), testUser(), policies)
	if err != nil {
		t.Fatalf("a denial must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("denied namespaces must be filtered out, got %d", len(got))
	}

	got, err = erroringHandler().filterPoliciesByRBAC(context.Background(), testUser(), policies)
	if err == nil {
		t.Fatal("a failed access review must surface as an error, not an empty list")
	}
	if got != nil {
		t.Fatalf("no partial list may be returned alongside the error, got %d items", len(got))
	}
}

// Cluster-scoped items take a different branch that never consults the
// checker, so they must stay unaffected by a checker that is failing.
func TestFilterViolationsByRBAC_ClusterScopedUnaffectedByCheckerFailure(t *testing.T) {
	admin := &auth.User{KubernetesUsername: "root", KubernetesGroups: []string{"system:masters"}, Roles: []string{"admin"}}
	got, err := erroringHandler().filterViolationsByRBAC(
		context.Background(), admin, []NormalizedViolation{{Namespace: "", Policy: "cluster-rule"}},
	)
	if err != nil {
		t.Fatalf("no namespace means no access review to fail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("an admin keeps cluster-scoped violations, got %d", len(got))
	}
}
