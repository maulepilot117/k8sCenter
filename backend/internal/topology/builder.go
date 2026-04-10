package topology

import (
	"context"
	"fmt"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// maxNodes caps the total number of nodes in a graph to prevent
// oversized responses for very large namespaces.
const maxNodes = 2000

// ResourceLister abstracts resource listing for the graph builder.
// Implemented by InformerLister (wraps InformerManager) and test fakes.
type ResourceLister interface {
	ListPods(ctx context.Context, namespace string) ([]*corev1.Pod, error)
	ListServices(ctx context.Context, namespace string) ([]*corev1.Service, error)
	ListDeployments(ctx context.Context, namespace string) ([]*appsv1.Deployment, error)
	ListReplicaSets(ctx context.Context, namespace string) ([]*appsv1.ReplicaSet, error)
	ListStatefulSets(ctx context.Context, namespace string) ([]*appsv1.StatefulSet, error)
	ListDaemonSets(ctx context.Context, namespace string) ([]*appsv1.DaemonSet, error)
	ListJobs(ctx context.Context, namespace string) ([]*batchv1.Job, error)
	ListCronJobs(ctx context.Context, namespace string) ([]*batchv1.CronJob, error)
	ListIngresses(ctx context.Context, namespace string) ([]*networkingv1.Ingress, error)
	ListConfigMaps(ctx context.Context, namespace string) ([]*corev1.ConfigMap, error)
	ListPVCs(ctx context.Context, namespace string) ([]*corev1.PersistentVolumeClaim, error)
	ListHPAs(ctx context.Context, namespace string) ([]*autoscalingv2.HorizontalPodAutoscaler, error)
}

// Builder constructs resource dependency graphs from a ResourceLister.
type Builder struct {
	lister ResourceLister
	logger *slog.Logger
}

// NewBuilder creates a new topology graph builder.
func NewBuilder(lister ResourceLister, logger *slog.Logger) *Builder {
	return &Builder{lister: lister, logger: logger}
}

// resourceMeta is a normalized representation of a k8s resource for generic node building.
type resourceMeta struct {
	uid       string
	name      string
	namespace string
	ownerRefs []metav1.OwnerReference
	obj       any
}

// healthFn computes health and summary for a resource.
type healthFn func(resourceMeta) (Health, string)

// canAccess checks if the user has "list" permission for the given resource.
func canAccess(ctx context.Context, user *auth.User, checker *resources.AccessChecker, resource, namespace string) bool {
	allowed, _ := checker.CanAccess(ctx, user.KubernetesUsername, user.KubernetesGroups, "list", resource, namespace)
	return allowed
}

// BuildNamespaceGraph builds a full resource dependency graph for a namespace.
func (b *Builder) BuildNamespaceGraph(ctx context.Context, namespace string, user *auth.User, checker *resources.AccessChecker) (*Graph, error) {
	graph := NewGraph()
	nameIndex := make(map[string]string) // "Kind/Name" -> UID

	// Pods
	var pods []*corev1.Pod
	if canAccess(ctx, user, checker, "pods", namespace) {
		r, err := b.lister.ListPods(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list pods", "namespace", namespace, "error", err)
		} else {
			pods = r
			addResourceNodes(graph, "Pod", nameIndex, toMetas(pods, func(p *corev1.Pod) resourceMeta {
				return resourceMeta{uid: string(p.UID), name: p.Name, namespace: p.Namespace, ownerRefs: p.OwnerReferences, obj: p}
			}), func(m resourceMeta) (Health, string) {
				return podHealth(m.obj.(*corev1.Pod))
			})
		}
	}

	// Services
	var services []*corev1.Service
	if canAccess(ctx, user, checker, "services", namespace) {
		r, err := b.lister.ListServices(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list services", "namespace", namespace, "error", err)
		} else {
			services = r
			addResourceNodes(graph, "Service", nameIndex, toMetas(services, func(s *corev1.Service) resourceMeta {
				return resourceMeta{uid: string(s.UID), name: s.Name, namespace: s.Namespace, obj: s}
			}), func(m resourceMeta) (Health, string) {
				svc := m.obj.(*corev1.Service)
				return HealthHealthy, fmt.Sprintf("type=%s", svc.Spec.Type)
			})
		}
	}

	// Deployments
	var deployments []*appsv1.Deployment
	if canAccess(ctx, user, checker, "deployments", namespace) {
		r, err := b.lister.ListDeployments(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list deployments", "namespace", namespace, "error", err)
		} else {
			deployments = r
			addResourceNodes(graph, "Deployment", nameIndex, toMetas(deployments, func(d *appsv1.Deployment) resourceMeta {
				return resourceMeta{uid: string(d.UID), name: d.Name, namespace: d.Namespace, ownerRefs: d.OwnerReferences, obj: d}
			}), func(m resourceMeta) (Health, string) {
				return deploymentHealth(m.obj.(*appsv1.Deployment))
			})
		}
	}

	// ReplicaSets
	var replicaSets []*appsv1.ReplicaSet
	if canAccess(ctx, user, checker, "replicasets", namespace) {
		r, err := b.lister.ListReplicaSets(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list replicasets", "namespace", namespace, "error", err)
		} else {
			replicaSets = r
			addResourceNodes(graph, "ReplicaSet", nameIndex, toMetas(replicaSets, func(rs *appsv1.ReplicaSet) resourceMeta {
				return resourceMeta{uid: string(rs.UID), name: rs.Name, namespace: rs.Namespace, ownerRefs: rs.OwnerReferences, obj: rs}
			}), func(m resourceMeta) (Health, string) {
				return replicaSetHealth(m.obj.(*appsv1.ReplicaSet))
			})
		}
	}

	// StatefulSets
	if canAccess(ctx, user, checker, "statefulsets", namespace) {
		r, err := b.lister.ListStatefulSets(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list statefulsets", "namespace", namespace, "error", err)
		} else {
			addResourceNodes(graph, "StatefulSet", nameIndex, toMetas(r, func(s *appsv1.StatefulSet) resourceMeta {
				return resourceMeta{uid: string(s.UID), name: s.Name, namespace: s.Namespace, ownerRefs: s.OwnerReferences, obj: s}
			}), func(m resourceMeta) (Health, string) {
				return statefulSetHealth(m.obj.(*appsv1.StatefulSet))
			})
		}
	}

	// DaemonSets
	if canAccess(ctx, user, checker, "daemonsets", namespace) {
		r, err := b.lister.ListDaemonSets(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list daemonsets", "namespace", namespace, "error", err)
		} else {
			addResourceNodes(graph, "DaemonSet", nameIndex, toMetas(r, func(d *appsv1.DaemonSet) resourceMeta {
				return resourceMeta{uid: string(d.UID), name: d.Name, namespace: d.Namespace, ownerRefs: d.OwnerReferences, obj: d}
			}), func(m resourceMeta) (Health, string) {
				return daemonSetHealth(m.obj.(*appsv1.DaemonSet))
			})
		}
	}

	// Jobs
	var jobs []*batchv1.Job
	if canAccess(ctx, user, checker, "jobs", namespace) {
		r, err := b.lister.ListJobs(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list jobs", "namespace", namespace, "error", err)
		} else {
			jobs = r
			addResourceNodes(graph, "Job", nameIndex, toMetas(jobs, func(j *batchv1.Job) resourceMeta {
				return resourceMeta{uid: string(j.UID), name: j.Name, namespace: j.Namespace, ownerRefs: j.OwnerReferences, obj: j}
			}), func(m resourceMeta) (Health, string) {
				return jobHealth(m.obj.(*batchv1.Job))
			})
		}
	}

	// CronJobs
	if canAccess(ctx, user, checker, "cronjobs", namespace) {
		r, err := b.lister.ListCronJobs(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list cronjobs", "namespace", namespace, "error", err)
		} else {
			addResourceNodes(graph, "CronJob", nameIndex, toMetas(r, func(c *batchv1.CronJob) resourceMeta {
				return resourceMeta{uid: string(c.UID), name: c.Name, namespace: c.Namespace, obj: c}
			}), func(m resourceMeta) (Health, string) {
				cj := m.obj.(*batchv1.CronJob)
				return HealthHealthy, fmt.Sprintf("schedule=%s", cj.Spec.Schedule)
			})
		}
	}

	// Ingresses
	var ingresses []*networkingv1.Ingress
	if canAccess(ctx, user, checker, "ingresses", namespace) {
		r, err := b.lister.ListIngresses(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list ingresses", "namespace", namespace, "error", err)
		} else {
			ingresses = r
			addResourceNodes(graph, "Ingress", nameIndex, toMetas(ingresses, func(i *networkingv1.Ingress) resourceMeta {
				return resourceMeta{uid: string(i.UID), name: i.Name, namespace: i.Namespace, obj: i}
			}), func(m resourceMeta) (Health, string) {
				ing := m.obj.(*networkingv1.Ingress)
				ruleCount := 0
				for _, rule := range ing.Spec.Rules {
					if rule.HTTP != nil {
						ruleCount += len(rule.HTTP.Paths)
					}
				}
				return HealthHealthy, fmt.Sprintf("%d rules", ruleCount)
			})
		}
	}

	// ConfigMaps
	if canAccess(ctx, user, checker, "configmaps", namespace) {
		r, err := b.lister.ListConfigMaps(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list configmaps", "namespace", namespace, "error", err)
		} else {
			addResourceNodes(graph, "ConfigMap", nameIndex, toMetas(r, func(c *corev1.ConfigMap) resourceMeta {
				return resourceMeta{uid: string(c.UID), name: c.Name, namespace: c.Namespace, obj: c}
			}), func(m resourceMeta) (Health, string) {
				cm := m.obj.(*corev1.ConfigMap)
				return HealthHealthy, fmt.Sprintf("%d keys", len(cm.Data))
			})
		}
	}

	// PVCs
	if canAccess(ctx, user, checker, "persistentvolumeclaims", namespace) {
		r, err := b.lister.ListPVCs(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list pvcs", "namespace", namespace, "error", err)
		} else {
			addResourceNodes(graph, "PersistentVolumeClaim", nameIndex, toMetas(r, func(p *corev1.PersistentVolumeClaim) resourceMeta {
				return resourceMeta{uid: string(p.UID), name: p.Name, namespace: p.Namespace, obj: p}
			}), func(m resourceMeta) (Health, string) {
				pvc := m.obj.(*corev1.PersistentVolumeClaim)
				return HealthHealthy, fmt.Sprintf("phase=%s", pvc.Status.Phase)
			})
		}
	}

	// HPAs
	var hpas []*autoscalingv2.HorizontalPodAutoscaler
	if canAccess(ctx, user, checker, "horizontalpodautoscalers", namespace) {
		r, err := b.lister.ListHPAs(ctx, namespace)
		if err != nil {
			b.logger.Warn("failed to list hpas", "namespace", namespace, "error", err)
		} else {
			hpas = r
			addResourceNodes(graph, "HorizontalPodAutoscaler", nameIndex, toMetas(hpas, func(h *autoscalingv2.HorizontalPodAutoscaler) resourceMeta {
				return resourceMeta{uid: string(h.UID), name: h.Name, namespace: h.Namespace, obj: h}
			}), func(m resourceMeta) (Health, string) {
				hpa := m.obj.(*autoscalingv2.HorizontalPodAutoscaler)
				return HealthHealthy, fmt.Sprintf("%d/%d replicas", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas)
			})
		}
	}

	// Check truncation
	if len(graph.Nodes) >= maxNodes {
		graph.Truncated = true
	}

	// Build node lookup map for edge building
	nodeMap := make(map[string]*Node, len(graph.Nodes))
	for i := range graph.Nodes {
		nodeMap[graph.Nodes[i].ID] = &graph.Nodes[i]
	}

	// Build edges
	graph.Edges = append(graph.Edges, buildOwnerEdges(pods, replicaSets, jobs, nodeMap)...)
	graph.Edges = append(graph.Edges, buildServiceSelectorEdges(services, pods, nodeMap)...)
	graph.Edges = append(graph.Edges, buildIngressEdges(ingresses, nameIndex)...)
	graph.Edges = append(graph.Edges, buildMountEdges(pods, nameIndex)...)
	graph.Edges = append(graph.Edges, buildHPAEdges(hpas, nameIndex)...)

	// Propagate health
	propagateHealth(nodeMap, graph.Edges)

	return graph, nil
}

// addResourceNodes adds nodes for a list of resources, respecting the node cap.
func addResourceNodes(graph *Graph, kind string, nameIndex map[string]string, items []resourceMeta, hfn healthFn) {
	for _, item := range items {
		if len(graph.Nodes) >= maxNodes {
			graph.Truncated = true
			return
		}
		health, summary := hfn(item)
		graph.Nodes = append(graph.Nodes, Node{
			ID:        item.uid,
			Kind:      kind,
			Name:      item.name,
			Namespace: item.namespace,
			Health:    health,
			Summary:   summary,
		})
		nameIndex[kind+"/"+item.name] = item.uid
	}
}

// toMetas converts a typed slice to []resourceMeta using a mapper function.
func toMetas[T any](items []T, fn func(T) resourceMeta) []resourceMeta {
	out := make([]resourceMeta, len(items))
	for i, item := range items {
		out[i] = fn(item)
	}
	return out
}

// --- Health computation ---

func podHealth(pod *corev1.Pod) (Health, string) {
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		return HealthHealthy, "Completed"
	case corev1.PodFailed:
		return HealthFailing, "Failed"
	case corev1.PodPending:
		return HealthDegraded, "Pending"
	}

	allReady := true
	readyCount := 0
	total := len(pod.Spec.Containers)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" {
				return HealthFailing, reason
			}
		}
		if cs.Ready {
			readyCount++
		} else {
			allReady = false
		}
	}

	if allReady && pod.Status.Phase == corev1.PodRunning {
		return HealthHealthy, fmt.Sprintf("%d/%d ready", readyCount, total)
	}
	return HealthDegraded, fmt.Sprintf("%d/%d ready", readyCount, total)
}

func deploymentHealth(dep *appsv1.Deployment) (Health, string) {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	ready := dep.Status.ReadyReplicas
	summary := fmt.Sprintf("%d/%d ready", ready, desired)

	if ready == desired {
		return HealthHealthy, summary
	}
	if ready > 0 {
		return HealthDegraded, summary
	}
	return HealthFailing, summary
}

func replicaSetHealth(rs *appsv1.ReplicaSet) (Health, string) {
	desired := int32(0)
	if rs.Spec.Replicas != nil {
		desired = *rs.Spec.Replicas
	}
	ready := rs.Status.ReadyReplicas
	health := HealthHealthy
	if desired > 0 {
		if ready == 0 {
			health = HealthFailing
		} else if ready < desired {
			health = HealthDegraded
		}
	}
	return health, fmt.Sprintf("%d/%d ready", ready, desired)
}

func statefulSetHealth(sts *appsv1.StatefulSet) (Health, string) {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	ready := sts.Status.ReadyReplicas
	summary := fmt.Sprintf("%d/%d ready", ready, desired)

	if ready == desired {
		return HealthHealthy, summary
	}
	if ready > 0 {
		return HealthDegraded, summary
	}
	return HealthFailing, summary
}

func daemonSetHealth(ds *appsv1.DaemonSet) (Health, string) {
	desired := ds.Status.DesiredNumberScheduled
	ready := ds.Status.NumberReady
	summary := fmt.Sprintf("%d/%d ready", ready, desired)

	if ready == desired {
		return HealthHealthy, summary
	}
	if ready > 0 {
		return HealthDegraded, summary
	}
	return HealthFailing, summary
}

func jobHealth(job *batchv1.Job) (Health, string) {
	if job.Status.Succeeded > 0 {
		return HealthHealthy, fmt.Sprintf("%d succeeded", job.Status.Succeeded)
	}
	if job.Status.Failed > 0 {
		return HealthFailing, fmt.Sprintf("%d failed", job.Status.Failed)
	}
	return HealthUnknown, fmt.Sprintf("%d active", job.Status.Active)
}

// --- Edge builders ---

func buildOwnerEdges(pods []*corev1.Pod, replicaSets []*appsv1.ReplicaSet, jobs []*batchv1.Job, nodeMap map[string]*Node) []Edge {
	var edges []Edge

	ownerEdge := func(childUID string, refs []metav1.OwnerReference) {
		for _, ref := range refs {
			ownerUID := string(ref.UID)
			if _, ok := nodeMap[ownerUID]; ok {
				if _, ok2 := nodeMap[childUID]; ok2 {
					edges = append(edges, Edge{Source: ownerUID, Target: childUID, Type: EdgeOwner})
				}
			}
		}
	}

	for _, pod := range pods {
		ownerEdge(string(pod.UID), pod.OwnerReferences)
	}
	for _, rs := range replicaSets {
		ownerEdge(string(rs.UID), rs.OwnerReferences)
	}
	for _, job := range jobs {
		ownerEdge(string(job.UID), job.OwnerReferences)
	}

	return edges
}

func buildServiceSelectorEdges(services []*corev1.Service, pods []*corev1.Pod, nodeMap map[string]*Node) []Edge {
	var edges []Edge

	for _, svc := range services {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		svcUID := string(svc.UID)
		if _, ok := nodeMap[svcUID]; !ok {
			continue
		}

		selector := labels.Set(svc.Spec.Selector).AsSelector()
		for _, pod := range pods {
			podUID := string(pod.UID)
			if _, ok := nodeMap[podUID]; !ok {
				continue
			}
			if selector.Matches(labels.Set(pod.Labels)) {
				edges = append(edges, Edge{
					Source: svcUID,
					Target: podUID,
					Type:   EdgeSelector,
				})
			}
		}
	}

	return edges
}

func buildIngressEdges(ingresses []*networkingv1.Ingress, nameIndex map[string]string) []Edge {
	var edges []Edge
	seen := make(map[string]bool) // dedup "source->target" keys

	addEdge := func(ingUID, svcUID string) {
		key := ingUID + "->" + svcUID
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, Edge{Source: ingUID, Target: svcUID, Type: EdgeIngress})
	}

	for _, ing := range ingresses {
		ingUID := string(ing.UID)

		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			if svcUID, ok := nameIndex["Service/"+ing.Spec.DefaultBackend.Service.Name]; ok {
				addEdge(ingUID, svcUID)
			}
		}

		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					if svcUID, ok := nameIndex["Service/"+path.Backend.Service.Name]; ok {
						addEdge(ingUID, svcUID)
					}
				}
			}
		}
	}

	return edges
}

func buildMountEdges(pods []*corev1.Pod, nameIndex map[string]string) []Edge {
	var edges []Edge

	for _, pod := range pods {
		podUID := string(pod.UID)

		for _, vol := range pod.Spec.Volumes {
			if vol.ConfigMap != nil {
				if cmUID, ok := nameIndex["ConfigMap/"+vol.ConfigMap.Name]; ok {
					edges = append(edges, Edge{Source: podUID, Target: cmUID, Type: EdgeMount})
				}
			}
			if vol.Secret != nil {
				if secUID, ok := nameIndex["Secret/"+vol.Secret.SecretName]; ok {
					edges = append(edges, Edge{Source: podUID, Target: secUID, Type: EdgeMount})
				}
			}
			if vol.PersistentVolumeClaim != nil {
				if pvcUID, ok := nameIndex["PersistentVolumeClaim/"+vol.PersistentVolumeClaim.ClaimName]; ok {
					edges = append(edges, Edge{Source: podUID, Target: pvcUID, Type: EdgeMount})
				}
			}
		}
	}

	return edges
}

func buildHPAEdges(hpas []*autoscalingv2.HorizontalPodAutoscaler, nameIndex map[string]string) []Edge {
	var edges []Edge

	for _, hpa := range hpas {
		hpaUID := string(hpa.UID)
		ref := hpa.Spec.ScaleTargetRef
		key := ref.Kind + "/" + ref.Name
		if targetUID, ok := nameIndex[key]; ok {
			edges = append(edges, Edge{Source: hpaUID, Target: targetUID, Type: EdgeSelector})
		}
	}

	return edges
}

// --- Health propagation ---

// propagateHealth walks edges and propagates child health to parents.
// If any child is failing, parent becomes at least degraded.
// If ALL children are failing, parent becomes failing.
// Degraded children also propagate to parent.
func propagateHealth(nodeMap map[string]*Node, edges []Edge) {
	// Build parent->children map (only owner and selector edges propagate health)
	children := make(map[string][]string)
	for _, e := range edges {
		if e.Type == EdgeMount || e.Type == EdgeIngress {
			continue
		}
		children[e.Source] = append(children[e.Source], e.Target)
	}

	// Iterate until stable (simple fixed-point)
	changed := true
	for iterations := 0; changed && iterations < 10; iterations++ {
		changed = false
		for parentID, childIDs := range children {
			parent, ok := nodeMap[parentID]
			if !ok {
				continue
			}

			if len(childIDs) == 0 {
				continue
			}

			allFailing := true
			anyFailing := false
			anyDegraded := false
			for _, childID := range childIDs {
				child, ok := nodeMap[childID]
				if !ok {
					continue
				}
				switch child.Health {
				case HealthFailing:
					anyFailing = true
				case HealthDegraded:
					anyDegraded = true
				default:
					allFailing = false
				}
			}

			var newHealth Health
			if allFailing && anyFailing {
				newHealth = HealthFailing
			} else if anyFailing || anyDegraded {
				newHealth = HealthDegraded
			} else {
				continue
			}

			// Only upgrade severity (healthy -> degraded -> failing)
			if healthSeverity(newHealth) > healthSeverity(parent.Health) {
				parent.Health = newHealth
				changed = true
			}
		}
	}
}

func healthSeverity(h Health) int {
	switch h {
	case HealthHealthy:
		return 0
	case HealthUnknown:
		return 1
	case HealthDegraded:
		return 2
	case HealthFailing:
		return 3
	default:
		return 0
	}
}
