package velero

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
)

// dnsLabelRegex validates DNS label names (RFC 1123).
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

const cacheTTL = 30 * time.Second

// Handler serves Velero HTTP endpoints.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  *notifications.NotificationService
	Logger        *slog.Logger

	fetchGroup singleflight.Group
	cacheMu    sync.RWMutex
	cachedData *cachedVeleroData
	cacheGen   uint64
}

type cachedVeleroData struct {
	backups   []Backup
	restores  []Restore
	schedules []Schedule
	locations *LocationsResponse
	fetchedAt time.Time
}

// NewHandler creates a new Velero handler.
func NewHandler(
	k8sClient *k8s.ClientFactory,
	discoverer *Discoverer,
	accessChecker *resources.AccessChecker,
	auditLogger audit.Logger,
	notifService *notifications.NotificationService,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Logger:        logger,
	}
}

// InvalidateCache clears the cached data and emits a notification.
func (h *Handler) InvalidateCache() {
	h.cacheMu.Lock()
	h.cacheGen++
	h.cachedData = nil
	h.cacheMu.Unlock()

	if h.NotifService != nil {
		go h.NotifService.Emit(context.Background(), notifications.Notification{
			Source:   notifications.SourceVelero,
			Severity: notifications.SeverityInfo,
			Title:    "Velero data updated",
			Message:  "Backup or restore data has changed",
		})
	}
}

// getImpersonatingClient creates a dynamic client impersonating the user and handles errors.
func (h *Handler) getImpersonatingClient(w http.ResponseWriter, user *auth.User) (dynamic.Interface, bool) {
	client, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return nil, false
	}
	return client, true
}

// validateDNSLabel validates that a string is a valid DNS label.
func validateDNSLabel(s string) bool {
	return len(s) > 0 && len(s) <= 63 && dnsLabelRegex.MatchString(s)
}

// canAccess checks if the user can access a Velero resource.
func (h *Handler) canAccess(ctx context.Context, user *auth.User, verb, resource, namespace string) bool {
	can, err := h.AccessChecker.CanAccessGroupResource(
		ctx,
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		"velero.io",
		resource,
		namespace,
	)
	return err == nil && can
}

// auditLog writes an audit entry for a Velero action.
func (h *Handler) auditLog(r *http.Request, user *auth.User, action audit.Action, kind, ns, name string, result audit.Result) {
	if h.AuditLogger == nil {
		return
	}
	_ = h.AuditLogger.Log(r.Context(), audit.Entry{
		Timestamp:         time.Now(),
		ClusterID:         middleware.ClusterIDFromContext(r.Context()),
		User:              user.Username,
		SourceIP:          r.RemoteAddr,
		Action:            action,
		ResourceKind:      kind,
		ResourceNamespace: ns,
		ResourceName:      name,
		Result:            result,
	})
}

// HandleStatus returns the Velero detection status.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	_, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	httputil.WriteData(w, status)
}

// HandleListBackups returns all Velero backups.
func (h *Handler) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	if !status.Detected {
		httputil.WriteData(w, []Backup{})
		return
	}

	backups, err := h.fetchBackups(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch backups", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch backups", "")
		return
	}

	// RBAC filter
	if !h.canAccess(r.Context(), user, "list", "backups", "") {
		httputil.WriteData(w, []Backup{})
		return
	}

	// Sort by start time descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].StartTime == nil {
			return false
		}
		if backups[j].StartTime == nil {
			return true
		}
		return backups[i].StartTime.After(*backups[j].StartTime)
	})

	httputil.WriteData(w, backups)
}

// HandleGetBackup returns a single backup.
func (h *Handler) HandleGetBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "backups", namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	backup, err := h.getBackup(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get backup", "namespace", namespace, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "backup not found", "")
		return
	}

	httputil.WriteData(w, backup)
}

// HandleCreateBackup creates a new backup.
func (h *Handler) HandleCreateBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var input struct {
		Name               string            `json:"name"`
		Namespace          string            `json:"namespace"`
		IncludedNamespaces []string          `json:"includedNamespaces"`
		ExcludedNamespaces []string          `json:"excludedNamespaces"`
		StorageLocation    string            `json:"storageLocation"`
		TTL                string            `json:"ttl"`
		SnapshotVolumes    *bool             `json:"snapshotVolumes"`
		Labels             map[string]string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Validate DNS label names
	if !validateDNSLabel(input.Name) {
		httputil.WriteError(w, http.StatusBadRequest, "name must be a valid DNS label (lowercase, 1-63 chars)", "")
		return
	}
	if input.Namespace == "" {
		input.Namespace = "velero"
	}
	if !validateDNSLabel(input.Namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "namespace must be a valid DNS label", "")
		return
	}

	// RBAC pre-check
	if !h.canAccess(r.Context(), user, "create", "backups", input.Namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	spec := map[string]any{}
	if len(input.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = input.IncludedNamespaces
	}
	if len(input.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = input.ExcludedNamespaces
	}
	if input.StorageLocation != "" {
		spec["storageLocation"] = input.StorageLocation
	}
	if input.TTL != "" {
		spec["ttl"] = input.TTL
	}
	if input.SnapshotVolumes != nil {
		spec["snapshotVolumes"] = *input.SnapshotVolumes
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]any{
				"name":      input.Name,
				"namespace": input.Namespace,
				"labels":    input.Labels,
			},
			"spec": spec,
		},
	}

	created, err := dynClient.Resource(BackupGVR).Namespace(input.Namespace).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.Logger.Error("failed to create backup", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create backup", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroBackupCreate, "Backup", input.Namespace, input.Name, audit.ResultSuccess)

	backup := parseBackup(created)
	httputil.WriteData(w, backup)
}

// HandleDeleteBackup deletes a backup by creating a DeleteBackupRequest.
func (h *Handler) HandleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	// RBAC pre-check
	if !h.canAccess(r.Context(), user, "delete", "backups", namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	// Check for dependent restores that reference this backup
	restores, err := h.fetchRestores(r.Context())
	if err == nil {
		for _, restore := range restores {
			if restore.BackupName == name && restore.Namespace == namespace {
				// Only block if restore is in progress
				if restore.Phase == "InProgress" || restore.Phase == "New" || restore.Phase == "WaitingForPluginOperations" {
					httputil.WriteError(w, http.StatusConflict,
						"cannot delete backup with in-progress restore",
						fmt.Sprintf("restore %s/%s is using this backup", restore.Namespace, restore.Name))
					return
				}
			}
		}
	}

	// Create a DeleteBackupRequest to gracefully delete the backup
	deleteRequest := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "DeleteBackupRequest",
			"metadata": map[string]any{
				"name":      name + "-delete-" + time.Now().Format("20060102150405"),
				"namespace": namespace,
			},
			"spec": map[string]any{
				"backupName": name,
			},
		},
	}

	_, err = dynClient.Resource(DeleteBackupRequestGVR).Namespace(namespace).Create(r.Context(), deleteRequest, metav1.CreateOptions{})
	if err != nil {
		h.Logger.Error("failed to create delete backup request", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete backup", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroBackupDelete, "Backup", namespace, name, audit.ResultSuccess)

	w.WriteHeader(http.StatusNoContent)
}

// HandleGetBackupLogs creates a DownloadRequest and returns the presigned URL.
func (h *Handler) HandleGetBackupLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "backups", namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	url, err := h.requestBackupLogs(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get backup logs", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get backup logs", err.Error())
		return
	}

	httputil.WriteData(w, map[string]string{"url": url})
}

// HandleListRestores returns all Velero restores.
func (h *Handler) HandleListRestores(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	if !status.Detected {
		httputil.WriteData(w, []Restore{})
		return
	}

	restores, err := h.fetchRestores(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch restores", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch restores", "")
		return
	}

	// RBAC filter
	if !h.canAccess(r.Context(), user, "list", "restores", "") {
		httputil.WriteData(w, []Restore{})
		return
	}

	// Sort by start time descending
	sort.Slice(restores, func(i, j int) bool {
		if restores[i].StartTime == nil {
			return false
		}
		if restores[j].StartTime == nil {
			return true
		}
		return restores[i].StartTime.After(*restores[j].StartTime)
	})

	httputil.WriteData(w, restores)
}

// HandleGetRestore returns a single restore.
func (h *Handler) HandleGetRestore(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "restores", namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	restore, err := h.getRestore(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get restore", "namespace", namespace, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "restore not found", "")
		return
	}

	httputil.WriteData(w, restore)
}

// HandleCreateRestore creates a new restore.
func (h *Handler) HandleCreateRestore(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var input struct {
		Name                   string            `json:"name"`
		Namespace              string            `json:"namespace"`
		BackupName             string            `json:"backupName"`
		ScheduleName           string            `json:"scheduleName"`
		IncludedNamespaces     []string          `json:"includedNamespaces"`
		ExcludedNamespaces     []string          `json:"excludedNamespaces"`
		NamespaceMapping       map[string]string `json:"namespaceMapping"`
		ExistingResourcePolicy string            `json:"existingResourcePolicy"`
		RestorePVs             *bool             `json:"restorePVs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Validate DNS label names
	if !validateDNSLabel(input.Name) {
		httputil.WriteError(w, http.StatusBadRequest, "name must be a valid DNS label (lowercase, 1-63 chars)", "")
		return
	}
	if input.BackupName == "" && input.ScheduleName == "" {
		httputil.WriteError(w, http.StatusBadRequest, "backupName or scheduleName is required", "")
		return
	}
	if input.Namespace == "" {
		input.Namespace = "velero"
	}
	if !validateDNSLabel(input.Namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "namespace must be a valid DNS label", "")
		return
	}

	// Validate existingResourcePolicy enum
	if input.ExistingResourcePolicy != "" && !slices.Contains([]string{"none", "update"}, input.ExistingResourcePolicy) {
		httputil.WriteError(w, http.StatusBadRequest, "existingResourcePolicy must be 'none' or 'update'", "")
		return
	}

	// RBAC pre-check
	if !h.canAccess(r.Context(), user, "create", "restores", input.Namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	// Validate backup exists and is completed (if backupName is specified)
	if input.BackupName != "" {
		backup, err := h.getBackup(r.Context(), dynClient, input.Namespace, input.BackupName)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "backup not found", input.BackupName)
			return
		}
		if backup.Phase != "Completed" && backup.Phase != "PartiallyFailed" {
			httputil.WriteError(w, http.StatusBadRequest, "backup must be Completed or PartiallyFailed", backup.Phase)
			return
		}
	}

	spec := map[string]any{}
	if input.BackupName != "" {
		spec["backupName"] = input.BackupName
	}
	if input.ScheduleName != "" {
		spec["scheduleName"] = input.ScheduleName
	}
	if len(input.IncludedNamespaces) > 0 {
		spec["includedNamespaces"] = input.IncludedNamespaces
	}
	if len(input.ExcludedNamespaces) > 0 {
		spec["excludedNamespaces"] = input.ExcludedNamespaces
	}
	if len(input.NamespaceMapping) > 0 {
		spec["namespaceMapping"] = input.NamespaceMapping
	}
	if input.ExistingResourcePolicy != "" {
		spec["existingResourcePolicy"] = input.ExistingResourcePolicy
	}
	if input.RestorePVs != nil {
		spec["restorePVs"] = *input.RestorePVs
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Restore",
			"metadata": map[string]any{
				"name":      input.Name,
				"namespace": input.Namespace,
			},
			"spec": spec,
		},
	}

	created, err := dynClient.Resource(RestoreGVR).Namespace(input.Namespace).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.Logger.Error("failed to create restore", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create restore", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroRestoreCreate, "Restore", input.Namespace, input.Name, audit.ResultSuccess)

	restore := parseRestore(created)
	httputil.WriteData(w, restore)
}

// HandleListSchedules returns all Velero schedules.
func (h *Handler) HandleListSchedules(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	if !status.Detected {
		httputil.WriteData(w, []Schedule{})
		return
	}

	schedules, err := h.fetchSchedules(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch schedules", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch schedules", "")
		return
	}

	// RBAC filter
	if !h.canAccess(r.Context(), user, "list", "schedules", "") {
		httputil.WriteData(w, []Schedule{})
		return
	}

	// Sort by name
	sort.Slice(schedules, func(i, j int) bool {
		return schedules[i].Name < schedules[j].Name
	})

	httputil.WriteData(w, schedules)
}

// HandleGetSchedule returns a single schedule.
func (h *Handler) HandleGetSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	if !h.canAccess(r.Context(), user, "get", "schedules", namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	schedule, err := h.getSchedule(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get schedule", "namespace", namespace, "name", name, "error", err)
		httputil.WriteError(w, http.StatusNotFound, "schedule not found", "")
		return
	}

	httputil.WriteData(w, schedule)
}

// HandleCreateSchedule creates a new schedule.
func (h *Handler) HandleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	var input struct {
		Name               string   `json:"name"`
		Namespace          string   `json:"namespace"`
		Schedule           string   `json:"schedule"`
		IncludedNamespaces []string `json:"includedNamespaces"`
		ExcludedNamespaces []string `json:"excludedNamespaces"`
		StorageLocation    string   `json:"storageLocation"`
		TTL                string   `json:"ttl"`
		SnapshotVolumes    *bool    `json:"snapshotVolumes"`
		Paused             bool     `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Validate DNS label names
	if !validateDNSLabel(input.Name) {
		httputil.WriteError(w, http.StatusBadRequest, "name must be a valid DNS label (lowercase, 1-63 chars)", "")
		return
	}
	if input.Schedule == "" {
		httputil.WriteError(w, http.StatusBadRequest, "schedule is required", "")
		return
	}
	if input.Namespace == "" {
		input.Namespace = "velero"
	}
	if !validateDNSLabel(input.Namespace) {
		httputil.WriteError(w, http.StatusBadRequest, "namespace must be a valid DNS label", "")
		return
	}

	// Validate cron expression
	if _, err := cron.ParseStandard(input.Schedule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid cron expression", err.Error())
		return
	}

	// RBAC pre-check
	if !h.canAccess(r.Context(), user, "create", "schedules", input.Namespace) {
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
		return
	}

	dynClient, ok := h.getImpersonatingClient(w, user)
	if !ok {
		return
	}

	template := map[string]any{}
	if len(input.IncludedNamespaces) > 0 {
		template["includedNamespaces"] = input.IncludedNamespaces
	}
	if len(input.ExcludedNamespaces) > 0 {
		template["excludedNamespaces"] = input.ExcludedNamespaces
	}
	if input.StorageLocation != "" {
		template["storageLocation"] = input.StorageLocation
	}
	if input.TTL != "" {
		template["ttl"] = input.TTL
	}
	if input.SnapshotVolumes != nil {
		template["snapshotVolumes"] = *input.SnapshotVolumes
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Schedule",
			"metadata": map[string]any{
				"name":      input.Name,
				"namespace": input.Namespace,
			},
			"spec": map[string]any{
				"schedule": input.Schedule,
				"paused":   input.Paused,
				"template": template,
			},
		},
	}

	created, err := dynClient.Resource(ScheduleGVR).Namespace(input.Namespace).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.Logger.Error("failed to create schedule", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create schedule", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroScheduleCreate, "Schedule", input.Namespace, input.Name, audit.ResultSuccess)

	schedule := parseSchedule(created)
	httputil.WriteData(w, schedule)
}

// HandleUpdateSchedule updates a schedule (pause/resume or full update).
func (h *Handler) HandleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	var input struct {
		Paused   *bool  `json:"paused"`
		Schedule string `json:"schedule"`
		TTL      string `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Get existing schedule
	existing, err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get schedule", "error", err)
		httputil.WriteError(w, http.StatusNotFound, "schedule not found", "")
		return
	}

	spec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	if spec == nil {
		spec = map[string]any{}
	}

	if input.Paused != nil {
		spec["paused"] = *input.Paused
	}
	if input.Schedule != "" {
		if _, err := cron.ParseStandard(input.Schedule); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid cron expression", err.Error())
			return
		}
		spec["schedule"] = input.Schedule
	}
	if input.TTL != "" {
		template, _, _ := unstructured.NestedMap(spec, "template")
		if template == nil {
			template = map[string]any{}
		}
		template["ttl"] = input.TTL
		spec["template"] = template
	}

	if err := unstructured.SetNestedMap(existing.Object, spec, "spec"); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update spec", err.Error())
		return
	}

	updated, err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Update(r.Context(), existing, metav1.UpdateOptions{})
	if err != nil {
		h.Logger.Error("failed to update schedule", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update schedule", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroScheduleUpdate, "Schedule", namespace, name, audit.ResultSuccess)

	schedule := parseSchedule(updated)
	httputil.WriteData(w, schedule)
}

// HandleDeleteSchedule deletes a schedule.
func (h *Handler) HandleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	err = dynClient.Resource(ScheduleGVR).Namespace(namespace).Delete(r.Context(), name, metav1.DeleteOptions{})
	if err != nil {
		h.Logger.Error("failed to delete schedule", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete schedule", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroScheduleDelete, "Schedule", namespace, name, audit.ResultSuccess)

	w.WriteHeader(http.StatusNoContent)
}

// HandleTriggerSchedule creates an on-demand backup from a schedule.
func (h *Handler) HandleTriggerSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	dynClient, err := h.K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)
	if err != nil {
		h.Logger.Error("failed to create impersonating client", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Get schedule to copy template
	scheduleObj, err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get schedule", "error", err)
		httputil.WriteError(w, http.StatusNotFound, "schedule not found", "")
		return
	}

	template, _, _ := unstructured.NestedMap(scheduleObj.Object, "spec", "template")
	if template == nil {
		template = map[string]any{}
	}

	backupName := fmt.Sprintf("%s-manual-%s", name, time.Now().Format("20060102150405"))

	backupObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]any{
				"name":      backupName,
				"namespace": namespace,
				"labels": map[string]any{
					"velero.io/schedule-name": name,
				},
			},
			"spec": template,
		},
	}

	created, err := dynClient.Resource(BackupGVR).Namespace(namespace).Create(r.Context(), backupObj, metav1.CreateOptions{})
	if err != nil {
		h.Logger.Error("failed to trigger backup", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to trigger backup", err.Error())
		return
	}

	h.InvalidateCache()
	h.auditLog(r, user, audit.ActionVeleroScheduleTrigger, "Backup", namespace, name, audit.ResultSuccess)

	backup := parseBackup(created)
	httputil.WriteData(w, backup)
}

// HandleListLocations returns BSLs and VSLs.
func (h *Handler) HandleListLocations(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	status := h.Discoverer.Status(r.Context())
	if !status.Detected {
		httputil.WriteData(w, &LocationsResponse{
			BackupStorageLocations:  []BackupStorageLocation{},
			VolumeSnapshotLocations: []VolumeSnapshotLocation{},
		})
		return
	}

	locations, err := h.fetchLocations(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch locations", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to fetch locations", "")
		return
	}

	// RBAC filter
	if !h.canAccess(r.Context(), user, "list", "backupstoragelocations", "") {
		locations.BackupStorageLocations = []BackupStorageLocation{}
	}
	if !h.canAccess(r.Context(), user, "list", "volumesnapshotlocations", "") {
		locations.VolumeSnapshotLocations = []VolumeSnapshotLocation{}
	}

	httputil.WriteData(w, locations)
}

// fetchAll fetches all Velero data in parallel and caches the result.
func (h *Handler) fetchAll(ctx context.Context) (*cachedVeleroData, error) {
	h.cacheMu.RLock()
	if h.cachedData != nil && time.Since(h.cachedData.fetchedAt) < cacheTTL {
		data := h.cachedData
		h.cacheMu.RUnlock()
		return data, nil
	}
	gen := h.cacheGen
	h.cacheMu.RUnlock()

	result, err, _ := h.fetchGroup.Do("all", func() (any, error) {
		return h.doFetchAll(ctx, gen)
	})
	if err != nil {
		return nil, err
	}
	return result.(*cachedVeleroData), nil
}

func (h *Handler) doFetchAll(ctx context.Context, gen uint64) (*cachedVeleroData, error) {
	// Add timeout to prevent hanging on slow k8s API
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dynClient := h.K8sClient.BaseDynamicClient()

	var (
		backups   []Backup
		restores  []Restore
		schedules []Schedule
		bsls      []BackupStorageLocation
		vsls      []VolumeSnapshotLocation
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		list, err := dynClient.Resource(BackupGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		backups = make([]Backup, 0, len(list.Items))
		for _, item := range list.Items {
			backups = append(backups, parseBackup(&item))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(RestoreGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		restores = make([]Restore, 0, len(list.Items))
		for _, item := range list.Items {
			restores = append(restores, parseRestore(&item))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(ScheduleGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		schedules = make([]Schedule, 0, len(list.Items))
		for _, item := range list.Items {
			schedules = append(schedules, parseSchedule(&item))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(BackupStorageLocationGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		bsls = make([]BackupStorageLocation, 0, len(list.Items))
		for _, item := range list.Items {
			bsls = append(bsls, parseBSL(&item))
		}
		return nil
	})

	g.Go(func() error {
		list, err := dynClient.Resource(VolumeSnapshotLocationGVR).Namespace("").List(gctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		vsls = make([]VolumeSnapshotLocation, 0, len(list.Items))
		for _, item := range list.Items {
			vsls = append(vsls, parseVSL(&item))
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	data := &cachedVeleroData{
		backups:   backups,
		restores:  restores,
		schedules: schedules,
		locations: &LocationsResponse{
			BackupStorageLocations:  bsls,
			VolumeSnapshotLocations: vsls,
		},
		fetchedAt: time.Now(),
	}

	// Store in cache if not invalidated
	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cachedData = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

func (h *Handler) fetchBackups(ctx context.Context) ([]Backup, error) {
	data, err := h.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	return data.backups, nil
}

func (h *Handler) fetchRestores(ctx context.Context) ([]Restore, error) {
	data, err := h.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	return data.restores, nil
}

func (h *Handler) fetchSchedules(ctx context.Context) ([]Schedule, error) {
	data, err := h.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	return data.schedules, nil
}

func (h *Handler) fetchLocations(ctx context.Context) (*LocationsResponse, error) {
	data, err := h.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	return data.locations, nil
}

func (h *Handler) getBackup(ctx context.Context, client dynamic.Interface, namespace, name string) (*Backup, error) {
	obj, err := client.Resource(BackupGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	backup := parseBackup(obj)
	return &backup, nil
}

func (h *Handler) getRestore(ctx context.Context, client dynamic.Interface, namespace, name string) (*Restore, error) {
	obj, err := client.Resource(RestoreGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	restore := parseRestore(obj)
	return &restore, nil
}

func (h *Handler) getSchedule(ctx context.Context, client dynamic.Interface, namespace, name string) (*Schedule, error) {
	obj, err := client.Resource(ScheduleGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	schedule := parseSchedule(obj)
	return &schedule, nil
}

func (h *Handler) requestBackupLogs(ctx context.Context, client dynamic.Interface, namespace, backupName string) (string, error) {
	// Create a DownloadRequest
	requestName := fmt.Sprintf("%s-logs-%s", backupName, time.Now().Format("20060102150405"))

	downloadReq := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "velero.io/v1",
			"kind":       "DownloadRequest",
			"metadata": map[string]any{
				"name":      requestName,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"target": map[string]any{
					"kind": "BackupLog",
					"name": backupName,
				},
			},
		},
	}

	_, err := client.Resource(DownloadRequestGVR).Namespace(namespace).Create(ctx, downloadReq, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	// Poll for completion (max 30 seconds)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, err := client.Resource(DownloadRequestGVR).Namespace(namespace).Get(ctx, requestName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}

		phase, _, _ := unstructured.NestedString(req.Object, "status", "phase")
		if phase == "Processed" {
			url, _, _ := unstructured.NestedString(req.Object, "status", "downloadURL")
			return url, nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return "", fmt.Errorf("timeout waiting for download request to be processed")
}

func parseBackup(obj *unstructured.Unstructured) Backup {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	backup := Backup{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Labels:    obj.GetLabels(),
	}

	// Spec fields
	if spec != nil {
		backup.IncludedNamespaces = getStringSlice(spec, "includedNamespaces")
		backup.ExcludedNamespaces = getStringSlice(spec, "excludedNamespaces")
		backup.StorageLocation, _, _ = unstructured.NestedString(spec, "storageLocation")
		backup.TTL, _, _ = unstructured.NestedString(spec, "ttl")
		backup.SnapshotVolumes, _, _ = unstructured.NestedBool(spec, "snapshotVolumes")
	}

	// Status fields
	if status != nil {
		backup.Phase, _, _ = unstructured.NestedString(status, "phase")
		backup.StartTime = getTime(status, "startTimestamp")
		backup.CompletionTime = getTime(status, "completionTimestamp")
		backup.Expiration = getTime(status, "expiration")
		backup.ItemsBackedUp = getInt(status, "progress", "itemsBackedUp")
		backup.TotalItems = getInt(status, "progress", "totalItems")
		backup.Warnings = getInt(status, "warnings")
		backup.Errors = getInt(status, "errors")
	}

	// Schedule name from label
	if labels := obj.GetLabels(); labels != nil {
		backup.ScheduleName = labels["velero.io/schedule-name"]
	}

	return backup
}

func parseRestore(obj *unstructured.Unstructured) Restore {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	restore := Restore{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	// Spec fields
	if spec != nil {
		restore.BackupName, _, _ = unstructured.NestedString(spec, "backupName")
		restore.ScheduleName, _, _ = unstructured.NestedString(spec, "scheduleName")
		restore.IncludedNamespaces = getStringSlice(spec, "includedNamespaces")
		nsMapping, _, _ := unstructured.NestedStringMap(spec, "namespaceMapping")
		if len(nsMapping) > 0 {
			restore.NamespaceMapping = nsMapping
		}
	}

	// Status fields
	if status != nil {
		restore.Phase, _, _ = unstructured.NestedString(status, "phase")
		restore.StartTime = getTime(status, "startTimestamp")
		restore.CompletionTime = getTime(status, "completionTimestamp")
		restore.ItemsRestored = getInt(status, "progress", "itemsRestored")
		restore.TotalItems = getInt(status, "progress", "totalItems")
		restore.Warnings = getInt(status, "warnings")
		restore.Errors = getInt(status, "errors")
		restore.FailureReason, _, _ = unstructured.NestedString(status, "failureReason")
	}

	return restore
}

func parseSchedule(obj *unstructured.Unstructured) Schedule {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	schedule := Schedule{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	// Spec fields
	if spec != nil {
		schedule.Schedule, _, _ = unstructured.NestedString(spec, "schedule")
		schedule.Paused, _, _ = unstructured.NestedBool(spec, "paused")

		template, _, _ := unstructured.NestedMap(spec, "template")
		if template != nil {
			schedule.IncludedNamespaces = getStringSlice(template, "includedNamespaces")
			schedule.TTL, _, _ = unstructured.NestedString(template, "ttl")
			schedule.StorageLocation, _, _ = unstructured.NestedString(template, "storageLocation")
		}
	}

	// Status fields
	if status != nil {
		schedule.Phase, _, _ = unstructured.NestedString(status, "phase")
		schedule.LastBackup = getTime(status, "lastBackup")

		validationErrors, _, _ := unstructured.NestedStringSlice(status, "validationErrors")
		if len(validationErrors) > 0 {
			schedule.ValidationErrors = validationErrors
		}
	}

	// Compute next run time
	if schedule.Schedule != "" && !schedule.Paused && schedule.Phase == "Enabled" {
		schedule.NextRunTime = computeNextRun(schedule.Schedule, schedule.LastBackup)
	}

	return schedule
}

func parseBSL(obj *unstructured.Unstructured) BackupStorageLocation {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	bsl := BackupStorageLocation{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	if spec != nil {
		bsl.Provider, _, _ = unstructured.NestedString(spec, "provider")
		bsl.Bucket, _, _ = unstructured.NestedString(spec, "objectStorage", "bucket")
		bsl.Prefix, _, _ = unstructured.NestedString(spec, "objectStorage", "prefix")
		bsl.Default, _, _ = unstructured.NestedBool(spec, "default")
	}

	if status != nil {
		bsl.Phase, _, _ = unstructured.NestedString(status, "phase")
		bsl.LastSyncedTime = getTime(status, "lastSyncedTime")
		bsl.Message, _, _ = unstructured.NestedString(status, "message")
	}

	return bsl
}

func parseVSL(obj *unstructured.Unstructured) VolumeSnapshotLocation {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")

	vsl := VolumeSnapshotLocation{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	if spec != nil {
		vsl.Provider, _, _ = unstructured.NestedString(spec, "provider")
	}

	return vsl
}

func getStringSlice(m map[string]any, key string) []string {
	val, found, _ := unstructured.NestedStringSlice(m, key)
	if !found {
		return nil
	}
	return val
}

func getInt(m map[string]any, keys ...string) int {
	val, found, _ := unstructured.NestedInt64(m, keys...)
	if !found {
		return 0
	}
	return int(val)
}

func getTime(m map[string]any, key string) *time.Time {
	val, found, _ := unstructured.NestedString(m, key)
	if !found || val == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return nil
	}
	return &t
}

func computeNextRun(cronExpr string, lastRun *time.Time) *time.Time {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		// Try standard parser
		sched, err = cron.ParseStandard(cronExpr)
		if err != nil {
			return nil
		}
	}

	var from time.Time
	if lastRun != nil {
		from = *lastRun
	} else {
		from = time.Now()
	}

	// Ensure we start from at least now
	if from.Before(time.Now()) {
		from = time.Now()
	}

	next := sched.Next(from)
	return &next
}
