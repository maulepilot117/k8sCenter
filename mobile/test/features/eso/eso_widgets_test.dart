// Widget tests for shared ESO pill/widget primitives.
//
// **Critical assertion: `DriftStatus.unknown` renders with the
// `textMuted` token — NEVER `error`.** This is the regression guard
// for the PR-3f learnings #9 risk (the M4 plan calls it out under
// "Risks & Dependencies"). If a future refactor accidentally maps
// drift Unknown onto the error palette, this test fires.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/eso_repository.dart';
import 'package:kubecenter/features/eso/eso_widgets.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

Future<void> _pumpWith(WidgetTester tester, Widget child) async {
  await tester.pumpWidget(
    MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(body: Center(child: child)),
    ),
  );
}

KubeColors _kubeColors(BuildContext context) =>
    Theme.of(context).extension<KubeColors>()!;

void main() {
  group('DriftPill', () {
    testWidgets('InSync uses success token, not error', (tester) async {
      await _pumpWith(tester, const DriftPill(status: DriftStatus.inSync));
      final text = tester.widget<Text>(find.text('In sync'));
      final ctx = tester.element(find.text('In sync'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.success);
      expect(text.style?.color, isNot(colors.error));
    });

    testWidgets('Drifted uses warning token', (tester) async {
      await _pumpWith(tester, const DriftPill(status: DriftStatus.drifted));
      final text = tester.widget<Text>(find.text('Drifted'));
      final ctx = tester.element(find.text('Drifted'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.warning);
      expect(text.style?.color, isNot(colors.error));
    });

    testWidgets(
      'Unknown uses textMuted — never red (PR-3f learnings #9)',
      (tester) async {
        await _pumpWith(
          tester,
          const DriftPill(
            status: DriftStatus.unknown,
            reason: DriftUnknownReason.noSyncedRv,
          ),
        );
        // Find the Text inside the pill, not the surrounding tooltip
        // wrapper. The pill has one Text child rendering "Unknown".
        final text = tester.widget<Text>(find.text('Unknown'));
        final ctx = tester.element(find.text('Unknown'));
        final colors = _kubeColors(ctx);

        expect(text.style?.color, colors.textMuted,
            reason:
                'Drift Unknown MUST render as textMuted — operators see '
                'this on every ESO store whose provider omits '
                'syncedResourceVersion (the Kubernetes provider, for '
                'instance). Rendering it as error would confuse oncall '
                'on every cluster.');
        expect(text.style?.color, isNot(colors.error));
        expect(text.style?.color, isNot(colors.warning));
      },
    );

    testWidgets('Unknown surface carries the reason tooltip', (tester) async {
      await _pumpWith(
        tester,
        const DriftPill(
          status: DriftStatus.unknown,
          reason: DriftUnknownReason.rbacDenied,
        ),
      );
      final tooltip = tester.widget<Tooltip>(find.byType(Tooltip));
      expect(tooltip.message, contains('permission'));
      expect(tooltip.message, contains('get secret'));
    });

    testWidgets(
      'notObserved renders nothing (zero-size box, no "Unknown" text)',
      (tester) async {
        await _pumpWith(
          tester,
          const DriftPill(status: DriftStatus.notObserved),
        );
        expect(find.text('Unknown'), findsNothing);
        expect(find.text('In sync'), findsNothing);
        expect(find.text('Drifted'), findsNothing);
      },
    );
  });

  group('EsoStatusPill', () {
    testWidgets('SyncFailed uses error token', (tester) async {
      await _pumpWith(
        tester,
        const EsoStatusPill(status: EsoStatus.syncFailed),
      );
      final text = tester.widget<Text>(find.text('SyncFailed'));
      final ctx = tester.element(find.text('SyncFailed'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.error);
    });

    testWidgets('Synced uses success token', (tester) async {
      await _pumpWith(
        tester,
        const EsoStatusPill(status: EsoStatus.synced),
      );
      final text = tester.widget<Text>(find.text('Synced'));
      final ctx = tester.element(find.text('Synced'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.success);
    });

    testWidgets('Unknown (status, not drift) uses textMuted', (tester) async {
      await _pumpWith(
        tester,
        const EsoStatusPill(status: EsoStatus.unknown),
      );
      final text = tester.widget<Text>(find.text('Unknown'));
      final ctx = tester.element(find.text('Unknown'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.textMuted);
      expect(text.style?.color, isNot(colors.error));
    });

    // PR-4h-review #30: Stale + Refreshing arms previously had no tests.
    // Closing the switch arms guards against a future palette refactor
    // that drifts them off their expected severity tokens.
    testWidgets('Stale uses warning token (not error)', (tester) async {
      await _pumpWith(
        tester,
        const EsoStatusPill(status: EsoStatus.stale),
      );
      final text = tester.widget<Text>(find.text('Stale'));
      final ctx = tester.element(find.text('Stale'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, colors.warning);
      expect(text.style?.color, isNot(colors.error));
    });

    testWidgets('Refreshing uses an info/transient token, never error',
        (tester) async {
      await _pumpWith(
        tester,
        const EsoStatusPill(status: EsoStatus.refreshing),
      );
      final text = tester.widget<Text>(find.text('Refreshing'));
      final ctx = tester.element(find.text('Refreshing'));
      final colors = _kubeColors(ctx);
      expect(text.style?.color, isNot(colors.error),
          reason: 'Refreshing is a transient state — fleet-wide '
              'reconciles must not render the fleet as failed.');
      expect(text.style?.color, isNot(colors.warning));
    });
  });

  // PR-4h-review #6: ChipStrip caps eager-rendered chips so a
  // ClusterExternalSecret whose namespaceSelector matches hundreds of
  // namespaces does not freeze the detail screen layout pass.
  group('ChipStrip', () {
    testWidgets('renders all items when under the cap', (tester) async {
      final items =
          List<String>.generate(8, (i) => 'namespace-$i', growable: false);
      await _pumpWith(
        tester,
        ChipStrip(label: 'Namespaces', items: items),
      );
      for (final ns in items) {
        expect(find.text(ns), findsOneWidget);
      }
      expect(find.textContaining('+'), findsNothing);
    });

    testWidgets('truncates with "+N more" trailing chip when over the cap',
        (tester) async {
      final items =
          List<String>.generate(120, (i) => 'ns-$i', growable: false);
      await _pumpWith(
        tester,
        ChipStrip(label: 'Namespaces', items: items, maxVisible: 50),
      );
      expect(find.text('ns-0'), findsOneWidget);
      expect(find.text('ns-49'), findsOneWidget);
      expect(find.text('ns-50'), findsNothing);
      expect(find.text('+70 more'), findsOneWidget);
    });
  });

  group('DisabledRevertDriftButton', () {
    testWidgets('renders disabled with desktop tooltip', (tester) async {
      await _pumpWith(tester, const DisabledRevertDriftButton());
      // The button is disabled when onPressed is null.
      final btn = tester.widget<OutlinedButton>(find.byType(OutlinedButton));
      expect(btn.onPressed, isNull,
          reason:
              'Revert is disabled per R12 — write actions defer to desktop.');

      final tooltip = tester.widget<Tooltip>(find.byType(Tooltip));
      expect(tooltip.message, DisabledRevertDriftButton.desktopMessage);
      expect(tooltip.message, contains('desktop'));
    });
  });
}
