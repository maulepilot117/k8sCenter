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
    WebSocketChannel Function(Uri)? channelFactory,
  })  : _backendUrl = backendUrl,
        _initialBackoff = initialBackoff,
        _maxBackoff = maxBackoff,
        _maxRetries = maxRetries,
        _channelFactory = channelFactory ?? WebSocketChannel.connect {
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
  final WebSocketChannel Function(Uri) _channelFactory;

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _channelSub;
  Timer? _reconnectTimer;
  Duration _backoff = const Duration(seconds: 1);
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
    _backoff = _initialBackoff;

    final wsUrl = _wsUrl();
    try {
      final channel = _channelFactory(wsUrl);
      _channel = channel;

      // Auth handshake — send JWT first thing on open. The channel
      // sink accepts JSON strings; backend parses {type, token}.
      final token = tokenHolder.accessToken;
      if (token == null) {
        _emitFatal('no access token');
        return;
      }
      channel.sink.add(jsonEncode({'type': 'auth', 'token': token}));

      // Optional subscribe message (e.g., log filter).
      if (subscribeMessage != null) {
        channel.sink.add(jsonEncode(subscribeMessage));
      }

      _state.add(WebSocketState.open);
      _retryCount = 0;

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
    // dio_client uses http(s); WebSocket uses ws(s).
    final base = Uri.parse(_backendUrl);
    final scheme = base.scheme == 'https' ? 'wss' : 'ws';
    final cleanPath = path.startsWith('/') ? path : '/$path';
    final fullPath = '/api/v1$cleanPath';
    return Uri(
      scheme: scheme,
      host: base.host,
      port: base.hasPort ? base.port : null,
      path: fullPath,
      // Cluster context — handlers read this from the URL when present;
      // primary mechanism is the X-Cluster-ID header on REST, but WS
      // upgrade requests in browsers can't easily set custom headers.
      queryParameters: {'cluster': clusterId},
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
    _reconnectTimer = Timer(delay, _connect);
  }

  void _emitFatal(String message) {
    _channelSub?.cancel();
    _channel?.sink.close();
    _channel = null;
    _state.add(WebSocketState.failed);
    _messages.addError(WebSocketError(message, fatal: true));
    _closed = true;
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
}) {
  final client = KubeWebSocketClient(
    path: path,
    backendUrl: ref.read(backendUrlProvider),
    tokenHolder: ref.read(authTokenHolderProvider),
    clusterId: ref.read(activeClusterProvider),
    subscribeMessage: subscribeMessage,
  );
  ref.onDispose(client.close);
  return client;
}
