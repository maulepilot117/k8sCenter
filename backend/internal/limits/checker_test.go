package limits

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecenter/kubecenter/internal/notifications"
)

// mockNotifier captures emitted notifications for testing.
type mockNotifier struct {
	mu            sync.Mutex
	notifications []notifications.Notification
}

func (m *mockNotifier) Emit(ctx context.Context, n notifications.Notification) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, n)
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notifications)
}

func (m *mockNotifier) get(i int) notifications.Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifications[i]
}

func TestCheckerStartStop(t *testing.T) {
	informers := newTestInformerSource(nil, nil)
	h := NewHandler(informers, &mockAccessChecker{alwaysAllow: true}, slog.Default())
	notifier := &mockNotifier{}

	checker := NewChecker(h, notifier, 100*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	checker.Start(ctx)

	// Give it time to run at least one check
	time.Sleep(150 * time.Millisecond)

	checker.Stop()

	// Should not panic or hang
}

func TestCheckerContextCancellation(t *testing.T) {
	informers := newTestInformerSource(nil, nil)
	h := NewHandler(informers, &mockAccessChecker{alwaysAllow: true}, slog.Default())
	notifier := &mockNotifier{}

	checker := NewChecker(h, notifier, time.Hour, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	checker.Start(ctx)

	// Cancel context should stop the checker
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Should not panic or hang
}

func TestDispatchIfChanged(t *testing.T) {
	notifier := &mockNotifier{}
	checker := &Checker{
		notifier:  notifier,
		lastState: make(map[string]ThresholdStatus),
		logger:    slog.Default(),
	}

	ctx := context.Background()

	// First transition to Warning - should dispatch
	checker.dispatchIfChanged(ctx, "ns:quota:cpu", ThresholdWarning, QuotaThresholdEvent{
		Namespace:   "ns",
		QuotaName:   "quota",
		Resource:    "cpu",
		Status:      ThresholdWarning,
		UsedPercent: 85,
		Threshold:   80,
		Used:        "850m",
		Hard:        "1",
	})

	if notifier.count() != 1 {
		t.Errorf("expected 1 notification, got %d", notifier.count())
	}

	// Same status again - should NOT dispatch
	checker.dispatchIfChanged(ctx, "ns:quota:cpu", ThresholdWarning, QuotaThresholdEvent{
		Namespace:   "ns",
		QuotaName:   "quota",
		Resource:    "cpu",
		Status:      ThresholdWarning,
		UsedPercent: 87,
		Threshold:   80,
	})

	if notifier.count() != 1 {
		t.Errorf("expected still 1 notification (no change), got %d", notifier.count())
	}

	// Transition to Critical - should dispatch
	checker.dispatchIfChanged(ctx, "ns:quota:cpu", ThresholdCritical, QuotaThresholdEvent{
		Namespace:   "ns",
		QuotaName:   "quota",
		Resource:    "cpu",
		Status:      ThresholdCritical,
		UsedPercent: 96,
		Threshold:   95,
	})

	if notifier.count() != 2 {
		t.Errorf("expected 2 notifications (critical transition), got %d", notifier.count())
	}

	// Transition to OK - should NOT dispatch (recovery is silent)
	checker.dispatchIfChanged(ctx, "ns:quota:cpu", ThresholdOK, QuotaThresholdEvent{
		Namespace:   "ns",
		QuotaName:   "quota",
		Resource:    "cpu",
		Status:      ThresholdOK,
		UsedPercent: 50,
		Threshold:   0,
	})

	if notifier.count() != 2 {
		t.Errorf("expected still 2 notifications (OK is silent), got %d", notifier.count())
	}
}

func TestStateKeyFormat(t *testing.T) {
	key := stateKey("my-namespace", "my-quota", "cpu")
	expected := "my-namespace:my-quota:cpu"
	if key != expected {
		t.Errorf("stateKey = %q, want %q", key, expected)
	}
}

func TestCheckerWithQuotas(t *testing.T) {
	quotas := []*corev1.ResourceQuota{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "quota1",
				Namespace: "ns1",
			},
			Status: corev1.ResourceQuotaStatus{
				Hard: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				Used: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("3.5"), // 87.5% - warning
				},
			},
		},
	}

	informers := newTestInformerSource(quotas, nil)
	h := NewHandler(informers, &mockAccessChecker{alwaysAllow: true}, slog.Default())
	notifier := &mockNotifier{}

	checker := NewChecker(h, notifier, time.Hour, slog.Default())

	// Run a single check
	checker.check(context.Background())

	// Should have dispatched a warning notification
	if notifier.count() != 1 {
		t.Errorf("expected 1 notification for warning threshold, got %d", notifier.count())
	}

	if notifier.count() > 0 {
		n := notifier.get(0)
		if n.Source != notifications.SourceLimits {
			t.Errorf("expected source %q, got %q", notifications.SourceLimits, n.Source)
		}
		if n.Severity != notifications.SeverityWarning {
			t.Errorf("expected severity %q, got %q", notifications.SeverityWarning, n.Severity)
		}
		if n.ResourceKind != "ResourceQuota" {
			t.Errorf("expected resource kind ResourceQuota, got %s", n.ResourceKind)
		}
	}

	// Run check again - no new notification (same status)
	checker.check(context.Background())
	if notifier.count() != 1 {
		t.Errorf("expected still 1 notification (no status change), got %d", notifier.count())
	}
}

func TestThresholdForStatus(t *testing.T) {
	tests := []struct {
		status   ThresholdStatus
		warn     float64
		critical float64
		expected float64
	}{
		{ThresholdOK, 80, 95, 0},
		{ThresholdWarning, 80, 95, 80},
		{ThresholdCritical, 80, 95, 95},
		{ThresholdWarning, 70, 90, 70},
		{ThresholdCritical, 70, 90, 90},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := thresholdForStatus(tt.status, tt.warn, tt.critical)
			if result != tt.expected {
				t.Errorf("thresholdForStatus(%v, %v, %v) = %v, want %v",
					tt.status, tt.warn, tt.critical, result, tt.expected)
			}
		})
	}
}

func TestCheckerNilHandler(t *testing.T) {
	notifier := &mockNotifier{}
	checker := NewChecker(nil, notifier, time.Hour, slog.Default())

	// Should not panic
	checker.check(context.Background())

	if notifier.count() != 0 {
		t.Errorf("expected 0 notifications for nil handler, got %d", notifier.count())
	}
}

func TestDefaultCheckInterval(t *testing.T) {
	checker := NewChecker(nil, nil, 0, slog.Default())
	if checker.interval != DefaultCheckInterval {
		t.Errorf("expected default interval %v, got %v", DefaultCheckInterval, checker.interval)
	}
}
