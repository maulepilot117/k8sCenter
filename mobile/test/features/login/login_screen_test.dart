// Login screen widget tests: empty validation, successful submit reaches
// authRepository.login, and error from a previous attempt renders inline.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/login/login_screen.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../../support/mock_dio_adapter.dart';

ResponseBody _json(Map<String, dynamic> body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

Widget _harness(ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: const LoginScreen(),
    ),
  );
}

void main() {
  testWidgets('empty submit shows validators and does not call login',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);
    mock.onJson('GET', '/api/v1/auth/providers', body: {'data': <Object>[]});

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('login-submit')));
    await tester.pumpAndSettle();

    expect(find.text('Required'), findsNWidgets(2));
    // No POST to login fired.
    expect(
      mock.requests.where((r) => r.path.contains('/auth/login')),
      isEmpty,
    );
  });

  testWidgets('valid submit transitions Authenticating then Authenticated',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson('GET', '/api/v1/auth/providers', body: {'data': <Object>[]});
    mock.onJson(
      'POST',
      '/api/v1/auth/login',
      body: {
        'data': {
          'accessToken': 'access-1',
          'refreshToken': 'refresh-1',
          'expiresIn': 900,
        },
      },
    );
    mock.onJson(
      'GET',
      '/api/v1/auth/me',
      body: {
        'data': {
          'user': {
            'id': 'u1',
            'username': 'admin',
            'provider': 'local',
            'roles': ['admin'],
          },
          'rbac': <String, dynamic>{},
        },
      },
    );

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    await tester.enterText(
        find.byKey(const ValueKey('login-username')), 'admin');
    await tester.enterText(
        find.byKey(const ValueKey('login-password')), 'password');
    await tester.tap(find.byKey(const ValueKey('login-submit')));
    await tester.pumpAndSettle();

    expect(container.read(authRepositoryProvider), isA<AuthAuthenticated>());
  });

  testWidgets('error message from previous attempt renders inline',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson('GET', '/api/v1/auth/providers', body: {'data': <Object>[]});
    mock.on('POST', '/api/v1/auth/login', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'invalid credentials'},
        },
        status: 401,
      );
    });

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    await tester.enterText(
        find.byKey(const ValueKey('login-username')), 'admin');
    await tester.enterText(
        find.byKey(const ValueKey('login-password')), 'wrong');
    await tester.tap(find.byKey(const ValueKey('login-submit')));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-error')), findsOneWidget);
    expect(find.text('invalid credentials'), findsOneWidget);
  });

  testWidgets('provider dropdown hidden when only one credential provider',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);
    mock.onJson(
      'GET',
      '/api/v1/auth/providers',
      body: {
        'data': [
          {'id': 'local', 'name': 'Local', 'kind': 'credential'},
        ],
      },
    );

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-provider')), findsNothing);
  });

  testWidgets('provider dropdown rendered when multiple credential providers',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);
    mock.onJson(
      'GET',
      '/api/v1/auth/providers',
      body: {
        'data': [
          {'id': 'local', 'name': 'Local', 'kind': 'credential'},
          {'id': 'ldap', 'name': 'Corporate LDAP', 'kind': 'credential'},
        ],
      },
    );

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-provider')), findsOneWidget);
  });
}
