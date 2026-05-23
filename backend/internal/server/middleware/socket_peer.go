package middleware

import (
	"context"
	"net/http"
)

// socketPeerContextKey is an unexported type for the socket-peer context key,
// preventing collisions with keys defined in other packages.
type socketPeerContextKey struct{}

// CaptureSocketPeer is a middleware that must be registered BEFORE chi's
// RealIP middleware in the handler chain. It captures the raw TCP peer
// address (r.RemoteAddr as set by net/http from the socket layer) into the
// request context before RealIP has a chance to overwrite RemoteAddr with a
// value sourced from attacker-controlled headers (X-Forwarded-For,
// X-Real-IP).
//
// The captured value is available via SocketPeerFromContext. Callers that
// need the true origin of a request (e.g., the loopback-setup gate, audit
// logging) should use this helper instead of r.RemoteAddr so that an
// attacker cannot spoof the loopback check by injecting
// "X-Forwarded-For: 127.0.0.1" from a non-loopback connection.
//
// Security note: RealIP rewriting is still useful for downstream handlers
// that want the client's logical IP (e.g., rate limiting behind a trusted
// load balancer). CaptureSocketPeer does not disable RealIP — it just
// preserves the ground truth before the rewrite.
func CaptureSocketPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), socketPeerContextKey{}, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SocketPeerFromContext returns the original TCP peer address stored by
// CaptureSocketPeer before chi's RealIP rewrote r.RemoteAddr. Returns an
// empty string if the middleware was not in the chain (e.g., in unit tests
// that construct requests without routing through the full middleware stack).
//
// Callers should fall back to r.RemoteAddr when an empty string is returned
// so that tests using bare httptest.NewRequest still work. Log a warning in
// production paths to surface misconfigured middleware order.
func SocketPeerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(socketPeerContextKey{}).(string)
	return v
}
