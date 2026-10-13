package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ErrImpersonationDenied means a remote cluster's registration credential is
// not allowed to impersonate an identity: a SelfSubjectAccessReview for the
// user (or service account) or one of the groups came back denied.
var ErrImpersonationDenied = errors.New("the cluster credential may not impersonate this identity")

// ImpersonationDeniedError names the review that came back denied: Resource
// is "users", "serviceaccounts" or "groups", Name the denied identity. It
// matches ErrImpersonationDenied under errors.Is.
type ImpersonationDeniedError struct {
	Resource string
	Name     string
}

func (e *ImpersonationDeniedError) Error() string {
	return fmt.Sprintf("%v: impersonate %s %q", ErrImpersonationDenied, e.Resource, e.Name)
}

func (e *ImpersonationDeniedError) Unwrap() error { return ErrImpersonationDenied }

// impersonationCheckTimeout bounds each review request of CanImpersonate.
const impersonationCheckTimeout = 10 * time.Second

// CanImpersonate reports whether clusterID's registration credential may
// impersonate username and every one of groups (#608, KTD2). It is the one
// remote read made with the credential's own permissions rather than an
// impersonated identity, and it reads only SelfSubjectAccessReviews.
//
// It returns nil when every review is allowed, an error wrapping
// ErrImpersonationDenied (an *ImpersonationDeniedError) when one is denied,
// and a TargetError (which
// ClassifyTargetErr understands) when the cluster could not be resolved,
// reached or authenticated to. The local cluster and an empty username are
// refused outright: the check is for remote clusters, and Kubernetes rejects
// group impersonation without a user.
func (cr *ClusterRouter) CanImpersonate(ctx context.Context, clusterID, username string, groups []string) error {
	if isLocalClusterID(clusterID) {
		return fmt.Errorf("impersonation rights are checked only on remote clusters, not %q", clusterID)
	}
	if username == "" {
		return errors.New("impersonation rights check needs a username: Kubernetes refuses group-only impersonation")
	}
	if err := cr.requireClusterStore(clusterID); err != nil {
		return TargetError{Err: err}
	}
	cfg, err := cr.registrationConfig(ctx, clusterID)
	if err != nil {
		return TargetError{Err: err}
	}
	cfg.Timeout = impersonationCheckTimeout
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return TargetError{Err: fmt.Errorf("creating review client for cluster %s: %w", clusterID, err)}
	}
	return checkImpersonateRights(ctx, client, username, groups)
}

// ServiceAccountUserPrefix starts the username of a service account.
const ServiceAccountUserPrefix = "system:serviceaccount:"

// impersonationReviews lists the resource attributes the API server checks
// before it lets a request impersonate username and groups. A
// system:serviceaccount:<ns>:<name> username is authorized as impersonate on
// serviceaccounts <name> in <ns>, not on users; any other username, including
// a malformed service-account one, is authorized on users.
func impersonationReviews(username string, groups []string) []authzv1.ResourceAttributes {
	user := authzv1.ResourceAttributes{Verb: "impersonate", Resource: "users", Name: username}
	if saRef, ok := strings.CutPrefix(username, ServiceAccountUserPrefix); ok {
		if ns, name, ok := strings.Cut(saRef, ":"); ok && ns != "" && name != "" && !strings.Contains(name, ":") {
			user = authzv1.ResourceAttributes{Verb: "impersonate", Resource: "serviceaccounts", Namespace: ns, Name: name}
		}
	}
	attrs := make([]authzv1.ResourceAttributes, 0, 1+len(groups))
	attrs = append(attrs, user)
	for _, g := range groups {
		attrs = append(attrs, authzv1.ResourceAttributes{Verb: "impersonate", Resource: "groups", Name: g})
	}
	return attrs
}

// checkImpersonateRights runs one SelfSubjectAccessReview per entry of
// impersonationReviews with client, which must carry no impersonation. It
// stops at the first denied review.
func checkImpersonateRights(ctx context.Context, client kubernetes.Interface, username string, groups []string) error {
	for _, attrs := range impersonationReviews(username, groups) {
		sar := &authzv1.SelfSubjectAccessReview{
			Spec: authzv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &attrs},
		}
		resp, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return TargetError{Err: fmt.Errorf("impersonation review on %s: %w", attrs.Resource, err)}
		}
		if !resp.Status.Allowed {
			return &ImpersonationDeniedError{Resource: attrs.Resource, Name: attrs.Name}
		}
	}
	return nil
}
