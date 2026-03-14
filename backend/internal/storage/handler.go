package storage

import (
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Handler serves storage-related HTTP endpoints.
type Handler struct {
	K8sClient       *k8s.ClientFactory
	Informers       *k8s.InformerManager
	Logger          *slog.Logger
	snapshotOnce    sync.Once
	snapshotAvail   bool
}

// volumeSnapshotGVR is the GVR for VolumeSnapshot CRDs.
var volumeSnapshotGVR = schema.GroupVersionResource{
	Group:    "snapshot.storage.k8s.io",
	Version:  "v1",
	Resource: "volumesnapshots",
}

// volumeSnapshotClassGVR is the GVR for VolumeSnapshotClass CRDs.
var volumeSnapshotClassGVR = schema.GroupVersionResource{
	Group:    "snapshot.storage.k8s.io",
	Version:  "v1",
	Resource: "volumesnapshotclasses",
}

// HandleListDrivers returns CSI drivers with enriched capability info.
// GET /api/v1/storage/drivers
func (h *Handler) HandleListDrivers(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	drivers, err := h.Informers.CSIDrivers().List(labels.Everything())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list CSI drivers", "")
		return
	}

	// Get StorageClasses to check driver capabilities
	classes, _ := h.Informers.StorageClasses().List(labels.Everything())

	// Check snapshot support
	hasSnapshots := h.checkSnapshotCRDs()
	snapshotDrivers := make(map[string]bool)
	if hasSnapshots {
		snapshotDrivers = h.getSnapshotDrivers(r)
	}

	result := make([]DriverInfo, 0, len(drivers))
	for _, d := range drivers {
		info := buildDriverInfo(d, classes, snapshotDrivers)
		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data":     result,
		"metadata": map[string]any{"total": len(result)},
	})
}

// HandleListClasses returns StorageClasses from the informer cache.
// GET /api/v1/storage/classes
func (h *Handler) HandleListClasses(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	classes, err := h.Informers.StorageClasses().List(labels.Everything())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list storage classes", "")
		return
	}

	result := make([]ClassInfo, 0, len(classes))
	for _, sc := range classes {
		result = append(result, buildClassInfo(sc))
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data":     result,
		"metadata": map[string]any{"total": len(result)},
	})
}

// HandleListSnapshots returns VolumeSnapshots via the dynamic client.
// GET /api/v1/storage/snapshots
// GET /api/v1/storage/snapshots/{namespace}
func (h *Handler) HandleListSnapshots(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !h.checkSnapshotCRDs() {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"data":     []any{},
			"metadata": map[string]any{"total": 0, "available": false},
		})
		return
	}

	ns := chi.URLParam(r, "namespace")

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create impersonated client", "")
		return
	}

	var res dynamic.ResourceInterface
	if ns != "" {
		res = dynClient.Resource(volumeSnapshotGVR).Namespace(ns)
	} else {
		res = dynClient.Resource(volumeSnapshotGVR)
	}

	list, err := res.List(r.Context(), metav1.ListOptions{})
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to list volume snapshots", "")
		return
	}

	snapshots := make([]SnapshotInfo, 0, len(list.Items))
	for _, item := range list.Items {
		snapshots = append(snapshots, buildSnapshotInfo(item.Object))
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data":     snapshots,
		"metadata": map[string]any{"total": len(snapshots), "available": true},
	})
}

// HandleListPresets returns driver-specific parameter presets.
// GET /api/v1/storage/presets
func (h *Handler) HandleListPresets(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}
	httputil.WriteData(w, DriverPresets)
}

// checkSnapshotCRDs checks if VolumeSnapshot CRDs are installed (thread-safe, checked once).
func (h *Handler) checkSnapshotCRDs() bool {
	h.snapshotOnce.Do(func() {
		disc := h.K8sClient.DiscoveryClient()
		if disc == nil {
			return
		}
		resources, err := disc.ServerResourcesForGroupVersion("snapshot.storage.k8s.io/v1")
		if err != nil {
			return
		}
		for _, r := range resources.APIResources {
			if r.Name == "volumesnapshots" {
				h.snapshotAvail = true
				return
			}
		}
	})
	return h.snapshotAvail
}

// getSnapshotDrivers returns a set of driver names that have VolumeSnapshotClasses.
func (h *Handler) getSnapshotDrivers(r *http.Request) map[string]bool {
	result := make(map[string]bool)
	dynClient := h.K8sClient.BaseDynamicClient()
	if dynClient == nil {
		return result
	}
	list, err := dynClient.Resource(volumeSnapshotClassGVR).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		return result
	}
	for _, item := range list.Items {
		if driver, ok := item.Object["driver"].(string); ok {
			result[driver] = true
		}
	}
	return result
}

// DriverInfo is the JSON response shape for a CSI driver.
type DriverInfo struct {
	Name               string           `json:"name"`
	AttachRequired     bool             `json:"attachRequired"`
	PodInfoOnMount     bool             `json:"podInfoOnMount"`
	VolumeLifecycle    []string         `json:"volumeLifecycleModes"`
	StorageCapacity    bool             `json:"storageCapacity"`
	FSGroupPolicy      string           `json:"fsGroupPolicy"`
	Capabilities       DriverCapability `json:"capabilities"`
}

// DriverCapability describes what a CSI driver supports.
type DriverCapability struct {
	VolumeExpansion bool `json:"volumeExpansion"`
	Snapshot        bool `json:"snapshot"`
	Clone           bool `json:"clone"`
}

// buildDriverInfo constructs a DriverInfo from a CSIDriver object.
func buildDriverInfo(d *storagev1.CSIDriver, classes []*storagev1.StorageClass, snapshotDrivers map[string]bool) DriverInfo {
	info := DriverInfo{
		Name: d.Name,
	}

	if d.Spec.AttachRequired != nil {
		info.AttachRequired = *d.Spec.AttachRequired
	}
	if d.Spec.PodInfoOnMount != nil {
		info.PodInfoOnMount = *d.Spec.PodInfoOnMount
	}
	if d.Spec.StorageCapacity != nil {
		info.StorageCapacity = *d.Spec.StorageCapacity
	}
	if d.Spec.FSGroupPolicy != nil {
		info.FSGroupPolicy = string(*d.Spec.FSGroupPolicy)
	}

	modes := make([]string, 0, len(d.Spec.VolumeLifecycleModes))
	for _, m := range d.Spec.VolumeLifecycleModes {
		modes = append(modes, string(m))
	}
	info.VolumeLifecycle = modes

	// Check capabilities from StorageClasses using this driver
	for _, sc := range classes {
		if sc.Provisioner == d.Name {
			if sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion {
				info.Capabilities.VolumeExpansion = true
			}
		}
	}

	info.Capabilities.Snapshot = snapshotDrivers[d.Name]
	// Clone support is hard to detect; set true if snapshot is supported (common correlation)
	info.Capabilities.Clone = info.Capabilities.Snapshot

	return info
}

// ClassInfo is the JSON response shape for a StorageClass.
type ClassInfo struct {
	Name                 string            `json:"name"`
	Provisioner          string            `json:"provisioner"`
	ReclaimPolicy        string            `json:"reclaimPolicy"`
	VolumeBindingMode    string            `json:"volumeBindingMode"`
	AllowVolumeExpansion bool              `json:"allowVolumeExpansion"`
	IsDefault            bool              `json:"isDefault"`
	Parameters           map[string]string `json:"parameters,omitempty"`
	CreatedAt            string            `json:"createdAt"`
}

// buildClassInfo constructs a ClassInfo from a StorageClass.
func buildClassInfo(sc *storagev1.StorageClass) ClassInfo {
	info := ClassInfo{
		Name:        sc.Name,
		Provisioner: sc.Provisioner,
		Parameters:  sc.Parameters,
		CreatedAt:   sc.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"),
	}

	if sc.ReclaimPolicy != nil {
		info.ReclaimPolicy = string(*sc.ReclaimPolicy)
	}
	if sc.VolumeBindingMode != nil {
		info.VolumeBindingMode = string(*sc.VolumeBindingMode)
	}
	if sc.AllowVolumeExpansion != nil {
		info.AllowVolumeExpansion = *sc.AllowVolumeExpansion
	}

	// Check default annotation
	if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		info.IsDefault = true
	}

	return info
}

// SnapshotInfo is the JSON response shape for a VolumeSnapshot.
type SnapshotInfo struct {
	Name                    string `json:"name"`
	Namespace               string `json:"namespace"`
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
	SourcePVC               string `json:"sourcePVC,omitempty"`
	ReadyToUse              bool   `json:"readyToUse"`
	RestoreSize             string `json:"restoreSize,omitempty"`
	CreatedAt               string `json:"createdAt"`
}

// buildSnapshotInfo constructs a SnapshotInfo from an unstructured VolumeSnapshot.
func buildSnapshotInfo(item map[string]any) SnapshotInfo {
	name, _, _ := unstructured.NestedString(item, "metadata", "name")
	namespace, _, _ := unstructured.NestedString(item, "metadata", "namespace")
	createdAt, _, _ := unstructured.NestedString(item, "metadata", "creationTimestamp")
	snapClass, _, _ := unstructured.NestedString(item, "spec", "volumeSnapshotClassName")
	sourcePVC, _, _ := unstructured.NestedString(item, "spec", "source", "persistentVolumeClaimName")
	readyToUse, _, _ := unstructured.NestedBool(item, "status", "readyToUse")
	restoreSize, _, _ := unstructured.NestedString(item, "status", "restoreSize")

	return SnapshotInfo{
		Name:                    name,
		Namespace:               namespace,
		CreatedAt:               createdAt,
		VolumeSnapshotClassName: snapClass,
		SourcePVC:               sourcePVC,
		ReadyToUse:              readyToUse,
		RestoreSize:             restoreSize,
	}
}
