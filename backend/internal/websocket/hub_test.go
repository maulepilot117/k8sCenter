package websocket

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testHub() *Hub {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHub(logger, &alwaysAllowChecker{})
}

type alwaysAllowChecker struct{}

func (a *alwaysAllowChecker) CanAccess(_ context.Context, _ string, _ []string, _, _, _ string) (bool, error) {
	return true, nil
}

func (a *alwaysAllowChecker) CanAccessGroupResource(_ context.Context, _ string, _ []string, _, _, _, _ string) (bool, error) {
	return true, nil
}

func TestHub_ClientRegistration(t *testing.T) {
	hub := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", hub.ClientCount())
	}

	// Create a fake client (no actual WebSocket connection needed for registration test)
	client := &Client{
		hub:  hub,
		send: make(chan []byte, 256),
	}

	hub.register <- client
	time.Sleep(50 * time.Millisecond) // let the hub process

	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client after register, got %d", hub.ClientCount())
	}

	hub.unregister <- client
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after unregister, got %d", hub.ClientCount())
	}
}

func TestHub_EventBroadcast(t *testing.T) {
	hub := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	// Register a client with a subscription
	client := &Client{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(50 * time.Millisecond)

	// Subscribe to pods in default namespace
	hub.addSub <- subChange{
		client: client,
		key:    subKey{Kind: "pods", Namespace: "default"},
		id:     "sub-1",
	}
	time.Sleep(50 * time.Millisecond)

	// Send an event that matches
	hub.events <- ResourceEvent{
		EventType: "ADDED",
		Kind:      "pods",
		Namespace: "default",
		Name:      "test-pod",
		Object:    map[string]string{"name": "test-pod"},
	}
	time.Sleep(50 * time.Millisecond)

	// Client should have received a message
	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("received empty message")
		}
	default:
		t.Error("expected message on client send channel, got none")
	}

	// Send event for different namespace — should NOT reach client
	hub.events <- ResourceEvent{
		EventType: "ADDED",
		Kind:      "pods",
		Namespace: "kube-system",
		Name:      "other-pod",
		Object:    map[string]string{"name": "other-pod"},
	}
	time.Sleep(50 * time.Millisecond)

	select {
	case <-client.send:
		t.Error("client should NOT have received event for different namespace")
	default:
		// correct — no message
	}
}

func TestHub_ContextCancellationStopsRun(t *testing.T) {
	hub := testHub()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	// Cancel context — Run should return
	cancel()

	select {
	case <-done:
		// success — Run exited
	case <-time.After(2 * time.Second):
		t.Fatal("hub.Run did not exit after context cancellation")
	}
}

func TestHub_HandleEventNonBlocking(t *testing.T) {
	hub := testHub()

	// Fill the event channel
	for i := 0; i < 1024; i++ {
		hub.HandleEvent("ADDED", "pods", "default", "pod", nil)
	}

	// The 1025th event should be dropped (non-blocking), not block
	done := make(chan struct{})
	go func() {
		hub.HandleEvent("ADDED", "pods", "default", "overflow-pod", nil)
		close(done)
	}()

	select {
	case <-done:
		// success — did not block
	case <-time.After(1 * time.Second):
		t.Fatal("HandleEvent blocked on full channel")
	}
}
