// Generic WebSocket client wrapper. Handles the auth-in-band handshake
// the backend's `wsAuthAndUpgrade` expects (`{"type":"auth","token":...}`),
// then yields parsed JSON messages as a broadcast Stream so multiple
// listeners (UI + side-effect handlers) can attach.
//
// Reconnect uses capped exponential backoff (1s → 2s → 4s → 8s → 16s,
// max 30s) and clears on a successful connect. Caller is responsible for
// disposing the client when its associated provider/widget tears down.
//
// Backend protocol per `backend/internal/server/handle_ws_logs.go` and
// `backend/internal/websocket/events.go`:
//   1. Open WS connection
//   2. Send {"type":"auth","token":"<jwt>"}
//   3. (Optional) send a per-endpoint subscribe/filter message
//   4. Receive {"type":"<event>","data":...} or {"type":"error","message":...}
//      until the server emits {"type":"end"} or the socket closes.

import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/io.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../api/auth_token_holder.dart';
import '../api/dio_client.dart';
import '../cluster/cluster_provider.dart';

/// Lifecycle states emitted on the [KubeWebSocketClient.state] stream.
enum WebSocketState {
  connecting,
  open,
  reconnecting,
  closed,
  failed,
}

/// Auth/error reasons the client can surface without leaking JWT material.
class WebSocketError {
  const WebSocketError(this.message, {this.fatal = false});
  final String message;

  /// When true, the client will NOT attempt to reconnect — typical for
  /// auth rejections that won't recover from a retry on the same token.
  final bool fatal;

  @override
  String toString() => 'WebSocketError($message, fatal=$fatal)';
}

/// Wraps a single WebSocket subscription. Created via [KubeWebSocketClient].
/// Cancel by calling [close]; the broadcast stream will close, and any
/// in-flight reconnect timer will fire its onDispose hook.
class KubeWebSocketClient {
  KubeWebSocketClient({
    required this.path,
    required String backendUrl,
    required this.tokenHolder,
    required this.clusterId,
    this.subscribeMessage,
    Duration initialBackoff = const Duration(seconds: 1),
    Duration maxBackoff = const Duration(seconds: 30),
    int maxRetries = -1,
    Future<bool> Function()? refreshAccessToken,
    WebSocketChannel Function(Uri, Map<String, String>)? channelFactory,
  })  : _backendUrl = backendUrl,
        _refreshAccessToken = refreshAccessToken,
        _initialBackoff = initialBackoff,
        _maxBackoff = maxBackoff,
        _maxRetries = maxRetries,
        _channelFactory = channelFactory ??
            ((uri, headers) => IOWebSocketChannel.connect(
                  uri,
                  headers: headers,
                )),
        _backoff = initialBackoff {
    _connect();
  }

  /// Endpoint path under `/api/v1/`, e.g.
  /// `ws/logs/default/web-7d4f-abc/web` (the backend prepends `/api/v1`).
  final String path;
  final String _backendUrl;
  final AuthTokenHolder tokenHolder;
  final String clusterId;

  /// Optional per-endpoint subscribe/filter message sent immediately
  /// after the auth handshake. Endpoints like `/ws/logs/...` require it
  /// (filter shape); broadcast endpoints like `/ws/notifications` do not.
  final Map<String, dynamic>? subscribeMessage;

  final Duration _initialBackoff;
  final Duration _maxBackoff;
  final int _maxRetries;
  final Future<bool> Function()? _refreshAccessToken;
  final WebSocketChannel Function(Uri, Map<String, String>) _channelFactory;

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _channelSub;
  Timer? _reconnectTimer;
  Duration _backoff;
  int _retryCount = 0;
  bool _closed = false;

  final _messages = StreamController<Map<String, dynamic>>.broadcast();
  final _state = StreamController<WebSocketState>.broadcast();

  /// Parsed JSON messages from the server. Each event arrives in the
  /// shape `{"type":"<name>", ...}` (the type field comes from the
  /// backend's MsgType* constants in `internal/websocket/events.go`).
  Stream<Map<String, dynamic>> get messages => _messages.stream;

  /// Connection lifecycle events.
  Stream<WebSocketState> get state => _state.stream;

  void _connect() {
    if (_closed) return;
    _state.add(_retryCount == 0
        ? WebSocketState.connecting
        : WebSocketState.reconnecting);

    final wsUrl = _wsUrl();
    try {
      final token = tokenHolder.accessToken;
      if (token == null) {
        _emitFatal('no access token');
        return;
      }
      // Pass cluster id as a request header on the WS upgrade — the
      // backend's cluster-context middleware reads X-Cluster-ID, not a
      // query param. dart:io supports custom WS upgrade headers via
      // IOWebSocketChannel.connect.
      final channel = _channelFactory(wsUrl, {
        'X-Cluster-ID': clusterId,
      });
      _channel = channel;

      // Auth handshake — send JWT first thing on open. The channel
      // sink accepts JSON strings; backend parses {type, token}.
      channel.sink.add(jsonEncode({'type': 'auth', 'token': token}));

      // Optional subscribe message (e.g., log filter).
      if (subscribeMessage != null) {
        channel.sink.add(jsonEncode(subscribeMessage));
      }

      // Reset backoff only after a successful connect attempt — moving
      // this from the top of _connect ensures consecutive failures
      // climb the curve instead of bouncing between 1s and 2s.
      _state.add(WebSocketState.open);
      _retryCount = 0;
      _backoff = _initialBackoff;

      _channelSub = channel.stream.listen(
        _onData,
        onError: _onError,
        onDone: _onDone,
        cancelOnError: false,
      );
    } catch (e) {
      _scheduleReconnect();
    }
  }

  Uri _wsUrl() {
    // dio_client uses http(s); WebSocket uses ws(s). Cluster id is
    // injected via the X-Cluster-ID upgrade header (see _connect),
    // matching the REST cluster-context middleware on the backend.
    final base = Uri.parse(_backendUrl);
    final scheme = base.scheme == 'https' ? 'wss' : 'ws';
    final cleanPath = path.startsWith('/') ? path : '/$path';
    final fullPath = '/api/v1$cleanPath';
    return Uri(
      scheme: scheme,
      host: base.host,
      port: base.hasPort ? base.port : null,
      path: fullPath,
    );
  }

  void _onData(dynamic raw) {
    try {
      final decoded = jsonDecode(raw as String);
      if (decoded is! Map<String, dynamic>) return;
      // Surface server-side auth/permission failures as fatal — the
      // token won't suddenly become valid on retry.
      if (decoded['type'] == 'error') {
        final message = decoded['message'] as String? ?? 'unknown error';
        final fatal = message.toLowerCase().contains('auth') ||
            message.toLowerCase().contains('token') ||
            message.toLowerCase().contains('permission');
        if (fatal) {
          _emitFatal(message);
          return;
        }
      }
      _messages.add(decoded);
    } catch (_) {
      // Drop malformed frames; do not tear down the channel.
    }
  }

  void _onError(Object error, StackTrace _) {
    _scheduleReconnect();
  }

  void _onDone() {
    if (_closed) return;
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    _channelSub?.cancel();
    _channel?.sink.close();
    _channel = null;

    if (_closed) return;
    if (_maxRetries >= 0 && _retryCount >= _maxRetries) {
      _state.add(WebSocketState.failed);
      return;
    }

    _state.add(WebSocketState.reconnecting);
    _retryCount++;
    final delay = _backoff;
    _backoff = Duration(
      milliseconds: (_backoff.inMilliseconds * 2).clamp(
        _initialBackoff.inMilliseconds,
        _maxBackoff.inMilliseconds,
      ),
    );

    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(delay, _maybeRefreshThenConnect);
  }

  /// Wraps `_connect` with an optional access-token refresh attempt.
  /// On a long-lived WS (log tail open >15min), the next reconnect
  /// after token expiry needs a fresh token or the auth handshake will
  /// fail. The injected refresher (typically `AuthRepository.refresh`)
  /// returns true on success; on failure or when no refresher is wired,
  /// we proceed with whatever token is in the holder.
  Future<void> _maybeRefreshThenConnect() async {
    if (_closed) return;
    final refresh = _refreshAccessToken;
    if (refresh != null) {
      try {
        await refresh();
      } catch (_) {
        // Refresh failure surfaces as an auth error during the next
        // _connect handshake — let the existing fatal-error path
        // handle it rather than racing here.
      }
    }
    _connect();
  }

  void _emitFatal(String message) {
    _channelSub?.cancel();
    _channel?.sink.close();
    _channel = null;
    _state.add(WebSocketState.failed);
    _messages.addError(WebSocketError(message, fatal: true));
    _closed = true;
    // Close the broadcast streams so subscribers see `done` and
    // their resources release. Without this, subscribers wait forever
    // and the StreamControllers leak until the parent provider tears
    // down. Errors are queued before close so addError still surfaces.
    _messages.close();
    _state.close();
  }

  /// Send a JSON message to the server. Used by callers that need to
  /// push commands mid-stream (e.g., pause/resume); most subscribers
  /// only need [messages].
  void send(Map<String, dynamic> payload) {
    final ch = _channel;
    if (ch == null) return;
    ch.sink.add(jsonEncode(payload));
  }

  /// Tear down the connection. Subsequent calls are no-ops.
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _reconnectTimer?.cancel();
    await _channelSub?.cancel();
    await _channel?.sink.close();
    _state.add(WebSocketState.closed);
    await _messages.close();
    await _state.close();
  }
}

/// Provider factory: creates a client for the given path/subscribe.
/// Callers `ref.watch` the provider tied to their key shape (e.g., a
/// log-tail keyed family). The returned client auto-disposes via
/// `ref.onDispose` when the provider tears down.
KubeWebSocketClient buildKubeWebSocket(
  Ref ref, {
  required String path,
  Map<String, dynamic>? subscribeMessage,
  Future<bool> Function()? refreshAccessToken,
}) {
  final client = KubeWebSocketClient(
    path: path,
    backendUrl: ref.read(backendUrlProvider),
    tokenHolder: ref.read(authTokenHolderProvider),
    clusterId: ref.read(activeClusterProvider),
    subscribeMessage: subscribeMessage,
    refreshAccessToken: refreshAccessToken,
  );
  ref.onDispose(client.close);
  return client;
}
