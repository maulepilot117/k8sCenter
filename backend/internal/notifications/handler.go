package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
)

// Handler handles HTTP requests for the notification center.
type Handler struct {
	Service       *NotificationService
	RBACChecker   *auth.RBACChecker
	AuditLogger   audit.Logger
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

	// Mask sensitive config fields
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
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if ch.Name == "" || ch.Type == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and type are required", "")
		return
	}

	// SSRF validation for webhook/Slack URLs
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
	var ch Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
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
		httputil.WriteError(w, http.StatusBadGateway, "test failed", err.Error())
		return
	}

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

// HandleCreateRule creates a new notification routing rule.
func (h *Handler) HandleCreateRule(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}

	var rule Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if rule.Name == "" || rule.ChannelID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and channelId are required", "")
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
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
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

// accessibleNamespaces returns the list of namespaces the user can access.
// Uses the RBAC summary cache (60s TTL). Returns nil for cluster-admin users
// (no namespace filtering needed).
func (h *Handler) accessibleNamespaces(r *http.Request, user *auth.User) ([]string, error) {
	if auth.IsAdmin(user) {
		return nil, nil // admin sees everything
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
func validateChannelURLs(ch Channel) error {
	if ch.Type == ChannelSlack {
		if url, _ := ch.Config["webhookUrl"].(string); url != "" {
			if err := k8s.ValidateRemoteURL(url); err != nil {
				return fmt.Errorf("invalid Slack webhook URL: %w", err)
			}
		}
	}
	if ch.Type == ChannelWebhook {
		if url, _ := ch.Config["url"].(string); url != "" {
			if err := k8s.ValidateRemoteURL(url); err != nil {
				return fmt.Errorf("invalid webhook URL: %w", err)
			}
		}
	}
	return nil
}

// maskConfig replaces sensitive values with "****" for API responses.
func maskConfig(cfg ChannelConfig) ChannelConfig {
	masked := make(ChannelConfig, len(cfg))
	for k, v := range cfg {
		switch k {
		case "webhookUrl", "url", "secret":
			if s, ok := v.(string); ok && len(s) > 4 {
				masked[k] = "****" + s[len(s)-4:]
			} else {
				masked[k] = "****"
			}
		default:
			masked[k] = v
		}
	}
	return masked
}

func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, name string) {
	if h.AuditLogger == nil {
		return
	}
	h.AuditLogger.Log(r.Context(), audit.Entry{
		Action:            action,
		ResourceKind:      kind,
		ResourceName:      name,
		User:              user.KubernetesUsername,
		SourceIP:          r.RemoteAddr,
		Result:            audit.ResultSuccess,
	})
}
