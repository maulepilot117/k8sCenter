package monitoring

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertCountsAdapter_CountsBoundRemoteCluster(t *testing.T) {
	prom := newFakePrometheus(t, "1")
	am := newFakeAlertmanager(t, amList(
		amAlert("HighLatency", "critical", "active"),
		amAlert("DiskFull", "critical", "active"),
		amAlert("SlowQuery", "warning", "active"),
	))
	b := &fakeBindings{}
	b.set("remote-1", twoTokenBinding(prom, am))
	a := &AlertCountsAdapter{Resolver: newTestResolver(nil, b)}

	active, critical, err := a.RemoteAlertCounts(context.Background(), "remote-1")
	if err != nil {
		t.Fatalf("RemoteAlertCounts: %v", err)
	}
	if active != 3 || critical != 2 {
		t.Errorf("counts = %d active, %d critical; want 3, 2", active, critical)
	}
}

func TestAlertCountsAdapter_NotConfiguredIsDistinctFromNoBinding(t *testing.T) {
	prom := newFakePrometheus(t, "1")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: prom.srv.URL})
	a := &AlertCountsAdapter{Resolver: newTestResolver(nil, b)}

	_, _, err := a.RemoteAlertCounts(context.Background(), "remote-1")
	if !errors.Is(err, ErrNoAlertmanager) || errors.Is(err, ErrNoMetricsBinding) {
		t.Errorf("no Alertmanager URL: err = %v; want ErrNoAlertmanager only", err)
	}
	_, _, err = a.RemoteAlertCounts(context.Background(), "remote-2")
	if !errors.Is(err, ErrNoMetricsBinding) || errors.Is(err, ErrNoAlertmanager) {
		t.Errorf("no binding: err = %v; want ErrNoMetricsBinding only", err)
	}
	_, _, err = (&AlertCountsAdapter{}).RemoteAlertCounts(context.Background(), "remote-1")
	if !errors.Is(err, ErrNoMetricsBinding) {
		t.Errorf("nil resolver: err = %v; want ErrNoMetricsBinding", err)
	}
}

func TestAlertCountsAdapter_TokenReentryStaysDistinct(t *testing.T) {
	prom := newFakePrometheus(t, "1")
	am := newFakeAlertmanager(t, "[]")
	b := &fakeBindings{}
	b.set("remote-1", MetricsBinding{
		ClusterID:              "remote-1",
		PrometheusURL:          prom.srv.URL,
		AlertmanagerURL:        am.srv.URL,
		AlertmanagerTokenStale: true,
	})
	a := &AlertCountsAdapter{Resolver: newTestResolver(nil, b)}

	_, _, err := a.RemoteAlertCounts(context.Background(), "remote-1")
	if !errors.Is(err, ErrAlertmanagerTokenReentry) || errors.Is(err, ErrNoAlertmanager) {
		t.Fatalf("err = %v; want ErrAlertmanagerTokenReentry only", err)
	}
	if am.hits.Load() != 0 {
		t.Error("a request reached the Alertmanager although its token must be re-entered")
	}
}

// Every Alertmanager failure class passes through classifiable, and no error
// text carries the upstream body.
func TestAlertCountsAdapter_ErrorClassesPassThroughWithoutBody(t *testing.T) {
	const secret = "SECRET-UPSTREAM-BODY"
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    error
	}{
		{"refused", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(secret))
		}, ErrAlertmanagerRefused},
		{"redirected", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.invalid/"+secret, http.StatusFound)
		}, ErrAlertmanagerRedirected},
		{"unreachable", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(secret))
		}, ErrAlertmanagerUnreachable},
		{"unreadable", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(secret))
		}, ErrAlertmanagerUnreadable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prom := newFakePrometheus(t, "1")
			srv := httptest.NewTLSServer(tc.handler)
			t.Cleanup(srv.Close)
			b := &fakeBindings{}
			b.set("remote-1", MetricsBinding{ClusterID: "remote-1", PrometheusURL: prom.srv.URL, AlertmanagerURL: srv.URL})
			a := &AlertCountsAdapter{Resolver: newTestResolver(nil, b)}

			active, critical, err := a.RemoteAlertCounts(context.Background(), "remote-1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v; want %v", err, tc.want)
			}
			if active != 0 || critical != 0 {
				t.Errorf("a failure returned a partial count %d/%d", active, critical)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error carries upstream body: %v", err)
			}
		})
	}
}
