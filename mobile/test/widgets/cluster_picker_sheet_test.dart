// Verifies the cluster picker sheet:
//   - non-admin: no "Add cluster" entry
//   - admin: "Add cluster" entry rendered + tap shows "coming soon" SnackBar
//   - selecting a cluster updates activeClusterProvider + pops sheet

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
import 'package:kubecenter/widgets/cluster_picker_sheet.dart';

import '../support/mock_dio_adapter.dart';

class _FakeAuth extends AuthRepository {
  _FakeAuth(this._initial);
  final AuthState _initial;
  @override
  AuthState build() => _initial;
}

({ProviderContainer container, MockDioAdapter mock}) _make({
  required bool admin,
}) {
  final mock = MockDioAdapter();
  final user = UserInfo(
    id: 'u1',
    username: admin ? 'admin' : 'viewer',
    provider: 'local',
    roles: admin ? const ['admin'] : const ['viewer'],
  );
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      authRepositoryProvider.overrideWith(
        () => _FakeAuth(
          AuthAuthenticated(user: user, rbac: const RBACSummary()),
        ),
      ),
    ],
  );
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;

  mock.onJson(
    'GET',
    '/api/v1/clusters',
    body: {
      'data': [
        {'id': 'local', 'name': 'local', 'isLocal': true, 'status': 'ready'},
        {'id': 'prod', 'name': 'prod', 'isLocal': false, 'status': 'ready'},
      ],
    },
  );
  return (container: container, mock: mock);
}

Widget _harness(ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: buildKubeTheme('liquid-glass'),
      home: Consumer(
        builder: (context, ref, _) => Scaffold(
          body: Center(
            child: FilledButton(
              onPressed: () => ClusterPickerSheet.show(context, ref),
              child: const Text('open'),
            ),
          ),
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('non-admin sees no Add cluster entry', (tester) async {
    final (:container, :mock) = _make(admin: false);
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('cluster-picker-add')), findsNothing);
    expect(find.byKey(const ValueKey('cluster-radio-local')), findsOneWidget);
    expect(find.byKey(const ValueKey('cluster-radio-prod')), findsOneWidget);
  });

  testWidgets('admin sees Add cluster + tap shows SnackBar', (tester) async {
    final (:container, :mock) = _make(admin: true);
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('cluster-picker-add')), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('cluster-picker-add')));
    await tester.pumpAndSettle();

    expect(
      find.text('Cluster registration ships in a follow-up PR.'),
      findsOneWidget,
    );
  });

  testWidgets('selecting cluster updates active provider + pops', (tester) async {
    final (:container, :mock) = _make(admin: false);
    addTearDown(container.dispose);

    await tester.pumpWidget(_harness(container));
    await tester.pumpAndSettle();
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('cluster-radio-prod')));
    await tester.pumpAndSettle();

    expect(find.byType(ClusterPickerSheet), findsNothing);
    expect(container.read(activeClusterProvider), 'prod');
  });
}
