package alerting

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/config"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/websocket"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const maxWebhookBody = 1 << 20 // 1MB

// Handler serves alerting HTTP endpoints.
type Handler struct {
	Store        Store
	Notifier     *Notifier
	Rules        *RulesManager
	Hub          *websocket.Hub
	AuditLogger  audit.Logger
	NotifService *notifications.NotificationService
	Logger       *slog.Logger
	ClusterID    string
	WebhookToken string
	enabled      atomic.Bool

	configMu sync.RWMutex
	config   config.AlertingConfig
}

// SetEnabled sets the alerting enabled state (thread-safe).
func (h *Handler) SetEnabled(v bool) { h.enabled.Store(v) }

// SetConfig sets the initial alerting config.
func (h *Handler) SetConfig(cfg config.AlertingConfig) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	h.config = cfg
}

// HandleWebhook receives Alertmanager webhook payloads.
// POST /api/v1/alerts/webhook
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.enabled.Load() {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"alerting is disabled", "")
		return
	}

	// Reject if no webhook token is configured (prevents empty-token bypass)
	if h.WebhookToken == "" {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"webhook token not configured", "")
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
				"alert missing fingerprint", "alert index: "+strconv.Itoa(i))
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

		if h.NotifService != nil {
			sev := notifications.SeverityWarning
			if s, ok := action.Alert.Labels["severity"]; ok {
				switch s {
				case "critical":
					sev = notifications.SeverityCritical
				case "warning":
					sev = notifications.SeverityWarning
				default:
					sev = notifications.SeverityInfo
				}
			}
			title := "Alert " + action.Type + ": " + action.Alert.Labels["alertname"]
			h.NotifService.Emit(r.Context(), notifications.Notification{
				Source:       notifications.SourceAlert,
				Severity:     sev,
				Title:        title,
				Message:      action.Alert.Annotations["description"],
				ResourceKind: action.Alert.Labels["kind"],
				ResourceNS:   action.Alert.Labels["namespace"],
				ResourceName: action.Alert.Labels["name"],
			})
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
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = n
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
		writeK8sError(w, err, "list alert rules")
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
		writeK8sError(w, err, "get alert rule")
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
		writeK8sError(w, err, "create alert rule")
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
		writeK8sError(w, err, "update alert rule")
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
		writeK8sError(w, err, "delete alert rule")
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

	h.configMu.RLock()
	cfg := h.config
	h.configMu.RUnlock()

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
		"enabled":       cfg.Enabled,
		"webhookToken":  maskedToken,
		"retentionDays": cfg.RetentionDays,
		"rateLimit":     cfg.RateLimit,
		"recipients":    cfg.Recipients,
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
		Recipients    []string `json:"recipients"`
		RateLimit     int      `json:"rateLimit"`
		RetentionDays int      `json:"retentionDays"`
		Enabled       bool     `json:"enabled"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebhookBody)).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	h.configMu.Lock()
	// Preserve existing password if empty in request
	if req.SMTP.Password == "" {
		req.SMTP.Password = h.config.SMTP.Password
	}

	h.config.Enabled = req.Enabled
	h.config.SMTP.Host = req.SMTP.Host
	if req.SMTP.Port > 0 {
		h.config.SMTP.Port = req.SMTP.Port
	}
	h.config.SMTP.Username = req.SMTP.Username
	h.config.SMTP.Password = req.SMTP.Password
	h.config.SMTP.From = req.SMTP.From
	h.config.SMTP.TLSInsecure = req.SMTP.TLSInsecure
	if req.RateLimit > 0 {
		h.config.RateLimit = req.RateLimit
	}
	if req.RetentionDays > 0 {
		h.config.RetentionDays = req.RetentionDays
	}
	if len(req.Recipients) > 0 {
		h.config.Recipients = req.Recipients
	}
	h.enabled.Store(req.Enabled)
	// Capture config while lock is held for notifier update
	smtpCfg := h.config.SMTP
	smtpFrom := h.config.SMTP.From
	rcpts := h.config.Recipients
	h.configMu.Unlock()

	// Update notifier config (using captured snapshot, not h.config)
	if h.Notifier != nil {
		h.Notifier.UpdateConfig(smtpCfg, smtpFrom, rcpts)
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

// writeK8sError maps a Kubernetes API error to the appropriate HTTP status code.
func writeK8sError(w http.ResponseWriter, err error, action string) {
	switch {
	case apierrors.IsNotFound(err):
		httputil.WriteError(w, http.StatusNotFound, "resource not found", err.Error())
	case apierrors.IsForbidden(err):
		httputil.WriteError(w, http.StatusForbidden, "permission denied", err.Error())
	case apierrors.IsAlreadyExists(err):
		httputil.WriteError(w, http.StatusConflict, "resource already exists", err.Error())
	default:
		httputil.WriteError(w, http.StatusInternalServerError, "failed to "+action, err.Error())
	}
}
