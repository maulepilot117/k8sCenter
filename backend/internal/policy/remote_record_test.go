package policy

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/k8s"
)

const (
	recordUser  = "policy-recorder"
	recordGroup = "compliance-readers"
)

var recordGroups = []string{recordGroup}

// identityClients records the identity of every client and schema
// resolution, so a test can prove each read impersonated it.
type identityClients struct {
	inner k8s.ClusterClients
	mu    sync.Mutex
	seen  []string // "kind cluster username groups"
}

func (c *identityClients) note(kind, id, username string, groups []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, kind+" "+id+" "+username+" "+strings.Join(groups, ","))
}

func (c *identityClients) ClientForCluster(ctx context.Context, id, username string, groups []string) (kubernetes.Interface, error) {
	c.note("typed", id, username, groups)
	return c.inner.ClientForCluster(ctx, id, username, groups)
}

func (c *identityClients) DynamicClientForCluster(ctx context.Context, id, username string, groups []string) (dynamic.Interface, error) {
	c.note("dynamic", id, username, groups)
	return c.inner.DynamicClientForCluster(ctx, id, username, groups)
}

func (c *identityClients) TargetSchemaFor(ctx context.Context, id, username string, groups []string) (*k8s.TargetSchema, error) {
	c.note("schema", id, username, groups)
	return c.inner.TargetSchemaFor(ctx, id, username, groups)
}

// readForRecord runs the recorder read of remote as recordUser.
func readForRecord(t *testing.T, remote *fakeCluster) (*remoteRecord, *remoteFixture) {
	t.Helper()
	f := newRemoteFixture(t, remote)
	rec, err := f.h.readRemoteForRecord(context.Background(), remoteCluster, recordUser, recordGroups)
	if err != nil {
		t.Fatalf("readRemoteForRecord: %v", err)
	}
	f.assertNoLocal(t)
	return rec, f
}

func forbidList(c *fakeCluster, gvr schema.GroupVersionResource) {
	c.dyn.PrependReactor("list", gvr.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(gvr.GroupResource(), "", errors.New("RBAC: cannot list"))
	})
}

func failList(c *fakeCluster, gvr schema.GroupVersionResource) {
	c.dyn.PrependReactor("list", gvr.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, unreachableErr
	})
}

func assertPoint(t *testing.T, rec *remoteRecord, want recordOutcome, unreadable []Engine) {
	t.Helper()
	if rec.Outcome != want || rec.GapReason != "" {
		t.Fatalf("outcome = %q gap %q, want %q point", rec.Outcome, rec.GapReason, want)
	}
	if !reflect.DeepEqual(rec.Unreadable, unreadable) {
		t.Errorf("unreadable = %v, want %v", rec.Unreadable, unreadable)
	}
	if want := computeCompliance(rec.Policies, rec.Violations, ""); !reflect.DeepEqual(rec.Score, want) {
		t.Errorf("score = %+v, want %+v from the readable data", rec.Score, want)
	}
}

func assertGap(t *testing.T, rec *remoteRecord, reason string) {
	t.Helper()
	if rec.Outcome != recordGap || rec.GapReason != reason {
		t.Fatalf("outcome = %q gap %q, want gap %q", rec.Outcome, rec.GapReason, reason)
	}
	if len(rec.Policies)+len(rec.Violations) != 0 || rec.Score.Total != 0 {
		t.Errorf("gap carries data: %d policies, %d violations, score %+v", len(rec.Policies), len(rec.Violations), rec.Score)
	}
}

// recordPolicyNames is the sorted names of ps.
func recordPolicyNames(ps []NormalizedPolicy) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

func engines(es ...Engine) []Engine { return es }

// Covers AE3: Kyverno reads, Gatekeeper is refused. The point is partial,
// names Gatekeeper, and is scored from the Kyverno data alone.
func TestRecordRead_GatekeeperForbiddenIsPartial(t *testing.T) {
	remote := remoteBothEngines()
	forbidList(remote, requiredLabelsGVR)
	rec, _ := readForRecord(t, remote)

	assertPoint(t, rec, recordPartial, engines(EngineGatekeeper))
	if !reflect.DeepEqual(rec.Installed, engines(EngineKyverno, EngineGatekeeper)) {
		t.Errorf("installed = %v, want kyverno and gatekeeper", rec.Installed)
	}
	if got := recordPolicyNames(rec.Policies); !reflect.DeepEqual(got, []string{"remote-require-limits"}) {
		t.Errorf("policies = %v, want only the Kyverno policy", got)
	}
	for _, v := range rec.Violations {
		if v.Engine != EngineKyverno {
			t.Errorf("violation from unreadable engine %q kept: %+v", v.Engine, v)
		}
	}
	if len(rec.Violations) != 1 || rec.Score.Total != 1 {
		t.Errorf("violations = %d, score total = %d, want 1 and 1", len(rec.Violations), rec.Score.Total)
	}
	if len(rec.Coverage) != 1 || rec.Coverage[0].Source != "k8srequiredlabels" || rec.Coverage[0].Status != k8s.CoverageStatusForbidden {
		t.Errorf("coverage = %+v, want k8srequiredlabels forbidden", rec.Coverage)
	}
}

func TestRecordRead_BothEnginesReadableIsFull(t *testing.T) {
	rec, _ := readForRecord(t, remoteBothEngines())

	assertPoint(t, rec, recordFull, nil)
	if got := recordPolicyNames(rec.Policies); !reflect.DeepEqual(got, []string{"remote-owner-label", "remote-require-limits"}) {
		t.Errorf("policies = %v, want the Kyverno policy and the constraint", got)
	}
	if len(rec.Violations) != 2 || len(rec.Coverage) != 0 {
		t.Errorf("violations = %d, coverage = %+v, want 2 and none", len(rec.Violations), rec.Coverage)
	}
}

func TestRecordRead_EveryListForbiddenIsForbiddenGap(t *testing.T) {
	remote := remoteBothEngines()
	for gvr := range listKinds {
		forbidList(remote, gvr)
	}
	rec, _ := readForRecord(t, remote)

	assertGap(t, rec, gapForbidden)
	if !reflect.DeepEqual(rec.Unreadable, engines(EngineKyverno, EngineGatekeeper)) {
		t.Errorf("unreadable = %v, want both engines", rec.Unreadable)
	}
}

// No engine readable, and one failure is not a refusal: unreachable.
func TestRecordRead_NoEngineReadableWithFailureIsUnreachableGap(t *testing.T) {
	remote := remoteBothEngines()
	forbidList(remote, KyvernoClusterPolicyGVR)
	failList(remote, requiredLabelsGVR)
	rec, _ := readForRecord(t, remote)

	assertGap(t, rec, gapUnreachable)
}

func TestRecordRead_NoEngineIsNoPolicyEngineGap(t *testing.T) {
	rec, f := readForRecord(t, newFakeCluster(nil, nil))

	assertGap(t, rec, gapNoPolicyEngine)
	if len(rec.Installed) != 0 {
		t.Errorf("installed = %v, want none", rec.Installed)
	}
	if n := remoteLists(f.clients.clusters[remoteCluster]); n != 0 {
		t.Errorf("remote got %d list calls, want 0", n)
	}
}

// An unreadable discovery is unknown, never "no engine".
func TestRecordRead_DiscoveryUnreadableIsDiscoveryUnavailableGap(t *testing.T) {
	for _, tt := range []struct {
		name     string
		override func(*fakeCluster)
	}{
		{"discovery fails", func(c *fakeCluster) { c.discOverride = failingDiscovery{c.disc} }},
		{"engine group fails to load", func(c *fakeCluster) {
			c.discOverride = partialDiscovery{FakeDiscovery: c.disc, failed: []string{"kyverno.io"}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			remote := remoteBothEngines()
			tt.override(remote)
			rec, _ := readForRecord(t, remote)
			assertGap(t, rec, gapDiscoveryUnavailable)
			if n := remoteLists(remote); n != 0 {
				t.Errorf("remote got %d list calls, want 0", n)
			}
		})
	}
}

// An engine is readable only when every list it needs succeeded.
func TestRecordRead_OneKyvernoListFailingMakesKyvernoUnreadable(t *testing.T) {
	for _, tt := range []struct {
		name string
		fail func(*fakeCluster)
	}{
		{"report list refused", func(c *fakeCluster) { forbidList(c, PolicyReportGVR) }},
		{"policy list unreachable", func(c *fakeCluster) { failList(c, KyvernoClusterPolicyGVR) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			remote := remoteBothEngines()
			tt.fail(remote)
			rec, _ := readForRecord(t, remote)

			assertPoint(t, rec, recordPartial, engines(EngineKyverno))
			if got := recordPolicyNames(rec.Policies); !reflect.DeepEqual(got, []string{"remote-owner-label"}) {
				t.Errorf("policies = %v, want only the Gatekeeper constraint", got)
			}
			for _, v := range rec.Violations {
				if v.Engine != EngineGatekeeper {
					t.Errorf("violation from unreadable Kyverno kept: %+v", v)
				}
			}
		})
	}
}

// A list whose resource went away is not a failure.
func TestRecordRead_GoneResourceIsReadable(t *testing.T) {
	remote := remoteBothEngines()
	remote.dyn.PrependReactor("list", "k8srequiredlabels", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(requiredLabelsGVR.GroupResource(), "")
	})
	rec, _ := readForRecord(t, remote)
	assertPoint(t, rec, recordFull, nil)
}

func TestRecordRead_TargetErrorIsReturned(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())
	f.clients.targetErr = unreachableErr
	_, err := f.h.readRemoteForRecord(context.Background(), remoteCluster, recordUser, recordGroups)
	var target k8s.TargetError
	if !errors.As(err, &target) {
		t.Fatalf("err = %v, want a k8s.TargetError for the recorder to classify", err)
	}
}

// Every client and schema the read resolves impersonates the given
// identity, on the given cluster.
func TestRecordRead_ImpersonatesTheGivenIdentity(t *testing.T) {
	f := newRemoteFixture(t, remoteBothEngines())
	ids := &identityClients{inner: f.clients}
	f.h.Clients = ids
	f.h.Presence = k8s.NewPresence(ids)

	rec, err := f.h.readRemoteForRecord(context.Background(), remoteCluster, recordUser, recordGroups)
	if err != nil || rec.Outcome != recordFull {
		t.Fatalf("read = %+v, %v; want a full point", rec, err)
	}
	kinds := map[string]bool{}
	want := " " + remoteCluster + " " + recordUser + " " + recordGroup
	for _, s := range ids.seen {
		kind, rest, _ := strings.Cut(s, " ")
		kinds[kind] = true
		if " "+rest != want {
			t.Errorf("resolution %q, want identity %q", s, strings.TrimSpace(want))
		}
	}
	for _, k := range []string{"schema", "dynamic"} {
		if !kinds[k] {
			t.Errorf("no %s resolution recorded; seen %v", k, ids.seen)
		}
	}
	f.assertNoLocal(t)
}
