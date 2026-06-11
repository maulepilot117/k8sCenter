package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kubecenter/kubecenter/internal/k8s/resources"
)

// promResponse builds a minimal Prometheus instant-query JSON response for a
// vector result. Each entry in samples is a [jobLabel, value] pair.
func promVectorResponse(samples [][]string) string {
	result := `{"status":"success","data":{"resultType":"vector","result":[`
	for i, s := range samples {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf(`{"metric":{"job":%q},"value":[1234567890,%q]}`, s[0], s[1])
	}
	result += `]}}`
	return result
}

// newTestAdapter creates a ControlPlaneAdapter backed by a mock Prometheus server
// that returns the given raw JSON body.
func newTestAdapter(t *testing.T, responseBody string) *ControlPlaneAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, responseBody)
	}))
	t.Cleanup(srv.Close)

	pc, err := NewPrometheusClientWithTransport(srv.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("creating test prometheus client: %v", err)
	}
	d := &Discoverer{
		status:     &MonitoringStatus{},
		promClient: pc,
	}
	return &ControlPlaneAdapter{Discoverer: d}
}

// TestControlPlane_AllUp verifies that when all three jobs report value 1,
// all components are mapped to ComponentUp.
func TestControlPlane_AllUp(t *testing.T) {
	body := promVectorResponse([][]string{
		{jobScheduler, "1"},
		{jobControllerManager, "1"},
		{jobEtcd, "1"},
	})
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SchedulerState != resources.ComponentUp {
		t.Errorf("scheduler: want Up, got %v", got.SchedulerState)
	}
	if got.ControllerManagerState != resources.ComponentUp {
		t.Errorf("controller-manager: want Up, got %v", got.ControllerManagerState)
	}
	if got.EtcdState != resources.ComponentUp {
		t.Errorf("etcd: want Up, got %v", got.EtcdState)
	}
}

// TestControlPlane_JobDown verifies that a job reporting value 0 maps to ComponentDown.
func TestControlPlane_JobDown(t *testing.T) {
	body := promVectorResponse([][]string{
		{jobScheduler, "0"},
		{jobControllerManager, "1"},
		{jobEtcd, "1"},
	})
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SchedulerState != resources.ComponentDown {
		t.Errorf("scheduler: want Down, got %v", got.SchedulerState)
	}
	if got.ControllerManagerState != resources.ComponentUp {
		t.Errorf("controller-manager: want Up, got %v", got.ControllerManagerState)
	}
	if got.EtcdState != resources.ComponentUp {
		t.Errorf("etcd: want Up, got %v", got.EtcdState)
	}
}

// TestControlPlane_MissingJob verifies that a job absent from the result vector
// maps to ComponentUnscraped.
func TestControlPlane_MissingJob(t *testing.T) {
	// Only scheduler in the response; controller-manager and etcd are absent.
	body := promVectorResponse([][]string{
		{jobScheduler, "1"},
	})
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SchedulerState != resources.ComponentUp {
		t.Errorf("scheduler: want Up, got %v", got.SchedulerState)
	}
	if got.ControllerManagerState != resources.ComponentUnscraped {
		t.Errorf("controller-manager: want Unscraped, got %v", got.ControllerManagerState)
	}
	if got.EtcdState != resources.ComponentUnscraped {
		t.Errorf("etcd: want Unscraped, got %v", got.EtcdState)
	}
}

// TestControlPlane_EmptyVector verifies the k3s case: empty result vector →
// all components not-scraped (no error).
func TestControlPlane_EmptyVector(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"vector","result":[]}}`
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SchedulerState != resources.ComponentUnscraped {
		t.Errorf("scheduler: want Unscraped, got %v", got.SchedulerState)
	}
	if got.ControllerManagerState != resources.ComponentUnscraped {
		t.Errorf("controller-manager: want Unscraped, got %v", got.ControllerManagerState)
	}
	if got.EtcdState != resources.ComponentUnscraped {
		t.Errorf("etcd: want Unscraped, got %v", got.EtcdState)
	}
}

// TestControlPlane_NilClient verifies that a nil Prometheus client returns an error.
func TestControlPlane_NilClient(t *testing.T) {
	d := &Discoverer{
		status:     &MonitoringStatus{},
		promClient: nil, // explicitly nil
	}
	a := &ControlPlaneAdapter{Discoverer: d}

	_, err := a.ControlPlaneStatus(context.Background())
	if err == nil {
		t.Fatal("expected error when prometheus client is nil, got nil")
	}
}

// TestControlPlane_QueryError verifies that a Prometheus query error propagates.
func TestControlPlane_QueryError(t *testing.T) {
	// Server returns a non-200 status to trigger a client error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	pc, err := NewPrometheusClientWithTransport(srv.URL, http.DefaultTransport)
	if err != nil {
		t.Fatalf("creating prometheus client: %v", err)
	}
	d := &Discoverer{
		status:     &MonitoringStatus{},
		promClient: pc,
	}
	a := &ControlPlaneAdapter{Discoverer: d}

	_, err = a.ControlPlaneStatus(context.Background())
	if err == nil {
		t.Fatal("expected error from query failure, got nil")
	}
}

// TestControlPlane_EtcdKubeEtcd verifies that etcd reported under the alternate
// "kube-etcd" job name is recognized as ComponentUp.
func TestControlPlane_EtcdKubeEtcd(t *testing.T) {
	body := promVectorResponse([][]string{
		{jobScheduler, "1"},
		{jobControllerManager, "1"},
		{jobEtcdAlt, "1"}, // "kube-etcd" instead of "etcd"
	})
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.EtcdState != resources.ComponentUp {
		t.Errorf("etcd via kube-etcd: want Up, got %v", got.EtcdState)
	}
}

// TestControlPlane_EtcdKubeEtcdDown verifies that etcd reported under "kube-etcd"
// with value 0 is recognized as ComponentDown.
func TestControlPlane_EtcdKubeEtcdDown(t *testing.T) {
	body := promVectorResponse([][]string{
		{jobEtcdAlt, "0"},
	})
	a := newTestAdapter(t, body)

	got, err := a.ControlPlaneStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.EtcdState != resources.ComponentDown {
		t.Errorf("etcd via kube-etcd down: want Down, got %v", got.EtcdState)
	}
}

// TestResolveEtcdState_BothPresent verifies that when both "etcd" and "kube-etcd"
// job names appear in the result (unusual but possible), ComponentDown wins.
func TestResolveEtcdState_BothPresent(t *testing.T) {
	// Both present: one up, one down — down should win.
	states := map[string]resources.ComponentState{
		jobEtcd:    resources.ComponentUp,
		jobEtcdAlt: resources.ComponentDown,
	}
	got := resolveEtcdState(states)
	if got != resources.ComponentDown {
		t.Errorf("both etcd names present (up+down): want Down, got %v", got)
	}

	// Both up → up.
	states2 := map[string]resources.ComponentState{
		jobEtcd:    resources.ComponentUp,
		jobEtcdAlt: resources.ComponentUp,
	}
	got2 := resolveEtcdState(states2)
	if got2 != resources.ComponentUp {
		t.Errorf("both etcd names present (up+up): want Up, got %v", got2)
	}
}
