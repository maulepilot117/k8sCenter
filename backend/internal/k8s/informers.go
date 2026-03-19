package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	admissionregistrationv1listers "k8s.io/client-go/listers/admissionregistration/v1"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	autoscalingv2listers "k8s.io/client-go/listers/autoscaling/v2"
	batchv1listers "k8s.io/client-go/listers/batch/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	discoveryv1listers "k8s.io/client-go/listers/discovery/v1"
	networkingv1listers "k8s.io/client-go/listers/networking/v1"
	policyv1listers "k8s.io/client-go/listers/policy/v1"
	rbacv1listers "k8s.io/client-go/listers/rbac/v1"
	storagev1listers "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/tools/cache"
)

const defaultResyncPeriod = 5 * time.Minute

// CiliumPolicyGVR is the GroupVersionResource for CiliumNetworkPolicy.
// Defined here (not in resources/cilium.go) to avoid circular imports.
var CiliumPolicyGVR = schema.GroupVersionResource{
	Group:    "cilium.io",
	Version:  "v2",
	Resource: "ciliumnetworkpolicies",
}

// InformerManager wraps typed and dynamic informer factories and manages their lifecycle.
type InformerManager struct {
	factory    informers.SharedInformerFactory
	dynFactory dynamicinformer.DynamicSharedInformerFactory // nil if no CRDs detected
	logger     *slog.Logger
}

// NewInformerManager creates a new InformerManager with informers for all
// resource types KubeCenter needs to watch. The dynClient parameter may be nil
// if the dynamic client failed to initialize.
func NewInformerManager(clientset kubernetes.Interface, dynClient dynamic.Interface, disco discovery.DiscoveryInterface, logger *slog.Logger) *InformerManager {
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
	factory.Core().V1().PersistentVolumes().Informer()
	factory.Core().V1().Endpoints().Informer()
	factory.Core().V1().Events().Informer()
	factory.Core().V1().ResourceQuotas().Informer()
	factory.Core().V1().LimitRanges().Informer()
	factory.Core().V1().ServiceAccounts().Informer()

	// Apps resources
	factory.Apps().V1().Deployments().Informer()
	factory.Apps().V1().ReplicaSets().Informer()
	factory.Apps().V1().StatefulSets().Informer()
	factory.Apps().V1().DaemonSets().Informer()

	// Batch resources
	factory.Batch().V1().Jobs().Informer()
	factory.Batch().V1().CronJobs().Informer()

	// Policy resources
	factory.Policy().V1().PodDisruptionBudgets().Informer()

	// Networking resources
	factory.Networking().V1().Ingresses().Informer()
	factory.Networking().V1().NetworkPolicies().Informer()

	// Discovery resources
	factory.Discovery().V1().EndpointSlices().Informer()

	// RBAC resources (read-only viewer)
	factory.Rbac().V1().Roles().Informer()
	factory.Rbac().V1().ClusterRoles().Informer()
	factory.Rbac().V1().RoleBindings().Informer()
	factory.Rbac().V1().ClusterRoleBindings().Informer()

	// Autoscaling resources
	factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()

	// Storage resources (cluster-scoped)
	factory.Storage().V1().StorageClasses().Informer()
	factory.Storage().V1().CSIDrivers().Informer()

	// Admission registration resources (cluster-scoped, read-only)
	factory.Admissionregistration().V1().ValidatingWebhookConfigurations().Informer()
	factory.Admissionregistration().V1().MutatingWebhookConfigurations().Informer()

	mgr := &InformerManager{
		factory: factory,
		logger:  logger,
	}

	// Dynamic informer for CRDs — probe discovery first to avoid reflector
	// spin on clusters where the CRD is not installed.
	if dynClient != nil {
		mgr.dynFactory = probeCRDsAndCreateFactory(dynClient, disco, defaultResyncPeriod, logger)
	}

	typedCount := 31
	dynCount := 0
	if mgr.dynFactory != nil {
		dynCount = 1
	}
	logger.Info("informer manager created",
		"typedResources", typedCount,
		"dynamicResources", dynCount,
		"resyncPeriod", defaultResyncPeriod,
	)

	return mgr
}

// probeCRDsAndCreateFactory checks if known CRDs exist via discovery and creates
// a dynamic informer factory for those that are present. Returns nil if no CRDs found.
func probeCRDsAndCreateFactory(dynClient dynamic.Interface, disco discovery.DiscoveryInterface, resync time.Duration, logger *slog.Logger) dynamicinformer.DynamicSharedInformerFactory {
	_, err := disco.ServerResourcesForGroupVersion("cilium.io/v2")
	if err != nil {
		logger.Info("cilium.io/v2 CRD not found — skipping dynamic informer", "error", err)
		return nil
	}

	logger.Info("cilium.io/v2 CRD detected — registering dynamic informer")
	dynFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynClient, resync)
	dynFactory.ForResource(CiliumPolicyGVR)
	return dynFactory
}

// Start begins all registered informers. Call this once after creating the manager.
func (m *InformerManager) Start(ctx context.Context) {
	m.factory.Start(ctx.Done())
	if m.dynFactory != nil {
		m.dynFactory.Start(ctx.Done())
	}
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

	if m.dynFactory != nil {
		dynSynced := m.dynFactory.WaitForCacheSync(ctx.Done())
		for gvr, ok := range dynSynced {
			if !ok {
				m.logger.Warn("dynamic informer sync failed", "gvr", gvr.String())
				// Don't fail startup — the CRD may have been removed after discovery probe.
				// Handlers will get errors from the lister and return appropriate HTTP responses.
			}
		}
	}

	m.logger.Info("all informer caches synced")
	return nil
}

// Factory returns the underlying SharedInformerFactory for direct access to listers.
func (m *InformerManager) Factory() informers.SharedInformerFactory {
	return m.factory
}

// CiliumNetworkPolicies returns the dynamic lister for CiliumNetworkPolicy CRDs.
// Returns nil if the Cilium CRD was not detected at startup.
func (m *InformerManager) CiliumNetworkPolicies() cache.GenericLister {
	if m.dynFactory == nil {
		return nil
	}
	return m.dynFactory.ForResource(CiliumPolicyGVR).Lister()
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

func (m *InformerManager) PersistentVolumes() corev1listers.PersistentVolumeLister {
	return m.factory.Core().V1().PersistentVolumes().Lister()
}

func (m *InformerManager) Endpoints() corev1listers.EndpointsLister {
	return m.factory.Core().V1().Endpoints().Lister()
}

func (m *InformerManager) Events() corev1listers.EventLister {
	return m.factory.Core().V1().Events().Lister()
}

func (m *InformerManager) ResourceQuotas() corev1listers.ResourceQuotaLister {
	return m.factory.Core().V1().ResourceQuotas().Lister()
}

func (m *InformerManager) LimitRanges() corev1listers.LimitRangeLister {
	return m.factory.Core().V1().LimitRanges().Lister()
}

func (m *InformerManager) ServiceAccounts() corev1listers.ServiceAccountLister {
	return m.factory.Core().V1().ServiceAccounts().Lister()
}

// Typed lister accessors — Apps

func (m *InformerManager) Deployments() appsv1listers.DeploymentLister {
	return m.factory.Apps().V1().Deployments().Lister()
}

func (m *InformerManager) ReplicaSets() appsv1listers.ReplicaSetLister {
	return m.factory.Apps().V1().ReplicaSets().Lister()
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

// Typed lister accessors — Autoscaling

func (m *InformerManager) HorizontalPodAutoscalers() autoscalingv2listers.HorizontalPodAutoscalerLister {
	return m.factory.Autoscaling().V2().HorizontalPodAutoscalers().Lister()
}

// Typed lister accessors — Policy

func (m *InformerManager) PodDisruptionBudgets() policyv1listers.PodDisruptionBudgetLister {
	return m.factory.Policy().V1().PodDisruptionBudgets().Lister()
}

// Typed lister accessors — Networking

func (m *InformerManager) Ingresses() networkingv1listers.IngressLister {
	return m.factory.Networking().V1().Ingresses().Lister()
}

func (m *InformerManager) NetworkPolicies() networkingv1listers.NetworkPolicyLister {
	return m.factory.Networking().V1().NetworkPolicies().Lister()
}

// Typed lister accessors — Discovery

func (m *InformerManager) EndpointSlices() discoveryv1listers.EndpointSliceLister {
	return m.factory.Discovery().V1().EndpointSlices().Lister()
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

// Typed lister accessors — Storage

func (m *InformerManager) StorageClasses() storagev1listers.StorageClassLister {
	return m.factory.Storage().V1().StorageClasses().Lister()
}

func (m *InformerManager) CSIDrivers() storagev1listers.CSIDriverLister {
	return m.factory.Storage().V1().CSIDrivers().Lister()
}

// Typed lister accessors — Admission Registration

func (m *InformerManager) ValidatingWebhookConfigurations() admissionregistrationv1listers.ValidatingWebhookConfigurationLister {
	return m.factory.Admissionregistration().V1().ValidatingWebhookConfigurations().Lister()
}

func (m *InformerManager) MutatingWebhookConfigurations() admissionregistrationv1listers.MutatingWebhookConfigurationLister {
	return m.factory.Admissionregistration().V1().MutatingWebhookConfigurations().Lister()
}

// EventCallback is called when an informer observes a resource change.
type EventCallback func(eventType, kind, namespace, name string, obj any)

// RegisterEventHandlers wires up informer event handlers that invoke the
// callback on every add/update/delete. Call BEFORE Start() to avoid missing events.
// Secrets are excluded (not in informer cache).
func (m *InformerManager) RegisterEventHandlers(cb EventCallback) {
	type informerSpec struct {
		kind     string
		informer interface{ AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) }
	}

	specs := []informerSpec{
		{"pods", m.factory.Core().V1().Pods().Informer()},
		{"services", m.factory.Core().V1().Services().Informer()},
		{"configmaps", m.factory.Core().V1().ConfigMaps().Informer()},
		{"namespaces", m.factory.Core().V1().Namespaces().Informer()},
		{"nodes", m.factory.Core().V1().Nodes().Informer()},
		{"persistentvolumeclaims", m.factory.Core().V1().PersistentVolumeClaims().Informer()},
		{"persistentvolumes", m.factory.Core().V1().PersistentVolumes().Informer()},
		{"endpoints", m.factory.Core().V1().Endpoints().Informer()},
		{"events", m.factory.Core().V1().Events().Informer()},
		{"resourcequotas", m.factory.Core().V1().ResourceQuotas().Informer()},
		{"limitranges", m.factory.Core().V1().LimitRanges().Informer()},
		{"serviceaccounts", m.factory.Core().V1().ServiceAccounts().Informer()},
		{"deployments", m.factory.Apps().V1().Deployments().Informer()},
		{"replicasets", m.factory.Apps().V1().ReplicaSets().Informer()},
		{"statefulsets", m.factory.Apps().V1().StatefulSets().Informer()},
		{"daemonsets", m.factory.Apps().V1().DaemonSets().Informer()},
		{"jobs", m.factory.Batch().V1().Jobs().Informer()},
		{"cronjobs", m.factory.Batch().V1().CronJobs().Informer()},
		{"ingresses", m.factory.Networking().V1().Ingresses().Informer()},
		{"networkpolicies", m.factory.Networking().V1().NetworkPolicies().Informer()},
		{"endpointslices", m.factory.Discovery().V1().EndpointSlices().Informer()},
		{"poddisruptionbudgets", m.factory.Policy().V1().PodDisruptionBudgets().Informer()},
		{"horizontalpodautoscalers", m.factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()},
		{"roles", m.factory.Rbac().V1().Roles().Informer()},
		{"clusterroles", m.factory.Rbac().V1().ClusterRoles().Informer()},
		{"rolebindings", m.factory.Rbac().V1().RoleBindings().Informer()},
		{"clusterrolebindings", m.factory.Rbac().V1().ClusterRoleBindings().Informer()},
		{"storageclasses", m.factory.Storage().V1().StorageClasses().Informer()},
		{"csidrivers", m.factory.Storage().V1().CSIDrivers().Informer()},
		{"validatingwebhookconfigurations", m.factory.Admissionregistration().V1().ValidatingWebhookConfigurations().Informer()},
		{"mutatingwebhookconfigurations", m.factory.Admissionregistration().V1().MutatingWebhookConfigurations().Informer()},
	}

	// Dynamic CRD informers
	if m.dynFactory != nil {
		specs = append(specs, informerSpec{
			"ciliumnetworkpolicies", m.dynFactory.ForResource(CiliumPolicyGVR).Informer(),
		})
	}

	for _, spec := range specs {
		kind := spec.kind
		handler := cache.ResourceEventHandlerDetailedFuncs{
			AddFunc: func(obj interface{}, isInInitialList bool) {
				if isInInitialList {
					return // suppress initial sync flood
				}
				m.emitEvent(cb, "ADDED", kind, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldMeta, ok1 := oldObj.(metav1.Object)
				newMeta, ok2 := newObj.(metav1.Object)
				if ok1 && ok2 && oldMeta.GetResourceVersion() == newMeta.GetResourceVersion() {
					return // suppress resync noise
				}
				m.emitEvent(cb, "MODIFIED", kind, newObj)
			},
			DeleteFunc: func(obj interface{}) {
				if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = d.Obj
				}
				m.emitEvent(cb, "DELETED", kind, obj)
			},
		}
		if _, err := spec.informer.AddEventHandler(handler); err != nil {
			m.logger.Error("failed to add event handler", "kind", kind, "error", err)
		}
	}

	m.logger.Info("informer event handlers registered", "kinds", len(specs))
}

// emitEvent deep-copies a k8s object and invokes the callback with its metadata.
// Metadata is read AFTER deep copy to avoid races with the informer cache —
// particularly important for *unstructured.Unstructured where GetNamespace/GetName
// traverse a mutable map[string]interface{}.
func (m *InformerManager) emitEvent(cb EventCallback, eventType, kind string, obj interface{}) {
	// Deep copy first to avoid data races with informer cache
	if copier, ok := obj.(runtime.Object); ok {
		obj = copier.DeepCopyObject()
	}
	meta, ok := obj.(metav1.Object)
	if !ok {
		m.logger.Warn("event object does not implement metav1.Object", "kind", kind)
		return
	}
	cb(eventType, kind, meta.GetNamespace(), meta.GetName(), obj)
}
