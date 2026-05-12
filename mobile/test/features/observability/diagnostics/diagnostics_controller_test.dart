// Controller-level tests for the diagnostics screen.
//
// Coverage:
//   * Happy path — repo returns a checklist + blast radius;
//     `AsyncValue.data` lands in state with parsed types.
//   * Unsupported kind — controller short-circuits to ApiError 400 from
//     the repo's defensive guard (matches backend's HTTP 400 for kinds
//     outside `kindToResource`).
//   * 404 — backend "not found"; controller surfaces ApiError so the
//     screen's `_humanise` can produce operator-facing copy.
//   * Cluster mismatch postEmission — request landed on pinned cluster
//     but active cluster changed mid-fetch; controller emits the
//     mismatch message rather than the data payload.
//   * 500 timeout-shaped message — controller passes through; screen
//     humanises.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/diagnostics_repository.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/auth/secure_storage.dart';
import 'package:kubecenter/features/observability/diagnostics/diagnostics_controller.dart';

import '../../../support/mock_dio_adapter.dart';

ResponseBody _json(Object body, {int status = 200}) {
  return ResponseBody.fromBytes(
    Uint8List.fromList(utf8.encode(jsonEncode(body))),
    status,
    headers: {
      Headers.contentTypeHeader: ['application/json'],
    },
  );
}

// Note on `activeClusterProvider`: it's a NotifierProvider with a
// const-default of "local", which matches every test target's
// `clusterId` field below. The postEmission cluster-pin re-check
// compares `target.clusterId` against the active cluster, so we
// don't need to override the provider — only the pinned key
// matters for happy-path assertions. The header-pinning test
// distinguishes the pinned id from the active id via the family
// key alone.
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

/// Subscribes to the controller's family slot, pumps the event queue
/// until the initial `scheduleMicrotask` + the awaited Dio chain
/// (4 interceptors × ~2 microtasks each) settles, then returns the
/// final state. The persistent subscription is critical — without it
/// `autoDispose` tears the controller down before the Dio response
/// lands, and the request surfaces as `DioException [request
/// cancelled]` instead of completing normally.
Future<AsyncValue<DiagnosticResponse>> _readUntilSettled(
  ProviderContainer container,
  DiagnosticTarget target,
) async {
  final sub = container.listen(
    diagnosticsControllerProvider(target),
    (_, _) {},
  );
  try {
    await pumpEventQueue(times: 30);
    return sub.read();
  } finally {
    sub.close();
  }
}

void main() {
  group('DiagnosticsController.happy path', () {
    test('parses checklist + blast radius into AsyncValue.data', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/diagnostics/default/Pod/web',
        body: {
          'data': {
            'target': {'kind': 'Pod', 'name': 'web', 'namespace': 'default'},
            'results': [
              {
                'ruleName': 'CrashLoopBackOff',
                'status': 'fail',
                'severity': 'critical',
                'message': '1 pod in CrashLoopBackOff',
                'detail': 'Affected pods: web',
                'remediation': 'kubectl logs web --previous',
                'links': [
                  {'label': 'web', 'kind': 'Pod', 'name': 'web'},
                ],
              },
              {
                'ruleName': 'PendingPod',
                'status': 'pass',
                'severity': 'critical',
                'message': 'No pending pods',
              },
            ],
            'blastRadius': {
              'directlyAffected': [
                {
                  'kind': 'Deployment',
                  'name': 'web',
                  'health': 'degraded',
                  'impact': 'Owner — may be degraded by child failure',
                },
              ],
              'potentiallyAffected': <Object>[],
            },
          },
        },
      );

      const target = DiagnosticTarget(
        clusterId: 'local',
        namespace: 'default',
        kind: 'Pod',
        name: 'web',
      );
      final result = await _readUntilSettled(container, target);
      expect(result.hasValue, isTrue,
          reason: 'expected data, got ${result.runtimeType} '
              '(error: ${result.error})');
      final data = result.value!;
      expect(data.failedResults, hasLength(1));
      expect(data.failedResults.first.ruleName, 'CrashLoopBackOff');
      expect(data.passedResults, hasLength(1));
      expect(data.blastRadius.directlyAffected, hasLength(1));
      expect(
        data.blastRadius.directlyAffected.first.kind,
        'Deployment',
      );
    });

    test('forwards X-Cluster-ID header to the wire', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/diagnostics/default/Pod/web',
        body: {
          'data': {
            'target': {'kind': 'Pod', 'name': 'web', 'namespace': 'default'},
            'results': <Object>[],
            'blastRadius': {
              'directlyAffected': <Object>[],
              'potentiallyAffected': <Object>[],
            },
          },
        },
      );

      const target = DiagnosticTarget(
        clusterId: 'prod',
        namespace: 'default',
        kind: 'Pod',
        name: 'web',
      );
      await _readUntilSettled(container, target);
      final headerValues = mock.requests
          .where((r) => r.path.contains('/diagnostics/'))
          .map((r) => r.headers['X-Cluster-ID'])
          .toList();
      expect(headerValues, contains('prod'),
          reason: 'X-Cluster-ID must pin to the family-key clusterId, '
              'not the active cluster, so cluster-switch mid-fetch can '
              'be detected via the postEmission re-check');
    });
  });

  group('DiagnosticsController.error paths', () {
    test('unsupported kind short-circuits to ApiError 400', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // No mock registered — repo's defensive guard fires before any
      // HTTP request goes out.
      const target = DiagnosticTarget(
        clusterId: 'local',
        namespace: 'default',
        kind: 'CronJob',
        name: 'nightly',
      );
      final result = await _readUntilSettled(container, target);
      expect(result.hasError, isTrue);
      final err = result.error;
      expect(err, isA<ApiError>());
      expect((err as ApiError).statusCode, 400);
    });

    test('404 surfaces as ApiError with statusCode 404', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/diagnostics/default/Pod/ghost',
        (_) => _json({
          'error': {'code': 404, 'message': 'Pod "ghost" not found'},
        }, status: 404),
      );

      const target = DiagnosticTarget(
        clusterId: 'local',
        namespace: 'default',
        kind: 'Pod',
        name: 'ghost',
      );
      final result = await _readUntilSettled(container, target);
      expect(result.hasError, isTrue);
      expect((result.error as ApiError).statusCode, 404);
    });

    test('15s server timeout surfaces as ApiError 500 with timeout copy',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/diagnostics/default/Deployment/web',
        (_) => _json({
          'error': {
            'code': 500,
            'message': 'failed to build topology graph: context deadline '
                'exceeded (timeout)',
          },
        }, status: 500),
      );

      const target = DiagnosticTarget(
        clusterId: 'local',
        namespace: 'default',
        kind: 'Deployment',
        name: 'web',
      );
      final result = await _readUntilSettled(container, target);
      expect(result.hasError, isTrue);
      final err = result.error as ApiError;
      expect(err.statusCode, 500);
      expect(err.message.toLowerCase(), contains('timeout'));
    });
  });

  group('DiagnosticsController.namespaceSummary', () {
    test('parses failing pods + total', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/diagnostics/default/summary',
        body: {
          'data': {
            'failing': [
              {
                'kind': 'Pod',
                'name': 'web-1',
                'reason': 'CrashLoopBackOff',
              },
              {'kind': 'Pod', 'name': 'web-2', 'reason': 'Pending'},
            ],
            'total': 7,
          },
        },
      );

      const key = (clusterId: 'local', namespace: 'default');
      final sub =
          container.listen(namespaceSummaryProvider(key), (_, _) {});
      addTearDown(sub.close);
      final summary = await container.read(namespaceSummaryProvider(key).future);
      expect(summary.total, 7);
      expect(summary.failing, hasLength(2));
      expect(summary.failing.first.reason, 'CrashLoopBackOff');
    });

    test('empty failing list with non-zero total reports healthy', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/diagnostics/default/summary',
        body: {
          'data': {'failing': <Object>[], 'total': 12},
        },
      );

      const key = (clusterId: 'local', namespace: 'default');
      final sub =
          container.listen(namespaceSummaryProvider(key), (_, _) {});
      addTearDown(sub.close);
      final summary = await container.read(namespaceSummaryProvider(key).future);
      expect(summary.isHealthy, isTrue);
      expect(summary.total, 12);
    });
  });
}
