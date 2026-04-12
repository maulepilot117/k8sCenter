# Velero Backup & Restore Integration

Feature: Kubernetes backup and restore operations via Velero integration with scheduled backups, snapshot browsing, and one-click restore.

**Priority:** #5 on roadmap
**Design Date:** 2026-04-10
**Revised:** 2026-04-10 (post-review simplifications)

---

## Overview

Add Velero integration for comprehensive Kubernetes backup and restore operations. This feature enables:

- **Scheduled Backups** — Cron-based recurring backup definitions with configurable retention
- **Browse Snapshots** — Dashboard showing all backups with status, storage location, and item counts
- **One-Click Restore** — Wizard-driven restore from any backup with namespace remapping
- **Storage Location Management** — View BackupStorageLocation and VolumeSnapshotLocation health

Velero is the de facto standard for Kubernetes backup/restore, with 11 CRDs covering backups, restores, schedules, and storage locations.

---

## Architecture

```
┌─────────────────┐     ┌──────────────────────────────────────┐
│  Dynamic Client │────▶│  velero.Handler                      │
│  (user imperson)│     │  - singleflight + cache (30s)        │
└─────────────────┘     │  - RBAC filtering per user           │
                        │  - inline resource helpers            │
                        └────────────────┬─────────────────────┘
                                         │
                                         ▼
                                ┌─────────────────────┐
                                │  Notification       │
                                │  (on-demand check)  │
                                └─────────────────────┘
```

**Key Decisions:**
- Package named `velero` (matches feature name, consistent with `gitops/`, `policy/`)
- Discovery via synchronous CRD probing (`backups.velero.io` existence check) — no background loop
- Singleflight + 30s cache for list endpoints
- RBAC: Admin required for write operations; read access via `CanAccessGroupResource`
- All endpoints use `{namespace}/{name}` path parameters consistently
- Failure notifications dispatched on island mount (no background checker goroutine)
- Multi-cluster support via ClusterRouter (X-Cluster-ID header routing)
- Frontend routes: `/velero/*` (consistent with API routes, not "data-protection")

---

## Velero CRDs

| CRD | API Group | Purpose |
|-----|-----------|---------|
| **Backup** | `velero.io/v1` | Point-in-time backup of cluster resources and volumes |
| **Restore** | `velero.io/v1` | Request to restore resources from a Backup |
| **Schedule** | `velero.io/v1` | Cron-based recurring backup definition |
| **BackupStorageLocation** | `velero.io/v1` | Where backup files are stored (S3, GCS, Azure) |
| **VolumeSnapshotLocation** | `velero.io/v1` | Provider-specific volume snapshot configuration |
| **DeleteBackupRequest** | `velero.io/v1` | Request to delete a backup and its data |
| **DownloadRequest** | `velero.io/v1` | Request to generate download URL for backup logs |

**GVR Constants:**
```go
var (
    BackupGVR = schema.GroupVersionResource{
        Group: "velero.io", Version: "v1", Resource: "backups",
    }
    RestoreGVR = schema.GroupVersionResource{
        Group: "velero.io", Version: "v1", Resource: "restores",
    }
    ScheduleGVR = schema.GroupVersionResource{
        Group: "velero.io", Version: "v1", Resource: "schedules",
    }
    BackupStorageLocationGVR = schema.GroupVersionResource{
        Group: "velero.io", Version: "v1", Resource: "backupstoragelocations",
    }
    VolumeSnapshotLocationGVR = schema.GroupVersionResource{
        Group: "velero.io", Version: "v1", Resource: "volumesnapshotlocations",
    }
)
```

---

## Phase 1: Backend Foundation

### Step 1: Types and Discovery

**Files:**
- `backend/internal/velero/types.go`
- `backend/internal/velero/discovery.go`
- `backend/internal/velero/discovery_test.go`

**Types (`types.go`):**

```go
package velero

import "time"

// VeleroStatus is returned by GET /velero/status
type VeleroStatus struct {
    Detected    bool      `json:"detected"`
    Namespace   string    `json:"namespace,omitempty"`   // Usually "velero"
    Version     string    `json:"version,omitempty"`     // From velero deployment label
    BSLCount    int       `json:"bslCount"`
    VSLCount    int       `json:"vslCount"`
    LastChecked time.Time `json:"lastChecked"`
}

// Backup is the API response for a Velero backup
// Note: No "Normalized" prefix — we only have one backup tool (Velero)
type Backup struct {
    Name               string            `json:"name"`
    Namespace          string            `json:"namespace"`
    Phase              string            `json:"phase"`  // Pass through Velero's native phases
    IncludedNamespaces []string          `json:"includedNamespaces"`
    ExcludedNamespaces []string          `json:"excludedNamespaces"`
    StorageLocation    string            `json:"storageLocation"`
    TTL                string            `json:"ttl"`
    StartTime          *time.Time        `json:"startTime,omitempty"`
    CompletionTime     *time.Time        `json:"completionTime,omitempty"`
    Expiration         *time.Time        `json:"expiration,omitempty"`
    ItemsBackedUp      int               `json:"itemsBackedUp"`
    TotalItems         int               `json:"totalItems"`
    Warnings           int               `json:"warnings"`
    Errors             int               `json:"errors"`
    ScheduleName       string            `json:"scheduleName,omitempty"`  // From label
    SnapshotVolumes    bool              `json:"snapshotVolumes"`
    Labels             map[string]string `json:"labels,omitempty"`
}

// Restore is the API response for a Velero restore
type Restore struct {
    Name               string            `json:"name"`
    Namespace          string            `json:"namespace"`
    Phase              string            `json:"phase"`  // Pass through Velero's native phases
    BackupName         string            `json:"backupName"`
    ScheduleName       string            `json:"scheduleName,omitempty"`
    IncludedNamespaces []string          `json:"includedNamespaces"`
    NamespaceMapping   map[string]string `json:"namespaceMapping,omitempty"`
    StartTime          *time.Time        `json:"startTime,omitempty"`
    CompletionTime     *time.Time        `json:"completionTime,omitempty"`
    ItemsRestored      int               `json:"itemsRestored"`
    TotalItems         int               `json:"totalItems"`
    Warnings           int               `json:"warnings"`
    Errors             int               `json:"errors"`
    FailureReason      string            `json:"failureReason,omitempty"`
}

// Schedule is the API response for a Velero schedule
type Schedule struct {
    Name               string     `json:"name"`
    Namespace          string     `json:"namespace"`
    Phase              string     `json:"phase"`  // "New", "Enabled", "FailedValidation"
    Schedule           string     `json:"schedule"`  // Cron expression
    Paused             bool       `json:"paused"`
    LastBackup         *time.Time `json:"lastBackup,omitempty"`
    NextRunTime        *time.Time `json:"nextRunTime,omitempty"`  // Computed
    IncludedNamespaces []string   `json:"includedNamespaces"`
    TTL                string     `json:"ttl"`
    StorageLocation    string     `json:"storageLocation"`
    LastBackupPhase    string     `json:"lastBackupPhase,omitempty"`
    ValidationErrors   []string   `json:"validationErrors,omitempty"`  // For FailedValidation phase
}

// BackupStorageLocation is the API response for a BSL
type BackupStorageLocation struct {
    Name           string     `json:"name"`
    Namespace      string     `json:"namespace"`
    Provider       string     `json:"provider"`
    Bucket         string     `json:"bucket"`
    Prefix         string     `json:"prefix,omitempty"`
    Phase          string     `json:"phase"`  // "Available" or "Unavailable"
    Default        bool       `json:"default"`
    LastSyncedTime *time.Time `json:"lastSyncedTime,omitempty"`
    Message        string     `json:"message,omitempty"`
}

// VolumeSnapshotLocation is the API response for a VSL
type VolumeSnapshotLocation struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace"`
    Provider  string `json:"provider"`
}

// LocationsResponse combines BSL and VSL lists
type LocationsResponse struct {
    BackupStorageLocations  []BackupStorageLocation  `json:"backupStorageLocations"`
    VolumeSnapshotLocations []VolumeSnapshotLocation `json:"volumeSnapshotLocations"`
}
```

**Discovery (`discovery.go`):**

```go
package velero

import (
    "context"
    "log/slog"
    "sync"
)

type VeleroDiscoverer struct {
    k8sClient *k8s.ClientFactory
    logger    *slog.Logger
    mu        sync.RWMutex
    status    VeleroStatus
}

func NewVeleroDiscoverer(client *k8s.ClientFactory, logger *slog.Logger) *VeleroDiscoverer

// GetStatus returns the current discovery status (thread-safe)
func (d *VeleroDiscoverer) GetStatus() VeleroStatus

// Probe checks if velero.io/v1 CRDs exist (synchronous, called on-demand)
func (d *VeleroDiscoverer) Probe(ctx context.Context) VeleroStatus
```

**Detection Mechanism (synchronous, no goroutine loop):**
1. Check for `backups.velero.io` CRD via Discovery API
2. If found, probe the `velero` namespace for the Velero deployment
3. Extract version from deployment labels
4. Count BSL and VSL objects
5. Cache result with timestamp, re-probe if stale (>5 min)

---

### Step 2: Handler (Consolidated)

**Files:**
- `backend/internal/velero/handler.go`
- `backend/internal/velero/handler_test.go`

**Handler (`handler.go`):**

All CRUD operations are inline in this file — no separate adapter files.

```go
package velero

import (
    "context"
    "log/slog"
    "net/http"
    "sync"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/robfig/cron/v3"
    "golang.org/x/sync/singleflight"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/client-go/dynamic"
)

const cacheTTL = 30 * time.Second

type Handler struct {
    ClientFactory *k8s.ClientFactory
    Discoverer    *VeleroDiscoverer
    AccessChecker *resources.AccessChecker
    AuditLogger   audit.Logger
    NotifService  *notifications.NotificationService
    Logger        *slog.Logger

    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    cachedData *cachedVeleroData
    cacheTime  time.Time
}

type cachedVeleroData struct {
    backups   []Backup
    restores  []Restore
    schedules []Schedule
    locations *LocationsResponse
}

func NewHandler(...) *Handler

// === Status ===
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request)

// === Backups ===
func (h *Handler) HandleListBackups(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleGetBackup(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleCreateBackup(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleDeleteBackup(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleGetBackupLogs(w http.ResponseWriter, r *http.Request)

// === Restores ===
func (h *Handler) HandleListRestores(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleGetRestore(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleCreateRestore(w http.ResponseWriter, r *http.Request)

// === Schedules ===
func (h *Handler) HandleListSchedules(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleGetSchedule(w http.ResponseWriter, r *http.Request)  // Added: was missing
func (h *Handler) HandleCreateSchedule(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleUpdateSchedule(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleDeleteSchedule(w http.ResponseWriter, r *http.Request)
func (h *Handler) HandleTriggerSchedule(w http.ResponseWriter, r *http.Request)

// === Locations ===
func (h *Handler) HandleListLocations(w http.ResponseWriter, r *http.Request)

// === Inline Helpers (no separate adapter files) ===
func (h *Handler) listBackups(ctx context.Context, client dynamic.Interface) ([]Backup, error)
func (h *Handler) getBackup(ctx context.Context, client dynamic.Interface, namespace, name string) (*Backup, error)
func (h *Handler) createBackup(ctx context.Context, client dynamic.Interface, backup *unstructured.Unstructured) (*Backup, error)
func (h *Handler) deleteBackup(ctx context.Context, client dynamic.Interface, namespace, name string) error
func (h *Handler) requestBackupLogs(ctx context.Context, client dynamic.Interface, namespace, name string) (string, error)

func (h *Handler) listRestores(ctx context.Context, client dynamic.Interface) ([]Restore, error)
func (h *Handler) getRestore(ctx context.Context, client dynamic.Interface, namespace, name string) (*Restore, error)
func (h *Handler) createRestore(ctx context.Context, client dynamic.Interface, restore *unstructured.Unstructured) (*Restore, error)

func (h *Handler) listSchedules(ctx context.Context, client dynamic.Interface) ([]Schedule, error)
func (h *Handler) getSchedule(ctx context.Context, client dynamic.Interface, namespace, name string) (*Schedule, error)
func (h *Handler) createSchedule(ctx context.Context, client dynamic.Interface, schedule *unstructured.Unstructured) (*Schedule, error)
func (h *Handler) updateSchedule(ctx context.Context, client dynamic.Interface, namespace, name string, spec map[string]any) error
func (h *Handler) deleteSchedule(ctx context.Context, client dynamic.Interface, namespace, name string) error
func (h *Handler) triggerSchedule(ctx context.Context, client dynamic.Interface, namespace, name string) (*Backup, error)

func (h *Handler) listLocations(ctx context.Context, client dynamic.Interface) (*LocationsResponse, error)

// === Parse Helpers ===
func parseBackup(obj *unstructured.Unstructured) Backup
func parseRestore(obj *unstructured.Unstructured) Restore
func parseSchedule(obj *unstructured.Unstructured) Schedule
func parseBSL(obj *unstructured.Unstructured) BackupStorageLocation
func parseVSL(obj *unstructured.Unstructured) VolumeSnapshotLocation

// === Cron Helper ===
func computeNextRun(cronExpr string, lastRun *time.Time) (*time.Time, error)

// === Cache ===
func (h *Handler) InvalidateCache()

// === Failure Check (called on island mount, not background loop) ===
func (h *Handler) CheckRecentFailures(ctx context.Context) []notifications.Event
```

**RBAC Model:**
- **Read operations** (list, get): Check `CanAccessGroupResource` against `backups.velero.io` or `restores.velero.io`
- **Write operations** (create, delete, trigger): Require admin role via `RequireAdmin` middleware
- Restore is a destructive operation requiring confirmation in the frontend

---

### Step 3: Routes and Server Integration

**Files:**
- `backend/internal/server/routes.go` (modify)
- `backend/internal/server/server.go` (modify)
- `backend/cmd/kubecenter/main.go` (modify)

**Routes (`routes.go`):**

```go
func (s *Server) registerVeleroRoutes(ar chi.Router) {
    if s.VeleroHandler == nil {
        return
    }
    h := s.VeleroHandler

    ar.Route("/velero", func(vr chi.Router) {
        vr.Use(middleware.RateLimit(s.veleroRL))  // Dedicated rate limiter

        // Status (discovery)
        vr.Get("/status", h.HandleStatus)

        // Backups
        vr.Get("/backups", h.HandleListBackups)
        vr.Get("/backups/{namespace}/{name}", h.HandleGetBackup)
        vr.Get("/backups/{namespace}/{name}/logs", h.HandleGetBackupLogs)
        vr.With(middleware.RequireAdmin).Post("/backups", h.HandleCreateBackup)
        vr.With(middleware.RequireAdmin).Delete("/backups/{namespace}/{name}", h.HandleDeleteBackup)

        // Restores
        vr.Get("/restores", h.HandleListRestores)
        vr.Get("/restores/{namespace}/{name}", h.HandleGetRestore)
        vr.With(middleware.RequireAdmin).Post("/restores", h.HandleCreateRestore)

        // Schedules
        vr.Get("/schedules", h.HandleListSchedules)
        vr.Get("/schedules/{namespace}/{name}", h.HandleGetSchedule)  // Added
        vr.With(middleware.RequireAdmin).Post("/schedules", h.HandleCreateSchedule)
        vr.With(middleware.RequireAdmin).Put("/schedules/{namespace}/{name}", h.HandleUpdateSchedule)
        vr.With(middleware.RequireAdmin).Delete("/schedules/{namespace}/{name}", h.HandleDeleteSchedule)
        vr.With(middleware.RequireAdmin).Post("/schedules/{namespace}/{name}/trigger", h.HandleTriggerSchedule)

        // Locations (read-only in v1)
        vr.Get("/locations", h.HandleListLocations)
    })
}
```

**API Summary (17 endpoints):**

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/velero/status` | GET | User | Discovery status |
| `/velero/backups` | GET | User | List all backups |
| `/velero/backups/{namespace}/{name}` | GET | User | Backup detail |
| `/velero/backups/{namespace}/{name}/logs` | GET | User | Backup logs (presigned URL) |
| `/velero/backups` | POST | Admin | Create backup |
| `/velero/backups/{namespace}/{name}` | DELETE | Admin | Delete backup |
| `/velero/restores` | GET | User | List all restores |
| `/velero/restores/{namespace}/{name}` | GET | User | Restore detail |
| `/velero/restores` | POST | Admin | Create restore |
| `/velero/schedules` | GET | User | List all schedules |
| `/velero/schedules/{namespace}/{name}` | GET | User | Schedule detail |
| `/velero/schedules` | POST | Admin | Create schedule |
| `/velero/schedules/{namespace}/{name}` | PUT | Admin | Update schedule |
| `/velero/schedules/{namespace}/{name}` | DELETE | Admin | Delete schedule |
| `/velero/schedules/{namespace}/{name}/trigger` | POST | Admin | Trigger on-demand backup |
| `/velero/locations` | GET | User | List BSL and VSL |

**Add to `notifications/types.go`:**
```go
const SourceVelero Source = "velero"
```

**Add to `audit/logger.go`:**
```go
const (
    ActionVeleroBackupCreate   = "velero.backup.create"
    ActionVeleroBackupDelete   = "velero.backup.delete"
    ActionVeleroRestoreCreate  = "velero.restore.create"
    ActionVeleroScheduleCreate = "velero.schedule.create"
    ActionVeleroScheduleUpdate = "velero.schedule.update"
    ActionVeleroScheduleDelete = "velero.schedule.delete"
    ActionVeleroScheduleTrigger = "velero.schedule.trigger"
)
```

---

## Phase 2: Wizard Backend

### Step 4: Wizard Input Types

**Files:**
- `backend/internal/wizard/velero.go` (single file for all 3 wizard types)
- `backend/internal/wizard/velero_test.go`
- `backend/internal/wizard/handler.go` (modify)

**Velero Wizards (`velero.go`):**

```go
package wizard

// VeleroBackupInput for backup creation wizard
type VeleroBackupInput struct {
    Name                     string            `json:"name"`
    Namespace                string            `json:"namespace"`  // Usually "velero"
    IncludedNamespaces       []string          `json:"includedNamespaces"`
    ExcludedNamespaces       []string          `json:"excludedNamespaces,omitempty"`
    IncludedResources        []string          `json:"includedResources,omitempty"`
    ExcludedResources        []string          `json:"excludedResources,omitempty"`
    LabelSelector            map[string]string `json:"labelSelector,omitempty"`
    SnapshotVolumes          bool              `json:"snapshotVolumes"`
    DefaultVolumesToFsBackup bool              `json:"defaultVolumesToFsBackup"`
    StorageLocation          string            `json:"storageLocation"`
    VolumeSnapshotLocations  []string          `json:"volumeSnapshotLocations,omitempty"`
    TTL                      string            `json:"ttl"`  // e.g., "720h"
}

func (i *VeleroBackupInput) Validate() []FieldError
func (i *VeleroBackupInput) ToYAML() (string, error)

// VeleroRestoreInput for restore creation wizard
type VeleroRestoreInput struct {
    Name                   string            `json:"name"`
    Namespace              string            `json:"namespace"`
    BackupName             string            `json:"backupName"`
    ScheduleName           string            `json:"scheduleName,omitempty"`  // Alternative to backupName
    IncludedNamespaces     []string          `json:"includedNamespaces,omitempty"`
    ExcludedNamespaces     []string          `json:"excludedNamespaces,omitempty"`
    NamespaceMapping       map[string]string `json:"namespaceMapping,omitempty"`
    ExistingResourcePolicy string            `json:"existingResourcePolicy"`  // "none" or "update"
    RestorePVs             bool              `json:"restorePVs"`
    IncludeClusterResources *bool            `json:"includeClusterResources,omitempty"`
    PreserveNodePorts      bool              `json:"preserveNodePorts"`
}

func (i *VeleroRestoreInput) Validate() []FieldError
func (i *VeleroRestoreInput) ToYAML() (string, error)

// VeleroScheduleInput for schedule creation wizard
type VeleroScheduleInput struct {
    Name                     string            `json:"name"`
    Namespace                string            `json:"namespace"`
    Schedule                 string            `json:"schedule"`  // Cron expression
    IncludedNamespaces       []string          `json:"includedNamespaces"`
    ExcludedNamespaces       []string          `json:"excludedNamespaces,omitempty"`
    IncludedResources        []string          `json:"includedResources,omitempty"`
    ExcludedResources        []string          `json:"excludedResources,omitempty"`
    LabelSelector            map[string]string `json:"labelSelector,omitempty"`
    SnapshotVolumes          bool              `json:"snapshotVolumes"`
    DefaultVolumesToFsBackup bool              `json:"defaultVolumesToFsBackup"`
    StorageLocation          string            `json:"storageLocation"`
    VolumeSnapshotLocations  []string          `json:"volumeSnapshotLocations,omitempty"`
    TTL                      string            `json:"ttl"`
    Paused                   bool              `json:"paused"`
}

func (i *VeleroScheduleInput) Validate() []FieldError
func (i *VeleroScheduleInput) ToYAML() (string, error)
```

**Handler Registration (`handler.go`):**

```go
// Add to wizard type switch
case "velero-backup":
    return h.HandlePreview(func() WizardInput { return &VeleroBackupInput{} })
case "velero-restore":
    return h.HandlePreview(func() WizardInput { return &VeleroRestoreInput{} })
case "velero-schedule":
    return h.HandlePreview(func() WizardInput { return &VeleroScheduleInput{} })
```

---

## Phase 3: Frontend Foundation

### Step 5: Types and API Client

**Files:**
- `frontend/lib/velero-types.ts`
- `frontend/lib/api.ts` (modify)

**Types (`velero-types.ts`):**

```typescript
export interface VeleroStatus {
  detected: boolean;
  namespace?: string;
  version?: string;
  bslCount: number;
  vslCount: number;
  lastChecked: string;
}

// No "Normalized" prefix — pass through Velero's native phases
export interface Backup {
  name: string;
  namespace: string;
  phase: string;  // Velero's native phases
  includedNamespaces: string[];
  excludedNamespaces: string[];
  storageLocation: string;
  ttl: string;
  startTime?: string;
  completionTime?: string;
  expiration?: string;
  itemsBackedUp: number;
  totalItems: number;
  warnings: number;
  errors: number;
  scheduleName?: string;
  snapshotVolumes: boolean;
  labels?: Record<string, string>;
}

export interface Restore {
  name: string;
  namespace: string;
  phase: string;  // Velero's native phases
  backupName: string;
  scheduleName?: string;
  includedNamespaces: string[];
  namespaceMapping?: Record<string, string>;
  startTime?: string;
  completionTime?: string;
  itemsRestored: number;
  totalItems: number;
  warnings: number;
  errors: number;
  failureReason?: string;
}

export interface Schedule {
  name: string;
  namespace: string;
  phase: string;  // "New", "Enabled", "FailedValidation"
  schedule: string;
  paused: boolean;
  lastBackup?: string;
  nextRunTime?: string;
  includedNamespaces: string[];
  ttl: string;
  storageLocation: string;
  lastBackupPhase?: string;
  validationErrors?: string[];  // For FailedValidation phase
}

export interface BackupStorageLocation {
  name: string;
  namespace: string;
  provider: string;
  bucket: string;
  prefix?: string;
  phase: string;  // "Available" or "Unavailable"
  default: boolean;
  lastSyncedTime?: string;
  message?: string;
}

export interface VolumeSnapshotLocation {
  name: string;
  namespace: string;
  provider: string;
}

export interface LocationsResponse {
  backupStorageLocations: BackupStorageLocation[];
  volumeSnapshotLocations: VolumeSnapshotLocation[];
}

// Phase badge helpers (coloring at UI level, not type level)
export function isFailedPhase(phase: string): boolean {
  return ["Failed", "FailedValidation"].includes(phase);
}

export function isWarningPhase(phase: string): boolean {
  return ["PartiallyFailed"].includes(phase);
}

export function isSuccessPhase(phase: string): boolean {
  return ["Completed", "Available", "Enabled"].includes(phase);
}

export function isProgressPhase(phase: string): boolean {
  return ["InProgress", "New", "WaitingForPluginOperations", "Finalizing"].includes(phase);
}
```

**API Client (`api.ts`):**

```typescript
export const veleroApi = {
  status: () => apiGet<VeleroStatus>("/velero/status"),
  
  // Backups
  listBackups: () => apiGet<Backup[]>("/velero/backups"),
  getBackup: (namespace: string, name: string) =>
    apiGet<Backup>(`/velero/backups/${namespace}/${name}`),
  getBackupLogs: (namespace: string, name: string) =>
    apiGet<{ url: string }>(`/velero/backups/${namespace}/${name}/logs`),
  createBackup: (data: unknown) =>
    apiPost<Backup>("/velero/backups", data),
  deleteBackup: (namespace: string, name: string) =>
    apiDelete(`/velero/backups/${namespace}/${name}`),
  
  // Restores
  listRestores: () => apiGet<Restore[]>("/velero/restores"),
  getRestore: (namespace: string, name: string) =>
    apiGet<Restore>(`/velero/restores/${namespace}/${name}`),
  createRestore: (data: unknown) =>
    apiPost<Restore>("/velero/restores", data),
  
  // Schedules
  listSchedules: () => apiGet<Schedule[]>("/velero/schedules"),
  getSchedule: (namespace: string, name: string) =>
    apiGet<Schedule>(`/velero/schedules/${namespace}/${name}`),
  createSchedule: (data: unknown) =>
    apiPost<Schedule>("/velero/schedules", data),
  updateSchedule: (namespace: string, name: string, data: unknown) =>
    apiPut(`/velero/schedules/${namespace}/${name}`, data),
  deleteSchedule: (namespace: string, name: string) =>
    apiDelete(`/velero/schedules/${namespace}/${name}`),
  triggerSchedule: (namespace: string, name: string) =>
    apiPost<Backup>(`/velero/schedules/${namespace}/${name}/trigger`, {}),
  
  // Locations
  listLocations: () => apiGet<LocationsResponse>("/velero/locations"),
};
```

---

### Step 6: Islands

**Files:**
- `frontend/islands/VeleroBackups.tsx` — Backup list + status banner + locations section
- `frontend/islands/VeleroBackupDetail.tsx` — Single backup detail with logs/warnings + inline restore form
- `frontend/islands/VeleroRestores.tsx` — Restore list
- `frontend/islands/VeleroSchedules.tsx` — Schedule list with pause/resume/trigger + CRUD
- `frontend/components/ui/VeleroBadges.tsx` — Phase badges, location status badges

**Note:** No separate VeleroDashboard or VeleroLocations islands. Status banner and locations are inline in VeleroBackups.

**VeleroBackups.tsx Pattern:**

```typescript
export default function VeleroBackups() {
  const status = useSignal<VeleroStatus | null>(null);
  const backups = useSignal<Backup[]>([]);
  const locations = useSignal<LocationsResponse | null>(null);
  const loading = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;
    Promise.all([
      veleroApi.status(),
      veleroApi.listBackups(),
      veleroApi.listLocations(),
    ]).then(([statusRes, backupsRes, locationsRes]) => {
      status.value = statusRes.data;
      backups.value = backupsRes.data ?? [];
      locations.value = locationsRes.data;
      loading.value = false;
      
      // Check for recent failures and dispatch notification on mount
      const recentFailures = backups.value.filter(
        b => isFailedPhase(b.phase) && isRecent(b.completionTime, 24)
      );
      if (recentFailures.length > 0) {
        // Dispatch notification via existing notification system
      }
    });
  }, []);

  // Poll for updates
  usePoll(() => veleroApi.listBackups(), 15_000);

  if (!status.value?.detected) {
    return (
      <div class="p-6">
        <h1 class="text-2xl font-bold">Velero</h1>
        <div class="mt-4 rounded-lg border border-warning/50 bg-warning/10 p-4">
          <p class="text-warning">Velero not detected in this cluster.</p>
          <a href="https://velero.io/docs/v1.18/basic-install/" 
             class="text-accent underline">Install Velero</a>
        </div>
      </div>
    );
  }

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold">Velero</h1>
      {/* Summary cards: BSL count, VSL count, recent backups, failed count */}
      {/* Locations section (inline, not separate page) */}
      {/* Backup table */}
    </div>
  );
}
```

**Polling Pattern (15-second interval):**

```typescript
import { usePoll } from "@/lib/hooks/use-poll.ts";

// Inside island
usePoll(() => veleroApi.listBackups(), 15_000);
```

---

### Step 7: Routes and Navigation

**Files:**
- `frontend/routes/velero/index.tsx` — Redirect to /backups
- `frontend/routes/velero/backups.tsx`
- `frontend/routes/velero/backups/[namespace]/[name].tsx`
- `frontend/routes/velero/restores.tsx`
- `frontend/routes/velero/schedules.tsx`
- `frontend/routes/velero/schedules/[namespace]/[name]/edit.tsx`
- `frontend/lib/constants.ts` (modify)
- `frontend/components/nav/SubNav.tsx` (modify)

**Navigation Structure:**

```typescript
// In constants.ts NAV_SECTIONS
{
  label: "Velero",
  icon: "Archive",  // or ShieldCheck
  href: "/velero",
  items: [
    { label: "Backups", href: "/velero/backups" },
    { label: "Restores", href: "/velero/restores" },
    { label: "Schedules", href: "/velero/schedules" },
  ],
}
```

**Command Palette Actions:**

```typescript
// In command-palette-actions.ts
{ label: "Go to Velero", href: "/velero/backups" },
{ label: "Create Backup", href: "/velero/backups/new" },
{ label: "Restore from Backup", href: "/velero/restores/new" },  // Added
{ label: "Create Schedule", href: "/velero/schedules/new" },
```

---

## Phase 4: Wizards

### Step 8: Backup Wizard (Self-Contained)

**Files:**
- `frontend/islands/BackupWizard.tsx` — Single self-contained island with inline steps
- `frontend/routes/velero/backups/new.tsx`

**Wizard Steps (inline functions, not separate files):**

| Step | Content |
|------|---------|
| 1 - Scope | Namespace selection (all or subset), resource types, label selectors |
| 2 - Volumes | Volume snapshots vs file-system backup (Kopia), VSL selection |
| 3 - Storage | BSL selection (dropdown with health badges), TTL duration picker |
| 4 - Review | YAML preview, Apply button |

**TTL Presets:**
- 24 hours, 48 hours, 7 days, 14 days, 30 days, 90 days, 180 days, 1 year, Custom

---

### Step 9: Restore Wizard (Self-Contained)

**Files:**
- `frontend/islands/RestoreWizard.tsx` — Single self-contained island with inline steps
- `frontend/routes/velero/restores/new.tsx`

**Wizard Steps (inline functions, not separate files):**

| Step | Content |
|------|---------|
| 1 - Source | Select backup from list OR "latest from schedule" |
| 2 - Mapping | Namespace remapping (source → target), add/remove rows |
| 3 - Options | Existing resource policy (none/update), restore PVs, include cluster resources |
| 4 - Review | YAML preview, **typed confirmation input**, Apply button |

**Confirmation Pattern (Step 4):**
```typescript
// User must type "restore" to enable Apply button
// For multi-namespace restores, type the primary target namespace
<input 
  type="text" 
  placeholder='Type "restore" to confirm'
  onInput={(e) => confirmationValid.value = e.target.value === "restore"}
/>
<Button disabled={!confirmationValid.value}>Apply Restore</Button>
```

---

### Step 10: Schedule Wizard (Self-Contained)

**Files:**
- `frontend/islands/ScheduleWizard.tsx` — Single self-contained island with inline steps
- `frontend/routes/velero/schedules/new.tsx`
- `frontend/routes/velero/schedules/[namespace]/[name]/edit.tsx`

**Wizard Steps (inline functions, not separate files):**

| Step | Content |
|------|---------|
| 1 - Scope | Namespace selection, resource types, label selectors |
| 2 - Volumes | Volume snapshots vs file-system backup, VSL selection |
| 3 - Storage | BSL selection, TTL duration picker |
| 4 - Cron | Cron expression with presets and human-readable preview |
| 5 - Review | YAML preview, Apply button |

**Cron Step Features:**
- Preset buttons: Hourly, Every 6h, Daily at 2AM, Weekly Sunday
- Custom cron expression input
- Human-readable preview: "Every day at 2:00 AM"
- Next 5 run times preview

**Edit Mode:**
- Same wizard, populated with existing schedule data
- PUT instead of POST on submit

---

## Phase 5: Testing

### Step 11: E2E Tests

**Files:**
- `e2e/velero.spec.ts`

**Test Cases:**
- Navigate to Velero section when Velero not installed → shows detection banner
- Navigate to backups list → table renders or empty state
- Create backup via wizard → appears in list
- View backup detail → logs tab, warnings tab
- Delete backup → confirmation dialog, removed from list
- Trigger restore from backup → restore appears in restores list
- Create schedule → appears in schedules list
- Pause/resume schedule → toggle works
- Trigger schedule → creates on-demand backup
- Locations section → shows BSL and VSL health inline on backups page

---

## File Inventory

### New Files (Backend: 6)

| Path | Purpose |
|------|---------|
| `backend/internal/velero/types.go` | Type definitions (Backup, Restore, Schedule, BSL, VSL) |
| `backend/internal/velero/discovery.go` | CRD discovery (synchronous probe) |
| `backend/internal/velero/discovery_test.go` | Discovery tests |
| `backend/internal/velero/handler.go` | HTTP handlers + inline resource helpers |
| `backend/internal/velero/handler_test.go` | Handler tests |
| `backend/internal/wizard/velero.go` | All 3 wizard input types |
| `backend/internal/wizard/velero_test.go` | Wizard input tests |

### New Files (Frontend: 12)

| Path | Purpose |
|------|---------|
| `frontend/lib/velero-types.ts` | TypeScript types + phase helpers |
| `frontend/components/ui/VeleroBadges.tsx` | Phase/status badges |
| `frontend/islands/VeleroBackups.tsx` | Backup list + status + locations |
| `frontend/islands/VeleroBackupDetail.tsx` | Backup detail + inline restore |
| `frontend/islands/VeleroRestores.tsx` | Restore list |
| `frontend/islands/VeleroSchedules.tsx` | Schedule list + CRUD |
| `frontend/islands/BackupWizard.tsx` | Backup wizard (self-contained) |
| `frontend/islands/RestoreWizard.tsx` | Restore wizard (self-contained) |
| `frontend/islands/ScheduleWizard.tsx` | Schedule wizard (self-contained) |
| `frontend/routes/velero/index.tsx` | Redirect to /backups |
| `frontend/routes/velero/backups.tsx` | Backups page |
| `frontend/routes/velero/backups/[namespace]/[name].tsx` | Backup detail |
| `frontend/routes/velero/backups/new.tsx` | Create backup |
| `frontend/routes/velero/restores.tsx` | Restores page |
| `frontend/routes/velero/restores/new.tsx` | Create restore |
| `frontend/routes/velero/schedules.tsx` | Schedules page |
| `frontend/routes/velero/schedules/new.tsx` | Create schedule |
| `frontend/routes/velero/schedules/[namespace]/[name]/edit.tsx` | Edit schedule |
| `e2e/velero.spec.ts` | E2E tests |

### Modified Files (6)

| Path | Change |
|------|--------|
| `backend/internal/server/routes.go` | Add `/velero` routes |
| `backend/internal/server/server.go` | Wire VeleroHandler |
| `backend/cmd/kubecenter/main.go` | Initialize discoverer, handler |
| `backend/internal/wizard/handler.go` | Add velero wizard types |
| `backend/internal/notifications/types.go` | Add `SourceVelero` |
| `backend/internal/audit/logger.go` | Add `ActionVelero*` constants |
| `frontend/lib/api.ts` | Add veleroApi functions |
| `frontend/lib/constants.ts` | Add Velero nav section |
| `frontend/components/nav/SubNav.tsx` | Add Velero tabs |
| `frontend/lib/command-palette-actions.ts` | Add velero actions |

**Total: ~18 new files (down from 32+)**

---

## Implementation Order

```
Phase 1 (Backend Foundation):
  Step 1: Types + Discovery              ← foundation
  Step 2: Handler (consolidated)         ← depends on Step 1
  Step 3: Routes + Server Integration    ← depends on Step 2

Phase 2 (Wizard Backend):
  Step 4: Wizard Input Types             ← depends on Phase 1

Phase 3 (Frontend Foundation):
  Step 5: Types + API Client             ← depends on Phase 1
  Step 6: Islands                        ← depends on Step 5
  Step 7: Routes + Navigation            ← depends on Step 6

Phase 4 (Wizards):
  Step 8: Backup Wizard                  ← depends on Step 4, 7
  Step 9: Restore Wizard                 ← parallel with Step 8
  Step 10: Schedule Wizard               ← parallel with Step 8

Phase 5 (Testing):
  Step 11: E2E Tests                     ← depends on Phase 4
```

---

## Scope Exclusions (v1)

The following are explicitly **out of scope** for the initial implementation:

1. **BSL/VSL Creation/Edit/Delete** — Locations are read-only. Users must create BSL/VSL via kubectl or raw YAML.
2. **Backup Hooks Configuration** — No pre/post hook UI in the wizard. Advanced users can edit YAML.
3. **Resource Modifiers** — No JSON patch configuration for restores.
4. **Backup Comparison/Diff** — Not implementing manifest diff between backups.
5. **Prometheus Metrics Dashboard** — No dedicated Velero metrics visualization.
6. **Real-time WebSocket Updates** — Using 15-second polling, not informer-based WebSocket.
7. **Background Checker Goroutine** — Failure notifications on island mount, not polling loop.

---

## Review Findings Applied

This plan was reviewed by 3 specialized agents. Key changes made:

| Finding | Resolution |
|---------|------------|
| Naming inconsistency (`velero` vs "Data Protection") | Use `/velero/*` consistently for routes and nav |
| Missing test files | Added `restore_test.go`, `schedule_test.go` → consolidated into `handler_test.go` |
| Missing GET schedule endpoint | Added `HandleGetSchedule` |
| Missing AuditLogger in Handler | Added `AuditLogger audit.Logger` field |
| Background checker unnecessary | Removed — check failures on island mount instead |
| 13 wizard step components | Removed — self-contained wizard islands with inline steps |
| Separate adapter files | Consolidated into `handler.go` with inline helpers |
| "Normalized" type prefix | Removed — `Backup`, `Restore`, `Schedule` directly |
| Phase enum types | Removed — pass through Velero's native phases, badge coloring at UI level |
| Discovery goroutine loop | Removed — synchronous probe with staleness check |
| Missing ValidationErrors | Added to Schedule type |
| Missing "Restore from Backup" action | Added to command palette |
| Separate VeleroDashboard/Locations | Consolidated into VeleroBackups |

**Result:** 32+ files → ~18 files, ~35-40% LOC reduction

---

## References

### Internal References
- `backend/internal/gitops/discovery.go` — Discovery pattern
- `backend/internal/gitops/handler.go` — Singleflight + cache pattern
- `frontend/islands/NamespaceLimitsWizard.tsx` — Self-contained wizard pattern
- `plans/namespace-limits.md` — Similar feature spec structure

### External References
- [Velero Documentation](https://velero.io/docs/v1.18/)
- [Velero API Types](https://pkg.go.dev/github.com/vmware-tanzu/velero/pkg/apis/velero/v1)
- [Velero GitHub](https://github.com/vmware-tanzu/velero)
- [Velero Backup Reference](https://velero.io/docs/main/backup-reference/)
- [Velero Restore Reference](https://velero.io/docs/main/restore-reference/)
