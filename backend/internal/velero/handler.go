package velero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/audit"
	"github.com/kubecenter/kubecenter/internal/auth"
	"github.com/kubecenter/kubecenter/internal/httputil"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/internal/k8s/remotecache"
	"github.com/kubecenter/kubecenter/internal/k8s/resources"
	"github.com/kubecenter/kubecenter/internal/notifications"
	"github.com/kubecenter/kubecenter/internal/recoverutil"
	"github.com/kubecenter/kubecenter/internal/server/middleware"
	"github.com/kubecenter/kubecenter/internal/store"
)

// dnsLabelRegex validates DNS label names (RFC 1123).
var dnsLabelRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

const cacheTTL = 30 * time.Second

// NotificationEmitter is the part of the notification service the handler
// uses.
type NotificationEmitter interface {
	Emit(ctx context.Context, n notifications.Notification)
}

// Handler serves Velero HTTP endpoints for the cluster a request selects.
// The local cluster is read through a service-account cache and the local
// Discoverer; a remote cluster through Clients, as the requesting identity,
// with its lists held briefly per identity in remote.
type Handler struct {
	K8sClient     *k8s.ClientFactory
	Discoverer    *Discoverer
	AccessChecker *resources.AccessChecker
	AuditLogger   audit.Logger
	NotifService  NotificationEmitter
	Clients       k8s.ClusterClients
	Presence      *k8s.Presence
	Logger        *slog.Logger

	// Assurance backs the Release F /velero/assurance/* endpoints. Assigned
	// after construction (like NotifService); nil until main wires it, and
	// the collector inside is disabled when no PostgreSQL is configured.
	Assurance *AssuranceService
	// AssuranceStore is the same store the collector uses, for the policy and
	// exception endpoints. Nil when no PostgreSQL is configured.
	AssuranceStore *store.BackupAssuranceStore

	remote *remotecache.Cache[*snapshot]

	// baseDynOverride is a test-only seam for the local service-account
	// client; production leaves it nil.
	baseDynOverride dynamic.Interface

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
	notifService NotificationEmitter,
	clients k8s.ClusterClients,
	presence *k8s.Presence,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		K8sClient:     k8sClient,
		Discoverer:    discoverer,
		AccessChecker: accessChecker,
		AuditLogger:   auditLogger,
		NotifService:  notifService,
		Clients:       clients,
		Presence:      presence,
		Logger:        logger,
		remote:        remotecache.New[*snapshot](remotecache.DefaultTTL, remotecache.DefaultMaxEntries, logger),
	}
}

// afterWrite makes the next read see a write: the local cache is dropped,
// or on a remote cluster, which sends no informer events, every identity's
// cached view of it (R-8 KTD7). The notification names the cluster that was
// written.
func (h *Handler) afterWrite(ctx context.Context) {
	clusterID := middleware.ClusterIDFromContext(ctx)
	if k8s.IsLocalClusterID(clusterID) {
		h.cacheMu.Lock()
		h.cacheGen++
		h.cachedData = nil
		h.cacheMu.Unlock()
	} else {
		h.EvictRemoteCache(clusterID)
	}

	if h.NotifService != nil {
		go recoverutil.Safe(h.Logger, "velero notify", func() {
			h.NotifService.Emit(context.Background(), notifications.Notification{
				Source:    notifications.SourceVelero,
				Severity:  notifications.SeverityInfo,
				Title:     "Velero data updated",
				Message:   "Backup or restore data has changed",
				ClusterID: clusterID,
			})
		})
	}
}

// validateDNSLabel validates that a string is a valid DNS label.
func validateDNSLabel(s string) bool {
	return len(s) > 0 && len(s) <= 63 && dnsLabelRegex.MatchString(s)
}

// accessResult is the outcome of an RBAC pre-check.
type accessResult int

const (
	accessAllowed accessResult = iota
	accessDenied
	// accessFailed means the check could not be made and the error response
	// has already been written.
	accessFailed
)

// checkAccess asks whether the user may verb a Velero resource on the
// request's cluster; the SAR runs against that cluster (F#9). On a remote
// cluster a check that could not be made is answered with the classified
// error, so an unreachable cluster never looks empty or forbidden (R-8 R6,
// R7). On the local cluster it counts as a denial, as it always has.
func (h *Handler) checkAccess(w http.ResponseWriter, r *http.Request, user *auth.User, verb, resource, namespace string) accessResult {
	can, err := h.canAccess(r, user, verb, resource, namespace)
	switch {
	case err != nil && !isLocal(r.Context()):
		writeAccessCheckError(w, err)
		return accessFailed
	case err != nil || !can:
		return accessDenied
	}
	return accessAllowed
}

// canAccess runs the SAR for verb on a Velero resource against the
// request's cluster.
func (h *Handler) canAccess(r *http.Request, user *auth.User, verb, resource, namespace string) (bool, error) {
	return h.AccessChecker.CanAccessGroupResource(
		r.Context(),
		middleware.ClusterIDFromContext(r.Context()),
		user.KubernetesUsername,
		user.KubernetesGroups,
		verb,
		"velero.io",
		resource,
		namespace,
	)
}

// allowed is checkAccess for an endpoint that answers a denial with 403. It
// reports whether the request may proceed.
func (h *Handler) allowed(w http.ResponseWriter, r *http.Request, user *auth.User, verb, resource, namespace string) bool {
	switch h.checkAccess(w, r, user, verb, resource, namespace) {
	case accessAllowed:
		return true
	case accessDenied:
		httputil.WriteError(w, http.StatusForbidden, "access denied", "")
	}
	return false
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
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	if !isLocal(r.Context()) {
		httputil.WriteData(w, h.remoteStatus(r.Context(), user))
		return
	}
	httputil.WriteData(w, h.Discoverer.Status(r.Context()))
}

// HandleListBackups returns all Velero backups.
func (h *Handler) HandleListBackups(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	// RBAC filter
	switch h.checkAccess(w, r, user, "list", "backups", "") {
	case accessFailed:
		return
	case accessDenied:
		httputil.WriteData(w, []Backup{})
		return
	}

	data, _, ok := h.loadList(w, r, user, BackupGVR, "backups")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []Backup{})
		return
	}

	// Sort a copy by start time descending (newest first): the cached
	// slice is shared with concurrent requests.
	backups := slices.Clone(data.backups)
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

	if !h.allowed(w, r, user, "get", "backups", namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	backup, err := h.getBackup(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get backup", "namespace", namespace, "name", name, "error", err)
		h.writeGetError(w, r, err, "backup not found")
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
	if !h.allowed(w, r, user, "create", "backups", input.Namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
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
			},
			"spec": spec,
		},
	}
	// SetLabels stores them as JSON values; a map[string]string placed in
	// the object directly is not one, and breaks DeepCopy.
	if len(input.Labels) > 0 {
		obj.SetLabels(input.Labels)
	}

	created, err := dynClient.Resource(BackupGVR).Namespace(input.Namespace).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		h.failWrite(w, r, user, err, "failed to create backup", audit.ActionVeleroBackupCreate, "Backup", input.Namespace, input.Name)
		return
	}

	h.afterWrite(r.Context())
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
	if !h.allowed(w, r, user, "delete", "backups", namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	// Refuse while a restore still reads this backup. Fails closed: when the
	// restores cannot be read, the backup is not deleted.
	restores, err := h.restoresOf(r.Context(), dynClient, namespace)
	if err != nil {
		h.failWrite(w, r, user, err, "failed to check restores of the backup", audit.ActionVeleroBackupDelete, "Backup", namespace, name)
		return
	}
	for _, restore := range restores {
		// A restore with no phase yet has not been picked up by Velero, and
		// will read the backup once it is.
		if restore.BackupName == name && restore.Namespace == namespace && (restore.Phase == "" || IsProgressPhase(restore.Phase)) {
			httputil.WriteError(w, http.StatusConflict,
				"cannot delete backup with in-progress restore",
				fmt.Sprintf("restore %s/%s is using this backup", restore.Namespace, restore.Name))
			return
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
		h.failWrite(w, r, user, err, "failed to delete backup", audit.ActionVeleroBackupDelete, "Backup", namespace, name)
		return
	}

	h.afterWrite(r.Context())
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

	if !h.allowed(w, r, user, "get", "backups", namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	// The request creates a DownloadRequest on the cluster, so it is audited
	// like any other write.
	url, err := h.requestBackupLogs(r.Context(), dynClient, namespace, name)
	if errors.Is(err, errLogsTimeout) {
		h.auditLog(r, user, audit.ActionVeleroBackupLogs, "Backup", namespace, name, audit.ResultFailure)
		httputil.WriteError(w, http.StatusGatewayTimeout, errLogsTimeout.Error(), "")
		return
	}
	if err != nil {
		h.failWrite(w, r, user, err, "failed to get backup logs", audit.ActionVeleroBackupLogs, "Backup", namespace, name)
		return
	}
	h.auditLog(r, user, audit.ActionVeleroBackupLogs, "Backup", namespace, name, audit.ResultSuccess)

	httputil.WriteData(w, map[string]string{"url": url})
}

// HandleListRestores returns all Velero restores.
func (h *Handler) HandleListRestores(w http.ResponseWriter, r *http.Request) {
	user, ok := httputil.RequireUser(w, r)
	if !ok {
		return
	}

	// RBAC filter
	switch h.checkAccess(w, r, user, "list", "restores", "") {
	case accessFailed:
		return
	case accessDenied:
		httputil.WriteData(w, []Restore{})
		return
	}

	data, _, ok := h.loadList(w, r, user, RestoreGVR, "restores")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []Restore{})
		return
	}

	// Sort a copy by start time descending: the cached slice is shared.
	restores := slices.Clone(data.restores)
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

	if !h.allowed(w, r, user, "get", "restores", namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	restore, err := h.getRestore(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get restore", "namespace", namespace, "name", name, "error", err)
		h.writeGetError(w, r, err, "restore not found")
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
	if !h.allowed(w, r, user, "create", "restores", input.Namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	// Validate backup exists and is completed (if backupName is specified)
	if input.BackupName != "" {
		backup, err := h.getBackup(r.Context(), dynClient, input.Namespace, input.BackupName)
		if err != nil {
			if isLocal(r.Context()) || apierrors.IsNotFound(err) {
				httputil.WriteError(w, http.StatusBadRequest, "backup not found", input.BackupName)
			} else {
				h.writeClusterError(w, r, err, "failed to read the backup to restore")
			}
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
		h.failWrite(w, r, user, err, "failed to create restore", audit.ActionVeleroRestoreCreate, "Restore", input.Namespace, input.Name)
		return
	}

	h.afterWrite(r.Context())
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

	// RBAC filter
	switch h.checkAccess(w, r, user, "list", "schedules", "") {
	case accessFailed:
		return
	case accessDenied:
		httputil.WriteData(w, []Schedule{})
		return
	}

	data, failed, ok := h.loadList(w, r, user, ScheduleGVR, "schedules")
	if !ok {
		return
	}
	if data == nil {
		httputil.WriteData(w, []Schedule{})
		return
	}

	// The last-backup phase needs the backup list, read and permitted for
	// this user; without it the schedules are still served, phases empty.
	var backups []Backup
	if failed[BackupGVR.Resource] == nil {
		backups = h.listableBackups(r, user, data.backups)
	}
	// withLastBackups returns a copy, so sorting leaves the shared cache alone.
	schedules := withLastBackups(data.schedules, backups)
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

	if !h.allowed(w, r, user, "get", "schedules", namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	schedule, err := h.getSchedule(r.Context(), dynClient, namespace, name)
	if err != nil {
		h.Logger.Error("failed to get schedule", "namespace", namespace, "name", name, "error", err)
		h.writeGetError(w, r, err, "schedule not found")
		return
	}
	h.withLastBackup(r.Context(), dynClient, schedule)

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
	if _, err := parseCron(input.Schedule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid cron expression", err.Error())
		return
	}

	// RBAC pre-check
	if !h.allowed(w, r, user, "create", "schedules", input.Namespace) {
		return
	}

	dynClient, ok := h.dynamicClient(w, r, user)
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
		h.failWrite(w, r, user, err, "failed to create schedule", audit.ActionVeleroScheduleCreate, "Schedule", input.Namespace, input.Name)
		return
	}

	h.afterWrite(r.Context())
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

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	// Get existing schedule
	existing, err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get schedule", "error", err)
		h.writeGetError(w, r, err, "schedule not found")
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
		if _, err := parseCron(input.Schedule); err != nil {
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
		h.failWrite(w, r, user, err, "failed to update schedule", audit.ActionVeleroScheduleUpdate, "Schedule", namespace, name)
		return
	}

	h.afterWrite(r.Context())
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

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	if err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Delete(r.Context(), name, metav1.DeleteOptions{}); err != nil {
		h.failWrite(w, r, user, err, "failed to delete schedule", audit.ActionVeleroScheduleDelete, "Schedule", namespace, name)
		return
	}

	h.afterWrite(r.Context())
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

	dynClient, ok := h.dynamicClient(w, r, user)
	if !ok {
		return
	}

	// Get schedule to copy template
	scheduleObj, err := dynClient.Resource(ScheduleGVR).Namespace(namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("failed to get schedule", "error", err)
		h.writeGetError(w, r, err, "schedule not found")
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
					scheduleNameLabel: scheduleLabelValue(name),
				},
			},
			"spec": template,
		},
	}

	created, err := dynClient.Resource(BackupGVR).Namespace(namespace).Create(r.Context(), backupObj, metav1.CreateOptions{})
	if err != nil {
		h.failWrite(w, r, user, err, "failed to trigger backup", audit.ActionVeleroScheduleTrigger, "Backup", namespace, name)
		return
	}

	h.afterWrite(r.Context())
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

	data, failed, err := h.load(r.Context(), user)
	if err != nil {
		h.writeLoadError(w, r, err, "locations")
		return
	}
	// A copy: the cached response is shared with concurrent requests.
	locations := LocationsResponse{
		BackupStorageLocations:  []BackupStorageLocation{},
		VolumeSnapshotLocations: []VolumeSnapshotLocation{},
	}
	if data != nil {
		bsl, vsl := BackupStorageLocationGVR.Resource, VolumeSnapshotLocationGVR.Resource
		// One location list failing on a remote cluster is disclosed
		// rather than hidden; both failing is a failed view.
		if failed[bsl] != nil && failed[vsl] != nil {
			h.writeLoadError(w, r, failed[bsl], "locations")
			return
		}
		locations.Coverage = k8s.CoverageOf(failed, bsl, vsl)
		if failed[bsl] == nil {
			locations.BackupStorageLocations = data.locations.BackupStorageLocations
		}
		if failed[vsl] == nil {
			locations.VolumeSnapshotLocations = data.locations.VolumeSnapshotLocations
		}
	}

	// RBAC filter
	switch h.checkAccess(w, r, user, "list", "backupstoragelocations", "") {
	case accessFailed:
		return
	case accessDenied:
		locations.BackupStorageLocations = []BackupStorageLocation{}
	}
	switch h.checkAccess(w, r, user, "list", "volumesnapshotlocations", "") {
	case accessFailed:
		return
	case accessDenied:
		locations.VolumeSnapshotLocations = []VolumeSnapshotLocation{}
	}

	httputil.WriteData(w, &locations)
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
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	data := newCachedData()
	for _, err := range k8s.RunLists(h.Logger, namedLists(ctx, h.baseDyn(), data.sources())) {
		if err != nil {
			return nil, err
		}
	}

	// Store in cache if not invalidated
	h.cacheMu.Lock()
	if h.cacheGen == gen {
		h.cachedData = data
	}
	h.cacheMu.Unlock()

	return data, nil
}

// newCachedData returns empty Velero data, fetched now, for sources to fill.
func newCachedData() *cachedVeleroData {
	return &cachedVeleroData{locations: &LocationsResponse{}, fetchedAt: time.Now()}
}

// source is one cluster-wide Velero list and how to store it.
type source struct {
	gvr  schema.GroupVersionResource
	list func(context.Context, dynamic.Interface) error
}

// sources are the lists that fill d. Each one writes its own field of d, so
// they run concurrently without a lock.
func (d *cachedVeleroData) sources() []source {
	return []source{
		{BackupGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			return listInto(ctx, dyn, BackupGVR, "", parseBackup, &d.backups)
		}},
		{RestoreGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			return listInto(ctx, dyn, RestoreGVR, "", parseRestore, &d.restores)
		}},
		{ScheduleGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			return listInto(ctx, dyn, ScheduleGVR, "", parseSchedule, &d.schedules)
		}},
		{BackupStorageLocationGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			return listInto(ctx, dyn, BackupStorageLocationGVR, "", parseBSL, &d.locations.BackupStorageLocations)
		}},
		{VolumeSnapshotLocationGVR, func(ctx context.Context, dyn dynamic.Interface) error {
			return listInto(ctx, dyn, VolumeSnapshotLocationGVR, "", parseVSL, &d.locations.VolumeSnapshotLocations)
		}},
	}
}

// namedLists runs each of sources against dyn under ctx.
func namedLists(ctx context.Context, dyn dynamic.Interface, sources []source) []k8s.NamedList {
	lists := make([]k8s.NamedList, len(sources))
	for i, src := range sources {
		lists[i] = k8s.NamedList{Label: "velero list " + src.gvr.Resource, Run: func() error { return src.list(ctx, dyn) }}
	}
	return lists
}

// listInto lists gvr in namespace (every namespace when empty) and stores
// the parsed items in dst, leaving dst untouched on error.
func listInto[T any](ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string, parse func(*unstructured.Unstructured) T, dst *[]T) error {
	list, err := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	items := make([]T, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, parse(&list.Items[i]))
	}
	*dst = items
	return nil
}

func (h *Handler) fetchRestores(ctx context.Context) ([]Restore, error) {
	data, err := h.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	return data.restores, nil
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

// logsWait is how long a backup-logs request waits for Velero to prepare
// the download.
const logsWait = 30 * time.Second

// errLogsTimeout means Velero did not prepare a backup's logs within logsWait.
var errLogsTimeout = errors.New("timed out waiting for Velero to prepare the backup logs")

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

	// Poll for completion for at most logsWait, and no longer than the
	// request lasts.
	timeout := time.NewTimer(logsWait)
	defer timeout.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		req, err := client.Resource(DownloadRequestGVR).Namespace(namespace).Get(ctx, requestName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}

		phase, _, _ := unstructured.NestedString(req.Object, "status", "phase")
		if phase == "Processed" {
			url, _, _ := unstructured.NestedString(req.Object, "status", "downloadURL")
			return url, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errLogsTimeout
		case <-tick.C:
		}
	}
}

func parseBackup(obj *unstructured.Unstructured) Backup {
	spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	status, _, _ := unstructured.NestedMap(obj.Object, "status")

	backup := Backup{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		UID:       string(obj.GetUID()),
		Labels:    obj.GetLabels(),
		created:   obj.GetCreationTimestamp().Time,
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
		backup.ScheduleName = labels[scheduleNameLabel]
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
		UID:       string(obj.GetUID()),
		created:   obj.GetCreationTimestamp().Time,
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

// computeNextRun is a display helper for the schedules page: it clamps the
// start of its walk to now, so it can never report a missed run. Backup
// assurance uses ExpectedRunsSince (assurance.go) instead.
func computeNextRun(cronExpr string, lastRun *time.Time) *time.Time {
	sched, err := parseCron(cronExpr)
	if err != nil {
		return nil
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
