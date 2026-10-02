package storage

// Remote-cluster reads of the CSI driver and StorageClass lists (R-8, #533).
// A remote cluster has no informers, so these are listed live as the
// requesting identity and held briefly in a per-(cluster, identity) cache
// (KTD1, KTD2). The local cluster keeps reading its informers in handler.go.

import (
	"context"
	"errors"
	"time"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// remoteListTimeout bounds one remote fetch of a storage list.
const remoteListTimeout = 10 * time.Second

// errSnapshotsUnknown means whether a remote cluster serves
// VolumeSnapshotClasses could not be told.
var errSnapshotsUnknown = errors.New("snapshot class presence unknown")

// remoteCaches holds the remote storage lists per (cluster, identity). Each
// list has its own cache, so a failure to read one never fails a request
// that needs only another, and errors are never cached.
type remoteCaches struct {
	drivers         *remotecache.Cache[[]*storagev1.CSIDriver]
	classes         *remotecache.Cache[[]*storagev1.StorageClass]
	snapshotDrivers *remotecache.Cache[map[string]bool]
}

// caches returns the remote caches, creating them on first use.
func (h *Handler) caches() *remoteCaches {
	h.remoteOnce.Do(func() {
		h.remote = &remoteCaches{
			drivers:         remotecache.New[[]*storagev1.CSIDriver](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger),
			classes:         remotecache.New[[]*storagev1.StorageClass](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger),
			snapshotDrivers: remotecache.New[map[string]bool](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger),
		}
	})
	return h.remote
}

// EvictRemoteCache drops every identity's cached storage lists for
// clusterID. Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	c := h.caches()
	c.drivers.EvictCluster(clusterID)
	c.classes.EvictCluster(clusterID)
	c.snapshotDrivers.EvictCluster(clusterID)
}

// loadRemoteClasses returns the request cluster's StorageClasses as the user.
// A failure to resolve the cluster is a k8s.TargetError.
func (h *Handler) loadRemoteClasses(ctx context.Context, user *auth.User) ([]*storagev1.StorageClass, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.caches().classes.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) ([]*storagev1.StorageClass, error) {
		client, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			return nil, k8s.TargetError{Err: err}
		}
		listCtx, cancel := context.WithTimeout(ctx, remoteListTimeout)
		defer cancel()
		list, err := client.StorageV1().StorageClasses().List(listCtx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		classes := make([]*storagev1.StorageClass, len(list.Items))
		for i := range list.Items {
			classes[i] = &list.Items[i]
		}
		return classes, nil
	})
}

// loadRemoteDrivers returns the request cluster's CSI drivers as the user.
// A failure to resolve the cluster is a k8s.TargetError.
func (h *Handler) loadRemoteDrivers(ctx context.Context, user *auth.User) ([]*storagev1.CSIDriver, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	return h.caches().drivers.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) ([]*storagev1.CSIDriver, error) {
		client, err := h.Clients.ClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
		if err != nil {
			return nil, k8s.TargetError{Err: err}
		}
		listCtx, cancel := context.WithTimeout(ctx, remoteListTimeout)
		defer cancel()
		list, err := client.StorageV1().CSIDrivers().List(listCtx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		drivers := make([]*storagev1.CSIDriver, len(list.Items))
		for i := range list.Items {
			drivers[i] = &list.Items[i]
		}
		return drivers, nil
	})
}

// remoteSnapshotDrivers returns the drivers named by the request cluster's
// VolumeSnapshotClasses, as the user. Like the local getSnapshotDrivers it is
// best effort: when the lookup fails the drivers report no snapshot support
// rather than failing the driver list. Only a confirmed answer is cached; a
// failed lookup is retried on the next request instead of hiding snapshot
// support for the cache TTL.
func (h *Handler) remoteSnapshotDrivers(ctx context.Context, user *auth.User) map[string]bool {
	clusterID := middleware.ClusterIDFromContext(ctx)
	drivers, err := h.caches().snapshotDrivers.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (map[string]bool, error) {
		return h.fetchRemoteSnapshotDrivers(ctx, clusterID, user)
	})
	if err != nil {
		h.Logger.Warn("remote snapshot classes unavailable; drivers report no snapshot support", "cluster", clusterID, "error", err)
		return map[string]bool{}
	}
	return drivers
}

// fetchRemoteSnapshotDrivers reads a remote cluster's VolumeSnapshotClasses.
// A cluster confirmed not to serve them answers an empty set; any lookup
// that could not tell is an error, so it is not cached.
func (h *Handler) fetchRemoteSnapshotDrivers(ctx context.Context, clusterID string, user *auth.User) (map[string]bool, error) {
	result := make(map[string]bool)
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, volumeSnapshotClassGVR.GroupResource())
	if verdict.Installed == nil {
		return nil, errSnapshotsUnknown
	}
	if !*verdict.Installed {
		return result, nil
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		return nil, k8s.TargetError{Err: err}
	}
	listCtx, cancel := context.WithTimeout(ctx, remoteListTimeout)
	defer cancel()
	list, err := dyn.Resource(volumeSnapshotClassGVR).List(listCtx, metav1.ListOptions{})
	if err != nil {
		if k8s.IsResourceGone(err) {
			// The CRD went away after discovery was cached: re-read it, and
			// answer "none" once the re-read confirms the removal.
			recheck := h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, volumeSnapshotClassGVR.GroupResource())
			if recheck.Installed != nil && !*recheck.Installed {
				return result, nil
			}
		}
		return nil, err
	}
	for _, item := range list.Items {
		if driver, ok := item.Object["driver"].(string); ok {
			result[driver] = true
		}
	}
	return result, nil
}
