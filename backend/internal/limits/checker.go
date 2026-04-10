package limits

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

const (
	DefaultCheckInterval = 5 * time.Minute
)

// NotificationEmitter abstracts the notification service for testing.
type NotificationEmitter interface {
	Emit(ctx context.Context, n notifications.Notification)
}

// Checker monitors ResourceQuotas and dispatches notifications when thresholds are crossed.
type Checker struct {
	handler  *Handler
	notifier NotificationEmitter
	interval time.Duration
	logger   *slog.Logger

	mu        sync.Mutex
	lastState map[string]ThresholdStatus // key: "namespace:quotaName:resource"

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewChecker creates a Checker that monitors quotas and dispatches threshold notifications.
func NewChecker(handler *Handler, notifier NotificationEmitter, interval time.Duration, logger *slog.Logger) *Checker {
	if interval <= 0 {
		interval = DefaultCheckInterval
	}
	return &Checker{
		handler:   handler,
		notifier:  notifier,
		interval:  interval,
		logger:    logger,
		lastState: make(map[string]ThresholdStatus),
		stopCh:    make(chan struct{}),
	}
}

// Start begins the background checking loop (non-blocking).
func (c *Checker) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.run(ctx)
	c.logger.Info("limits checker started", "interval", c.interval)
}

// Stop gracefully shuts down the checker.
func (c *Checker) Stop() {
	close(c.stopCh)
	c.wg.Wait()
	c.logger.Info("limits checker stopped")
}

func (c *Checker) run(ctx context.Context) {
	defer c.wg.Done()

	// Run initial check immediately
	c.check(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

// check runs one check cycle across all quotas.
func (c *Checker) check(ctx context.Context) {
	if c.handler == nil || c.handler.Informers == nil {
		return
	}

	quotas, err := c.handler.Informers.ResourceQuotas().List(labels.Everything())
	if err != nil {
		c.logger.Error("failed to list resource quotas for threshold check", "error", err)
		return
	}

	// Track current keys to prune stale entries after iteration
	currentKeys := make(map[string]struct{}, len(quotas)*8)

	for _, quota := range quotas {
		warn, critical := ParseThresholdAnnotations(quota)
		utilization := c.handler.computeUtilization(quota)

		for resName, util := range utilization {
			key := stateKey(quota.Namespace, quota.Name, resName)
			currentKeys[key] = struct{}{}
			currentStatus := computeStatus(util.Percentage, warn, critical)

			// Only dispatch if status changed
			c.dispatchIfChanged(ctx, key, currentStatus, QuotaThresholdEvent{
				Namespace:   quota.Namespace,
				QuotaName:   quota.Name,
				Resource:    resName,
				Status:      currentStatus,
				UsedPercent: util.Percentage,
				Threshold:   thresholdForStatus(currentStatus, warn, critical),
				Used:        util.Used,
				Hard:        util.Hard,
			})
		}
	}

	// Prune stale entries for deleted quotas/resources (prevents memory leak)
	c.mu.Lock()
	for key := range c.lastState {
		if _, exists := currentKeys[key]; !exists {
			delete(c.lastState, key)
		}
	}
	c.mu.Unlock()
}

// dispatchIfChanged sends notification only when status changes.
func (c *Checker) dispatchIfChanged(ctx context.Context, key string, current ThresholdStatus, event QuotaThresholdEvent) {
	c.mu.Lock()
	previous, exists := c.lastState[key]
	c.lastState[key] = current
	c.mu.Unlock()

	// Skip if status unchanged
	if exists && previous == current {
		return
	}

	// Skip if transitioning to OK (don't spam notifications for recovery)
	// Only notify on Warning or Critical transitions
	if current == ThresholdOK {
		return
	}

	if c.notifier == nil {
		return
	}

	severity := notifications.SeverityWarning
	if current == ThresholdCritical {
		severity = notifications.SeverityCritical
	}

	title := fmt.Sprintf("Quota %s in %s: %s at %.1f%%",
		event.QuotaName, event.Namespace, event.Resource, event.UsedPercent)
	message := fmt.Sprintf("Resource %s is at %.1f%% utilization (threshold: %.0f%%). Used: %s, Hard: %s",
		event.Resource, event.UsedPercent, event.Threshold, event.Used, event.Hard)

	c.notifier.Emit(ctx, notifications.Notification{
		Source:       notifications.SourceLimits,
		Severity:     severity,
		Title:        title,
		Message:      message,
		ResourceKind: "ResourceQuota",
		ResourceNS:   event.Namespace,
		ResourceName: event.QuotaName,
	})

	c.logger.Info("quota threshold notification dispatched",
		"namespace", event.Namespace,
		"quota", event.QuotaName,
		"resource", event.Resource,
		"status", current,
		"percentage", event.UsedPercent,
	)
}

// stateKey builds composite key using null byte delimiter to avoid collisions.
// Null bytes cannot appear in Kubernetes resource names, making keys unambiguous.
func stateKey(namespace, quotaName, resource string) string {
	return namespace + "\x00" + quotaName + "\x00" + resource
}

// thresholdForStatus returns the threshold value that was crossed.
func thresholdForStatus(status ThresholdStatus, warn, critical float64) float64 {
	switch status {
	case ThresholdCritical:
		return critical
	case ThresholdWarning:
		return warn
	default:
		return 0
	}
}
