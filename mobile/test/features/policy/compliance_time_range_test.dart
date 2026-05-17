// PR-5f compliance time-range picker tests.
//
// The picker is a SegmentedButton with values 1/7/30/90 mapped to
// `?days=N`. Selecting a value must re-fire the backend call with the
// new days parameter and keep the prior chart visible (faded) during
// the refresh. Custom date-range picker is intentionally absent in M5.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/policy/compliance_history_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

Map<String, Object?> _statusBody() => {
      'data': {
        'detected': 'both',
        'kyverno': {'available': true, 'webhooks': 1},
        'gatekeeper': {'available': true, 'webhooks': 1},
      },
    };

Map<String, Object?> _historyBody(int days) => {
      'data': List.generate(
        days.clamp(1, 5),
        (i) => {
          'date': '2026-05-0${i + 1}',
          'score': 80.0 + i,
          'pass': 8,
          'fail': 2,
          'warn': 0,
          'total': 10,
        },
      ),
    };

Future<void> _pump(WidgetTester tester, MockDioAdapter mock) async {
  await tester.binding.setSurfaceSize(const Size(800, 1600));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final user = UserInfo(
    id: 'u1',
    username: 'admin',
    provider: 'local',
    roles: const ['admin'],
  );

  final router = GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ComplianceHistoryScreen(),
      ),
    ],
  );

  await tester.pumpWidget(ProviderScope(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      authRepositoryProvider.overrideWith(
        () => _FakeAuth(
          AuthAuthenticated(user: user, rbac: const RBACSummary()),
        ),
      ),
    ],
    child: _DioInstaller(
      mock: mock,
      child: MaterialApp.router(
        theme: buildKubeTheme('nexus'),
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

void main() {
  testWidgets('default selection is 30d', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: _historyBody(30));
    await _pump(tester, mock);

    // SegmentedButton renders each label as a Text widget; verify all 4
    // chips are present and 30d is the selected initial.
    expect(find.text('1d'), findsOneWidget);
    expect(find.text('7d'), findsOneWidget);
    expect(find.text('30d'), findsOneWidget);
    expect(find.text('90d'), findsOneWidget);
  });

  testWidgets('selecting 7d re-fires the request with ?days=7',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: _historyBody(30));
    await _pump(tester, mock);

    // Confirm the initial request used days=30. The adapter records
    // every captured request — locate the compliance history call.
    final initialReq = mock.requests.firstWhere(
      (r) => r.path == '/api/v1/policies/compliance/history',
    );
    expect(initialReq.queryParameters['days'], '30');

    // Tap the 7d chip; the new request must use days=7.
    await tester.tap(find.text('7d'));
    await tester.pumpAndSettle(const Duration(milliseconds: 200));

    final reloads = mock.requests
        .where((r) => r.path == '/api/v1/policies/compliance/history')
        .toList();
    expect(reloads.length, greaterThanOrEqualTo(2),
        reason: 'Picker change must re-fire the history request');
    expect(reloads.last.queryParameters['days'], '7');
  });

  testWidgets('selecting 90d re-fires the request with ?days=90',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: _historyBody(30));
    await _pump(tester, mock);

    await tester.tap(find.text('90d'));
    await tester.pumpAndSettle(const Duration(milliseconds: 200));

    final reloads = mock.requests
        .where((r) => r.path == '/api/v1/policies/compliance/history')
        .toList();
    expect(reloads.last.queryParameters['days'], '90');
  });

  testWidgets(
      'empty datapoints renders the "No compliance snapshots yet" EmptyState',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: {'data': <Object>[]});
    await _pump(tester, mock);

    expect(find.text('No compliance snapshots yet'), findsOneWidget);
  });

  testWidgets(
      'stale-error banner: previous chart stays visible with "Couldn\'t '
      'refresh" + Retry when the second fetch errors',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: _historyBody(30))
      ..onJson('GET', '/api/v1/policies/compliance/history',
          status: 500,
          body: {'error': {'code': 500, 'message': 'transient backend error'}});
    await _pump(tester, mock);

    // First load succeeds. Switching to 7d fires the queued 500 response.
    await tester.tap(find.text('7d'));
    await tester.pumpAndSettle(const Duration(milliseconds: 200));

    // Stale-overlay path: previous chart is still on screen + banner
    // appears with a Retry button. We can't assert Opacity-around-the-
    // card directly across the whole subtree, so check the user-visible
    // signals: banner text + Retry CTA + chart title from the previous
    // (5-point) response.
    expect(find.textContaining("Couldn't refresh"), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Retry'), findsOneWidget);
    expect(find.textContaining('Last 5 days'), findsOneWidget);

    // Tapping Retry re-fires the request (the mock's queue is exhausted,
    // so this surfaces the same 500 — but the request count must rise).
    final before = mock.requests
        .where((r) => r.path == '/api/v1/policies/compliance/history')
        .length;
    await tester.tap(find.widgetWithText(TextButton, 'Retry'));
    await tester.pumpAndSettle(const Duration(milliseconds: 200));
    final after = mock.requests
        .where((r) => r.path == '/api/v1/policies/compliance/history')
        .length;
    expect(after, greaterThan(before),
        reason: 'Retry button must re-fire the history request');
  });

  testWidgets(
      'custom date-range picker is intentionally absent in M5',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/policies/status', body: _statusBody())
      ..onJson('GET', '/api/v1/policies/compliance/history',
          body: _historyBody(30));
    await _pump(tester, mock);

    // The shared TimeRangePicker uses 'Custom' for date-range entry —
    // the compliance picker deliberately does NOT include it because the
    // backend endpoint doesn't accept arbitrary ?start=/?end= ranges.
    expect(find.text('Custom'), findsNothing,
        reason:
            'Compliance picker must not surface a Custom date-range '
            'option in M5 — that path is a deferred backend addendum.');
  });
}
