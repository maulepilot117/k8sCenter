// Widget tests for LogResultsList — verifies severity tinting matches
// web parity (parseSeverity heuristic), truncation banner, and the
// empty-state branch.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/loki_repository.dart';
import 'package:kubecenter/features/observability/logs/log_results_list.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart' show buildKubeTheme;

LogLine _ln(String text, {Map<String, String> labels = const {}, int ts = 0}) {
  return LogLine(timestampNanos: ts, line: text, labels: labels);
}

Future<void> _pump(WidgetTester tester, LogQueryResult result) async {
  await tester.pumpWidget(
    MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Scaffold(
        body: SingleChildScrollView(
          child: LogResultsList(result: result),
        ),
      ),
    ),
  );
  await tester.pump();
}

void main() {
  group('LogResultsList severity tinting', () {
    testWidgets('JSON level=error tints ERROR', (tester) async {
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('{"level":"error","msg":"boom"}', ts: 1)],
          streamCount: 1,
        ),
      );
      expect(find.text('ERROR'), findsOneWidget);
    });

    testWidgets('logfmt level=warn tints WARN', (tester) async {
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('ts=2026-05-01 level=warn caller=foo msg="slow"', ts: 2)],
          streamCount: 1,
        ),
      );
      expect(find.text('WARN'), findsOneWidget);
    });

    testWidgets('plain-prefix whole-word ERROR tints ERROR', (tester) async {
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('ERROR: connection refused', ts: 3)],
          streamCount: 1,
        ),
      );
      expect(find.text('ERROR'), findsOneWidget);
    });

    testWidgets('embedded "xerrorx" must NOT match (whole-word boundary)',
        (tester) async {
      // Whole-word logic skips substring matches; xerrorx should fall
      // through to INFO. Without _isWordChar guarding, this would
      // falsely color as ERROR and silently mis-tint operational lines
      // with names like "xerrorxformatter".
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('msg=xerrorxhandler initialized', ts: 4)],
          streamCount: 1,
        ),
      );
      expect(find.text('ERROR'), findsNothing);
      expect(find.text('INFO'), findsOneWidget);
    });

    testWidgets('plain "debug" word tints DEBUG', (tester) async {
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('debug: scheduled tick', ts: 5)],
          streamCount: 1,
        ),
      );
      expect(find.text('DEBUG'), findsOneWidget);
    });

    testWidgets('line with no severity keywords defaults to INFO',
        (tester) async {
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('Started replica on port 9090', ts: 6)],
          streamCount: 1,
        ),
      );
      expect(find.text('INFO'), findsOneWidget);
    });

    testWidgets('JSON "level":"error" past column 100 still matches',
        (tester) async {
      // Web's parseSeverity scans the FULL line for the JSON / logfmt
      // level fields; only the word-boundary scan is prefix-bounded.
      // Mobile must mirror this so a long correlation-ID prefix
      // doesn't accidentally drop the severity color.
      final padding = 'x' * 150;
      await _pump(
        tester,
        LogQueryResult(
          lines: [_ln('$padding "level":"error"', ts: 7)],
          streamCount: 1,
        ),
      );
      expect(find.text('ERROR'), findsOneWidget);
    });
  });

  group('LogResultsList layout states', () {
    testWidgets('empty result renders "no log lines" message',
        (tester) async {
      await _pump(
        tester,
        const LogQueryResult(lines: [], streamCount: 0),
      );
      expect(find.text('No log lines for this query.'), findsOneWidget);
    });

    testWidgets('5000-line result shows truncation banner', (tester) async {
      final lines = List.generate(
        5000,
        (i) => _ln('line $i', ts: i),
      );
      await _pump(tester, LogQueryResult(lines: lines, streamCount: 1));
      // The exact banner text uses "5000 of (truncated) results".
      expect(
        find.textContaining('5000 of'),
        findsOneWidget,
      );
    });

    testWidgets('non-truncated result shows footer count', (tester) async {
      final lines = [_ln('only line', ts: 1)];
      await _pump(tester, LogQueryResult(lines: lines, streamCount: 1));
      expect(find.text('1 lines · 1 streams'), findsOneWidget);
    });
  });
}
