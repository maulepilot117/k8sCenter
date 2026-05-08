// ScheduledSnapshotWizardController tests. Covers 3-step happy path
// + retention bounds + per-step error routing.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/scheduled_snapshot/scheduled_snapshot_wizard_controller.dart';
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
  return container.listen(scheduledSnapshotWizardProvider(_key), (_, _) {});
}

void main() {
  group('ScheduledSnapshotWizardController', () {
    test('happy path: nightly retention 7 body', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container
          .read(scheduledSnapshotWizardProvider(_key).notifier);
      final body = c.toPreviewBody(const ScheduledSnapshotForm(
        name: 'nightly-data',
        namespace: 'app',
        sourcePVC: 'data',
        volumeSnapshotClassName: 'csi-hostpath',
        schedule: '0 2 * * *',
        retentionCount: 7,
      ));

      expect(body['name'], 'nightly-data');
      expect(body['namespace'], 'app');
      expect(body['sourcePVC'], 'data');
      expect(body['volumeSnapshotClassName'], 'csi-hostpath');
      expect(body['schedule'], '0 2 * * *');
      expect(body['retentionCount'], 7);
    });

    test('cron presets all match the backend strict 5-field cronRegex '
        '(no @-shorthand — backend rejects it)', () {
      // Mirrors backend/internal/wizard/container.go:20 cronRegex.
      final cronRegex = RegExp(r'^(\S+\s+){4}\S+$');
      for (final preset in kCronPresets) {
        expect(
          cronRegex.hasMatch(preset.value),
          isTrue,
          reason:
              'preset "${preset.label}" → "${preset.value}" must match the '
              'backend 5-field cron regex; @-shorthand is rejected.',
        );
      }
    });

    test('three-step navigation: clean step 0 → step 1 → step 2 (Review)',
        () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(scheduledSnapshotWizardProvider(_key).notifier);

      // Step 0 → Step 1
      notifier.updateForm((f) => f.copyWith(
            name: 'x',
            namespace: 'y',
            sourcePVC: 'data',
            schedule: '0 2 * * *',
          ));
      await notifier.next();
      expect(
          container
              .read(scheduledSnapshotWizardProvider(_key))
              .currentStep,
          1);

      // Step 1 → preview (last form step). Local validation passes;
      // status transitions to previewing (HTTP not stubbed, so the
      // request will fail and land in `failed` — but the step
      // advancement itself is what we're verifying).
      notifier.updateForm((f) => f.copyWith(
            volumeSnapshotClassName: 'csi-hostpath',
            retentionCount: 7,
          ));
      await notifier.next();
      final state = container.read(scheduledSnapshotWizardProvider(_key));
      // Step 1 was the last form step (index 1, with Review at 2).
      // After local validation passes, the controller transitions to
      // previewing. Reaching `previewing` from step 1 confirms the
      // step 1 → review pipeline is wired up.
      expect(
        [WizardStatus.previewing, WizardStatus.failed],
        contains(state.status),
        reason: 'step 1 → preview transition fired',
      );
    });

    test('retention 0 fails step-1 validation', () async {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final notifier =
          container.read(scheduledSnapshotWizardProvider(_key).notifier);
      // Fill step 0 cleanly so we land on step 1.
      notifier.updateForm((f) => f.copyWith(
            name: 'x',
            namespace: 'y',
            sourcePVC: 'data',
            schedule: '0 2 * * *',
          ));
      await notifier.next();
      expect(
          container.read(scheduledSnapshotWizardProvider(_key)).currentStep,
          1);
      // Now step 1 with retention 0 should fail.
      notifier.updateForm((f) => f.copyWith(
            volumeSnapshotClassName: 'csi-hostpath',
            retentionCount: 0,
          ));
      await notifier.next();
      final state = container.read(scheduledSnapshotWizardProvider(_key));
      expect(state.currentStep, 1);
      expect(state.stepErrors[1]?['retentionCount'], isNotNull);
    });

    test('errorRouter splits fields between steps 0 and 1', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c =
          container.read(scheduledSnapshotWizardProvider(_key).notifier);
      expect(c.errorRouter('name'), 0);
      expect(c.errorRouter('namespace'), 0);
      expect(c.errorRouter('sourcePVC'), 0);
      expect(c.errorRouter('schedule'), 0);
      expect(c.errorRouter('volumeSnapshotClassName'), 1);
      expect(c.errorRouter('retentionCount'), 1);
      expect(c.errorRouter('unknown'), null);
    });
  });
}
