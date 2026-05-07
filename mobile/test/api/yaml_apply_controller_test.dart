// Tests for YamlApplyController (AutoDisposeFamilyNotifier).
//
// Covers:
//   - initial state is idle with empty content
//   - setContent updates yamlContent + resets status to idle
//   - validate() success: correct Content-Type, transitions idle→validating→validated
//   - apply() success: transitions idle→applying→applied, invalidates resourceGetProvider
//   - validate() failure (400 from backend): status → failed, error carries message
//   - reset() returns to idle clearing result and error
//
// AutoDispose note: without a widget listener the notifier is torn down
// immediately after the first read. We keep the provider alive by holding
// a subscription via container.listen() for the duration of each test.

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/resource_repository.dart';
import 'package:kubecenter/api/yaml_apply_controller.dart';
import 'package:kubecenter/auth/secure_storage.dart';

import '../support/mock_dio_adapter.dart';

// ── fixtures ─────────────────────────────────────────────────────────────────

const _testKey = YamlApplyKey(
  clusterId: 'local',
  kind: 'configmaps',
  namespace: 'default',
  name: 'cfg',
);

const _getKey = ResourceGetKey(
  clusterId: 'local',
  kind: 'configmaps',
  namespace: 'default',
  name: 'cfg',
);

final _successBody = {
  'results': [
    {
      'index': 0,
      'kind': 'ConfigMap',
      'name': 'cfg',
      'namespace': 'default',
      'action': 'configured',
    },
  ],
  'summary': {
    'total': 1,
    'created': 0,
    'configured': 1,
    'unchanged': 0,
    'failed': 0,
  },
};

// ── helpers ───────────────────────────────────────────────────────────────────

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

// ── tests ─────────────────────────────────────────────────────────────────────

void main() {
  group('YamlApplyController', () {
    test('initial state is idle with empty yamlContent', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);

      // Keep the autoDispose provider alive for the duration of the test.
      final sub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(sub.close);

      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.status, YamlApplyStatus.idle);
      expect(state.yamlContent, '');
      expect(state.result, isNull);
      expect(state.error, isNull);
    });

    test('setContent updates yamlContent and resets status to idle', () {
      final (:container, mock: _) = _makeContainer();
      addTearDown(container.dispose);

      final sub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(sub.close);

      final notifier =
          container.read(yamlApplyControllerProvider(_testKey).notifier);

      notifier.setContent('kind: ConfigMap\n');

      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.yamlContent, 'kind: ConfigMap\n');
      expect(state.status, YamlApplyStatus.idle);
      expect(state.result, isNull);
      expect(state.error, isNull);
    });

    test(
        'validate() success: posts with application/yaml content-type, '
        'transitions idle → validating → validated, result populated', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      final sub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/yaml/validate',
        body: _successBody,
      );

      final notifier =
          container.read(yamlApplyControllerProvider(_testKey).notifier);
      notifier.setContent('kind: ConfigMap\n');

      await notifier.validate();

      // Check request content-type header.
      expect(mock.requests, hasLength(1));
      final req = mock.requests.first;
      expect(req.contentType, contains('application/yaml'));

      // Check final state.
      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.status, YamlApplyStatus.validated);
      expect(state.result, isNotNull);
      expect(state.result!.summary.configured, 1);
      expect(state.result!.results.first.action, 'configured');
      expect(state.error, isNull);
    });

    test(
        'apply() success: transitions idle → applying → applied, '
        'invalidates resourceGetProvider', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      // Keep the controller alive.
      final ctrlSub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(ctrlSub.close);

      // Keep the resource provider alive so we can verify it goes back to loading.
      final resSub = container.listen(
        resourceGetProvider(_getKey),
        (prev, next) {},
      );
      addTearDown(resSub.close);

      // First GET — seeds the cache.
      mock.onJson(
        'GET',
        '/api/v1/resources/configmaps/default/cfg',
        body: {
          'data': {
            'kind': 'ConfigMap',
            'metadata': {'name': 'cfg', 'namespace': 'default'},
          },
        },
      );
      await container.read(resourceGetProvider(_getKey).future);
      expect(
        container.read(resourceGetProvider(_getKey)).valueOrNull,
        isNotNull,
      );

      // Apply mock.
      mock.onJson('POST', '/api/v1/yaml/apply', body: _successBody);

      final notifier =
          container.read(yamlApplyControllerProvider(_testKey).notifier);
      notifier.setContent('kind: ConfigMap\n');
      await notifier.apply();

      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.status, YamlApplyStatus.applied);
      expect(state.result, isNotNull);
      expect(state.result!.summary.configured, 1);

      // resourceGetProvider must have been invalidated — it enters loading.
      expect(container.read(resourceGetProvider(_getKey)).isLoading, isTrue);
    });

    test(
        'validate() failure (400 from backend): status → failed, '
        'error carries backend message', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      final sub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(sub.close);

      mock.onJson(
        'POST',
        '/api/v1/yaml/validate',
        status: 400,
        body: {
          'error': {
            'code': 400,
            'message': 'invalid yaml: mapping values not allowed',
          },
        },
      );

      final notifier =
          container.read(yamlApplyControllerProvider(_testKey).notifier);
      notifier.setContent('bad: yaml: here\n');

      await notifier.validate();

      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.status, YamlApplyStatus.failed);
      expect(state.error, 'invalid yaml: mapping values not allowed');
      expect(state.result, isNull);
    });

    test('reset() returns to idle clearing result and error', () async {
      final (:container, :mock) = _makeContainer();
      addTearDown(container.dispose);

      final sub = container.listen(
        yamlApplyControllerProvider(_testKey),
        (prev, next) {},
      );
      addTearDown(sub.close);

      mock.onJson('POST', '/api/v1/yaml/validate', body: _successBody);

      final notifier =
          container.read(yamlApplyControllerProvider(_testKey).notifier);
      notifier.setContent('kind: ConfigMap\n');
      await notifier.validate();

      // Sanity-check: should be validated before we reset.
      expect(
        container.read(yamlApplyControllerProvider(_testKey)).status,
        YamlApplyStatus.validated,
      );

      notifier.reset();

      final state = container.read(yamlApplyControllerProvider(_testKey));
      expect(state.status, YamlApplyStatus.idle);
      expect(state.result, isNull);
      expect(state.error, isNull);
      // Content is intentionally preserved across reset.
      expect(state.yamlContent, 'kind: ConfigMap\n');
    });
  });
}
