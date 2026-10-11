package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/monitoring"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// The two "nothing to read" answers of ClusterMetricsStore.Get must reach the
// client as different reasons: an unregistered cluster (the store's wrapped
// pgx.ErrNoRows) is 404 cluster_unknown, a registered cluster without a
// binding (ErrMetricsBindingNotFound) is 404 metrics_not_configured. This
// drives the production chain: reader -> ClientResolver -> monitoring
// handler.
func TestMetricsBindingReader_ErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		storeErr   error
		wantNoRows bool
		wantNoBind bool
		wantReason k8s.ReasonCode
	}{
		{
			name:       "unregistered cluster",
			storeErr:   fmt.Errorf("getting metrics binding: cluster %s: %w", testRemoteID, pgx.ErrNoRows),
			wantNoRows: true,
			wantReason: k8s.ReasonClusterUnknown,
		},
		{
			name:       "registered cluster without a binding",
			storeErr:   store.ErrMetricsBindingNotFound,
			wantNoBind: true,
			wantReason: k8s.ReasonMetricsNotConfigured,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeMetricsStore()
			fs.getErr = c.storeErr
			reader := metricsBindingReader{store: fs}

			_, err := reader.GetMetricsBinding(t.Context(), testRemoteID)
			if got := errors.Is(err, pgx.ErrNoRows); got != c.wantNoRows {
				t.Errorf("errors.Is(err, pgx.ErrNoRows) = %v for %v; want %v", got, err, c.wantNoRows)
			}
			if got := errors.Is(err, monitoring.ErrNoMetricsBinding); got != c.wantNoBind {
				t.Errorf("errors.Is(err, ErrNoMetricsBinding) = %v for %v; want %v", got, err, c.wantNoBind)
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			h := &monitoring.Handler{
				Resolver: monitoring.NewClientResolver(nil, reader, logger),
				Logger:   logger,
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/query?query=up", nil)
			req = req.WithContext(middleware.WithClusterID(req.Context(), testRemoteID))
			rec := httptest.NewRecorder()
			h.HandleQuery(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body %s; want 404", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Reason string `json:"reason"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding %q: %v", rec.Body.String(), err)
			}
			if body.Error.Reason != string(c.wantReason) {
				t.Errorf("reason = %q; want %q", body.Error.Reason, c.wantReason)
			}
		})
	}
}

// The reader hands the resolver the Alertmanager token only as the store
// returned it (origin-checked), and carries the store's stale flag so the
// resolver can answer "token must be re-entered" without sending anything.
func TestMetricsBindingReader_AlertmanagerToken(t *testing.T) {
	const amURL = "https://8.8.4.4:9093/alertmanager"
	cases := []struct {
		name      string
		amOrigin  string
		wantToken string
		wantStale bool
	}{
		{"origin matches", "https://8.8.4.4:9093", "am-tok", false},
		{"origin moved", "https://1.1.1.1:9093", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeMetricsStore()
			fs.bindings[testRemoteID] = store.MetricsBinding{ClusterID: testRemoteID, PrometheusURL: testPublicProm, AlertmanagerURL: amURL}
			fs.tokens[testRemoteID] = "prom-tok"
			fs.amTokens[testRemoteID] = "am-tok"
			fs.amOrigins[testRemoteID] = c.amOrigin

			b, err := metricsBindingReader{store: fs}.GetMetricsBinding(t.Context(), testRemoteID)
			if err != nil {
				t.Fatalf("GetMetricsBinding: %v", err)
			}
			want := monitoring.MetricsBinding{
				ClusterID:              testRemoteID,
				PrometheusURL:          testPublicProm,
				AlertmanagerURL:        amURL,
				Token:                  "prom-tok",
				AlertmanagerToken:      c.wantToken,
				AlertmanagerTokenStale: c.wantStale,
			}
			if b != want {
				t.Errorf("binding = %+v; want %+v", b, want)
			}
		})
	}
}

// The reader passes the store's updated_at through as the binding revision,
// on both the full read and the revision-only read, and maps the revision
// read's errors the way GetMetricsBinding maps Get's.
func TestMetricsBindingReader_Revision(t *testing.T) {
	rev := time.Date(2026, 10, 11, 9, 30, 0, 123456000, time.UTC)
	fs := newFakeMetricsStore()
	fs.bindings[testRemoteID] = store.MetricsBinding{ClusterID: testRemoteID, PrometheusURL: testPublicProm, UpdatedAt: rev}
	reader := metricsBindingReader{store: fs}

	b, err := reader.GetMetricsBinding(t.Context(), testRemoteID)
	if err != nil || !b.Revision.Equal(rev) {
		t.Fatalf("GetMetricsBinding revision = (%v, %v); want %v", b.Revision, err, rev)
	}
	got, err := reader.MetricsBindingRevision(t.Context(), testRemoteID)
	if err != nil || !got.Equal(rev) {
		t.Fatalf("MetricsBindingRevision = (%v, %v); want %v", got, err, rev)
	}

	if _, err := reader.MetricsBindingRevision(t.Context(), "unbound"); !errors.Is(err, monitoring.ErrNoMetricsBinding) {
		t.Errorf("revision of an unbound cluster = %v; want ErrNoMetricsBinding", err)
	}
	fs.getErr = fmt.Errorf("getting metrics binding revision: cluster %s: %w", testRemoteID, pgx.ErrNoRows)
	_, err = reader.MetricsBindingRevision(t.Context(), testRemoteID)
	if !errors.Is(err, pgx.ErrNoRows) || errors.Is(err, monitoring.ErrNoMetricsBinding) {
		t.Errorf("revision of an unregistered cluster = %v; want pgx.ErrNoRows passed through", err)
	}
}
