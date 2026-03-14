package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/config"
)

// --- Store Tests ---

func TestMemoryStore_RecordAndActive(t *testing.T) {
	s := NewMemoryStore()

	err := s.Record(context.Background(), AlertEvent{
		Fingerprint: "fp1",
		Status:      "firing",
		AlertName:   "TestAlert",
		Namespace:   "default",
		Severity:    "warning",
	})
	if err != nil {
		t.Fatal(err)
	}

	active, err := s.ActiveAlerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert, got %d", len(active))
	}
	if active[0].AlertName != "TestAlert" {
		t.Errorf("expected AlertName=TestAlert, got %s", active[0].AlertName)
	}
}

func TestMemoryStore_Resolve(t *testing.T) {
	s := NewMemoryStore()

	s.Record(context.Background(), AlertEvent{
		Fingerprint: "fp1",
		Status:      "firing",
		AlertName:   "TestAlert",
	})

	err := s.Resolve(context.Background(), "fp1", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	active, _ := s.ActiveAlerts(context.Background())
	if len(active) != 0 {
		t.Errorf("expected 0 active alerts after resolve, got %d", len(active))
	}

	// History should have both the firing and resolved events
	items, _, _ := s.List(context.Background(), ListOptions{Limit: 10})
	if len(items) != 2 {
		t.Errorf("expected 2 history items, got %d", len(items))
	}
}

func TestMemoryStore_ResolveNonExistent(t *testing.T) {
	s := NewMemoryStore()
	err := s.Resolve(context.Background(), "nonexistent", time.Now())
	if err != nil {
		t.Errorf("resolving non-existent alert should not error, got %v", err)
	}
}

func TestMemoryStore_HistoryCap(t *testing.T) {
	s := NewMemoryStore()

	for i := range maxHistoryEntries + 100 {
		s.Record(context.Background(), AlertEvent{
			Fingerprint: "fp",
			Status:      "firing",
			AlertName:   "Alert",
			Namespace:   string(rune('a' + i%26)),
		})
	}

	items, _, _ := s.List(context.Background(), ListOptions{Limit: maxHistoryEntries + 1})
	if len(items) > maxHistoryEntries {
		t.Errorf("history exceeded cap: got %d, max %d", len(items), maxHistoryEntries)
	}
}

func TestMemoryStore_ListFilters(t *testing.T) {
	s := NewMemoryStore()

	s.Record(context.Background(), AlertEvent{
		Fingerprint: "fp1", Status: "firing", AlertName: "HighCPU",
		Namespace: "prod", Severity: "critical",
	})
	s.Record(context.Background(), AlertEvent{
		Fingerprint: "fp2", Status: "firing", AlertName: "LowDisk",
		Namespace: "staging", Severity: "warning",
	})

	// Filter by namespace
	items, _, _ := s.List(context.Background(), ListOptions{Namespace: "prod"})
	if len(items) != 1 {
		t.Errorf("namespace filter: expected 1, got %d", len(items))
	}

	// Filter by severity
	items, _, _ = s.List(context.Background(), ListOptions{Severity: "warning"})
	if len(items) != 1 {
		t.Errorf("severity filter: expected 1, got %d", len(items))
	}
}

func TestMemoryStore_ListPagination(t *testing.T) {
	s := NewMemoryStore()

	for range 10 {
		s.Record(context.Background(), AlertEvent{
			Fingerprint: "fp", Status: "firing", AlertName: "Alert",
		})
		time.Sleep(time.Millisecond) // ensure different ReceivedAt
	}

	// Page 1
	items, token, _ := s.List(context.Background(), ListOptions{Limit: 3})
	if len(items) != 3 {
		t.Fatalf("page 1: expected 3 items, got %d", len(items))
	}
	if token == "" {
		t.Fatal("expected continue token for page 1")
	}

	// Page 2
	items2, _, _ := s.List(context.Background(), ListOptions{Limit: 3, Continue: token})
	if len(items2) != 3 {
		t.Fatalf("page 2: expected 3 items, got %d", len(items2))
	}
}

func TestMemoryStore_Prune(t *testing.T) {
	s := NewMemoryStore()

	// Add old events manually
	s.mu.Lock()
	oldTime := time.Now().AddDate(0, 0, -31)
	s.history = append(s.history, AlertEvent{
		ID: "old", Fingerprint: "fp", ReceivedAt: oldTime,
	})
	s.mu.Unlock()

	pruned, err := s.Prune(context.Background(), time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 pruned, got %d", pruned)
	}
}

// --- Webhook Processing Tests ---

func TestProcessWebhook_FiringAlert(t *testing.T) {
	store := NewMemoryStore()
	payload := &WebhookPayload{
		Version: "4",
		Status:  "firing",
		Alerts: []WebhookAlert{
			{
				Status:      "firing",
				Labels:      map[string]string{"alertname": "TestAlert", "severity": "critical"},
				Fingerprint: "fp1",
				StartsAt:    time.Now(),
			},
		},
	}

	actions, err := ProcessWebhook(context.Background(), store, payload, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Type != "new" {
		t.Errorf("expected action type=new, got %s", actions[0].Type)
	}

	active, _ := store.ActiveAlerts(context.Background())
	if len(active) != 1 {
		t.Errorf("expected 1 active alert, got %d", len(active))
	}
}

func TestProcessWebhook_ResolvedAlert(t *testing.T) {
	store := NewMemoryStore()

	// First fire it
	ProcessWebhook(context.Background(), store, &WebhookPayload{
		Alerts: []WebhookAlert{
			{Status: "firing", Labels: map[string]string{"alertname": "Test"}, Fingerprint: "fp1"},
		},
	}, "local")

	// Then resolve it
	actions, _ := ProcessWebhook(context.Background(), store, &WebhookPayload{
		Alerts: []WebhookAlert{
			{Status: "resolved", Labels: map[string]string{"alertname": "Test"}, Fingerprint: "fp1", EndsAt: time.Now()},
		},
	}, "local")

	if len(actions) != 1 || actions[0].Type != "resolved" {
		t.Errorf("expected 1 resolved action, got %v", actions)
	}

	active, _ := store.ActiveAlerts(context.Background())
	if len(active) != 0 {
		t.Errorf("expected 0 active alerts after resolve, got %d", len(active))
	}
}

// --- Handler Tests ---

func testHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{
		Store:        NewMemoryStore(),
		AuditLogger:  audit.NewSlogLogger(slog.Default()),
		Logger:       slog.Default(),
		ClusterID:    "test",
		WebhookToken: "test-token",
		Enabled:      true,
		Config: config.AlertingConfig{
			Enabled:       true,
			RetentionDays: 30,
			RateLimit:     120,
			SMTP:          config.SMTPConfig{Port: 587},
		},
	}
}

func TestHandleWebhook_ValidPayload(t *testing.T) {
	h := testHandler(t)

	payload := WebhookPayload{
		Version: "4",
		Status:  "firing",
		Alerts: []WebhookAlert{
			{
				Status:      "firing",
				Labels:      map[string]string{"alertname": "TestAlert", "severity": "warning"},
				Fingerprint: "abc123",
				StartsAt:    time.Now(),
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]any)
	if data["accepted"].(float64) != 1 {
		t.Errorf("expected accepted=1, got %v", data["accepted"])
	}
}

func TestHandleWebhook_InvalidToken(t *testing.T) {
	h := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"Test"},"fingerprint":"fp1"}]}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleWebhook_MissingToken(t *testing.T) {
	h := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleWebhook_Disabled(t *testing.T) {
	h := testHandler(t)
	h.Enabled = false

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleWebhook_MalformedJSON(t *testing.T) {
	h := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`not json`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleWebhook_EmptyAlerts(t *testing.T) {
	h := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`{"version":"4","alerts":[]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleWebhook_MissingFingerprint(t *testing.T) {
	h := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/webhook",
		strings.NewReader(`{"version":"4","alerts":[{"status":"firing","labels":{"alertname":"Test"}}]}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	h.HandleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGetSettings_MasksPassword(t *testing.T) {
	h := testHandler(t)
	h.Config.SMTP.Password = "secret-password"
	h.Config.SMTP.Host = "smtp.example.com"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/settings", nil)
	// Simulate authenticated user context
	ctx := setUserContext(req.Context())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.HandleGetSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]any)
	smtp := data["smtp"].(map[string]any)

	if smtp["password"] != "****" {
		t.Errorf("expected masked password, got %v", smtp["password"])
	}
	if smtp["host"] != "smtp.example.com" {
		t.Errorf("expected host=smtp.example.com, got %v", smtp["host"])
	}
}

// setUserContext adds a fake authenticated user to the request context.
func setUserContext(ctx context.Context) context.Context {
	return auth.ContextWithUser(ctx, &auth.User{
		Username:           "admin",
		KubernetesUsername: "admin",
	})
}
