package externalsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// --- Fixtures ----------------------------------------------------------------

// esoAPIResource is one discovery entry for an ESO kind.
func esoAPIResource(plural, kind string, namespaced bool) metav1.APIResource {
	return metav1.APIResource{Name: plural, Kind: kind, Namespaced: namespaced, Verbs: []string{"get", "list"}}
}

// servedAt is a discovery group-version list. The first list for a group is
// the group's preferred version (FakeDiscovery's rule).
func servedAt(version string, res ...metav1.APIResource) *metav1.APIResourceList {
	return &metav1.APIResourceList{GroupVersion: GroupName + "/" + version, APIResources: res}
}

// allKindsAtV1 is a cluster serving every ESO kind at v1.
func allKindsAtV1() []*metav1.APIResourceList {
	return []*metav1.APIResourceList{servedAt("v1",
		esoAPIResource("externalsecrets", "ExternalSecret", true),
		esoAPIResource("externalsecrets/status", "ExternalSecret", true),
		esoAPIResource("clusterexternalsecrets", "ClusterExternalSecret", false),
		esoAPIResource("secretstores", "SecretStore", true),
		esoAPIResource("clustersecretstores", "ClusterSecretStore", false),
		esoAPIResource("pushsecrets", "PushSecret", true),
	)}
}

// evidenceFixture wires a Handler to fake discovery, a fake impersonating
// dynamic client, and a fake impersonating typed client, and counts the
// discovery calls so the cache can be observed.
type evidenceFixture struct {
	h          *Handler
	kube       *kubefake.Clientset
	discoCalls *atomic.Int32
}

func newEvidenceFixture(disco []*metav1.APIResourceList, objs []runtime.Object, events []corev1.Event, ac *resources.AccessChecker) *evidenceFixture {
	fake := &clienttesting.Fake{Resources: disco}
	calls := &atomic.Int32{}
	fake.AddReactor("get", "group", func(clienttesting.Action) (bool, runtime.Object, error) {
		calls.Add(1)
		return false, nil, nil
	})
	d := detectedDiscoverer()
	d.discoOverride = func() discovery.DiscoveryInterface { return &fakediscovery.FakeDiscovery{Fake: fake} }

	eventObjs := make([]runtime.Object, 0, len(events))
	for i := range events {
		eventObjs = append(eventObjs, &events[i])
	}
	kube := kubefake.NewClientset(eventObjs...)
	dynFake := evidenceDynClient(disco, objs...)

	h := &Handler{
		Discoverer:    d,
		AccessChecker: ac,
		Logger:        slog.Default(),
		ClusterID:     "local",
		dynForUserOverride: func(string, []string) (dynamic.Interface, error) {
			return dynFake, nil
		},
		clientForUserOverride: func(string, []string) (kubernetes.Interface, error) {
			return kube, nil
		},
	}
	return &evidenceFixture{h: h, kube: kube, discoCalls: calls}
}

// evidenceDynClient registers every kind the discovery fixture serves, at the
// version it serves it, so a Get at any other version finds nothing.
func evidenceDynClient(disco []*metav1.APIResourceList, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{}
	for _, l := range disco {
		gv, _ := schema.ParseGroupVersion(l.GroupVersion)
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			scheme.AddKnownTypeWithName(gv.WithKind(r.Kind), &unstructured.Unstructured{})
			scheme.AddKnownTypeWithName(gv.WithKind(r.Kind+"List"), &unstructured.UnstructuredList{})
			listKinds[gv.WithResource(r.Name)] = r.Kind + "List"
		}
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// esoObject builds a minimal unstructured ESO object; an empty ns makes it
// cluster-scoped.
func esoObject(version, kind, ns, name, uid string) *unstructured.Unstructured {
	meta := map[string]any{"name": name, "uid": uid}
	if ns != "" {
		meta["namespace"] = ns
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": GroupName + "/" + version,
		"kind":       kind,
		"metadata":   meta,
	}}
}

// esoEvent is an event about the object with the given uid. The message
// carries a marker an outcome-only response must never contain.
func esoEvent(ns, name, uid string, last time.Time) corev1.Event {
	return corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		InvolvedObject: corev1.ObjectReference{
			UID: types.UID(uid), Name: "db-creds", Namespace: ns, Kind: "ExternalSecret",
		},
		Type:           corev1.EventTypeWarning,
		Reason:         "UpdateFailed",
		Message:        "could not get secret data from provider: key 'prod/db/EVENT_MSG_MARKER' not found",
		Count:          3,
		FirstTimestamp: metav1.NewTime(last.Add(-time.Hour)),
		LastTimestamp:  metav1.NewTime(last),
		Source:         corev1.EventSource{Component: "external-secrets"},
	}
}

// makeEventPage builds n events about uid, named sequentially from
// startIndex with LastTimestamp increasing one minute per global index — the
// shape a real API-server page for a long-lived, frequently-firing object
// would have. Concatenating pages built this way keeps a single unambiguous
// "globally newest" event at the highest index across the whole run.
func makeEventPage(ns, uid string, base time.Time, n, startIndex int) []corev1.Event {
	out := make([]corev1.Event, n)
	for i := 0; i < n; i++ {
		idx := startIndex + i
		out[i] = esoEvent(ns, fmt.Sprintf("ev-%05d", idx), uid, base.Add(time.Duration(idx)*time.Minute))
	}
	return out
}

var evidenceUser = &auth.User{KubernetesUsername: "alice", KubernetesGroups: []string{"dev"}}

func evidenceRequest(kind, ns, name string, u *auth.User) *http.Request {
	r := withUser(httptest.NewRequest(http.MethodGet, "/externalsecrets/evidence/"+
		url.PathEscape(kind)+"/"+url.PathEscape(ns)+"/"+url.PathEscape(name)+"/events", nil), u)
	return urlWithChiParams(r, map[string]string{"kind": kind, "namespace": ns, "name": name})
}

func getEvidence(h *Handler, kind, ns, name string, u *auth.User) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.HandleGetEvidenceEvents(w, evidenceRequest(kind, ns, name, u))
	return w
}

type evidenceBody struct {
	Data struct {
		UID        string `json:"uid"`
		Projection struct {
			Level         string   `json:"level"`
			DroppedFields []string `json:"droppedFields"`
		} `json:"projection"`
		Events    []map[string]json.RawMessage `json:"events"`
		Truncated bool                         `json:"truncated"`
	} `json:"data"`
	Metadata struct {
		Total int `json:"total"`
	} `json:"metadata"`
}

func decodeEvidence(t *testing.T, w *httptest.ResponseRecorder) evidenceBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", w.Code, w.Body.String())
	}
	var b evidenceBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	if b.Data.Events == nil {
		t.Fatalf("events is null; no events must be []\nbody: %s", w.Body.String())
	}
	return b
}

// eventListCalls returns the namespace and field selector of every events
// List the typed client received.
func eventListCalls(kube *kubefake.Clientset) []clienttesting.ListActionImpl {
	var out []clienttesting.ListActionImpl
	for _, a := range kube.Actions() {
		if l, ok := a.(clienttesting.ListActionImpl); ok && l.GetResource().Resource == "events" {
			out = append(out, l)
		}
	}
	return out
}

var evidenceNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

// --- R1: identity ------------------------------------------------------------

func TestEvidenceEvents_ExcludesReplacedSameNameObject(t *testing.T) {
	// The ES was deleted and recreated under the same name: "uid-old" events
	// describe the predecessor and must not appear on the live object.
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-new")},
		[]corev1.Event{
			esoEvent("apps", "ev-old", "uid-old", evidenceNow),
			esoEvent("apps", "ev-new", "uid-new", evidenceNow.Add(-time.Minute)),
		},
		resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))

	if b.Data.UID != "uid-new" {
		t.Errorf("uid = %q; want the live object's uid-new", b.Data.UID)
	}
	if len(b.Data.Events) != 1 || b.Metadata.Total != 1 {
		t.Fatalf("events = %d (total %d); want only the live object's 1", len(b.Data.Events), b.Metadata.Total)
	}
	calls := eventListCalls(f.kube)
	if len(calls) != 1 {
		t.Fatalf("events List calls = %d; want 1", len(calls))
	}
	if got := calls[0].GetListRestrictions().Fields.String(); got != "involvedObject.uid=uid-new" {
		t.Errorf("field selector = %q; want the server-side UID filter involvedObject.uid=uid-new", got)
	}
	if calls[0].GetNamespace() != "apps" {
		t.Errorf("events listed in %q; want the object's namespace", calls[0].GetNamespace())
	}
}

// --- R3: forbidden and unavailable are never empty ---------------------------

func TestEvidenceEvents_ForbiddenIsNotEmpty(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())
	f.kube.PrependReactor("list", "events", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("denied"))
	})

	w := getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser)
	assertErrorReason(t, w, http.StatusForbidden, "events_forbidden")
	if !strings.Contains(w.Body.String(), `list events`) || !strings.Contains(w.Body.String(), `apps`) {
		t.Errorf("forbidden response must name the missing grant and its namespace: %s", w.Body.String())
	}
}

func TestEvidenceEvents_DiscoveryFailure_Returns503(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())
	fake := &clienttesting.Fake{}
	fake.AddReactor("get", "group", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("discovery unreachable")
	})
	f.h.Discoverer.discoOverride = func() discovery.DiscoveryInterface { return &fakediscovery.FakeDiscovery{Fake: fake} }

	w := getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser)
	assertErrorReason(t, w, http.StatusServiceUnavailable, "discovery_unavailable")
	if len(eventListCalls(f.kube)) != 0 {
		t.Error("events were listed although the kind could not be resolved")
	}
}

func TestEvidenceEvents_KindNotServed_Returns404(t *testing.T) {
	disco := []*metav1.APIResourceList{servedAt("v1", esoAPIResource("externalsecrets", "ExternalSecret", true))}
	f := newEvidenceFixture(disco, nil, nil, resources.NewAlwaysAllowAccessChecker())

	assertErrorReason(t, getEvidence(f.h, "pushsecrets", "apps", "p", evidenceUser),
		http.StatusNotFound, "evidence_kind_not_served")
}

func TestEvidenceEvents_EmptyIs200EmptyArray(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if len(b.Data.Events) != 0 || b.Data.Truncated {
		t.Errorf("events = %d, truncated = %t; want [] and false", len(b.Data.Events), b.Data.Truncated)
	}
}

// --- C7: every kind resolves its own served version ---------------------------

func TestEvidenceEvents_EachKindResolvesDiscoveredVersion(t *testing.T) {
	cases := []struct {
		plural, kind, ns string
		namespaced       bool
	}{
		{"externalsecrets", "ExternalSecret", "apps", true},
		{"clusterexternalsecrets", "ClusterExternalSecret", "", false},
		{"secretstores", "SecretStore", "apps", true},
		{"clustersecretstores", "ClusterSecretStore", "", false},
		{"pushsecrets", "PushSecret", "apps", true},
	}
	for _, tc := range cases {
		t.Run(tc.plural, func(t *testing.T) {
			// Served only at v1beta1: a Get at the package's pinned v1 GVR
			// finds nothing in the fake and the request 404s.
			disco := []*metav1.APIResourceList{servedAt("v1beta1", esoAPIResource(tc.plural, tc.kind, tc.namespaced))}
			f := newEvidenceFixture(disco,
				[]runtime.Object{esoObject("v1beta1", tc.kind, tc.ns, "obj", "uid-"+tc.plural)},
				nil, resources.NewAlwaysAllowAccessChecker())

			urlNS := tc.ns
			if !tc.namespaced {
				urlNS = "_"
			}
			b := decodeEvidence(t, getEvidence(f.h, tc.plural, urlNS, "obj", evidenceUser))
			if b.Data.UID != "uid-"+tc.plural {
				t.Errorf("uid = %q; want uid-%s", b.Data.UID, tc.plural)
			}
		})
	}
}

// TestEvidenceEvents_ResourceOnlyInNonPreferredVersion is the homelab's real
// shape: the group prefers v1, but PushSecret is served only at v1alpha1.
// Resolving through the group's preferred version alone would 404 it.
func TestEvidenceEvents_ResourceOnlyInNonPreferredVersion(t *testing.T) {
	disco := []*metav1.APIResourceList{
		servedAt("v1", esoAPIResource("externalsecrets", "ExternalSecret", true)),
		servedAt("v1alpha1", esoAPIResource("pushsecrets", "PushSecret", true)),
	}
	f := newEvidenceFixture(disco,
		[]runtime.Object{esoObject("v1alpha1", "PushSecret", "apps", "push", "uid-push")},
		nil, resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "pushsecrets", "apps", "push", evidenceUser))
	if b.Data.UID != "uid-push" {
		t.Errorf("uid = %q; want uid-push", b.Data.UID)
	}
}

func TestEvidenceEvents_PrefersGroupPreferredVersion(t *testing.T) {
	// Served at both; the preferred (first) version wins, so the object
	// that exists only at v1 is found.
	disco := []*metav1.APIResourceList{
		servedAt("v1", esoAPIResource("externalsecrets", "ExternalSecret", true)),
		servedAt("v1beta1", esoAPIResource("externalsecrets", "ExternalSecret", true)),
	}
	f := newEvidenceFixture(disco,
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-v1")},
		nil, resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if b.Data.UID != "uid-v1" {
		t.Errorf("uid = %q; want uid-v1 from the preferred version", b.Data.UID)
	}
}

// --- R12: scope ---------------------------------------------------------------

func TestEvidenceEvents_ClusterScopedListsAllNamespaces(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ClusterSecretStore", "", "vault", "uid-css")},
		[]corev1.Event{esoEvent("default", "ev-css", "uid-css", evidenceNow)},
		resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "clustersecretstores", "_", "vault", evidenceUser))
	if len(b.Data.Events) != 1 {
		t.Fatalf("events = %d; want the 1 event recorded in another namespace", len(b.Data.Events))
	}
	calls := eventListCalls(f.kube)
	if len(calls) != 1 || calls[0].GetNamespace() != "" {
		t.Fatalf("events List calls = %+v; want one all-namespace List", calls)
	}
}

func TestEvidenceEvents_NamespacedPermissionCannotReachClusterScoped(t *testing.T) {
	// Every grant is namespace-scoped: the cluster-scoped object's own `get`
	// check (namespace "") is denied before anything is read.
	nsOnly := resources.NewPredicateAccessChecker(func(_, _, _, namespace string) bool { return namespace != "" })
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ClusterSecretStore", "", "vault", "uid-css")},
		[]corev1.Event{esoEvent("default", "ev-css", "uid-css", evidenceNow)},
		nsOnly)

	w := getEvidence(f.h, "clustersecretstores", "_", "vault", evidenceUser)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403\nbody: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "EVENT_MSG_MARKER") || len(eventListCalls(f.kube)) != 0 {
		t.Error("a namespace-only grant reached cluster-scoped events")
	}
}

func TestEvidenceEvents_ClusterScopedEventsForbidden_NamesClusterWideGrant(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ClusterSecretStore", "", "vault", "uid-css")},
		nil, resources.NewAlwaysAllowAccessChecker())
	f.kube.PrependReactor("list", "events", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("denied"))
	})

	w := getEvidence(f.h, "clustersecretstores", "_", "vault", evidenceUser)
	assertErrorReason(t, w, http.StatusForbidden, "events_forbidden")
	if !strings.Contains(w.Body.String(), "cluster-wide") {
		t.Errorf("forbidden response must name the cluster-wide grant: %s", w.Body.String())
	}
}

func TestEvidenceEvents_ScopeMismatch_Returns400(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	for _, tc := range []struct{ kind, ns string }{
		{"externalsecrets", "_"},        // namespaced kind addressed cluster-wide
		{"clustersecretstores", "apps"}, // cluster-scoped kind addressed in a namespace
	} {
		assertErrorReason(t, getEvidence(f.h, tc.kind, tc.ns, "x", evidenceUser),
			http.StatusBadRequest, "invalid_scope")
	}
}

// --- Input validation ----------------------------------------------------------

func TestEvidenceEvents_UnknownKind_Returns400(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	for _, kind := range []string{"secrets", "../", "ExternalSecrets", "externalsecrets/status", ""} {
		w := getEvidence(f.h, kind, "apps", "x", evidenceUser)
		assertErrorReason(t, w, http.StatusBadRequest, "unknown_evidence_kind")
	}
	if f.discoCalls.Load() != 0 {
		t.Error("an unknown kind reached discovery")
	}
}

func TestEvidenceEvents_InvalidNames_Return400(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	for _, tc := range []struct{ ns, name string }{
		{"apps", "Bad_Name"}, {"apps", ""}, {"Bad NS", "x"}, {"", "x"},
	} {
		w := getEvidence(f.h, "externalsecrets", tc.ns, tc.name, evidenceUser)
		if w.Code != http.StatusBadRequest {
			t.Errorf("ns=%q name=%q: status = %d; want 400", tc.ns, tc.name, w.Code)
		}
	}
}

// --- AE4: projection ------------------------------------------------------------

func TestEvidenceEvents_ESOnlyReader_OmitsMessages(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		[]corev1.Event{esoEvent("apps", "ev-1", "uid-1", evidenceNow)},
		resources.NewPredicateAccessChecker(esOnly))

	w := getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser)
	b := decodeEvidence(t, w)
	if strings.Contains(w.Body.String(), "EVENT_MSG_MARKER") {
		t.Fatalf("outcome-only body leaks the event message\nbody: %s", w.Body.String())
	}
	if b.Data.Projection.Level != "outcome-only" {
		t.Errorf("projection level = %q; want outcome-only", b.Data.Projection.Level)
	}
	wantDropped := []string{"message", "messageTruncated"}
	if strings.Join(b.Data.Projection.DroppedFields, ",") != strings.Join(wantDropped, ",") {
		t.Errorf("droppedFields = %v; want %v", b.Data.Projection.DroppedFields, wantDropped)
	}
	if len(b.Data.Events) != 1 {
		t.Fatalf("events = %d; want 1", len(b.Data.Events))
	}
	ev := b.Data.Events[0]
	for _, k := range wantDropped {
		if _, present := ev[k]; present {
			t.Errorf("outcome-only event carries %q; it must be absent, not empty", k)
		}
	}
	for k, want := range map[string]string{
		"type": `"Warning"`, "reason": `"UpdateFailed"`, "count": `3`, "source": `"external-secrets"`,
	} {
		if string(ev[k]) != want {
			t.Errorf("%s = %s; want %s", k, ev[k], want)
		}
	}
}

func TestEvidenceEvents_ESPlusSecretReader_SanitizedMessage(t *testing.T) {
	ev := esoEvent("apps", "ev-1", "uid-1", evidenceNow)
	ev.Message = "fetch failed\x1b]0;owned\x07: " + strings.Repeat("x", 2000)
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		[]corev1.Event{ev}, resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if b.Data.Projection.Level != "full" || len(b.Data.Projection.DroppedFields) != 0 {
		t.Errorf("projection = %+v; want full with nothing dropped", b.Data.Projection)
	}
	var msg string
	if err := json.Unmarshal(b.Data.Events[0]["message"], &msg); err != nil {
		t.Fatalf("message: %v", err)
	}
	if strings.ContainsAny(msg, "\x1b\x07") {
		t.Errorf("message carries terminal control characters: %q", msg)
	}
	if !strings.HasPrefix(msg, "fetch failed]0;owned: xxx") || len(msg) > 1024 || !strings.HasSuffix(msg, "…") {
		t.Errorf("message = %q (%d bytes); want the sanitized text cut to 1024 bytes with an ellipsis", msg, len(msg))
	}
	if string(b.Data.Events[0]["messageTruncated"]) != "true" {
		t.Errorf("messageTruncated = %s; want true", b.Data.Events[0]["messageTruncated"])
	}
}

// --- Ordering and truncation ------------------------------------------------------

func TestEvidenceEvents_NewestFirst(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		[]corev1.Event{
			esoEvent("apps", "ev-old", "uid-1", evidenceNow.Add(-2*time.Hour)),
			esoEvent("apps", "ev-new", "uid-1", evidenceNow),
			esoEvent("apps", "ev-mid", "uid-1", evidenceNow.Add(-time.Hour)),
		},
		resources.NewAlwaysAllowAccessChecker())

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	var got []time.Time
	for _, e := range b.Data.Events {
		var ts time.Time
		if err := json.Unmarshal(e["lastTimestamp"], &ts); err != nil {
			t.Fatalf("lastTimestamp: %v", err)
		}
		got = append(got, ts)
	}
	want := []time.Time{evidenceNow, evidenceNow.Add(-time.Hour), evidenceNow.Add(-2 * time.Hour)}
	if len(got) != 3 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) || !got[2].Equal(want[2]) {
		t.Errorf("lastTimestamps = %v; want newest first %v", got, want)
	}
}

// The reactor never returns an empty Continue, so this exercises the
// evidenceEventsMaxPages cap itself: the walk must stop after exactly that
// many pages rather than following Continue forever.
func TestEvidenceEvents_ContinueTokenMarksTruncated(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())
	var limit int64
	f.kube.PrependReactor("list", "events", func(a clienttesting.Action) (bool, runtime.Object, error) {
		limit = a.(clienttesting.ListActionImpl).ListOptions.Limit
		list := &corev1.EventList{Items: []corev1.Event{esoEvent("apps", "ev-1", "uid-1", evidenceNow)}}
		list.Continue = "more"
		return true, list, nil
	})

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if !b.Data.Truncated {
		t.Error("truncated = false although a continue token remained after the page cap")
	}
	if limit != 200 {
		t.Errorf("List limit = %d; want 200", limit)
	}
	if got := len(eventListCalls(f.kube)); got != evidenceEventsMaxPages {
		t.Errorf("List calls = %d; want evidenceEventsMaxPages (%d) — the walk must stop at the page cap", got, evidenceEventsMaxPages)
	}
}

// TestEvidenceEvents_MultiPage_ReturnsNewestAcrossPages is R-3's real-world
// case: an object with more than evidenceEventsListLimit matching events,
// where the API server serves them oldest-first across several pages. The
// response must hold the newest evidenceEventsListLimit, not the oldest.
func TestEvidenceEvents_MultiPage_ReturnsNewestAcrossPages(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())

	base := evidenceNow.Add(-10 * time.Hour)
	pages := [][]corev1.Event{
		makeEventPage("apps", "uid-1", base, 200, 0),   // oldest 200
		makeEventPage("apps", "uid-1", base, 200, 200), // next 200, newer
		makeEventPage("apps", "uid-1", base, 50, 400),  // newest 50
	}
	continues := []string{"", "p1", "p2"}
	callN := 0
	f.kube.PrependReactor("list", "events", func(a clienttesting.Action) (bool, runtime.Object, error) {
		got := a.(clienttesting.ListActionImpl).ListOptions.Continue
		if callN >= len(continues) {
			t.Fatalf("unexpected extra List call (continue=%q); page cap must stop the walk", got)
		}
		if got != continues[callN] {
			t.Fatalf("call %d: continue = %q; want %q", callN, got, continues[callN])
		}
		list := &corev1.EventList{Items: pages[callN]}
		if callN < len(pages)-1 {
			list.Continue = continues[callN+1]
		}
		callN++
		return true, list, nil
	})

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if !b.Data.Truncated {
		t.Error("truncated = false; want true — 450 matching events cut to 200")
	}
	if len(b.Data.Events) != evidenceEventsListLimit {
		t.Fatalf("events = %d; want evidenceEventsListLimit (%d)", len(b.Data.Events), evidenceEventsListLimit)
	}
	var newest time.Time
	if err := json.Unmarshal(b.Data.Events[0]["lastTimestamp"], &newest); err != nil {
		t.Fatalf("lastTimestamp: %v", err)
	}
	wantNewest := base.Add(449 * time.Minute).UTC()
	if !newest.Equal(wantNewest) {
		t.Errorf("first event lastTimestamp = %v; want the globally newest %v", newest, wantNewest)
	}
}

// TestEvidenceEvents_AllFitAcrossTwoPages is the non-truncating case: every
// matching event across the paged walk fits under evidenceEventsListLimit,
// so nothing is cut and truncated stays false.
func TestEvidenceEvents_AllFitAcrossTwoPages(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())

	base := evidenceNow.Add(-2 * time.Hour)
	pages := [][]corev1.Event{
		makeEventPage("apps", "uid-1", base, 100, 0),
		makeEventPage("apps", "uid-1", base, 50, 100),
	}
	callN := 0
	f.kube.PrependReactor("list", "events", func(a clienttesting.Action) (bool, runtime.Object, error) {
		got := a.(clienttesting.ListActionImpl).ListOptions.Continue
		wantContinue := ""
		if callN == 1 {
			wantContinue = "p2"
		}
		if got != wantContinue {
			t.Fatalf("call %d: continue = %q; want %q", callN, got, wantContinue)
		}
		list := &corev1.EventList{Items: pages[callN]}
		if callN == 0 {
			list.Continue = "p2"
		}
		callN++
		return true, list, nil
	})

	b := decodeEvidence(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser))
	if b.Data.Truncated {
		t.Error("truncated = true; want false — all 150 matching events fit under the limit")
	}
	if len(b.Data.Events) != 150 {
		t.Errorf("events = %d; want all 150", len(b.Data.Events))
	}
}

// --- Gates ---------------------------------------------------------------------

func TestEvidenceEvents_RemoteCluster_Returns501(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		nil, resources.NewAlwaysAllowAccessChecker())
	router := chi.NewRouter()
	router.With(middleware.ClusterContext).
		Get("/externalsecrets/evidence/{kind}/{namespace}/{name}/events", f.h.HandleGetEvidenceEvents)

	u := &auth.User{KubernetesUsername: "alice", Roles: []string{"admin"}}
	r := withUser(httptest.NewRequest(http.MethodGet,
		"/externalsecrets/evidence/externalsecrets/apps/db-creds/events", nil), u)
	r.Header.Set("X-Cluster-ID", "prod")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	assertErrorReason(t, w, http.StatusNotImplemented, "remote_events_unsupported")
	if len(eventListCalls(f.kube)) != 0 {
		t.Error("events listed for a remote cluster")
	}
}

func TestEvidenceEvents_ESONotDetected_Returns503(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	f.h.Discoverer = undetectedDiscoverer()
	assertErrorReason(t, getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser),
		http.StatusServiceUnavailable, "eso_not_detected")
}

func TestEvidenceEvents_Unauthenticated_Returns401(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	if w := getEvidence(f.h, "externalsecrets", "apps", "db-creds", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestEvidenceEvents_ObjectForbiddenOrMissing(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil,
		resources.NewPredicateAccessChecker(func(_, _, resource, _ string) bool { return resource != "secretstores" }))
	if w := getEvidence(f.h, "secretstores", "apps", "vault", evidenceUser); w.Code != http.StatusForbidden {
		t.Errorf("no get grant: status = %d; want 403", w.Code)
	}
	if w := getEvidence(f.h, "externalsecrets", "apps", "missing", evidenceUser); w.Code != http.StatusNotFound {
		t.Errorf("missing object: status = %d; want 404", w.Code)
	}
	if len(eventListCalls(f.kube)) != 0 {
		t.Error("events listed without a readable live object")
	}
}

func TestEvidenceEvents_ContextCancelled(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(),
		[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
		[]corev1.Event{esoEvent("apps", "ev-1", "uid-1", evidenceNow)},
		resources.NewAlwaysAllowAccessChecker())
	f.kube.PrependReactor("list", "events", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.Canceled
	})
	// Warm the discovery cache so the cancellation reaches the events List;
	// on a cold cache the caller would stop waiting on discovery first.
	if _, err := f.h.resolveESOGVR(context.Background(), "externalsecrets"); err != nil {
		t.Fatalf("warm discovery cache: %v", err)
	}
	r := evidenceRequest("externalsecrets", "apps", "db-creds", evidenceUser)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	f.h.HandleGetEvidenceEvents(w, r.WithContext(ctx))

	// A cancelled events List is a failed read: one well-formed 500 envelope,
	// never a 200 page or an empty list.
	assertErrorReason(t, w, http.StatusInternalServerError, "")
	if strings.Contains(w.Body.String(), "EVENT_MSG_MARKER") {
		t.Error("cancelled request leaked event content")
	}
}

// Every 500 branch before the events List must stop there: an evidence page
// is never served for an object whose identity or client could not be
// established, and events are never listed in that case. Mirrors
// TestHistory_InternalErrors_Return500WithoutQuerying in history_handler_test.go.
func TestEvidenceEvents_InternalErrors_Return500WithoutListingEvents(t *testing.T) {
	cases := map[string]func(f *evidenceFixture){
		"dynForUser fails": func(f *evidenceFixture) {
			f.h.dynForUserOverride = func(string, []string) (dynamic.Interface, error) {
				return nil, errors.New("no rest config")
			}
		},
		"live get fails": func(f *evidenceFixture) {
			dyn := evidenceDynClient(allKindsAtV1(), esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1"))
			dyn.PrependReactor("get", "externalsecrets", func(clienttesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("etcd timeout")
			})
			f.h.dynForUserOverride = func(string, []string) (dynamic.Interface, error) { return dyn, nil }
		},
		"object has no uid": func(f *evidenceFixture) {
			dyn := evidenceDynClient(allKindsAtV1(), esoObject("v1", "ExternalSecret", "apps", "db-creds", ""))
			f.h.dynForUserOverride = func(string, []string) (dynamic.Interface, error) { return dyn, nil }
		},
		"clientForUser fails": func(f *evidenceFixture) {
			f.h.clientForUserOverride = func(string, []string) (kubernetes.Interface, error) {
				return nil, errors.New("impersonation denied")
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newEvidenceFixture(allKindsAtV1(),
				[]runtime.Object{esoObject("v1", "ExternalSecret", "apps", "db-creds", "uid-1")},
				nil, resources.NewAlwaysAllowAccessChecker())
			breakIt(f)

			w := getEvidence(f.h, "externalsecrets", "apps", "db-creds", evidenceUser)
			assertErrorReason(t, w, http.StatusInternalServerError, "")
			if len(eventListCalls(f.kube)) != 0 {
				t.Error("events listed after the object or client could not be resolved")
			}
			for _, leak := range []string{"no rest config", "etcd timeout", "impersonation denied"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Errorf("internal error text %q reached the client", leak)
				}
			}
		})
	}
}

// --- Discovery cache ----------------------------------------------------------------

func TestResolveESOGVR_CachesAndExpires(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	now := evidenceNow
	f.h.evidenceGVRs.now = func() time.Time { return now }
	ctx := context.Background()

	res, err := f.h.resolveESOGVR(ctx, "clustersecretstores")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.gvr != (schema.GroupVersionResource{Group: GroupName, Version: "v1", Resource: "clustersecretstores"}) || res.namespaced {
		t.Errorf("resolved %+v; want cluster-scoped v1 clustersecretstores", res)
	}
	first := f.discoCalls.Load()
	if first == 0 {
		t.Fatal("first resolve made no discovery call")
	}

	now = now.Add(staleDuration - time.Second)
	if _, err := f.h.resolveESOGVR(ctx, "externalsecrets"); err != nil {
		t.Fatalf("resolve within TTL: %v", err)
	}
	if got := f.discoCalls.Load(); got != first {
		t.Errorf("discovery calls within TTL = %d; want %d (cached)", got, first)
	}

	now = now.Add(2 * time.Second)
	if _, err := f.h.resolveESOGVR(ctx, "externalsecrets"); err != nil {
		t.Fatalf("resolve after TTL: %v", err)
	}
	if got := f.discoCalls.Load(); got == first {
		t.Error("discovery was not re-walked after the TTL expired")
	}
}

func TestResolveESOGVR_FailureIsNotCached(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())
	var fail atomic.Bool
	fail.Store(true)
	fake := &clienttesting.Fake{Resources: allKindsAtV1()}
	fake.AddReactor("get", "group", func(clienttesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, errors.New("transient")
		}
		return false, nil, nil
	})
	f.h.Discoverer.discoOverride = func() discovery.DiscoveryInterface { return &fakediscovery.FakeDiscovery{Fake: fake} }

	if _, err := f.h.resolveESOGVR(context.Background(), "externalsecrets"); err == nil {
		t.Fatal("resolve succeeded against failing discovery")
	}
	fail.Store(false)
	if _, err := f.h.resolveESOGVR(context.Background(), "externalsecrets"); err != nil {
		t.Errorf("a transient failure was cached: %v", err)
	}
}

// TestResolveESOGVR_ConcurrentColdLookupsCoalesce guards the singleflight
// wiring: a slow discovery walk must not be re-run once per waiter. The
// discovery reactor blocks until this test has given every goroutine a
// scheduling window to pile onto the same in-flight walk, so the assertion
// that exactly one walk happened is meaningful rather than a race.
func TestResolveESOGVR_ConcurrentColdLookupsCoalesce(t *testing.T) {
	f := newEvidenceFixture(allKindsAtV1(), nil, nil, resources.NewAlwaysAllowAccessChecker())

	release := make(chan struct{})
	var entered atomic.Int32
	fake := &clienttesting.Fake{Resources: allKindsAtV1()}
	fake.AddReactor("get", "group", func(clienttesting.Action) (bool, runtime.Object, error) {
		entered.Add(1)
		<-release
		return false, nil, nil
	})
	f.h.Discoverer.discoOverride = func() discovery.DiscoveryInterface { return &fakediscovery.FakeDiscovery{Fake: fake} }

	const n = 10
	var wg sync.WaitGroup
	var started atomic.Int32
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Barrier: don't call resolveESOGVR until all n goroutines have
			// been scheduled, so the calls arrive close together.
			started.Add(1)
			for started.Load() < n {
				goruntime.Gosched()
			}
			_, err := f.h.resolveESOGVR(context.Background(), "externalsecrets")
			errs[i] = err
		}(i)
	}

	// Wait for the walk to start, then give the remaining goroutines a
	// scheduling window to join the same singleflight call before it's
	// allowed to complete.
	for entered.Load() == 0 {
		goruntime.Gosched()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: resolve failed: %v", i, err)
		}
	}
	// entered is this test's own discovery reactor's call count — the
	// fixture's f.discoCalls is wired to the original fixture-level fake and
	// is not incremented once discoOverride is replaced above.
	if got := entered.Load(); got != 1 {
		t.Errorf("discovery reactor entered %d times across %d concurrent cold lookups; want exactly 1 (singleflight must coalesce)", got, n)
	}
}
