// Widget tests for the blast-radius two-section panel.
//
// Coverage:
//   * Sorting — `sortAffected` lifts failing health to the top, then
//     degraded, then healthy / unknown, with alphabetical tie-break
//     within each health bucket.
//   * Empty-blast-radius surfaces the "failure contained to itself"
//     success banner instead of the two empty Sections.
//   * 100-row list renders via `ListView.builder` (no eager
//     materialization — only the visible window paints).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/diagnostics_repository.dart';
import 'package:kubecenter/features/observability/diagnostics/blast_radius_panel.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

Future<void> _pump(WidgetTester tester, BlastRadius blast) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => Scaffold(
          body: SingleChildScrollView(
            child: BlastRadiusPanel(
              clusterId: 'local',
              namespace: 'default',
              blastRadius: blast,
            ),
          ),
        ),
      ),
    ],
  );
  await tester.pumpWidget(ProviderScope(
    child: MaterialApp.router(
      theme: buildKubeTheme('nexus'),
      routerConfig: router,
    ),
  ));
  await tester.pump();
}

void main() {
  group('sortAffected', () {
    test('orders failing → degraded → healthy → unknown', () {
      const inputs = [
        AffectedResource(
            kind: 'Pod', name: 'a', health: 'healthy', impact: ''),
        AffectedResource(
            kind: 'Pod', name: 'b', health: 'failing', impact: ''),
        AffectedResource(
            kind: 'Pod', name: 'c', health: 'unknown', impact: ''),
        AffectedResource(
            kind: 'Pod', name: 'd', health: 'degraded', impact: ''),
      ];
      final out = sortAffected(inputs);
      expect(out.map((r) => r.health).toList(),
          ['failing', 'degraded', 'healthy', 'unknown']);
    });

    test('alphabetical within the same health bucket', () {
      const inputs = [
        AffectedResource(
            kind: 'Pod', name: 'zeta', health: 'failing', impact: ''),
        AffectedResource(
            kind: 'Pod', name: 'alpha', health: 'failing', impact: ''),
        AffectedResource(
            kind: 'Pod', name: 'mu', health: 'failing', impact: ''),
      ];
      final out = sortAffected(inputs);
      expect(out.map((r) => r.name).toList(), ['alpha', 'mu', 'zeta']);
    });
  });

  group('BlastRadiusPanel widget', () {
    testWidgets('empty blast radius renders the contained-to-itself banner',
        (tester) async {
      await _pump(tester, BlastRadius.empty);
      expect(
        find.textContaining('failure of this resource is contained'),
        findsOneWidget,
      );
      // No section headers when blast radius is empty.
      expect(find.text('Directly Affected'), findsNothing);
      expect(find.text('Potentially Affected'), findsNothing);
    });

    testWidgets('renders both sections with counts when non-empty',
        (tester) async {
      const blast = BlastRadius(
        directlyAffected: [
          AffectedResource(
              kind: 'Deployment',
              name: 'web',
              health: 'failing',
              impact: 'owned'),
        ],
        potentiallyAffected: [
          AffectedResource(
              kind: 'Service',
              name: 'web-svc',
              health: 'degraded',
              impact: 'selector source'),
          AffectedResource(
              kind: 'Ingress',
              name: 'web-ing',
              health: 'healthy',
              impact: 'ingress'),
        ],
      );
      await _pump(tester, blast);
      expect(find.text('Directly Affected'), findsOneWidget);
      expect(find.text('(1)'), findsOneWidget);
      expect(find.text('Potentially Affected'), findsOneWidget);
      expect(find.text('(2)'), findsOneWidget);
      expect(find.text('web'), findsOneWidget);
      expect(find.text('web-svc'), findsOneWidget);
    });

    testWidgets('100 rows render via ListView.builder (no eager materialize)',
        (tester) async {
      final many = List<AffectedResource>.generate(
        100,
        (i) => AffectedResource(
          kind: 'Pod',
          name: 'pod-${i.toString().padLeft(3, '0')}',
          health: 'failing',
          impact: 'owned',
        ),
      );
      final blast = BlastRadius(
        directlyAffected: many,
        potentiallyAffected: const [],
      );
      await _pump(tester, blast);
      // First page must render — at least the alphabetically-first
      // few names should be on screen.
      expect(find.text('pod-000'), findsOneWidget);
      // The very last entry is below the 480px max-height window and
      // should not be in the tree until scrolled — proves virtualization.
      expect(find.text('pod-099'), findsNothing);
    });
  });
}
