// Tests for CronJobWizardController.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/wizards/types/cronjob/cronjob_wizard_controller.dart';
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
  return container.listen(cronJobWizardProvider(_key), (_, _) {});
}

void main() {
  group('CronJobWizardController', () {
    test(
        'default form has restartPolicy=OnFailure, concurrency=Allow, '
        'suspend=false', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final state = container.read(cronJobWizardProvider(_key));
      expect(state.form.restartPolicy, 'OnFailure');
      expect(state.form.concurrencyPolicy, 'Allow');
      expect(state.form.suspend, false);
    });

    test(
        'common-pattern picker exposes all six canonical patterns from the '
        'web frontend', () {
      // Sanity guard so a future refactor doesn't silently drop a chip
      // and leave operators with a different set on mobile vs web.
      expect(kCronCommonPatterns, [
        '@hourly',
        '@daily',
        '@weekly',
        '@monthly',
        '0 */6 * * *',
        '*/15 * * * *',
      ]);
    });

    test('toPreviewBody includes suspend only when true', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      final c = container.read(cronJobWizardProvider(_key).notifier);
      const off = CronJobForm(
        name: 'cj',
        namespace: 'default',
        schedule: '0 2 * * *',
        image: 'busybox',
      );
      const on = CronJobForm(
        name: 'cj',
        namespace: 'default',
        schedule: '0 2 * * *',
        image: 'busybox',
        suspend: true,
      );
      expect(c.toPreviewBody(off).containsKey('suspend'), false);
      expect(c.toPreviewBody(on)['suspend'], true);
    });

    test('happy path with `0 2 * * *` schedule previews', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/cronjob/preview',
        body: {
          'data': {
            'yaml':
                'apiVersion: batch/v1\nkind: CronJob\nspec:\n  jobTemplate:\n    spec:\n      template:\n        spec:\n          containers:\n          - image: busybox\n',
          },
        },
      );

      final notifier =
          container.read(cronJobWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'nightly',
            namespace: 'default',
            schedule: '0 2 * * *',
            image: 'busybox:latest',
            concurrencyPolicy: 'Forbid',
          ));
      await notifier.next();

      final state = container.read(cronJobWizardProvider(_key));
      expect(state.status, WizardStatus.reviewing);
      expect(state.previewYaml, contains('jobTemplate'));
    });

    test('422 with field=schedule rewinds to Configure', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);
      final sub = _keepAlive(container);
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/wizards/cronjob/preview',
        status: 422,
        body: {
          'error': {
            'code': 422,
            'message': 'validation failed',
            'detail': json.encode([
              {
                'field': 'schedule',
                'message': 'must be a valid 5-field cron expression'
              },
            ]),
          },
        },
      );

      final notifier =
          container.read(cronJobWizardProvider(_key).notifier);
      notifier.updateForm((f) => f.copyWith(
            name: 'cj',
            namespace: 'default',
            schedule: '* * * *', // 4 fields, invalid
            image: 'busybox',
          ));
      await notifier.next();

      final state = container.read(cronJobWizardProvider(_key));
      expect(state.currentStep, 0);
      expect(state.stepErrors[0]?['schedule'], contains('5-field'));
    });
  });
}
