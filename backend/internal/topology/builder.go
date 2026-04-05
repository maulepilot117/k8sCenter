package topology

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
)

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
	ListSecrets(ctx context.Context, namespace string) ([]*corev1.Secret, error)
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

// fetchedResources holds all resources fetched from the lister.
type fetchedResources struct {
	mu          sync.Mutex
	pods        []*corev1.Pod
	services    []*corev1.Service
	deployments []*appsv1.Deployment
	replicaSets []*appsv1.ReplicaSet
	statefulSets []*appsv1.StatefulSet
	daemonSets  []*appsv1.DaemonSet
	jobs        []*batchv1.Job
	cronJobs    []*batchv1.CronJob
	ingresses   []*networkingv1.Ingress
	configMaps  []*corev1.ConfigMap
	secrets     []*corev1.Secret
	pvcs        []*corev1.PersistentVolumeClaim
	hpas        []*autoscalingv2.HorizontalPodAutoscaler
}

// BuildNamespaceGraph builds a full resource dependency graph for a namespace.
func (b *Builder) BuildNamespaceGraph(ctx context.Context, namespace string) (*Graph, error) {
	res := b.fetchAll(ctx, namespace)
	graph := NewGraph()
	nodeMap := make(map[string]*Node)   // UID -> Node
	nameIndex := make(map[string]string) // "Kind/Name" -> UID (for name-based lookups)

	// Step 1: Build nodes
	b.addPodNodes(res.pods, nodeMap, nameIndex)
	b.addServiceNodes(res.services, nodeMap, nameIndex)
	b.addDeploymentNodes(res.deployments, nodeMap, nameIndex)
	b.addReplicaSetNodes(res.replicaSets, nodeMap, nameIndex)
	b.addStatefulSetNodes(res.statefulSets, nodeMap, nameIndex)
	b.addDaemonSetNodes(res.daemonSets, nodeMap, nameIndex)
	b.addJobNodes(res.jobs, nodeMap, nameIndex)
	b.addCronJobNodes(res.cronJobs, nodeMap, nameIndex)
	b.addIngressNodes(res.ingresses, nodeMap, nameIndex)
	b.addConfigMapNodes(res.configMaps, nodeMap, nameIndex)
	b.addSecretNodes(res.secrets, nodeMap, nameIndex)
	b.addPVCNodes(res.pvcs, nodeMap, nameIndex)
	b.addHPANodes(res.hpas, nodeMap, nameIndex)

	// Step 2: Build edges
	var edges []Edge
	edges = append(edges, b.buildOwnerEdges(res, nodeMap)...)
	edges = append(edges, b.buildServiceSelectorEdges(res.services, res.pods, nodeMap)...)
	edges = append(edges, b.buildIngressEdges(res.ingresses, nameIndex)...)
	edges = append(edges, b.buildMountEdges(res.pods, nameIndex)...)
	edges = append(edges, b.buildHPAEdges(res.hpas, nameIndex)...)

	// Step 3: Propagate health
	b.propagateHealth(nodeMap, edges)

	// Assemble graph
	for _, n := range nodeMap {
		graph.Nodes = append(graph.Nodes, *n)
	}
	graph.Edges = edges

	return graph, nil
}

// BuildFocusedGraph builds a subgraph centered on a specific resource,
// including all resources reachable by traversing edges in either direction.
func (b *Builder) BuildFocusedGraph(ctx context.Context, namespace, kind, name string) (*Graph, error) {
	full, err := b.BuildNamespaceGraph(ctx, namespace)
	if err != nil {
		return nil, err
	}

	// Find target node
	var targetID string
	for i := range full.Nodes {
		if full.Nodes[i].Kind == kind && full.Nodes[i].Name == name {
			targetID = full.Nodes[i].ID
			break
		}
	}
	if targetID == "" {
		return nil, fmt.Errorf("resource %s/%s not found in namespace %s", kind, name, namespace)
	}

	// Build adjacency list (bidirectional)
	adj := make(map[string][]string)
	for _, e := range full.Edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
		adj[e.Target] = append(adj[e.Target], e.Source)
	}

	// BFS from target
	visited := make(map[string]bool)
	queue := []string{targetID}
	visited[targetID] = true
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, neighbor := range adj[current] {
			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}

	// Build subgraph
	graph := NewGraph()
	for i := range full.Nodes {
		if visited[full.Nodes[i].ID] {
			graph.Nodes = append(graph.Nodes, full.Nodes[i])
		}
	}
	for _, e := range full.Edges {
		if visited[e.Source] && visited[e.Target] {
			graph.Edges = append(graph.Edges, e)
		}
	}

	return graph, nil
}

// SummarizeHealth counts nodes by health state.
func SummarizeHealth(g *Graph) HealthSummary {
	var s HealthSummary
	for i := range g.Nodes {
		s.Total++
		switch g.Nodes[i].Health {
		case HealthHealthy:
			s.Healthy++
		case HealthDegraded:
			s.Degraded++
		case HealthFailing:
			s.Failing++
		}
	}
	return s
}

// fetchAll retrieves all resources in parallel.
func (b *Builder) fetchAll(ctx context.Context, namespace string) *fetchedResources {
	res := &fetchedResources{}
	var wg sync.WaitGroup

	type fetcher struct {
		name string
		fn   func()
	}

	fetchers := []fetcher{
		{"pods", func() {
			r, err := b.lister.ListPods(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list pods", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.pods = r
			res.mu.Unlock()
		}},
		{"services", func() {
			r, err := b.lister.ListServices(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list services", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.services = r
			res.mu.Unlock()
		}},
		{"deployments", func() {
			r, err := b.lister.ListDeployments(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list deployments", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.deployments = r
			res.mu.Unlock()
		}},
		{"replicasets", func() {
			r, err := b.lister.ListReplicaSets(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list replicasets", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.replicaSets = r
			res.mu.Unlock()
		}},
		{"statefulsets", func() {
			r, err := b.lister.ListStatefulSets(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list statefulsets", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.statefulSets = r
			res.mu.Unlock()
		}},
		{"daemonsets", func() {
			r, err := b.lister.ListDaemonSets(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list daemonsets", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.daemonSets = r
			res.mu.Unlock()
		}},
		{"jobs", func() {
			r, err := b.lister.ListJobs(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list jobs", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.jobs = r
			res.mu.Unlock()
		}},
		{"cronjobs", func() {
			r, err := b.lister.ListCronJobs(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list cronjobs", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.cronJobs = r
			res.mu.Unlock()
		}},
		{"ingresses", func() {
			r, err := b.lister.ListIngresses(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list ingresses", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.ingresses = r
			res.mu.Unlock()
		}},
		{"configmaps", func() {
			r, err := b.lister.ListConfigMaps(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list configmaps", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.configMaps = r
			res.mu.Unlock()
		}},
		{"secrets", func() {
			r, err := b.lister.ListSecrets(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list secrets", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.secrets = r
			res.mu.Unlock()
		}},
		{"pvcs", func() {
			r, err := b.lister.ListPVCs(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list pvcs", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.pvcs = r
			res.mu.Unlock()
		}},
		{"hpas", func() {
			r, err := b.lister.ListHPAs(ctx, namespace)
			if err != nil {
				b.logger.Warn("failed to list hpas", "namespace", namespace, "error", err)
				return
			}
			res.mu.Lock()
			res.hpas = r
			res.mu.Unlock()
		}},
	}

	wg.Add(len(fetchers))
	for _, f := range fetchers {
		go func(fn func()) {
			defer wg.Done()
			fn()
		}(f.fn)
	}
	wg.Wait()

	return res
}

// --- Node builders ---

func (b *Builder) addPodNodes(pods []*corev1.Pod, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, pod := range pods {
		uid := string(pod.UID)
		health, summary := podHealth(pod)
		node := &Node{
			ID:        uid,
			Kind:      "Pod",
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Health:    health,
			Summary:   summary,
		}
		nodeMap[uid] = node
		nameIndex["Pod/"+pod.Name] = uid
	}
}

func (b *Builder) addServiceNodes(services []*corev1.Service, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, svc := range services {
		uid := string(svc.UID)
		node := &Node{
			ID:        uid,
			Kind:      "Service",
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("type=%s", svc.Spec.Type),
		}
		nodeMap[uid] = node
		nameIndex["Service/"+svc.Name] = uid
	}
}

func (b *Builder) addDeploymentNodes(deployments []*appsv1.Deployment, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, dep := range deployments {
		uid := string(dep.UID)
		health, summary := deploymentHealth(dep)
		node := &Node{
			ID:        uid,
			Kind:      "Deployment",
			Name:      dep.Name,
			Namespace: dep.Namespace,
			Health:    health,
			Summary:   summary,
		}
		nodeMap[uid] = node
		nameIndex["Deployment/"+dep.Name] = uid
	}
}

func (b *Builder) addReplicaSetNodes(replicaSets []*appsv1.ReplicaSet, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, rs := range replicaSets {
		uid := string(rs.UID)
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
		node := &Node{
			ID:        uid,
			Kind:      "ReplicaSet",
			Name:      rs.Name,
			Namespace: rs.Namespace,
			Health:    health,
			Summary:   fmt.Sprintf("%d/%d ready", ready, desired),
		}
		nodeMap[uid] = node
		nameIndex["ReplicaSet/"+rs.Name] = uid
	}
}

func (b *Builder) addStatefulSetNodes(statefulSets []*appsv1.StatefulSet, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, sts := range statefulSets {
		uid := string(sts.UID)
		health, summary := statefulSetHealth(sts)
		node := &Node{
			ID:        uid,
			Kind:      "StatefulSet",
			Name:      sts.Name,
			Namespace: sts.Namespace,
			Health:    health,
			Summary:   summary,
		}
		nodeMap[uid] = node
		nameIndex["StatefulSet/"+sts.Name] = uid
	}
}

func (b *Builder) addDaemonSetNodes(daemonSets []*appsv1.DaemonSet, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, ds := range daemonSets {
		uid := string(ds.UID)
		health, summary := daemonSetHealth(ds)
		node := &Node{
			ID:        uid,
			Kind:      "DaemonSet",
			Name:      ds.Name,
			Namespace: ds.Namespace,
			Health:    health,
			Summary:   summary,
		}
		nodeMap[uid] = node
		nameIndex["DaemonSet/"+ds.Name] = uid
	}
}

func (b *Builder) addJobNodes(jobs []*batchv1.Job, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, job := range jobs {
		uid := string(job.UID)
		health, summary := jobHealth(job)
		node := &Node{
			ID:        uid,
			Kind:      "Job",
			Name:      job.Name,
			Namespace: job.Namespace,
			Health:    health,
			Summary:   summary,
		}
		nodeMap[uid] = node
		nameIndex["Job/"+job.Name] = uid
	}
}

func (b *Builder) addCronJobNodes(cronJobs []*batchv1.CronJob, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, cj := range cronJobs {
		uid := string(cj.UID)
		node := &Node{
			ID:        uid,
			Kind:      "CronJob",
			Name:      cj.Name,
			Namespace: cj.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("schedule=%s", cj.Spec.Schedule),
		}
		nodeMap[uid] = node
		nameIndex["CronJob/"+cj.Name] = uid
	}
}

func (b *Builder) addIngressNodes(ingresses []*networkingv1.Ingress, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, ing := range ingresses {
		uid := string(ing.UID)
		ruleCount := 0
		for _, r := range ing.Spec.Rules {
			if r.HTTP != nil {
				ruleCount += len(r.HTTP.Paths)
			}
		}
		node := &Node{
			ID:        uid,
			Kind:      "Ingress",
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("%d rules", ruleCount),
		}
		nodeMap[uid] = node
		nameIndex["Ingress/"+ing.Name] = uid
	}
}

func (b *Builder) addConfigMapNodes(configMaps []*corev1.ConfigMap, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, cm := range configMaps {
		uid := string(cm.UID)
		node := &Node{
			ID:        uid,
			Kind:      "ConfigMap",
			Name:      cm.Name,
			Namespace: cm.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("%d keys", len(cm.Data)),
		}
		nodeMap[uid] = node
		nameIndex["ConfigMap/"+cm.Name] = uid
	}
}

func (b *Builder) addSecretNodes(secrets []*corev1.Secret, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, sec := range secrets {
		uid := string(sec.UID)
		node := &Node{
			ID:        uid,
			Kind:      "Secret",
			Name:      sec.Name,
			Namespace: sec.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("type=%s", sec.Type),
		}
		nodeMap[uid] = node
		nameIndex["Secret/"+sec.Name] = uid
	}
}

func (b *Builder) addPVCNodes(pvcs []*corev1.PersistentVolumeClaim, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, pvc := range pvcs {
		uid := string(pvc.UID)
		node := &Node{
			ID:        uid,
			Kind:      "PersistentVolumeClaim",
			Name:      pvc.Name,
			Namespace: pvc.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("phase=%s", pvc.Status.Phase),
		}
		nodeMap[uid] = node
		nameIndex["PersistentVolumeClaim/"+pvc.Name] = uid
	}
}

func (b *Builder) addHPANodes(hpas []*autoscalingv2.HorizontalPodAutoscaler, nodeMap map[string]*Node, nameIndex map[string]string) {
	for _, hpa := range hpas {
		uid := string(hpa.UID)
		node := &Node{
			ID:        uid,
			Kind:      "HorizontalPodAutoscaler",
			Name:      hpa.Name,
			Namespace: hpa.Namespace,
			Health:    HealthHealthy,
			Summary:   fmt.Sprintf("%d/%d replicas", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas),
		}
		nodeMap[uid] = node
		nameIndex["HorizontalPodAutoscaler/"+hpa.Name] = uid
	}
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

	// Check container statuses for CrashLoopBackOff / ImagePullBackOff
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
	active := job.Status.Active
	return HealthUnknown, fmt.Sprintf("%d active", active)
}

// --- Edge builders ---

func (b *Builder) buildOwnerEdges(res *fetchedResources, nodeMap map[string]*Node) []Edge {
	var edges []Edge

	// ownerEdge creates an EdgeOwner if both parent and child exist in nodeMap.
	ownerEdge := func(childUID, ownerUID string) {
		if _, ok := nodeMap[ownerUID]; ok {
			if _, ok2 := nodeMap[childUID]; ok2 {
				edges = append(edges, Edge{Source: ownerUID, Target: childUID, Type: EdgeOwner})
			}
		}
	}

	// Pods
	for _, pod := range res.pods {
		uid := string(pod.UID)
		for _, ref := range pod.OwnerReferences {
			ownerEdge(uid, string(ref.UID))
		}
	}
	// ReplicaSets
	for _, rs := range res.replicaSets {
		uid := string(rs.UID)
		for _, ref := range rs.OwnerReferences {
			ownerEdge(uid, string(ref.UID))
		}
	}
	// Jobs
	for _, job := range res.jobs {
		uid := string(job.UID)
		for _, ref := range job.OwnerReferences {
			ownerEdge(uid, string(ref.UID))
		}
	}

	return edges
}

func (b *Builder) buildServiceSelectorEdges(services []*corev1.Service, pods []*corev1.Pod, nodeMap map[string]*Node) []Edge {
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

func (b *Builder) buildIngressEdges(ingresses []*networkingv1.Ingress, nameIndex map[string]string) []Edge {
	var edges []Edge

	for _, ing := range ingresses {
		ingUID := string(ing.UID)

		// Default backend
		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			svcName := ing.Spec.DefaultBackend.Service.Name
			if svcUID, ok := nameIndex["Service/"+svcName]; ok {
				edges = append(edges, Edge{
					Source: ingUID,
					Target: svcUID,
					Type:   EdgeIngress,
				})
			}
		}

		// Rules
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					svcName := path.Backend.Service.Name
					if svcUID, ok := nameIndex["Service/"+svcName]; ok {
						edges = append(edges, Edge{
							Source: ingUID,
							Target: svcUID,
							Type:   EdgeIngress,
						})
					}
				}
			}
		}
	}

	return edges
}

func (b *Builder) buildMountEdges(pods []*corev1.Pod, nameIndex map[string]string) []Edge {
	var edges []Edge

	for _, pod := range pods {
		podUID := string(pod.UID)

		for _, vol := range pod.Spec.Volumes {
			if vol.ConfigMap != nil {
				if cmUID, ok := nameIndex["ConfigMap/"+vol.ConfigMap.Name]; ok {
					edges = append(edges, Edge{
						Source: podUID,
						Target: cmUID,
						Type:   EdgeMount,
					})
				}
			}
			if vol.Secret != nil {
				if secUID, ok := nameIndex["Secret/"+vol.Secret.SecretName]; ok {
					edges = append(edges, Edge{
						Source: podUID,
						Target: secUID,
						Type:   EdgeMount,
					})
				}
			}
			if vol.PersistentVolumeClaim != nil {
				if pvcUID, ok := nameIndex["PersistentVolumeClaim/"+vol.PersistentVolumeClaim.ClaimName]; ok {
					edges = append(edges, Edge{
						Source: podUID,
						Target: pvcUID,
						Type:   EdgeMount,
					})
				}
			}
		}
	}

	return edges
}

func (b *Builder) buildHPAEdges(hpas []*autoscalingv2.HorizontalPodAutoscaler, nameIndex map[string]string) []Edge {
	var edges []Edge

	for _, hpa := range hpas {
		hpaUID := string(hpa.UID)
		ref := hpa.Spec.ScaleTargetRef
		key := ref.Kind + "/" + ref.Name
		if targetUID, ok := nameIndex[key]; ok {
			edges = append(edges, Edge{
				Source: hpaUID,
				Target: targetUID,
				Type:   EdgeSelector,
			})
		}
	}

	return edges
}

// --- Health propagation ---

// propagateHealth walks edges and propagates child health to parents.
// If any child is failing, parent becomes at least degraded.
// If ALL children are failing, parent becomes failing.
func (b *Builder) propagateHealth(nodeMap map[string]*Node, edges []Edge) {
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

			allFailing := true
			anyFailing := false
			for _, childID := range childIDs {
				child, ok := nodeMap[childID]
				if !ok {
					continue
				}
				if child.Health == HealthFailing {
					anyFailing = true
				} else {
					allFailing = false
				}
			}

			if len(childIDs) == 0 {
				continue
			}

			var newHealth Health
			if allFailing {
				newHealth = HealthFailing
			} else if anyFailing {
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
