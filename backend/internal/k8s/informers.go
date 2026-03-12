package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1listers "k8s.io/client-go/listers/apps/v1"
	batchv1listers "k8s.io/client-go/listers/batch/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	networkingv1listers "k8s.io/client-go/listers/networking/v1"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const defaultResyncPeriod = 5 * time.Minute

// InformerManager wraps a SharedInformerFactory and manages its lifecycle.
type InformerManager struct {
	factory informers.SharedInformerFactory
	logger  *slog.Logger
}

// NewInformerManager creates a new InformerManager with informers for all
// resource types KubeCenter needs to watch.
func NewInformerManager(clientset kubernetes.Interface, logger *slog.Logger) *InformerManager {
	factory := informers.NewSharedInformerFactory(clientset, defaultResyncPeriod)

	// Pre-register informers so the factory knows what to start.
	// Core resources
	factory.Core().V1().Pods().Informer()
	factory.Core().V1().Services().Informer()
	factory.Core().V1().ConfigMaps().Informer()
	// Secrets are intentionally NOT cached in the informer. They are fetched
	// on-demand via the impersonated client to avoid holding all cluster
	// secrets in process memory.
	factory.Core().V1().Namespaces().Informer()
	factory.Core().V1().Nodes().Informer()
	factory.Core().V1().PersistentVolumeClaims().Informer()
	factory.Core().V1().Events().Informer()

	// Apps resources
	factory.Apps().V1().Deployments().Informer()
	factory.Apps().V1().StatefulSets().Informer()
	factory.Apps().V1().DaemonSets().Informer()

	// Batch resources
	factory.Batch().V1().Jobs().Informer()
	factory.Batch().V1().CronJobs().Informer()

	// Networking resources
	factory.Networking().V1().Ingresses().Informer()
	factory.Networking().V1().NetworkPolicies().Informer()

	// RBAC resources (read-only viewer)
	factory.Rbac().V1().Roles().Informer()
	factory.Rbac().V1().ClusterRoles().Informer()
	factory.Rbac().V1().RoleBindings().Informer()
	factory.Rbac().V1().ClusterRoleBindings().Informer()

	logger.Info("informer manager created",
		"resourceTypes", 18,
		"resyncPeriod", defaultResyncPeriod,
	)

	return &InformerManager{
		factory: factory,
		logger:  logger,
	}
}

// Start begins all registered informers. Call this once after creating the manager.
func (m *InformerManager) Start(ctx context.Context) {
	m.factory.Start(ctx.Done())
	m.logger.Info("informers started")
}

// WaitForSync blocks until all informer caches are synced or the context is cancelled.
func (m *InformerManager) WaitForSync(ctx context.Context) error {
	m.logger.Info("waiting for informer cache sync")

	synced := m.factory.WaitForCacheSync(ctx.Done())
	for resource, ok := range synced {
		if !ok {
			return fmt.Errorf("informer cache sync failed for %v", resource)
		}
	}

	m.logger.Info("all informer caches synced")
	return nil
}

// Factory returns the underlying SharedInformerFactory for direct access to listers.
func (m *InformerManager) Factory() informers.SharedInformerFactory {
	return m.factory
}

// Typed lister accessors — Core

func (m *InformerManager) Pods() corev1listers.PodLister {
	return m.factory.Core().V1().Pods().Lister()
}

func (m *InformerManager) Services() corev1listers.ServiceLister {
	return m.factory.Core().V1().Services().Lister()
}

func (m *InformerManager) ConfigMaps() corev1listers.ConfigMapLister {
	return m.factory.Core().V1().ConfigMaps().Lister()
}

func (m *InformerManager) Namespaces() corev1listers.NamespaceLister {
	return m.factory.Core().V1().Namespaces().Lister()
}

func (m *InformerManager) Nodes() corev1listers.NodeLister {
	return m.factory.Core().V1().Nodes().Lister()
}

func (m *InformerManager) PersistentVolumeClaims() corev1listers.PersistentVolumeClaimLister {
	return m.factory.Core().V1().PersistentVolumeClaims().Lister()
}

func (m *InformerManager) Events() corev1listers.EventLister {
	return m.factory.Core().V1().Events().Lister()
}

// Typed lister accessors — Apps

func (m *InformerManager) Deployments() appsv1listers.DeploymentLister {
	return m.factory.Apps().V1().Deployments().Lister()
}

func (m *InformerManager) StatefulSets() appsv1listers.StatefulSetLister {
	return m.factory.Apps().V1().StatefulSets().Lister()
}

func (m *InformerManager) DaemonSets() appsv1listers.DaemonSetLister {
	return m.factory.Apps().V1().DaemonSets().Lister()
}

// Typed lister accessors — Batch

func (m *InformerManager) Jobs() batchv1listers.JobLister {
	return m.factory.Batch().V1().Jobs().Lister()
}

func (m *InformerManager) CronJobs() batchv1listers.CronJobLister {
	return m.factory.Batch().V1().CronJobs().Lister()
}

// Typed lister accessors — Networking

func (m *InformerManager) Ingresses() networkingv1listers.IngressLister {
	return m.factory.Networking().V1().Ingresses().Lister()
}

func (m *InformerManager) NetworkPolicies() networkingv1listers.NetworkPolicyLister {
	return m.factory.Networking().V1().NetworkPolicies().Lister()
}

// Typed lister accessors — RBAC

func (m *InformerManager) Roles() rbacv1listers.RoleLister {
	return m.factory.Rbac().V1().Roles().Lister()
}

func (m *InformerManager) ClusterRoles() rbacv1listers.ClusterRoleLister {
	return m.factory.Rbac().V1().ClusterRoles().Lister()
}

func (m *InformerManager) RoleBindings() rbacv1listers.RoleBindingLister {
	return m.factory.Rbac().V1().RoleBindings().Lister()
}

func (m *InformerManager) ClusterRoleBindings() rbacv1listers.ClusterRoleBindingLister {
	return m.factory.Rbac().V1().ClusterRoleBindings().Lister()
}
