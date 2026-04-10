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
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	Status    TaskStatus `json:"status"`
	Message   string     `json:"message,omitempty"`
	Progress  int        `json:"progress"` // 0-100
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	User      string     `json:"user"`
}

// TaskManager tracks long-running operations.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// HasActiveTask returns true if there is a running or pending task of the given
// kind for the given name. Used to prevent duplicate drain operations.
func (tm *TaskManager) HasActiveTask(kind, name string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for _, t := range tm.tasks {
		if t.Kind == kind && t.Name == name && (t.Status == TaskStatusPending || t.Status == TaskStatusRunning) {
			return true
		}
	}
	return false
}

// NewTaskManager creates a new TaskManager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

// Create registers a new task and returns its ID.
func (tm *TaskManager) Create(kind, name, namespace, user string) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	id := generateTaskID()
	tm.tasks[id] = &Task{
		ID:        id,
		Kind:      kind,
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

// UpdateStatus updates the status and message of a task.
func (tm *TaskManager) UpdateStatus(id string, status TaskStatus, message string, progress int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t, ok := tm.tasks[id]
	if !ok {
		return
	}
	t.Status = status
	t.Message = message
	t.Progress = progress
	if status == TaskStatusComplete || status == TaskStatusFailed {
		now := timeNow()
		t.EndedAt = &now
	}
}

// StartReaper runs a background goroutine that periodically removes completed
// tasks older than completedTaskTTL. Stops when ctx is cancelled.
func (tm *TaskManager) StartReaper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tm.reapCompleted()
			}
		}
	}()
}

func (tm *TaskManager) reapCompleted() {
	now := timeNow()
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for id, t := range tm.tasks {
		if (t.Status == TaskStatusComplete || t.Status == TaskStatusFailed) && t.EndedAt != nil && now.Sub(*t.EndedAt) > completedTaskTTL {
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
