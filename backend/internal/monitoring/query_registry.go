package monitoring

// QueryDef is a server-owned PromQL query template gated by per-user RBAC.
// Non-admins can hit /api/v1/monitoring/queries/{slug}; admins keep raw /query access.
// Finding P2-4 of the 2026-05-22 security audit.
//
// Template syntax: Go text/template with two variables:
//   {{.Namespace}} — Kubernetes namespace (validated against k8s name rules)
//   {{.Name}}      — Resource name (validated against k8s name rules)
//
// RequiredGVR uses the format "resource.group" or just "resource" for core API group.
// An empty RequiredGVR skips the RBAC check (cluster-scoped resources without
// a direct k8s equivalent, e.g. node-exporter metrics).
type QueryDef struct {
	// Slug is the URL-safe identifier, e.g. "pods/cpu".
	Slug string

	// Template is the PromQL expression with {{.Namespace}} / {{.Name}} placeholders.
	Template string

	// RequiredVerbs are the RBAC verbs required (usually ["list"] or ["get"]).
	RequiredVerbs []string

	// RequiredGVR is the resource identifier checked via CanAccessGroupResource.
	// Format: "resource" for core group, "resource.group" for named group.
	// Empty string disables the RBAC sub-check (still requires authenticated user).
	RequiredGVR string

	// Description is surfaced in dashboard tooltips and API docs.
	Description string
}

// Registry is the canonical set of allowed non-admin PromQL slugs.
// Keyed by slug string for O(1) lookup.
var Registry = map[string]QueryDef{
	// ── pods ──────────────────────────────────────────────────────────────────
	"pods/cpu": {
		Slug:          "pods/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod="{{.Name}}",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Pod CPU usage in cores",
	},
	"pods/memory": {
		Slug:          "pods/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod="{{.Name}}",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Pod memory working set in MB",
	},
	"pods/network-rx": {
		Slug:          "pods/network-rx",
		Template:      `sum(rate(container_network_receive_bytes_total{namespace="{{.Namespace}}",pod="{{.Name}}"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Pod network receive KB/s",
	},
	"pods/network-tx": {
		Slug:          "pods/network-tx",
		Template:      `sum(rate(container_network_transmit_bytes_total{namespace="{{.Namespace}}",pod="{{.Name}}"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Pod network transmit KB/s",
	},
	"pods/restarts": {
		Slug:          "pods/restarts",
		Template:      `sum(kube_pod_container_status_restarts_total{namespace="{{.Namespace}}",pod="{{.Name}}"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Pod container restart count",
	},
	"pods/cilium-drops": {
		Slug:          "pods/cilium-drops",
		Template:      `sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{{.Namespace}}/{{.Name}}"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Cilium policy denied drops for pod",
	},

	// ── deployments ───────────────────────────────────────────────────────────
	"deployments/cpu": {
		Slug:          "deployments/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment CPU usage in cores",
	},
	"deployments/memory": {
		Slug:          "deployments/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment memory working set in MB",
	},
	"deployments/network-rx": {
		Slug:          "deployments/network-rx",
		Template:      `sum(rate(container_network_receive_bytes_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment network receive KB/s",
	},
	"deployments/network-tx": {
		Slug:          "deployments/network-tx",
		Template:      `sum(rate(container_network_transmit_bytes_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment network transmit KB/s",
	},
	"deployments/replicas": {
		Slug:          "deployments/replicas",
		Template:      `kube_deployment_status_replicas{namespace="{{.Namespace}}",deployment="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment current replica count",
	},
	"deployments/replicas-unavailable": {
		Slug:          "deployments/replicas-unavailable",
		Template:      `kube_deployment_status_replicas_unavailable{namespace="{{.Namespace}}",deployment="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment unavailable replica count",
	},
	"deployments/cpu-request": {
		Slug:          "deployments/cpu-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="cpu"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment CPU requests in cores",
	},
	"deployments/memory-request": {
		Slug:          "deployments/memory-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="memory"}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "deployments.apps",
		Description:   "Deployment memory requests in MB",
	},

	// ── statefulsets ──────────────────────────────────────────────────────────
	"statefulsets/cpu": {
		Slug:          "statefulsets/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet CPU usage in cores",
	},
	"statefulsets/memory": {
		Slug:          "statefulsets/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet memory working set in MB",
	},
	"statefulsets/network-rx": {
		Slug:          "statefulsets/network-rx",
		Template:      `sum(rate(container_network_receive_bytes_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet network receive KB/s",
	},
	"statefulsets/network-tx": {
		Slug:          "statefulsets/network-tx",
		Template:      `sum(rate(container_network_transmit_bytes_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet network transmit KB/s",
	},
	"statefulsets/replicas-ready": {
		Slug:          "statefulsets/replicas-ready",
		Template:      `kube_statefulset_status_replicas_ready{namespace="{{.Namespace}}",statefulset="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet ready replica count",
	},
	"statefulsets/cpu-request": {
		Slug:          "statefulsets/cpu-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="cpu"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet CPU requests in cores",
	},
	"statefulsets/memory-request": {
		Slug:          "statefulsets/memory-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="memory"}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "statefulsets.apps",
		Description:   "StatefulSet memory requests in MB",
	},

	// ── daemonsets ────────────────────────────────────────────────────────────
	"daemonsets/cpu": {
		Slug:          "daemonsets/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet CPU usage in cores",
	},
	"daemonsets/memory": {
		Slug:          "daemonsets/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet memory working set in MB",
	},
	"daemonsets/network-rx": {
		Slug:          "daemonsets/network-rx",
		Template:      `sum(rate(container_network_receive_bytes_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet network receive KB/s",
	},
	"daemonsets/ready": {
		Slug:          "daemonsets/ready",
		Template:      `kube_daemonset_status_number_ready{namespace="{{.Namespace}}",daemonset="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet ready pod count",
	},
	"daemonsets/cpu-request": {
		Slug:          "daemonsets/cpu-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="cpu"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet CPU requests in cores",
	},
	"daemonsets/memory-request": {
		Slug:          "daemonsets/memory-request",
		Template:      `sum(kube_pod_container_resource_requests{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",resource="memory"}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "daemonsets.apps",
		Description:   "DaemonSet memory requests in MB",
	},

	// ── nodes (cluster-scoped, no namespace RBAC check) ───────────────────────
	// .Name for nodes holds the node-exporter instance label (IP:9100 or node name).
	"nodes/cpu": {
		Slug:          "nodes/cpu",
		Template:      `100 - (avg(rate(node_cpu_seconds_total{mode="idle",instance=~"{{.Name}}"}[5m])) * 100)`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node CPU utilization %",
	},
	"nodes/memory": {
		Slug:          "nodes/memory",
		Template:      `100 * (1 - node_memory_MemAvailable_bytes{instance=~"{{.Name}}"} / node_memory_MemTotal_bytes{instance=~"{{.Name}}"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node memory utilization %",
	},
	"nodes/load": {
		Slug:          "nodes/load",
		Template:      `node_load5{instance=~"{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node 5-minute load average",
	},
	"nodes/network-rx": {
		Slug:          "nodes/network-rx",
		Template:      `sum(rate(node_network_receive_bytes_total{instance=~"{{.Name}}",device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node network receive MB/s",
	},
	"nodes/network-tx": {
		Slug:          "nodes/network-tx",
		Template:      `sum(rate(node_network_transmit_bytes_total{instance=~"{{.Name}}",device!~"veth.*|cali.*|lxc.*|cilium.*"}[5m])) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node network transmit MB/s",
	},
	"nodes/disk-read": {
		Slug:          "nodes/disk-read",
		Template:      `sum(rate(node_disk_read_bytes_total{instance=~"{{.Name}}"}[5m])) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node disk read MB/s",
	},
	"nodes/disk-write": {
		Slug:          "nodes/disk-write",
		Template:      `sum(rate(node_disk_written_bytes_total{instance=~"{{.Name}}"}[5m])) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node disk write MB/s",
	},
	// Node info query — resolves internal_ip for node-exporter instance matching.
	"nodes/info": {
		Slug:          "nodes/info",
		Template:      `kube_node_info{node="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "nodes",
		Description:   "Node info labels (used to resolve node-exporter instance)",
	},

	// ── replicasets ───────────────────────────────────────────────────────────
	"replicasets/cpu": {
		Slug:          "replicasets/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "replicasets.apps",
		Description:   "ReplicaSet CPU usage in cores",
	},
	"replicasets/memory": {
		Slug:          "replicasets/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "replicasets.apps",
		Description:   "ReplicaSet memory working set in MB",
	},

	// ── jobs ──────────────────────────────────────────────────────────────────
	"jobs/cpu": {
		Slug:          "jobs/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "jobs.batch",
		Description:   "Job CPU usage in cores",
	},
	"jobs/memory": {
		Slug:          "jobs/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "jobs.batch",
		Description:   "Job memory working set in MB",
	},

	// ── cronjobs ──────────────────────────────────────────────────────────────
	"cronjobs/cpu": {
		Slug:          "cronjobs/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "cronjobs.batch",
		Description:   "CronJob last-job CPU usage in cores",
	},
	"cronjobs/memory": {
		Slug:          "cronjobs/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Namespace}}",pod=~"{{.Name}}-.*",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "cronjobs.batch",
		Description:   "CronJob last-job memory working set in MB",
	},

	// ── namespaces ────────────────────────────────────────────────────────────
	// For namespaces, .Name is the namespace itself; no .Namespace param.
	"namespaces/cpu": {
		Slug:          "namespaces/cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Name}}",container!=""}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace total CPU usage in cores",
	},
	"namespaces/memory": {
		Slug:          "namespaces/memory",
		Template:      `sum(container_memory_working_set_bytes{namespace="{{.Name}}",container!=""}) / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace total memory working set in MB",
	},
	"namespaces/network-rx": {
		Slug:          "namespaces/network-rx",
		Template:      `sum(rate(container_network_receive_bytes_total{namespace="{{.Name}}"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace network receive KB/s",
	},
	"namespaces/network-tx": {
		Slug:          "namespaces/network-tx",
		Template:      `sum(rate(container_network_transmit_bytes_total{namespace="{{.Name}}"}[5m])) / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace network transmit KB/s",
	},
	"namespaces/pod-count": {
		Slug:          "namespaces/pod-count",
		Template:      `count(kube_pod_info{namespace="{{.Name}}"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace pod count",
	},
	"namespaces/cilium-drops": {
		Slug:          "namespaces/cilium-drops",
		Template:      `sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{{.Name}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "pods",
		Description:   "Namespace Cilium policy denied drops",
	},

	// ── pvcs ──────────────────────────────────────────────────────────────────
	"pvcs/usage": {
		Slug:          "pvcs/usage",
		Template:      `kubelet_volume_stats_used_bytes{namespace="{{.Namespace}}",persistentvolumeclaim="{{.Name}}"} / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumeclaims",
		Description:   "PVC volume usage in GiB",
	},
	"pvcs/capacity": {
		Slug:          "pvcs/capacity",
		Template:      `kubelet_volume_stats_capacity_bytes{namespace="{{.Namespace}}",persistentvolumeclaim="{{.Name}}"} / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumeclaims",
		Description:   "PVC volume capacity in GiB",
	},
	"pvcs/inodes": {
		Slug:          "pvcs/inodes",
		Template:      `kubelet_volume_stats_inodes_used{namespace="{{.Namespace}}",persistentvolumeclaim="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumeclaims",
		Description:   "PVC inodes used",
	},

	// ── pvs (cluster-scoped) ──────────────────────────────────────────────────
	"pvs/capacity": {
		Slug:          "pvs/capacity",
		Template:      `kube_persistentvolume_capacity_bytes{persistentvolume="{{.Name}}"} / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumes",
		Description:   "PV capacity in GiB",
	},
	"pvs/phase": {
		Slug:          "pvs/phase",
		Template:      `kube_persistentvolume_status_phase{persistentvolume="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumes",
		Description:   "PV status phase",
	},
	"pvs/pvc-usage": {
		Slug:          "pvs/pvc-usage",
		Template:      `kubelet_volume_stats_used_bytes{persistentvolumeclaim=~".*"} * on(persistentvolumeclaim, namespace) group_left kube_persistentvolumeclaim_info{volumename="{{.Name}}"} / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumes",
		Description:   "PV bound PVC usage in GiB",
	},
	"pvs/pvc-inodes": {
		Slug:          "pvs/pvc-inodes",
		Template:      `kubelet_volume_stats_inodes_used * on(persistentvolumeclaim, namespace) group_left kube_persistentvolumeclaim_info{volumename="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "persistentvolumes",
		Description:   "PV bound PVC inodes used",
	},

	// ── storageclasses (cluster-scoped) ───────────────────────────────────────
	"storageclasses/pv-count": {
		Slug:          "storageclasses/pv-count",
		Template:      `count(kube_persistentvolume_info{storageclass="{{.Name}}"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "storageclasses.storage.k8s.io",
		Description:   "StorageClass PV count",
	},
	"storageclasses/pvc-count": {
		Slug:          "storageclasses/pvc-count",
		Template:      `count(kube_persistentvolumeclaim_info{storageclass="{{.Name}}"})`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "storageclasses.storage.k8s.io",
		Description:   "StorageClass PVC count",
	},
	"storageclasses/provisioned": {
		Slug:          "storageclasses/provisioned",
		Template:      `sum(kube_persistentvolume_capacity_bytes{storageclass="{{.Name}}"}) / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "storageclasses.storage.k8s.io",
		Description:   "StorageClass total provisioned GiB",
	},
	"storageclasses/used": {
		Slug:          "storageclasses/used",
		Template:      `sum(kubelet_volume_stats_used_bytes * on(persistentvolumeclaim, namespace) group_left(storageclass) kube_persistentvolumeclaim_info{storageclass="{{.Name}}"}) / 1024 / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "storageclasses.storage.k8s.io",
		Description:   "StorageClass total used GiB",
	},

	// ── hpas ──────────────────────────────────────────────────────────────────
	"hpas/current-replicas": {
		Slug:          "hpas/current-replicas",
		Template:      `kube_horizontalpodautoscaler_status_current_replicas{namespace="{{.Namespace}}",horizontalpodautoscaler="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "horizontalpodautoscalers.autoscaling",
		Description:   "HPA current replica count",
	},
	"hpas/desired-replicas": {
		Slug:          "hpas/desired-replicas",
		Template:      `kube_horizontalpodautoscaler_status_desired_replicas{namespace="{{.Namespace}}",horizontalpodautoscaler="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "horizontalpodautoscalers.autoscaling",
		Description:   "HPA desired replica count",
	},

	// ── ingresses ─────────────────────────────────────────────────────────────
	"ingresses/request-rate": {
		Slug:          "ingresses/request-rate",
		Template:      `sum(rate(nginx_ingress_controller_requests{namespace="{{.Namespace}}",ingress="{{.Name}}"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ingresses.networking.k8s.io",
		Description:   "Ingress request rate req/s",
	},
	"ingresses/error-rate": {
		Slug:          "ingresses/error-rate",
		Template:      `sum(rate(nginx_ingress_controller_requests{namespace="{{.Namespace}}",ingress="{{.Name}}",status=~"5.."}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ingresses.networking.k8s.io",
		Description:   "Ingress 5xx error rate req/s",
	},

	// ── networkpolicies ───────────────────────────────────────────────────────
	"networkpolicies/cilium-forwarded": {
		Slug:          "networkpolicies/cilium-forwarded",
		Template:      `sum(rate(hubble_flows_processed_total{verdict="FORWARDED",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "networkpolicies.networking.k8s.io",
		Description:   "NetworkPolicy namespace Cilium forwarded flows",
	},
	"networkpolicies/cilium-dropped": {
		Slug:          "networkpolicies/cilium-dropped",
		Template:      `sum(rate(hubble_flows_processed_total{verdict="DROPPED",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "networkpolicies.networking.k8s.io",
		Description:   "NetworkPolicy namespace Cilium dropped flows",
	},
	"networkpolicies/policy-denied": {
		Slug:          "networkpolicies/policy-denied",
		Template:      `sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "networkpolicies.networking.k8s.io",
		Description:   "NetworkPolicy namespace policy denied drops",
	},
	"networkpolicies/tcp-syn": {
		Slug:          "networkpolicies/tcp-syn",
		Template:      `sum(rate(hubble_tcp_flags_total{flag="SYN",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "networkpolicies.networking.k8s.io",
		Description:   "NetworkPolicy namespace TCP SYN rate",
	},

	// ── ciliumnetworkpolicies ─────────────────────────────────────────────────
	"ciliumnetworkpolicies/cilium-forwarded": {
		Slug:          "ciliumnetworkpolicies/cilium-forwarded",
		Template:      `sum(rate(hubble_flows_processed_total{verdict="FORWARDED",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ciliumnetworkpolicies.cilium.io",
		Description:   "CiliumNetworkPolicy namespace forwarded flows",
	},
	"ciliumnetworkpolicies/cilium-dropped": {
		Slug:          "ciliumnetworkpolicies/cilium-dropped",
		Template:      `sum(rate(hubble_flows_processed_total{verdict="DROPPED",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ciliumnetworkpolicies.cilium.io",
		Description:   "CiliumNetworkPolicy namespace dropped flows",
	},
	"ciliumnetworkpolicies/policy-denied": {
		Slug:          "ciliumnetworkpolicies/policy-denied",
		Template:      `sum(rate(hubble_drop_total{reason="Policy denied",destination=~"{{.Namespace}}/.*"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ciliumnetworkpolicies.cilium.io",
		Description:   "CiliumNetworkPolicy namespace policy denied drops",
	},
	"ciliumnetworkpolicies/policy-verdicts": {
		Slug:          "ciliumnetworkpolicies/policy-verdicts",
		Template:      `sum(rate(hubble_policy_verdict_total{destination=~"{{.Namespace}}/.*"}[5m])) by (action)`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "ciliumnetworkpolicies.cilium.io",
		Description:   "CiliumNetworkPolicy namespace policy verdicts by action",
	},

	// ── pdbs ──────────────────────────────────────────────────────────────────
	"pdbs/current-healthy": {
		Slug:          "pdbs/current-healthy",
		Template:      `kube_poddisruptionbudget_status_current_healthy{namespace="{{.Namespace}}",poddisruptionbudget="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "poddisruptionbudgets.policy",
		Description:   "PDB current healthy pod count",
	},
	"pdbs/desired-healthy": {
		Slug:          "pdbs/desired-healthy",
		Template:      `kube_poddisruptionbudget_status_desired_healthy{namespace="{{.Namespace}}",poddisruptionbudget="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "poddisruptionbudgets.policy",
		Description:   "PDB desired healthy pod count",
	},
	"pdbs/disruptions-allowed": {
		Slug:          "pdbs/disruptions-allowed",
		Template:      `kube_poddisruptionbudget_status_pod_disruptions_allowed{namespace="{{.Namespace}}",poddisruptionbudget="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "poddisruptionbudgets.policy",
		Description:   "PDB disruptions currently allowed",
	},
	"pdbs/expected-pods": {
		Slug:          "pdbs/expected-pods",
		Template:      `kube_poddisruptionbudget_status_expected_pods{namespace="{{.Namespace}}",poddisruptionbudget="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "poddisruptionbudgets.policy",
		Description:   "PDB expected pod count",
	},

	// ── resourcequotas ────────────────────────────────────────────────────────
	"resourcequotas/cpu-used": {
		Slug:          "resourcequotas/cpu-used",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="requests.cpu",type="used"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota CPU requests used",
	},
	"resourcequotas/cpu-hard": {
		Slug:          "resourcequotas/cpu-hard",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="requests.cpu",type="hard"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota CPU requests hard limit",
	},
	"resourcequotas/memory-used": {
		Slug:          "resourcequotas/memory-used",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="requests.memory",type="used"} / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota memory requests used in MB",
	},
	"resourcequotas/memory-hard": {
		Slug:          "resourcequotas/memory-hard",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="requests.memory",type="hard"} / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota memory requests hard limit in MB",
	},
	"resourcequotas/pods-used": {
		Slug:          "resourcequotas/pods-used",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="pods",type="used"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota pods used",
	},
	"resourcequotas/pods-hard": {
		Slug:          "resourcequotas/pods-hard",
		Template:      `kube_resourcequota{namespace="{{.Namespace}}",resourcequota="{{.Name}}",resource="pods",type="hard"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "resourcequotas",
		Description:   "ResourceQuota pods hard limit",
	},

	// ── limitranges ───────────────────────────────────────────────────────────
	"limitranges/cpu-default": {
		Slug:          "limitranges/cpu-default",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="cpu",type="Container",constraint="default"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange default CPU limit",
	},
	"limitranges/memory-default": {
		Slug:          "limitranges/memory-default",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="memory",type="Container",constraint="default"} / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange default memory limit in MB",
	},
	"limitranges/cpu-min": {
		Slug:          "limitranges/cpu-min",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="cpu",type="Container",constraint="min"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange min CPU request",
	},
	"limitranges/cpu-max": {
		Slug:          "limitranges/cpu-max",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="cpu",type="Container",constraint="max"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange max CPU limit",
	},
	"limitranges/memory-min": {
		Slug:          "limitranges/memory-min",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="memory",type="Container",constraint="min"} / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange min memory request in MB",
	},
	"limitranges/memory-max": {
		Slug:          "limitranges/memory-max",
		Template:      `kube_limitrange{namespace="{{.Namespace}}",limitrange="{{.Name}}",resource="memory",type="Container",constraint="max"} / 1024 / 1024`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "limitranges",
		Description:   "LimitRange max memory limit in MB",
	},

	// ── endpoints ─────────────────────────────────────────────────────────────
	"endpoints/available": {
		Slug:          "endpoints/available",
		Template:      `kube_endpoint_address_available{namespace="{{.Namespace}}",endpoint="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "endpoints",
		Description:   "Endpoint available address count",
	},
	"endpoints/not-ready": {
		Slug:          "endpoints/not-ready",
		Template:      `kube_endpoint_address_not_ready{namespace="{{.Namespace}}",endpoint="{{.Name}}"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "endpoints",
		Description:   "Endpoint not-ready address count",
	},

	// ── endpointslices ────────────────────────────────────────────────────────
	"endpointslices/ready": {
		Slug:          "endpointslices/ready",
		Template:      `kube_endpointslice_endpoints{namespace="{{.Namespace}}",endpointslice="{{.Name}}",ready="true"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "endpointslices.discovery.k8s.io",
		Description:   "EndpointSlice ready endpoint count",
	},
	"endpointslices/serving": {
		Slug:          "endpointslices/serving",
		Template:      `kube_endpointslice_endpoints{namespace="{{.Namespace}}",endpointslice="{{.Name}}",serving="true"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "endpointslices.discovery.k8s.io",
		Description:   "EndpointSlice serving endpoint count",
	},
	"endpointslices/terminating": {
		Slug:          "endpointslices/terminating",
		Template:      `kube_endpointslice_endpoints{namespace="{{.Namespace}}",endpointslice="{{.Name}}",terminating="true"}`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "endpointslices.discovery.k8s.io",
		Description:   "EndpointSlice terminating endpoint count",
	},

	// ── validatingwebhookconfigurations (cluster-scoped) ─────────────────────
	"validatingwebhookconfigurations/latency-p99": {
		Slug:          "validatingwebhookconfigurations/latency-p99",
		Template:      `histogram_quantile(0.99, sum(rate(apiserver_admission_webhook_admission_duration_seconds_bucket{name="{{.Name}}",type="validating"}[5m])) by (le)) * 1000`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "validatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "ValidatingWebhookConfiguration admission latency p99 in ms",
	},
	"validatingwebhookconfigurations/request-rate": {
		Slug:          "validatingwebhookconfigurations/request-rate",
		Template:      `sum(rate(apiserver_admission_webhook_request_total{name="{{.Name}}",type="validating"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "validatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "ValidatingWebhookConfiguration request rate req/s",
	},
	"validatingwebhookconfigurations/rejection-rate": {
		Slug:          "validatingwebhookconfigurations/rejection-rate",
		Template:      `sum(rate(apiserver_admission_webhook_request_total{name="{{.Name}}",type="validating",rejected="true"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "validatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "ValidatingWebhookConfiguration rejection rate req/s",
	},

	// ── mutatingwebhookconfigurations (cluster-scoped) ────────────────────────
	"mutatingwebhookconfigurations/latency-p99": {
		Slug:          "mutatingwebhookconfigurations/latency-p99",
		Template:      `histogram_quantile(0.99, sum(rate(apiserver_admission_webhook_admission_duration_seconds_bucket{name="{{.Name}}",type="mutating"}[5m])) by (le)) * 1000`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "mutatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "MutatingWebhookConfiguration admission latency p99 in ms",
	},
	"mutatingwebhookconfigurations/request-rate": {
		Slug:          "mutatingwebhookconfigurations/request-rate",
		Template:      `sum(rate(apiserver_admission_webhook_request_total{name="{{.Name}}",type="mutating"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "mutatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "MutatingWebhookConfiguration request rate req/s",
	},
	"mutatingwebhookconfigurations/rejection-rate": {
		Slug:          "mutatingwebhookconfigurations/rejection-rate",
		Template:      `sum(rate(apiserver_admission_webhook_request_total{name="{{.Name}}",type="mutating",rejected="true"}[5m]))`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "mutatingwebhookconfigurations.admissionregistration.k8s.io",
		Description:   "MutatingWebhookConfiguration rejection rate req/s",
	},

	// ── services ──────────────────────────────────────────────────────────────
	"services/endpoint-cpu": {
		Slug:          "services/endpoint-cpu",
		Template:      `sum(rate(container_cpu_usage_seconds_total{namespace="{{.Namespace}}",pod=~".*",container!=""}[5m])) by (pod)`,
		RequiredVerbs: []string{"list"},
		RequiredGVR:   "services",
		Description:   "Service endpoint pods CPU usage in cores",
	},
}

// clusterScopedGVRs are resources where namespace RBAC checks use "" (cluster-scoped).
// For these, HandleSlugQuery passes namespace="" to CanAccessGroupResource.
var clusterScopedGVRs = map[string]bool{
	"nodes":              true,
	"persistentvolumes":  true,
	"storageclasses.storage.k8s.io":                        true,
	"validatingwebhookconfigurations.admissionregistration.k8s.io": true,
	"mutatingwebhookconfigurations.admissionregistration.k8s.io":   true,
}
