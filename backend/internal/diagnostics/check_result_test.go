package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var checkNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// limitLister serves the countingLister fixtures plus the two behaviours the
// limitation tests need: a Deployment to resolve, and a pod listing that fails.
type limitLister struct {
	*countingLister
	deployments []*appsv1.Deployment
	podsErr     error
}

func (l *limitLister) ListPods(ctx context.Context, ns string) ([]*corev1.Pod, error) {
	if l.podsErr != nil {
		return nil, l.podsErr
	}
	return l.countingLister.ListPods(ctx, ns)
}

func (l *limitLister) ListDeployments(context.Context, string) ([]*appsv1.Deployment, error) {
	return l.deployments, nil
}

func checkByID(t *testing.T, checks []CheckResult, id string) CheckResult {
	t.Helper()
	for _, c := range checks {
		if c.CheckID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, checks)
	return CheckResult{}
}

func resultByRule(t *testing.T, results []Result, rule string) Result {
	t.Helper()
	for _, r := range results {
		if r.RuleName == rule {
			return r
		}
	}
	t.Fatalf("no result for rule %q in %+v", rule, results)
	return Result{}
}

// TestDenormalizeRoundTripsEveryRule is the compatibility proof: every
// consumer of the legacy wire sees exactly what RunDiagnostics produced, for
// every registered rule, every status, with and without links, and with or
// without a limitation that downgrades the normalized view.
func TestDenormalizeRoundTripsEveryRule(t *testing.T) {
	linkSets := map[string][]Link{
		"nil links":   nil,
		"empty links": {},
		"two links":   {{Label: "web-0", Kind: "Pod", Name: "web-0"}, {Label: "web-1", Kind: "Pod", Name: "web-1"}},
	}
	limitSets := map[string][]Limitation{
		"no limitation":  nil,
		"pods denied":    {{Kind: limitPods, Reason: ReasonPermissionDenied}},
		"pods and rs":    {{Kind: limitPods, Reason: ReasonPermissionDenied}, {Kind: limitReplicaSets, Reason: ReasonPermissionDenied}},
		"pods list fail": {{Kind: limitPods, Reason: ReasonSourceUnavailable}},
	}

	for _, rule := range rules {
		for _, status := range []string{"pass", "warn", "fail"} {
			for linkName, links := range linkSets {
				for limitName, limits := range limitSets {
					name := rule.name + "/" + status + "/" + linkName + "/" + limitName
					t.Run(name, func(t *testing.T) {
						in := []Result{{
							RuleName:    rule.name,
							Status:      status,
							Severity:    rule.severity,
							Message:     "message for " + rule.name,
							Detail:      "detail",
							Remediation: "remediation",
							Links:       links,
						}}
						target := &DiagnosticTarget{Kind: "Pod", Name: "web", Namespace: "team-a", Limitations: limits}
						got := Denormalize(Normalize("local", target, checkNow, in))
						if !reflect.DeepEqual(got, in) {
							t.Fatalf("round trip changed the legacy result:\n got  %#v\n want %#v", got, in)
						}
					})
				}
			}
		}
	}

	t.Run("nil results stay nil", func(t *testing.T) {
		if got := Denormalize(Normalize("local", &DiagnosticTarget{}, checkNow, nil)); got != nil {
			t.Fatalf("nil results round-tripped to %#v", got)
		}
	})
	t.Run("empty results stay empty", func(t *testing.T) {
		got := Denormalize(Normalize("local", &DiagnosticTarget{}, checkNow, []Result{}))
		if got == nil || len(got) != 0 {
			t.Fatalf("empty results round-tripped to %#v", got)
		}
	})
}

// TestNormalizeMarksPodDeniedChecksInconclusive drives the real entry points.
// A user who cannot list pods gets target.Pods == nil, so the legacy rules
// report "pass" for a crash-looping pod: a missing observation reads as
// healthy. The legacy wire keeps saying so (it is frozen); the normalized
// result says it could not tell.
func TestNormalizeMarksPodDeniedChecksInconclusive(t *testing.T) {
	ctx := context.Background()
	lister := &countingLister{pods: []*corev1.Pod{testPod(true)}}

	podRules := map[string]string{
		"CrashLoopBackOff": "diagnostics/crashloopbackoff",
		"ImagePullBackOff": "diagnostics/imagepullbackoff",
		"PendingPod":       "diagnostics/pendingpod",
	}

	t.Run("denied", func(t *testing.T) {
		target, err := Resolve(ctx, lister, "team-a", "Pod", "web", &RelatedRBAC{})
		if err != nil {
			t.Fatal(err)
		}
		results := RunDiagnostics(ctx, target)
		checks := Normalize("local", target, checkNow, results)

		for rule, id := range podRules {
			if got := resultByRule(t, results, rule).Status; got != "pass" {
				t.Fatalf("legacy %s status = %q, want the frozen false \"pass\"", rule, got)
			}
			c := checkByID(t, checks, id)
			if c.Status != CheckInconclusive || c.Reason != ReasonPermissionDenied {
				t.Errorf("%s = %s/%s, want inconclusive/permission_denied", id, c.Status, c.Reason)
			}
		}
	})

	t.Run("allowed", func(t *testing.T) {
		target, err := Resolve(ctx, lister, "team-a", "Pod", "web", &RelatedRBAC{Pods: true})
		if err != nil {
			t.Fatal(err)
		}
		checks := Normalize("local", target, checkNow, RunDiagnostics(ctx, target))

		crash := checkByID(t, checks, "diagnostics/crashloopbackoff")
		if crash.Status != CheckFail || crash.Reason != ReasonFinding {
			t.Errorf("crash-looping pod = %s/%q, want fail/finding", crash.Status, crash.Reason)
		}
		for _, id := range []string{"diagnostics/imagepullbackoff", "diagnostics/pendingpod"} {
			if c := checkByID(t, checks, id); c.Status != CheckPass {
				t.Errorf("%s = %s, want a conclusive pass when pods were observed", id, c.Status)
			}
		}
	})

	// A Deployment's pods are found through its ReplicaSets, so a user who can
	// list pods but not ReplicaSets gets no pods either. The workload rules must
	// read that as not observed, while ReplicaMismatch still reads the
	// Deployment itself.
	t.Run("replicasets denied", func(t *testing.T) {
		dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-dep"}}
		depLister := &limitLister{
			countingLister: &countingLister{pods: []*corev1.Pod{testPod(true)}},
			deployments:    []*appsv1.Deployment{dep},
		}
		target, err := Resolve(ctx, depLister, "team-a", "Deployment", "web", &RelatedRBAC{Pods: true})
		if err != nil {
			t.Fatal(err)
		}
		results := RunDiagnostics(ctx, target)
		checks := Normalize("local", target, checkNow, results)

		for rule, id := range podRules {
			if got := resultByRule(t, results, rule).Status; got != "pass" {
				t.Fatalf("legacy %s status = %q, want the frozen false \"pass\"", rule, got)
			}
			c := checkByID(t, checks, id)
			if c.Status != CheckInconclusive || c.Reason != ReasonPermissionDenied {
				t.Errorf("%s = %s/%s, want inconclusive/permission_denied", id, c.Status, c.Reason)
			}
		}
		// The Deployment has no ready replicas of the one it defaults to.
		if c := checkByID(t, checks, "diagnostics/replicamismatch"); c.Status != CheckFail || c.Reason != ReasonFinding {
			t.Errorf("ReplicaMismatch = %s/%q, want a conclusive fail/finding", c.Status, c.Reason)
		}
	})
}

// TestNormalizeMarksUnreadablePodsInconclusive covers the other way pods go
// missing: the lister fails, not RBAC. Nothing was observed either way.
func TestNormalizeMarksUnreadablePodsInconclusive(t *testing.T) {
	ctx := context.Background()
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-dep"}}
	lister := &limitLister{
		countingLister: &countingLister{},
		deployments:    []*appsv1.Deployment{dep},
		podsErr:        errors.New("informer cache not synced"),
	}

	target, err := Resolve(ctx, lister, "team-a", "Deployment", "web", &RelatedRBAC{Pods: true, ReplicaSets: true})
	if err != nil {
		t.Fatal(err)
	}
	checks := Normalize("local", target, checkNow, RunDiagnostics(ctx, target))

	c := checkByID(t, checks, "diagnostics/crashloopbackoff")
	if c.Status != CheckInconclusive || c.Reason != ReasonSourceUnavailable {
		t.Fatalf("crashloop with unreadable pods = %s/%s, want inconclusive/source_unavailable", c.Status, c.Reason)
	}
}

func TestResolveRecordsLimitations(t *testing.T) {
	ctx := context.Background()
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-dep"}}

	cases := []struct {
		name    string
		kind    string
		related *RelatedRBAC
		podsErr error
		want    []Limitation
	}{
		{name: "legacy nil gate records nothing", kind: "Pod", related: nil, want: nil},
		{name: "everything allowed records nothing", kind: "Deployment", related: &RelatedRBAC{Pods: true, ReplicaSets: true}, want: nil},
		{
			name: "pods denied", kind: "Pod", related: &RelatedRBAC{},
			want: []Limitation{{Kind: limitPods, Reason: ReasonPermissionDenied}},
		},
		{
			name: "replicasets denied on a deployment", kind: "Deployment", related: &RelatedRBAC{Pods: true},
			want: []Limitation{{Kind: limitReplicaSets, Reason: ReasonPermissionDenied}},
		},
		{
			name: "replicasets denied is irrelevant to a pod target", kind: "Pod", related: &RelatedRBAC{Pods: true},
			want: nil,
		},
		{
			name: "pods and replicasets denied records only the pods gate that stopped resolution", kind: "Deployment", related: &RelatedRBAC{},
			want: []Limitation{{Kind: limitPods, Reason: ReasonPermissionDenied}},
		},
		{
			name: "pod listing fails", kind: "Deployment", related: &RelatedRBAC{Pods: true, ReplicaSets: true},
			podsErr: errors.New("boom"),
			want:    []Limitation{{Kind: limitPods, Reason: ReasonSourceUnavailable}},
		},
		{
			name: "pod listing fails while replicasets are denied: only the failed listing is recorded", kind: "Deployment", related: &RelatedRBAC{Pods: true},
			podsErr: errors.New("boom"),
			want:    []Limitation{{Kind: limitPods, Reason: ReasonSourceUnavailable}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lister := &limitLister{
				countingLister: &countingLister{pods: []*corev1.Pod{testPod(false)}},
				deployments:    []*appsv1.Deployment{dep},
				podsErr:        c.podsErr,
			}
			target, err := Resolve(ctx, lister, "team-a", c.kind, "web", c.related)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(target.Limitations, c.want) {
				t.Fatalf("Limitations = %#v, want %#v", target.Limitations, c.want)
			}
		})
	}
}

// TestNormalizeDowngradesAbsenceFindingUnderLimitation: ZeroEndpoints reports
// "zero matching pods" from an empty pod list. Under pod denial that list is
// empty because nobody looked, so the finding itself is unsupported, not just
// a pass.
func TestNormalizeDowngradesAbsenceFindingUnderLimitation(t *testing.T) {
	ctx := context.Background()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-svc"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
	}
	target := &DiagnosticTarget{Kind: "Service", Name: "web", Namespace: "team-a", Object: svc}

	t.Run("pods observed empty", func(t *testing.T) {
		results := RunDiagnostics(ctx, target)
		if got := resultByRule(t, results, "ZeroEndpoints").Status; got != "warn" {
			t.Fatalf("legacy ZeroEndpoints = %q, want warn", got)
		}
		c := checkByID(t, Normalize("local", target, checkNow, results), "diagnostics/zeroendpoints")
		if c.Status != CheckWarn || c.Reason != ReasonFinding {
			t.Errorf("observed-empty ZeroEndpoints = %s/%q, want warn/finding", c.Status, c.Reason)
		}
	})

	t.Run("pods not observable", func(t *testing.T) {
		limited := *target
		limited.Limitations = []Limitation{{Kind: limitPods, Reason: ReasonPermissionDenied}}
		results := RunDiagnostics(ctx, &limited)
		if got := resultByRule(t, results, "ZeroEndpoints").Status; got != "warn" {
			t.Fatalf("legacy ZeroEndpoints = %q, want the frozen warn", got)
		}
		c := checkByID(t, Normalize("local", &limited, checkNow, results), "diagnostics/zeroendpoints")
		if c.Status != CheckInconclusive || c.Reason != ReasonPermissionDenied {
			t.Errorf("unobservable ZeroEndpoints = %s/%q, want inconclusive/permission_denied", c.Status, c.Reason)
		}
	})

	t.Run("a finding that cites observed evidence stands", func(t *testing.T) {
		limited := &DiagnosticTarget{Kind: "Deployment", Name: "web", Namespace: "team-a",
			Limitations: []Limitation{{Kind: limitReplicaSets, Reason: ReasonPermissionDenied}}}
		in := []Result{{
			RuleName: "CrashLoopBackOff", Status: "fail", Severity: SeverityCritical, Message: "1 pod(s) in CrashLoopBackOff",
			Links: []Link{{Label: "web-0", Kind: "Pod", Name: "web-0"}},
		}}
		c := Normalize("local", limited, checkNow, in)[0]
		if c.Status != CheckFail || c.Reason != ReasonFinding {
			t.Errorf("evidence-backed failure = %s/%q, want fail/finding", c.Status, c.Reason)
		}
	})
}

// TestNormalizeLeavesObjectDerivedChecksConclusiveUnderPodDenial: ReplicaMismatch
// and PendingPVC read the target object only, which the user was allowed to
// list. Pod denial says nothing about them.
func TestNormalizeLeavesObjectDerivedChecksConclusiveUnderPodDenial(t *testing.T) {
	ctx := context.Background()
	denied := []Limitation{{Kind: limitPods, Reason: ReasonPermissionDenied}}

	t.Run("replica mismatch", func(t *testing.T) {
		target := &DiagnosticTarget{
			Kind: "Deployment", Name: "web", Namespace: "team-a", Limitations: denied,
			Object: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-dep"},
				Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(3)},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
			},
		}
		checks := Normalize("local", target, checkNow, RunDiagnostics(ctx, target))
		if c := checkByID(t, checks, "diagnostics/replicamismatch"); c.Status != CheckWarn || c.Reason != ReasonFinding {
			t.Errorf("ReplicaMismatch = %s/%q, want a conclusive warn/finding", c.Status, c.Reason)
		}
	})

	t.Run("replicas healthy", func(t *testing.T) {
		target := &DiagnosticTarget{
			Kind: "Deployment", Name: "web", Namespace: "team-a", Limitations: denied,
			Object: &appsv1.Deployment{
				Spec:   appsv1.DeploymentSpec{Replicas: int32Ptr(2)},
				Status: appsv1.DeploymentStatus{ReadyReplicas: 2},
			},
		}
		checks := Normalize("local", target, checkNow, RunDiagnostics(ctx, target))
		if c := checkByID(t, checks, "diagnostics/replicamismatch"); c.Status != CheckPass {
			t.Errorf("healthy ReplicaMismatch = %s, want a conclusive pass", c.Status)
		}
	})

	t.Run("pending pvc", func(t *testing.T) {
		target := &DiagnosticTarget{
			Kind: "PersistentVolumeClaim", Name: "data", Namespace: "team-a", Limitations: denied,
			Object: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "team-a"},
				Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
			},
		}
		checks := Normalize("local", target, checkNow, RunDiagnostics(ctx, target))
		if c := checkByID(t, checks, "diagnostics/pendingpvc"); c.Status != CheckWarn || c.Reason != ReasonFinding {
			t.Errorf("PendingPVC = %s/%q, want a conclusive warn/finding", c.Status, c.Reason)
		}
	})
}

// TestNormalizeMarksTimedOutCheckInconclusive builds the timeout result with
// the real runSafeCheck, so renaming its message breaks this test instead of
// silently turning timeouts back into failures.
func TestNormalizeMarksTimedOutCheckInconclusive(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hang := ruleEntry{
		name:     "CrashLoopBackOff",
		severity: SeverityCritical,
		check: func(context.Context, *DiagnosticTarget) Result {
			<-release
			return Result{Status: "pass"}
		},
	}
	timedOut := runSafeCheck(ctx, hang, &DiagnosticTarget{Kind: "Pod", Name: "web", Namespace: "team-a"})
	if timedOut.Status != "fail" {
		t.Fatalf("legacy timeout status = %q, want the frozen fail", timedOut.Status)
	}

	in := []Result{timedOut}
	checks := Normalize("local", &DiagnosticTarget{Kind: "Pod", Name: "web", Namespace: "team-a"}, checkNow, in)
	if checks[0].Status != CheckInconclusive || checks[0].Reason != ReasonTimedOut {
		t.Fatalf("timed-out check = %s/%s, want inconclusive/timed_out", checks[0].Status, checks[0].Reason)
	}
	if got := Denormalize(checks); !reflect.DeepEqual(got, in) {
		t.Fatalf("timeout did not round-trip: %#v", got)
	}
}

func TestNormalizeUnknownLegacyStatusIsInconclusive(t *testing.T) {
	in := []Result{{RuleName: "CrashLoopBackOff", Status: "mystery", Severity: SeverityCritical, Message: "m"}}
	c := Normalize("local", &DiagnosticTarget{}, checkNow, in)[0]
	if c.Status != CheckInconclusive || c.Reason != ReasonSourceUnavailable {
		t.Fatalf("unknown status = %s/%s, want inconclusive/source_unavailable, never a pass", c.Status, c.Reason)
	}
	if got := Denormalize([]CheckResult{c}); !reflect.DeepEqual(got, in) {
		t.Fatalf("unknown status did not round-trip: %#v", got)
	}
}

func TestCheckIDsAreStableAndUnique(t *testing.T) {
	// A literal, not a copy of the map: this is the frozen wire contract and a
	// rule rename must not move an id that incidents and receipts persist.
	want := map[string]string{
		"CrashLoopBackOff": "diagnostics/crashloopbackoff",
		"ImagePullBackOff": "diagnostics/imagepullbackoff",
		"PendingPod":       "diagnostics/pendingpod",
		"ReplicaMismatch":  "diagnostics/replicamismatch",
		"ZeroEndpoints":    "diagnostics/zeroendpoints",
		"PendingPVC":       "diagnostics/pendingpvc",
	}
	if !reflect.DeepEqual(checkIDForRule, want) {
		t.Fatalf("checkIDForRule drifted from the frozen contract:\n got  %v\n want %v", checkIDForRule, want)
	}

	seen := map[string]string{}
	for rule, id := range checkIDForRule {
		if prev, dup := seen[id]; dup {
			t.Errorf("check id %q is used by both %q and %q", id, prev, rule)
		}
		seen[id] = rule
		if !strings.HasPrefix(id, "diagnostics/") {
			t.Errorf("check id %q for %q is missing the producer prefix", id, rule)
		}
	}

	for _, rule := range rules {
		if _, ok := checkIDForRule[rule.name]; !ok {
			t.Errorf("registered rule %q has no stable check id; add it to checkIDForRule", rule.name)
		}
	}
}

func TestNormalizeCarriesSourceIdentity(t *testing.T) {
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team-a", UID: "uid-dep-1"}}
	in := []Result{{RuleName: "ReplicaMismatch", Status: "pass", Severity: SeverityWarning, Message: "ok"}}

	t.Run("object uid and group resource", func(t *testing.T) {
		target := &DiagnosticTarget{Kind: "Deployment", Name: "web", Namespace: "team-a", Object: deploy}
		got := Normalize("cluster-7", target, checkNow, in)[0]
		want := SourceRef{
			ClusterID: "cluster-7", Group: "apps", Version: "v1", Resource: "deployments",
			Kind: "Deployment", Namespace: "team-a", Name: "web", UID: "uid-dep-1",
		}
		if got.Source != want {
			t.Fatalf("Source = %+v, want %+v", got.Source, want)
		}
		if !got.ObservedAt.Equal(checkNow) {
			t.Fatalf("ObservedAt = %v, want the caller's %v", got.ObservedAt, checkNow)
		}
	})

	t.Run("recreated object has a different identity", func(t *testing.T) {
		recreated := deploy.DeepCopy()
		recreated.UID = "uid-dep-2"
		a := Normalize("c", &DiagnosticTarget{Kind: "Deployment", Name: "web", Namespace: "team-a", Object: deploy}, checkNow, in)[0]
		b := Normalize("c", &DiagnosticTarget{Kind: "Deployment", Name: "web", Namespace: "team-a", Object: recreated}, checkNow, in)[0]
		if a.Source.UID == "" || a.Source.UID == b.Source.UID {
			t.Fatalf("same-name recreation must not share a uid: %q vs %q", a.Source.UID, b.Source.UID)
		}
	})

	t.Run("no object means no fabricated uid", func(t *testing.T) {
		got := Normalize("c", &DiagnosticTarget{Kind: "Pod", Name: "web", Namespace: "team-a"}, checkNow, in)[0]
		if got.Source.UID != "" {
			t.Fatalf("Source.UID = %q with no object", got.Source.UID)
		}
		if got.Source.Resource != "pods" || got.Source.Group != "" || got.Source.Version != "v1" {
			t.Fatalf("core-group source = %+v", got.Source)
		}
	})

	t.Run("unknown kind has no resource", func(t *testing.T) {
		got := Normalize("c", &DiagnosticTarget{Kind: "Widget", Name: "w"}, checkNow, in)[0]
		if got.Source.Resource != "" || got.Source.Group != "" || got.Source.Version != "" {
			t.Fatalf("unknown kind produced %+v", got.Source)
		}
	})
}

// TestNormalizeEvidenceCarriesObservedPodUIDs: evidence that names a pod must
// say which instance was observed, so a pod recreated under the same name
// cannot inherit the finding.
func TestNormalizeEvidenceCarriesObservedPodUIDs(t *testing.T) {
	ctx := context.Background()

	normalizeCrashLoop := func(uid types.UID) CheckResult {
		pod := testPod(true)
		pod.UID = uid
		target, err := Resolve(ctx, &countingLister{pods: []*corev1.Pod{pod}}, "team-a", "Pod", "web", &RelatedRBAC{Pods: true})
		if err != nil {
			t.Fatal(err)
		}
		return checkByID(t, Normalize("local", target, checkNow, RunDiagnostics(ctx, target)), "diagnostics/crashloopbackoff")
	}

	first := normalizeCrashLoop("uid-web-1")
	if want := map[string]string{"Pod/web": "uid-web-1"}; !reflect.DeepEqual(first.Evidence, want) {
		t.Fatalf("evidence = %v, want %v", first.Evidence, want)
	}
	recreated := normalizeCrashLoop("uid-web-2")
	if want := map[string]string{"Pod/web": "uid-web-2"}; !reflect.DeepEqual(recreated.Evidence, want) {
		t.Fatalf("recreated evidence = %v, want %v", recreated.Evidence, want)
	}

	link := func(kind, name string) []Result {
		return []Result{{
			RuleName: "CrashLoopBackOff", Status: "fail", Severity: SeverityCritical, Message: "m",
			Links: []Link{{Label: name, Kind: kind, Name: name}},
		}}
	}
	observed := &DiagnosticTarget{Pods: []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "web", UID: "uid-web-1"}}}}

	t.Run("a pod that was not observed gets no uid", func(t *testing.T) {
		want := map[string]string{"Pod/ghost": ""}
		if got := Normalize("local", observed, checkNow, link("Pod", "ghost"))[0].Evidence; !reflect.DeepEqual(got, want) {
			t.Fatalf("evidence = %v for a pod the target never listed, want %v", got, want)
		}
	})
	t.Run("only pod links take a pod uid", func(t *testing.T) {
		want := map[string]string{"Service/web": ""}
		if got := Normalize("local", observed, checkNow, link("Service", "web"))[0].Evidence; !reflect.DeepEqual(got, want) {
			t.Fatalf("evidence = %v on a Service link, want %v", got, want)
		}
	})
	t.Run("the legacy links are unchanged", func(t *testing.T) {
		in := link("Pod", "web")
		if got := Denormalize(Normalize("local", observed, checkNow, in)); !reflect.DeepEqual(got, in) {
			t.Fatalf("round trip changed the legacy links: %#v", got)
		}
	})
}

func TestKindGroupVersionCoversEveryDiagnosticKind(t *testing.T) {
	for kind := range kindToResource {
		gv, ok := kindGroupVersion[kind]
		if !ok {
			t.Errorf("kind %q is diagnosable but has no kindGroupVersion entry", kind)
		} else if gv.Version == "" {
			t.Errorf("kind %q has no API version", kind)
		}
	}
	for kind := range kindGroupVersion {
		if _, ok := kindToResource[kind]; !ok {
			t.Errorf("kindGroupVersion lists %q, which diagnostics cannot resolve", kind)
		}
	}
}

// TestNormalizeNeverEmbedsSecretValues feeds a target whose object and pods are
// full of canaries in the places a Secret value realistically hides, and checks
// none survive serialization: only identity fields leave the object.
func TestNormalizeNeverEmbedsSecretValues(t *testing.T) {
	const canary = "CANARY-s3cr3t-value"
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "team-a", UID: "uid-dep",
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"stringData":{"password":"` + canary + `"}}`},
			Labels:      map[string]string{"token": canary},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Env: []corev1.EnvVar{{Name: "DB_PASSWORD", Value: canary}}}},
		}}},
	}
	pod := testPod(true)
	pod.Annotations = map[string]string{"leak": canary}
	pod.Spec.Containers = []corev1.Container{{Name: "app", Env: []corev1.EnvVar{{Name: "TOKEN", Value: canary}}}}

	target := &DiagnosticTarget{Kind: "Deployment", Name: "web", Namespace: "team-a", Object: deploy, Pods: []*corev1.Pod{pod}}
	checks := Normalize("local", target, checkNow, RunDiagnostics(context.Background(), target))
	if len(checks) == 0 {
		t.Fatal("no checks produced")
	}
	raw, err := json.Marshal(checks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), canary) || strings.Contains(string(raw), "last-applied-configuration") {
		t.Fatalf("serialized checks carry object content: %s", raw)
	}
}

func TestNormalizeIsNilSafe(t *testing.T) {
	in := []Result{{RuleName: "CrashLoopBackOff", Status: "pass", Severity: SeverityCritical, Message: "m"}}

	if got := Normalize("local", nil, checkNow, nil); got != nil {
		t.Errorf("nil target, nil results = %#v, want nil", got)
	}
	got := Normalize("local", nil, checkNow, in)
	if len(got) != 1 || got[0].Source.ClusterID != "local" || got[0].Source.Kind != "" {
		t.Errorf("nil target produced %+v", got)
	}
	if got := Normalize("local", &DiagnosticTarget{Limitations: []Limitation{}}, checkNow, in); got[0].Status != CheckPass {
		t.Errorf("empty limitations downgraded a pass: %+v", got[0])
	}
	if Denormalize(nil) != nil {
		t.Error("Denormalize(nil) is not nil")
	}
}

// TestCheckResultJSONShape pins the wire keys. Release D evidence and Release E
// receipts persist this JSON, so a tag change is a data migration.
func TestCheckResultJSONShape(t *testing.T) {
	full := CheckResult{
		CheckID: "diagnostics/crashloopbackoff", Status: CheckInconclusive, Severity: SeverityCritical,
		Reason: ReasonPermissionDenied, Message: "m", Detail: "d", Remediation: "r",
		Source: SourceRef{
			ClusterID: "local", Group: "apps", Version: "v1", Resource: "deployments",
			Kind: "Deployment", Namespace: "team-a", Name: "web", UID: "u",
		},
		ObservedAt: checkNow,
		Evidence:   map[string]string{"Pod/web-0": "pu"},
		// The unexported legacy result must never reach the wire.
		legacy: Result{RuleName: "CrashLoopBackOff", Status: "pass", Message: "legacy-only"},
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"checkId":"diagnostics/crashloopbackoff","status":"inconclusive","severity":"critical",` +
		`"reason":"permission_denied","message":"m","detail":"d","remediation":"r",` +
		`"source":{"clusterId":"local","group":"apps","version":"v1","resource":"deployments","kind":"Deployment","namespace":"team-a","name":"web","uid":"u"},` +
		`"observedAt":"2026-10-04T12:00:00Z",` +
		`"evidence":{"Pod/web-0":"pu"}}`
	if string(raw) != want {
		t.Fatalf("wire shape changed:\n got  %s\n want %s", raw, want)
	}

	minimal, err := json.Marshal(CheckResult{
		CheckID: "diagnostics/pendingpvc", Status: CheckPass, Severity: SeverityWarning,
		Reason: ReasonOK, Message: "ok", ObservedAt: checkNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	const wantMin = `{"checkId":"diagnostics/pendingpvc","status":"pass","severity":"warning","reason":"ok","message":"ok",` +
		`"source":{"clusterId":"","resource":"","kind":"","name":""},` +
		`"observedAt":"2026-10-04T12:00:00Z"}`
	if string(minimal) != wantMin {
		t.Fatalf("minimal wire shape changed:\n got  %s\n want %s", minimal, wantMin)
	}
}

// The types below mirror, field for field, the "Interface contract with Release D
// U20" section of docs/plans/2026-09-10-release-e-tracked-changes-impl.md.
// Release E persists CheckResult in change_receipts.verification and decodes it
// with exactly these, so CheckResult must stay readable through them.
type releaseEStatus string

type releaseESourceRef struct {
	ClusterID string `json:"clusterId"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

type releaseECheckResult struct {
	CheckID    string            `json:"checkId"`
	Status     releaseEStatus    `json:"status"`
	Severity   Severity          `json:"severity"`
	Reason     string            `json:"reason"`
	Message    string            `json:"message"`
	Detail     string            `json:"detail,omitempty"`
	Source     releaseESourceRef `json:"source"`
	ObservedAt time.Time         `json:"observedAt"`
	Evidence   map[string]string `json:"evidence,omitempty"`
}

func TestCheckResultSatisfiesReleaseEContract(t *testing.T) {
	t.Run("status values", func(t *testing.T) {
		for got, want := range map[CheckStatus]string{
			CheckPass: "pass", CheckWarn: "warn", CheckFail: "fail", CheckInconclusive: "inconclusive",
		} {
			if string(got) != want {
				t.Errorf("status constant = %q, want %q", got, want)
			}
		}
	})

	// Every field Release E declares exists here with the same JSON key and the
	// same Go kind. A rename or a type change fails here before it reaches a
	// persisted receipt.
	t.Run("field keys and kinds", func(t *testing.T) {
		assertCovers := func(t *testing.T, declared, actual reflect.Type) {
			t.Helper()
			byTag := map[string]reflect.StructField{}
			for i := 0; i < actual.NumField(); i++ {
				f := actual.Field(i)
				if tag := f.Tag.Get("json"); tag != "" {
					byTag[tag] = f
				}
			}
			for i := 0; i < declared.NumField(); i++ {
				d := declared.Field(i)
				tag := d.Tag.Get("json")
				got, ok := byTag[tag]
				if !ok {
					t.Errorf("Release E declares json:%q (%s) and %s has no such field", tag, d.Name, actual.Name())
					continue
				}
				if got.Type.Kind() != d.Type.Kind() {
					t.Errorf("json:%q is %s here, %s in Release E's contract", tag, got.Type.Kind(), d.Type.Kind())
				}
			}
		}
		assertCovers(t, reflect.TypeOf(releaseECheckResult{}), reflect.TypeOf(CheckResult{}))
		assertCovers(t, reflect.TypeOf(releaseESourceRef{}), reflect.TypeOf(SourceRef{}))
	})

	// Real output, decoded through Release E's types, loses nothing it declares.
	t.Run("real results decode", func(t *testing.T) {
		ctx := context.Background()
		lister := &countingLister{pods: []*corev1.Pod{testPod(true)}}
		var checks []CheckResult
		for _, related := range []*RelatedRBAC{{Pods: true}, {}} {
			target, err := Resolve(ctx, lister, "team-a", "Pod", "web", related)
			if err != nil {
				t.Fatal(err)
			}
			checks = append(checks, Normalize("cluster-7", target, checkNow, RunDiagnostics(ctx, target))...)
		}

		sawStatus := map[CheckStatus]bool{}
		for _, c := range checks {
			sawStatus[c.Status] = true
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var got releaseECheckResult
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("Release E cannot decode %s: %v", raw, err)
			}
			want := releaseECheckResult{
				CheckID: c.CheckID, Status: releaseEStatus(c.Status), Severity: c.Severity,
				Reason: c.Reason, Message: c.Message, Detail: c.Detail,
				Source:     releaseESourceRef(c.Source),
				ObservedAt: c.ObservedAt, Evidence: c.Evidence,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s decoded through Release E's contract as\n got  %+v\n want %+v", c.CheckID, got, want)
			}
		}
		// The corpus must exercise a conclusive and an inconclusive result.
		if !sawStatus[CheckFail] || !sawStatus[CheckInconclusive] || !sawStatus[CheckPass] {
			t.Fatalf("decode corpus is too narrow: %v", sawStatus)
		}
	})
}

func TestNormalizeKeepsWarnDistinctFromFail(t *testing.T) {
	cases := []struct {
		legacy     string
		wantStatus CheckStatus
		wantReason string
	}{
		{"pass", CheckPass, ReasonOK},
		{"warn", CheckWarn, ReasonFinding},
		{"fail", CheckFail, ReasonFinding},
	}
	for _, c := range cases {
		t.Run(c.legacy, func(t *testing.T) {
			in := []Result{{
				RuleName: "ReplicaMismatch", Status: c.legacy, Severity: SeverityWarning, Message: "m",
				Links: []Link{{Label: "web-0", Kind: "Pod", Name: "web-0"}},
			}}
			got := Normalize("local", &DiagnosticTarget{}, checkNow, in)[0]
			if got.Status != c.wantStatus || got.Reason != c.wantReason {
				t.Fatalf("legacy %q = %s/%q, want %s/%q", c.legacy, got.Status, got.Reason, c.wantStatus, c.wantReason)
			}
		})
	}
}

// TestNormalizeMarksPanickedCheckInconclusive builds the panic result with the
// real runSafeCheck. A rule that crashed observed nothing about the target, so
// it must not read as a finding against it.
func TestNormalizeMarksPanickedCheckInconclusive(t *testing.T) {
	boom := ruleEntry{
		name:     "CrashLoopBackOff",
		severity: SeverityCritical,
		check:    func(context.Context, *DiagnosticTarget) Result { panic("rule bug") },
	}
	target := &DiagnosticTarget{Kind: "Pod", Name: "web", Namespace: "team-a"}
	panicked := runSafeCheck(context.Background(), boom, target)
	if panicked.Status != "fail" {
		t.Fatalf("legacy panic status = %q, want the frozen fail", panicked.Status)
	}

	in := []Result{panicked}
	checks := Normalize("local", target, checkNow, in)
	if checks[0].Status != CheckInconclusive || checks[0].Reason != ReasonInternalError {
		t.Fatalf("panicked check = %s/%s, want inconclusive/internal_error", checks[0].Status, checks[0].Reason)
	}
	if got := Denormalize(checks); !reflect.DeepEqual(got, in) {
		t.Fatalf("panic did not round-trip: %#v", got)
	}
}

// TestLimitedByTakesFirstMatchingLimitation pins the order when more than one
// limitation hits a rule, so the reason a consumer branches on is deterministic.
func TestLimitedByTakesFirstMatchingLimitation(t *testing.T) {
	deps := []string{limitPods, limitReplicaSets}
	denied := Limitation{Kind: limitPods, Reason: ReasonPermissionDenied}
	rsDown := Limitation{Kind: limitReplicaSets, Reason: ReasonSourceUnavailable}

	if got, ok := limitedBy(deps, []Limitation{denied, rsDown}); !ok || got != ReasonPermissionDenied {
		t.Errorf("pods first = %q/%v, want permission_denied", got, ok)
	}
	if got, ok := limitedBy(deps, []Limitation{rsDown, denied}); !ok || got != ReasonSourceUnavailable {
		t.Errorf("replicasets first = %q/%v, want source_unavailable", got, ok)
	}
	if _, ok := limitedBy(deps, []Limitation{{Kind: "services", Reason: ReasonPermissionDenied}}); ok {
		t.Error("a limitation on a resolution the rule does not read still matched")
	}
	if _, ok := limitedBy(nil, []Limitation{denied}); ok {
		t.Error("a rule with no dependencies was limited")
	}
}

// TestNormalizeDowngradesPassThatCarriesLinks: a pass is an absence claim, so
// links on it do not make it observed evidence the way they do on a finding.
func TestNormalizeDowngradesPassThatCarriesLinks(t *testing.T) {
	target := &DiagnosticTarget{Limitations: []Limitation{{Kind: limitPods, Reason: ReasonPermissionDenied}}}
	in := []Result{{
		RuleName: "CrashLoopBackOff", Status: "pass", Severity: SeverityCritical, Message: "m",
		Links: []Link{{Label: "web-0", Kind: "Pod", Name: "web-0"}},
	}}
	got := Normalize("local", target, checkNow, in)[0]
	if got.Status != CheckInconclusive || got.Reason != ReasonPermissionDenied {
		t.Fatalf("pass with links under a limitation = %s/%s, want inconclusive/permission_denied", got.Status, got.Reason)
	}
}
