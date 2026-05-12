// Tests for MonitoringRepository: step formula pinned to plan values,
// status endpoint parsing, query_range envelope decoding, 503 fallthrough.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/monitoring_repository.dart';
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
  group('MonitoringRepository.computeStep', () {
    // The five plan-pinned values. Drift here means web/mobile
    // Prometheus cache slots stop aligning — at minimum the same
    // (start, end) tuple should produce a deterministic step on
    // mobile so chart frames look the same across launches.
    test('60s range → 15s step', () {
      expect(MonitoringRepository.computeStep(60), 15);
    });

    test('1h range → 15s step', () {
      expect(MonitoringRepository.computeStep(3600), 15);
    });

    test('6h range → 30s step', () {
      expect(MonitoringRepository.computeStep(21600), 30);
    });

    test('24h range → 5m step (300s)', () {
      expect(MonitoringRepository.computeStep(86400), 300);
    });

    test('7d range → 30m step (1800s)', () {
      expect(MonitoringRepository.computeStep(604800), 1800);
    });

    test('intermediate ranges snap UP to next preset', () {
      // 22 raw → 30 (next preset above)
      expect(MonitoringRepository.computeStep(22000), 30);
      // 87 raw → 300 (next preset above 60)
      expect(MonitoringRepository.computeStep(86001), 300);
    });

    test('ranges past 1h preset clamp to 3600 (largest preset)', () {
      // 10x 7d range, would compute step 6048 — clamps to 3600.
      expect(MonitoringRepository.computeStep(604800 * 10), 3600);
    });

    test('zero or negative range falls back to 15s', () {
      expect(MonitoringRepository.computeStep(0), 15);
      expect(MonitoringRepository.computeStep(-5), 15);
    });
  });

  group('MonitoringRepository.status', () {
    test('detected: true with both engines available', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'detected': true,
            'prometheus': {'available': true},
            'grafana': {'available': true},
          },
        },
      );

      final s = await container
          .read(monitoringRepositoryProvider)
          .status();
      expect(s.detected, isTrue);
      expect(s.prometheusAvailable, isTrue);
      expect(s.grafanaAvailable, isTrue);
    });

    test('503 from status endpoint returns MonitoringStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/monitoring/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'monitoring not configured'},
        }, status: 503),
      );

      final s = await container
          .read(monitoringRepositoryProvider)
          .status();
      expect(s.detected, isFalse);
      expect(s.prometheusAvailable, isFalse);
    });

    test('missing detected field falls back to OR of engine flags', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // Some discoverer paths omit `detected` and just expose the per-
      // engine availability. The factory falls back to OR rather than
      // surfacing a confusing "detected: false but Prom: true" state.
      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'prometheus': {'available': true},
            'grafana': {'available': false},
          },
        },
      );

      final s = await container
          .read(monitoringRepositoryProvider)
          .status();
      expect(s.detected, isTrue);
      expect(s.prometheusAvailable, isTrue);
      expect(s.grafanaAvailable, isFalse);
    });

    test('prometheus: null in status response → prometheusAvailable: false',
        () async {
      // A discoverer that explicitly emits `prometheus: null` (vs.
      // absent) must not coerce to "available". The `prom is Map`
      // check defends this; locking it down with a test prevents a
      // future refactor from regressing the null case.
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/monitoring/status',
        body: {
          'data': {
            'detected': true,
            'prometheus': null,
            'grafana': {'available': false},
          },
        },
      );

      final s = await container
          .read(monitoringRepositoryProvider)
          .status();
      expect(s.detected, isTrue);
      expect(s.prometheusAvailable, isFalse);
    });
  });

  group('MonitoringRepository.queryRange', () {
    test('parses matrix result into typed series + points', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/monitoring/query_range',
        body: {
          'data': {
            'resultType': 'matrix',
            'result': [
              {
                'metric': {'container': 'web'},
                'values': [
                  [1700000000.0, '0.5'],
                  [1700000060.0, '0.6'],
                ],
              },
              {
                'metric': {'container': 'sidecar'},
                'values': [
                  [1700000000.0, '0.1'],
                ],
              },
            ],
            'warnings': ['high cardinality result'],
          },
        },
      );

      final result =
          await container.read(monitoringRepositoryProvider).queryRange(
                query: 'up',
                start: DateTime.utc(2026, 5, 1),
                end: DateTime.utc(2026, 5, 1, 1),
                stepSeconds: 15,
              );

      expect(result.series, hasLength(2));
      expect(result.series.first.labels['container'], 'web');
      expect(result.series.first.points, hasLength(2));
      expect(result.series.first.points.first.v, 0.5);
      expect(result.warnings, contains('high cardinality result'));
      expect(result.isEmpty, isFalse);
    });

    test('empty result list surfaces as isEmpty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/monitoring/query_range',
        body: {
          'data': {
            'resultType': 'matrix',
            'result': <Object>[],
          },
        },
      );

      final result =
          await container.read(monitoringRepositoryProvider).queryRange(
                query: 'up',
                start: DateTime.utc(2026, 5, 1),
                end: DateTime.utc(2026, 5, 1, 1),
                stepSeconds: 15,
              );

      expect(result.isEmpty, isTrue);
      expect(result.series, isEmpty);
    });

    test('502 from Prometheus surfaces as ApiError(502)', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/monitoring/query_range',
        (_) => _json({
          'error': {'code': 502, 'message': 'Prometheus query failed'},
        }, status: 502),
      );

      expect(
        () => container.read(monitoringRepositoryProvider).queryRange(
              query: 'up',
              start: DateTime.utc(2026, 5, 1),
              end: DateTime.utc(2026, 5, 1, 1),
              stepSeconds: 15,
            ),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 502)),
      );
    });

    test('clusterIdOverride forwards as explicit X-Cluster-ID header',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/monitoring/query_range',
        body: {
          'data': {'resultType': 'matrix', 'result': <Object>[]},
        },
      );

      await container.read(monitoringRepositoryProvider).queryRange(
            query: 'up',
            start: DateTime.utc(2026, 5, 1),
            end: DateTime.utc(2026, 5, 1, 1),
            stepSeconds: 15,
            clusterIdOverride: 'cluster-b',
          );

      expect(mock.requests.single.headers['X-Cluster-ID'], 'cluster-b');
    });
  });
}
