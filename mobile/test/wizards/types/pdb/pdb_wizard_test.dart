// Tests for PdbWizardController. Covers minAvailable=2,
// maxUnavailable=50%, and confirmation that exactly one of the two
// fields lands on the wire (mutual exclusion enforced at form layer).

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/pdb/pdb_wizard_controller.dart';
import 'package:kubecenter/wizards/widgets/key_value_table.dart';
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
  return container.listen(pdbWizardProvider(_key), (_, _) {});
}

void main() {
  group('PdbWizardController', () {
    test('minAvailable=2 emits only minAvailable on the wire', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pdbWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const PdbForm(
        name: 'web-pdb',
        namespace: 'default',
        selector: [KeyValuePair(key: 'app', value: 'web')],
        policy: PdbPolicy.minAvailable,
        value: '2',
      ));
      expect(body['minAvailable'], '2');
      expect(body.containsKey('maxUnavailable'), false);
      expect(body['selector'], {'app': 'web'});
    });

    test('maxUnavailable=50% emits only maxUnavailable on the wire', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pdbWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const PdbForm(
        name: 'web-pdb',
        namespace: 'default',
        selector: [KeyValuePair(key: 'app', value: 'web')],
        policy: PdbPolicy.maxUnavailable,
        value: '50%',
      ));
      expect(body['maxUnavailable'], '50%');
      expect(body.containsKey('minAvailable'), false);
    });

    test('mutual exclusion: switching policy changes which key emits', () {
      // Form layer guarantees only one of minAvailable / maxUnavailable
      // is on the wire because `toPreviewBody` writes one based on the
      // active radio. The backend's "both set is rejected" rule is
      // unreachable from the wizard.
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(pdbWizardProvider(_key).notifier);
      final initial = const PdbForm(
        name: 'p',
        namespace: 'default',
        selector: [KeyValuePair(key: 'app', value: 'web')],
        policy: PdbPolicy.minAvailable,
        value: '2',
      );
      expect(c.toPreviewBody(initial).containsKey('minAvailable'), true);
      expect(c.toPreviewBody(initial).containsKey('maxUnavailable'), false);

      final flipped = initial.copyWith(policy: PdbPolicy.maxUnavailable);
      expect(c.toPreviewBody(flipped).containsKey('minAvailable'), false);
      expect(c.toPreviewBody(flipped).containsKey('maxUnavailable'), true);
    });

    test('validateLocally requires selector + value', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier = container.read(pdbWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(name: 'p', namespace: 'default'));
      await notifier.next();

      final state = container.read(pdbWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['selector'], isNotNull);
      expect(state.stepErrors[0]?['minAvailable'], isNotNull);
    });
  });
}
