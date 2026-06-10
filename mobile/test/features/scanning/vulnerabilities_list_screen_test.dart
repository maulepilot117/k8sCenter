// Widget tests for the vulnerabilities list.
//
// Coverage:
//   * Row renders workload name, kind, severity chips, scanner badge.
//   * Empty list → "No vulnerability reports in this namespace" empty
//     state.
//   * Severity chip ("None") filters to workloads with zero CVEs.
//   * Scanner discriminator chips render only when both scanners
//     contributed rows; the chip click narrows the list.
//   * Virtual-scroll via `SliverChildBuilderDelegate` — off-viewport
//     rows do not construct.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/scanning_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/scanning/scanning_widgets.dart';
import 'package:kubecenter/features/scanning/vulnerabilities_list_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  await tester.binding.setSurfaceSize(const Size(800, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        // initialNamespace seed bypasses the bottom-sheet picker so the
        // tests focus on list rendering, not the picker mechanic.
        builder: (context, state) =>
            const VulnerabilitiesListScreen(initialNamespace: 'app'),
      ),
      GoRoute(
        path: '/clusters/:clusterId/scanning/vulnerabilities/'
            ':namespace/:kind/:name',
        builder: (context, state) => const Scaffold(body: Text('detail')),
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

Map<String, Object?> _statusBoth() => {
      'data': {
        'detected': 'both',
        'trivy': {'available': true},
        'kubescape': {'available': true},
        'lastChecked': '2026-05-15T12:00:00Z',
      },
    };

Map<String, Object?> _vulnEnvelope(List<Map<String, Object?>> rows) {
  int crit = 0, high = 0, med = 0, low = 0;
  for (final r in rows) {
    final t = r['total'] as Map<String, Object?>?;
    if (t != null) {
      crit += (t['critical'] as num?)?.toInt() ?? 0;
      high += (t['high'] as num?)?.toInt() ?? 0;
      med += (t['medium'] as num?)?.toInt() ?? 0;
      low += (t['low'] as num?)?.toInt() ?? 0;
    }
  }
  return {
    'data': {
      'vulnerabilities': rows,
      'summary': {
        'total': rows.length,
        'severity': {
          'critical': crit,
          'high': high,
          'medium': med,
          'low': low,
        },
      },
    },
  };
}

Map<String, Object?> _row({
  required String name,
  required String scanner,
  int critical = 0,
  int high = 0,
  int medium = 0,
  int low = 0,
}) =>
    {
      'namespace': 'app',
      'kind': 'Deployment',
      'name': name,
      'images': [
        {
          'image': 'docker.io/library/$name:1.0',
          'severities': {
            'critical': critical,
            'high': high,
            'medium': medium,
            'low': low,
          },
        },
      ],
      'total': {
        'critical': critical,
        'high': high,
        'medium': medium,
        'low': low,
      },
      'lastScanned': '2026-05-15T11:00:00Z',
      'scanner': scanner,
    };

void main() {
  testWidgets('row renders workload + scanner + severity chips',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([
          _row(name: 'web', scanner: 'trivy', critical: 2, high: 5, medium: 1),
          _row(name: 'cache', scanner: 'kubescape', high: 3),
        ]),
      );

    await _pump(tester, mock);
    expect(find.text('web'), findsOneWidget);
    expect(find.text('cache'), findsOneWidget);
    // Severity chips on `web`: critical 2, high 5, medium 1.
    expect(find.text('C 2'), findsOneWidget);
    expect(find.text('H 5'), findsOneWidget);
    expect(find.text('M 1'), findsOneWidget);
    // Scanner badges from both engines render.
    expect(find.text('Trivy'), findsWidgets);
    expect(find.text('Kubescape'), findsWidgets);
    // Since both scanners contributed rows, the discriminator chips
    // row renders.
    expect(find.text('All scanners'), findsOneWidget);
  });

  testWidgets('empty list shows targeted empty-state copy', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope(const []),
      );

    await _pump(tester, mock);
    expect(find.textContaining('No vulnerability reports in this namespace'),
        findsOneWidget);
  });

  testWidgets('"No CVEs" chip filters to clean workloads', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([
          _row(name: 'web', scanner: 'trivy', critical: 2),
          _row(name: 'clean', scanner: 'trivy'),
        ]),
      );

    await _pump(tester, mock);
    expect(find.text('web'), findsOneWidget);
    expect(find.text('clean'), findsOneWidget);

    // Tap the "No CVEs" chip — narrow to the ChoiceChip widget so the
    // tap doesn't ambiguously match the row's "No CVEs" label that
    // renders on clean workloads.
    await tester.tap(find.widgetWithText(ChoiceChip, 'No CVEs'));
    await tester.pumpAndSettle();

    expect(find.text('web'), findsNothing,
        reason: 'workload with critical CVEs filtered out');
    expect(find.text('clean'), findsOneWidget,
        reason: 'workload with zero CVEs remains');
  });

  testWidgets('scanner discriminator chips hide when only one scanner',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: {
        'data': {
          'detected': 'trivy',
          'trivy': {'available': true},
          'kubescape': {'available': false},
          'lastChecked': '',
        },
      })
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([
          _row(name: 'web', scanner: 'trivy', high: 1),
        ]),
      );

    await _pump(tester, mock);
    expect(find.text('web'), findsOneWidget);
    expect(find.text('All scanners'), findsNothing,
        reason:
            'Discriminator chips are only rendered when both Trivy and '
            'Kubescape contributed rows.');
  });

  // #27 — StaleScanBanner renders for stale timestamp.
  testWidgets('StaleScanBanner renders when a workload scan is stale',
      (tester) async {
    // 8 days ago → stale.
    final staleDt = DateTime.now().toUtc().subtract(const Duration(days: 8));
    final staleTs = staleDt.toIso8601String();

    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: {
          'data': {
            'vulnerabilities': [
              {
                'namespace': 'app',
                'kind': 'Deployment',
                'name': 'web',
                'images': <Map<String, Object?>>[],
                'total': {
                  'critical': 1,
                  'high': 0,
                  'medium': 0,
                  'low': 0,
                },
                'lastScanned': staleTs,
                'scanner': 'trivy',
              },
            ],
            'summary': {
              'total': 1,
              'severity': {'critical': 1, 'high': 0, 'medium': 0, 'low': 0},
            },
          },
        },
      );

    await _pump(tester, mock);
    expect(find.byType(StaleScanBanner), findsOneWidget,
        reason: 'A scan timestamp 8 days old must surface the stale banner.');
    expect(
      find.textContaining(
          'more than ${kScanStaleThreshold.inDays} days old'),
      findsOneWidget,
      reason:
          'Banner text must reference the configured threshold, not a '
          'hardcoded literal.',
    );
  });

  // #28 — null initialNamespace triggers picker prompt.
  testWidgets('null initialNamespace triggers bottom-sheet picker',
      (tester) async {
    // Build with null initialNamespace — the screen should show the
    // namespace prompt and open the picker bottom sheet.
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([]),
      );

    await tester.binding.setSurfaceSize(const Size(800, 1600));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    final router = GoRouter(
      initialLocation: '/',
      routes: [
        GoRoute(
          path: '/',
          builder: (context, state) =>
              // null initialNamespace — picker must open.
              const VulnerabilitiesListScreen(),
        ),
        GoRoute(
          path: '/clusters/:clusterId/scanning/vulnerabilities/'
              ':namespace/:kind/:name',
          builder: (context, state) => const Scaffold(body: Text('detail')),
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

    // The namespace prompt screen shows a "pick namespace" button.
    // Verify the prompt state appears (the screen text and button).
    // The bottom sheet header and the prompt screen hint may both render
    // "Choose a namespace", so use findsWidgets (>=1).
    expect(find.textContaining('Choose a namespace'), findsWidgets,
        reason:
            'Null initialNamespace must trigger the namespace picker '
            'bottom sheet on first visit.');

    // A second pump+settle must NOT open a second bottom sheet
    // (_promptShown guard). We verify this by confirming the count
    // doesn't grow beyond what was found on the first frame.
    final countAfterFirst =
        tester.widgetList(find.textContaining('Choose a namespace')).length;
    await tester.pumpAndSettle(const Duration(milliseconds: 200));
    final countAfterSecond =
        tester.widgetList(find.textContaining('Choose a namespace')).length;
    expect(countAfterSecond, countAfterFirst,
        reason:
            '_promptShown guard must prevent a second picker from '
            'stacking on subsequent rebuilds.');
  });

  // #29 — free-text search filter.
  testWidgets('free-text search hides non-matching workloads', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([
          _row(name: 'alpha', scanner: 'trivy', critical: 1),
          _row(name: 'beta', scanner: 'trivy', high: 1),
          _row(name: 'gamma', scanner: 'trivy', medium: 1),
        ]),
      );

    await _pump(tester, mock);
    expect(find.text('alpha'), findsOneWidget);
    expect(find.text('beta'), findsOneWidget);
    expect(find.text('gamma'), findsOneWidget);

    // Type a substring that only matches 'alpha'. Use the hint-text
    // TextField to avoid ambiguity from the existing search content.
    await tester.enterText(
        find.widgetWithText(TextField, 'Search workloads or image refs'),
        'alpha');
    await tester.pumpAndSettle();

    // After filtering, 'beta' and 'gamma' workload rows must disappear.
    // For 'alpha': the search field itself also shows 'alpha' as entered
    // text (EditableText), so there are 2 matches. Verify at least one
    // non-EditableText match remains (the workload row's Text widget).
    expect(
      find.descendant(
          of: find.byType(CustomScrollView),
          matching: find.byWidgetPredicate(
              (w) => w is Text && w.data == 'alpha')),
      findsOneWidget,
      reason: 'Matching workload Text node must remain visible in the list.',
    );
    expect(
      find.descendant(
          of: find.byType(CustomScrollView), matching: find.text('beta')),
      findsNothing,
      reason: 'Non-matching workload must be hidden.',
    );
    expect(
      find.descendant(
          of: find.byType(CustomScrollView), matching: find.text('gamma')),
      findsNothing,
      reason: 'Non-matching workload must be hidden.',
    );
  });

  // #30 — scanner discriminator filter.
  testWidgets('Trivy chip hides Kubescape rows', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope([
          _row(name: 'trivy-workload', scanner: 'trivy', critical: 2),
          _row(name: 'kubescape-workload', scanner: 'kubescape', high: 1),
        ]),
      );

    await _pump(tester, mock);
    expect(find.text('trivy-workload'), findsOneWidget);
    expect(find.text('kubescape-workload'), findsOneWidget);
    // Both scanners present → discriminator chips render.
    expect(find.text('Trivy'), findsWidgets);

    // Tap the "Trivy" scanner discriminator chip.
    await tester.tap(find.widgetWithText(ChoiceChip, 'Trivy'));
    await tester.pumpAndSettle();

    expect(find.text('trivy-workload'), findsOneWidget,
        reason: 'Trivy workload must remain after Trivy chip tap.');
    expect(find.text('kubescape-workload'), findsNothing,
        reason: 'Kubescape row must be hidden after Trivy scanner filter.');
  });

  testWidgets('virtual scroll: large response only mounts visible window',
      (tester) async {
    // 50 rows overflows the viewport (each row is ≥80dp; 800x1600 fits
    // roughly 18 rows). Plan calls out a 5000-CVE perf check; that
    // scale is exercised by headless benchmark separately so the widget
    // test stays fast and stays within `pumpAndSettle`'s default budget.
    // The unique-per-row name `wl-$i` proves SliverChildBuilderDelegate
    // built only the visible window: `wl-0` lands on screen, `wl-49`
    // never does.
    final rows = List<Map<String, Object?>>.generate(
      50,
      (i) => _row(name: 'wl-$i', scanner: 'trivy', high: 1),
    );
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/scanning/status', body: _statusBoth())
      ..onJson(
        'GET',
        '/api/v1/scanning/vulnerabilities',
        body: _vulnEnvelope(rows),
      );

    await _pump(tester, mock);

    expect(find.text('wl-0'), findsOneWidget);
    expect(find.text('wl-49'), findsNothing,
        reason:
            'A 50-row list under virtual scroll must not construct '
            'every row; only the visible window mounts.');
  });
}
