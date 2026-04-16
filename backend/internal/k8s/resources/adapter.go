package resources

import (
	"context"
	"errors"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ResourceAdapter defines the per-resource contract for generic CRUD dispatch.
// Each adapter encapsulates the resource-specific logic (API calls, cache lookups)
// while the shared crud.go handlers provide auth, RBAC, audit, and error mapping.
type ResourceAdapter interface {
	// Kind returns the lowercase plural kind used in URL routing (e.g. "deployments").
	Kind() string

	// APIResource returns the Kubernetes API resource name for RBAC checks (e.g. "deployments").
	APIResource() string

	// DisplayName returns a human-friendly name for error messages (e.g. "Deployment").
	DisplayName() string

	// ClusterScoped returns true for cluster-scoped resources (e.g. Namespaces, ClusterRoles).
	// When true, CRUD handlers skip namespace extraction from the URL.
	ClusterScoped() bool

	// ListFromCache returns items from the informer cache, filtered by namespace and selector.
	// Namespace is ignored for cluster-scoped resources.
	ListFromCache(inf *k8s.InformerManager, ns string, sel labels.Selector) ([]any, error)

	// GetFromCache retrieves a single item from the informer cache by namespace and name.
	// Namespace is ignored for cluster-scoped resources.
	GetFromCache(inf *k8s.InformerManager, ns, name string) (any, error)

	// Create creates a new resource from the JSON body using an impersonating client.
	// The context should be the HTTP request context to propagate cancellation and timeouts.
	Create(ctx context.Context, cs kubernetes.Interface, ns string, body []byte) (any, error)

	// Update updates an existing resource from the JSON body using an impersonating client.
	// The context should be the HTTP request context to propagate cancellation and timeouts.
	Update(ctx context.Context, cs kubernetes.Interface, ns, name string, body []byte) (any, error)

	// Delete removes a resource by namespace and name using an impersonating client.
	// The context should be the HTTP request context to propagate cancellation and timeouts.
	Delete(ctx context.Context, cs kubernetes.Interface, ns, name string) error
}

// ReadOnlyAdapter provides default no-op implementations for Create, Update, and Delete
// that return 501 Not Implemented errors. Embed this in adapters for read-only resources
// (e.g. Events, Endpoints) that only support list/get from cache.
type ReadOnlyAdapter struct{}

// Create is not supported for read-only resources.
func (ReadOnlyAdapter) Create(_ context.Context, _ kubernetes.Interface, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

// Update is not supported for read-only resources.
func (ReadOnlyAdapter) Update(_ context.Context, _ kubernetes.Interface, _, _ string, _ []byte) (any, error) {
	return nil, errReadOnly
}

// Delete is not supported for read-only resources.
func (ReadOnlyAdapter) Delete(_ context.Context, _ kubernetes.Interface, _, _ string) error {
	return errReadOnly
}

var errReadOnly = errors.New("this resource is read-only and does not support this operation")

// ---------------------------------------------------------------------------
// Capability interfaces — adapters may optionally implement these for actions.
// The actions.go handlers type-assert against them at runtime.
// ---------------------------------------------------------------------------

// Scalable indicates a resource supports scale (e.g. Deployments, StatefulSets, ReplicaSets).
type Scalable interface {
	Scale(ctx context.Context, cs kubernetes.Interface, ns, name string, replicas int32) (any, error)
}

// Restartable indicates a resource supports rolling restart (e.g. Deployments, StatefulSets, DaemonSets).
type Restartable interface {
	Restart(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

// Suspendable indicates a resource supports suspend/resume (e.g. CronJobs).
type Suspendable interface {
	Suspend(ctx context.Context, cs kubernetes.Interface, ns, name string, suspend bool) (any, error)
}

// Triggerable indicates a resource supports manual triggering (e.g. CronJobs → create Job).
type Triggerable interface {
	Trigger(ctx context.Context, cs kubernetes.Interface, ns, name string) (any, error)
}

// Rollbackable indicates a resource supports rollback to a previous revision (e.g. Deployments).
type Rollbackable interface {
	Rollback(ctx context.Context, cs kubernetes.Interface, ns, name string, revision int64) (any, error)
}
