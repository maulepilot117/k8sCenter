import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/auth_token_holder.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/auth_repository.dart';
import 'package:kubecenter/auth/auth_state.dart';
import 'package:kubecenter/auth/oidc_controller.dart';
import 'package:kubecenter/auth/oidc_repository.dart';
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

/// Stub repository that throws a plain Exception from exchangeMobile —
/// exercises the catch-all branch in [OIDCController.completeFlow] that
/// handles errors not translated to [ApiError] (parse failures, etc.).
class _ThrowingOIDCRepository implements OIDCRepository {
  @override
  Future<OIDCMobileAuthConfig> fetchMobileConfig(String providerID) {
    throw UnimplementedError();
  }

  @override
  Future<OIDCExchangeResult> exchangeMobile({
    required String providerID,
    required String code,
    required String codeVerifier,
    required String nonce,
  }) async {
    throw Exception('boom');
  }
}

/// Stub repository whose exchangeMobile never completes — parks the
/// controller in [OIDCFlowExchanging] so a test can prove
/// [OIDCController.abandonLaunching] leaves an in-flight exchange alone.
class _HangingOIDCRepository implements OIDCRepository {
  final Completer<OIDCExchangeResult> _never = Completer();

  @override
  Future<OIDCMobileAuthConfig> fetchMobileConfig(String providerID) {
    throw UnimplementedError();
  }

  @override
  Future<OIDCExchangeResult> exchangeMobile({
    required String providerID,
    required String code,
    required String codeVerifier,
    required String nonce,
  }) {
    return _never.future;
  }
}

/// Pending store whose [read] never completes — suspends
/// [OIDCController.completeFlow] in its pre-Exchanging read window, where
/// state is still [OIDCFlowLaunching] and the controller's `_resolving`
/// flag is set. Proves [OIDCController.abandonLaunching] no-ops during
/// that window. [write]/[clear] behave normally so [OIDCController.startFlow]
/// can still park the controller in [OIDCFlowLaunching].
class _HangingReadPendingOidcStore implements PendingOidcStore {
  final Completer<PendingOidc?> _never = Completer();

  @override
  Future<PendingOidc?> read() => _never.future;

  @override
  Future<void> write(PendingOidc pending) async {}

  @override
  Future<void> clear() async {}
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
            'clientId': 'kubecenter-mobile',
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
            'clientId': 'c',
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

    test(
        'consent denied with matched state: consentDenied error, clears '
        'pending (P3-6 happy path)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'STATE123');

      // Audit finding P3-6: error callbacks must carry a matched state
      // to be honored. The legitimate IdP redirect always includes the
      // state it received in the auth request.
      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=access_denied&error_description=User+declined&state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.consentDenied);
      expect(state.detail, 'User declined');
      expect(await s.pending.read(), isNull);
    });

    test(
        'error callback with NO state: silently dropped, pending preserved '
        '(P3-6 — login DoS guard)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'EXPECTED');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=access_denied'));

      // No state surfaced — controller stays idle so the legitimate
      // success callback can still race-in and complete the flow.
      expect(
        s.container.read(oidcControllerProvider),
        isA<OIDCFlowIdle>(),
        reason: 'no error banner should appear from an unbound callback',
      );
      // Pending state preserved — the legitimate callback still has its
      // verifier/state intact.
      expect(
        await s.pending.read(),
        isNotNull,
        reason: 'attacker callback must not wipe in-flight verifier/state',
      );
    });

    test(
        'error callback with WRONG state: silently dropped, pending preserved '
        '(P3-6 — login DoS guard)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'EXPECTED');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=access_denied&state=ATTACKER_STATE'));

      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
      expect(
        await s.pending.read(),
        isNotNull,
        reason: 'mismatched state must not clear pending',
      );
    });

    // Cross-reviewer testing T-1 (Phase 5): the error-code switch routes
    // five distinct OAuth/OIDC error values to different controller
    // reasons. Only access_denied was exercised before. A subtle swap
    // (e.g., routing temporarily_unavailable to exchangeRejected instead
    // of networkError) would not be caught. One test per branch through
    // the matched-state path so the switch can't drift undetected.

    test('login_required → consentDenied (matched state)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'S');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=login_required&state=S'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.consentDenied);
      expect(await s.pending.read(), isNull);
    });

    test('interaction_required → consentDenied (matched state)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'S');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=interaction_required&state=S'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.consentDenied);
    });

    test('temporarily_unavailable → networkError (matched state)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'S');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=temporarily_unavailable&state=S'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.networkError);
    });

    test('server_error → networkError (matched state)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'S');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=server_error&state=S'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.networkError);
    });

    test(
        'unknown error code → exchangeRejected (matched state, wildcard '
        'branch)', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now(), state: 'S');

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?error=teapot&state=S'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.exchangeRejected);
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

    test('exchange 404: providerUnknown error', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      s.mock.onJson(
        'POST',
        '/api/v1/auth/oidc/authelia/mobile-exchange',
        status: 404,
        body: {
          'error': {'code': 404, 'message': 'unknown OIDC provider'},
        },
      );

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.providerUnknown);
    });

    test('exchange 500: networkError', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      s.mock.onJson(
        'POST',
        '/api/v1/auth/oidc/authelia/mobile-exchange',
        status: 500,
        body: {
          'error': {'code': 500, 'message': 'boom'},
        },
      );

      await s.container
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      final state =
          s.container.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.networkError);
    });

    test('non-ApiError exception from repo: internalError', () async {
      final s = _setup();
      addTearDown(s.container.dispose);
      await seedPending(s.pending, s.now());

      // Override the repository with a throwing stub so completeFlow
      // hits the catch-all `catch (e)` branch (not the `on ApiError`
      // branch). Mirrors a parse-failure or programmer-error inside the
      // repo that didn't get translated to ApiError upstream.
      final throwingContainer = ProviderContainer(overrides: [
        pendingOidcStoreProvider.overrideWithValue(s.pending),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        oidcControllerProvider.overrideWith(() => OIDCController(
              universalLinkHost: 'k8scenter.test',
              now: s.now,
            )),
        oidcRepositoryProvider
            .overrideWithValue(_ThrowingOIDCRepository()),
      ]);
      addTearDown(throwingContainer.dispose);

      await throwingContainer
          .read(oidcControllerProvider.notifier)
          .completeFlow(Uri.parse(
              'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123'));

      final state =
          throwingContainer.read(oidcControllerProvider) as OIDCFlowError;
      expect(state.reason, OIDCFlowErrorReason.internalError);
      expect(state.detail, contains('boom'));
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

  group('abandonLaunching', () {
    test('resets OIDCFlowLaunching back to idle', () async {
      final s = _setup();
      addTearDown(s.container.dispose);

      s.mock.onJson(
        'GET',
        '/api/v1/auth/oidc/authelia/mobile-config',
        body: {
          'data': {
            'authorizationEndpoint': 'https://idp.example.com/oauth2/auth',
            'clientId': 'kubecenter-mobile',
            'scopes': ['openid'],
          },
        },
      );

      // startFlow launches the custom-tab (stub launcher resolves
      // immediately) but no callback Uri arrives, so the controller
      // parks in OIDCFlowLaunching — the dead-end this fix recovers from.
      await s.container
          .read(oidcControllerProvider.notifier)
          .startFlow('authelia');
      expect(
        s.container.read(oidcControllerProvider),
        isA<OIDCFlowLaunching>(),
      );

      s.container.read(oidcControllerProvider.notifier).abandonLaunching();
      expect(s.container.read(oidcControllerProvider), isA<OIDCFlowIdle>());
    });

    test('leaves OIDCFlowExchanging untouched', () async {
      // A never-completing exchange parks the controller in
      // OIDCFlowExchanging so abandonLaunching() can be proven to no-op
      // against a real in-flight exchange (callback already landed).
      final pending = InMemoryPendingOidcStore();
      final container = ProviderContainer(overrides: [
        pendingOidcStoreProvider.overrideWithValue(pending),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        oidcControllerProvider.overrideWith(() => OIDCController(
              universalLinkHost: 'k8scenter.test',
              now: () => DateTime.utc(2026, 5, 16, 12, 0, 0),
            )),
        oidcRepositoryProvider
            .overrideWithValue(_HangingOIDCRepository()),
      ]);
      addTearDown(container.dispose);

      await pending.write(PendingOidc(
        providerID: 'authelia',
        state: 'STATE123',
        codeVerifier: 'VERIFIER456',
        nonce: 'NONCE789',
        createdAtMillis:
            DateTime.utc(2026, 5, 16, 12, 0, 0).millisecondsSinceEpoch,
      ));

      // Fire completeFlow but don't await — the exchange hangs, leaving
      // the controller in OIDCFlowExchanging.
      unawaited(
        container.read(oidcControllerProvider.notifier).completeFlow(
              Uri.parse(
                'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123',
              ),
            ),
      );
      // Let the synchronous portion of completeFlow up to the exchange
      // await run so the state advances to OIDCFlowExchanging.
      await pumpEventQueue();
      expect(
        container.read(oidcControllerProvider),
        isA<OIDCFlowExchanging>(),
      );

      // Guard is strict on OIDCFlowLaunching — exchanging is preserved.
      container.read(oidcControllerProvider.notifier).abandonLaunching();
      expect(
        container.read(oidcControllerProvider),
        isA<OIDCFlowExchanging>(),
      );
    });

    test('no-op during completeFlow read window (_resolving guard)', () async {
      // Findings #10/#11: completeFlow's pre-Exchanging
      // `await pendingStore.read()` leaves state in OIDCFlowLaunching. A
      // resume-timer-driven abandonLaunching() firing in that window would
      // briefly flip the flow to idle out from under an in-flight
      // resolution. The `_resolving` guard must suppress it.
      final launcher = _StubLauncher();
      final hangingReadStore = _HangingReadPendingOidcStore();
      final mock = MockDioAdapter();
      final container = ProviderContainer(overrides: [
        pendingOidcStoreProvider.overrideWithValue(hangingReadStore),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        oidcControllerProvider.overrideWith(() => OIDCController(
              launchUrl: launcher.call,
              universalLinkHost: 'k8scenter.test',
              now: () => DateTime.utc(2026, 5, 16, 12, 0, 0),
            )),
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
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/auth/oidc/authelia/mobile-config',
        body: {
          'data': {
            'authorizationEndpoint': 'https://idp.example.com/oauth2/auth',
            'clientId': 'kubecenter-mobile',
            'scopes': ['openid'],
          },
        },
      );

      // Park the controller in OIDCFlowLaunching (startFlow only writes
      // pending — it never reads — so the hanging-read store doesn't block
      // it).
      await container
          .read(oidcControllerProvider.notifier)
          .startFlow('authelia');
      expect(
        container.read(oidcControllerProvider),
        isA<OIDCFlowLaunching>(),
      );

      // Fire completeFlow but don't await — it suspends on
      // pendingStore.read(), which never completes. State stays
      // OIDCFlowLaunching, but _resolving is now true.
      unawaited(
        container.read(oidcControllerProvider.notifier).completeFlow(
              Uri.parse(
                'https://k8scenter.test/m/auth/callback?code=AUTHCODE&state=STATE123',
              ),
            ),
      );
      await pumpEventQueue();
      expect(
        container.read(oidcControllerProvider),
        isA<OIDCFlowLaunching>(),
        reason: 'still Launching while the read await is suspended',
      );

      // abandonLaunching() must no-op because _resolving is true, even
      // though the state is still Launching.
      container.read(oidcControllerProvider.notifier).abandonLaunching();
      expect(
        container.read(oidcControllerProvider),
        isA<OIDCFlowLaunching>(),
        reason: 'read-window resolution must not be clobbered to idle',
      );
    });
  });
}
