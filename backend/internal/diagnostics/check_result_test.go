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

func checkByID(t *testing.T, checks []CheckResult, id CheckID) CheckResult {
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

	podRules := map[string]CheckID{
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
			if c.Status != CheckStatusInconclusive || c.Inconclusive != ReasonPermissionDenied {
				t.Errorf("%s = %s/%s, want inconclusive/permission_denied", id, c.Status, c.Inconclusive)
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
		if crash.Status != CheckStatusFail || crash.Inconclusive != "" {
			t.Errorf("crash-looping pod = %s/%q, want fail with no inconclusive reason", crash.Status, crash.Inconclusive)
		}
		for _, id := range []CheckID{"diagnostics/imagepullbackoff", "diagnostics/pendingpod"} {
			if c := checkByID(t, checks, id); c.Status != CheckStatusPass {
				t.Errorf("%s = %s, want a conclusive pass when pods were observed", id, c.Status)
			}
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
	if c.Status != CheckStatusInconclusive || c.Inconclusive != ReasonSourceUnavailable {
		t.Fatalf("crashloop with unreadable pods = %s/%s, want inconclusive/source_unavailable", c.Status, c.Inconclusive)
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
		if c.Status != CheckStatusFail || c.Inconclusive != "" {
			t.Errorf("observed-empty ZeroEndpoints = %s/%q, want fail", c.Status, c.Inconclusive)
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
		if c.Status != CheckStatusInconclusive || c.Inconclusive != ReasonPermissionDenied {
			t.Errorf("unobservable ZeroEndpoints = %s/%q, want inconclusive/permission_denied", c.Status, c.Inconclusive)
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
		if c.Status != CheckStatusFail || c.Inconclusive != "" {
			t.Errorf("evidence-backed failure = %s/%q, want fail", c.Status, c.Inconclusive)
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
		if c := checkByID(t, checks, "diagnostics/replicamismatch"); c.Status != CheckStatusFail || c.Inconclusive != "" {
			t.Errorf("ReplicaMismatch = %s/%q, want a conclusive fail", c.Status, c.Inconclusive)
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
		if c := checkByID(t, checks, "diagnostics/replicamismatch"); c.Status != CheckStatusPass {
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
		if c := checkByID(t, checks, "diagnostics/pendingpvc"); c.Status != CheckStatusFail || c.Inconclusive != "" {
			t.Errorf("PendingPVC = %s/%q, want a conclusive fail", c.Status, c.Inconclusive)
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
	if checks[0].Status != CheckStatusInconclusive || checks[0].Inconclusive != ReasonTimedOut {
		t.Fatalf("timed-out check = %s/%s, want inconclusive/timed_out", checks[0].Status, checks[0].Inconclusive)
	}
	if got := Denormalize(checks); !reflect.DeepEqual(got, in) {
		t.Fatalf("timeout did not round-trip: %#v", got)
	}
}

func TestNormalizeUnknownLegacyStatusIsInconclusive(t *testing.T) {
	in := []Result{{RuleName: "CrashLoopBackOff", Status: "mystery", Severity: SeverityCritical, Message: "m"}}
	c := Normalize("local", &DiagnosticTarget{}, checkNow, in)[0]
	if c.Status != CheckStatusInconclusive || c.Inconclusive != ReasonSourceUnavailable {
		t.Fatalf("unknown status = %s/%s, want inconclusive/source_unavailable, never a pass", c.Status, c.Inconclusive)
	}
	if got := Denormalize([]CheckResult{c}); !reflect.DeepEqual(got, in) {
		t.Fatalf("unknown status did not round-trip: %#v", got)
	}
}

func TestCheckIDsAreStableAndUnique(t *testing.T) {
	// A literal, not a copy of the map: this is the frozen wire contract and a
	// rule rename must not move an id that incidents and receipts persist.
	want := map[string]CheckID{
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

	seen := map[CheckID]string{}
	for rule, id := range checkIDForRule {
		if prev, dup := seen[id]; dup {
			t.Errorf("check id %q is used by both %q and %q", id, prev, rule)
		}
		seen[id] = rule
		if !strings.HasPrefix(string(id), "diagnostics/") {
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
		want := CheckSourceRef{
			ClusterID: "cluster-7", APIGroup: "apps", Resource: "deployments",
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
		if got.Source.Resource != "pods" || got.Source.APIGroup != "" {
			t.Fatalf("core-group source = %+v", got.Source)
		}
	})

	t.Run("unknown kind has no resource", func(t *testing.T) {
		got := Normalize("c", &DiagnosticTarget{Kind: "Widget", Name: "w"}, checkNow, in)[0]
		if got.Source.Resource != "" || got.Source.APIGroup != "" {
			t.Fatalf("unknown kind produced %+v", got.Source)
		}
	})
}

func TestKindAPIGroupCoversEveryDiagnosticKind(t *testing.T) {
	for kind := range kindToResource {
		if _, ok := kindAPIGroup[kind]; !ok {
			t.Errorf("kind %q is diagnosable but has no kindAPIGroup entry", kind)
		}
	}
	for kind := range kindAPIGroup {
		if _, ok := kindToResource[kind]; !ok {
			t.Errorf("kindAPIGroup lists %q, which diagnostics cannot resolve", kind)
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
	if got := Normalize("local", &DiagnosticTarget{Limitations: []Limitation{}}, checkNow, in); got[0].Status != CheckStatusPass {
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
		CheckID: "diagnostics/crashloopbackoff", Status: CheckStatusInconclusive, Severity: SeverityCritical,
		Summary: "s", Detail: "d", Remediation: "r",
		Source: CheckSourceRef{
			ClusterID: "local", APIGroup: "apps", Resource: "deployments",
			Kind: "Deployment", Namespace: "team-a", Name: "web", UID: "u",
		},
		ObservedAt:   checkNow,
		Evidence:     []CheckEvidenceItem{{Label: "web-0", Kind: "Pod", Name: "web-0", UID: "pu", Observed: map[string]string{"k": "v"}}},
		Inconclusive: ReasonPermissionDenied,
		// The unexported legacy fields must never reach the wire.
		legacyRuleName: "CrashLoopBackOff", legacyStatus: "pass",
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"checkId":"diagnostics/crashloopbackoff","status":"inconclusive","severity":"critical",` +
		`"summary":"s","detail":"d","remediation":"r",` +
		`"source":{"clusterId":"local","apiGroup":"apps","resource":"deployments","kind":"Deployment","namespace":"team-a","name":"web","uid":"u"},` +
		`"observedAt":"2026-10-04T12:00:00Z",` +
		`"evidence":[{"label":"web-0","kind":"Pod","name":"web-0","uid":"pu","observed":{"k":"v"}}],` +
		`"inconclusive":"permission_denied"}`
	if string(raw) != want {
		t.Fatalf("wire shape changed:\n got  %s\n want %s", raw, want)
	}

	minimal, err := json.Marshal(CheckResult{CheckID: "diagnostics/pendingpvc", Status: CheckStatusPass, Severity: SeverityWarning, Summary: "ok", ObservedAt: checkNow})
	if err != nil {
		t.Fatal(err)
	}
	const wantMin = `{"checkId":"diagnostics/pendingpvc","status":"pass","severity":"warning","summary":"ok",` +
		`"source":{"clusterId":"","apiGroup":"","resource":"","kind":"","namespace":"","name":""},` +
		`"observedAt":"2026-10-04T12:00:00Z"}`
	if string(minimal) != wantMin {
		t.Fatalf("minimal wire shape changed:\n got  %s\n want %s", minimal, wantMin)
	}
}
