package gitops

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// authoritativeKinds is the KTD10 set, re-derived here from the wire values
// rather than read from Authoritative(), so a widened or narrowed
// Authoritative() fails this oracle instead of silently agreeing with it.
var authoritativeKinds = map[OwnershipEvidenceKind]bool{
	"argo-status-resource": true,
	"flux-inventory-entry": true,
}

// fuzzEvidenceKinds are the kinds an evidence byte selects from: every
// declared kind plus two that are not.
var fuzzEvidenceKinds = []OwnershipEvidenceKind{
	EvidenceArgoStatusResource,
	EvidenceFluxInventoryEntry,
	EvidenceArgoTrackingID,
	EvidenceInstanceLabel,
	EvidenceManagedByLabel,
	EvidenceFieldManager,
	EvidenceFluxOwnerLabel,
	"",
	"bogus",
}

var fuzzTools = []Tool{ToolArgoCD, ToolFluxCD, ToolNone, ToolBoth}

// fuzzApps are the applications offered to decide on every input, so any
// evidence naming one of their ids could confirm it if the gate let it.
var fuzzApps = []OwnedByApp{
	{AppID: "argo:argocd:web", Tool: ToolArgoCD, Kind: "Application", Namespace: "argocd", Name: "web", Source: AppSource{RepoURL: "https://git.example/web"}},
	{AppID: "flux-ks:flux-system:apps", Tool: ToolFluxCD, Kind: "Kustomization", Namespace: "flux-system", Name: "apps", Source: AppSource{Path: "./apps"}},
	{AppID: "argo:argocd:web", Tool: ToolFluxCD, Kind: "Application", Namespace: "argocd", Name: "web"},
}

var fuzzAppIDs = []string{"argo:argocd:web", "flux-ks:flux-system:apps", ""}

// evidenceFromFuzz turns byte pairs into evidence: the first byte picks the
// kind, the second the tool (low two bits) and app id (the rest).
func evidenceFromFuzz(spec []byte, raw string) []OwnershipEvidence {
	var ev []OwnershipEvidence
	for i := 0; i+1 < len(spec) && len(ev) < 16; i += 2 {
		ev = append(ev, OwnershipEvidence{
			Kind:     fuzzEvidenceKinds[int(spec[i])%len(fuzzEvidenceKinds)],
			Tool:     fuzzTools[int(spec[i+1]&3)],
			AppID:    fuzzAppIDs[int(spec[i+1]>>2)%len(fuzzAppIDs)],
			RawValue: raw,
		})
	}
	return ev
}

// evPair encodes one evidence entry for evidenceFromFuzz.
func evPair(kind, tool, app int) []byte { return []byte{byte(kind), byte(tool | app<<2)} }

func statesFromFuzz(bits uint16) (argo, flux toolState) {
	bit := func(n uint) bool { return bits&(1<<n) != 0 }
	argo = toolState{installed: bit(0), unavailable: bit(1), forbidden: bit(2), hidden: bit(3), exhausted: bit(4)}
	flux = toolState{installed: bit(5), unavailable: bit(6), forbidden: bit(7), hidden: bit(8), exhausted: bit(9), helmReleaseHinted: bit(10)}
	return argo, flux
}

// FuzzOwnershipEvidence guards the KTD10 rule at its chokepoint.
//
// Oracle A: the hint parsers and decide never panic, on any annotation value
// or any unstructured object.
//
// Oracle C: evidence without an authoritative kind never yields a confirmed
// or conflicting verdict, a controller, or an application; every listed
// application is backed by authoritative evidence naming it for its tool;
// WritableGitSource is false on every input. Conversely an authoritative
// match is never dropped.
func FuzzOwnershipEvidence(f *testing.F) {
	hintsOnly := append(append(append(evPair(2, 0, 0), evPair(3, 0, 0)...), evPair(4, 1, 1)...), evPair(6, 1, 1)...)
	confirmed := append(evPair(0, 0, 0), evPair(3, 0, 0)...)
	both := append(evPair(0, 0, 0), evPair(1, 1, 1)...)
	crossed := append(evPair(0, 0, 0), evPair(1, 1, 0)...) // same app id claimed by both tools
	kustomization := []byte(`
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
  namespace: flux-system
status:
  inventory:
    entries:
    - id: prod_web_apps_Deployment
    - id: a_b_c_d_e_f
    - id: ____
    - id: prod_web__Kind_With_Underscores
    - id: ""
    - id: 7
`)
	live := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: prod
  labels:
    app.kubernetes.io/instance: web
    app.kubernetes.io/managed-by: argocd
    kustomize.toolkit.fluxcd.io/name: apps
    kustomize.toolkit.fluxcd.io/namespace: flux-system
    helm.toolkit.fluxcd.io/name: web
  annotations:
    argocd.argoproj.io/tracking-id: "web:apps/Deployment:prod/web"
  managedFields:
  - manager: argocd-controller
  - manager: kustomize-controller
  - manager: 7
  - "not a map"
status:
  resources:
  - {group: apps, kind: Deployment, namespace: prod, name: web}
  - "not a map"
`)
	for _, seed := range []struct {
		hint   string
		obj    []byte
		ev     []byte
		states uint16
	}{
		{"web:apps/Deployment:prod/web", live, hintsOnly, 0b0000_0010_0001},
		{"web:apps/Deployment:prod/we:b:c", live, hintsOnly, 0},
		{"argocd_web:/Service:prod/web", kustomization, confirmed, 0b0000_0010_0001},
		{"", []byte(`{}`), nil, 0},
		{strings.Repeat("a\x00‮", 1400), live, hintsOnly, 0xffff},
		{"web:apps/Kind_With_Under_scores:prod/web", kustomization, both, 0b0000_0010_0001},
		{"web:apps/Deployment:prod/web", []byte(`{"metadata":"oops"}`), crossed, 0},
		{"::", []byte(`{"metadata":{"labels":[],"annotations":{"argocd.argoproj.io/tracking-id":7},"managedFields":{}}}`), hintsOnly, 0b0100_0000_0000},
	} {
		f.Add(seed.hint, seed.obj, seed.ev, seed.states)
	}

	f.Fuzz(func(t *testing.T, hint string, objData, evSpec []byte, states uint16) {
		// Oracle A over the parsers.
		parseArgoTrackingID(hint)
		if s := sanitizeHint(hint); len(s) > maxHintValueBytes {
			t.Fatalf("sanitized hint is %d bytes", len(s))
		}
		synthetic := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": hint}}}
		synthetic.SetAnnotations(map[string]string{argoTrackingIDAnnotation: hint})
		synthetic.SetLabels(map[string]string{instanceLabel: hint, managedByLabel: hint, fluxKSNameLabel: hint, fluxHRNameLabel: hint})
		checkHints(t, hintsFor(synthetic))

		ref := ObjectRef{Group: "apps", Kind: "Deployment", Namespace: "prod", Name: hint}
		if u, ok := unstructuredFromFuzz(objData); ok {
			checkHints(t, hintsFor(u))
			for _, mr := range append(extractArgoResources(u), extractFluxInventory(u)...) {
				matchesRef(mr, ref)
			}
		}

		// Oracle C over decide.
		ev := evidenceFromFuzz(evSpec, hint)
		argo, flux := statesFromFuzz(states)
		res := decide(ref, ev, fuzzApps, argo, flux)
		checkVerdict(t, ev, res)
	})
}

// checkHints: hints are never authoritative, never name an application, and
// never carry an unsanitized value.
func checkHints(t *testing.T, h objectHints) {
	t.Helper()
	for _, e := range h.evidence {
		if authoritativeKinds[e.Kind] || e.AppID != "" {
			t.Fatalf("hint %+v is authoritative or names an app", e)
		}
		if len(e.RawValue) > maxHintValueBytes {
			t.Fatalf("hint raw value is %d bytes", len(e.RawValue))
		}
	}
}

func checkVerdict(t *testing.T, ev []OwnershipEvidence, res OwnershipResult) {
	t.Helper()
	if res.WritableGitSource {
		t.Fatal("WritableGitSource = true")
	}
	if res.UIDConfirmed || res.IdentityBasis != "group-kind-namespace-name" {
		t.Fatalf("identity = %q/uidConfirmed=%v", res.IdentityBasis, res.UIDConfirmed)
	}
	switch res.Confidence {
	case ConfidenceConfirmed, ConfidenceConflicting, ConfidenceUnknown, ConfidenceForbidden, ConfidenceUnavailable:
	default:
		t.Fatalf("confidence %q is not a declared value", res.Confidence)
	}
	if res.Reason == "" {
		t.Fatal("empty reason")
	}

	type claim struct {
		appID string
		tool  Tool
	}
	backed := map[claim]bool{}
	for _, e := range ev {
		if authoritativeKinds[e.Kind] && e.AppID != "" {
			backed[claim{e.AppID, e.Tool}] = true
		}
	}
	claimed := res.Confidence == ConfidenceConfirmed || res.Confidence == ConfidenceConflicting
	if len(backed) == 0 && (claimed || res.Controller != OwnedByNone || len(res.Apps) != 0) {
		t.Fatalf("hint-only evidence %+v produced %s/%s with apps %+v", ev, res.Controller, res.Confidence, res.Apps)
	}
	for _, a := range res.Apps {
		if !backed[claim{a.AppID, a.Tool}] {
			t.Fatalf("app %+v listed without authoritative evidence naming it", a)
		}
	}
	if claimed != (len(res.Apps) > 0) {
		t.Fatalf("verdict %s with %d apps", res.Confidence, len(res.Apps))
	}
	// Every authoritative match is kept: the controller is exactly the set of
	// tools with a backed application.
	var argoBacked, fluxBacked bool
	for _, a := range fuzzApps {
		if backed[claim{a.AppID, a.Tool}] {
			argoBacked = argoBacked || a.Tool == ToolArgoCD
			fluxBacked = fluxBacked || a.Tool == ToolFluxCD
		}
	}
	want := OwnedByNone
	switch {
	case argoBacked && fluxBacked:
		want = OwnedByBoth
	case argoBacked:
		want = OwnedByArgoCD
	case fluxBacked:
		want = OwnedByFluxCD
	}
	if res.Controller != want {
		t.Fatalf("controller = %s, want %s from evidence %+v", res.Controller, want, ev)
	}
}
