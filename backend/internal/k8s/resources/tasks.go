package resources

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/internal/k8s"
	"github.com/kubecenter/kubecenter/pkg/api"
)

const completedTaskTTL = 1 * time.Hour

// TaskStatus represents the state of a long-running operation.
type TaskStatus string

const (
	TaskStatusPending  TaskStatus = "pending"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusComplete TaskStatus = "complete"
	TaskStatusFailed   TaskStatus = "failed"
)

// Task represents a long-running operation (e.g., node drain).
type Task struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// ClusterID is the normalized id of the cluster the task acts on. Two
	// clusters can hold same-named objects, so a task is identified by
	// (cluster, kind, name), never by name alone.
	ClusterID string     `json:"clusterId"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	Status    TaskStatus `json:"status"`
	Message   string     `json:"message,omitempty"`
	Progress  int        `json:"progress"` // 0-100
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	User      string     `json:"user"`
}

// taskClusterRemovedMessage is the terminal message of a task cancelled
// because ClusterRouter evicted its cluster.
const taskClusterRemovedMessage = "cancelled: cluster removed while the task was running"

// TaskManager tracks long-running operations.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	// cancels holds the cancel func of each running task that registered one
	// via SetCancel, so CancelCluster can stop work on a removed cluster.
	cancels map[string]context.CancelFunc
}

func isActive(t *Task) bool {
	return t.Status == TaskStatusPending || t.Status == TaskStatusRunning
}

// HasActiveTask returns true if there is a running or pending task of the
// given kind for the given name on the given cluster. Used to prevent
// duplicate drain operations.
func (tm *TaskManager) HasActiveTask(kind, clusterID, name string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.hasActiveLocked(kind, k8s.NormalizedClusterID(clusterID), name)
}

// hasActiveLocked reports whether an active task of kind exists for name on
// the normalized clusterID. Caller holds tm.mu.
func (tm *TaskManager) hasActiveLocked(kind, clusterID, name string) bool {
	for _, t := range tm.tasks {
		if t.Kind == kind && t.ClusterID == clusterID && t.Name == name && isActive(t) {
			return true
		}
	}
	return false
}

// NewTaskManager creates a new TaskManager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks:   make(map[string]*Task),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Create registers a new task on the given cluster and returns its ID.
func (tm *TaskManager) Create(kind, clusterID, name, namespace, user string) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.createLocked(kind, k8s.NormalizedClusterID(clusterID), name, namespace, user)
}

// CreateIfNoActive registers a new task unless one of the same kind is
// already active for name on clusterID. The check and the insert happen under
// one lock, so two concurrent requests cannot both start the same operation.
// It returns the new task's ID and true, or "" and false.
func (tm *TaskManager) CreateIfNoActive(kind, clusterID, name, namespace, user string) (string, bool) {
	clusterID = k8s.NormalizedClusterID(clusterID)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.hasActiveLocked(kind, clusterID, name) {
		return "", false
	}
	return tm.createLocked(kind, clusterID, name, namespace, user), true
}

// createLocked inserts a pending task. clusterID is already normalized.
// Caller holds tm.mu.
func (tm *TaskManager) createLocked(kind, clusterID, name, namespace, user string) string {
	tm.reapCompletedLocked()

	id := generateTaskID()
	tm.tasks[id] = &Task{
		ID:        id,
		Kind:      kind,
		ClusterID: clusterID,
		Name:      name,
		Namespace: namespace,
		Status:    TaskStatusPending,
		StartedAt: timeNow(),
		User:      user,
	}
	return id
}

// generateTaskID returns a cryptographically random task ID.
func generateTaskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if crypto/rand fails (should not happen)
		return "task-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "task-" + hex.EncodeToString(b)
}

// Get returns a task by ID.
func (tm *TaskManager) Get(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// UpdateStatus updates the status and message of a task. A task that has
// already finished keeps its outcome: a late write from work that was
// cancelled underneath it must not overwrite why it ended.
func (tm *TaskManager) UpdateStatus(id string, status TaskStatus, message string, progress int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t, ok := tm.tasks[id]
	if !ok || !isActive(t) {
		return
	}
	t.Status = status
	t.Message = message
	t.Progress = progress
	if !isActive(t) {
		tm.finishLocked(t)
	}
}

// finishLocked stamps the end time and releases the task's cancel func.
// Caller holds tm.mu.
func (tm *TaskManager) finishLocked(t *Task) {
	now := timeNow()
	t.EndedAt = &now
	if cancel, ok := tm.cancels[t.ID]; ok {
		delete(tm.cancels, t.ID)
		cancel()
	}
}

// SetCancel registers the cancel func that stops a task's work. It is called
// when the task finishes or its cluster is removed. A task that has already
// finished is cancelled immediately.
func (tm *TaskManager) SetCancel(id string, cancel context.CancelFunc) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if t, ok := tm.tasks[id]; ok && isActive(t) {
		tm.cancels[id] = cancel
		return
	}
	cancel()
}

// CancelCluster fails every active task on clusterID and cancels its work.
// Registered as a ClusterRouter evict hook, so any EvictCluster call (today,
// deregistering the cluster) stops drains still running against it. It does
// not block.
func (tm *TaskManager) CancelCluster(clusterID string) {
	clusterID = k8s.NormalizedClusterID(clusterID)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, t := range tm.tasks {
		if t.ClusterID != clusterID || !isActive(t) {
			continue
		}
		t.Status = TaskStatusFailed
		t.Message = taskClusterRemovedMessage
		tm.finishLocked(t)
	}
}

// reapCompletedLocked removes finished tasks older than completedTaskTTL.
// Create runs it, so the table stays bounded by recent activity without a
// background goroutine. Caller holds tm.mu.
func (tm *TaskManager) reapCompletedLocked() {
	now := timeNow()
	for id, t := range tm.tasks {
		if !isActive(t) && t.EndedAt != nil && now.Sub(*t.EndedAt) > completedTaskTTL {
			delete(tm.tasks, id)
		}
	}
}

// HandleGetTask handles GET /api/v1/tasks/:taskID.
// Only the task owner can view their tasks.
func (h *Handler) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskID")
	task, found := h.TaskManager.Get(taskID)
	if !found || task.User != user.Username {
		writeError(w, http.StatusNotFound, "task not found", "")
		return
	}
	writeJSON(w, http.StatusOK, api.Response{Data: task})
}
