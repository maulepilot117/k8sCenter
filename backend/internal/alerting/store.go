package alerting

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AlertEvent represents a single alert occurrence in the store.
type AlertEvent struct {
	ID          string            `json:"id"`
	ClusterID   string            `json:"clusterID"`
	Fingerprint string            `json:"fingerprint"`
	Status      string            `json:"status"` // "firing" or "resolved"
	AlertName   string            `json:"alertName"`
	Namespace   string            `json:"namespace"`
	Severity    string            `json:"severity"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt,omitzero"`
	ReceivedAt  time.Time         `json:"receivedAt"`
	ResolvedAt  time.Time         `json:"resolvedAt,omitzero"`
}

// ListOptions configures alert history queries.
type ListOptions struct {
	Namespace string
	AlertName string
	Severity  string
	Status    string // "firing", "resolved", or "" for all
	Since     time.Time
	Until     time.Time
	Limit     int
	Continue  string // opaque cursor (base64-encoded "receivedAtNanos:id")
}

// Store is the interface for alert persistence.
type Store interface {
	Record(ctx context.Context, event AlertEvent) error
	ActiveAlerts(ctx context.Context) ([]AlertEvent, error)
	List(ctx context.Context, opts ListOptions) (items []AlertEvent, continueToken string, err error)
	Resolve(ctx context.Context, fingerprint string, endsAt time.Time) error
	Prune(ctx context.Context, olderThan time.Time) (int, error)
}

const (
	maxHistoryEntries = 10000
	defaultListLimit  = 50
)

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu      sync.RWMutex
	active  map[string]*AlertEvent // keyed by fingerprint
	history []AlertEvent           // sorted by receivedAt descending
	nextID  int64
}

// NewMemoryStore creates a new in-memory alert store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		active: make(map[string]*AlertEvent),
	}
}

// Record stores a new alert event in both active and history.
func (s *MemoryStore) Record(_ context.Context, event AlertEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	event.ID = fmt.Sprintf("alert-%d", s.nextID)
	event.ReceivedAt = time.Now().UTC()

	if event.Status == "firing" {
		s.active[event.Fingerprint] = &event
	}

	// Append to history (we sort on read)
	s.history = append(s.history, event)

	// Evict oldest if over cap
	if len(s.history) > maxHistoryEntries {
		trimmed := make([]AlertEvent, maxHistoryEntries)
		copy(trimmed, s.history[len(s.history)-maxHistoryEntries:])
		s.history = trimmed
	}

	return nil
}

// ActiveAlerts returns all currently firing alerts.
func (s *MemoryStore) ActiveAlerts(_ context.Context) ([]AlertEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AlertEvent, 0, len(s.active))
	for _, a := range s.active {
		result = append(result, *a)
	}

	// Sort by startsAt descending for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartsAt.After(result[j].StartsAt)
	})

	return result, nil
}

// List returns paginated alert history matching the given options.
func (s *MemoryStore) List(_ context.Context, opts ListOptions) ([]AlertEvent, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}

	// Decode continue cursor (composite: "receivedAtNanos:id")
	var cursorTime time.Time
	var cursorID string
	if opts.Continue != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.Continue)
		if err != nil {
			return nil, "", fmt.Errorf("invalid continue token")
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		nanos, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("invalid continue token")
		}
		cursorTime = time.Unix(0, nanos)
		if len(parts) == 2 {
			cursorID = parts[1]
		}
	}

	var result []AlertEvent
	// Iterate in reverse (newest first)
	for i := len(s.history) - 1; i >= 0; i-- {
		event := s.history[i]
		// Skip entries at or before cursor (using composite key to handle timestamp collisions)
		if !cursorTime.IsZero() {
			if event.ReceivedAt.After(cursorTime) {
				continue
			}
			if event.ReceivedAt.Equal(cursorTime) && (cursorID == "" || event.ID >= cursorID) {
				continue
			}
		}

		// Apply filters
		if opts.Namespace != "" && event.Namespace != opts.Namespace {
			continue
		}
		if opts.AlertName != "" && !strings.Contains(strings.ToLower(event.AlertName), strings.ToLower(opts.AlertName)) {
			continue
		}
		if opts.Severity != "" && event.Severity != opts.Severity {
			continue
		}
		if opts.Status != "" && event.Status != opts.Status {
			continue
		}
		if !opts.Since.IsZero() && event.ReceivedAt.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && event.ReceivedAt.After(opts.Until) {
			continue
		}

		result = append(result, event)
		if len(result) >= limit+1 {
			break
		}
	}

	// Determine continue token (composite: "receivedAtNanos:id")
	var continueToken string
	if len(result) > limit {
		result = result[:limit]
		last := result[len(result)-1]
		cursor := strconv.FormatInt(last.ReceivedAt.UnixNano(), 10) + ":" + last.ID
		continueToken = base64.StdEncoding.EncodeToString([]byte(cursor))
	}

	return result, continueToken, nil
}

// Resolve marks an alert as resolved and adds a resolved event to history.
func (s *MemoryStore) Resolve(_ context.Context, fingerprint string, endsAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, exists := s.active[fingerprint]
	if !exists {
		return nil // already resolved or never seen
	}

	now := time.Now().UTC()
	resolved := *active
	resolved.Status = "resolved"
	resolved.EndsAt = endsAt
	resolved.ResolvedAt = now
	resolved.ReceivedAt = now
	s.nextID++
	resolved.ID = fmt.Sprintf("alert-%d", s.nextID)

	delete(s.active, fingerprint)

	// Add resolved event to history
	s.history = append(s.history, resolved)
	if len(s.history) > maxHistoryEntries {
		trimmed := make([]AlertEvent, maxHistoryEntries)
		copy(trimmed, s.history[len(s.history)-maxHistoryEntries:])
		s.history = trimmed
	}

	return nil
}

// Prune removes history entries and stale active alerts older than the given time.
func (s *MemoryStore) Prune(_ context.Context, olderThan time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]AlertEvent, 0, len(s.history))
	pruned := 0
	for _, event := range s.history {
		if event.ReceivedAt.Before(olderThan) {
			pruned++
			continue
		}
		kept = append(kept, event)
	}
	s.history = kept

	// Also prune stale active alerts that were never resolved
	// (e.g., Alertmanager restarted and never sent a resolved notification)
	for fp, alert := range s.active {
		if alert.ReceivedAt.Before(olderThan) {
			delete(s.active, fp)
			pruned++
		}
	}

	return pruned, nil
}

// RunPruner starts a background goroutine that prunes old entries hourly.
func (s *MemoryStore) RunPruner(ctx context.Context, retentionDays int, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
			pruned, err := s.Prune(ctx, cutoff)
			if err != nil {
				logger.Error("alert store prune failed", "error", err)
				continue
			}
			if pruned > 0 {
				logger.Info("alert store pruned", "removed", pruned, "retentionDays", retentionDays)
			}
		}
	}
}
