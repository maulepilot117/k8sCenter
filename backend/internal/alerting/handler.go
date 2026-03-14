package alerting

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/websocket"
)

const maxWebhookBody = 1 << 20 // 1MB

// Handler serves alerting HTTP endpoints.
type Handler struct {
	Store        Store
	Notifier     *Notifier
	Rules        *RulesManager
	Hub          *websocket.Hub
	AuditLogger  audit.Logger
	Logger       *slog.Logger
	ClusterID    string
	WebhookToken string
	Enabled      bool

	ConfigMu sync.RWMutex
	Config   config.AlertingConfig
}

// HandleWebhook receives Alertmanager webhook payloads.
// POST /api/v1/alerts/webhook
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"alerting is disabled", "")
		return
	}

	// Verify bearer token
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "authorization required", "")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.WebhookToken)) != 1 {
		httputil.WriteError(w, http.StatusUnauthorized, "invalid webhook token", "")
		return
	}

	// Read and parse payload
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to read request body", "")
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON payload", err.Error())
		return
	}

	// Validate payload
	if len(payload.Alerts) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "no alerts in payload", "")
		return
	}
	for i, alert := range payload.Alerts {
		if alert.Fingerprint == "" {
			httputil.WriteError(w, http.StatusBadRequest,
				"alert missing fingerprint", "alert index: "+string(rune('0'+i)))
			return
		}
		if alert.Labels["alertname"] == "" {
			httputil.WriteError(w, http.StatusBadRequest,
				"alert missing alertname label", "")
			return
		}
	}

	// Process alerts
	actions, err := ProcessWebhook(r.Context(), h.Store, &payload, h.ClusterID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to process alerts", err.Error())
		return
	}

	// Broadcast to WebSocket and queue emails
	for _, action := range actions {
		eventType := "ADDED"
		if action.Type == "resolved" {
			eventType = "DELETED"
		}

		if h.Hub != nil {
			h.Hub.HandleEvent(
				eventType,
				"alerts",
				action.Alert.Labels["namespace"],
				action.Alert.Labels["alertname"],
				action.Event,
			)
		}

		if h.Notifier != nil {
			h.Notifier.QueueAlert(action)
		}
	}

	httputil.WriteData(w, map[string]int{"accepted": len(actions)})
}

// HandleListActive returns currently firing alerts.
// GET /api/v1/alerts
func (h *Handler) HandleListActive(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	alerts, err := h.Store.ActiveAlerts(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list active alerts", err.Error())
		return
	}

	httputil.WriteData(w, alerts)
}

// HandleListHistory returns paginated alert history.
// GET /api/v1/alerts/history
func (h *Handler) HandleListHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	q := r.URL.Query()
	opts := ListOptions{
		Namespace: q.Get("namespace"),
		AlertName: q.Get("alertName"),
		Severity:  q.Get("severity"),
		Status:    q.Get("status"),
		Continue:  q.Get("continue"),
	}

	if v := q.Get("limit"); v != "" {
		var limit int
		if _, err := parsePositiveInt(v); err == nil {
			limit = parseIntOrDefault(v, 50)
			opts.Limit = limit
		}
	}

	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Since = t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Until = t
		}
	}

	items, continueToken, err := h.Store.List(r.Context(), opts)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	result := map[string]any{
		"items": items,
	}
	if continueToken != "" {
		result["continue"] = continueToken
	}
	httputil.WriteData(w, result)
}

// HandleListRules lists PrometheusRule CRDs.
// GET /api/v1/alerts/rules
func (h *Handler) HandleListRules(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := r.URL.Query().Get("namespace")

	rules, err := h.Rules.List(r.Context(), user.KubernetesUsername, user.KubernetesGroups, namespace)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to list alert rules", err.Error())
		return
	}

	httputil.WriteData(w, rules)
}

// HandleGetRule returns a single PrometheusRule.
// GET /api/v1/alerts/rules/{namespace}/{name}
func (h *Handler) HandleGetRule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	rule, err := h.Rules.Get(r.Context(), user.KubernetesUsername, user.KubernetesGroups, namespace, name)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to get alert rule", err.Error())
		return
	}

	httputil.WriteData(w, rule)
}

// HandleCreateRule creates a new PrometheusRule.
// POST /api/v1/alerts/rules
func (h *Handler) HandleCreateRule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var body struct {
		Namespace string                 `json:"namespace"`
		Content   map[string]interface{} `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebhookBody)).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if body.Namespace == "" {
		httputil.WriteError(w, http.StatusBadRequest, "namespace is required", "")
		return
	}
	if body.Content == nil {
		httputil.WriteError(w, http.StatusBadRequest, "content is required", "")
		return
	}

	result, err := h.Rules.Create(r.Context(), user.KubernetesUsername, user.KubernetesGroups, body.Namespace, body.Content)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to create alert rule", err.Error())
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionAlertRuleCreate,
		ResourceKind:      "PrometheusRule",
		ResourceNamespace: body.Namespace,
		ResourceName:      getName(body.Content),
		Result:            audit.ResultSuccess,
	})

	httputil.WriteJSON(w, http.StatusCreated, map[string]any{"data": result})
}

// HandleUpdateRule updates a PrometheusRule via server-side apply.
// PUT /api/v1/alerts/rules/{namespace}/{name}
func (h *Handler) HandleUpdateRule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	var content map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebhookBody)).Decode(&content); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	result, err := h.Rules.Update(r.Context(), user.KubernetesUsername, user.KubernetesGroups, namespace, name, content)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to update alert rule", err.Error())
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionAlertRuleUpdate,
		ResourceKind:      "PrometheusRule",
		ResourceNamespace: namespace,
		ResourceName:      name,
		Result:            audit.ResultSuccess,
	})

	httputil.WriteData(w, result)
}

// HandleDeleteRule deletes a PrometheusRule.
// DELETE /api/v1/alerts/rules/{namespace}/{name}
func (h *Handler) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if err := h.Rules.Delete(r.Context(), user.KubernetesUsername, user.KubernetesGroups, namespace, name); err != nil {
		if strings.Contains(err.Error(), "not managed by KubeCenter") {
			httputil.WriteError(w, http.StatusForbidden, err.Error(), "")
			return
		}
		httputil.WriteError(w, http.StatusBadGateway, "failed to delete alert rule", err.Error())
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now().UTC(),
		ClusterID:         h.ClusterID,
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            audit.ActionAlertRuleDelete,
		ResourceKind:      "PrometheusRule",
		ResourceNamespace: namespace,
		ResourceName:      name,
		Result:            audit.ResultSuccess,
	})

	httputil.WriteData(w, map[string]string{"status": "deleted"})
}

// HandleGetSettings returns the alerting configuration (SMTP password masked).
// GET /api/v1/alerts/settings
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := httputil.RequireUser(w, r); !ok {
		return
	}

	h.ConfigMu.RLock()
	cfg := h.Config
	h.ConfigMu.RUnlock()

	// Mask SMTP password
	maskedPassword := ""
	if cfg.SMTP.Password != "" {
		maskedPassword = "****"
	}

	maskedToken := ""
	if h.WebhookToken != "" {
		maskedToken = "****"
	}

	httputil.WriteData(w, map[string]any{
		"enabled":      cfg.Enabled,
		"webhookToken": maskedToken,
		"retentionDays": cfg.RetentionDays,
		"rateLimit":    cfg.RateLimit,
		"smtp": map[string]any{
			"host":        cfg.SMTP.Host,
			"port":        cfg.SMTP.Port,
			"username":    cfg.SMTP.Username,
			"password":    maskedPassword,
			"from":        cfg.SMTP.From,
			"tlsInsecure": cfg.SMTP.TLSInsecure,
		},
	})
}

// HandleUpdateSettings updates the alerting configuration in memory.
// PUT /api/v1/alerts/settings
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var req struct {
		SMTP struct {
			Host        string `json:"host"`
			Port        int    `json:"port"`
			Username    string `json:"username"`
			Password    string `json:"password"`
			From        string `json:"from"`
			TLSInsecure bool   `json:"tlsInsecure"`
		} `json:"smtp"`
		RateLimit     int  `json:"rateLimit"`
		RetentionDays int  `json:"retentionDays"`
		Enabled       bool `json:"enabled"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebhookBody)).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	h.ConfigMu.Lock()
	// Preserve existing password if empty in request
	if req.SMTP.Password == "" {
		req.SMTP.Password = h.Config.SMTP.Password
	}

	h.Config.Enabled = req.Enabled
	h.Config.SMTP.Host = req.SMTP.Host
	if req.SMTP.Port > 0 {
		h.Config.SMTP.Port = req.SMTP.Port
	}
	h.Config.SMTP.Username = req.SMTP.Username
	h.Config.SMTP.Password = req.SMTP.Password
	h.Config.SMTP.From = req.SMTP.From
	h.Config.SMTP.TLSInsecure = req.SMTP.TLSInsecure
	if req.RateLimit > 0 {
		h.Config.RateLimit = req.RateLimit
	}
	if req.RetentionDays > 0 {
		h.Config.RetentionDays = req.RetentionDays
	}
	h.Enabled = req.Enabled
	h.ConfigMu.Unlock()

	// Update notifier config
	if h.Notifier != nil {
		h.Notifier.UpdateConfig(h.Config.SMTP, h.Config.SMTP.From)
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now().UTC(),
		ClusterID: h.ClusterID,
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionAlertSettingsUpdate,
		Result:    audit.ResultSuccess,
		Detail:    "alerting settings updated",
	})

	h.Logger.Info("alerting settings updated", "user", user.Username)

	// Return updated settings (via the get handler)
	h.HandleGetSettings(w, r)
}

// HandleTestEmail sends a test email.
// POST /api/v1/alerts/test
func (h *Handler) HandleTestEmail(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if h.Notifier == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "email notifier is not configured", "")
		return
	}

	if err := h.Notifier.QueueTestEmail(); err != nil {
		h.AuditLogger.Log(r.Context(), audit.Entry{
			Timestamp: time.Now().UTC(),
			ClusterID: h.ClusterID,
			User:      user.Username,
			SourceIP:  r.RemoteAddr,
			Action:    audit.ActionAlertTest,
			Result:    audit.ResultFailure,
			Detail:    err.Error(),
		})
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp: time.Now().UTC(),
		ClusterID: h.ClusterID,
		User:      user.Username,
		SourceIP:  r.RemoteAddr,
		Action:    audit.ActionAlertTest,
		Result:    audit.ResultSuccess,
		Detail:    "test email queued",
	})

	httputil.WriteData(w, map[string]string{"status": "test email queued"})
}

// Helper functions

func getName(content map[string]interface{}) string {
	if meta, ok := content["metadata"].(map[string]interface{}); ok {
		if name, ok := meta["name"].(string); ok {
			return name
		}
	}
	return ""
}

func parsePositiveInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, io.ErrUnexpectedEOF
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func parseIntOrDefault(s string, def int) int {
	n, err := parsePositiveInt(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
