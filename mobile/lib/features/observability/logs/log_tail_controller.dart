// Riverpod glue for the pod log tail screen. Wraps a KubeWebSocketClient
// keyed by (clusterId, namespace, pod, container) and exposes:
//   - `lines` — append-only ring buffer of log lines (cap 5 000)
//   - `connection` — WS lifecycle (open/reconnecting/failed)
//   - `paused` — controller-owned flag the UI flips via pause/resume
//
// Buffer is bounded at 5 000 lines. On overflow the oldest line is
// evicted; UI shows the rolling tail. This avoids unbounded memory
// growth on chatty containers without forcing scrollback off the screen.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/auth_token_holder.dart';
import '../../../api/websocket_client.dart';
import '../../../auth/auth_repository.dart';

const int _logBufferLimit = 5000;

class LogTailKey {
  const LogTailKey({
    required this.clusterId,
    required this.namespace,
    required this.pod,
    required this.container,
    this.tailLines = 200,
    this.previous = false,
    this.timestamps = false,
  });

  final String clusterId;
  final String namespace;
  final String pod;
  final String container;
  final int tailLines;
  final bool previous;
  final bool timestamps;

  @override
  bool operator ==(Object other) =>
      other is LogTailKey &&
      other.clusterId == clusterId &&
      other.namespace == namespace &&
      other.pod == pod &&
      other.container == container &&
      other.tailLines == tailLines &&
      other.previous == previous &&
      other.timestamps == timestamps;

  @override
  int get hashCode => Object.hash(
        clusterId,
        namespace,
        pod,
        container,
        tailLines,
        previous,
        timestamps,
      );
}

class LogTailState {
  const LogTailState({
    required this.lines,
    required this.connection,
    required this.paused,
    this.dropped = 0,
    this.errorMessage,
  });

  /// Newest line is `lines.last`. Phone UI auto-scrolls to bottom unless
  /// `paused` is true.
  final List<String> lines;
  final WebSocketState connection;
  final bool paused;

  /// Cumulative count of server-reported drops (slow-client warnings).
  final int dropped;

  /// Set when the server emits a fatal error (auth/permission rejection).
  final String? errorMessage;

  LogTailState copyWith({
    List<String>? lines,
    WebSocketState? connection,
    bool? paused,
    int? dropped,
    String? errorMessage,
    bool clearError = false,
  }) =>
      LogTailState(
        lines: lines ?? this.lines,
        connection: connection ?? this.connection,
        paused: paused ?? this.paused,
        dropped: dropped ?? this.dropped,
        errorMessage: clearError ? null : (errorMessage ?? this.errorMessage),
      );
}

class LogTailController
    extends AutoDisposeFamilyNotifier<LogTailState, LogTailKey> {
  StreamSubscription<Map<String, dynamic>>? _msgSub;
  StreamSubscription<WebSocketState>? _stateSub;

  @override
  LogTailState build(LogTailKey key) {
    final ws = buildKubeWebSocket(
      ref,
      path: '/ws/logs/${Uri.encodeComponent(key.namespace)}'
          '/${Uri.encodeComponent(key.pod)}'
          '/${Uri.encodeComponent(key.container)}',
      subscribeMessage: {
        'container': key.container,
        'tailLines': key.tailLines,
        'previous': key.previous,
        'timestamps': key.timestamps,
      },
      // Long-lived log tails outlive the 15min access-token TTL. On
      // reconnect, refresh the token via the same body-mode flow the
      // AuthRepository bootstrap uses; tokenHolder picks up the new
      // value before the next auth handshake fires.
      refreshAccessToken: () async {
        await ref.read(authRepositoryProvider.notifier).bootstrap();
        return ref.read(authTokenHolderProvider).accessToken != null;
      },
    );

    _msgSub = ws.messages.listen(
      _onMessage,
      onError: (Object e) {
        if (e is WebSocketError) {
          state = state.copyWith(errorMessage: e.message);
        }
      },
    );
    _stateSub = ws.state.listen((s) {
      state = state.copyWith(connection: s);
    });

    ref.onDispose(() {
      _msgSub?.cancel();
      _stateSub?.cancel();
    });

    return const LogTailState(
      lines: [],
      connection: WebSocketState.connecting,
      paused: false,
    );
  }

  void _onMessage(Map<String, dynamic> msg) {
    final type = msg['type'] as String?;
    switch (type) {
      case 'log':
        final line = msg['data'] as String? ?? '';
        if (state.paused) return;
        final next = List<String>.of(state.lines)..add(line);
        if (next.length > _logBufferLimit) {
          next.removeRange(0, next.length - _logBufferLimit);
        }
        state = state.copyWith(lines: next);
      case 'dropped':
        final n = (msg['count'] as num?)?.toInt() ?? 0;
        state = state.copyWith(dropped: state.dropped + n);
      case 'end':
        // Server signaled clean stream end — no reconnect.
        state = state.copyWith(connection: WebSocketState.closed);
      case 'error':
        // F#13 (round-2) — surface server-side rejection frames to the UI
        // instead of silently swallowing them. Without this arm, the
        // remote-cluster gate (handle_ws_logs.go), invalid-namespace gate,
        // and stream-open failure all wrote a JSON {type: "error", message:
        // ...} that the controller acked-then-dropped — leaving the UI
        // stuck on "connecting" with no diagnostic. The error string lands
        // on `state.errorMessage` so the screen banner picks it up.
        final message = msg['message'] as String? ?? 'unknown server error';
        state = state.copyWith(
          errorMessage: message,
          connection: WebSocketState.closed,
        );
    }
  }

  void pause() => state = state.copyWith(paused: true);

  void resume() => state = state.copyWith(paused: false);

  void clear() => state = state.copyWith(lines: const []);
}

final logTailControllerProvider = NotifierProvider.autoDispose
    .family<LogTailController, LogTailState, LogTailKey>(
  LogTailController.new,
);
