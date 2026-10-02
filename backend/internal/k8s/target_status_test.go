package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	k8stesting "k8s.io/client-go/testing"
)

const remoteID = "remote-1"

var argoApps = schema.GroupResource{Group: "argoproj.io", Resource: "applications"}

// fakeSchemaClients is a ClusterClients whose TargetSchemaFor serves a
// swappable discovery client and counts Invalidate calls.
type fakeSchemaClients struct {
	disc        atomic.Pointer[discovery.DiscoveryInterface]
	targetErr   error
	invalidates atomic.Int32
	calls       atomic.Int32
}

func (f *fakeSchemaClients) setDiscovery(d discovery.DiscoveryInterface) { f.disc.Store(&d) }

func (f *fakeSchemaClients) ClientForCluster(context.Context, string, string, []string) (kubernetes.Interface, error) {
	return nil, errors.New("not used")
}

func (f *fakeSchemaClients) DynamicClientForCluster(context.Context, string, string, []string) (dynamic.Interface, error) {
	return nil, errors.New("not used")
}

func (f *fakeSchemaClients) TargetSchemaFor(_ context.Context, clusterID, _ string, _ []string) (*TargetSchema, error) {
	f.calls.Add(1)
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	return &TargetSchema{
		ClusterID:  clusterID,
		Discovery:  *f.disc.Load(),
		Invalidate: func() { f.invalidates.Add(1) },
	}, nil
}

// discoveryWith serves the given group/version resource lists.
func discoveryWith(lists ...*metav1.APIResourceList) *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: lists}}
}

func argoList() *metav1.APIResourceList {
	return &metav1.APIResourceList{
		GroupVersion: "argoproj.io/v1alpha1",
		APIResources: []metav1.APIResource{{Name: "applications", Kind: "Application"}},
	}
}

func coreList() *metav1.APIResourceList {
	return &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod"}},
	}
}

// failingGroupDiscovery returns the lists it has plus a per-group failure
// for failedGV, the shape client-go uses for a partially loaded discovery.
type failingGroupDiscovery struct {
	*fakediscovery.FakeDiscovery
	failedGV schema.GroupVersion
}

func (d failingGroupDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	groups, lists, _ := d.FakeDiscovery.ServerGroupsAndResources()
	return groups, lists, &discovery.ErrGroupDiscoveryFailed{
		Groups: map[schema.GroupVersion]error{d.failedGV: errors.New("503")},
	}
}

// brokenDiscovery fails outright with no lists.
type brokenDiscovery struct{ *fakediscovery.FakeDiscovery }

func (brokenDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, nil, errors.New("connection reset")
}

func newTestPresence(disc discovery.DiscoveryInterface) (*Presence, *fakeSchemaClients, *time.Time) {
	f := &fakeSchemaClients{}
	f.setDiscovery(disc)
	p := NewPresence(f)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	return p, f, &now
}

func check(t *testing.T, p *Presence, gr schema.GroupResource) PresenceVerdict {
	t.Helper()
	return p.Check(context.Background(), remoteID, "alice", []string{"team-a"}, gr)
}

func wantVerdict(t *testing.T, got PresenceVerdict, installed *bool, reason ReasonCode) {
	t.Helper()
	switch {
	case installed == nil && got.Installed != nil:
		t.Errorf("Installed = %v, want unknown (nil)", *got.Installed)
	case installed != nil && (got.Installed == nil || *got.Installed != *installed):
		t.Errorf("Installed = %v, want %v", got.Installed, *installed)
	}
	if got.Reason != reason {
		t.Errorf("Reason = %q, want %q", got.Reason, reason)
	}
}

var (
	yes = func() *bool { b := true; return &b }()
	no  = func() *bool { b := false; return &b }()
)

func TestPresence_InstalledOnRemote(t *testing.T) {
	p, _, _ := newTestPresence(discoveryWith(coreList(), argoList()))
	wantVerdict(t, check(t, p, argoApps), yes, ReasonOK)
}

func TestPresence_AbsentFromLoadedListIsNotInstalled(t *testing.T) {
	p, _, _ := newTestPresence(discoveryWith(coreList()))
	wantVerdict(t, check(t, p, argoApps), no, ReasonDiscoveryMissing)
}

// The remote discovery client is a memcache client, whose per-group-version
// lookup returns a plain ErrCacheNotFound for an unserved group. Reading the
// full group list keeps that from being misreported as "unknown".
func TestPresence_MemcacheWrappedAbsenceIsNotInstalled(t *testing.T) {
	p, _, _ := newTestPresence(memory.NewMemCacheClient(discoveryWith(coreList())))
	wantVerdict(t, check(t, p, argoApps), no, ReasonDiscoveryMissing)
}

func TestPresence_FailedGroupIsUnknown(t *testing.T) {
	disc := failingGroupDiscovery{
		FakeDiscovery: discoveryWith(coreList()),
		failedGV:      schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"},
	}
	p, _, _ := newTestPresence(disc)
	wantVerdict(t, check(t, p, argoApps), nil, ReasonDiscoveryUnavailable)
}

func TestPresence_OtherGroupFailingDoesNotHideAbsence(t *testing.T) {
	disc := failingGroupDiscovery{
		FakeDiscovery: discoveryWith(coreList()),
		failedGV:      schema.GroupVersion{Group: "metrics.k8s.io", Version: "v1beta1"},
	}
	p, _, _ := newTestPresence(disc)
	wantVerdict(t, check(t, p, argoApps), no, ReasonDiscoveryMissing)
}

func TestPresence_NoDiscoveryAtAllIsUnknown(t *testing.T) {
	p, _, _ := newTestPresence(brokenDiscovery{discoveryWith()})
	wantVerdict(t, check(t, p, argoApps), nil, ReasonDiscoveryUnavailable)
}

func TestPresence_TargetResolutionFailureIsUnknownWithItsReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ReasonCode
	}{
		{"unknown cluster", fmt.Errorf("lookup: %w", pgx.ErrNoRows), ReasonClusterUnknown},
		{"database down", &pgconn.PgError{Code: "57P01", Message: "terminating connection"}, ReasonDBUnavailable},
		{"unreachable", &net.DNSError{Err: "no such host", Name: "api.example.test"}, ReasonUnreachable},
		{"credentials", errors.New("decrypt credentials: cipher: message authentication failed"), ReasonCredentialsInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, f, _ := newTestPresence(discoveryWith(argoList()))
			f.targetErr = tt.err
			wantVerdict(t, check(t, p, argoApps), nil, tt.want)
		})
	}
}

// A CRD installed on the remote after a "not installed" answer is seen once
// the verdict window passes: the re-check invalidates the cached schema so
// discovery is read again.
func TestPresence_NewlyInstalledCRDSeenAfterTheWindow(t *testing.T) {
	p, f, now := newTestPresence(discoveryWith(coreList()))
	wantVerdict(t, check(t, p, argoApps), no, ReasonDiscoveryMissing)

	f.setDiscovery(discoveryWith(coreList(), argoList()))
	*now = now.Add(presenceAbsentTTL - time.Second)
	wantVerdict(t, check(t, p, argoApps), no, ReasonDiscoveryMissing)
	if n := f.invalidates.Load(); n != 0 {
		t.Errorf("schema invalidated %d times inside the window, want 0", n)
	}

	*now = now.Add(2 * time.Second)
	wantVerdict(t, check(t, p, argoApps), yes, ReasonOK)
	if n := f.invalidates.Load(); n != 1 {
		t.Errorf("schema invalidated %d times after the window, want 1", n)
	}
}

// Installed, then removed: a list that fails with NoMatch/NotFound calls
// Recheck, which re-reads discovery once.
func TestPresence_RecheckSeesRemovedCRD(t *testing.T) {
	p, f, _ := newTestPresence(discoveryWith(coreList(), argoList()))
	wantVerdict(t, check(t, p, argoApps), yes, ReasonOK)

	f.setDiscovery(discoveryWith(coreList()))
	got := p.Recheck(context.Background(), remoteID, "alice", []string{"team-a"}, argoApps)
	wantVerdict(t, got, no, ReasonDiscoveryMissing)
	if n := f.invalidates.Load(); n != 1 {
		t.Errorf("schema invalidated %d times, want 1", n)
	}
}

// Another key's read must not erase an expired absence before that key's own
// re-check, or the re-check skips the schema invalidation and a newly
// installed CRD stays hidden until the 5-minute schema cache expires.
func TestPresence_OtherKeysReadDoesNotEraseTheInvalidationTrigger(t *testing.T) {
	p, f, now := newTestPresence(discoveryWith(coreList()))
	check(t, p, argoApps) // absent, remembered

	f.setDiscovery(discoveryWith(coreList(), argoList()))
	*now = now.Add(presenceAbsentTTL + time.Second)
	pods := schema.GroupResource{Resource: "pods"}
	wantVerdict(t, check(t, p, pods), yes, ReasonOK) // an installed key reads first

	wantVerdict(t, check(t, p, argoApps), yes, ReasonOK)
	if n := f.invalidates.Load(); n != 1 {
		t.Errorf("schema invalidated %d times on the expired key's re-check, want 1", n)
	}
}

// singleflight re-panics a panicking body on a fresh goroutine; the probe
// must turn that into an unknown verdict rather than crash or hang.
func TestPresence_PanickingDiscoveryIsUnknown(t *testing.T) {
	p, _, _ := newTestPresence(panickingDiscovery{discoveryWith()})
	done := make(chan PresenceVerdict, 1)
	go func() { done <- check(t, p, argoApps) }()
	select {
	case v := <-done:
		wantVerdict(t, v, nil, ReasonDiscoveryUnavailable)
	case <-time.After(2 * time.Second):
		t.Fatal("a panicking probe hung its caller")
	}
	if len(p.absent) != 0 {
		t.Error("a panicking probe must not record an absence")
	}
}

type panickingDiscovery struct{ *fakediscovery.FakeDiscovery }

func (panickingDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	panic("malformed discovery document")
}

func TestPresence_CallerCancellationReturnsUnknownPromptly(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	p, _, _ := newTestPresence(slowDiscovery{FakeDiscovery: discoveryWith(coreList()), gate: gate})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan PresenceVerdict, 1)
	go func() { done <- p.Check(ctx, remoteID, "alice", []string{"team-a"}, argoApps) }()
	cancel()
	select {
	case v := <-done:
		wantVerdict(t, v, nil, ReasonUnreachable)
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled caller kept waiting on the probe")
	}
}

func TestPresence_ConcurrentReprobesInvalidateOnce(t *testing.T) {
	gate := make(chan struct{})
	slow := slowDiscovery{FakeDiscovery: discoveryWith(coreList()), gate: gate}
	p, f, now := newTestPresence(discoveryWith(coreList()))
	check(t, p, argoApps) // remembered absent
	f.setDiscovery(slow)
	*now = now.Add(presenceAbsentTTL + time.Second)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Check(context.Background(), remoteID, "alice", []string{"team-a"}, argoApps)
		}()
	}
	time.Sleep(50 * time.Millisecond) // let every caller join the flight
	close(gate)
	wg.Wait()

	if n := f.invalidates.Load(); n != 1 {
		t.Errorf("schema invalidated %d times by 8 concurrent re-probes, want 1", n)
	}
}

// slowDiscovery blocks its list read until gate closes.
type slowDiscovery struct {
	*fakediscovery.FakeDiscovery
	gate chan struct{}
}

func (d slowDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	<-d.gate
	return d.FakeDiscovery.ServerGroupsAndResources()
}

func TestPresence_AbsentVerdictIsPerIdentity(t *testing.T) {
	p, f, _ := newTestPresence(discoveryWith(coreList()))
	check(t, p, argoApps)
	before := f.calls.Load()

	p.Check(context.Background(), remoteID, "bob", []string{"team-b"}, argoApps)
	if f.calls.Load() == before {
		t.Error("another identity was served alice's cached verdict")
	}
}

func TestPresence_EvictClusterDropsCachedVerdicts(t *testing.T) {
	p, f, _ := newTestPresence(discoveryWith(coreList()))
	check(t, p, argoApps)

	f.setDiscovery(discoveryWith(coreList(), argoList()))
	p.EvictCluster(remoteID)
	wantVerdict(t, check(t, p, argoApps), yes, ReasonOK)
}

func TestIsResourceGone(t *testing.T) {
	gone := []error{
		&meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "argoproj.io", Kind: "Application"}},
		fmt.Errorf("list: %w", &meta.NoResourceMatchError{PartialResource: argoApps.WithVersion("v1alpha1")}),
		apierrors.NewNotFound(argoApps, ""),
	}
	for _, err := range gone {
		if !IsResourceGone(err) {
			t.Errorf("IsResourceGone(%v) = false, want true", err)
		}
	}
	notGone := []error{
		apierrors.NewForbidden(argoApps, "", errors.New("denied")),
		apierrors.NewNotFound(argoApps, "my-app"), // one named object, not the collection
		errors.New("connection reset"),
	}
	for _, err := range notGone {
		if IsResourceGone(err) {
			t.Errorf("IsResourceGone(%v) = true, want false", err)
		}
	}
}

// ServesGVR answers for one exact version: a resource served only at another
// version, or under another group, is not served.
func TestServesGVR(t *testing.T) {
	lists := []*metav1.APIResourceList{
		{GroupVersion: "example.io/v1beta1", APIResources: []metav1.APIResource{{Name: "widgets"}}},
		{GroupVersion: "other.io/v1", APIResources: []metav1.APIResource{{Name: "gadgets"}}},
	}
	cases := []struct {
		gvr  schema.GroupVersionResource
		want bool
	}{
		{schema.GroupVersionResource{Group: "example.io", Version: "v1beta1", Resource: "widgets"}, true},
		{schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}, false},
		{schema.GroupVersionResource{Group: "example.io", Version: "v1beta1", Resource: "gadgets"}, false},
		{schema.GroupVersionResource{Group: "other.io", Version: "v1", Resource: "gadgets"}, true},
	}
	for _, c := range cases {
		if got := ServesGVR(lists, c.gvr); got != c.want {
			t.Errorf("ServesGVR(%v) = %v, want %v", c.gvr, got, c.want)
		}
	}
}
