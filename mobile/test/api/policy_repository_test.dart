// PolicyRepository tests: endpoint paths, envelope unwrapping,
// X-Cluster-ID forwarding, status 5xx → unreachable, severity parsing,
// composite-id round-trip, and the 503 distinguished-error discriminator
// for compliance history.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/policy_repository.dart';
import 'package:kubecenter/auth/secure_storage.dart';

import '../support/mock_dio_adapter.dart';

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

void main() {
  group('PolicyRepository.status', () {
    test('parses detected status with both engines available', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/status', body: {
        'data': {
          'detected': 'both',
          'kyverno': {
            'available': true,
            'namespace': 'kyverno',
            'webhooks': 3,
          },
          'gatekeeper': {
            'available': true,
            'namespace': 'gatekeeper-system',
            'webhooks': 2,
          },
          'lastChecked': '2026-05-12T10:00:00Z',
        },
      });

      final s = await container.read(policyRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.kyvernoAvailable, isTrue);
      expect(s.gatekeeperAvailable, isTrue);
      expect(s.kyvernoNamespace, 'kyverno');
      expect(s.kyvernoWebhooks, 3);
      expect(s.gatekeeperWebhooks, 2);
    });

    test('detected=false when neither engine available', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/status', body: {
        'data': {'detected': ''},
      });

      final s = await container.read(policyRepositoryProvider).status();
      expect(s.detected, isFalse);
      expect(s.kyvernoAvailable, isFalse);
      expect(s.gatekeeperAvailable, isFalse);
    });

    test('503 from status returns PolicyDiscoveryStatus.unreachable', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/policies/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'discovery offline'},
        }, status: 503),
      );

      final s = await container.read(policyRepositoryProvider).status();
      expect(s.detected, isFalse);
      expect(s.serviceUnavailable, isTrue,
          reason:
              '5xx must round-trip through `unreachable` so the UI can '
              'distinguish "policy engine not installed" from "backend '
              'temporarily unreachable".');
    });

    test('forwards X-Cluster-ID when overridden', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/status', body: {
        'data': {'detected': ''},
      });

      await container
          .read(policyRepositoryProvider)
          .status(clusterIdOverride: 'remote-1');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.headers['X-Cluster-ID'], 'remote-1');
    });
  });

  group('listPolicies', () {
    test('parses severity, engine, blocking, violationCount', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/', body: {
        'data': [
          {
            'id': 'kyverno::ClusterPolicy:require-labels',
            'name': 'require-labels',
            'kind': 'ClusterPolicy',
            'action': 'enforce',
            'severity': 'high',
            'engine': 'kyverno',
            'blocking': true,
            'ready': true,
            'ruleCount': 1,
            'violationCount': 4,
          },
          {
            'id': 'gatekeeper::K8sRequiredLabels:require-team-label',
            'name': 'require-team-label',
            'kind': 'K8sRequiredLabels',
            'action': 'dryrun',
            'severity': 'medium',
            'engine': 'gatekeeper',
            'blocking': false,
            'ready': true,
            'ruleCount': 1,
            'violationCount': 0,
          },
        ],
      });

      final out =
          await container.read(policyRepositoryProvider).listPolicies();
      expect(out, hasLength(2));
      expect(out[0].engine, PolicyEngine.kyverno);
      expect(out[0].severity, 'high');
      expect(out[0].blocking, isTrue);
      expect(out[0].violationCount, 4);
      expect(out[1].engine, PolicyEngine.gatekeeper);
      expect(out[1].blocking, isFalse);
    });

    test('unknown engine string falls through to PolicyEngine.unknown',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/', body: {
        'data': [
          {
            'id': 'newengine::Whatever:foo',
            'name': 'foo',
            'kind': 'Whatever',
            'action': 'audit',
            'severity': 'low',
            'engine': 'newengine-2027',
            'blocking': false,
            'ready': true,
            'ruleCount': 1,
            'violationCount': 0,
          },
        ],
      });

      final out =
          await container.read(policyRepositoryProvider).listPolicies();
      expect(out, hasLength(1));
      expect(out[0].engine, PolicyEngine.unknown,
          reason: 'Future engine names must not crash the parser.');
    });
  });

  group('listViolations', () {
    test('stableKey derives from policy|rule|namespace|kind|name', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/violations', body: {
        'data': [
          {
            'policy': 'require-labels',
            'rule': 'check-team',
            'namespace': 'default',
            'kind': 'Pod',
            'name': 'app-abc',
            'severity': 'high',
            'action': 'enforce',
            'message': 'team label required',
            'engine': 'kyverno',
            'blocking': true,
          },
          {
            'policy': 'K8sRequiredLabels/team',
            // Gatekeeper violations have no rule sub-name
            'namespace': '',
            'kind': 'Namespace',
            'name': 'ns-1',
            'severity': 'medium',
            'action': 'dryrun',
            'message': 'namespace missing label',
            'engine': 'gatekeeper',
            'blocking': false,
          },
        ],
      });

      final out =
          await container.read(policyRepositoryProvider).listViolations();
      expect(out, hasLength(2));
      expect(
        out[0].stableKey,
        'require-labels|check-team|default|Pod|app-abc',
      );
      expect(
        out[1].stableKey,
        'K8sRequiredLabels/team|||Namespace|ns-1',
        reason: 'empty rule and namespace segments are preserved verbatim '
            'so two violations with different fields cannot collide.',
      );
    });

    test('cluster-scoped violation has empty namespace', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/violations', body: {
        'data': [
          {
            'policy': 'p',
            'kind': 'ClusterRole',
            'name': 'cr-1',
            'severity': 'low',
            'action': 'audit',
            'message': '',
            'engine': 'kyverno',
            'blocking': false,
          },
        ],
      });

      final out =
          await container.read(policyRepositoryProvider).listViolations();
      expect(out, hasLength(1));
      expect(out[0].namespace, '');
    });
  });

  group('compliance', () {
    test('parses score + bySeverity breakdown', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/compliance', body: {
        'data': {
          'scope': '',
          'score': 87.5,
          'pass': 14,
          'fail': 2,
          'warn': 0,
          'total': 16,
          'bySeverity': {
            'critical': {'pass': 2, 'fail': 0, 'total': 2},
            'high': {'pass': 5, 'fail': 1, 'total': 6},
            'medium': {'pass': 5, 'fail': 1, 'total': 6},
            'low': {'pass': 2, 'fail': 0, 'total': 2},
          },
        },
      });

      final s = await container.read(policyRepositoryProvider).compliance();
      expect(s.score, 87.5);
      expect(s.pass, 14);
      expect(s.fail, 2);
      expect(s.total, 16);
      expect(s.bySeverity, hasLength(4));
      expect(s.bySeverity['critical']!.total, 2);
      expect(s.bySeverity['high']!.fail, 1);
    });

    test('namespace scope forwards as query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/compliance', body: {
        'data': {'scope': 'app', 'score': 100, 'pass': 0, 'fail': 0,
            'warn': 0, 'total': 0},
      });

      await container
          .read(policyRepositoryProvider)
          .compliance(scopeNamespace: 'app');

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.queryParameters['namespace'], 'app');
    });
  });

  group('complianceHistory', () {
    test('parses datapoint array', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/policies/compliance/history', body: {
        'data': [
          {
            'date': '2026-05-01',
            'score': 80.0,
            'pass': 8,
            'fail': 2,
            'warn': 0,
            'total': 10,
          },
          {
            'date': '2026-05-02',
            'score': 90.0,
            'pass': 9,
            'fail': 1,
            'warn': 0,
            'total': 10,
          },
        ],
      });

      final out = await container
          .read(policyRepositoryProvider)
          .complianceHistory();
      expect(out, hasLength(2));
      expect(out[0].date, '2026-05-01');
      expect(out[1].score, 90.0);
    });

    test('days param forwards as query param', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/policies/compliance/history',
        body: {'data': <Object>[]},
      );

      await container
          .read(policyRepositoryProvider)
          .complianceHistory(days: 7);

      expect(mock.requests, hasLength(1));
      expect(mock.requests.first.queryParameters['days'], '7');
    });

    test('503 with "requires a database" surfaces via the discriminator',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/policies/compliance/history',
        (_) => _json({
          'error': {
            'code': 503,
            'message': 'compliance history requires a database',
          },
        }, status: 503),
      );

      Object? caught;
      try {
        await container
            .read(policyRepositoryProvider)
            .complianceHistory();
      } on Object catch (e) {
        caught = e;
      }
      expect(caught, isNotNull);
      expect(caught, isA<ApiError>());
      expect(
        isComplianceHistoryNotConfigured(caught!),
        isTrue,
        reason: 'The discriminator must recognise the canonical backend '
            'wording so the UI routes to the permanent empty state.',
      );
    });

    test('503 with non-database wording stays retry-able', () async {
      final apiErrorNetworkDown = ApiError(
        statusCode: 503,
        code: 503,
        message: 'backend temporarily unreachable',
      );
      expect(isComplianceHistoryNotConfigured(apiErrorNetworkDown), isFalse,
          reason: 'Generic 503 must not collapse to the permanent empty '
              'state — the operator should be able to retry.');
    });

    test('non-503 ApiErrors never trigger the discriminator', () async {
      final apiError500 = ApiError(
        statusCode: 500,
        code: 500,
        message: 'compliance history requires a database',
      );
      expect(
        isComplianceHistoryNotConfigured(apiError500),
        isFalse,
        reason: 'Only 503s with the database wording route to the empty '
            'state. A 500 with the same wording is a backend bug — '
            'operator should see a retry-able error.',
      );
    });

    test('non-ApiError errors never trigger the discriminator', () async {
      expect(
        isComplianceHistoryNotConfigured(StateError('something else')),
        isFalse,
      );
    });
  });

  // PolicyId parsing lives in mobile/lib/util/composite_id.dart; tests for
  // it live in mobile/test/util/composite_id_test.dart. Do not duplicate
  // here.
}
