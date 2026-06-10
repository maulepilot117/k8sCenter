// Widget tests for the per-store metrics panel.
//
// PR-4h-review #5: The null-vs-zero invariant ("UI MUST NOT fabricate a
// zero — null ≠ 0", from backend R25) is verified at the parser layer by
// the repository tests but had no widget-level coverage. _RatePanel's
// three-way branch is the failure point — a future refactor that prints
// "0.00" for null inputs, or hides the degraded banner, would not be
// caught by any prior test. This file pins all three cases.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/eso_repository.dart';
import 'package:kubecenter/features/eso/store_metrics_panel.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

Future<void> _pump(WidgetTester tester, StoreMetrics metrics) async {
  await tester.pumpWidget(
    ProviderScope(
      child: MaterialApp(
        theme: buildKubeTheme('liquid-glass'),
        home: Scaffold(
          body: StoreMetricsPanel(
            metricsAsync: AsyncValue.data(metrics),
            onRetry: () {},
            storeLabel: 'vault-store / app',
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

void main() {
  testWidgets(
    'null ratePerMin + no error renders "no data yet" — never 0.00',
    (tester) async {
      await _pump(
        tester,
        const StoreMetrics(),
      );
      expect(find.text('no data yet'), findsWidgets,
          reason: 'The "null = no series yet" path MUST surface a '
              'discriminable label so operators do not read it as a real 0.');
      expect(find.text('0.00'), findsNothing,
          reason: 'A null ratePerMin must never render as a fabricated 0.');
      expect(find.byIcon(Icons.warning_amber_outlined), findsNothing,
          reason: 'No degradation → no degraded banner.');
    },
  );

  testWidgets(
    'null ratePerMin + error string renders the degraded banner + "—"',
    (tester) async {
      await _pump(
        tester,
        const StoreMetrics(error: 'Prometheus offline'),
      );
      expect(find.text('Prometheus offline'), findsOneWidget,
          reason: 'The error envelope must surface as the degraded banner.');
      expect(find.byIcon(Icons.warning_amber_outlined), findsOneWidget);
      expect(find.text('—'), findsWidgets,
          reason: 'Degraded null path renders an em dash, not "0.00".');
      expect(find.text('0.00'), findsNothing);
    },
  );

  testWidgets(
    'concrete ratePerMin renders the formatted number, not a fallback',
    (tester) async {
      await _pump(
        tester,
        const StoreMetrics(ratePerMin: 12.5, last24h: 18000),
      );
      expect(find.text('12.50'), findsOneWidget);
      expect(find.text('no data yet'), findsNothing);
      expect(find.text('—'), findsNothing);
      expect(find.byIcon(Icons.warning_amber_outlined), findsNothing);
    },
  );
}
