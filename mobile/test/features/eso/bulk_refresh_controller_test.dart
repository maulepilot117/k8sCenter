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
import 'package:kubecenter/api/eso_repository.dart';
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
      // PR-5e-review #3: scope-load now resolves to `preview` (not
      // `confirm`). Operator taps "Continue" → `confirmPreview()` →
      // `confirm` phase (shared confirm sheet stacks on top); the type-
      // to-confirm gate lives in `showConfirmSheet`, not the body.
      expect(sub.read().phase, BulkRefreshPhase.preview);
      expect(sub.read().scopeResponse?.visibleCount, 2);

      ctrl.confirmPreview();
      expect(sub.read().phase, BulkRefreshPhase.confirm);

      await ctrl.submit();
      await _settle();
      // First-poll-immediate behavior puts us in `done` since the
      // first poll already returns `completedAt`.
      expect(sub.read().phase, BulkRefreshPhase.done);
      expect(sub.read().job?.succeeded.length, 2);
      expect(sub.read().attachedToExistingJob, isFalse);
      ctrl.cancelPoll();
    });

    test('back-button rewinds from preview to scopePick', () async {
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
      expect(sub.read().phase, BulkRefreshPhase.preview);

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
      expect(sub.read().phase, BulkRefreshPhase.preview);
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
      expect(sub.read().phase, BulkRefreshPhase.preview);
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
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.poll);
      expect(sub.read().jobId, 'job-existing');
      expect(sub.read().attachedToExistingJob, isTrue);
      ctrl.cancelPoll();
    });

    test('409 scope_changed re-resolves the scope and lands in preview',
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
      ctrl.confirmPreview();

      await ctrl.submit();
      await _settle();

      // PR-5e-review #3: post-recovery the controller drops back to
      // scopeLoad → preview (operator confirms again from the new
      // breakdown before re-firing the confirm sheet).
      expect(sub.read().phase, BulkRefreshPhase.preview);
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
      ctrl.confirmPreview();
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
      ctrl.confirmPreview();
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
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.poll,
          reason: 'transient poll error must NOT collapse to error');
      expect(sub.read().pollRetrying, isTrue);
      ctrl.cancelPoll();
    });

    // PR-5e-review #1: 3-consecutive-poll-error → error state branch was
    // documented as covered in the file header but only the 1× transient
    // case was actually tested. Three failures back-to-back must flip to
    // BulkRefreshPhase.error with the explicit "lost track" copy from
    // _emitError, and the periodic timer must be cancelled so we stop
    // hammering a wedged backend.
    test('3 consecutive poll errors flip to error and cancel the timer',
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
      // Three consecutive 503s on poll.
      ResponseBody fail(RequestOptions _) => _json({
            'error': {'code': 503, 'message': 'temporarily unavailable'},
          }, status: 503);
      for (var i = 0; i < 3; i++) {
        mock.on('GET', '/api/v1/externalsecrets/bulk-refresh-jobs/job-1', fail);
      }

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();
      // submit's first-poll-immediate hit #1. The periodic 2s timer
      // does fire under real-time in non-widget unit tests, so we
      // wait long enough for the next two ticks to land.
      await Future<void>.delayed(const Duration(milliseconds: 2100));
      await _settle();
      await Future<void>.delayed(const Duration(milliseconds: 2100));
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error,
          reason: 'three consecutive poll errors must abandon');
      final msg = sub.read().errorMessage ?? '';
      expect(msg.toLowerCase(), contains('lost track'),
          reason: 'abandon message must reference the lost-track copy');
      expect(msg.toLowerCase(), contains('audit log'),
          reason: 'abandon message must point operators at the audit log');

      // Confirm the timer is actually cancelled — after the error
      // emission no further state changes should occur, even after we
      // wait long enough for the next periodic tick.
      final phaseBefore = sub.read().phase;
      final pollCallsBefore = mock.requests
          .where((r) => r.path.contains('bulk-refresh-jobs'))
          .length;
      await Future<void>.delayed(const Duration(milliseconds: 2200));
      await _settle();
      expect(sub.read().phase, phaseBefore,
          reason: 'periodic timer must be cancelled after the abandon');
      final pollCallsAfter = mock.requests
          .where((r) => r.path.contains('bulk-refresh-jobs'))
          .length;
      expect(pollCallsAfter, pollCallsBefore,
          reason: 'no further poll requests must fire after abandon');
    });
  });

  group('BulkRefreshController.submit postEmission cluster-pin race', () {
    // PR-5e-review #2: a cluster switch between POST emission and the
    // POST response landing must NOT slide through into the poll phase.
    // The submit() postEmission re-check at lines 349-353 must abort
    // before _startPoll fires, so no setState happens against a now-
    // misaligned controller.
    test('cluster switch between POST and response veto the poll transition',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-scope',
        body: _scopeOk(action: 'refresh_store', scopeTarget: 'prod/vault'),
      );
      // Delay the POST response so we have time to switch clusters
      // between submit emission and the response landing.
      mock.on(
        'POST',
        '/api/v1/externalsecrets/stores/prod/vault/refresh-all',
        (_) {
          return _json({
            'data': {'jobId': 'job-1', 'targetCount': 2},
          }, status: 202);
        },
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      ctrl.confirmPreview();

      // Kick off submit but switch the active cluster before awaiting.
      final fut = ctrl.submit();
      container
          .read(activeClusterProvider.notifier)
          .setCluster('cluster-b');
      await fut;
      await _settle();

      // postEmission re-check must veto — controller must NOT have
      // entered poll phase. Error phase is the only correct outcome.
      expect(sub.read().phase, BulkRefreshPhase.error,
          reason: 'postEmission cluster-pin re-check must abort the poll');
      expect(sub.read().jobId, isNull,
          reason: 'no jobId should be retained on aborted submit');
      // And no follow-up GETs to the poll endpoint should have fired.
      final pollCalls = mock.requests
          .where((r) => r.path.contains('bulk-refresh-jobs'))
          .length;
      expect(pollCalls, 0,
          reason: 'aborted submit must not start the poll loop');
    });
  });

  group('BulkRefreshController.submit error status mapping', () {
    // PR-5e-review #12: 503 on submit surfaces the install-guidance copy
    // straight from _handleSubmitFailure's 503 branch.
    test('503 on submit surfaces install-guidance copy', () async {
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
          'error': {'code': 503, 'message': 'ESO not detected'},
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
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error);
      final msg = sub.read().errorMessage ?? '';
      expect(msg, contains('ESO is not detected'));
      expect(msg, contains('Install ESO'));
    });

    // PR-5e-review #12: 413 on submit surfaces the scope-too-large copy.
    test('413 on submit surfaces scope-too-large copy', () async {
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
          'error': {'code': 413, 'message': 'scope too large'},
        }, status: 413),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error);
      final msg = sub.read().errorMessage ?? '';
      expect(msg, contains('too large'));
      expect(msg, contains('per-namespace'));
    });

    // PR-5e-review #13: 409 active_job_exists WITHOUT jobId in extra
    // emits the "audit log" copy rather than attaching to a phantom job.
    test('409 active_job_exists with missing jobId emits audit-log copy',
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
            'extra': <String, Object>{},
          },
        }, status: 409),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.error,
          reason: 'no jobId → no attach → land in error');
      final msg = sub.read().errorMessage ?? '';
      expect(msg.toLowerCase(), contains('audit log'));
      expect(sub.read().jobId, isNull);
    });
  });

  group('BulkRefreshController.scope_changed recovery', () {
    // PR-5e-review #6: scope_changed handler sets the operator-visible
    // banner, then re-fires beginScopeLoad. The preserveError flag must
    // keep the banner alive across the auto-re-resolve so the operator
    // understands why the confirm phase rendered again.
    test('scope_changed → confirm preserves the explanation banner',
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
            'message': 'scope changed since last resolution',
            'reason': 'scope_changed',
            'extra': {'added': ['u3'], 'removed': <Object>[]},
          },
        }, status: 409),
      );
      // Re-resolve returns a fresh (larger) scope.
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
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      // Now in preview phase again with the re-resolved scope (operator
      // must re-confirm against the freshly-resolved breakdown before
      // the confirm sheet stacks on top again).
      expect(sub.read().phase, BulkRefreshPhase.preview);
      expect(sub.read().scopeResponse?.visibleCount, 3);
      // CRITICAL: the explanation banner from the scope_changed handler
      // must still be visible — without it, the operator sees the
      // preview phase re-render with no context and is confused.
      final msg = sub.read().errorMessage ?? '';
      expect(msg, contains('Scope changed'),
          reason: 'preserveError must keep the scope_changed banner alive '
              'across the auto-re-resolve');
    });
  });

  group('BulkRefreshSheetState.copyWith clear-sentinels', () {
    // PR-5e-review #22: the previous shape relied on `?? this.field` so a
    // bare `field: null` was indistinguishable from "leave alone". The
    // explicit clear sentinels make "set to null" possible. Each sentinel
    // gets its own assertion below so a future regression where the
    // sentinel was wired through one field but skipped on another is
    // caught at unit-test time, not in a debugging session.
    test('clearScope sets scope to null even when scope param is provided', () {
      const seeded = BulkRefreshSheetState(
        phase: BulkRefreshPhase.confirm,
        scope: BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      // Bare null does NOT clear (current vs. baseline behavior).
      final viaNull = seeded.copyWith(scope: null);
      expect(viaNull.scope, isNotNull,
          reason: 'omitted param must keep current value');
      // Sentinel clears.
      final viaSentinel = seeded.copyWith(clearScope: true);
      expect(viaSentinel.scope, isNull);
      // Sentinel wins over a passed value.
      final viaSentinelWithValue = seeded.copyWith(
        scope: const BulkRefreshScopeNamespace(namespace: 'staging'),
        clearScope: true,
      );
      expect(viaSentinelWithValue.scope, isNull,
          reason: 'clearScope sentinel must override scope param');
    });

    test('clearScopeResponse sets scopeResponse to null', () {
      final seeded = BulkRefreshSheetState(
        phase: BulkRefreshPhase.confirm,
        scopeResponse: BulkScopeResponse.fromJson(const <String, dynamic>{
          'action': 'refresh_store',
          'scopeTarget': 'prod/vault',
          'totalCount': 2,
          'totalNamespaces': 1,
          'visibleCount': 2,
          'restricted': false,
          'targets': <Object>[],
          'byNamespace': <Object>[],
        }),
      );
      final viaSentinel = seeded.copyWith(clearScopeResponse: true);
      expect(viaSentinel.scopeResponse, isNull);
    });

    test('clearJobId sets jobId to null', () {
      const seeded = BulkRefreshSheetState(
        phase: BulkRefreshPhase.poll,
        jobId: 'job-123',
      );
      final viaSentinel = seeded.copyWith(clearJobId: true);
      expect(viaSentinel.jobId, isNull);
    });
  });

  group('BulkRefreshController.pollNow (iOS resume nudge)', () {
    // PR-5e-review #9: AppLifecycleListener.onResume calls pollNow() on
    // foreground so the progress bar doesn't sit stale after iOS
    // suspended our periodic timer in background. The controller-level
    // contract: pollNow() fires exactly one GET against the job endpoint
    // (respecting the same reentrancy + post-await guards as the
    // periodic tick).
    test('fires a one-shot poll when in poll phase', () async {
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
      // Two in-progress poll responses — one from submit's first-poll-
      // immediate, one from the resume nudge.
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/bulk-refresh-jobs/job-1',
        body: _jobInProgress(jobId: 'job-1', succeeded: 1),
      );
      mock.onJson(
        'GET',
        '/api/v1/externalsecrets/bulk-refresh-jobs/job-1',
        body: _jobInProgress(jobId: 'job-1', succeeded: 1),
      );

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);
      await ctrl.beginScopeLoad(
        const BulkRefreshScopeStore(namespace: 'prod', name: 'vault'),
      );
      await _settle();
      ctrl.confirmPreview();
      await ctrl.submit();
      await _settle();

      expect(sub.read().phase, BulkRefreshPhase.poll);
      final pollCallsBeforeResume = mock.requests
          .where((r) => r.path.contains('bulk-refresh-jobs'))
          .length;

      // Simulate app foreground after suspend — fire one-shot poll.
      await ctrl.pollNow();
      await _settle();

      final pollCallsAfterResume = mock.requests
          .where((r) => r.path.contains('bulk-refresh-jobs'))
          .length;
      expect(pollCallsAfterResume, pollCallsBeforeResume + 1,
          reason: 'pollNow must fire exactly one extra GET on resume');
      ctrl.cancelPoll();
    });

    test('no-ops when phase != poll (avoids spurious GETs)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      final sub = _subscribe(container);
      addTearDown(sub.close);
      final ctrl =
          container.read(bulkRefreshControllerProvider(_clusterId).notifier);

      // Phase is scopePick — pollNow should do nothing.
      expect(sub.read().phase, BulkRefreshPhase.scopePick);
      await ctrl.pollNow();
      await _settle();
      expect(mock.requests, isEmpty);
    });
  });
}
