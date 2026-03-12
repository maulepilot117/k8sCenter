package resources

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kubecenter/kubecenter/pkg/api"
)

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
	nextID int
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

	tm.nextID++
	id := "task-" + time.Now().Format("20060102150405") + "-" + itoa(tm.nextID)
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

// HandleGetTask handles GET /api/v1/tasks/:taskID.
func (h *Handler) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, ok := h.TaskManager.Get(taskID)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found", "task ID: "+taskID)
		return
	}
	writeJSON(w, http.StatusOK, api.Response{Data: task})
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
