// Auth repository tests covering bootstrap, login, refresh, and logout
// state transitions against a mocked Dio adapter.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/auth_token_holder.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/secure_storage.dart';

import '../support/mock_dio_adapter.dart';

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
  // Attach the mock adapter to both Dio instances.
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

void main() {
  test('login: success stores tokens and transitions to Authenticated',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

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

    await container.read(authRepositoryProvider.notifier).login(
          username: 'admin',
          password: 'password1234',
        );

    final state = container.read(authRepositoryProvider);
    expect(state, isA<AuthAuthenticated>());
    expect((state as AuthAuthenticated).user.username, 'admin');
    expect(container.read(authTokenHolderProvider).accessToken, 'access-1');

    final stored = await container
        .read(secureTokenStoreProvider)
        .readRefreshToken();
    expect(stored, 'refresh-1');
  });

  test('login: invalid credentials stays Unauthenticated with error message',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.on('POST', '/api/v1/auth/login', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'invalid credentials'},
        },
        status: 401,
      );
    });

    await container.read(authRepositoryProvider.notifier).login(
          username: 'admin',
          password: 'wrong',
        );

    final state = container.read(authRepositoryProvider);
    expect(state, isA<AuthUnauthenticated>());
    expect((state as AuthUnauthenticated).errorMessage, 'invalid credentials');
    expect(container.read(authTokenHolderProvider).accessToken, isNull);
  });

  // Issue #279 — when /v1/auth/login returns 200 but /v1/auth/me errors
  // (or returns a malformed payload), the credentials path must roll
  // back the tokens it just wrote. Without rollback, the refresh token
  // survives in secure_storage and the user gets silently re-auth'd on
  // next cold-start despite being told "Sign-in failed".
  test(
      'login: rolls back tokens when /auth/me fails after /auth/login succeeds',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson(
      'POST',
      '/api/v1/auth/login',
      body: {
        'data': {
          'accessToken': 'leaked-access',
          'refreshToken': 'leaked-refresh',
          'expiresIn': 900,
        },
      },
    );
    mock.on('GET', '/api/v1/auth/me', (_) {
      return _json(
        {
          'error': {'code': 500, 'message': 'hydrate failed'},
        },
        status: 500,
      );
    });

    await container.read(authRepositoryProvider.notifier).login(
          username: 'admin',
          password: 'password1234',
        );

    final state = container.read(authRepositoryProvider);
    expect(state, isA<AuthUnauthenticated>());

    // The critical assertion: no token survives the failed login.
    expect(
      container.read(authTokenHolderProvider).accessToken,
      isNull,
      reason: 'access token must be cleared after hydration failure',
    );
    final stored = await container
        .read(secureTokenStoreProvider)
        .readRefreshToken();
    expect(
      stored,
      isNull,
      reason: 'refresh token must be deleted from secure_storage '
          'after hydration failure (issue #279)',
    );
  });

  test(
      'login: rolls back when /auth/me returns 200 with malformed payload',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson(
      'POST',
      '/api/v1/auth/login',
      body: {
        'data': {
          'accessToken': 'leaked-access',
          'refreshToken': 'leaked-refresh',
          'expiresIn': 900,
        },
      },
    );
    // /auth/me returns 200 but the user object is missing — same
    // AuthUnauthenticated transition as a 500, must also roll back.
    mock.onJson(
      'GET',
      '/api/v1/auth/me',
      body: {'data': <String, dynamic>{}},
    );

    await container.read(authRepositoryProvider.notifier).login(
          username: 'admin',
          password: 'password1234',
        );

    expect(container.read(authRepositoryProvider), isA<AuthUnauthenticated>());
    expect(container.read(authTokenHolderProvider).accessToken, isNull);
    final stored = await container
        .read(secureTokenStoreProvider)
        .readRefreshToken();
    expect(stored, isNull);
  });

  test('bootstrap: with no stored token transitions to Unauthenticated',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    await container.read(authRepositoryProvider.notifier).bootstrap();
    expect(container.read(authRepositoryProvider), isA<AuthUnauthenticated>());
  });

  test('bootstrap: stored token exchanges via body-mode refresh and hydrates',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('refresh-cold');

    mock.onJson(
      'POST',
      '/api/v1/auth/refresh',
      body: {
        'data': {
          'accessToken': 'access-cold',
          'refreshToken': 'refresh-rotated',
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

    await container.read(authRepositoryProvider.notifier).bootstrap();
    expect(container.read(authRepositoryProvider), isA<AuthAuthenticated>());
    expect(container.read(authTokenHolderProvider).accessToken, 'access-cold');
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      'refresh-rotated',
    );
  });

  test('bootstrap: stale refresh token clears storage and lands at /login',
      () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('stale');

    mock.on('POST', '/api/v1/auth/refresh', (_) {
      return _json(
        {
          'error': {'code': 401, 'message': 'invalid or expired refresh token'},
        },
        status: 401,
      );
    });

    await container.read(authRepositoryProvider.notifier).bootstrap();
    expect(container.read(authRepositoryProvider), isA<AuthUnauthenticated>());
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      isNull,
    );
  });

  test('logout: clears tokens and transitions to Unauthenticated', () async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    container.read(authTokenHolderProvider).set('access-x');
    await container
        .read(secureTokenStoreProvider)
        .writeRefreshToken('refresh-x');

    mock.onJson(
      'POST',
      '/api/v1/auth/logout',
      body: {'data': <String, dynamic>{}},
    );

    await container.read(authRepositoryProvider.notifier).logout();
    expect(container.read(authRepositoryProvider), isA<AuthUnauthenticated>());
    expect(container.read(authTokenHolderProvider).accessToken, isNull);
    expect(
      await container.read(secureTokenStoreProvider).readRefreshToken(),
      isNull,
    );
  });

  test('login providers: returns all providers (credential + OIDC)', () async {
    // PR-5c update: listProviders no longer filters out OIDC providers.
    // The login screen filters by `kind` itself: credential providers
    // feed the dropdown, OIDC providers render as separate buttons.
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson(
      'GET',
      '/api/v1/auth/providers',
      body: {
        'data': [
          {'id': 'local', 'name': 'Local', 'kind': 'credential'},
          {'id': 'ldap', 'name': 'Corporate LDAP', 'kind': 'credential'},
          {'id': 'oidc-google', 'name': 'Google', 'kind': 'oidc'},
        ],
      },
    );

    final providers =
        await container.read(authRepositoryProvider.notifier).listProviders();
    expect(providers, hasLength(3));
    expect(providers.map((p) => p.id),
        containsAll(['local', 'ldap', 'oidc-google']));
    // Helper-property still tagged correctly for the login-screen filter.
    expect(providers.where((p) => p.isCredentialProvider), hasLength(2));
    expect(providers.where((p) => !p.isCredentialProvider).single.id,
        'oidc-google');
  });
}
