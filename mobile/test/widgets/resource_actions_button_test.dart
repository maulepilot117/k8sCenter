// Verifies ResourceActionsButton's contract:
//   - hidden for kinds with no actions
//   - tapping the bolt opens the action sheet
//   - restart flow posts to the correct endpoint
//   - backend 403 surfaces the error message in a snackbar
//   - cluster-switch abort: if the active cluster changes mid-flow,
//     the action is aborted with an explanatory snackbar

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
import 'package:kubecenter/auth/user.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/widgets/resource_actions_button.dart';

import '../support/mock_dio_adapter.dart';

// ── fixtures ─────────────────────────────────────────────────────────────────

const _deploymentResource = <String, dynamic>{
  'kind': 'Deployment',
  'metadata': {'name': 'app', 'namespace': 'default'},
  'spec': {'replicas': 3},
};

// ── helpers ───────────────────────────────────────────────────────────────────

class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

ResponseBody _jsonBody(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

/// Build a ProviderContainer with auth + dio wired up for tests.
({ProviderContainer container, MockDioAdapter mock}) _makeContainer({
  bool admin = true,
  String initialCluster = 'local',
}) {
  final mock = MockDioAdapter();
  final user = UserInfo(
    id: 'u1',
    username: admin ? 'admin' : 'viewer',
    provider: 'local',
    roles: admin ? const ['admin'] : const ['viewer'],
  );

  // Give admin full write access so actions are visible.
  final rbacJson = admin
      ? {
          'namespaces': {
            'default': {
              'deployments': ['*'],
            },
          },
        }
      : <String, dynamic>{};

  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      authRepositoryProvider.overrideWith(
        () => _FakeAuth(
          AuthAuthenticated(
            user: user,
            rbac: RBACSummary.fromJson(rbacJson),
          ),
        ),
      ),
      // Pin the initial cluster.
      activeClusterProvider.overrideWith(
        () {
          final ctrl = ActiveClusterController();
          // Let build() run; it starts at defaultClusterId ('local'), which is
          // fine because we switch via setCluster() in the test body.
          return ctrl;
        },
      ),
    ],
  );
  container.read(dioProvider).httpClientAdapter = mock;
  container.read(refreshDioProvider).httpClientAdapter = mock;

  // Force the cluster to the requested initial value.
  if (initialCluster != defaultClusterId) {
    container.read(activeClusterProvider.notifier).setCluster(initialCluster);
  }

  return (container: container, mock: mock);
}

/// Minimal host that places ResourceActionsButton in the app bar.
Widget _harness(
  ProviderContainer container, {
  String kind = 'deployments',
  Map<String, dynamic> resource = _deploymentResource,
}) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        appBar: AppBar(
          actions: [
            ResourceActionsButton(
              kind: kind,
              namespace: 'default',
              name: 'app',
              resource: resource,
            ),
          ],
        ),
        body: const SizedBox.expand(),
      ),
    ),
  );
}

// ── tests ─────────────────────────────────────────────────────────────────────

void main() {
  testWidgets('bolt icon is hidden for kinds with no actions', (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container, kind: 'mysteries'));
    await tester.pump();

    expect(find.byIcon(Icons.bolt_outlined), findsNothing);
  });

  testWidgets('bolt icon is visible for deployments', (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    expect(find.byIcon(Icons.bolt_outlined), findsOneWidget);
  });

  testWidgets('tapping the bolt opens the action sheet with "Actions" header',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    await tester.tap(find.byIcon(Icons.bolt_outlined));
    await tester.pumpAndSettle();

    expect(find.text('Actions'), findsOneWidget);
  });

  testWidgets('restart flow POSTs to /restart and shows success snackbar',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.on(
      'POST',
      '/api/v1/resources/deployments/default/app/restart',
      (_) => _jsonBody({'data': <String, dynamic>{}}),
    );

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    // Open the action sheet.
    await tester.tap(find.byIcon(Icons.bolt_outlined));
    await tester.pumpAndSettle();

    // Tap "Restart" in the action list.
    await tester.tap(find.text('Restart'));
    await tester.pumpAndSettle();

    // Confirm sheet appears — tap the filled "Restart" button.
    final confirmBtn = find.widgetWithText(FilledButton, 'Restart');
    expect(confirmBtn, findsOneWidget);
    await tester.tap(confirmBtn);
    await tester.pumpAndSettle();

    // Verify the request was captured.
    expect(
      mock.requests.any((r) =>
          r.method == 'POST' &&
          r.path.endsWith('/deployments/default/app/restart')),
      isTrue,
    );

    // Success snackbar.
    expect(find.text('Rolling restart initiated'), findsOneWidget);
  });

  testWidgets('backend 403 surfaces error.message in a snackbar',
      (tester) async {
    final (:container, :mock) = _makeContainer();
    addTearDown(container.dispose);

    mock.on(
      'POST',
      '/api/v1/resources/deployments/default/app/restart',
      (_) => _jsonBody(
        {'error': {'code': 403, 'message': 'forbidden'}},
        status: 403,
      ),
    );

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    await tester.tap(find.byIcon(Icons.bolt_outlined));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Restart'));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Restart'));
    await tester.pumpAndSettle();

    expect(find.text('forbidden'), findsOneWidget);
    // No request should have produced the success message.
    expect(find.text('Rolling restart initiated'), findsNothing);
  });

  testWidgets(
      'cluster-switch abort: changing active cluster between open and execute '
      'shows abort snackbar without making a request', (tester) async {
    final (:container, :mock) = _makeContainer(initialCluster: 'local');
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pump();

    // Open action sheet — cluster is pinned to 'local' at this point.
    await tester.tap(find.byIcon(Icons.bolt_outlined));
    await tester.pumpAndSettle();

    // Simulate an operator switching clusters mid-flow before they confirm.
    container.read(activeClusterProvider.notifier).setCluster('prod');

    // Continue through the confirm sheet.
    await tester.tap(find.text('Restart'));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Restart'));
    await tester.pumpAndSettle();

    // Abort snackbar — no actual request should have been made.
    expect(
      find.textContaining('Cluster changed during this action'),
      findsOneWidget,
    );
    expect(
      mock.requests.any((r) => r.path.endsWith('/restart')),
      isFalse,
    );
  });
}
