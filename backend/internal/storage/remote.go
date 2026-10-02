package storage

// Remote-cluster reads of the CSI driver and StorageClass lists (R-8, #533).
// A remote cluster has no informers, so these are listed live as the
// requesting identity and held briefly in a per-(cluster, identity) cache
// (KTD1, KTD2). The local cluster keeps reading its informers in handler.go.

import (
	"context"
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

// remoteDrivers is a remote cluster's CSI drivers as one identity sees them,
// with the drivers that have a VolumeSnapshotClass.
type remoteDrivers struct {
	drivers         []*storagev1.CSIDriver
	snapshotDrivers map[string]bool
}

// remoteCaches returns the remote driver and class caches, creating them on
// first use. They are separate so a failure to read one list never fails a
// request that needs only the other.
func (h *Handler) remoteCaches() (*remotecache.Cache[*remoteDrivers], *remotecache.Cache[[]*storagev1.StorageClass]) {
	h.remoteOnce.Do(func() {
		h.remoteDriverCache = remotecache.New[*remoteDrivers](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
		h.remoteClassCache = remotecache.New[[]*storagev1.StorageClass](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, h.Logger)
	})
	return h.remoteDriverCache, h.remoteClassCache
}

// EvictRemoteCache drops every identity's cached storage lists for
// clusterID. Registered as a ClusterRouter evict hook.
func (h *Handler) EvictRemoteCache(clusterID string) {
	drivers, classes := h.remoteCaches()
	drivers.EvictCluster(clusterID)
	classes.EvictCluster(clusterID)
}

// loadRemoteClasses returns the request cluster's StorageClasses as the user.
// A failure to resolve the cluster is a k8s.TargetError.
func (h *Handler) loadRemoteClasses(ctx context.Context, user *auth.User) ([]*storagev1.StorageClass, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	_, cache := h.remoteCaches()
	return cache.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) ([]*storagev1.StorageClass, error) {
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

// loadRemoteDrivers returns the request cluster's CSI drivers as the user,
// with the drivers its VolumeSnapshotClasses name. A failure to resolve the
// cluster is a k8s.TargetError.
func (h *Handler) loadRemoteDrivers(ctx context.Context, user *auth.User) (*remoteDrivers, error) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	cache, _ := h.remoteCaches()
	return cache.Get(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, func(ctx context.Context) (*remoteDrivers, error) {
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
		result := &remoteDrivers{
			drivers:         make([]*storagev1.CSIDriver, len(list.Items)),
			snapshotDrivers: h.remoteSnapshotDrivers(listCtx, clusterID, user),
		}
		for i := range list.Items {
			result.drivers[i] = &list.Items[i]
		}
		return result, nil
	})
}

// remoteSnapshotDrivers returns the drivers named by a remote cluster's
// VolumeSnapshotClasses. Like the local getSnapshotDrivers it is best
// effort: when the snapshot CRDs are absent, or their presence or the class
// list cannot be read, drivers report no snapshot support rather than
// failing the driver list.
func (h *Handler) remoteSnapshotDrivers(ctx context.Context, clusterID string, user *auth.User) map[string]bool {
	result := make(map[string]bool)
	verdict := h.Presence.Check(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, volumeSnapshotClassGVR.GroupResource())
	if verdict.Installed == nil || !*verdict.Installed {
		return result
	}
	dyn, err := h.Clients.DynamicClientForCluster(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Warn("remote snapshot classes: client unavailable", "cluster", clusterID, "error", err)
		return result
	}
	list, err := dyn.Resource(volumeSnapshotClassGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		if k8s.IsResourceGone(err) {
			// The CRD went away after discovery was cached: re-read it so
			// the next fetch stops asking for it.
			h.Presence.Recheck(ctx, clusterID, user.KubernetesUsername, user.KubernetesGroups, volumeSnapshotClassGVR.GroupResource())
		}
		h.Logger.Warn("remote snapshot classes: list failed", "cluster", clusterID, "error", err)
		return result
	}
	for _, item := range list.Items {
		if driver, ok := item.Object["driver"].(string); ok {
			result[driver] = true
		}
	}
	return result
}
