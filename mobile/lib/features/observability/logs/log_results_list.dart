// Virtual-scroll log results list. Renders LogQueryResult.lines as a
// monospace stream with severity color hints + long-press-to-copy.
//
// 5000-line cap surfaces as a banner so the operator knows to narrow
// the query rather than scrolling endlessly. Severity detection mirrors
// `frontend/islands/LogResults.tsx`'s `parseSeverity` so the same line
// gets the same color on web and mobile.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:intl/intl.dart';

import '../../../api/loki_repository.dart';
import '../../../theme/kube_theme_builder.dart';

enum _LineSeverity { error, warn, info, debug }

class LogResultsList extends StatelessWidget {
  const LogResultsList({
    required this.result,
    super.key,
  });

  final LogQueryResult result;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    if (result.isEmpty) {
      return Container(
        padding: const EdgeInsets.all(24),
        decoration: BoxDecoration(
          color: colors.bgSurface,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: colors.borderSubtle),
        ),
        child: Center(
          child: Text(
            'No log lines for this query.',
            style: TextStyle(color: colors.textMuted, fontSize: 13),
          ),
        ),
      );
    }
    return Container(
      decoration: BoxDecoration(
        color: colors.bgSurface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: colors.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (result.truncated)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              color: colors.warningDim,
              child: Row(
                children: [
                  Icon(Icons.info_outline, size: 14, color: colors.warning),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      'Showing 5000 of (truncated) results. Refine the '
                      'query for the full set.',
                      style: TextStyle(color: colors.warning, fontSize: 12),
                    ),
                  ),
                ],
              ),
            ),
          // Virtual scroll. ListView.builder materializes only visible
          // rows so a 5000-line response stays under one frame's worth
          // of work on phone-class GPUs. SizedBox (not
          // ConstrainedBox + shrinkWrap) is critical: shrinkWrap forces
          // a full layout pass over every child at first paint, which
          // negates virtualization for the 5000-line case.
          SizedBox(
            height: 600,
            child: ListView.builder(
              itemCount: result.lines.length,
              itemBuilder: (context, i) => _LogLineRow(
                line: result.lines[i],
                colors: colors,
              ),
            ),
          ),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            decoration: BoxDecoration(
              color: colors.bgElevated,
              border: Border(top: BorderSide(color: colors.borderSubtle)),
            ),
            child: Text(
              '${result.lines.length} lines · '
              '${result.streamCount} streams',
              style: TextStyle(color: colors.textMuted, fontSize: 11),
            ),
          ),
        ],
      ),
    );
  }
}

class _LogLineRow extends StatelessWidget {
  const _LogLineRow({required this.line, required this.colors});

  final LogLine line;
  final KubeColors colors;

  @override
  Widget build(BuildContext context) {
    final severity = _parseSeverity(line.line);
    final tone = _severityColor(severity);
    final bg = severity == _LineSeverity.error
        ? colors.errorDim.withAlpha(40)
        : Colors.transparent;
    final pod = line.labels['pod'];

    return GestureDetector(
      onLongPress: () async {
        await Clipboard.setData(ClipboardData(text: line.line));
        if (!context.mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Line copied'),
            duration: Duration(seconds: 1),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 2),
        decoration: BoxDecoration(
          color: bg,
          border: Border(bottom: BorderSide(color: colors.borderSubtle)),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 86,
              child: Text(
                _formatTimestamp(line.timestamp),
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 10,
                  color: colors.textMuted,
                ),
              ),
            ),
            SizedBox(
              width: 48,
              child: Text(
                _severityLabel(severity),
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 10,
                  fontWeight: FontWeight.w600,
                  color: tone,
                ),
              ),
            ),
            if (pod != null && pod.isNotEmpty)
              SizedBox(
                width: 110,
                child: Text(
                  pod.length > 18 ? '${pod.substring(0, 18)}…' : pod,
                  style: TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 10,
                    color: colors.info,
                  ),
                ),
              ),
            Expanded(
              child: SelectableText(
                line.line,
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 11,
                  color: colors.textPrimary,
                  height: 1.3,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Color _severityColor(_LineSeverity s) {
    return switch (s) {
      _LineSeverity.error => colors.error,
      _LineSeverity.warn => colors.warning,
      _LineSeverity.info => colors.accent,
      _LineSeverity.debug => colors.textMuted,
    };
  }

  String _severityLabel(_LineSeverity s) {
    return switch (s) {
      _LineSeverity.error => 'ERROR',
      _LineSeverity.warn => 'WARN',
      _LineSeverity.info => 'INFO',
      _LineSeverity.debug => 'DEBUG',
    };
  }
}

/// Severity heuristic. Mirrors web's `parseSeverity` so the same line
/// gets the same tone on both surfaces. Inexpensive — only inspects
/// the prefix of the line rather than the full body.
_LineSeverity _parseSeverity(String line) {
  final lower = line.toLowerCase();
  // Use the full lowercased line for substring checks (matches web
  // parity for the `level=` / `"level":"X"` JSON / logfmt forms);
  // restrict the word-boundary scan to the 100-char prefix to keep
  // the per-row cost bounded on long lines.
  final prefixLower = lower.length > 100 ? lower.substring(0, 100) : lower;

  if (lower.contains('"level":"error"') ||
      lower.contains('level=error') ||
      _wordMatch(prefixLower, 'error')) {
    return _LineSeverity.error;
  }
  if (lower.contains('"level":"warn"') ||
      lower.contains('level=warn') ||
      _wordMatch(prefixLower, 'warn') ||
      _wordMatch(prefixLower, 'warning')) {
    return _LineSeverity.warn;
  }
  if (lower.contains('"level":"debug"') ||
      lower.contains('level=debug') ||
      _wordMatch(prefixLower, 'debug')) {
    return _LineSeverity.debug;
  }
  return _LineSeverity.info;
}

/// Whole-word match. Caller passes pre-lowercased text so the hot
/// per-row render path doesn't allocate a second lowercased copy.
/// RegExp would work but creating one per line per render burns
/// allocations on a long results list; inline scan is cheaper.
bool _wordMatch(String lower, String word) {
  var i = lower.indexOf(word);
  while (i >= 0) {
    final beforeOk = i == 0 || !_isWordChar(lower.codeUnitAt(i - 1));
    final afterIdx = i + word.length;
    final afterOk =
        afterIdx >= lower.length || !_isWordChar(lower.codeUnitAt(afterIdx));
    if (beforeOk && afterOk) return true;
    i = lower.indexOf(word, i + 1);
  }
  return false;
}

bool _isWordChar(int code) {
  return (code >= 48 && code <= 57) ||
      (code >= 65 && code <= 90) ||
      (code >= 97 && code <= 122) ||
      code == 95;
}

final DateFormat _tsFormat = DateFormat('HH:mm:ss.SSS');

String _formatTimestamp(DateTime t) {
  return _tsFormat.format(t.toLocal());
}
