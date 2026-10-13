package networking

// Remote Cilium agent exec (#608 slice 5, KTD7). On a remote cluster the
// agent pods are found and exec'd as the requesting user, through the strict
// stream transport the k8s package builds, never with k8sCenter's service
// account. Results are cached per (cluster, identity), so one user's exec
// output is never served to another, and each collection writes one audit
// row rather than one per pod.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
)

// PodExecer runs a fixed command in a pod of one remote cluster, as one user.
// *k8s.StreamTransport implements it.
type PodExecer interface {
	Exec(ctx context.Context, namespace, pod string, cmd k8s.ExecCommand, stdout, stderr io.Writer) error
}

// RemoteExec opens a PodExecer for a remote cluster and identity.
type RemoteExec interface {
	ExecerFor(ctx context.Context, clusterID, username string, groups []string) (PodExecer, error)
}

type routerExec struct{ router *k8s.ClusterRouter }

func (e routerExec) ExecerFor(ctx context.Context, clusterID, username string, groups []string) (PodExecer, error) {
	st, err := e.router.StreamTransportFor(ctx, clusterID, username, groups)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// StreamsFrom returns the RemoteExec backed by router, or a true nil
// interface when router is nil (docs/solutions/go-typed-nil-interface.md).
func StreamsFrom(router *k8s.ClusterRouter) RemoteExec {
	if router == nil {
		return nil
	}
	return routerExec{router: router}
}

// Per-node exec failure states, reported apart from node health.
const (
	execErrForbidden = "forbidden"
	execErrTimeout   = "timeout"
	execErrPodGone   = "pod_gone"
	execErrFailed    = "failed"
)

// errNoAgentPods means no running Cilium agent pod was found.
var errNoAgentPods = errors.New("no cilium agent pods found")

// remoteAgentCollector execs cilium-dbg status into a remote cluster's
// agent pods as the requesting user.
type remoteAgentCollector struct {
	clients       k8s.ClusterClients
	exec          RemoteExec
	auditLogger   audit.Logger
	logger        *slog.Logger
	maxConcurrent int
	execTimeout   time.Duration
	cache         *remotecache.Cache[*agentCollectionResult]
}

func newRemoteAgentCollector(clients k8s.ClusterClients, exec RemoteExec, auditLogger audit.Logger, logger *slog.Logger, maxConcurrent int) *remoteAgentCollector {
	if maxConcurrent <= 0 {
		maxConcurrent = agentDefaultMaxConcurrent
	}
	return &remoteAgentCollector{
		clients:       clients,
		exec:          exec,
		auditLogger:   auditLogger,
		logger:        logger,
		maxConcurrent: maxConcurrent,
		execTimeout:   agentExecTimeout,
		cache:         remotecache.New[*agentCollectionResult](agentCacheTTL, remotecache.DefaultMaxEntries, logger),
	}
}

// EvictCluster drops every identity's cached collection for clusterID.
func (c *remoteAgentCollector) EvictCluster(clusterID string) {
	c.cache.EvictCluster(clusterID)
}

// Collect returns the agent status of every running Cilium agent pod in
// namespace on clusterID, as user. A refused pod list, or a refused exec
// into the first pod, fails the whole collection as forbidden instead of
// reporting a failure per node.
func (c *remoteAgentCollector) Collect(ctx context.Context, clusterID string, user *auth.User, namespace string) (*agentCollectionResult, error) {
	return c.cache.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*agentCollectionResult, error) {
		return c.collect(ctx, clusterID, user, namespace)
	})
}

func (c *remoteAgentCollector) collect(ctx context.Context, clusterID string, user *auth.User, namespace string) (*agentCollectionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, agentOuterTimeout)
	defer cancel()

	cs, err := c.clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	pods, truncated, err := findRemoteAgentPods(ctx, cs, namespace)
	if err != nil {
		c.audit(ctx, clusterID, user, namespace, audit.ResultFailure, "agent pods could not be listed")
		return nil, fmt.Errorf("list cilium agent pods: %w", err)
	}
	if len(pods) == 0 {
		return nil, errNoAgentPods
	}
	execer, err := c.exec.ExecerFor(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}

	results := make([]agentNodeResult, len(pods))
	// The first exec runs alone: a user without pods/exec is refused here
	// once, not once per node.
	var firstErr error
	results[0], firstErr = c.execPod(ctx, execer, pods[0])
	if results[0].execError == execErrForbidden {
		c.audit(ctx, clusterID, user, namespace, audit.ResultFailure, fmt.Sprintf("exec refused; %d agent pods not collected", len(pods)))
		return nil, firstErr
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(c.maxConcurrent)
	for i := 1; i < len(pods); i++ {
		// Pre-seeded so a panicking exec reads as a failed node.
		results[i] = agentNodeResult{nodeName: pods[i].Spec.NodeName, podName: pods[i].Name, err: "agent exec failed", execError: execErrFailed}
		recoverutil.Go(g, c.logger, "networking remote agent-exec", func() error {
			results[i], _ = c.execPod(gCtx, execer, pods[i])
			return nil // errors are recorded per node
		})
	}
	_ = g.Wait()

	failures := 0
	for _, r := range results {
		if r.err != "" {
			failures++
		}
	}
	outcome := audit.ResultSuccess
	if failures > 0 {
		outcome = audit.ResultFailure
	}
	c.audit(ctx, clusterID, user, namespace, outcome, fmt.Sprintf("cilium-dbg status on %d agent pods, %d failed", len(pods), failures))
	// A pod list cut off at the remote cap leaves nodes unchecked.
	return &agentCollectionResult{nodes: results, collected: time.Now(), partial: failures > 0 || truncated}, nil
}

// findRemoteAgentPods lists the running Cilium agent pods in namespace,
// across every list page up to the remote cap. truncated reports that the
// cap cut the list off.
func findRemoteAgentPods(ctx context.Context, cs kubernetes.Interface, namespace string) (pods []corev1.Pod, truncated bool, err error) {
	for _, selector := range agentPodLabelSelectors() {
		items, truncated, err := k8s.PageList(ctx, metav1.ListOptions{LabelSelector: selector}, func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Pod, string, error) {
			list, err := cs.CoreV1().Pods(namespace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.Continue, nil
		})
		if err != nil {
			return nil, false, err
		}
		var running []corev1.Pod
		for _, p := range items {
			if p.Status.Phase == corev1.PodRunning && p.Namespace == namespace {
				running = append(running, p)
			}
		}
		if len(running) > 0 {
			return running, truncated, nil
		}
	}
	return nil, false, nil
}

// execPod runs cilium-dbg status in one remote pod. The node result carries
// a fixed message and an exec-error state; the exec error and stderr, which
// can hold the remote cluster's own text, are only logged.
func (c *remoteAgentCollector) execPod(ctx context.Context, execer PodExecer, pod corev1.Pod) (agentNodeResult, error) {
	result := agentNodeResult{nodeName: pod.Spec.NodeName, podName: pod.Name}
	ctx, cancel := context.WithTimeout(ctx, c.execTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	stdoutW := &limitedWriter{w: &stdout, remaining: agentMaxOutput}
	stderrW := &limitedWriter{w: &stderr, remaining: agentMaxStderr}
	err := execer.Exec(ctx, pod.Namespace, pod.Name, k8s.ExecCiliumStatusJSON, stdoutW, stderrW)
	if err == nil && stdoutW.exceeded {
		err = fmt.Errorf("output exceeded %d byte limit", agentMaxOutput)
	}
	if err != nil {
		result.execError = classifyExecErr(ctx, err)
		result.err = "agent exec failed: " + result.execError
		c.logger.Warn("remote agent exec failed", "pod", pod.Name, "node", result.nodeName,
			"state", result.execError, "error", err, "stderr", truncate(stderr.String(), 200))
		return result, err
	}
	var status ciliumAgentStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		result.execError = execErrFailed
		result.err = "agent status could not be parsed"
		c.logger.Warn("remote agent exec parse failed", "pod", pod.Name, "outputSize", stdout.Len(), "error", err)
		return result, err
	}
	result.status = &status
	return result, nil
}

// classifyExecErr maps an exec failure to its exec-error state.
func classifyExecErr(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return execErrTimeout
	case apierrors.IsForbidden(err):
		return execErrForbidden
	case apierrors.IsNotFound(err):
		return execErrPodGone
	default:
		return execErrFailed
	}
}

// audit records one remote agent collection.
func (c *remoteAgentCollector) audit(ctx context.Context, clusterID string, user *auth.User, namespace string, result audit.Result, detail string) {
	if c.auditLogger == nil {
		return
	}
	if err := c.auditLogger.Log(ctx, audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         k8s.NormalizedClusterID(clusterID),
		User:              user.Username,
		Action:            audit.ActionAgentExec,
		ResourceKind:      "Pod",
		ResourceNamespace: namespace,
		ResourceName:      "cilium-agents",
		Result:            result,
		Detail:            detail,
	}); err != nil {
		c.logger.Warn("failed to write audit log for remote agent exec", "cluster", clusterID, "error", err)
	}
}
