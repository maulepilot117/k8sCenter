package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

func csiDriver(name string) *storagev1.CSIDriver {
	return &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func storageClass(name, provisioner string, expandable bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name},
		Provisioner:          provisioner,
		AllowVolumeExpansion: &expandable,
	}
}

// seedStorage adds CSI drivers and StorageClasses to a fake cluster's typed
// client, then clears the recorded actions so a test counts only its own.
func seedStorage(t *testing.T, c *fakeCluster, objs ...runtime.Object) {
	t.Helper()
	for _, o := range objs {
		if err := c.kube.Tracker().Add(o); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	c.kube.ClearActions()
}

// newStorageHarness is newHarness with storage objects on both clusters. The
// local cluster's objects exist only in its fake clients; Informers stays
// nil, so a remote request that reaches a local informer panics.
func newStorageHarness(t *testing.T, remoteSnapshotsInstalled bool, remoteObjs ...*unstructured.Unstructured) *harness {
	t.Helper()
	hs := newHarness(t, remoteSnapshotsInstalled, remoteObjs...)
	seedStorage(t, hs.clients.clusters[remoteCluster],
		csiDriver("remote.csi"), storageClass("remote-sc", "remote.csi", true))
	seedStorage(t, hs.clients.clusters[localCluster],
		csiDriver("local.csi"), storageClass("local-sc", "local.csi", true))
	return hs
}

func countKubeVerb(c *kfake.Clientset, verb, resource string) int {
	n := 0
	for _, a := range c.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == resource {
			n++
		}
	}
	return n
}

func TestRemote_ListDriversReturnsTheRemoteDriversOnly(t *testing.T) {
	hs := newStorageHarness(t, true, snapshotClass("remote-class", "remote.csi"))

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListDrivers, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeList[DriverInfo](t, rr)
	if len(got.Data) != 1 || got.Data[0].Name != "remote.csi" || got.Metadata.Total != 1 {
		t.Fatalf("drivers = %+v, want only remote.csi", got)
	}
	// Every capability is derived from the remote's own classes and
	// snapshot classes, never the local cluster's.
	if caps := got.Data[0].Capabilities; !caps.VolumeExpansion || !caps.Snapshot || !caps.Clone {
		t.Errorf("capabilities = %+v, want expansion and snapshot from the remote", caps)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ListClassesReturnsTheRemoteClassesOnly(t *testing.T) {
	hs := newStorageHarness(t, true)

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeList[ClassInfo](t, rr)
	if len(got.Data) != 1 || got.Data[0].Name != "remote-sc" || got.Data[0].Provisioner != "remote.csi" || got.Metadata.Total != 1 {
		t.Errorf("classes = %+v, want only remote-sc", got)
	}
	if n := countKubeVerb(hs.clients.clusters[remoteCluster].kube, "list", "csidrivers"); n != 0 {
		t.Errorf("listing classes listed CSI drivers %d times, want 0", n)
	}
	if n := hs.localActions(); n != 0 {
		t.Errorf("local cluster recorded %d actions, want 0", n)
	}
}

func TestRemote_ListDriversWithoutSnapshotCRDsReportsNoSnapshotSupport(t *testing.T) {
	hs := newStorageHarness(t, false)

	rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListDrivers, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := decodeList[DriverInfo](t, rr)
	if len(got.Data) != 1 || got.Data[0].Capabilities.Snapshot {
		t.Errorf("drivers = %+v, want remote.csi without snapshot support", got.Data)
	}
	if n := countVerb(hs.dyn(remoteCluster), "list", "volumesnapshotclasses"); n != 0 {
		t.Errorf("listed snapshot classes %d times on a remote without the CRDs, want 0", n)
	}
}

func TestRemote_StorageListsReturnTheClusterError(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		hs := newStorageHarness(t, true)
		hs.clients.targetErr = unreachable()

		for name, h := range map[string]http.HandlerFunc{"drivers": hs.h.HandleListDrivers, "classes": hs.h.HandleListClasses} {
			rr := do(t, remoteCluster, http.MethodGet, h, nil, "")
			body := rr.Body.String()
			if rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) {
				t.Errorf("%s: status %d body %s, want 502 unreachable", name, rr.Code, body)
			}
			if strings.Contains(body, remoteHost) {
				t.Errorf("%s: body leaks the remote address: %s", name, body)
			}
		}
		if n := hs.localActions(); n != 0 {
			t.Errorf("local cluster recorded %d actions, want 0", n)
		}
	})

	t.Run("list fails after the client resolved", func(t *testing.T) {
		hs := newStorageHarness(t, true)
		remote := hs.clients.clusters[remoteCluster].kube
		remote.PrependReactor("list", "storageclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(storagev1.Resource("storageclasses"), "", errors.New("denied at "+remoteHost))
		})
		remote.PrependReactor("list", "csidrivers", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, unreachable()
		})

		rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, "")
		if body := rr.Body.String(); rr.Code != http.StatusForbidden || !strings.Contains(body, `"reason":"forbidden"`) || strings.Contains(body, remoteHost) {
			t.Errorf("classes: status %d body %s, want 403 forbidden without the address", rr.Code, body)
		}
		rr = do(t, remoteCluster, http.MethodGet, hs.h.HandleListDrivers, nil, "")
		if body := rr.Body.String(); rr.Code != http.StatusBadGateway || !strings.Contains(body, `"reason":"unreachable"`) || strings.Contains(body, remoteHost) {
			t.Errorf("drivers: status %d body %s, want 502 unreachable without the address", rr.Code, body)
		}
	})

	t.Run("drivers fail when the classes they are built from cannot be read", func(t *testing.T) {
		hs := newStorageHarness(t, true)
		hs.clients.clusters[remoteCluster].kube.PrependReactor("list", "storageclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, unreachable()
		})
		rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListDrivers, nil, "")
		if rr.Code != http.StatusBadGateway {
			t.Errorf("drivers: status %d body %s, want 502 rather than drivers with guessed capabilities", rr.Code, rr.Body.String())
		}
	})
}

func TestRemote_StorageListsAreCachedPerClusterAndIdentity(t *testing.T) {
	hs := newStorageHarness(t, true)
	remote := hs.clients.clusters[remoteCluster].kube

	for range 2 {
		if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, ""); rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
	}
	if n := countKubeVerb(remote, "list", "storageclasses"); n != 1 {
		t.Errorf("two reads by one identity listed StorageClasses %d times, want 1", n)
	}

	// Another identity never shares the first one's view.
	other := &auth.User{Username: "ops", KubernetesUsername: "ops", KubernetesGroups: []string{"ops"}, Roles: []string{"admin"}}
	if rr := doAs(t, other, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, ""); rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countKubeVerb(remote, "list", "storageclasses"); n != 2 {
		t.Errorf("a second identity listed StorageClasses %d times in total, want 2", n)
	}

	// Evicting the cluster (a removal or re-registration) drops every view.
	hs.h.EvictRemoteCache(remoteCluster)
	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, ""); rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if n := countKubeVerb(remote, "list", "storageclasses"); n != 3 {
		t.Errorf("after eviction StorageClasses were listed %d times in total, want 3", n)
	}
}

func TestRemote_StorageListErrorsAreNotCached(t *testing.T) {
	hs := newStorageHarness(t, true)
	remote := hs.clients.clusters[remoteCluster].kube
	fail := true
	remote.PrependReactor("list", "storageclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, unreachable()
		}
		return false, nil, nil
	})

	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, ""); rr.Code != http.StatusBadGateway {
		t.Fatalf("first read: status %d, want 502", rr.Code)
	}
	fail = false
	if rr := do(t, remoteCluster, http.MethodGet, hs.h.HandleListClasses, nil, ""); rr.Code != http.StatusOK {
		t.Errorf("read after recovery: status %d body %s, want 200", rr.Code, rr.Body.String())
	}
}

// TestLocal_StorageListsReadTheLocalInformers pins the local path: the lists
// come from the informer cache, with no call through ClusterClients.
func TestLocal_StorageListsReadTheLocalInformers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cs := kfake.NewSimpleClientset(csiDriver("local.csi"), storageClass("local-sc", "local.csi", true))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	im := k8s.NewInformerManager(cs, nil, logger)
	im.Start(ctx)
	if err := im.WaitForSync(ctx); err != nil {
		t.Fatalf("informer sync: %v", err)
	}

	hs := newStorageHarness(t, true)
	// nolint:cluster-routing local path: test fixture giving the local-path test its informers.
	hs.h.Informers = im
	// Any ClusterClients call on the local path would fail the request.
	hs.clients.targetErr = errors.New("ClusterClients must not be used for local storage lists")
	// The local snapshot answer comes from the local discovery check; mark
	// it fresh and negative so no service-account client is needed.
	hs.h.snapshotAvail, hs.h.snapshotCheckedAt = false, time.Now()

	rr := do(t, localCluster, http.MethodGet, hs.h.HandleListDrivers, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("drivers status %d: %s", rr.Code, rr.Body.String())
	}
	drivers := decodeList[DriverInfo](t, rr)
	if len(drivers.Data) != 1 || drivers.Data[0].Name != "local.csi" || !drivers.Data[0].Capabilities.VolumeExpansion || drivers.Data[0].Capabilities.Snapshot {
		t.Errorf("drivers = %+v, want local.csi with expansion and no snapshot support", drivers.Data)
	}

	rr = do(t, localCluster, http.MethodGet, hs.h.HandleListClasses, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("classes status %d: %s", rr.Code, rr.Body.String())
	}
	classes := decodeList[ClassInfo](t, rr)
	if len(classes.Data) != 1 || classes.Data[0].Name != "local-sc" {
		t.Errorf("classes = %+v, want local-sc", classes.Data)
	}
	if n := len(hs.clients.clusters[remoteCluster].kube.Actions()); n != 0 {
		t.Errorf("remote cluster recorded %d actions on a local request, want 0", n)
	}
}
