import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/auth/oidc_repository.dart';

import '../support/mock_dio_adapter.dart';

Dio _newDio(MockDioAdapter adapter) {
  final dio = Dio(BaseOptions(
    baseUrl: 'http://localhost:8080',
    headers: {'X-Requested-With': 'XMLHttpRequest'},
  ));
  dio.httpClientAdapter = adapter;
  return dio;
}

void main() {
  group('fetchMobileConfig', () {
    test('parses authorizationEndpoint + clientID + scopes from envelope', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'GET',
          '/api/v1/auth/oidc/authelia/mobile-config',
          body: {
            'data': {
              'authorizationEndpoint': 'https://idp.example.com/oauth2/auth',
              'clientID': 'kubecenter-mobile',
              'scopes': ['openid', 'profile', 'email'],
            },
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      final cfg = await repo.fetchMobileConfig('authelia');

      expect(cfg.authorizationEndpoint, 'https://idp.example.com/oauth2/auth');
      expect(cfg.clientID, 'kubecenter-mobile');
      expect(cfg.scopes, ['openid', 'profile', 'email']);
    });

    test('routes to the provider-id-scoped path', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'GET',
          '/api/v1/auth/oidc/my-corp-okta/mobile-config',
          body: {
            'data': {
              'authorizationEndpoint': 'https://corp.okta.com/oauth2/v1/authorize',
              'clientID': 'abc',
              'scopes': ['openid'],
            },
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      await repo.fetchMobileConfig('my-corp-okta');

      expect(adapter.requests.single.path,
          '/api/v1/auth/oidc/my-corp-okta/mobile-config');
    });

    test('404 surfaces as ApiError with the backend message', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'GET',
          '/api/v1/auth/oidc/missing/mobile-config',
          status: 404,
          body: {
            'error': {'code': 404, 'message': 'unknown OIDC provider'},
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      try {
        await repo.fetchMobileConfig('missing');
        fail('expected ApiError');
      } on ApiError catch (e) {
        expect(e.statusCode, 404);
        expect(e.message, 'unknown OIDC provider');
      }
    });

    test('tolerates missing scopes array (returns empty list)', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'GET',
          '/api/v1/auth/oidc/x/mobile-config',
          body: {
            'data': {
              'authorizationEndpoint': 'https://idp/x',
              'clientID': 'y',
              // scopes intentionally omitted
            },
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      final cfg = await repo.fetchMobileConfig('x');
      expect(cfg.scopes, isEmpty);
    });
  });

  group('exchangeMobile', () {
    test('parses full token + user payload', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'POST',
          '/api/v1/auth/oidc/authelia/mobile-exchange',
          body: {
            'data': {
              'accessToken': 'jwt.access',
              'refreshToken': 'rand.refresh',
              'expiresIn': 900,
              'refreshExpiresIn': 3600,
              'user': {
                'username': 'alice@corp.io',
                'groups': ['k8scenter:users', 'oidc:devs'],
                'provider': 'oidc',
              },
            },
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      final result = await repo.exchangeMobile(
        providerID: 'authelia',
        code: 'auth-code-abc',
        codeVerifier: 'verifier-xyz',
        nonce: 'nonce-123',
      );

      expect(result.accessToken, 'jwt.access');
      expect(result.refreshToken, 'rand.refresh');
      expect(result.expiresIn, 900);
      expect(result.refreshExpiresIn, 3600);
      expect(result.username, 'alice@corp.io');
      expect(result.groups, ['k8scenter:users', 'oidc:devs']);
      expect(result.provider, 'oidc');
    });

    test('POSTs the canonical body shape', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'POST',
          '/api/v1/auth/oidc/x/mobile-exchange',
          body: {
            'data': {
              'accessToken': 't',
              'refreshToken': 'r',
              'expiresIn': 900,
              'refreshExpiresIn': 3600,
              'user': {'username': 'u', 'groups': <String>[], 'provider': 'oidc'},
            },
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      await repo.exchangeMobile(
        providerID: 'x',
        code: 'C',
        codeVerifier: 'V',
        nonce: 'N',
      );

      final req = adapter.requests.single;
      expect(req.method, 'POST');
      final data = req.data as Map<String, dynamic>;
      expect(data['code'], 'C');
      expect(data['codeVerifier'], 'V');
      expect(data['nonce'], 'N');
      // State is intentionally NOT in the body — the backend has no
      // server-side store to validate against. Mobile validates state
      // before invoking this method.
      expect(data.containsKey('state'), isFalse);
    });

    test('401 surfaces as ApiError', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'POST',
          '/api/v1/auth/oidc/x/mobile-exchange',
          status: 401,
          body: {
            'error': {'code': 401, 'message': 'oidc exchange failed'},
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      try {
        await repo.exchangeMobile(
          providerID: 'x',
          code: 'C',
          codeVerifier: 'V',
          nonce: 'N',
        );
        fail('expected ApiError');
      } on ApiError catch (e) {
        expect(e.statusCode, 401);
        expect(e.message, 'oidc exchange failed');
      }
    });

    test('403 (domain not allowed) surfaces with the backend message', () async {
      final adapter = MockDioAdapter()
        ..onJson(
          'POST',
          '/api/v1/auth/oidc/x/mobile-exchange',
          status: 403,
          body: {
            'error': {'code': 403, 'message': 'email domain not allowed'},
          },
        );

      final repo = OIDCRepository(_newDio(adapter));
      try {
        await repo.exchangeMobile(
          providerID: 'x',
          code: 'C',
          codeVerifier: 'V',
          nonce: 'N',
        );
        fail('expected ApiError');
      } on ApiError catch (e) {
        expect(e.statusCode, 403);
        expect(e.message, 'email domain not allowed');
      }
    });
  });
}
