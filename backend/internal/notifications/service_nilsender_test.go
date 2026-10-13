package notifications

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelDispatchPanicRecoveredAndSlotReleased(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, quietLogger())
	svc.sem = make(chan struct{}, 1) // one slot: a leaked slot would block the second send
	svc.rules = []Rule{{ID: "r", Enabled: true, ChannelID: "c"}}
	svc.channels = []Channel{{ID: "c", Name: "c", Type: ChannelSlack}}

	var calls atomic.Int32
	second := make(chan struct{})
	svc.dispatchHook = func(context.Context, Channel, Notification) error {
		if calls.Add(1) == 1 {
			panic("send exploded")
		}
		close(second)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runDispatcher(ctx)

	svc.queue <- Notification{Title: "one"}
	svc.queue <- Notification{Title: "two"}

	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("second dispatch never ran: slot leaked or dispatcher stopped")
	}
}

// panicSender mimics *alerting.Notifier before the fix: SMTPConfigured
// dereferences its receiver, so a typed-nil pointer panics.
type panicSender struct{ configured bool }

func (p *panicSender) SMTPConfigured() bool                      { return p.configured }
func (p *panicSender) QueueEmail([]string, string, string) error { return nil }

type alwaysPanicSender struct{}

func (alwaysPanicSender) SMTPConfigured() bool                      { panic("boom") }
func (alwaysPanicSender) QueueEmail([]string, string, string) error { return nil }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTypedNilEmailSenderIsNormalised(t *testing.T) {
	var typedNil *panicSender
	svc := NewService(nil, nil, typedNil, nil, quietLogger())
	if svc.emailSender != nil {
		t.Fatal("typed-nil sender must be normalised to a nil interface")
	}
	// Neither path may call the nil receiver.
	svc.sendDigests(context.Background())
	if err := svc.sendTestEmail(Channel{Name: "x"}); err == nil {
		t.Fatal("sendTestEmail with no sender must return an error")
	}
}

func TestDigestPanicIsRecovered(t *testing.T) {
	svc := NewService(nil, nil, alwaysPanicSender{}, nil, quietLogger())
	svc.safeSendDigests(context.Background()) // a panic here would crash the test binary
}
