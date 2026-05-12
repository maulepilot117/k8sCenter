// Tests for LokiRepository: status, query envelope parsing, volume
// aggregation, label-values plumbing, namespace param injection,
// 4096-char query gate, X-Cluster-ID threading.

import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/api/api_error.dart';
import 'package:kubecenter/api/dio_client.dart';
import 'package:kubecenter/api/loki_repository.dart';
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
  group('LokiRepository.status', () {
    test('detected: true with url + detectedVia', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/status',
        body: {
          'data': {
            'detected': true,
            'url': 'http://loki-gateway.loki.svc:3100',
            'detectedVia': 'service',
          },
        },
      );

      final s = await container.read(lokiRepositoryProvider).status();
      expect(s.detected, isTrue);
      expect(s.url, contains('loki-gateway'));
      expect(s.detectedVia, 'service');
    });

    test('503 from status endpoint returns LokiStatus.empty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/status',
        (_) => _json({
          'error': {'code': 503, 'message': 'Loki not configured'},
        }, status: 503),
      );

      final s = await container.read(lokiRepositoryProvider).status();
      expect(s.detected, isFalse);
      expect(s.url, isNull);
    });
  });

  group('LokiRepository.query', () {
    test('parses streams into flattened LogLine list', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/query',
        body: {
          'data': {
            'data': {
              'resultType': 'streams',
              'result': [
                {
                  'stream': {'namespace': 'app', 'pod': 'web-abc', 'container': 'web'},
                  'values': [
                    ['1700000000000000000', 'first line'],
                    ['1700000001000000000', 'second line'],
                  ],
                },
                {
                  'stream': {'namespace': 'app', 'pod': 'web-xyz', 'container': 'web'},
                  'values': [
                    ['1700000002000000000', 'third line'],
                  ],
                },
              ],
            },
          },
        },
      );

      final result =
          await container.read(lokiRepositoryProvider).query(
                query: '{namespace="app"}',
                start: DateTime.utc(2026, 5, 1),
                end: DateTime.utc(2026, 5, 1, 1),
                namespace: 'app',
              );

      expect(result.lines, hasLength(3));
      expect(result.streamCount, 2);
      expect(result.lines.first.line, 'first line');
      expect(result.lines.first.labels['pod'], 'web-abc');
      expect(result.isEmpty, isFalse);
      expect(result.truncated, isFalse);
    });

    test('empty result list surfaces as isEmpty', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/query',
        body: {
          'data': {
            'data': {'resultType': 'streams', 'result': <Object>[]},
          },
        },
      );

      final result =
          await container.read(lokiRepositoryProvider).query(
                query: '{namespace="ghosts"}',
                start: DateTime.utc(2026, 5, 1),
                end: DateTime.utc(2026, 5, 1, 1),
                namespace: 'ghosts',
              );
      expect(result.isEmpty, isTrue);
      expect(result.streamCount, 0);
    });

    test('5000-line result flips truncated', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      // Cheaper to fabricate a synthetic 5000-line stream than to
      // emit one Map per line. The parse path is value-iteration so
      // a single stream with 5000 values exercises the cap branch.
      final values = List.generate(
        5000,
        (i) => ['$i', 'line $i'],
      );
      mock.onJson(
        'GET',
        '/api/v1/logs/query',
        body: {
          'data': {
            'data': {
              'resultType': 'streams',
              'result': [
                {
                  'stream': {'namespace': 'busy'},
                  'values': values,
                },
              ],
            },
          },
        },
      );

      final result =
          await container.read(lokiRepositoryProvider).query(
                query: '{namespace="busy"}',
                start: DateTime.utc(2026, 5, 1),
                end: DateTime.utc(2026, 5, 1, 1),
                namespace: 'busy',
              );
      expect(result.lines, hasLength(5000));
      expect(result.truncated, isTrue);
    });

    test('4096-char gate throws ArgumentError before request', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      final huge = '{${'x' * 5000}="y"}';
      expect(
        () => container.read(lokiRepositoryProvider).query(
              query: huge,
              start: DateTime.utc(2026, 5, 1),
              end: DateTime.utc(2026, 5, 1, 1),
              namespace: 'app',
            ),
        throwsA(isA<ArgumentError>()),
      );
      // No request should have hit the wire.
      expect(mock.requests, isEmpty);
    });

    test('403 from backend (missing namespace, non-admin) surfaces as ApiError',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/query',
        (_) => _json({
          'error': {
            'code': 403,
            'message': 'namespace access denied',
            'detail': 'namespace parameter required for non-admin users',
          },
        }, status: 403),
      );

      expect(
        () => container.read(lokiRepositoryProvider).query(
              query: '{job="kubelet"}',
              start: DateTime.utc(2026, 5, 1),
              end: DateTime.utc(2026, 5, 1, 1),
            ),
        throwsA(isA<ApiError>()
            .having((e) => e.statusCode, 'statusCode', 403)),
      );
    });

    test('400 from backend (invalid LogQL) surfaces ApiError verbatim',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/query',
        (_) => _json({
          'error': {
            'code': 400,
            'message': 'parse error: unexpected token',
          },
        }, status: 400),
      );

      try {
        await container.read(lokiRepositoryProvider).query(
              query: '{ this is not logql }',
              start: DateTime.utc(2026, 5, 1),
              end: DateTime.utc(2026, 5, 1, 1),
              namespace: 'app',
            );
        fail('expected ApiError');
      } on ApiError catch (e) {
        expect(e.statusCode, 400);
        expect(e.message, contains('parse error'));
      }
    });

    test('namespace param is injected when provided', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/logs/query', body: {
        'data': {
          'data': {'resultType': 'streams', 'result': <Object>[]},
        },
      });

      await container.read(lokiRepositoryProvider).query(
            query: '{namespace="app"}',
            start: DateTime.utc(2026, 5, 1),
            end: DateTime.utc(2026, 5, 1, 1),
            namespace: 'app',
          );

      final req = mock.requests.single;
      expect(req.queryParameters['namespace'], 'app');
      expect(req.queryParameters['query'], '{namespace="app"}');
      expect(req.queryParameters['direction'], 'backward');
    });

    test('clusterIdOverride forwards as explicit X-Cluster-ID header',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson('GET', '/api/v1/logs/query', body: {
        'data': {
          'data': {'resultType': 'streams', 'result': <Object>[]},
        },
      });

      await container.read(lokiRepositoryProvider).query(
            query: '{namespace="app"}',
            start: DateTime.utc(2026, 5, 1),
            end: DateTime.utc(2026, 5, 1, 1),
            namespace: 'app',
            clusterIdOverride: 'cluster-b',
          );

      expect(mock.requests.single.headers['X-Cluster-ID'], 'cluster-b');
    });
  });

  group('LokiRepository.volume', () {
    test('aggregates counts across entries into sorted buckets', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/volume',
        body: {
          'data': {
            'data': {
              'resultType': 'matrix',
              'result': [
                {
                  'metric': {'namespace': 'app'},
                  'values': [
                    [1700000000.0, '12'],
                    [1700000060.0, '7'],
                  ],
                },
                {
                  'metric': {'namespace': 'kube-system'},
                  'values': [
                    [1700000000.0, '4'],
                  ],
                },
              ],
            },
          },
        },
      );

      final result = await container.read(lokiRepositoryProvider).volume(
            query: '{namespace=~"app|kube-system"}',
            start: DateTime.utc(2026, 5, 1),
            end: DateTime.utc(2026, 5, 1, 1),
            step: '1m',
            namespace: 'app',
          );

      expect(result.buckets, hasLength(2));
      // First bucket = 12 + 4 (timestamps coalesce across entries).
      expect(result.buckets.first.count, 16);
      expect(result.buckets[1].count, 7);
      // Timestamps come out sorted ascending.
      expect(
        result.buckets.first.timestamp
            .isBefore(result.buckets[1].timestamp),
        isTrue,
      );
      expect(result.total, 23);
    });

    test('4096-char gate also covers volume requests', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      expect(
        () => container.read(lokiRepositoryProvider).volume(
              query: '{${'x' * 5000}="y"}',
              start: DateTime.utc(2026, 5, 1),
              end: DateTime.utc(2026, 5, 1, 1),
              step: '1m',
              namespace: 'app',
            ),
        throwsA(isA<ArgumentError>()),
      );
      expect(mock.requests, isEmpty);
    });
  });

  group('LokiRepository.labelValues', () {
    test('returns label values list from /labels/{name}/values', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/labels/namespace/values',
        body: {
          'data': ['app', 'kube-system', 'monitoring'],
        },
      );

      final values =
          await container.read(lokiRepositoryProvider).labelValues(
                name: 'namespace',
              );
      expect(values, ['app', 'kube-system', 'monitoring']);
    });

    test('threads namespace + scopeQuery as request params', () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.onJson(
        'GET',
        '/api/v1/logs/labels/pod/values',
        body: {'data': <String>[]},
      );

      await container.read(lokiRepositoryProvider).labelValues(
            name: 'pod',
            namespace: 'app',
            scopeQuery: '{namespace="app"}',
          );

      final req = mock.requests.single;
      expect(req.queryParameters['namespace'], 'app');
      expect(req.queryParameters['query'], '{namespace="app"}');
    });

    test('403 on label values degrades to empty list (graceful UX)',
        () async {
      final (:container, :mock) = _make();
      addTearDown(container.dispose);

      mock.on(
        'GET',
        '/api/v1/logs/labels/pod/values',
        (_) => _json({
          'error': {'code': 403, 'message': 'namespace access denied'},
        }, status: 403),
      );

      final values = await container.read(lokiRepositoryProvider).labelValues(
            name: 'pod',
          );
      // Empty rather than throwing — the dropdown stays operational
      // and the operator can type a value manually.
      expect(values, isEmpty);
    });
  });
}
