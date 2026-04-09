package networking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	agentCacheTTL      = 30 * time.Second
	agentOuterTimeout  = 30 * time.Second
	agentExecTimeout   = 5 * time.Second
	agentMaxConcurrent = 5
	agentMaxOutput     = 1 << 20 // 1 MB stdout cap
	agentContainer     = "cilium-agent"
)

var agentCommand = []string{"cilium-dbg", "status", "-o", "json"}

var agentPodLabels = []string{
	"app.kubernetes.io/name=cilium-agent",
	"k8s-app=cilium",
}

// CiliumAgentCollector execs cilium-dbg into Cilium agent pods to collect
// diagnostic data (WireGuard peers, ClusterMesh, Envoy proxy, node health).
// Uses service account credentials (not user impersonation) — see plan for rationale.
type CiliumAgentCollector struct {
	k8sClient   *k8s.ClientFactory
	auditLogger audit.Logger
	logger      *slog.Logger

	group   singleflight.Group
	cacheMu sync.RWMutex
	cache   *agentCollectionResult
}

// NewCiliumAgentCollector creates a collector for Cilium agent diagnostics.
func NewCiliumAgentCollector(k8sClient *k8s.ClientFactory, auditLogger audit.Logger, logger *slog.Logger) *CiliumAgentCollector {
	return &CiliumAgentCollector{
		k8sClient:   k8sClient,
		auditLogger: auditLogger,
		logger:      logger,
	}
}

// InvalidateCache clears the agent collection cache.
func (c *CiliumAgentCollector) InvalidateCache() {
	c.cacheMu.Lock()
	c.cache = nil
	c.cacheMu.Unlock()
}

// Collect runs cilium-dbg status on all Cilium agent pods and returns parsed results.
// Results are cached for 30s and coalesced via singleflight.
func (c *CiliumAgentCollector) Collect(ctx context.Context) (*agentCollectionResult, error) {
	// Check cache
	c.cacheMu.RLock()
	if c.cache != nil && time.Since(c.cache.collected) < agentCacheTTL {
		result := c.cache
		c.cacheMu.RUnlock()
		return result, nil
	}
	c.cacheMu.RUnlock()

	// Singleflight coalesces concurrent requests
	v, err, _ := c.group.Do("agent-collect", func() (any, error) {
		return c.collect(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.(*agentCollectionResult), nil
}

func (c *CiliumAgentCollector) collect(ctx context.Context) (*agentCollectionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, agentOuterTimeout)
	defer cancel()

	cs := c.k8sClient.BaseClientset()

	// Find Cilium agent pods
	pods, err := c.findAgentPods(ctx, cs)
	if err != nil {
		return nil, fmt.Errorf("finding cilium agent pods: %w", err)
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no cilium agent pods found")
	}

	// Exec into pods concurrently with bounded parallelism
	results := make([]agentNodeResult, len(pods))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(agentMaxConcurrent)

	for i, pod := range pods {
		g.Go(func() error {
			results[i] = c.execPod(gCtx, cs, pod)
			return nil // errors captured per-node
		})
	}
	_ = g.Wait() // always nil — per-node errors in results

	// Build result
	partial := false
	for _, r := range results {
		if r.err != "" {
			partial = true
			break
		}
	}

	result := &agentCollectionResult{
		nodes:     results,
		collected: time.Now(),
		partial:   partial,
	}

	// Cache the result
	c.cacheMu.Lock()
	c.cache = result
	c.cacheMu.Unlock()

	return result, nil
}

// findAgentPods searches ciliumSearchNamespaces for pods matching agentPodLabels.
func (c *CiliumAgentCollector) findAgentPods(ctx context.Context, cs kubernetes.Interface) ([]corev1.Pod, error) {
	for _, ns := range ciliumSearchNamespaces {
		for _, labelSelector := range agentPodLabels {
			pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				continue
			}
			if len(pods.Items) > 0 {
				// Filter to running pods in expected namespaces
				var valid []corev1.Pod
				for _, p := range pods.Items {
					if p.Status.Phase != corev1.PodRunning {
						continue
					}
					if !isAllowedNamespace(p.Namespace) {
						c.logger.Warn("cilium agent pod in unexpected namespace, skipping",
							"pod", p.Name, "namespace", p.Namespace)
						continue
					}
					valid = append(valid, p)
				}
				if len(valid) > 0 {
					return valid, nil
				}
			}
		}
	}
	return nil, nil
}

// isAllowedNamespace checks the pod is in an expected namespace.
func isAllowedNamespace(ns string) bool {
	for _, allowed := range ciliumSearchNamespaces {
		if ns == allowed {
			return true
		}
	}
	return false
}

// execPod runs cilium-dbg status in a single pod and returns the parsed result.
func (c *CiliumAgentCollector) execPod(ctx context.Context, cs kubernetes.Interface, pod corev1.Pod) agentNodeResult {
	nodeName := pod.Spec.NodeName
	result := agentNodeResult{
		nodeName: nodeName,
		podName:  pod.Name,
	}

	start := time.Now()
	stdout, stderr, err := c.execInPod(ctx, pod.Namespace, pod.Name, agentContainer, agentCommand)
	elapsed := time.Since(start)

	outcome := "success"
	if err != nil {
		outcome = "exec_error"
		result.err = fmt.Sprintf("exec failed: %v (stderr: %s)", err, truncate(string(stderr), 200))
		c.logger.Warn("agent exec failed",
			"pod", pod.Name, "node", nodeName, "duration", elapsed, "outcome", outcome, "error", err)
	} else {
		var status ciliumAgentStatus
		if parseErr := json.Unmarshal(stdout, &status); parseErr != nil {
			outcome = "parse_error"
			result.err = fmt.Sprintf("JSON parse failed: %v (output size: %d bytes)", parseErr, len(stdout))
			c.logger.Warn("agent exec parse failed",
				"pod", pod.Name, "node", nodeName, "duration", elapsed, "outcome", outcome,
				"outputSize", len(stdout), "error", parseErr)
		} else {
			result.status = &status
			c.logger.Info("agent exec",
				"pod", pod.Name, "node", nodeName, "duration", elapsed, "outcome", outcome)
		}
	}

	// Audit log
	auditResult := audit.ResultSuccess
	detail := fmt.Sprintf("cilium-dbg status on %s/%s (node: %s, duration: %s, outcome: %s)",
		pod.Namespace, pod.Name, nodeName, elapsed.Round(time.Millisecond), outcome)
	if result.err != "" {
		auditResult = audit.ResultFailure
		detail += ": " + result.err
	}
	_ = c.auditLogger.Log(ctx, audit.Entry{
		Timestamp:         time.Now().UTC(),
		User:              "system:serviceaccount",
		Action:            audit.ActionAgentExec,
		ResourceKind:      "Pod",
		ResourceNamespace: pod.Namespace,
		ResourceName:      pod.Name,
		Result:            auditResult,
		Detail:            truncate(detail, 500),
	})

	return result
}

// execInPod runs a non-interactive command in a pod using the service account's credentials.
func (c *CiliumAgentCollector) execInPod(ctx context.Context, namespace, podName, container string, command []string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	defer cancel()

	cs := c.k8sClient.BaseClientset()
	cfg := c.k8sClient.BaseConfig()

	// Build exec request URL using the same pattern as pods.go:256-266
	execReq := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		Param("container", container).
		Param("stdout", "true").
		Param("stderr", "true")

	for _, cmd := range command {
		execReq = execReq.Param("command", cmd)
	}

	executor, err := remotecommand.NewSPDYExecutor(cfg, "POST", execReq.URL())
	if err != nil {
		return nil, nil, fmt.Errorf("creating SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	lw := &limitedWriter{w: &stdout, remaining: agentMaxOutput}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: lw,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	if lw.exceeded {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("output exceeded %d byte limit", agentMaxOutput)
	}

	return stdout.Bytes(), stderr.Bytes(), nil
}

// limitedWriter wraps an io.Writer and stops accepting data after a limit.
type limitedWriter struct {
	w         io.Writer
	remaining int
	exceeded  bool
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		lw.exceeded = true
		return len(p), nil // discard but don't error to let stream finish
	}
	if len(p) > lw.remaining {
		lw.exceeded = true
		n, err := lw.w.Write(p[:lw.remaining])
		lw.remaining = 0
		return n + (len(p) - n), err // report full length to avoid short write errors
	}
	n, err := lw.w.Write(p)
	lw.remaining -= n
	return n, err
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
