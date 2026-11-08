package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/kubecenter/kubecenter/internal/auth"
)

func testHub() *Hub {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHub(logger, &alwaysAllowChecker{})
}

type alwaysAllowChecker struct{}

func (a *alwaysAllowChecker) CanAccess(_ context.Context, _, _ string, _ []string, _, _, _ string) (bool, error) {
	return true, nil
}

func (a *alwaysAllowChecker) CanAccessGroupResource(_ context.Context, _, _ string, _ []string, _, _, _, _ string) (bool, error) {
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
	hub.subChanges <- subChange{
		client: client,
		key:    subKey{Kind: "pods", Namespace: "default"},
		add:    true,
		id:     "sub-1",
	}
	expect(t, client, MsgTypeSubscribed, "sub-1")

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

// --- Subscription changes ---------------------------------------------------
//
// These drive the real client path (handleSubscribe / handleUnsubscribe) into
// a running hub. The hub applies one client's changes in the order they were
// sent and acknowledges each applied subscribe, so waiting for a subscribe's
// reply proves every change the client sent before it has been applied.

const frameWait = 2 * time.Second

type frame struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Code int    `json:"code"`
}

func startHub(t *testing.T) *Hub {
	t.Helper()
	hub := testHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)
	return hub
}

func connect(t *testing.T, hub *Hub, name string) *Client {
	t.Helper()
	c := &Client{
		hub:    hub,
		send:   make(chan []byte, sendBufferSize),
		user:   &auth.User{Username: name, KubernetesUsername: name},
		logger: hub.logger,
	}
	hub.Register(c)
	return c
}

func sub(c *Client, id, kind, namespace string) {
	c.handleSubscribe(IncomingMessage{Type: MsgTypeSubscribe, ID: id, Kind: kind, Namespace: namespace})
}

func unsub(c *Client, id string) {
	c.handleUnsubscribe(IncomingMessage{Type: MsgTypeUnsubscribe, ID: id})
}

func next(t *testing.T, c *Client) frame {
	t.Helper()
	select {
	case data, ok := <-c.send:
		if !ok {
			t.Fatal("send channel closed")
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("decode frame %q: %v", data, err)
		}
		return f
	case <-time.After(frameWait):
		t.Fatal("no frame within timeout")
	}
	return frame{}
}

func expect(t *testing.T, c *Client, typ, id string) frame {
	t.Helper()
	f := next(t, c)
	if f.Type != typ || f.ID != id {
		t.Fatalf("got frame %+v, want type %q id %q", f, typ, id)
	}
	return f
}

func expectNone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case data := <-c.send:
		t.Fatalf("unexpected frame %s", data)
	default:
	}
}

// barrier returns once every change c sent before it has been applied.
func barrier(t *testing.T, c *Client) {
	t.Helper()
	sub(c, "barrier", "configmaps", "barrier")
	expect(t, c, MsgTypeSubscribed, "barrier")
	unsub(c, "barrier")
}

// emit broadcasts one pods/default event and returns once the control
// client, subscribed to the same key, has received it. Every other client's
// copy is sent in the same broadcast, so after this returns a missing frame
// means no subscription matched.
func emit(t *testing.T, hub *Hub, control *Client) {
	t.Helper()
	hub.events <- ResourceEvent{EventType: "ADDED", Kind: "pods", Namespace: "default", Name: "p", Object: map[string]string{"name": "p"}}
	expect(t, control, MsgTypeEvent, "control")
}

// watcher connects the control client that emit waits on.
func watcher(t *testing.T, hub *Hub) *Client {
	t.Helper()
	control := connect(t, hub, "control")
	sub(control, "control", "pods", "default")
	expect(t, control, MsgTypeSubscribed, "control")
	return control
}

func TestHub_SubUnsubSubLeavesOneLiveSubscription(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	c := connect(t, hub, "alice")

	sub(c, "a", "pods", "default")
	unsub(c, "a")
	sub(c, "a", "pods", "default")
	expect(t, c, MsgTypeSubscribed, "a")
	expect(t, c, MsgTypeSubscribed, "a")

	emit(t, hub, control)
	expect(t, c, MsgTypeEvent, "a")
	expectNone(t, c)

	// Exactly one: a single unsubscribe ends it.
	unsub(c, "a")
	barrier(t, c)
	emit(t, hub, control)
	expectNone(t, c)
}

func TestHub_SubThenUnsubLeavesNoSubscription(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	c := connect(t, hub, "alice")

	sub(c, "a", "pods", "default")
	unsub(c, "a")
	expect(t, c, MsgTypeSubscribed, "a")
	barrier(t, c)

	emit(t, hub, control)
	expectNone(t, c)
}

func TestHub_SubscribeCyclesDoNotExhaustTheCap(t *testing.T) {
	hub := startHub(t)
	c := connect(t, hub, "alice")

	for range maxSubscriptions {
		sub(c, "cycle", "pods", "default")
		unsub(c, "cycle")
	}
	for range maxSubscriptions {
		expect(t, c, MsgTypeSubscribed, "cycle")
	}

	// Every cycle closed, so the full cap is free again...
	for i := range maxSubscriptions {
		id := string(rune('A' + i))
		sub(c, id, "pods", "default")
		expect(t, c, MsgTypeSubscribed, id)
	}
	// ...and still enforced.
	sub(c, "one-too-many", "pods", "default")
	if f := expect(t, c, MsgTypeError, "one-too-many"); f.Code != 429 {
		t.Fatalf("over-cap subscribe got code %d, want 429", f.Code)
	}

	// Unsubscribing an unknown id frees nothing; a real one frees a slot.
	unsub(c, "not-subscribed")
	sub(c, "still-full", "pods", "default")
	expect(t, c, MsgTypeError, "still-full")
	unsub(c, "A")
	sub(c, "fits", "pods", "default")
	expect(t, c, MsgTypeSubscribed, "fits")
}

func TestHub_ResubscribingAnIDReplacesIt(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	c := connect(t, hub, "alice")

	sub(c, "a", "pods", "kube-system")
	sub(c, "a", "pods", "default")
	expect(t, c, MsgTypeSubscribed, "a")
	expect(t, c, MsgTypeSubscribed, "a")

	emit(t, hub, control)
	expect(t, c, MsgTypeEvent, "a")

	// The kube-system subscription went with the replacement, not orphaned.
	unsub(c, "a")
	barrier(t, c)
	hub.events <- ResourceEvent{EventType: "ADDED", Kind: "pods", Namespace: "kube-system", Name: "p", Object: map[string]string{}}
	emit(t, hub, control)
	expectNone(t, c)
}

func TestHub_UnsubscribingOneOfTwoIDsOnAKeyKeepsTheOther(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	c := connect(t, hub, "alice")

	sub(c, "a", "pods", "default")
	sub(c, "b", "pods", "default")
	expect(t, c, MsgTypeSubscribed, "a")
	expect(t, c, MsgTypeSubscribed, "b")

	unsub(c, "b")
	barrier(t, c)
	emit(t, hub, control)
	expect(t, c, MsgTypeEvent, "a")
	expectNone(t, c)
}

func TestHub_ClientsChangesAreIndependent(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	alice := connect(t, hub, "alice")
	bob := connect(t, hub, "bob")

	// Both use the same id: ids are per client.
	sub(alice, "x", "pods", "default")
	sub(bob, "x", "pods", "default")
	unsub(alice, "x")
	sub(bob, "y", "pods", "kube-system")
	unsub(bob, "y")
	expect(t, alice, MsgTypeSubscribed, "x")
	expect(t, bob, MsgTypeSubscribed, "x")
	expect(t, bob, MsgTypeSubscribed, "y")
	barrier(t, alice)
	barrier(t, bob)

	emit(t, hub, control)
	expect(t, bob, MsgTypeEvent, "x")
	expectNone(t, alice)
	expectNone(t, bob)
}

func TestHub_ChangeAfterDisconnectIsDropped(t *testing.T) {
	hub := startHub(t)
	control := watcher(t, hub)
	gone := &Client{hub: hub, send: make(chan []byte, sendBufferSize)}
	hub.Register(gone)
	hub.unregister <- gone // closes gone.send

	// A subscribe still queued when the client disconnected. Applying it would
	// ack, and later broadcast, on the closed channel, which panics the hub.
	hub.subChanges <- subChange{client: gone, key: subKey{Kind: "pods", Namespace: "default"}, add: true, id: "late"}
	barrier(t, control)
	emit(t, hub, control)
}

// toggleChecker allows access until deny is set.
type toggleChecker struct{ deny bool }

func (c *toggleChecker) CanAccess(context.Context, string, string, []string, string, string, string) (bool, error) {
	return !c.deny, nil
}

func (c *toggleChecker) CanAccessGroupResource(context.Context, string, string, []string, string, string, string, string) (bool, error) {
	return !c.deny, nil
}

func TestHub_RevocationRemovesEveryIDOnTheKeyAndFreesTheCap(t *testing.T) {
	// Calls the hub's handlers on this goroutine, as Run would.
	checker := &toggleChecker{}
	hub := NewHub(testHub().logger, checker)
	c := &Client{hub: hub, send: make(chan []byte, 64), user: &auth.User{Username: "alice"}}
	hub.clients[c] = true
	key := subKey{Kind: "pods", Namespace: "default"}
	hub.applySubChange(subChange{client: c, key: key, add: true, id: "a"})
	hub.applySubChange(subChange{client: c, key: key, add: true, id: "b"})
	hub.applySubChange(subChange{client: c, key: subKey{Kind: "alerts"}, add: true, id: "kept"})
	expect(t, c, MsgTypeSubscribed, "a")
	expect(t, c, MsgTypeSubscribed, "b")
	expect(t, c, MsgTypeSubscribed, "kept")

	checker.deny = true
	hub.revalidateSubscriptions(context.Background())

	revoked := map[string]bool{}
	for range 2 {
		f := next(t, c)
		if f.Type != MsgTypeError || f.Code != 403 {
			t.Fatalf("got %+v, want a 403 revocation", f)
		}
		revoked[f.ID] = true
	}
	if !revoked["a"] || !revoked["b"] {
		t.Fatalf("revoked %v, want a and b", revoked)
	}
	expectNone(t, c)
	if got := len(hub.clientSubs[c]); got != 1 {
		t.Fatalf("client holds %d subscriptions after revocation, want 1", got)
	}
	if _, ok := hub.subscriptions[key]; ok {
		t.Fatal("revoked key is still indexed")
	}
}
