// LogFilterBar widget tests — query construction, mode swap, cascade
// dropdowns, admin-gated namespace requirement.

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/features/observability/logs/log_filter_bar.dart';
import 'package:kubecenter/features/observability/logs/log_search_controller.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart' show buildKubeTheme;

import '../../../support/mock_dio_adapter.dart';

/// Minimal stub auth controller that resolves to an authenticated user
/// with the chosen role. Avoids spinning up the real /v1/auth/me flow
/// for filter-bar widget tests.
class _StubAuthController extends AuthRepository {
  _StubAuthController(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

AuthState _stubAuth({required bool admin}) {
  return AuthAuthenticated(
    user: UserInfo(
      id: 'u-1',
      username: admin ? 'root' : 'alice',
      provider: 'local',
      roles: admin ? const ['admin'] : const ['operator'],
    ),
    rbac: const RBACSummary(),
  );
}

Future<LogFilterBarHarness> _pump(
  WidgetTester tester, {
  required MockDioAdapter mock,
  required bool admin,
}) async {
  final submitted = <LogSearchParams>[];
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendUrlProvider.overrideWithValue('http://test'),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        dioProvider.overrideWith((ref) {
          final dio = Dio(BaseOptions(baseUrl: 'http://test'));
          dio.httpClientAdapter = mock;
          return dio;
        }),
        refreshDioProvider.overrideWith((ref) {
          final dio = Dio(BaseOptions(baseUrl: 'http://test'));
          dio.httpClientAdapter = mock;
          return dio;
        }),
        authRepositoryProvider.overrideWith(
          () => _StubAuthController(_stubAuth(admin: admin)),
        ),
      ],
      child: MaterialApp(
        theme: buildKubeTheme('nexus'),
        home: Scaffold(
          body: SingleChildScrollView(
            child: LogFilterBar(onSubmit: submitted.add),
          ),
        ),
      ),
    ),
  );
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 50));
  return LogFilterBarHarness(tester: tester, submitted: submitted);
}

class LogFilterBarHarness {
  LogFilterBarHarness({required this.tester, required this.submitted});
  final WidgetTester tester;
  final List<LogSearchParams> submitted;
}

void main() {
  group('LogFilterBar.runEnabled', () {
    testWidgets('non-admin without namespace → Run is disabled',
        (tester) async {
      final mock = MockDioAdapter();
      // Namespace label values come back empty so the dropdown
      // stays unset.
      mock.onJson(
        'GET',
        '/api/v1/logs/labels/namespace/values',
        body: {'data': <String>[]},
      );

      final h = await _pump(tester, mock: mock, admin: false);
      final runButton = find.byKey(const ValueKey('logFilter-runButton'));
      // The Run FilledButton renders as disabled when onPressed is
      // null — Flutter exposes this through the underlying button
      // state, not as a `disabled` field.
      final pressedFn = (tester.widget(runButton) as FilledButton).onPressed;
      expect(pressedFn, isNull);
      // And the hint text is visible.
      expect(find.textContaining('Pick a namespace to run'), findsOneWidget);
      // Sanity: nothing submitted.
      await tester.tap(runButton, warnIfMissed: false);
      await tester.pump();
      expect(h.submitted, isEmpty);
    });

    testWidgets('admin without namespace → Run is enabled (cluster-wide)',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson(
        'GET',
        '/api/v1/logs/labels/namespace/values',
        body: {'data': <String>[]},
      );
      // Admin can submit a cluster-wide query. The backend lets this
      // through; the test confirms the client mirrors the policy.
      mock.onJson('GET', '/api/v1/logs/query', body: {
        'data': {
          'data': {'resultType': 'streams', 'result': <Object>[]},
        },
      });

      await _pump(tester, mock: mock, admin: true);
      final runButton = find.byKey(const ValueKey('logFilter-runButton'));
      final pressedFn = (tester.widget(runButton) as FilledButton).onPressed;
      expect(pressedFn, isNotNull);
      expect(find.textContaining('Pick a namespace to run'), findsNothing);
    });
  });

  group('LogFilterBar.search-mode query construction', () {
    testWidgets('namespace + pod prefix + container + severity + contains',
        (tester) async {
      final mock = MockDioAdapter();
      // Namespace values returned so the dropdown is non-empty.
      mock.onJson(
        'GET',
        '/api/v1/logs/labels/namespace/values',
        body: {'data': ['app']},
      );
      mock.onJson(
        'GET',
        '/api/v1/logs/labels/pod/values',
        body: {'data': ['web-abc']},
      );
      mock.onJson(
        'GET',
        '/api/v1/logs/labels/container/values',
        body: {'data': ['web']},
      );

      final h = await _pump(tester, mock: mock, admin: false);

      // Pick namespace='app'.
      await tester.tap(find.byKey(const ValueKey('logFilter-namespace')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('app').last);
      await tester.pumpAndSettle();
      // Pod cascade resolves.
      await tester.pump(const Duration(milliseconds: 50));

      // Pick pod='web-abc' (selectable now).
      await tester.tap(find.byKey(const ValueKey('logFilter-pod')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('web-abc').last);
      await tester.pumpAndSettle();
      await tester.pump(const Duration(milliseconds: 50));

      // Pick container='web'.
      await tester.tap(find.byKey(const ValueKey('logFilter-container')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('web').last);
      await tester.pumpAndSettle();

      // Pick severity = Error.
      await tester.tap(find.byKey(const ValueKey('logFilter-severity-error')));
      await tester.pumpAndSettle();

      // Type free-text.
      await tester.enterText(
        find.byKey(const ValueKey('logFilter-freeText')),
        'timeout',
      );
      await tester.pumpAndSettle();

      // Run.
      await tester.tap(find.byKey(const ValueKey('logFilter-runButton')));
      await tester.pumpAndSettle();

      expect(h.submitted, hasLength(1));
      final p = h.submitted.single;
      expect(p.namespace, 'app');
      expect(
        p.query,
        '{namespace="app",pod=~"web-abc.*",container="web"} '
        '| level=~"(?i)error" |= "timeout"',
      );
    });

    testWidgets('only namespace set → query is `{namespace="X"}`',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': ['app']});
      mock.onJson('GET', '/api/v1/logs/labels/pod/values',
          body: {'data': <String>[]});

      final h2 = await _pump(tester, mock: mock, admin: false);
      await tester.tap(find.byKey(const ValueKey('logFilter-namespace')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('app').last);
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('logFilter-runButton')));
      await tester.pumpAndSettle();

      expect(h2.submitted.single.query, '{namespace="app"}');
    });
  });

  group('LogFilterBar.mode swap', () {
    testWidgets('search → LogQL seeds raw textarea with built query',
        (tester) async {
      final mock = MockDioAdapter();
      mock.onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': ['app']});
      mock.onJson('GET', '/api/v1/logs/labels/pod/values',
          body: {'data': <String>[]});

      await _pump(tester, mock: mock, admin: false);
      // Configure namespace=app + severity=warn.
      await tester.tap(find.byKey(const ValueKey('logFilter-namespace')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('app').last);
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('logFilter-severity-warn')));
      await tester.pumpAndSettle();

      // Swap to LogQL.
      await tester.tap(find.text('LogQL'));
      await tester.pumpAndSettle();

      // Raw textarea is seeded with the built query.
      final raw = tester.widget<TextField>(
        find.byKey(const ValueKey('logFilter-rawLogql')),
      );
      expect(raw.controller!.text,
          '{namespace="app"} | level=~"(?i)warn"');
    });
  });
}
