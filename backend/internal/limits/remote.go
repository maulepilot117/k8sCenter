package limits

// Remote-cluster reads of ResourceQuotas and LimitRanges. A remote
// cluster has no informers, so its quotas and LimitRanges are listed live as
// the requesting identity. The dashboard summaries are held briefly in a
// per-(cluster, identity) cache; the namespace detail is read fresh. Nothing
// here ever falls back to the local cluster.

import (
	"context"
	"errors"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"golang.org/x/sync/errgroup"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

const (
	// remoteListTimeout bounds one remote fetch, all of its pages included.
	// A cluster-wide list still continuing after k8s.RemoteListMaxPages pages
	// is served as read.
	remoteListTimeout = 10 * time.Second

	resourceQuotasNoun = "resource quotas"
	limitRangesNoun    = "limit ranges"
)

func isLocal(ctx context.Context) bool {
	return k8s.IsLocalClusterID(middleware.ClusterIDFromContext(ctx))
}

// remoteListError records which list failed, so a refusal can name it.
type remoteListError struct {
	noun string
	err  error
}

func (e remoteListError) Error() string { return "list " + e.noun + ": " + e.err.Error() }
func (e remoteListError) Unwrap() error { return e.err }

// remoteSummaryCache returns the remote summary cache, creating it on first
// use.
func (h *Handler) remoteSummaryCache() *remotecache.Cache[[]NamespaceSummary] {
	h.remoteOnce.Do(func() {
		h.remote = remotecache.New[[]NamespaceSummary](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return h.remote
}

// EvictRemoteCache drops every identity's cached limits summaries for
// clusterID. Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	h.remoteSummaryCache().EvictCluster(clusterID)
}

// writeRemoteNotConfigured answers a remote selection when no ClusterClients
// were wired. It only happens on a wiring mistake.
func writeRemoteNotConfigured(w http.ResponseWriter) {
	httputil.WriteError(w, http.StatusNotImplemented, "remote limits are not available", "")
}

// remoteSummaries returns the request cluster's namespace summaries as the
// user, writing the error response and returning false on failure. The
// cached slice is shared: callers must not modify it in place.
func (h *Handler) remoteSummaries(w http.ResponseWriter, r *http.Request, user *auth.User) ([]NamespaceSummary, bool) {
	if h.Clients == nil {
		writeRemoteNotConfigured(w)
		return nil, false
	}
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)
	summaries, err := h.remoteSummaryCache().Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) ([]NamespaceSummary, error) {
		return h.fetchRemoteSummaries(ctx, clusterID, user)
	})
	if err != nil {
		h.writeRemoteFailure(w, clusterID, "", err)
		return nil, false
	}
	return summaries, true
}

// fetchRemoteSummaries lists every ResourceQuota and LimitRange on
// clusterID as the user and summarizes them. A failure to resolve the
// cluster is a k8s.TargetError; a list failure is a remoteListError.
func (h *Handler) fetchRemoteSummaries(ctx context.Context, clusterID string, user *auth.User) ([]NamespaceSummary, error) {
	client, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteListTimeout)
	defer cancel()

	// Both lists run at once under the shared budget; the first failure
	// cancels the other and is the one reported.
	var quotas []*corev1.ResourceQuota
	var limitRanges []*corev1.LimitRange
	g, gctx := errgroup.WithContext(ctx)
	recoverutil.Go(g, h.Logger, "limits remote list resourcequotas", func() error {
		items, truncated, err := k8s.PageList(gctx, metav1.ListOptions{}, func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.ResourceQuota, string, error) {
			list, err := client.CoreV1().ResourceQuotas(metav1.NamespaceAll).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointers(list.Items), list.Continue, nil
		})
		if err != nil {
			return remoteListError{noun: resourceQuotasNoun, err: err}
		}
		if truncated {
			h.Logger.Warn("remote resource quota list truncated; serving what was read", "cluster", clusterID, "items", len(items))
		}
		quotas = items
		return nil
	})
	recoverutil.Go(g, h.Logger, "limits remote list limitranges", func() error {
		items, truncated, err := k8s.PageList(gctx, metav1.ListOptions{}, func(ctx context.Context, opts metav1.ListOptions) ([]*corev1.LimitRange, string, error) {
			list, err := client.CoreV1().LimitRanges(metav1.NamespaceAll).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return pointers(list.Items), list.Continue, nil
		})
		if err != nil {
			return remoteListError{noun: limitRangesNoun, err: err}
		}
		if truncated {
			h.Logger.Warn("remote limit range list truncated; serving what was read", "cluster", clusterID, "items", len(items))
		}
		limitRanges = items
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return h.summarize(quotas, limitRanges), nil
}

// remoteNamespaceDetail reads one namespace's quotas and LimitRanges on the
// request cluster as the user, writing the error response and returning
// false on failure. The caller has already run the RBAC gate.
func (h *Handler) remoteNamespaceDetail(w http.ResponseWriter, r *http.Request, user *auth.User, namespace string) (*NamespaceLimits, bool) {
	ctx := r.Context()
	clusterID := middleware.ClusterIDFromContext(ctx)
	detail, err := h.fetchRemoteNamespaceDetail(ctx, clusterID, user, namespace)
	if err != nil {
		h.writeRemoteFailure(w, clusterID, namespace, err)
		return nil, false
	}
	return detail, true
}

func (h *Handler) fetchRemoteNamespaceDetail(ctx context.Context, clusterID string, user *auth.User, namespace string) (*NamespaceLimits, error) {
	client, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteListTimeout)
	defer cancel()

	// One namespace holds a handful of each, so one page is enough; a longer
	// list is served as read.
	// Both run at once; the first failure cancels the other and is reported.
	// The gate admits a user who may read either type, so the cluster
	// refusing one list is that section empty, not a failure; only both
	// refused is a refusal.
	opts := metav1.ListOptions{Limit: k8s.RemoteListPageSize}
	quotas := &corev1.ResourceQuotaList{}
	limitRanges := &corev1.LimitRangeList{}
	var quotasRefused, limitRangesRefused error
	g, gctx := errgroup.WithContext(ctx)
	recoverutil.Go(g, h.Logger, "limits remote namespace resourcequotas", func() error {
		list, err := client.CoreV1().ResourceQuotas(namespace).List(gctx, opts)
		switch {
		case apierrors.IsForbidden(err):
			quotasRefused = remoteListError{noun: resourceQuotasNoun, err: err}
		case err != nil:
			return remoteListError{noun: resourceQuotasNoun, err: err}
		default:
			quotas = list
		}
		return nil
	})
	recoverutil.Go(g, h.Logger, "limits remote namespace limitranges", func() error {
		list, err := client.CoreV1().LimitRanges(namespace).List(gctx, opts)
		switch {
		case apierrors.IsForbidden(err):
			limitRangesRefused = remoteListError{noun: limitRangesNoun, err: err}
		case err != nil:
			return remoteListError{noun: limitRangesNoun, err: err}
		default:
			limitRanges = list
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if quotasRefused != nil && limitRangesRefused != nil {
		return nil, quotasRefused
	}
	if quotas.Continue != "" || limitRanges.Continue != "" {
		h.Logger.Warn("remote namespace limits truncated; serving what was read", "cluster", clusterID, "namespace", namespace)
	}
	return h.buildDetail(namespace, pointers(quotas.Items), pointers(limitRanges.Items)), nil
}

// writeRemoteFailure answers a failed remote read with a fixed message; the
// raw error, which can carry the remote API server's address or its own
// message, is only logged. namespace is set on the detail route, where a
// NotFound means the namespace is gone.
func (h *Handler) writeRemoteFailure(w http.ResponseWriter, clusterID, namespace string, err error) {
	h.Logger.Warn("remote limits read failed", "cluster", clusterID, "namespace", namespace, "error", err)
	var target k8s.TargetError
	switch {
	case errors.As(err, &target):
		httputil.WriteTargetError(w, target.Err)
	case apierrors.IsForbidden(err):
		noun := resourceQuotasNoun
		var listErr remoteListError
		if errors.As(err, &listErr) {
			noun = listErr.noun
		}
		httputil.WriteErrorWithReason(w, http.StatusForbidden,
			"you do not have permission to list "+noun+" on the selected cluster", string(k8s.ReasonForbidden), nil)
	case namespace != "" && apierrors.IsNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, "namespace not found on the selected cluster", "")
	default:
		httputil.WriteRemoteError(w, err)
	}
}

// pointers returns a pointer to each element of items.
func pointers[T any](items []T) []*T {
	out := make([]*T, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}
