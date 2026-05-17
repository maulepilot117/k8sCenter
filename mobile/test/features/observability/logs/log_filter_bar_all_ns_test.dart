// PR-5f admin "All namespaces" checkbox tests.
//
// The checkbox is admin-only. When checked, the namespace dropdown is
// disabled and the generated LogQL has no namespace label selector.
// Non-admin users must not see the checkbox at all.

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

Future<List<LogSearchParams>> _pump(
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
  return submitted;
}

void main() {
  testWidgets('non-admin user sees no checkbox', (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>[]});
    await _pump(tester, mock: mock, admin: false);
    expect(
      find.byKey(const ValueKey('logFilter-allNamespaces')),
      findsNothing,
      reason:
          'Non-admin users must not see the all-namespaces toggle — '
          'backend would 403 the cluster-wide query anyway.',
    );
  });

  testWidgets('admin sees the checkbox above the namespace dropdown',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>['app', 'kube-system']});
    await _pump(tester, mock: mock, admin: true);
    expect(
      find.byKey(const ValueKey('logFilter-allNamespaces')),
      findsOneWidget,
    );
    expect(find.text('All namespaces (admin)'), findsOneWidget);
  });

  testWidgets(
      'checking the box disables the namespace dropdown',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>['app']});
    await _pump(tester, mock: mock, admin: true);

    // Tap to enable all-namespaces.
    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();

    final dropdown = tester.widget<DropdownButtonFormField<String?>>(
      find.byKey(const ValueKey('logFilter-namespace')),
    );
    expect(dropdown.onChanged, isNull,
        reason: 'Namespace dropdown must be disabled when checkbox is on');
  });

  testWidgets(
      'checked checkbox → query has no namespace label selector',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>['app']})
      ..onJson('GET', '/api/v1/logs/labels/pod/values',
          body: {'data': <String>[]});

    final submitted = await _pump(tester, mock: mock, admin: true);

    // Enable all-namespaces.
    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();
    // Pick a severity to put SOMETHING in the query so it's distinct
    // from the no-filter case.
    await tester
        .tap(find.byKey(const ValueKey('logFilter-severity-error')));
    await tester.pumpAndSettle();
    // Submit.
    await tester.tap(find.byKey(const ValueKey('logFilter-runButton')));
    await tester.pumpAndSettle();

    expect(submitted, hasLength(1));
    expect(submitted.single.namespace, isNull,
        reason: 'Submission must carry namespace=null on all-NS path');
    expect(
      submitted.single.query.contains('namespace='),
      isFalse,
      reason: 'LogQL must not include a namespace label selector when '
          'the all-namespaces checkbox is on',
    );
    expect(submitted.single.query, '{} | level=~"(?i)error"');
  });

  testWidgets(
      'Run button disabled when all-NS checked AND no other filter set',
      (tester) async {
    // The fix for the `{}` LogQL emission: admin all-NS without any
    // other matcher must keep Run disabled instead of submitting a
    // query Loki will reject.
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>[]});
    await _pump(tester, mock: mock, admin: true);

    // Enable all-namespaces.
    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();

    final runBtn = find.byKey(const ValueKey('logFilter-runButton'));
    final pressedFn = (tester.widget(runBtn) as FilledButton).onPressed;
    expect(pressedFn, isNull,
        reason: 'Run must stay disabled to prevent emitting `{}` LogQL');
  });

  testWidgets(
      'Run button enabled once all-NS + a Contains filter are set',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>[]});
    await _pump(tester, mock: mock, admin: true);

    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('logFilter-freeText')),
      'timeout',
    );
    await tester.pumpAndSettle();

    final pressedFn = (tester.widget(
            find.byKey(const ValueKey('logFilter-runButton'))) as FilledButton)
        .onPressed;
    expect(pressedFn, isNotNull,
        reason: 'A free-text filter is sufficient to unblock submission');
  });

  testWidgets(
      'LogQL escapes double-quote and backslash in free-text Contains',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': const ['app']})
      ..onJson('GET', '/api/v1/logs/labels/pod/values',
          body: {'data': <String>[]});
    final submitted = await _pump(tester, mock: mock, admin: false);

    await tester.tap(find.byKey(const ValueKey('logFilter-namespace')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('app').last);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('logFilter-freeText')),
      r'a"b\c',
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('logFilter-runButton')));
    await tester.pumpAndSettle();

    expect(submitted, hasLength(1));
    // Backslash escaped first, then double-quote, matching the
    // escapeLogQLLiteral implementation order.
    expect(submitted.single.query, r'{namespace="app"} |= "a\"b\\c"');
  });

  testWidgets(
      'unchecking the box re-enables the namespace dropdown',
      (tester) async {
    final mock = MockDioAdapter()
      ..onJson('GET', '/api/v1/logs/labels/namespace/values',
          body: {'data': <String>['app']});
    await _pump(tester, mock: mock, admin: true);

    // Check then uncheck.
    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('logFilter-allNamespaces')));
    await tester.pumpAndSettle();

    final dropdown = tester.widget<DropdownButtonFormField<String?>>(
      find.byKey(const ValueKey('logFilter-namespace')),
    );
    expect(dropdown.onChanged, isNotNull,
        reason: 'Namespace dropdown must re-enable when checkbox is off');
  });
}
