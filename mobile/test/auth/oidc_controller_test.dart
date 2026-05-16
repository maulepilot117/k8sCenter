import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/auth_token_holder.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/oidc_controller.dart';
import 'package:kubecenter/auth/pending_oidc_store.dart';
import 'package:kubecenter/auth/secure_storage.dart';

import '../support/mock_dio_adapter.dart';

class _StubLauncher {
  final List<Uri> launched = [];
  Object? errorOnNext;

  Future<void> call(Uri uri) async {
    if (errorOnNext != null) {
      final e = errorOnNext!;
      errorOnNext = null;
      throw e;
    }
    launched.add(uri);
  }
}

({
  ProviderContainer container,
  MockDioAdapter mock,
  _StubLauncher launcher,
  InMemoryPendingOidcStore pending,
  DateTime Function() now,
}) _setup({
  String? host,
  DateTime? clock,
}) {
  final launcher = _StubLauncher();
  final pending = InMemoryPendingOidcStore();
  final mock = MockDioAdapter();
  var clockNow = clock ?? DateTime.utc(2026, 5, 16, 12, 0, 0);

  final container = ProviderContainer(overrides: [
    pendingOidcStoreProvider.overrideWithValue(pending),
    secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    oidcControllerProvider.overrideWith(() => OIDCController(
          launchUrl: launcher.call,
          universalLinkHost: host ?? 'k8scenter.test',
          now: () => clockNow,
        )),
    // Wire the refresh + main Dio through the mock adapter so the
    // repository (refreshDio) AND the auth-me hydration (main dio)
    // both speak to mocks.
    refreshDioProvider.overrideWith((ref) {
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost:8080'));
      dio.httpClientAdapter = mock;
      return dio;
    }),
    dioProvider.overrideWith((ref) {
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost:8080'));
      dio.httpClientAdapter = mock;
      return dio;
    }),
  ]);

  return (
    container: container,
    mock: mock,
    launcher: launcher,
    pending: pending,
    now: () => clockNow,
  );
}

void main() {
  group('OIDCController.startFlow', () {
    test('happy path: fetches config, persists pending, launches auth URL',
        () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.mock.onJson(
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

      await s.container.read(oidcControllerProvider.notifier).startFlow('authelia');

      // Launched exactly once.
      expect(s.launcher.launched, hasLength(1));
      final launched = s.launcher.launched.single;

      // Base URL preserved + PKCE params attached.
      expect(launched.origin, 'https://idp.example.com');
      expect(launched.path, '/oauth2/auth');
      expect(launched.queryParameters['client_id'], 'kubecenter-mobile');
      expect(launched.queryParameters['response_type'], 'code');
      expect(launched.queryParameters['scope'], 'openid profile email');
      expect(launched.queryParameters['redirect_uri'],
          'https://k8scenter.test/m/auth/callback');
      expect(launched.queryParameters['code_challenge_method'], 'S256');
      expect(launched.queryParameters['code_challenge'], isNotEmpty);
      expect(launched.queryParameters['state'], isNotEmpty);
      expect(launched.queryParameters['nonce'], isNotEmpty);
      // state and nonce are independently random.
      expect(launched.queryParameters['state'],
          isNot(equals(launched.queryParameters['nonce'])));

      // Pending state persisted.
      final persisted = await s.pending.read();
      expect(persisted!.providerID, 'authelia');
      expect(persisted.state, launched.queryParameters['state']);
      expect(persisted.nonce, launched.queryParameters['nonce']);
      expect(persisted.codeVerifier, isNotEmpty);
      expect(persisted.createdAtMillis, greaterThan(0));
    });

    test('universalLinkHost empty: errors before any network call', () async {
      final s = _setup(host: '');
      addTearDown(s.container.dispose);

      await s.container.read(oidcControllerProvider.notifier).startFlow('authelia');

      expect(s.launcher.launched, isEmpty);
      expect(s.mock.requests, isEmpty);
      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.universalLinkNotConfigured);
    });

    test('mobile-config 404: providerUnknown error', () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.mock.onJson(
        'GET',
        '/api/v1/auth/oidc/missing/mobile-config',
        status: 404,
        body: {
          'error': {'code': 404, 'message': 'unknown OIDC provider'},
        },
      );

      await s.container.read(oidcControllerProvider.notifier).startFlow('missing');

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.providerUnknown);
      // No pending state written (no PKCE generated yet).
      expect(await s.pending.read(), isNull);
    });

    test('mobile-config 500: networkError', () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.mock.onJson(
        'GET',
        '/api/v1/auth/oidc/x/mobile-config',
        status: 500,
        body: {
          'error': {'code': 500, 'message': 'boom'},
        },
      );

      await s.container.read(oidcControllerProvider.notifier).startFlow('x');

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.networkError);
    });

    test('launch failure clears pending state and reports the error', () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.mock.onJson(
        'GET',
        '/api/v1/auth/oidc/x/mobile-config',
        body: {
          'data': {
            'authorizationEndpoint': 'https://idp/x',
            'clientID': 'c',
            'scopes': ['openid'],
          },
        },
      );

      s.launcher.errorOnNext = Exception('custom-tabs not available');

      await s.container.read(oidcControllerProvider.notifier).startFlow('x');

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.customTabsLaunchFailed);
      // Pending was cleared so a retry starts fresh.
      expect(await s.pending.read(), isNull);
    });
  });

  group('OIDCController.completeFlow', () {
    Future<void> seedPending(
      InMemoryPendingOidcStore store,
      DateTime now, {
      String providerID = 'authelia',
      String state = 'STATE123',
      String verifier = 'VERIFIER456',
      String nonce = 'NONCE789',
    }) {
      return store.write(PendingOidc(
        providerID: providerID,
        state: state,
        codeVerifier: verifier,
        nonce: nonce,
        createdAtMillis: now.millisecondsSinceEpoch,
      ));
    }

    test('no pending state: silent no-op (does not error)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=X&state=Y'));

      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
    });

    test('state mismatch: stateMismatch error, clears pending', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'EXPECTED');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=X&state=DIFFERENT'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.stateMismatch);
      expect(await s.pending.read(), isNull);
    });

    test('consent denied: consentDenied error', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=access_denied&error_description=User+declined'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.consentDenied);
      expect(state.detail, 'User declined');
      expect(await s.pending.read(), isNull);
    });

    test('ttl expired: ttlExpired error', () async {
      // Create pending 6 minutes ago, advance clock 10 minutes.
      final past = DateTime.utc(2026, 5, 16, 12, 0, 0);
      final s = _setup(clock: past);
      addTearDown(s.container.dispose);
      await seedPending(s.pending, past);

      // Re-create container with a later clock since we can't mutate the
      // controller's clock function post-build. Instead seed pending
      // BEFORE swapping the controller.
      // Simpler: write pending with a stale createdAtMillis directly.
      await s.pending.write(PendingOidc(
        providerID: 'x',
        state: 'STATE',
        codeVerifier: 'V',
        nonce: 'N',
        createdAtMillis:
            past.subtract(const Duration(minutes: 6)).millisecondsSinceEpoch,
      ));

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=X&state=STATE'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.ttlExpired);
      expect(await s.pending.read(), isNull);
    });

    test('missing code in callback: internalError', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.internalError);
    });

    test('exchange 401: exchangeRejected', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      s.mock.onJson(
        'POST',
        '/api/v1/auth/oidc/authelia/mobile-exchange',
        status: 401,
        body: {
          'error': {'code': 401, 'message': 'oidc exchange failed'},
        },
      );

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.exchangeRejected);
      expect(await s.pending.read(), isNull);
    });

    test('exchange 403: domainNotAllowed', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      s.mock.onJson(
        'POST',
        '/api/v1/auth/oidc/authelia/mobile-exchange',
        status: 403,
        body: {
          'error': {'code': 403, 'message': 'email domain not allowed'},
        },
      );

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.domainNotAllowed);
    });

    test('happy path: writes tokens + transitions auth state', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      s.mock.onJson(
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
              'groups': ['k8scenter:users'],
              'provider': 'oidc',
            },
          },
        },
      );

      // /v1/auth/me is called by applyAuthTokens → _hydrateUser
      s.mock.onJson(
        'GET',
        '/api/v1/auth/me',
        body: {
          'data': {
            'user': {
              'id': 'oidc:authelia:sub-1',
              'username': 'alice@corp.io',
              'provider': 'oidc',
              'kubernetesUsername': 'alice@corp.io',
              'kubernetesGroups': ['k8scenter:users'],
              'roles': ['user'],
            },
            'rbac': {
              'summary': const <String, dynamic>{},
            },
          },
        },
      );

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      // Token holder + secure storage populated.
      expect(s.container.read(authTokenHolderProvider).accessToken,
          'jwt.access');
      expect(
          await s.container.read(secureTokenStoreProvider).readRefreshToken(),
          'rand.refresh');

      // Auth state machine transitioned (applyAuthTokens fired hydrate).
      expect(s.container.read(authRepositoryProvider),
          isA<AuthAuthenticated>());

      // Flow returns to idle; pending cleared.
      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
      expect(await s.pending.read(), isNull);
    });
  });

  group('clearError', () {
    test('returns to idle from error', () {
      final s = _setup(host: '');
      addTearDown(s.container.dispose);

      // Trigger the universal-link error path.
      s.container.read(oidcControllerProvider.notifier).startFlow('x');
      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowError>());

      s.container.read(oidcControllerProvider.notifier).clearError();
      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
    });

    test('idle stays idle', () {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.container.read(oidcControllerProvider.notifier).clearError();
      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
    });
  });
}
