// Widget tests for the ExternalSecrets list screen.
//
// Coverage:
//   * detected=false → FeatureUnavailableState.eso().
//   * Mixed rows render with status + drift pills (poller hint surfaces).
//   * Drift Unknown row does NOT render an error-colored pill (PR-3f
//     learnings #9 regression guard — full screen path, not just the
//     pill widget in isolation).
//   * Filter chip narrows the list (SyncFailed).
//   * `?status=*` initialStatusFilter pre-selects the chip.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/eso_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/eso/external_secrets_list_screen.dart';
import 'package:kubecenter/features/eso/eso_widgets.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(
  WidgetTester tester,
  MockDioAdapter mock, {
  String? initialStatusFilter,
}) async {
  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => ExternalSecretsListScreen(
          initialStatusFilter: initialStatusFilter,
        ),
      ),
    ],
  );
  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
    child: _DioInstaller(
      mock: mock,
      child: MaterialApp.router(
        theme: buildKubeTheme('liquid-glass'),
        routerConfig: router,
      ),
    ),
  ));
  await tester.pump();
  await tester.pumpAndSettle(const Duration(milliseconds: 200));
}

class _DioInstaller extends ConsumerWidget {
  const _DioInstaller({required this.mock, required this.child});

  final MockDioAdapter mock;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.read(dioProvider).httpClientAdapter = mock;
    return child;
  }
}

Map<String, Object?> _statusDetected() => {
      'data': {
        'detected': true,
        'namespace': 'external-secrets',
        'version': '0.14.0',
        'lastChecked': '2026-05-12T10:00:00Z',
      },
    };

Map<String, Object?> _statusNotDetected() => {
      'data': {'detected': false},
    };

Map<String, Object?> _listBody() => {
      'data': [
        {
          'name': 'app-token',
          'namespace': 'app',
          'uid': 'es-1',
          'status': 'Synced',
          'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
          'targetSecretName': 'app-token-secret',
          'lastObservedDriftStatus': 'InSync',
        },
        {
          'name': 'broken',
          'namespace': 'app',
          'uid': 'es-2',
          'status': 'SyncFailed',
          'storeRef': {'name': 'vault', 'kind': 'SecretStore'},
          'readyMessage': 'auth method failed',
        },
        {
          'name': 'unobserved',
          'namespace': 'kube-system',
          'uid': 'es-3',
          'status': 'Synced',
          'storeRef': {'name': 'cluster-vault', 'kind': 'ClusterSecretStore'},
          // No lastObservedDriftStatus — poller hasn't observed yet.
        },
      ],
    };

void main() {
  testWidgets('detected=false renders FeatureUnavailableState.eso()',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusNotDetected());
    await _pump(tester, mock);

    expect(find.textContaining('External Secrets Operator'), findsWidgets);
    expect(find.textContaining('is not installed on this cluster'),
        findsOneWidget);
  });

  testWidgets('renders rows with name + namespace + status pills',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected())
      ..onJson('GET', '/api/v1/externalsecrets/externalsecrets',
          body: _listBody());
    await _pump(tester, mock);

    expect(find.text('app-token'), findsOneWidget);
    expect(find.text('broken'), findsOneWidget);
    expect(find.text('unobserved'), findsOneWidget);
    // Inspect EsoStatusPill widgets directly to avoid colliding with
    // chip labels that share the same text. 3 rows = 3 pills.
    final pills = tester
        .widgetList<EsoStatusPill>(find.byType(EsoStatusPill))
        .toList();
    final statuses = pills.map((p) => p.status).toList();
    expect(statuses.where((s) => s == EsoStatus.synced).length, 2);
    expect(statuses.where((s) => s == EsoStatus.syncFailed).length, 1);
  });

  testWidgets(
    'unobserved row (no lastObservedDriftStatus) shows no "Unknown" pill — '
    'the wire-shape contract',
    (tester) async {
      final mock = MockDioAdapter()
        ..onJson('GET', '/api/v1/externalsecrets/status',
            body: _statusDetected())
        ..onJson('GET', '/api/v1/externalsecrets/externalsecrets',
            body: _listBody());
      await _pump(tester, mock);

      // Inspect the rendered DriftPill widgets directly — this is
      // safer than text matching since ChoiceChip uses InkWell
      // internally and confuses descendant-scoped finders. Two assertions:
      //   1. The InSync pill renders for the observed row.
      //   2. No drift pill renders with status=DriftStatus.unknown
      //      OR DriftStatus.notObserved-yet-with-content — i.e., the
      //      unobserved row produces no DriftPill text at all.
      final pills = tester
          .widgetList<DriftPill>(find.byType(DriftPill))
          .toList();
      final renderedStates = pills.map((p) => p.status).toList();
      expect(renderedStates, contains(DriftStatus.inSync));
      expect(renderedStates, isNot(contains(DriftStatus.unknown)),
          reason:
              'List rows render lastObservedDriftStatus only. A drift '
              'Unknown pill on a row with no observation would '
              'contradict the backend wire contract (LastObservedDriftStatus '
              'omitempty).');
    },
  );

  testWidgets('SyncFailed filter chip narrows the list', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected())
      ..onJson('GET', '/api/v1/externalsecrets/externalsecrets',
          body: _listBody());
    await _pump(tester, mock);

    // Default = All, all three rows visible.
    expect(find.text('app-token'), findsOneWidget);
    expect(find.text('broken'), findsOneWidget);

    // Tap the SyncFailed chip — there are multiple matches (the chip
    // label + a row's status pill); the chip is the InkWell-wrapped
    // one inside a ChoiceChip.
    await tester.tap(find.widgetWithText(ChoiceChip, 'SyncFailed'));
    await tester.pumpAndSettle();

    expect(find.text('broken'), findsOneWidget);
    expect(find.text('app-token'), findsNothing);
    expect(find.text('unobserved'), findsNothing);
  });

  testWidgets('initialStatusFilter=syncfailed pre-selects chip',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/externalsecrets/status',
          body: _statusDetected())
      ..onJson('GET', '/api/v1/externalsecrets/externalsecrets',
          body: _listBody());
    await _pump(tester, mock, initialStatusFilter: 'syncfailed');

    // Only the broken row is visible on mount.
    expect(find.text('broken'), findsOneWidget);
    expect(find.text('app-token'), findsNothing);
  });
}
