package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// maxRequestBody limits JSON request body size to 64KB.
const maxRequestBody = 64 * 1024

// Handler handles HTTP requests for the notification center.
// The handler accesses the store directly via h.Service.store because both
// handler and service are in the same package. The service layer owns Emit,
// dispatch, and caching logic; the handler owns HTTP concerns and RBAC.
type Handler struct {
	Service     *NotificationService
	RBACChecker *auth.RBACChecker
	AuditLogger audit.Logger
	Logger      *slog.Logger
}

// --- Feed endpoints (all authenticated users) ---

// HandleList returns paginated, RBAC-filtered notifications for the current user.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	namespaces, err := h.accessibleNamespaces(r, user)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to check RBAC", "")
		return
	}

	opts := ListOpts{
		UserID:     user.KubernetesUsername,
		Namespaces: namespaces,
		Source:     Source(r.URL.Query().Get("source")),
		Severity:   Severity(r.URL.Query().Get("severity")),
		ReadFilter: r.URL.Query().Get("read"),
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if v, err := strconv.Atoi(limit); err == nil {
			opts.Limit = v
		}
	}
	if offset := r.URL.Query().Get("offset"); offset != "" {
		if v, err := strconv.Atoi(offset); err == nil {
			opts.Offset = v
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			opts.Until = t
		}
	}

	notifications, total, err := h.Service.store.ListNotifications(r.Context(), opts)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list notifications", "")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data":     notifications,
		"metadata": map[string]any{"total": total},
	})
}

// HandleUnreadCount returns the unread notification count for the current user.
func (h *Handler) HandleUnreadCount(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	namespaces, err := h.accessibleNamespaces(r, user)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to check RBAC", "")
		return
	}

	count, err := h.Service.store.UnreadCount(r.Context(), user.KubernetesUsername, namespaces)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to count unread", "")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data": map[string]int{"count": count},
	})
}

// HandleMarkRead marks a single notification as read.
func (h *Handler) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		httputil.WriteError(w, http.StatusBadRequest, "notification id required", "")
		return
	}

	if err := h.Service.store.MarkRead(r.Context(), user.KubernetesUsername, id); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to mark read", "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleMarkAllRead marks all notifications as read for the current user.
func (h *Handler) HandleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	if err := h.Service.store.MarkAllRead(r.Context(), user.KubernetesUsername); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to mark all read", "")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Channel management (admin only) ---

// HandleListChannels returns all notification channels with masked config.
func (h *Handler) HandleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.Service.store.ListChannels(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list channels", "")
		return
	}

	for i := range channels {
		channels[i].Config = maskConfig(channels[i].Config)
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": channels})
}

// HandleCreateChannel creates a new notification channel.
func (h *Handler) HandleCreateChannel(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	var ch Channel
	if err := decodeJSON(r, &ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if ch.Name == "" || ch.Type == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and type are required", "")
		return
	}
	if !isValidChannelType(ch.Type) {
		httputil.WriteError(w, http.StatusBadRequest, "type must be slack, email, or webhook", "")
		return
	}
	if err := validateChannelURLs(ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	ch.CreatedBy = user.KubernetesUsername
	id, err := h.Service.store.CreateChannel(r.Context(), ch)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create channel", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionCreate, "notification-channel", ch.Name)

	httputil.WriteJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
}

// HandleUpdateChannel updates a notification channel.
func (h *Handler) HandleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")

	// Fetch existing channel to validate URL against its type (not the request body type)
	existing, err := h.Service.store.GetChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "channel not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get channel", "")
		return
	}

	var ch Channel
	if err := decodeJSON(r, &ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	// Use existing type for SSRF validation if not provided in request
	if ch.Type == "" {
		ch.Type = existing.Type
	}
	if err := validateChannelURLs(ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	ch.ID = id
	ch.UpdatedBy = user.KubernetesUsername
	if err := h.Service.store.UpdateChannel(r.Context(), ch); err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "channel not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update channel", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionUpdate, "notification-channel", ch.Name)

	w.WriteHeader(http.StatusNoContent)
}

// HandleDeleteChannel deletes a notification channel (cascades to rules).
func (h *Handler) HandleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")
	if err := h.Service.store.DeleteChannel(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "channel not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete channel", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionDelete, "notification-channel", id)

	w.WriteHeader(http.StatusNoContent)
}

// HandleTestChannel sends a test notification to a channel.
func (h *Handler) HandleTestChannel(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")
	ch, err := h.Service.store.GetChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "channel not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get channel", "")
		return
	}

	if err := h.Service.TestChannel(r.Context(), ch); err != nil {
		h.Logger.Error("channel test failed", "channel", ch.Name, "type", ch.Type, "error", err)
		httputil.WriteError(w, http.StatusBadGateway, "channel test failed", "")
		return
	}

	h.auditLog(r, user, audit.ActionUpdate, "notification-channel-test", ch.Name)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"status": "ok"}})
}

// --- Rule management (admin only) ---

// HandleListRules returns all notification routing rules.
func (h *Handler) HandleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Service.store.ListRules(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list rules", "")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": rules})
}

// validateRuleFilters checks that every Source in rule.SourceFilter and
// every Severity in rule.SeverityFilter is a known enum value. Returns an
// empty string when valid, or a 400-suitable error message naming the bad
// value. nc_rules.source_filter is TEXT[] with no DB-level CHECK, so this
// application-layer guard is the only barrier — without it, a typo'd
// source string persists silently and matches no notification at dispatch
// time, leaving the rule a no-op.
func validateRuleFilters(rule Rule) string {
	for _, src := range rule.SourceFilter {
		if !src.Valid() {
			return "unknown source in sourceFilter: " + string(src)
		}
	}
	for _, sev := range rule.SeverityFilter {
		if !sev.Valid() {
			return "unknown severity in severityFilter: " + string(sev)
		}
	}
	return ""
}

// HandleCreateRule creates a new notification routing rule.
func (h *Handler) HandleCreateRule(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	var rule Rule
	if err := decodeJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}

	if rule.Name == "" || rule.ChannelID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and channelId are required", "")
		return
	}
	if msg := validateRuleFilters(rule); msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}

	rule.CreatedBy = user.KubernetesUsername
	id, err := h.Service.store.CreateRule(r.Context(), rule)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create rule", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionCreate, "notification-rule", rule.Name)

	httputil.WriteJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
}

// HandleUpdateRule updates a notification routing rule.
func (h *Handler) HandleUpdateRule(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")
	var rule Rule
	if err := decodeJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	if msg := validateRuleFilters(rule); msg != "" {
		httputil.WriteError(w, http.StatusBadRequest, msg, "")
		return
	}

	rule.ID = id
	rule.UpdatedBy = user.KubernetesUsername
	if err := h.Service.store.UpdateRule(r.Context(), rule); err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "rule not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update rule", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionUpdate, "notification-rule", rule.Name)

	w.WriteHeader(http.StatusNoContent)
}

// HandleDeleteRule deletes a notification routing rule.
func (h *Handler) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	id := chi.URLParam(r, "id")
	if err := h.Service.store.DeleteRule(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "rule not found", "")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete rule", "")
		return
	}

	h.Service.RefreshCache(r.Context())
	h.auditLog(r, user, audit.ActionDelete, "notification-rule", id)

	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

// decodeJSON decodes a JSON request body with a 64KB size limit.
func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxRequestBody)
	return json.NewDecoder(r.Body).Decode(v)
}

// isValidChannelType checks if the channel type is one of the known types.
func isValidChannelType(t ChannelType) bool {
	return t == ChannelSlack || t == ChannelEmail || t == ChannelWebhook
}

// accessibleNamespaces returns the list of namespaces the user can access.
// Returns nil for admin users (no namespace filtering needed — store treats nil as "all").
func (h *Handler) accessibleNamespaces(r *http.Request, user *auth.User) ([]string, error) {
	if auth.IsAdmin(user) {
		return nil, nil
	}

	summary, err := h.RBACChecker.GetSummary(r.Context(), user, nil)
	if err != nil {
		return nil, fmt.Errorf("get RBAC summary: %w", err)
	}

	namespaces := make([]string, 0, len(summary.Namespaces))
	for ns := range summary.Namespaces {
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

// validateChannelURLs validates webhook/Slack URLs against the SSRF blocklist.
// Requires URL to be non-empty for Slack and Webhook channel types.
func validateChannelURLs(ch Channel) error {
	switch ch.Type {
	case ChannelSlack:
		url, _ := ch.Config["webhookUrl"].(string)
		if url == "" {
			return fmt.Errorf("Slack channel requires a webhookUrl")
		}
		if err := k8s.ValidateRemoteURL(url); err != nil {
			return fmt.Errorf("invalid Slack webhook URL: %w", err)
		}
	case ChannelWebhook:
		url, _ := ch.Config["url"].(string)
		if url == "" {
			return fmt.Errorf("Webhook channel requires a url")
		}
		if err := k8s.ValidateRemoteURL(url); err != nil {
			return fmt.Errorf("invalid webhook URL: %w", err)
		}
	}
	return nil
}

// sensitiveKeys are config key patterns that should be masked in API responses.
var sensitiveKeys = []string{"url", "secret", "password", "token", "key", "authorization"}

// maskConfig replaces sensitive values with "****" for API responses.
// Nested maps (like headers) are deep-masked.
func maskConfig(cfg ChannelConfig) ChannelConfig {
	masked := make(ChannelConfig, len(cfg))
	for k, v := range cfg {
		if isSensitiveKey(k) {
			switch val := v.(type) {
			case string:
				if len(val) > 4 {
					masked[k] = "****" + val[len(val)-4:]
				} else {
					masked[k] = "****"
				}
			case map[string]any:
				// Deep-mask all values in nested maps (e.g., headers with Authorization)
				maskedMap := make(map[string]any, len(val))
				for hk := range val {
					maskedMap[hk] = "****"
				}
				masked[k] = maskedMap
			default:
				masked[k] = "****"
			}
		} else {
			masked[k] = v
		}
	}
	return masked
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, name string) {
	if h.AuditLogger == nil {
		return
	}
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Action:       action,
		ResourceKind: kind,
		ResourceName: name,
		User:         user.KubernetesUsername,
		SourceIP:     r.RemoteAddr,
		Result:       audit.ResultSuccess,
	})
}
