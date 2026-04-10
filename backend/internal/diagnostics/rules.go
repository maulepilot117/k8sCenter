package diagnostics

import (
	"context"
	"fmt"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func init() {
	registerRule("CrashLoopBackOff", SeverityCritical, []string{"Deployment", "StatefulSet", "DaemonSet", "Pod"}, checkCrashLoop)
	registerRule("ImagePullBackOff", SeverityCritical, []string{"Deployment", "StatefulSet", "DaemonSet", "Pod"}, checkImagePull)
	registerRule("PendingPod", SeverityCritical, []string{"Deployment", "StatefulSet", "DaemonSet", "Pod"}, checkPendingPod)
	registerRule("ReplicaMismatch", SeverityWarning, []string{"Deployment", "StatefulSet", "DaemonSet"}, checkReplicaMismatch)
	registerRule("ZeroEndpoints", SeverityWarning, []string{"Service"}, checkZeroEndpoints)
	registerRule("PendingPVC", SeverityWarning, []string{"PersistentVolumeClaim"}, checkPendingPVC)
}

// checkCrashLoop detects pods stuck in CrashLoopBackOff.
func checkCrashLoop(_ context.Context, target *DiagnosticTarget) Result {
	var affectedPods []string
	var totalRestarts int32

	for _, pod := range target.Pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				affectedPods = append(affectedPods, pod.Name)
				totalRestarts += cs.RestartCount
				break
			}
		}
		// Also check init container statuses
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				if !slices.Contains(affectedPods, pod.Name) {
					affectedPods = append(affectedPods, pod.Name)
				}
				totalRestarts += cs.RestartCount
				break
			}
		}
	}

	if len(affectedPods) == 0 {
		return Result{
			Status:  "pass",
			Message: "No pods in CrashLoopBackOff",
		}
	}

	links := make([]Link, 0, len(affectedPods))
	for _, name := range affectedPods {
		links = append(links, Link{Label: name, Kind: "Pod", Name: name})
	}

	return Result{
		Status:      "fail",
		Message:     fmt.Sprintf("%d pod(s) in CrashLoopBackOff with %d total restarts", len(affectedPods), totalRestarts),
		Detail:      fmt.Sprintf("Affected pods: %s", strings.Join(affectedPods, ", ")),
		Remediation: "Check container logs for crash reason: kubectl logs <pod> --previous",
		Links:       links,
	}
}

// checkImagePull detects pods with ImagePullBackOff or ErrImagePull errors.
func checkImagePull(_ context.Context, target *DiagnosticTarget) Result {
	type pullError struct {
		podName string
		image   string
		reason  string
	}

	var errors []pullError

	for _, pod := range target.Pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					errors = append(errors, pullError{
						podName: pod.Name,
						image:   cs.Image,
						reason:  reason,
					})
				}
			}
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
					errors = append(errors, pullError{
						podName: pod.Name,
						image:   cs.Image,
						reason:  reason,
					})
				}
			}
		}
	}

	if len(errors) == 0 {
		return Result{
			Status:  "pass",
			Message: "No image pull errors",
		}
	}

	// Collect unique images
	images := make(map[string]bool)
	links := make([]Link, 0, len(errors))
	for _, e := range errors {
		images[e.image] = true
		links = append(links, Link{Label: e.podName, Kind: "Pod", Name: e.podName})
	}

	imageList := make([]string, 0, len(images))
	for img := range images {
		imageList = append(imageList, img)
	}

	return Result{
		Status:      "fail",
		Message:     fmt.Sprintf("%d pod(s) with image pull errors", len(errors)),
		Detail:      fmt.Sprintf("Failed images: %s", strings.Join(imageList, ", ")),
		Remediation: "Verify the image exists and pull credentials are configured (imagePullSecrets)",
		Links:       links,
	}
}

// checkPendingPod detects pods stuck in Pending phase.
func checkPendingPod(_ context.Context, target *DiagnosticTarget) Result {
	var pendingPods []string
	var scheduleMessages []string

	for _, pod := range target.Pods {
		if pod.Status.Phase != corev1.PodPending {
			continue
		}
		pendingPods = append(pendingPods, pod.Name)

		// Look for scheduling failure conditions
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Message != "" {
				scheduleMessages = append(scheduleMessages, cond.Message)
			}
		}
	}

	if len(pendingPods) == 0 {
		return Result{
			Status:  "pass",
			Message: "No pods in Pending state",
		}
	}

	links := make([]Link, 0, len(pendingPods))
	for _, name := range pendingPods {
		links = append(links, Link{Label: name, Kind: "Pod", Name: name})
	}

	detail := fmt.Sprintf("Pending pods: %s", strings.Join(pendingPods, ", "))
	if len(scheduleMessages) > 0 {
		detail += fmt.Sprintf("; Schedule reason: %s", strings.Join(scheduleMessages, "; "))
	}

	return Result{
		Status:      "fail",
		Message:     fmt.Sprintf("%d pod(s) stuck in Pending", len(pendingPods)),
		Detail:      detail,
		Remediation: "Check node resources, taints, and affinity rules",
		Links:       links,
	}
}

// checkReplicaMismatch detects workloads where ready replicas don't match desired.
func checkReplicaMismatch(_ context.Context, target *DiagnosticTarget) Result {
	var desired, ready int32

	switch obj := target.Object.(type) {
	case *appsv1.Deployment:
		desired = 1
		if obj.Spec.Replicas != nil {
			desired = *obj.Spec.Replicas
		}
		ready = obj.Status.ReadyReplicas
	case *appsv1.StatefulSet:
		desired = 1
		if obj.Spec.Replicas != nil {
			desired = *obj.Spec.Replicas
		}
		ready = obj.Status.ReadyReplicas
	case *appsv1.DaemonSet:
		desired = obj.Status.DesiredNumberScheduled
		ready = obj.Status.NumberReady
	default:
		return Result{
			Status:  "pass",
			Message: "ReplicaMismatch not applicable",
		}
	}

	if ready >= desired {
		return Result{
			Status:  "pass",
			Message: fmt.Sprintf("All replicas ready (%d/%d)", ready, desired),
		}
	}

	status := "warn"
	if ready == 0 && desired > 0 {
		status = "fail"
	}

	return Result{
		Status:      status,
		Message:     fmt.Sprintf("Replica mismatch: %d/%d ready", ready, desired),
		Detail:      fmt.Sprintf("%s %q has %d ready of %d desired replicas", target.Kind, target.Name, ready, desired),
		Remediation: "Check pod events and resource quotas for scheduling failures",
	}
}

// checkZeroEndpoints detects services with a selector but no matching pods.
func checkZeroEndpoints(_ context.Context, target *DiagnosticTarget) Result {
	svc, ok := target.Object.(*corev1.Service)
	if !ok {
		return Result{
			Status:  "pass",
			Message: "ZeroEndpoints not applicable",
		}
	}

	// Services without selectors (e.g., ExternalName) are expected to have no pods
	if len(svc.Spec.Selector) == 0 {
		return Result{
			Status:  "pass",
			Message: "Service has no selector (external or headless)",
		}
	}

	if len(target.Pods) > 0 {
		return Result{
			Status:  "pass",
			Message: fmt.Sprintf("Service has %d matching pod(s)", len(target.Pods)),
		}
	}

	// Build selector string for detail
	var selectorParts []string
	for k, v := range svc.Spec.Selector {
		selectorParts = append(selectorParts, fmt.Sprintf("%s=%s", k, v))
	}

	return Result{
		Status:      "warn",
		Message:     "Service has zero matching pods",
		Detail:      fmt.Sprintf("Selector: %s", strings.Join(selectorParts, ", ")),
		Remediation: "Verify pods match the service selector labels",
	}
}

// checkPendingPVC detects PersistentVolumeClaims that are not bound.
func checkPendingPVC(_ context.Context, target *DiagnosticTarget) Result {
	pvc, ok := target.Object.(*corev1.PersistentVolumeClaim)
	if !ok {
		return Result{
			Status:  "pass",
			Message: "PendingPVC not applicable",
		}
	}

	if pvc.Status.Phase == corev1.ClaimBound {
		return Result{
			Status:  "pass",
			Message: "PVC is bound",
		}
	}

	storageClass := "<default>"
	if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		storageClass = *pvc.Spec.StorageClassName
	}

	requestedStorage := "unknown"
	if qty, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		requestedStorage = qty.String()
	}

	return Result{
		Status:      "warn",
		Message:     fmt.Sprintf("PVC is %s (not bound)", pvc.Status.Phase),
		Detail:      fmt.Sprintf("StorageClass: %s, Requested: %s", storageClass, requestedStorage),
		Remediation: "Check StorageClass provisioner status and available PersistentVolumes",
	}
}
