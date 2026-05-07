// Pod log tail screen. Monospaced ListView of streamed log lines with
// auto-scroll-to-bottom (suppressed when paused), pause/resume, copy
// line, and a connection-state banner. Backed by `LogTailController`
// against `/ws/logs/:namespace/:pod/:container`.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../api/websocket_client.dart';
import '../../../cluster/cluster_provider.dart';
import '../../../theme/kube_theme_builder.dart';
import 'log_tail_controller.dart';

class LogTailScreen extends ConsumerStatefulWidget {
  const LogTailScreen({
    super.key,
    required this.namespace,
    required this.pod,
    required this.container,
  });

  final String namespace;
  final String pod;
  final String container;

  @override
  ConsumerState<LogTailScreen> createState() => _LogTailScreenState();
}

class _LogTailScreenState extends ConsumerState<LogTailScreen> {
  final ScrollController _scroll = ScrollController();
  int _lastLineCount = 0;

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _maybeAutoScroll(LogTailState state) {
    // Only scroll on growth — don't fight the user when they manually
    // scroll up to inspect older lines.
    if (state.paused) return;
    if (state.lines.length == _lastLineCount) return;
    final wasAtBottom = _scroll.hasClients
        ? _scroll.position.pixels >=
            _scroll.position.maxScrollExtent - _autoScrollThreshold
        : true;
    _lastLineCount = state.lines.length;
    if (!_scroll.hasClients || !wasAtBottom) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      _scroll.jumpTo(_scroll.position.maxScrollExtent);
    });
  }

  /// Pixels from the bottom that still count as "at the tail" for the
  /// auto-scroll heuristic. Roughly two log lines tall — small enough
  /// that scrolling up by even a single line stops the auto-jump, but
  /// large enough that streaming-line bursts don't lose the tail to
  /// rounding.
  static const double _autoScrollThreshold = 32;

  @override
  Widget build(BuildContext context) {
    final clusterId = ref.watch(activeClusterProvider);
    final key = LogTailKey(
      clusterId: clusterId,
      namespace: widget.namespace,
      pod: widget.pod,
      container: widget.container,
    );
    final state = ref.watch(logTailControllerProvider(key));
    final controller = ref.read(logTailControllerProvider(key).notifier);
    final colors = Theme.of(context).extension<KubeColors>()!;

    _maybeAutoScroll(state);

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.pod, style: const TextStyle(fontSize: 16)),
            Text(
              '${widget.namespace} · ${widget.container}',
              style: TextStyle(color: colors.textMuted, fontSize: 12),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: Icon(state.paused
                ? Icons.play_arrow_outlined
                : Icons.pause_outlined),
            tooltip: state.paused ? 'Resume' : 'Pause',
            onPressed: () =>
                state.paused ? controller.resume() : controller.pause(),
          ),
          IconButton(
            icon: const Icon(Icons.delete_sweep_outlined),
            tooltip: 'Clear',
            onPressed: controller.clear,
          ),
        ],
      ),
      body: Column(
        children: [
          _ConnectionBanner(state: state),
          Expanded(
            child: state.lines.isEmpty
                ? Center(
                    child: Text(
                      state.connection == WebSocketState.connecting ||
                              state.connection == WebSocketState.reconnecting
                          ? 'Connecting…'
                          : 'No log lines yet',
                      style: TextStyle(color: colors.textMuted),
                    ),
                  )
                : ListView.builder(
                    controller: _scroll,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 8,
                      vertical: 4,
                    ),
                    itemCount: state.lines.length,
                    itemBuilder: (context, i) => _LogLine(line: state.lines[i]),
                  ),
          ),
        ],
      ),
    );
  }
}

class _ConnectionBanner extends StatelessWidget {
  const _ConnectionBanner({required this.state});
  final LogTailState state;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final showError = state.errorMessage != null;
    final showReconnect = state.connection == WebSocketState.reconnecting;
    final showDropped = state.dropped > 0;

    if (!showError && !showReconnect && !showDropped) {
      return const SizedBox.shrink();
    }

    final color = showError ? colors.error : colors.warning;
    final dim = showError ? colors.errorDim : colors.warningDim;
    final messages = [
      if (showError) state.errorMessage!,
      if (showReconnect) 'Reconnecting…',
      if (showDropped) '${state.dropped} lines dropped (slow client)',
    ];
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      color: dim,
      child: Text(
        messages.join(' · '),
        style: TextStyle(color: color, fontSize: 12),
      ),
    );
  }
}

class _LogLine extends StatelessWidget {
  const _LogLine({required this.line});
  final String line;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    return GestureDetector(
      onLongPress: () async {
        await Clipboard.setData(ClipboardData(text: line));
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Line copied'),
            duration: Duration(seconds: 1),
          ),
        );
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 1),
        child: SelectableText(
          line,
          style: TextStyle(
            fontFamily: 'monospace',
            fontSize: 12,
            color: colors.textPrimary,
            height: 1.3,
          ),
        ),
      ),
    );
  }
}
