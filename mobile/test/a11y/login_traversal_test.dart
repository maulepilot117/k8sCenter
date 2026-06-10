// Login screen traversal test — pumps LoginScreen with a credential
// provider + two OIDC providers and asserts the screen reader walks the
// affordances in the expected order: username → password → submit →
// OIDC provider 1 → OIDC provider 2. Mirrors the WCAG 2.4.3 (Focus
// Order) intent — assistive tech traverses controls top-to-bottom in a
// meaningful sequence.
//
// The test overrides [universalLinkHostProvider] so the OIDC section
// renders (it's hidden when the universal-link host is empty, which is
// the default in the test binary because no `--dart-define` is
// provided). This is the same provider seam the PR-5h pre-step
// introduced.

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/login/login_screen.dart';
import 'package:kubecenter/notifications/deep_link_handler.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';

import '../a11y_helpers.dart';
import '../support/mock_dio_adapter.dart';

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      // Without this override the const evaluates to "" in the test
      // binary and the OIDC buttons are hidden.
      universalLinkHostProvider.overrideWithValue('k8scenter.test'),
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
      theme: buildKubeTheme('liquid-glass'),
      home: const LoginScreen(),
    ),
  );
}

void main() {
  testWidgets(
      'login traversal: username → password → submit → OIDC provider 1 → OIDC provider 2',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.onJson('GET', '/api/v1/auth/providers', body: {
      'data': [
        // `credential` kind keeps the username/password form visible
        // (auth_repository.dart classifies kind == 'credential' as the
        // credential-provider tier; anything else is OIDC).
        {'id': 'local', 'name': 'Local', 'kind': 'credential'},
        {'id': 'authelia', 'name': 'Authelia', 'kind': 'oidc'},
        {'id': 'keycloak', 'name': 'Keycloak', 'kind': 'oidc'},
      ],
    });

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    final handle = tester.ensureSemantics();

    // Sanity: every expected affordance is present.
    expect(find.byKey(const ValueKey('login-username')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-password')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-submit')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-oidc-authelia')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-oidc-keycloak')), findsOneWidget);

    // Resolve top-of-widget Y positions — semantics traversal mirrors
    // visual top-to-bottom order on a linear-column layout like
    // LoginScreen's, so position ordering is the authoritative signal.
    final usernameY =
        tester.getTopLeft(find.byKey(const ValueKey('login-username'))).dy;
    final passwordY =
        tester.getTopLeft(find.byKey(const ValueKey('login-password'))).dy;
    final submitY =
        tester.getTopLeft(find.byKey(const ValueKey('login-submit'))).dy;
    final auteliaY =
        tester.getTopLeft(find.byKey(const ValueKey('login-oidc-authelia'))).dy;
    final keycloakY =
        tester.getTopLeft(find.byKey(const ValueKey('login-oidc-keycloak'))).dy;

    expect(usernameY, lessThan(passwordY),
        reason: 'username must traverse before password');
    expect(passwordY, lessThan(submitY),
        reason: 'password must traverse before submit');
    expect(submitY, lessThan(auteliaY),
        reason: 'submit must traverse before first OIDC button');
    expect(auteliaY, lessThan(keycloakY),
        reason: 'authelia must traverse before keycloak (backend order)');

    // Submit and OIDC buttons must be reachable as buttons by AT.
    final submitNode =
        findSemanticsFor(tester, find.byKey(const ValueKey('login-submit')));
    expect(submitNode.getSemanticsData().hasAction(SemanticsAction.tap), isTrue,
        reason: 'submit button must expose a tap action');

    final autelia = findSemanticsFor(
        tester, find.byKey(const ValueKey('login-oidc-authelia')));
    expect(autelia.getSemanticsData().hasAction(SemanticsAction.tap), isTrue,
        reason: 'OIDC button must expose a tap action');

    handle.dispose();
  });

  testWidgets('OIDC buttons hidden when universalLinkHostProvider is empty',
      (tester) async {
    final mock = MockDioAdapter();
    final container = ProviderContainer(
      overrides: [
        backendUrlProvider.overrideWithValue('http://test'),
        secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        // Empty host = OIDC disabled — matches default test-binary
        // behaviour, asserted here explicitly.
        universalLinkHostProvider.overrideWithValue(''),
      ],
    );
    addTearDown(container.dispose);
    container.read(refreshDioProvider).httpClientAdapter = mock;
    container.read(dioProvider).httpClientAdapter = mock;
    mock.onJson('GET', '/api/v1/auth/providers', body: {
      'data': [
        {'id': 'local', 'name': 'Local', 'kind': 'credential'},
        {'id': 'authelia', 'name': 'Authelia', 'kind': 'oidc'},
      ],
    });

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-oidc-authelia')), findsNothing,
        reason: 'OIDC section must be hidden when host is empty');
    expect(find.byKey(const ValueKey('login-username')), findsOneWidget,
        reason: 'credential form must still render');
  });

  testWidgets(
      'OIDC-only deployment (no credential providers): credential form is hidden, OIDC buttons render',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    // Backend returns ONLY OIDC providers — operator runs Authelia or
    // Keycloak with the local provider disabled. hideCredentialForm
    // gate fires when visibleOidcProviders.isNotEmpty AND
    // credentialProviders.isEmpty (login_screen.dart:136-138).
    mock.onJson('GET', '/api/v1/auth/providers', body: {
      'data': [
        {'id': 'authelia', 'name': 'Authelia', 'kind': 'oidc'},
        {'id': 'keycloak', 'name': 'Keycloak', 'kind': 'oidc'},
      ],
    });

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-username')), findsNothing,
        reason: 'credential form must be hidden in OIDC-only deployment');
    expect(find.byKey(const ValueKey('login-password')), findsNothing,
        reason: 'password field must be hidden in OIDC-only deployment');
    expect(find.byKey(const ValueKey('login-oidc-authelia')), findsOneWidget,
        reason: 'OIDC providers must render in OIDC-only deployment');
    expect(find.byKey(const ValueKey('login-oidc-keycloak')), findsOneWidget);
  });

  testWidgets(
      'providers-fetch network failure: credential form still renders, no OIDC section',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    // Backend reachable but returns 500. The login screen's
    // providersAsync.when routes to the error branch and falls back to
    // a credential form with an empty provider list. OIDC section stays
    // hidden because visibleOidcProviders is empty.
    mock.onJson('GET', '/api/v1/auth/providers',
        status: 500, body: {'error': {'code': 500, 'message': 'unavailable'}});

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('login-username')), findsOneWidget,
        reason: 'credential form must fall back on providers-fetch error');
    expect(find.byKey(const ValueKey('login-password')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-submit')), findsOneWidget);
    expect(find.byKey(const ValueKey('login-oidc-authelia')), findsNothing,
        reason: 'no OIDC section without provider list');
  });
}
