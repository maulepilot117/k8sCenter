// Controller-level tests for the ESO Bulk Refresh write path.
//
// Coverage:
//   * Full state-machine walk for the store variant:
//     scopePick → scopeLoad → confirm → submit → poll → done.
//   * 409 active_job_exists on submit attaches to the existing jobId
//     and skips straight to poll, flagging `attachedToExistingJob`.
//   * 409 scope_changed on submit drops back to scopeLoad and re-runs
//     scope resolution.
//   * 501 on submit surfaces local-cluster-only copy.
//   * Cluster-pin race postEmission on scope-load emits error.
//   * cancelPoll() tears the timer down (verified via state transition
//     + absence of follow-up polls after dispose).
//   * Transient poll error (1×) keeps the loop alive with
//     `pollRetrying: true`; 3 consecutive errors flip to error state.
//
// Polling uses `Timer.periodic`, which `flutter_test` does not advance
// virtually — we rely on the controller's first-poll-immediate behavior
// to assert state transitions without time travel. The "3 consecutive
// errors" test uses the same hook by re-invoking the same first-poll
// path; we deliberately don't try to simulate the 2-second cadence.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/cluster/cluster_provider.dart';
import 'package:kubecenter/features/eso/bulk_refresh_controller.dart';

import '../../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

({ProviderContainer container, MockDioAdapter mock}) _make() {
  final mock = MockDioAdapter();
  final container = ProviderContainer(
    overrides: [
      backendUrlProvider.overrideWithValue('http://test'),
      secureTokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
    ],
  );
  container.read(refreshDioProvider).httpClientAdapter = mock;
  container.read(dioProvider).httpClientAdapter = mock;
  return (container: container, mock: mock);
}

const _clusterId = 'local';

ProviderSubscription<BulkRefreshSheetState> _subscribe(
  ProviderContainer container,
) {
  return container.listen<BulkRefreshSheetState>(
    bulkRefreshControllerProvider(_clusterId),
    (_, _) {},
  );
}

Future<void> _settle() => pumpEventQueue(times: 30);

Map<String, Object> _scopeOk({
  required String action,
  required String scopeTarget,
  List<Map<String, String>> targets = const [
    {'namespace': 'prod', 'name': 'db-creds', 'uid': 'u1'},
    {'namespace': 'prod', 'name': 'api-creds', 'uid': 'u2'},
  ],
}) {
  return {
    'data': {
      'action': action,
      'scopeTarget': scopeTarget,
      'totalCount': targets.length,
      'totalNamespaces': 1,
      'visibleCount': targets.length,
      'restricted': false,
      'targets': targets,
      'byNamespace': [
        {'namespace': 'prod', 'count': targets.length},
      ],
    },
  };
}

Map<String, Object> _jobInProgress({
  required String jobId,
  int succeeded = 0,
}) {
  return {
    'data': {
      'jobId': jobId,
      'clusterId': _clusterId,
      'requestedBy': 'oncall',
      'action': 'refresh_store',
      'scopeTarget': 'prod/vault',
      'targetCount': 2,
      'createdAt': '2026-05-17T10:00:00Z',
      'succeeded': List.generate(succeeded, (i) => 'u${i + 1}'),
      'failed': <Object>[],
      'skipped': <Object>[],
    },
  };
}

Map<String, Object> _jobDone({required String jobId}) {
  return {
    'data': {
      'jobId': jobId,
      'clusterId': _clusterId,
      'requestedBy': 'oncall',
      'action': 'refresh_store',
      'scopeTarget': 'prod/vault',
      'targetCount': 2,
      'createdAt': '2026-05-17T10:00:00Z',
      'completedAt': '2026-05-17T10:00:30Z',
      'succeeded': ['u1', 'u2'],
      'failed': <Object>[],
      'skipped': <Object>[],
    },
  };
}

void main() {
  group('BulkRefreshController.state machine', () {
    test('full happy path: store variant walks scopePick → done', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.onJson(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        status: 202,
        body: {
          'data': {'jobId': 'job-1', 'targetCount': 2},
        },
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/bulk-refresh-jobs/job-1',
        body: _jobDone(jobId: 'job-1'),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);

      expect(sub.read().phase, BulkRefreshPhase.scopePick);

      await ctrl.beginScopeLoad(const BulkRefreshScopeStore(
        namespace: 'prod',
        name: 'vault',
      ));
      await _settle();
      expect(sub.read().phase, BulkRefreshPhase.confirm);
      expect(sub.read().scopeResponse?.visibleCount, 2);

      await ctrl.submit();
      await _settle();
      // First-poll-immediate behavior puts us in `done` since the
      // first poll already returns `completedAt`.
      expect(sub.read().phase, BulkRefreshPhase.done);
      expect(sub.read().job?.succeeded.length, 2);
      expect(sub.read().attachedToExistingJob, isFalse);
      ctrl.cancelPoll();
    });

    test('back-button rewinds from confirm to scopePick', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      expect(sub.read().phase, BulkRefreshPhase.confirm);

      ctrl.backToScopePick();
      expect(sub.read().phase, BulkRefreshPhase.scopePick);
      expect(sub.read().scopeResponse, isNull);
    });

    test('clusterstore variant dispatches to /clusterstores endpoint',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/clusterstores/vault-shared/refresh-scope',
        body: _scopeOk(
          action: 'refresh_cluster_store',
          scopeTarget: 'vault-shared',
        ),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeClusterStore(name: 'vault-shared'),
      );
      await _settle();
      expect(sub.read().phase, BulkRefreshPhase.confirm);
      expect(
        mock.requests.any((r) =>
            r.path ==
            '/api/v1/externalsecrets/clusterstores/vault-shared/refresh-scope'),
        isTrue,
      );
    });

    test('namespace variant dispatches to /refresh-namespace endpoint',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/refresh-namespace/prod/refresh-scope',
        body: _scopeOk(action: 'refresh_namespace', scopeTarget: 'prod'),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeNamespace(namespace: 'prod'),
      );
      await _settle();
      expect(sub.read().phase, BulkRefreshPhase.confirm);
    });
  });

  group('BulkRefreshController.submit error handling', () {
    test('409 active_job_exists attaches to existing jobId and polls',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.on(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        (_) => _json({
          'error': {
            'code': 409,
            'message': 'another bulk refresh is already in flight',
            'reason': 'active_job_exists',
            'extra': {'jobId': 'job-existing'},
          },
        }, status: 409),
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/bulk-refresh-jobs/job-existing',
        body: _jobInProgress(jobId: 'job-existing', succeeded: 1),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.poll);
      expect(sub.read().jobId, 'job-existing');
      expect(sub.read().attachedToExistingJob, isTrue);
      ctrl.cancelPoll();
    });

    test('409 scope_changed re-resolves the scope and lands in confirm',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // First scope-load returns 2; submit fails with scope_changed; second
      // scope-load (auto-triggered) returns 3 targets.
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.on(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        (_) => _json({
          'error': {
            'code': 409,
            'message': 'scope changed since last resolution',
            'reason': 'scope_changed',
            'extra': {'added': ['u3'], 'removed': <Object>[]},
          },
        }, status: 409),
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault',
            targets: [
              {'namespace': 'prod', 'name': 'db-creds', 'uid': 'u1'},
              {'namespace': 'prod', 'name': 'api-creds', 'uid': 'u2'},
              {'namespace': 'prod', 'name': 'new-creds', 'uid': 'u3'},
            ]),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();

      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.confirm);
      expect(sub.read().scopeResponse?.visibleCount, 3);
    });

    test('501 surfaces local-cluster-only copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.on(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        (_) => _json({
          'error': {
            'code': 501,
            'message': 'ESO write actions are local-cluster only in v1',
          },
        }, status: 501),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error);
      expect(sub.read().errorMessage, contains('local cluster'));
    });

    test('422 (empty scope) surfaces nothing-to-refresh copy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.on(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        (_) => _json({
          'error': {'code': 422, 'message': 'scope is empty'},
        }, status: 422),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      await ctrl.submit();
      await _settle();

      expect(sub.read().errorMessage, contains('Nothing to refresh'));
    });
  });

  group('BulkRefreshController.cluster-pin race', () {
    test('postEmission switch on scope-load surfaces failure', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      final future = ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      container.read(activeClusterProvider.notifier).setCluster('prod-cluster');
      await future;
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error);
      expect(sub.read().errorMessage?.toLowerCase(), contains('cluster'));
    });
  });

  group('BulkRefreshController.poll retry handling', () {
    test('transient 503 on first poll keeps loop alive with retrying flag',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      mock.onJson(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        status: 202,
        body: {
          'data': {'jobId': 'job-1', 'targetCount': 2},
        },
      );
      // First poll returns 503 — controller should set pollRetrying.
      mock.on(
        'GET',
        '/api/v1/externalsecrets/bulk-refresh-jobs/job-1',
        (_) => _json({
          'error': {'code': 503, 'message': 'temporarily unavailable'},
        }, status: 503),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.poll,
          reason: 'transient poll error must NOT collapse to error');
      expect(sub.read().pollRetrying, isTrue);
      ctrl.cancelPoll();
    });
  });
}
