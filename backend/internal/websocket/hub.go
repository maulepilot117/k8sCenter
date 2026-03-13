package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"
)

// AccessChecker verifies RBAC permissions for WebSocket subscriptions.
type AccessChecker interface {
	CanAccess(ctx context.Context, username string, groups []string, verb, resource, namespace string) (bool, error)
}

// subChange represents a subscription addition.
type subChange struct {
	client *Client
	key    subKey
	add    bool   // true = subscribe, false = unsubscribe (legacy, kept for removeClient)
	id     string // subscription ID
}

// unsubRequest represents a request to remove a subscription by ID.
type unsubRequest struct {
	client *Client
	id     string
}

// MaxClients is the maximum number of concurrent WebSocket connections.
const MaxClients = 1000

// Hub maintains active clients and fans out informer events
// to clients based on their subscriptions. All map mutations
// happen in the single Run goroutine via channels.
type Hub struct {
	clients       map[*Client]bool
	subscriptions map[subKey]map[*Client]string // subKey → client → subscription ID
	clientCount   atomic.Int32

	register   chan *Client
	unregister chan *Client
	events     chan ResourceEvent
	addSub     chan subChange
	unsubByID  chan unsubRequest

	accessChecker AccessChecker
	logger        *slog.Logger
}

// ClientCount returns the current number of connected WebSocket clients (thread-safe).
func (h *Hub) ClientCount() int32 {
	return h.clientCount.Load()
}

// NewHub creates a new Hub.
func NewHub(logger *slog.Logger, accessChecker AccessChecker) *Hub {
	return &Hub{
		clients:       make(map[*Client]bool),
		subscriptions: make(map[subKey]map[*Client]string),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		events:        make(chan ResourceEvent, 1024),
		addSub:        make(chan subChange, 256),
		unsubByID:     make(chan unsubRequest, 256),
		accessChecker: accessChecker,
		logger:        logger,
	}
}

// Events returns the channel for feeding informer events into the hub.
func (h *Hub) Events() chan<- ResourceEvent {
	return h.events
}

// HandleEvent is an EventCallback-compatible function that feeds events into the hub.
// It performs a non-blocking send to the events channel.
func (h *Hub) HandleEvent(eventType, kind, namespace, name string, obj any) {
	event := ResourceEvent{
		EventType: eventType,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		Object:    obj,
	}
	select {
	case h.events <- event:
	default:
		h.logger.Warn("event channel full, dropping event",
			"kind", kind, "name", name, "eventType", eventType)
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// rbacRevalidateInterval is how often the hub rechecks RBAC for active subscriptions.
const rbacRevalidateInterval = 5 * time.Minute

// Run is the main hub goroutine. Call as `go hub.Run(ctx)`.
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("websocket hub started")
	defer h.logger.Info("websocket hub stopped")

	rbacTicker := time.NewTicker(rbacRevalidateInterval)
	defer rbacTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return

		case <-rbacTicker.C:
			h.revalidateSubscriptions(ctx)

		case client := <-h.register:
			h.clients[client] = true
			h.clientCount.Add(1)
			h.logger.Info("client connected",
				"user", client.username(),
				"clients", len(h.clients),
			)

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				h.removeClient(client)
				h.logger.Info("client disconnected",
					"user", client.username(),
					"clients", len(h.clients),
				)
			}

		case change := <-h.addSub:
			if h.subscriptions[change.key] == nil {
				h.subscriptions[change.key] = make(map[*Client]string)
			}
			h.subscriptions[change.key][change.client] = change.id

		case req := <-h.unsubByID:
			for key, subs := range h.subscriptions {
				if subID, ok := subs[req.client]; ok && subID == req.id {
					delete(subs, req.client)
					if len(subs) == 0 {
						delete(h.subscriptions, key)
					}
					break
				}
			}

		case event := <-h.events:
			h.broadcastEvent(event)
		}
	}
}

// broadcastEvent sends an event to all clients subscribed to the event's kind+namespace.
func (h *Hub) broadcastEvent(event ResourceEvent) {
	// Collect target clients with their subscription IDs.
	// A client may match via exact namespace or all-namespace subscription;
	// prefer the exact match.
	type target struct {
		client *Client
		subID  string
	}
	targets := make(map[*Client]target)

	exactKey := subKey{Kind: event.Kind, Namespace: event.Namespace}
	for client, subID := range h.subscriptions[exactKey] {
		targets[client] = target{client: client, subID: subID}
	}

	allNsKey := subKey{Kind: event.Kind, Namespace: ""}
	for client, subID := range h.subscriptions[allNsKey] {
		if _, exists := targets[client]; !exists {
			targets[client] = target{client: client, subID: subID}
		}
	}

	if len(targets) == 0 {
		return
	}

	// Pre-serialize the object once for all clients (this is the expensive part).
	rawObj, err := json.Marshal(event.Object)
	if err != nil {
		h.logger.Error("failed to marshal event object", "error", err, "kind", event.Kind)
		return
	}

	// rawOutgoing is a lightweight wrapper that uses pre-serialized object bytes.
	type rawOutgoing struct {
		Type      string          `json:"type"`
		ID        string          `json:"id,omitempty"`
		EventType string          `json:"eventType,omitempty"`
		Object    json.RawMessage `json:"object,omitempty"`
	}

	for _, t := range targets {
		msg := rawOutgoing{
			Type:      MsgTypeEvent,
			ID:        t.subID,
			EventType: event.EventType,
			Object:    rawObj,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			h.logger.Error("failed to marshal event wrapper", "error", err, "kind", event.Kind)
			continue
		}

		// Non-blocking send — if the client's buffer is full, send a resync
		// notification instead of disconnecting (P2-078).
		select {
		case t.client.send <- data:
		default:
			h.sendResync(t.client, t.subID, event.Kind)
			h.logger.Warn("event buffer full, sent resync_required",
				"user", t.client.username(), "kind", event.Kind, "subID", t.subID)
		}
	}
}

// revalidateSubscriptions re-checks RBAC for all active subscriptions and removes
// any where the user no longer has access (P2-082).
func (h *Hub) revalidateSubscriptions(ctx context.Context) {
	if h.accessChecker == nil {
		return
	}

	type removal struct {
		key    subKey
		client *Client
		subID  string
	}
	var removals []removal

	for key, subs := range h.subscriptions {
		for client, subID := range subs {
			if client.user == nil {
				continue
			}
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			allowed, err := h.accessChecker.CanAccess(
				checkCtx,
				client.user.KubernetesUsername,
				client.user.KubernetesGroups,
				"list",
				key.Kind,
				key.Namespace,
			)
			cancel()
			if err != nil {
				h.logger.Warn("RBAC revalidation failed, keeping subscription",
					"error", err, "user", client.username(),
					"kind", key.Kind, "namespace", key.Namespace)
				continue
			}
			if !allowed {
				removals = append(removals, removal{key: key, client: client, subID: subID})
			}
		}
	}

	for _, r := range removals {
		if subs, ok := h.subscriptions[r.key]; ok {
			delete(subs, r.client)
			if len(subs) == 0 {
				delete(h.subscriptions, r.key)
			}
		}
		// Notify client their subscription was revoked
		msg := OutgoingMessage{
			Type:    MsgTypeError,
			ID:      r.subID,
			Code:    403,
			Message: "subscription revoked: access denied",
		}
		if data, err := MarshalOutgoing(msg); err == nil {
			select {
			case r.client.send <- data:
			default:
			}
		}
		h.logger.Info("subscription revoked by RBAC revalidation",
			"user", r.client.username(), "kind", r.key.Kind,
			"namespace", r.key.Namespace, "subID", r.subID)
	}
}

// sendResync sends a resync_required message to a slow client, telling it to re-fetch via REST.
// If even this message cannot be buffered, the client is disconnected as a last resort.
func (h *Hub) sendResync(client *Client, subID, kind string) {
	msg := OutgoingMessage{
		Type:    MsgTypeResyncRequired,
		ID:      subID,
		Message: kind,
	}
	data, err := MarshalOutgoing(msg)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
		// Client is completely stuck — disconnect
		h.removeClient(client)
		h.logger.Warn("client unresponsive after resync attempt, disconnected",
			"user", client.username())
	}
}

// removeClient removes a client from all subscriptions and closes its send channel.
func (h *Hub) removeClient(client *Client) {
	for key, subs := range h.subscriptions {
		delete(subs, client)
		if len(subs) == 0 {
			delete(h.subscriptions, key)
		}
	}
	delete(h.clients, client)
	h.clientCount.Add(-1)
	close(client.send)
}

// shutdown gracefully closes all client connections.
func (h *Hub) shutdown() {
	for client := range h.clients {
		h.removeClient(client)
	}
}
