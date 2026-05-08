// Tests for RoleBindingWizardController. Covers happy path body shape,
// ServiceAccount-namespace required, and User namespace omitted.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/rolebinding/rolebinding_wizard_controller.dart';
import 'package:kubecenter/wizards/wizard_controller.dart';

import '../../../support/mock_dio_adapter.dart';

const _key = WizardKey(clusterId: 'local');

({ProviderContainer container, MockDioAdapter mock}) _makeContainer() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(dioProvider).httpClientAdapter = mock;
  container.read(refreshDioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

ProviderSubscription _keepAlive(ProviderContainer container) {
  return container.listen(roleBindingWizardProvider(_key), (_, _) {});
}

void main() {
  group('RoleBindingWizardController', () {
    test('happy path: SA subject carries namespace', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(roleBindingWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const RoleBindingForm(
        name: 'view-binding',
        namespace: 'app',
        roleKind: 'Role',
        roleName: 'view',
        subjects: [
          RoleBindingSubject(
            kind: 'ServiceAccount',
            name: 'test-sa',
            namespace: 'default',
          ),
        ],
      ));

      expect(body['clusterScope'], false);
      expect(body['roleRef'], {'kind': 'Role', 'name': 'view'});
      final subjs = body['subjects'] as List;
      expect(subjs.length, 1);
      final s = subjs.first as Map<String, dynamic>;
      expect(s['kind'], 'ServiceAccount');
      expect(s['namespace'], 'default');
    });

    test('User subject emits empty namespace (server ignores it)', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(roleBindingWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const RoleBindingForm(
        name: 'view-binding',
        namespace: 'app',
        roleKind: 'Role',
        roleName: 'view',
        subjects: [
          RoleBindingSubject(
            kind: 'User',
            name: 'alice@example.com',
            namespace: 'should-be-stripped',
          ),
        ],
      ));

      final s = (body['subjects'] as List).first as Map<String, dynamic>;
      expect(s['kind'], 'User');
      expect(s['namespace'], '');
    });

    test('SA without namespace fails validateLocally', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(roleBindingWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'rb',
            namespace: 'app',
            roleKind: 'Role',
            roleName: 'view',
            subjects: const [
              RoleBindingSubject(kind: 'ServiceAccount', name: 'sa'),
            ],
          ));
      await notifier.next();

      final state = container.read(roleBindingWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['subjects[0].namespace'], isNotNull);
    });

    test('errorRouter routes roleRef + subjects to step 0', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(roleBindingWizardProvider(_key).notifier);
      expect(c.errorRouter('roleRef.name'), 0);
      expect(c.errorRouter('subjects[0].kind'), 0);
      expect(c.errorRouter('totally-unknown'), isNull);
    });
  });
}
