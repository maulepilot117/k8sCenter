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
