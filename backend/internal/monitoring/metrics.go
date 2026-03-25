package monitoring

// QueryTemplates contains named PromQL query templates for each resource type.
// Variable values are validated against Kubernetes name regex before substitution.
var QueryTemplates = map[string]QueryTemplate{
	// Pod metrics
	"pod_cpu_usage": {
		Name:        "pod_cpu_usage",
		Description: "CPU usage in cores per container",
		Query:       `sum(rate(container_cpu_usage_seconds_total{container!="",pod="$pod",namespace="$namespace"}[5m])) by (container)`,
		Variables:   []string{"namespace", "pod"},
	},
	"pod_memory_usage": {
		Name:        "pod_memory_usage",
		Description: "Memory working set bytes per container",
		Query:       `sum(container_memory_working_set_bytes{container!="",pod="$pod",namespace="$namespace"}) by (container)`,
		Variables:   []string{"namespace", "pod"},
	},
	"pod_network_rx": {
		Name:        "pod_network_rx",
		Description: "Network receive bytes per second",
		Query:       `sum(rate(container_network_receive_bytes_total{pod="$pod",namespace="$namespace"}[5m]))`,
		Variables:   []string{"namespace", "pod"},
	},
	"pod_network_tx": {
		Name:        "pod_network_tx",
		Description: "Network transmit bytes per second",
		Query:       `sum(rate(container_network_transmit_bytes_total{pod="$pod",namespace="$namespace"}[5m]))`,
		Variables:   []string{"namespace", "pod"},
	},

	// Node metrics
	"node_cpu_utilization": {
		Name:        "node_cpu_utilization",
		Description: "Node CPU utilization percentage",
		Query:       `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle",instance=~"$node.*"}[5m])) * 100)`,
		Variables:   []string{"node"},
	},
	"node_memory_utilization": {
		Name:        "node_memory_utilization",
		Description: "Node memory utilization percentage",
		Query:       `100 * (1 - node_memory_MemAvailable_bytes{instance=~"$node.*"} / node_memory_MemTotal_bytes{instance=~"$node.*"})`,
		Variables:   []string{"node"},
	},
	"node_disk_utilization": {
		Name:        "node_disk_utilization",
		Description: "Node root disk utilization percentage",
		Query:       `100 - (node_filesystem_avail_bytes{instance=~"$node.*",mountpoint="/",fstype!="rootfs"} / node_filesystem_size_bytes{instance=~"$node.*",mountpoint="/",fstype!="rootfs"} * 100)`,
		Variables:   []string{"node"},
	},
	"node_pod_count": {
		Name:        "node_pod_count",
		Description: "Number of pods on the node",
		Query:       `count(kube_pod_info{node="$node"})`,
		Variables:   []string{"node"},
	},

	// Deployment metrics
	"deployment_replica_health": {
		Name:        "deployment_replica_health",
		Description: "Ratio of available to desired replicas",
		Query:       `kube_deployment_status_replicas_available{namespace="$namespace",deployment="$deployment"} / kube_deployment_spec_replicas{namespace="$namespace",deployment="$deployment"}`,
		Variables:   []string{"namespace", "deployment"},
	},

	// PVC metrics
	"pvc_usage_percent": {
		Name:        "pvc_usage_percent",
		Description: "PVC storage usage percentage",
		Query:       `kubelet_volume_stats_used_bytes{namespace="$namespace",persistentvolumeclaim="$pvc"} / kubelet_volume_stats_capacity_bytes{namespace="$namespace",persistentvolumeclaim="$pvc"} * 100`,
		Variables:   []string{"namespace", "pvc"},
	},

	// PodDisruptionBudget metrics
	"pdb_current_healthy": {
		Name:        "pdb_current_healthy",
		Description: "Number of currently healthy pods for the PDB",
		Query:       `kube_poddisruptionbudget_status_current_healthy{namespace="$namespace",poddisruptionbudget="$pdb"}`,
		Variables:   []string{"namespace", "pdb"},
	},
	"pdb_desired_healthy": {
		Name:        "pdb_desired_healthy",
		Description: "Minimum desired healthy pods for the PDB",
		Query:       `kube_poddisruptionbudget_status_desired_healthy{namespace="$namespace",poddisruptionbudget="$pdb"}`,
		Variables:   []string{"namespace", "pdb"},
	},
	"pdb_disruptions_allowed": {
		Name:        "pdb_disruptions_allowed",
		Description: "Number of pod disruptions currently allowed",
		Query:       `kube_poddisruptionbudget_status_pod_disruptions_allowed{namespace="$namespace",poddisruptionbudget="$pdb"}`,
		Variables:   []string{"namespace", "pdb"},
	},
	"pdb_expected_pods": {
		Name:        "pdb_expected_pods",
		Description: "Total number of pods counted by the PDB",
		Query:       `kube_poddisruptionbudget_status_expected_pods{namespace="$namespace",poddisruptionbudget="$pdb"}`,
		Variables:   []string{"namespace", "pdb"},
	},

	// ResourceQuota metrics
	"resourcequota_cpu_used": {
		Name:        "resourcequota_cpu_used",
		Description: "CPU requests currently used in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="requests.cpu",type="used"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},
	"resourcequota_cpu_hard": {
		Name:        "resourcequota_cpu_hard",
		Description: "CPU requests hard limit in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="requests.cpu",type="hard"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},
	"resourcequota_memory_used": {
		Name:        "resourcequota_memory_used",
		Description: "Memory requests currently used in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="requests.memory",type="used"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},
	"resourcequota_memory_hard": {
		Name:        "resourcequota_memory_hard",
		Description: "Memory requests hard limit in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="requests.memory",type="hard"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},
	"resourcequota_pods_used": {
		Name:        "resourcequota_pods_used",
		Description: "Pods currently used in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="pods",type="used"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},
	"resourcequota_pods_hard": {
		Name:        "resourcequota_pods_hard",
		Description: "Pods hard limit in quota",
		Query:       `kube_resourcequota{namespace="$namespace",resourcequota="$resourcequota",resource="pods",type="hard"}`,
		Variables:   []string{"namespace", "resourcequota"},
	},

	// LimitRange metrics
	"limitrange_cpu_default": {
		Name:        "limitrange_cpu_default",
		Description: "Default CPU limit for containers",
		Query:       `kube_limitrange{namespace="$namespace",limitrange="$limitrange",resource="cpu",type="Container",constraint="default"}`,
		Variables:   []string{"namespace", "limitrange"},
	},
	"limitrange_memory_default": {
		Name:        "limitrange_memory_default",
		Description: "Default memory limit for containers",
		Query:       `kube_limitrange{namespace="$namespace",limitrange="$limitrange",resource="memory",type="Container",constraint="default"}`,
		Variables:   []string{"namespace", "limitrange"},
	},

	// Endpoint metrics
	"endpoint_address_available": {
		Name:        "endpoint_address_available",
		Description: "Number of available (ready) addresses",
		Query:       `kube_endpoint_address_available{namespace="$namespace",endpoint="$endpoint"}`,
		Variables:   []string{"namespace", "endpoint"},
	},
	"endpoint_address_not_ready": {
		Name:        "endpoint_address_not_ready",
		Description: "Number of not-ready addresses",
		Query:       `kube_endpoint_address_not_ready{namespace="$namespace",endpoint="$endpoint"}`,
		Variables:   []string{"namespace", "endpoint"},
	},

	// Webhook metrics (from kube-apiserver)
	"webhook_admission_latency_p99": {
		Name:        "webhook_admission_latency_p99",
		Description: "99th percentile admission webhook latency in milliseconds",
		Query:       `histogram_quantile(0.99, sum(rate(apiserver_admission_webhook_admission_duration_seconds_bucket{name="$webhook"}[5m])) by (le)) * 1000`,
		Variables:   []string{"webhook"},
	},
	"webhook_request_rate": {
		Name:        "webhook_request_rate",
		Description: "Admission webhook requests per second",
		Query:       `sum(rate(apiserver_admission_webhook_request_total{name="$webhook"}[5m]))`,
		Variables:   []string{"webhook"},
	},
	"webhook_rejection_rate": {
		Name:        "webhook_rejection_rate",
		Description: "Admission webhook rejections per second",
		Query:       `sum(rate(apiserver_admission_webhook_request_total{name="$webhook",rejected="true"}[5m]))`,
		Variables:   []string{"webhook"},
	},

	// CiliumNetworkPolicy metrics (via Hubble)
	"cilium_policy_forwarded": {
		Name:        "cilium_policy_forwarded",
		Description: "Forwarded flows in namespace (Hubble)",
		Query:       `sum(rate(hubble_flows_processed_total{verdict="FORWARDED",destination=~"$namespace/.*"}[5m]))`,
		Variables:   []string{"namespace"},
	},
	"cilium_policy_dropped": {
		Name:        "cilium_policy_dropped",
		Description: "Dropped flows in namespace (Hubble)",
		Query:       `sum(rate(hubble_flows_processed_total{verdict="DROPPED",destination=~"$namespace/.*"}[5m]))`,
		Variables:   []string{"namespace"},
	},
	"cilium_policy_denied": {
		Name:        "cilium_policy_denied",
		Description: "Policy denied drops in namespace (Hubble)",
		Query:       `sum(rate(hubble_drop_total{reason="Policy denied",destination=~"$namespace/.*"}[5m]))`,
		Variables:   []string{"namespace"},
	},
}

// ResourceDashboardMap maps resource kinds to their Grafana dashboard UIDs
// and template variable names.
var ResourceDashboardMap = map[string]struct {
	UID     string
	VarName string
}{
	"pods":                    {UID: "kubecenter-pod-detail", VarName: "pod"},
	"deployments":            {UID: "kubecenter-deployment-detail", VarName: "deployment"},
	"statefulsets":            {UID: "kubecenter-statefulset-detail", VarName: "statefulset"},
	"daemonsets":              {UID: "kubecenter-daemonset-detail", VarName: "daemonset"},
	"nodes":                  {UID: "kubecenter-node-detail", VarName: "node"},
	"persistentvolumeclaims": {UID: "kubecenter-pvc-detail", VarName: "pvc"},
}
