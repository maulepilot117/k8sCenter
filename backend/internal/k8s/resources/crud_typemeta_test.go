package resources

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Typed client-go objects read from an informer cache or a typed clientset
// carry an empty TypeMeta. The detail page's YAML editor renders the GET
// response and sends it back to /yaml/apply, which rejects a document with
// no apiVersion or kind. So a single-object GET must serve both fields.

// servedTypeMeta decodes a 200 get response's apiVersion and kind.
func servedTypeMeta(t *testing.T, rr *httptest.ResponseRecorder) metav1.TypeMeta {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data metav1.TypeMeta `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode get: %v (%s)", err, rr.Body.String())
	}
	return resp.Data
}

func TestGetResource_LocalServesTypeMeta(t *testing.T) {
	cases := []struct {
		name       string
		obj        runtime.Object
		kind       string
		segs       []string
		apiVersion string
		wantKind   string
	}{
		{"namespaced apps group", crudDeployment("default", "web", nil), "deployments",
			[]string{"default", "web"}, "apps/v1", "Deployment"},
		{"namespaced core group", &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}, "pods",
			[]string{"default", "p"}, "v1", "Pod"},
		{"cluster scoped", &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a"}}, "namespaces",
			[]string{"team-a"}, "v1", "Namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := testHandler(t, tc.obj)
			tm := servedTypeMeta(t, getResource(h, "", tc.kind, tc.segs...))
			if tm.APIVersion != tc.apiVersion || tm.Kind != tc.wantKind {
				t.Errorf("served apiVersion=%q kind=%q, want %q %q",
					tm.APIVersion, tm.Kind, tc.apiVersion, tc.wantKind)
			}
		})
	}
}

// Stamping the TypeMeta must not write into the shared informer cache: the
// cached object is read concurrently by every other request.
func TestGetResource_LocalDoesNotMutateCache(t *testing.T) {
	h, _ := testHandler(t, crudDeployment("default", "web", nil))
	servedTypeMeta(t, getResource(h, "", "deployments", "default", "web"))

	cached, err := h.Informers.Deployments().Deployments("default").Get("web")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if gvk := cached.GetObjectKind().GroupVersionKind(); !gvk.Empty() {
		t.Errorf("informer cache object was mutated: GVK = %v", gvk)
	}
}

func TestGetResource_RemoteServesTypeMeta(t *testing.T) {
	h, _, _ := remoteEventsHandler(t, nil,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}},
	)
	tm := servedTypeMeta(t, getResource(h, remoteTestClusterID, "deployments", "default", "web"))
	if tm.APIVersion != "apps/v1" || tm.Kind != "Deployment" {
		t.Errorf("served apiVersion=%q kind=%q, want apps/v1 Deployment", tm.APIVersion, tm.Kind)
	}
}
